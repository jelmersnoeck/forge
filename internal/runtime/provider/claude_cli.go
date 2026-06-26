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

// ClaudeCLIProvider implements types.LLMProvider by communicating with a
// persistent `claude -p` process over stdin/stdout using NDJSON
// (--input-format stream-json / --output-format stream-json).
//
// The process is spawned lazily on the first Chat() call and reused for
// subsequent calls, eliminating the ~3-4s startup overhead per turn.
//
//	┌─────────┐   stdin (NDJSON messages)   ┌──────────────┐
//	│  forge   │ ────────────────────────▶   │  claude -p   │
//	│ (client) │ ◀── stdout (NDJSON) ─────  │  (persistent)│
//	└─────────┘                             └──────────────┘
//
// The CLI handles its own tool execution internally. Forge sees only text
// output and treats every turn as end_turn (no tool_use forwarded).
//
// When a Chat() call requests a different model than the persistent process
// was started with, a one-shot subprocess is spawned instead.
type ClaudeCLIProvider struct {
	mu sync.Mutex // protects all fields below

	// Persistent process state.
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   *bufio.Scanner
	stdoutRC io.ReadCloser // raw stdout pipe — closed on cancel to unblock Scan()
	stderrRC io.ReadCloser // raw stderr pipe — closed on cancel to unblock drain
	alive    bool          // true when process is running

	// Session tracking.
	claudeSessionID string

	// Model the persistent process was started with.
	model string
}

// NewClaudeCLI creates a new Claude CLI provider. Requires `claude` on PATH.
// The persistent process is spawned lazily on the first Chat() call.
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

// ── Input types for --input-format stream-json ──

// cliInputMessage is the JSON envelope sent to stdin.
type cliInputMessage struct {
	Type    string          `json:"type"`    // "user"
	Message cliInputPayload `json:"message"`
}

type cliInputPayload struct {
	Role    string `json:"role"`    // "user"
	Content string `json:"content"`
}

// ── Main API ──

// Chat sends a message to the persistent CLI process and returns a channel of
// ChatDelta events. The process is spawned lazily on the first call.
//
// If the request's model differs from the persistent process's model, a
// one-shot subprocess is used instead to avoid model mismatch.
func (p *ClaudeCLIProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	p.mu.Lock()

	// If the persistent process is running with a different model than
	// requested, use a one-shot subprocess. This happens for lightweight
	// calls (classification, session naming) which request haiku while
	// the main loop uses opus.
	if p.alive && p.model != "" && req.Model != "" && req.Model != p.model {
		p.mu.Unlock()
		return p.chatOneShot(ctx, req)
	}

	// Spawn persistent process if needed (or re-spawn if it died).
	if err := p.ensureProcess(req.Model); err != nil {
		p.mu.Unlock()
		return nil, err
	}

	// Build the prompt with system prompt injection.
	prompt := buildPromptWithSystem(req)

	// Send the message to stdin.
	inputMsg := cliInputMessage{
		Type: "user",
		Message: cliInputPayload{
			Role:    "user",
			Content: prompt,
		},
	}

	msgBytes, err := json.Marshal(inputMsg)
	if err != nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("marshal input message: %w", err)
	}

	if _, err := p.stdin.Write(append(msgBytes, '\n')); err != nil {
		// Process likely died — clean up and let next call respawn.
		_ = p.killProcess()
		p.mu.Unlock()
		return nil, fmt.Errorf("write to claude stdin: %w", err)
	}

	ch := make(chan types.ChatDelta, 16)

	// Read stdout in a goroutine. The mutex is held for the entire read
	// duration to serialize access to the persistent process.
	go func() {
		defer close(ch)
		defer p.mu.Unlock()

		p.readTurn(ctx, ch)
	}()

	return ch, nil
}

// Close shuts down the persistent CLI process. Safe to call multiple times.
func (p *ClaudeCLIProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killProcess()
}

// ── Process lifecycle (must be called with p.mu held) ──

// ensureProcess starts the persistent CLI process if not already running.
func (p *ClaudeCLIProvider) ensureProcess(model string) error {
	if p.alive {
		return nil
	}

	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("claude CLI not found on PATH: %w", err)
	}

	// Determine model: prefer previously locked-in model, then caller's.
	effectiveModel := p.model
	if effectiveModel == "" {
		effectiveModel = model
	}

	args := []string{
		"-p",
		"--verbose",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--include-partial-messages",
		"--dangerously-skip-permissions",
	}

	if effectiveModel != "" {
		args = append(args, "--model", effectiveModel)
		p.model = effectiveModel
	}

	// Use exec.Command (no context) — the process should survive individual
	// turn cancellations. We manage its lifetime via Close()/killProcess().
	cmd := exec.Command("claude", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start claude CLI: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	p.cmd = cmd
	p.stdin = stdin
	p.stdout = scanner
	p.stdoutRC = stdout
	p.stderrRC = stderr
	p.alive = true

	// Drain stderr in background to prevent pipe buffer from filling up.
	go func() {
		_, _ = io.Copy(io.Discard, stderr)
	}()

	return nil
}

// killProcess terminates the CLI process. Must be called with p.mu held.
func (p *ClaudeCLIProvider) killProcess() error {
	if !p.alive {
		return nil
	}

	p.alive = false

	// Close stdin — signals the process to exit gracefully.
	if p.stdin != nil {
		_ = p.stdin.Close()
	}

	// Close stdout/stderr pipes to unblock any concurrent readers.
	if p.stdoutRC != nil {
		_ = p.stdoutRC.Close()
	}
	if p.stderrRC != nil {
		_ = p.stderrRC.Close()
	}

	// Give it a moment to exit, then force kill.
	done := make(chan error, 1)
	go func() {
		done <- p.cmd.Wait()
	}()

	select {
	case <-done:
		// Exited cleanly.
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}

	p.cmd = nil
	p.stdin = nil
	p.stdout = nil
	p.stdoutRC = nil
	p.stderrRC = nil

	return nil
}

