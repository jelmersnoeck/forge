// Package loop implements the agentic conversation loop.
package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	classifyerr "github.com/jelmersnoeck/forge/internal/runtime/errors"
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
	onComplete   func(history []types.ChatMessage)

	// Cumulative token usage across all turns in this session.
	totalUsage types.TokenUsage

	// Cache tracking for break detection
	lastCacheRead int
	callCount     int

	// Per-session file read dedup state, shared across all tool calls.
	readState types.ReadState

	// toolsUsed tracks whether any tool was executed during the loop.
	toolsUsed bool
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

	// OnComplete is called when a conversation turn finishes (after the LLM
	// stops requesting tools). Receives the full message history so the
	// caller can build a session summary. Only called when at least one
	// tool was executed during the turn.
	OnComplete func(history []types.ChatMessage)
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
		readState:    make(types.ReadState),
		onComplete:   opts.OnComplete,
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

const maxCompactRetries = 3

func (l *Loop) runLoop(ctx context.Context, emit func(types.OutboundEvent)) error {
	turnCount := 0
	streamRetries := 0
	compactRetries := 0

	emit(types.OutboundEvent{
		ID:        uuid.New().String(),
		SessionID: l.sessionID,
		Type:      "model",
		Content:   l.model,
		Timestamp: time.Now().Unix(),
	})

	for {
		turnCount++

		// Bail early if context is cancelled (e.g., user interrupted).
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if l.maxTurns > 0 && turnCount > l.maxTurns {
			l.fireOnComplete()
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

		// Sanitize history before sending — drops orphaned tool_results
		// and trailing tool_use without results (e.g., from interrupted turns).
		l.history = tokens.SanitizeHistory(l.history)

		// Add cache control to the last message (critical for caching efficiency)
		// Max 4 cache_control blocks: system(2) + tools(1) + messages(1) = 4
		messagesWithCache := addMessageCacheControl(l.history)

		// ── Context window management ──────────────────────────
		// Check if we need to compact before sending to the LLM.
		systemTokens := tokens.EstimateSystem(systemBlocks)
		historyTokens := tokens.EstimateHistory(messagesWithCache)
		toolTokens := tokens.EstimateTools(toolSchemas)

		if l.budget.ShouldCompact(systemTokens, historyTokens, toolTokens) {
			compacted, removed := tokens.Compact(messagesWithCache, l.budget, systemTokens, toolTokens)
			if removed > 0 {
				messagesWithCache = compacted
				// Re-add cache control after compaction
				messagesWithCache = addMessageCacheControl(messagesWithCache)
				historyTokens = tokens.EstimateHistory(messagesWithCache)

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
			Messages:  messagesWithCache,
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
			// ── Stream error classification ────────────────────
			// Errors from the delta channel carry HTTP status codes.
			// Classify them to decide: compact, retry, or bail.
			var se *streamError
			if !errors.As(err, &se) {
				return fmt.Errorf("collect assistant message: %w", err)
			}

			classified := classifyerr.Classify(se, se.statusCode)

			switch {
			case classified.ShouldCompact:
				compactRetries++
				if compactRetries > maxCompactRetries {
					return fmt.Errorf("prompt too long after %d compaction attempts: %w", maxCompactRetries, err)
				}

				compacted, removed := tokens.Compact(l.history, l.budget, systemTokens, toolTokens)
				l.history = compacted
				emit(types.OutboundEvent{
					ID:        uuid.New().String(),
					SessionID: l.sessionID,
					Type:      "compact",
					Content:   fmt.Sprintf("Prompt too long — compacted: removed %d messages (attempt %d/%d)", removed, compactRetries, maxCompactRetries),
					Timestamp: time.Now().Unix(),
				})
				turnCount-- // don't count failed turn
				continue

			case classified.IsRetryable:
				streamRetries++
				if streamRetries > l.retryPolicy.MaxRetries {
					return fmt.Errorf("stream error after %d retries: %w", l.retryPolicy.MaxRetries, err)
				}

				delay := retry.Backoff(streamRetries-1, l.retryPolicy.BaseDelay, l.retryPolicy.MaxDelay)
				emit(types.OutboundEvent{
					ID:        uuid.New().String(),
					SessionID: l.sessionID,
					Type:      "retry",
					Content:   fmt.Sprintf("Stream error (%s), retrying in %s (attempt %d/%d)", classified.Message, delay.Round(time.Millisecond), streamRetries, l.retryPolicy.MaxRetries),
					Timestamp: time.Now().Unix(),
				})

				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(delay):
				}
				turnCount-- // don't count failed turn
				continue

			default:
				return fmt.Errorf("API error: %w", err)
			}
		}

		// Success — reset stream retry counters.
		streamRetries = 0
		compactRetries = 0

		l.history = append(l.history, assistantMsg)

		if err := l.persistMessage("assistant", assistantMsg); err != nil {
			return fmt.Errorf("persist assistant message: %w", err)
		}

		// Check for tool use.
		toolUseBlocks := l.findToolUseBlocks(assistantMsg)
		if len(toolUseBlocks) == 0 {
			l.fireOnComplete()
			emit(types.OutboundEvent{
				ID:        uuid.New().String(),
				SessionID: l.sessionID,
				Type:      "done",
				Timestamp: time.Now().Unix(),
			})
			return nil
		}

		l.toolsUsed = true

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

// streamError carries an API error surfaced through the delta channel.
// Wraps the error message with the HTTP status code so the loop can classify it.
type streamError struct {
	message    string
	statusCode int
}

func (e *streamError) Error() string { return e.message }

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
				if delta.Usage != nil {
					l.totalUsage.InputTokens += delta.Usage.InputTokens
					l.totalUsage.OutputTokens += delta.Usage.OutputTokens
					l.totalUsage.CacheCreationTokens += delta.Usage.CacheCreationTokens
					l.totalUsage.CacheReadTokens += delta.Usage.CacheReadTokens

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
				return types.ChatMessage{}, &streamError{
					message:    delta.Text,
					statusCode: delta.StatusCode,
				}
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
// If the context is cancelled (e.g., user interrupt), the method returns
// immediately with "interrupted" results for any tools that haven't finished.
// Running tools still get the cancelled context and will clean up on their own.
//
// Results flow through a channel to avoid data races between background tool
// goroutines and the caller reading the returned slice.
//
//	             ┌─────────────┐
//	ctx.Done ──►│ select {    │◄── resultCh delivers completed tools
//	             │ case result │
//	             │ case <-ctx  │
//	             └─────────────┘
func (l *Loop) executeToolsGated(ctx context.Context, toolUseBlocks []types.ChatContentBlock, emit func(types.OutboundEvent)) []types.ChatContentBlock {
	type indexedResult struct {
		idx    int
		result types.ChatContentBlock
	}

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

	// Pre-fill all slots with interrupted stubs.
	results := make([]types.ChatContentBlock, len(toolUseBlocks))
	for i, block := range toolUseBlocks {
		results[i] = types.ChatContentBlock{
			Type:      "tool_result",
			ToolUseID: block.ID,
			Content: []types.ToolResultContent{
				{Type: "text", Text: "Interrupted before completion"},
			},
		}
	}

	total := len(toolUseBlocks)
	resultCh := make(chan indexedResult, total)

	// Background: run tools and send results through channel.
	go func() {
		// Phase 1: ReadOnly tools — fan out.
		if len(readOnly) > 0 {
			var wg sync.WaitGroup
			wg.Add(len(readOnly))
			for _, ib := range readOnly {
				go func(idx int, block types.ChatContentBlock) {
					defer wg.Done()
					resultCh <- indexedResult{idx, l.executeSingleTool(ctx, block, emit)}
				}(ib.idx, ib.block)
			}
			wg.Wait()
		}

		// Phase 2: Mutating tools — sequential.
		for _, ib := range mutating {
			if ctx.Err() != nil {
				return
			}
			resultCh <- indexedResult{ib.idx, l.executeSingleTool(ctx, ib.block, emit)}
		}
	}()

	// Collect results until all tools finish or context is cancelled.
	// Only the collector goroutine (this one) writes to results[],
	// so there's no race with background goroutines.
	collected := 0
	for collected < total {
		select {
		case ir := <-resultCh:
			results[ir.idx] = ir.result
			collected++
		case <-ctx.Done():
			return results
		}
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
		ReadState: l.readState,
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

// toolSummaryKeys maps tool names to the input key that best summarizes the call.
var toolSummaryKeys = map[string]string{
	"Bash": "command", "Read": "file_path", "Write": "file_path",
	"Edit": "file_path", "Glob": "pattern", "Grep": "pattern",
	"TaskGet": "task_id", "AgentGet": "agent_id",
	"TaskOutput": "task_id",
}

// toolUseSummary returns a short description of what a tool call is doing.
func toolUseSummary(name string, input map[string]any) string {
	key, ok := toolSummaryKeys[name]
	if !ok {
		return ""
	}
	s, _ := input[key].(string)
	return s
}

// nopAuditLogger discards all events.
type nopAuditLogger struct{}

func (nopAuditLogger) LogToolCall(types.ToolCallEvent) {}

// fireOnComplete invokes the OnComplete callback if tools were used.
// Recovers from panics so a bad callback never crashes the agent.
func (l *Loop) fireOnComplete() {
	if l.onComplete == nil || !l.toolsUsed {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			// Don't let a callback panic kill the agent.
			_ = r
		}
	}()
	l.onComplete(l.history)
}

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

// addMessageCacheControl adds cache_control breakpoints to conversation history.
//
// Strategy: tag the second-to-last user message's last content block. This
// creates a stable cache prefix covering all completed turns:
//
//	Call N:   [msg0 … msgK-2•, msgK-1, msgK]   ← breakpoint on K-2
//	Call N+1: [msg0 … msgK-2•, msgK-1, msgK, msgK+1, msgK+2]  ← breakpoint on K
//
// The prefix up to the breakpoint is identical between Call N and N+1's later
// iterations, so system+tools+history all hit cache. Only the newest messages
// (past the breakpoint) are uncached input — cheap because they're small.
//
// Previously the breakpoint sat on the absolute last message, which changed
// every single API call, causing ~50% cache miss rate on the 24K system+tools
// prefix.
//
// Max 4 cache_control blocks: system(2) + tools(1) + messages(1) = 4
func addMessageCacheControl(messages []types.ChatMessage) []types.ChatMessage {
	if len(messages) == 0 {
		return messages
	}

	// Find the second-to-last user message — that's the last "completed"
	// exchange boundary. Everything up to (and including) it is stable
	// across consecutive agentic-loop calls.
	targetIdx := -1
	userCount := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			userCount++
			if userCount == 2 {
				targetIdx = i
				break
			}
		}
	}

	// Not enough history for a stable breakpoint — fall back to last message.
	if targetIdx < 0 {
		targetIdx = len(messages) - 1
	}

	// Deep copy to avoid mutating the original history.
	result := make([]types.ChatMessage, len(messages))
	copy(result, messages)

	msg := &result[targetIdx]
	if len(msg.Content) > 0 {
		msg.Content = make([]types.ChatContentBlock, len(messages[targetIdx].Content))
		copy(msg.Content, messages[targetIdx].Content)

		lastBlock := &msg.Content[len(msg.Content)-1]
		lastBlock.CacheControl = &types.CacheControl{
			Type: "ephemeral",
			TTL:  "1h",
		}
	}

	return result
}
