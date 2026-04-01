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
	"github.com/jelmersnoeck/forge/internal/runtime/retry"
	"github.com/jelmersnoeck/forge/internal/runtime/session"
	"github.com/jelmersnoeck/forge/internal/runtime/tokens"
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
	budget       tokens.Budget
	retryPolicy  retry.Policy

	// Cumulative token usage across all turns in this session.
	totalUsage types.TokenUsage
	
	// Cache tracking for break detection
	lastCacheRead int
	callCount     int
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
	Budget       *tokens.Budget
	RetryPolicy  *retry.Policy
}

// New creates a new conversation loop.
func New(opts Options) *Loop {
	audit := opts.AuditLogger
	if audit == nil {
		audit = nopAuditLogger{}
	}

	budget := tokens.DefaultBudget()
	if opts.Budget != nil {
		budget = *opts.Budget
	}

	retryPolicy := retry.DefaultPolicy()
	if opts.RetryPolicy != nil {
		retryPolicy = *opts.RetryPolicy
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
		budget:       budget,
		retryPolicy:  retryPolicy,
	}
}

// HistoryID returns the current history ID (used for JSONL session persistence).
func (l *Loop) HistoryID() string {
	return l.historyID
}

// TotalUsage returns the cumulative token usage for this session.
func (l *Loop) TotalUsage() types.TokenUsage {
	return l.totalUsage
}

// Model returns the model being used.
func (l *Loop) Model() string {
	return l.model
}

// Send processes a user prompt and runs the agentic loop.
func (l *Loop) Send(ctx context.Context, promptText string, emit func(types.OutboundEvent)) error {
	userMsg := types.ChatMessage{
		Role: "user",
		Content: []types.ChatContentBlock{
			{Type: "text", Text: promptText},
		},
	}
	l.history = append(l.history, userMsg)

	if err := l.persistMessage("user", userMsg); err != nil {
		return fmt.Errorf("persist user message: %w", err)
	}

	return l.runLoop(ctx, emit)
}

