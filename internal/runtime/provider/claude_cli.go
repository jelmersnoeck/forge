package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

// ClaudeCLIProvider implements types.LLMProvider by spawning `claude -p` and
// parsing its NDJSON (stream-json) output. Uses the user's Claude.ai
// subscription instead of an API key.
//
//	┌─────────┐   stdin (prompt)         ┌──────────────┐
//	│  forge   │ ──────────────────────▶  │  claude -p   │
//	│ (client) │ ◀── stdout (NDJSON) ──── │  (CLI agent) │
//	└─────────┘                           └──────────────┘
//
// The CLI handles its own tool execution internally. Forge sees only text
// output and treats every turn as end_turn (no tool_use forwarded).
type ClaudeCLIProvider struct {
	claudeSessionID string     // CLI session ID for --resume across calls
	mu              sync.Mutex // protects claudeSessionID
}

// NewClaudeCLI creates a new Claude CLI provider. Requires `claude` on PATH.
func NewClaudeCLI() *ClaudeCLIProvider {
	return &ClaudeCLIProvider{}
}

// ── NDJSON types from `claude -p --output-format stream-json` ──

// cliMessage is the top-level NDJSON envelope.
type cliMessage struct {
	Type      string          `json:"type"`       // "system", "assistant", "result", "stream_event"
	SessionID string          `json:"session_id"` // present on most messages
	Message   json.RawMessage `json:"message,omitempty"`
	Event     json.RawMessage `json:"event,omitempty"`

	// result fields
	IsError    bool    `json:"is_error,omitempty"`
	DurationMS int     `json:"duration_ms,omitempty"`
	CostUSD    float64 `json:"cost_usd,omitempty"`
}

// cliAssistantMessage is the "message" field inside type:"assistant".
type cliAssistantMessage struct {
	Role    string            `json:"role"`
	Content []cliContentBlock `json:"content"`
	Usage   *cliUsage         `json:"usage,omitempty"`
}

type cliContentBlock struct {
	Type  string `json:"type"` // "text", "tool_use", "tool_result"
	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
}

type cliUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// cliStreamEvent is the "event" field inside type:"stream_event".
type cliStreamEvent struct {
	Type  string         `json:"type"` // "content_block_delta", "message_start", etc.
	Index int            `json:"index,omitempty"`
	Delta *cliEventDelta `json:"delta,omitempty"`
}

type cliEventDelta struct {
	Type        string `json:"type"` // "text_delta", "input_json_delta"
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

// Chat spawns `claude -p` with stream-json output and returns a channel of
// ChatDelta events. The prompt is built from the ChatRequest's messages.
func (p *ClaudeCLIProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return nil, fmt.Errorf("claude CLI not found on PATH: %w", err)
	}

	prompt := extractPrompt(req)
	args := p.buildArgs(req, prompt)

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second // prevent pipe deadlock from orphan children

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	// Use StderrPipe so we control when stderr is read, avoiding
	// a data race between exec.Cmd's internal copy goroutine and ours.
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude CLI: %w", err)
	}

	// Drain stderr in the background. Must finish before cmd.Wait() returns.
	var stderrBuf bytes.Buffer
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(&stderrBuf, stderrPipe)
	}()

	ch := make(chan types.ChatDelta, 16)

	go func() {
		defer close(ch)

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		gotResult := false
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				<-stderrDone
				_ = cmd.Wait()
				return
			default:
			}

			line := scanner.Text()
			if line == "" {
				continue
			}

			var msg cliMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue // skip malformed lines
			}

			p.captureSessionID(msg.SessionID)

			switch msg.Type {
			case "system":
				// Session init — session_id already captured above.

			case "stream_event":
				p.handleStreamEvent(msg.Event, ch)

			case "assistant":
				p.handleAssistant(msg.Message, ch)

			case "result":
				gotResult = true
				<-stderrDone
				_ = cmd.Wait()

				if msg.IsError {
					ch <- types.ChatDelta{
						Type: "error",
						Text: "Claude CLI returned an error",
					}
					return
				}
				ch <- types.ChatDelta{
					Type:       "message_stop",
					StopReason: "end_turn",
				}
				return
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- types.ChatDelta{
				Type: "error",
				Text: fmt.Sprintf("read claude stdout: %s", err),
			}
			<-stderrDone
			_ = cmd.Wait()
			return
		}

		// Wait for stderr drain + process exit before reading the buffer.
		<-stderrDone
		_ = cmd.Wait()

		if gotResult {
			return
		}

		// No result message — check stderr for errors.
		if stderr := strings.TrimSpace(stderrBuf.String()); stderr != "" {
			ch <- types.ChatDelta{
				Type: "error",
				Text: fmt.Sprintf("claude CLI error: %s", stderr),
			}
			return
		}

		// Stream ended without explicit result — emit stop.
		ch <- types.ChatDelta{
			Type:       "message_stop",
			StopReason: "end_turn",
		}
	}()

	return ch, nil
}