// ── Turn reading (must be called with p.mu held) ──

// readTurn reads NDJSON lines from stdout until a result line, emitting
// ChatDelta events. On context cancellation, closes the pipes to unblock
// Scan() and kills the process (it will be respawned on the next Chat()
// call). A watcher goroutine handles cancellation since Scan() blocks on
// the pipe and can't be interrupted by a select on ctx.Done().
//
// Must be called with p.mu held.
func (p *ClaudeCLIProvider) readTurn(ctx context.Context, ch chan<- types.ChatDelta) {
	// Capture references for the watcher goroutine. This is safe because
	// we hold p.mu and these are stable for the duration of this call.
	cmd := p.cmd
	stdoutRC := p.stdoutRC
	stderrRC := p.stderrRC

	// Watch for context cancellation. Close pipes to unblock Scan() and
	// the stderr drain goroutine, then kill the process.
	readDone := make(chan struct{})
	defer close(readDone)

	go func() {
		select {
		case <-ctx.Done():
			// Close pipes first — this immediately unblocks Scan() and
			// the stderr drain goroutine regardless of OS signal delivery.
			if stdoutRC != nil {
				_ = stdoutRC.Close()
			}
			if stderrRC != nil {
				_ = stderrRC.Close()
			}
			// Then kill the process to clean up.
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		case <-readDone:
		}
	}()

	var resultMsg *cliMessage

readLoop:
	for p.stdout.Scan() {
		line := p.stdout.Text()
		if line == "" {
			continue
		}

		var msg cliMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
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
			resultMsg = &msg
			break readLoop
		}
	}

	// If context was cancelled, the watcher closed pipes and killed the process.
	// Clean up state so the next Chat() call respawns.
	if ctx.Err() != nil {
		_ = cmd.Wait() // reap zombie
		p.alive = false
		p.cmd = nil
		p.stdin = nil
		p.stdout = nil
		p.stdoutRC = nil
		p.stderrRC = nil
		return
	}

	// Scanner returned false — check for error.
	if err := p.stdout.Err(); err != nil {
		_ = p.killProcess()
		ch <- types.ChatDelta{
			Type: "error",
			Text: fmt.Sprintf("read claude stdout: %s", err),
		}
		return
	}

	// No result and no error — process died.
	if resultMsg == nil {
		_ = p.killProcess()
		ch <- types.ChatDelta{
			Type: "error",
			Text: "claude CLI process terminated unexpectedly",
		}
		return
	}

	if resultMsg.IsError {
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
}

// ── One-shot fallback for model mismatches ──

// chatOneShot spawns a separate claude process for a single call.
// Used when the requested model differs from the persistent process's model
// (e.g., haiku for classification while the persistent process runs opus).
func (p *ClaudeCLIProvider) chatOneShot(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return nil, fmt.Errorf("claude CLI not found on PATH: %w", err)
	}

	prompt := extractPrompt(req)
	args := buildOneShotArgs(req, prompt)

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude CLI: %w", err)
	}

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

		var resultMsg *cliMessage
	readLoop:
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
				continue
			}

			switch msg.Type {
			case "system":
				// Init line — skip.
			case "stream_event":
				p.handleStreamEvent(msg.Event, ch)
			case "assistant":
				p.handleAssistant(msg.Message, ch)
			case "result":
				resultMsg = &msg
				break readLoop
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

		<-stderrDone
		_ = cmd.Wait()

		if resultMsg != nil {
			if resultMsg.IsError {
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

		if stderr := strings.TrimSpace(stderrBuf.String()); stderr != "" {
			ch <- types.ChatDelta{
				Type: "error",
				Text: fmt.Sprintf("claude CLI error: %s", stderr),
			}
			return
		}

		ch <- types.ChatDelta{
			Type:       "message_stop",
			StopReason: "end_turn",
		}
	}()

	return ch, nil
}

// buildOneShotArgs constructs CLI arguments for a one-shot (non-persistent) call.
func buildOneShotArgs(req types.ChatRequest, prompt string) []string {
	args := []string{
		"-p",
		"--verbose",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--dangerously-skip-permissions",
	}

	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	if len(req.System) > 0 {
		var systemParts []string
		for _, block := range req.System {
			systemParts = append(systemParts, block.Text)
		}
		systemPrompt := strings.Join(systemParts, "\n\n")
		args = append(args, "--system-prompt", systemPrompt)
	}

	args = append(args, prompt)
	return args
}

// ── Stream handling (stateless helpers) ──

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
// Must be called with p.mu held.
func (p *ClaudeCLIProvider) captureSessionID(sessionID string) {
	if sessionID == "" {
		return
	}
	if p.claudeSessionID == "" {
		p.claudeSessionID = sessionID
	}
}

// ── Prompt construction ──

// buildPromptWithSystem builds the user prompt and prepends the system prompt
// if present. For the persistent process, --system-prompt is a startup flag
// so we inject the system instructions into the user message content.
func buildPromptWithSystem(req types.ChatRequest) string {
	prompt := extractPrompt(req)

	if len(req.System) > 0 {
		var systemParts []string
		for _, block := range req.System {
			systemParts = append(systemParts, block.Text)
		}
		systemPrompt := strings.Join(systemParts, "\n\n")
		prompt = "[System Instructions]\n" + systemPrompt + "\n\n[User Message]\n" + prompt
	}

	return prompt
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
