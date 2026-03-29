// Package loop implements the agentic conversation loop.
package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jelmersnoeck/forge/internal/runtime/prompt"
	"github.com/jelmersnoeck/forge/internal/runtime/session"
	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
)

// Loop drives the agentic conversation with the LLM.
type Loop struct {
	provider     types.LLMProvider
	tools        *tools.Registry
	context      types.ContextBundle
	cwd          string
	sessionStore *session.Store
	sessionID    string
	model        string
	maxTurns     int
	historyID    string
	history      []types.ChatMessage
	audit        types.AuditLogger
}

// Options configures the conversation loop.
type Options struct {
	Provider     types.LLMProvider
	Tools        *tools.Registry
	Context      types.ContextBundle
	CWD          string
	SessionStore *session.Store
	SessionID    string
	Model        string
	MaxTurns     int
	AuditLogger  types.AuditLogger
}

// New creates a new conversation loop.
func New(opts Options) *Loop {
	audit := opts.AuditLogger
	if audit == nil {
		audit = nopAuditLogger{}
	}
	return &Loop{
		provider:     opts.Provider,
		tools:        opts.Tools,
		context:      opts.Context,
		cwd:          opts.CWD,
		sessionStore: opts.SessionStore,
		sessionID:    opts.SessionID,
		model:        opts.Model,
		maxTurns:     opts.MaxTurns,
		historyID:    uuid.New().String(),
		history:      []types.ChatMessage{},
		audit:        audit,
	}
}

// HistoryID returns the current history ID (used for JSONL session persistence).
func (l *Loop) HistoryID() string {
	return l.historyID
}

// Send processes a user prompt and runs the agentic loop.
func (l *Loop) Send(ctx context.Context, promptText string, emit func(types.OutboundEvent)) error {
	// Append user message to history
	userMsg := types.ChatMessage{
		Role: "user",
		Content: []types.ChatContentBlock{
			{Type: "text", Text: promptText},
		},
	}
	l.history = append(l.history, userMsg)

	// Persist user message
	if err := l.persistMessage("user", userMsg); err != nil {
		return fmt.Errorf("persist user message: %w", err)
	}

	// Run the agentic loop
	return l.runLoop(ctx, emit)
}

// Resume loads a session and continues with a new prompt.
func (l *Loop) Resume(ctx context.Context, historyID string, promptText string, emit func(types.OutboundEvent)) error {
	// Load session history
	sessionMessages, err := l.sessionStore.Load(historyID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	// Reconstruct history from session messages
	for _, msg := range sessionMessages {
		// Parse message content back into ChatMessage
		messageBytes, err := json.Marshal(msg.Message)
		if err != nil {
			continue
		}

		var chatMsg types.ChatMessage
		if err := json.Unmarshal(messageBytes, &chatMsg); err != nil {
			continue
		}

		l.history = append(l.history, chatMsg)
	}

	l.historyID = historyID

	// Now send the new prompt
	return l.Send(ctx, promptText, emit)
}

func (l *Loop) runLoop(ctx context.Context, emit func(types.OutboundEvent)) error {
	turnCount := 0

	for {
		turnCount++

		// Check max turns
		if l.maxTurns > 0 && turnCount > l.maxTurns {
			emit(types.OutboundEvent{
				ID:        uuid.New().String(),
				SessionID: l.sessionID,
				Type:      "error",
				Content:   fmt.Sprintf("Max turns reached (%d)", l.maxTurns),
				Timestamp: time.Now().Unix(),
			})
			emit(types.OutboundEvent{
				ID:        uuid.New().String(),
				SessionID: l.sessionID,
				Type:      "done",
				Timestamp: time.Now().Unix(),
			})
			return nil
		}

		// Emit thinking event
		emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			SessionID: l.sessionID,
			Type:      "thinking",
			Timestamp: time.Now().Unix(),
		})

		// Assemble chat request
		req := types.ChatRequest{
			Model:     l.model,
			System:    prompt.Assemble(l.context, l.cwd),
			Messages:  l.history,
			Tools:     l.tools.Schemas(),
			MaxTokens: 8192,
			Stream:    true,
		}

		// Call provider
		deltaChan, err := l.provider.Chat(ctx, req)
		if err != nil {
			return fmt.Errorf("call provider: %w", err)
		}

		// Collect assistant message from deltas
		assistantMsg, err := l.collectAssistantMessage(ctx, deltaChan, emit)
		if err != nil {
			return fmt.Errorf("collect assistant message: %w", err)
		}

		// Append to history
		l.history = append(l.history, assistantMsg)

		// Persist assistant message
		if err := l.persistMessage("assistant", assistantMsg); err != nil {
			return fmt.Errorf("persist assistant message: %w", err)
		}

		// Check for tool use
		toolUseBlocks := l.findToolUseBlocks(assistantMsg)
		if len(toolUseBlocks) == 0 {
			// No tool use, conversation is complete
			emit(types.OutboundEvent{
				ID:        uuid.New().String(),
				SessionID: l.sessionID,
				Type:      "done",
				Timestamp: time.Now().Unix(),
			})
			return nil
		}

		// Execute tools and collect results
		toolResults, err := l.executeTools(ctx, toolUseBlocks, emit)
		if err != nil {
			return fmt.Errorf("execute tools: %w", err)
		}

		// Add tool results as user message
		toolResultMsg := types.ChatMessage{
			Role:    "user",
			Content: toolResults,
		}
		l.history = append(l.history, toolResultMsg)

		// Persist tool result message
		if err := l.persistMessage("user", toolResultMsg); err != nil {
			return fmt.Errorf("persist tool result message: %w", err)
		}

		// Continue loop
	}
}