// Resume loads a session and continues with a new prompt.
func (l *Loop) Resume(ctx context.Context, historyID string, promptText string, emit func(types.OutboundEvent)) error {
	sessionMessages, err := l.sessionStore.Load(historyID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	for _, msg := range sessionMessages {
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
	return l.Send(ctx, promptText, emit)
}

func (l *Loop) runLoop(ctx context.Context, emit func(types.OutboundEvent)) error {
	turnCount := 0

	// Emit model info at the start
	if turnCount == 0 {
		emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			SessionID: l.sessionID,
			Type:      "model",
			Content:   l.model,
			Timestamp: time.Now().Unix(),
		})
	}

	for {
		turnCount++

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

		emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			SessionID: l.sessionID,
			Type:      "thinking",
			Timestamp: time.Now().Unix(),
		})

		// Assemble system prompt and tool schemas (stable across turns).
		systemBlocks := prompt.Assemble(l.context, l.cwd)
		toolSchemas := l.tools.Schemas()

		// ── Context window management ──────────────────────────
		// Check if we need to compact before sending to the LLM.
		systemTokens := tokens.EstimateSystem(systemBlocks)
		historyTokens := tokens.EstimateHistory(l.history)
		toolTokens := tokens.EstimateTools(toolSchemas)

		if l.budget.ShouldCompact(systemTokens, historyTokens, toolTokens) {
			compacted, removed := tokens.Compact(l.history, l.budget, systemTokens, toolTokens)
			if removed > 0 {
				l.history = compacted
				historyTokens = tokens.EstimateHistory(l.history)

				emit(types.OutboundEvent{
					ID:        uuid.New().String(),
					SessionID: l.sessionID,
					Type:      "compact",
					Content:   fmt.Sprintf("Compacted conversation: removed %d messages (now ~%d tokens)", removed, systemTokens+historyTokens+toolTokens),
					Timestamp: time.Now().Unix(),
				})
			}
		}

		req := types.ChatRequest{
			Model:     l.model,
			System:    systemBlocks,
			Messages:  l.history,
			Tools:     toolSchemas,
			MaxTokens: 8192,
			Stream:    true,
		}

		// ── Retry-wrapped provider call ────────────────────────
		var deltaChan <-chan types.ChatDelta
		deltaChan, err := retry.Do(ctx, l.retryPolicy,
			func(attempt retry.Attempt) {
				emit(types.OutboundEvent{
					ID:        uuid.New().String(),
					SessionID: l.sessionID,
					Type:      "retry",
					Content:   fmt.Sprintf("API call failed (%v), retrying in %s (attempt %d/%d)", attempt.Err, attempt.Delay.Round(time.Millisecond), attempt.Number, attempt.MaxRetry),
					Timestamp: time.Now().Unix(),
				})
			},
			func() (<-chan types.ChatDelta, error) {
				return l.provider.Chat(ctx, req)
			},
		)
		if err != nil {
			return fmt.Errorf("call provider: %w", err)
		}

		// Collect assistant message from deltas.
		assistantMsg, err := l.collectAssistantMessage(ctx, deltaChan, emit)
		if err != nil {
			return fmt.Errorf("collect assistant message: %w", err)
		}

		l.history = append(l.history, assistantMsg)

		if err := l.persistMessage("assistant", assistantMsg); err != nil {
			return fmt.Errorf("persist assistant message: %w", err)
		}

		// Check for tool use.
		toolUseBlocks := l.findToolUseBlocks(assistantMsg)
		if len(toolUseBlocks) == 0 {
			emit(types.OutboundEvent{
				ID:        uuid.New().String(),
				SessionID: l.sessionID,
				Type:      "done",
				Timestamp: time.Now().Unix(),
			})
			return nil
		}

		// ── ReadOnly-gated concurrent execution ────────────────
		toolResults := l.executeToolsGated(ctx, toolUseBlocks, emit)

		toolResultMsg := types.ChatMessage{
			Role:    "user",
			Content: toolResults,
		}
		l.history = append(l.history, toolResultMsg)

		if err := l.persistMessage("user", toolResultMsg); err != nil {
			return fmt.Errorf("persist tool result message: %w", err)
		}
	}
}