// buildArgs constructs the claude CLI arguments.
func (p *ClaudeCLIProvider) buildArgs(req types.ChatRequest, prompt string) []string {
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--dangerously-skip-permissions",
	}

	model := req.Model
	if model != "" {
		args = append(args, "--model", model)
	}

	// Pass system prompt if present.
	if len(req.System) > 0 {
		var systemParts []string
		for _, block := range req.System {
			systemParts = append(systemParts, block.Text)
		}
		systemPrompt := strings.Join(systemParts, "\n\n")
		args = append(args, "--system-prompt", systemPrompt)
	}

	// Resume existing CLI session if we have one.
	p.mu.Lock()
	sessionID := p.claudeSessionID
	p.mu.Unlock()

	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	args = append(args, prompt)
	return args
}

// handleStreamEvent processes a stream_event NDJSON line and emits ChatDelta.
func (p *ClaudeCLIProvider) handleStreamEvent(eventJSON json.RawMessage, ch chan<- types.ChatDelta) {
	if eventJSON == nil {
		return
	}

	var evt cliStreamEvent
	if err := json.Unmarshal(eventJSON, &evt); err != nil {
		return
	}

	switch evt.Type {
	case "content_block_delta":
		if evt.Delta == nil {
			return
		}
		switch evt.Delta.Type {
		case "text_delta":
			if evt.Delta.Text != "" {
				ch <- types.ChatDelta{
					Type: "text_delta",
					Text: evt.Delta.Text,
				}
			}
		}
	}
}

// handleAssistant processes a complete assistant message and emits usage/text.
func (p *ClaudeCLIProvider) handleAssistant(msgJSON json.RawMessage, ch chan<- types.ChatDelta) {
	if msgJSON == nil {
		return
	}

	var msg cliAssistantMessage
	if err := json.Unmarshal(msgJSON, &msg); err != nil {
		return
	}

	// Emit usage if available.
	if msg.Usage != nil {
		ch <- types.ChatDelta{
			Type: "usage",
			Usage: &types.TokenUsage{
				InputTokens:         msg.Usage.InputTokens,
				OutputTokens:        msg.Usage.OutputTokens,
				CacheCreationTokens: msg.Usage.CacheCreationInputTokens,
				CacheReadTokens:     msg.Usage.CacheReadInputTokens,
			},
		}
	}

	// Note: we don't emit text from assistant messages here because
	// the stream_events already delivered the text incrementally.
	// The assistant message is just the complete record.
}

// captureSessionID stores the CLI session ID from the first message that has one.
func (p *ClaudeCLIProvider) captureSessionID(sessionID string) {
	if sessionID == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.claudeSessionID == "" {
		p.claudeSessionID = sessionID
	}
}

// extractPrompt builds the user prompt from the ChatRequest messages.
// Takes the last user message's text content.
func extractPrompt(req types.ChatRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role != "user" {
			continue
		}
		var parts []string
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				parts = append(parts, block.Text)
			case "tool_result":
				// Include tool results as context.
				for _, c := range block.Content {
					if c.Type == "text" {
						parts = append(parts, c.Text)
					}
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return ""
}