func (l *Loop) collectAssistantMessage(ctx context.Context, deltaChan <-chan types.ChatDelta, emit func(types.OutboundEvent)) (types.ChatMessage, error) {
	var contentBlocks []types.ChatContentBlock
	var currentTextBlock *types.ChatContentBlock
	var currentToolUse *types.ChatContentBlock
	var toolUseJSONBuf string // accumulates partial JSON across deltas

	for {
		select {
		case <-ctx.Done():
			return types.ChatMessage{}, ctx.Err()

		case delta, ok := <-deltaChan:
			if !ok {
				// Channel closed, finalize message
				if currentTextBlock != nil && currentTextBlock.Text != "" {
					contentBlocks = append(contentBlocks, *currentTextBlock)
				}
				if currentToolUse != nil {
					contentBlocks = append(contentBlocks, *currentToolUse)
				}

				return types.ChatMessage{
					Role:    "assistant",
					Content: contentBlocks,
				}, nil
			}

			switch delta.Type {
			case "text_delta":
				// Emit text delta event
				emit(types.OutboundEvent{
					ID:        uuid.New().String(),
					SessionID: l.sessionID,
					Type:      "text",
					Content:   delta.Text,
					Timestamp: time.Now().Unix(),
				})

				// Accumulate text in current block
				if currentTextBlock == nil {
					currentTextBlock = &types.ChatContentBlock{
						Type: "text",
					}
				}
				currentTextBlock.Text += delta.Text

			case "tool_use_start":
				// Save any pending text block
				if currentTextBlock != nil && currentTextBlock.Text != "" {
					contentBlocks = append(contentBlocks, *currentTextBlock)
					currentTextBlock = nil
				}

				// Start new tool use block
				currentToolUse = &types.ChatContentBlock{
					Type: "tool_use",
					ID:   delta.ID,
					Name: delta.Name,
				}
				toolUseJSONBuf = ""

			case "tool_use_delta":
				// Accumulate partial JSON fragments — they are NOT individually
				// valid JSON. We concatenate them all and parse once at the end.
				toolUseJSONBuf += delta.PartialJSON

			case "tool_use_end":
				// Parse the fully accumulated JSON and finalize tool use block
				if currentToolUse != nil {
					if toolUseJSONBuf != "" {
						var input map[string]any
						if err := json.Unmarshal([]byte(toolUseJSONBuf), &input); err == nil {
							currentToolUse.Input = input
						}
					}

					// Emit tool_use event now that we have the full input
					emit(types.OutboundEvent{
						ID:        uuid.New().String(),
						SessionID: l.sessionID,
						Type:      "tool_use",
						ToolName:  currentToolUse.Name,
						Content:   toolUseSummary(currentToolUse.Name, currentToolUse.Input),
						Timestamp: time.Now().Unix(),
					})

					contentBlocks = append(contentBlocks, *currentToolUse)
					currentToolUse = nil
					toolUseJSONBuf = ""
				}

			case "message_stop":
				// Message complete
				if currentTextBlock != nil && currentTextBlock.Text != "" {
					contentBlocks = append(contentBlocks, *currentTextBlock)
				}
				if currentToolUse != nil {
					contentBlocks = append(contentBlocks, *currentToolUse)
				}

				return types.ChatMessage{
					Role:    "assistant",
					Content: contentBlocks,
				}, nil

			case "error":
				return types.ChatMessage{}, fmt.Errorf("stream error: %s", delta.Text)
			}
		}
	}
}