func (l *Loop) collectAssistantMessage(ctx context.Context, deltaChan <-chan types.ChatDelta, emit func(types.OutboundEvent)) (types.ChatMessage, error) {
	var contentBlocks []types.ChatContentBlock
	var currentTextBlock *types.ChatContentBlock
	var currentToolUse *types.ChatContentBlock
	var toolUseJSONBuf string

	for {
		select {
		case <-ctx.Done():
			return types.ChatMessage{}, ctx.Err()

		case delta, ok := <-deltaChan:
			if !ok {
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
				emit(types.OutboundEvent{
					ID:        uuid.New().String(),
					SessionID: l.sessionID,
					Type:      "text",
					Content:   delta.Text,
					Timestamp: time.Now().Unix(),
				})

				if currentTextBlock == nil {
					currentTextBlock = &types.ChatContentBlock{
						Type: "text",
					}
				}
				currentTextBlock.Text += delta.Text

			case "tool_use_start":
				if currentTextBlock != nil && currentTextBlock.Text != "" {
					contentBlocks = append(contentBlocks, *currentTextBlock)
					currentTextBlock = nil
				}

				currentToolUse = &types.ChatContentBlock{
					Type: "tool_use",
					ID:   delta.ID,
					Name: delta.Name,
				}
				toolUseJSONBuf = ""

			case "tool_use_delta":
				toolUseJSONBuf += delta.PartialJSON

			case "tool_use_end":
				if currentToolUse != nil {
					if toolUseJSONBuf != "" {
						var input map[string]any
						if err := json.Unmarshal([]byte(toolUseJSONBuf), &input); err == nil {
							currentToolUse.Input = input
						}
					}

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

			case "usage":
				// Track token usage from the provider.
				if delta.Usage != nil {
					if delta.Usage.InputTokens > 0 {
						l.totalUsage.InputTokens += delta.Usage.InputTokens
					}
					if delta.Usage.OutputTokens > 0 {
						l.totalUsage.OutputTokens += delta.Usage.OutputTokens
					}
					if delta.Usage.CacheCreationTokens > 0 {
						l.totalUsage.CacheCreationTokens += delta.Usage.CacheCreationTokens
					}
					if delta.Usage.CacheReadTokens > 0 {
						l.totalUsage.CacheReadTokens += delta.Usage.CacheReadTokens
					}
					
					// Check for cache breaks (simple detection)
					l.checkCacheHealth(delta.Usage, emit)

				emit(types.OutboundEvent{
					ID:        uuid.New().String(),
					SessionID: l.sessionID,
					Type:      "usage",
					Usage:     &l.totalUsage,
					Model:     l.model,
					Timestamp: time.Now().Unix(),
				})
				}

			case "message_stop":
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

// executeToolsGated partitions tools by ReadOnly and runs them in two phases:
//
//  1. ReadOnly tools run concurrently (Read, Glob, Grep, WebSearch, etc.)
//  2. Mutating tools run sequentially (Write, Edit, Bash, etc.)
//
// This prevents races between writes while keeping reads fast.
func (l *Loop) executeToolsGated(ctx context.Context, toolUseBlocks []types.ChatContentBlock, emit func(types.OutboundEvent)) []types.ChatContentBlock {
	type indexedBlock struct {
		idx   int
		block types.ChatContentBlock
	}

	var readOnly, mutating []indexedBlock
	for i, block := range toolUseBlocks {
		if l.tools.IsReadOnly(block.Name) {
			readOnly = append(readOnly, indexedBlock{i, block})
		} else {
			mutating = append(mutating, indexedBlock{i, block})
		}
	}

	results := make([]types.ChatContentBlock, len(toolUseBlocks))

	// Phase 1: ReadOnly tools — fan out.
	if len(readOnly) > 0 {
		var wg sync.WaitGroup
		wg.Add(len(readOnly))
		for _, ib := range readOnly {
			go func(idx int, block types.ChatContentBlock) {
				defer wg.Done()
				results[idx] = l.executeSingleTool(ctx, block, emit)
			}(ib.idx, ib.block)
		}
		wg.Wait()
	}

	// Phase 2: Mutating tools — sequential.
	for _, ib := range mutating {
		results[ib.idx] = l.executeSingleTool(ctx, ib.block, emit)
	}

	return results
}

func (l *Loop) executeSingleTool(ctx context.Context, block types.ChatContentBlock, emit func(types.OutboundEvent)) types.ChatContentBlock {
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

	return types.ChatContentBlock{
		Type:      "tool_result",
		ToolUseID: block.ID,
		Content:   result.Content,
	}
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

// checkCacheHealth detects unexpected cache invalidation.
// Logs a warning when cache_read_tokens drops significantly (>5% and >2K tokens).
func (l *Loop) checkCacheHealth(usage *types.TokenUsage, emit func(types.OutboundEvent)) {
	l.callCount++
	
	// First call - just record baseline
	if l.lastCacheRead == 0 {
		l.lastCacheRead = usage.CacheReadTokens
		return
	}
	
	// Check for cache break (>5% drop and >2K tokens)
	tokenDrop := l.lastCacheRead - usage.CacheReadTokens
	percentDrop := float64(l.lastCacheRead-usage.CacheReadTokens) / float64(l.lastCacheRead)
	
	if percentDrop > 0.05 && tokenDrop > 2000 {
		// Cache broke unexpectedly
		emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			SessionID: l.sessionID,
			Type:      "warning",
			Content: fmt.Sprintf(
				"[CACHE BREAK] Call #%d: %d → %d tokens (-%d, -%.0f%%) - Check for system prompt or tool schema changes",
				l.callCount,
				l.lastCacheRead,
				usage.CacheReadTokens,
				tokenDrop,
				percentDrop*100,
			),
			Timestamp: time.Now().Unix(),
		})
	}
	
	l.lastCacheRead = usage.CacheReadTokens
}