func (l *Loop) findToolUseBlocks(msg types.ChatMessage) []types.ChatContentBlock {
	var toolUseBlocks []types.ChatContentBlock
	for _, block := range msg.Content {
		if block.Type == "tool_use" {
			toolUseBlocks = append(toolUseBlocks, block)
		}
	}
	return toolUseBlocks
}

func (l *Loop) executeTools(ctx context.Context, toolUseBlocks []types.ChatContentBlock, emit func(types.OutboundEvent)) ([]types.ChatContentBlock, error) {
	results := make([]types.ChatContentBlock, len(toolUseBlocks))

	var wg sync.WaitGroup
	wg.Add(len(toolUseBlocks))

	for i, block := range toolUseBlocks {
		go func(idx int, block types.ChatContentBlock) {
			defer wg.Done()

			toolCtx := types.ToolContext{
				Ctx:       ctx,
				CWD:       l.cwd,
				SessionID: l.sessionID,
				HistoryID: l.historyID,
				Emit:      emit,
			}

			start := time.Now()
			result, err := l.tools.Execute(block.Name, block.Input, toolCtx)
			l.audit.LogToolCall(types.ToolCallEvent{
				SessionID: l.sessionID,
				ToolName:  block.Name,
				Input:     block.Input,
				Duration:  time.Since(start),
				Error:     err,
			})

			if err != nil {
				result = types.ToolResult{
					Content: []types.ToolResultContent{
						{Type: "text", Text: fmt.Sprintf("Error: %v", err)},
					},
					IsError: true,
				}
			}

			results[idx] = types.ChatContentBlock{
				Type:      "tool_result",
				ToolUseID: block.ID,
				Content:   result.Content,
			}
		}(i, block)
	}

	wg.Wait()
	return results, nil
}

// toolUseSummary returns a short description of what a tool call is doing.
func toolUseSummary(name string, input map[string]any) string {
	str := func(key string) string {
		if v, ok := input[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}

	switch name {
	case "Bash":
		return str("command")
	case "Read":
		return str("file_path")
	case "Write":
		return str("file_path")
	case "Edit":
		return str("file_path")
	case "Glob":
		return str("pattern")
	case "Grep":
		return str("pattern")
	default:
		return ""
	}
}

// nopAuditLogger discards all events.
type nopAuditLogger struct{}

func (nopAuditLogger) LogToolCall(types.ToolCallEvent) {}

func (l *Loop) persistMessage(msgType string, msg types.ChatMessage) error {
	sessionMsg := types.SessionMessage{
		UUID:      uuid.New().String(),
		SessionID: l.historyID,
		Type:      msgType,
		Message:   msg,
		Timestamp: time.Now().Unix(),
	}

	return l.sessionStore.Append(l.historyID, sessionMsg)
}
