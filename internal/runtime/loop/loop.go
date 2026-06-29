// Package loop implements the agentic conversation loop.
package loop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	provider       types.LLMProvider
	tools          *tools.Registry
	context        types.ContextBundle
	cwd            string
	sessionStore   *session.Store
	sessionID      string
	model          string
	maxTurns       int
	historyID      string
	history        []types.ChatMessage
	audit          types.AuditLogger
	budget         tokens.Budget
	retryPolicy    retry.Policy
	onComplete     func(history []types.ChatMessage)
	steeringSource func() (string, bool)

	// Cumulative token usage across all turns in this session.
	totalUsage types.TokenUsage

	// Cache tracking for break detection
	lastCacheRead int
	callCount     int

	// Hashes + serialized content of the previous request's cacheable
	// components, used to report what changed when the cache breaks.
	lastSystemHash string
	lastToolsHash  string
	lastMsgsHash   string
	lastParamsHash string
	lastSystemRaw  string
	lastToolsRaw   string
	lastMsgsRaw    string
	lastParamsRaw  string

	// Hashes of the in-flight request's components, computed just before
	// the provider call and compared against last* on cache break.
	currSystemHash string
	currToolsHash  string
	currMsgsHash   string
	currParamsHash string
	currSystemRaw  string
	currToolsRaw   string
	currMsgsRaw    string
	currParamsRaw  string

	// Per-session file read dedup state, shared across all tool calls.
	readState *types.ReadState

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

	// SteeringSource is called between LLM iterations to check for
	// mid-turn user messages. Returns (text, true) if a steering message
	// is available. Non-blocking — must not wait for input.
	SteeringSource func() (string, bool)
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
		provider:       opts.Provider,
		tools:          opts.Tools,
		context:        opts.Context,
		cwd:            opts.CWD,
		sessionStore:   opts.SessionStore,
		sessionID:      opts.SessionID,
		model:          opts.Model,
		maxTurns:       opts.MaxTurns,
		historyID:      uuid.New().String(),
		history:        []types.ChatMessage{},
		audit:          audit,
		budget:         budget,
		retryPolicy:    retryPolicy,
		readState:      types.NewReadState(),
		onComplete:     opts.OnComplete,
		steeringSource: opts.SteeringSource,
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

// ToolsUsed returns whether any tool was executed during this loop's lifetime.
func (l *Loop) ToolsUsed() bool {
	return l.toolsUsed
}

// Model returns the model being used.
func (l *Loop) Model() string {
	return l.model
}

// Send processes a user prompt and runs the agentic loop. An empty or
// whitespace-only promptText is not appended to history (the Anthropic API
// rejects empty text blocks), but the loop still runs against existing history
// — this lets resume/continuation paths advance without a new message.
func (l *Loop) Send(ctx context.Context, promptText string, emit func(types.OutboundEvent)) error {
	if strings.TrimSpace(promptText) != "" {
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

		// ── Steering checkpoint ──────────────────────────────
		// Consume any user messages queued mid-turn and inject them into
		// history before the next LLM call. This lets the user steer the
		// agent without interrupting — the LLM sees the new context on
		// its next iteration.
		if l.steeringSource != nil {
			for {
				text, ok := l.steeringSource()
				if !ok {
					break
				}

				// Skip empty/whitespace-only steering messages — they would
				// produce an empty text block that the API rejects.
				if strings.TrimSpace(text) == "" {
					continue
				}

				steerMsg := types.ChatMessage{
					Role: "user",
					Content: []types.ChatContentBlock{
						{Type: "text", Text: text},
					},
				}
				l.history = append(l.history, steerMsg)

				if err := l.persistMessage("user", steerMsg); err != nil {
					return fmt.Errorf("persist steering message: %w", err)
				}

				emit(types.OutboundEvent{
					ID:        uuid.New().String(),
					SessionID: l.sessionID,
					Type:      "steering",
					Content:   text,
					Timestamp: time.Now().Unix(),
				})
			}
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
			// Phase 1: summarize the about-to-be-dropped messages before
			// lossy deletion. Summarization failure is non-fatal — fall back
			// to count-only compaction.
			summary := ""
			if drop := tokens.DropSet(messagesWithCache, l.budget, systemTokens, toolTokens); len(drop) > 0 {
				s, err := tokens.Summarize(ctx, l.provider, drop, l.budget)
				switch {
				case err != nil:
					emit(types.OutboundEvent{
						ID:        uuid.New().String(),
						SessionID: l.sessionID,
						Type:      "warning",
						Content:   fmt.Sprintf("History summarization failed, compacting without summary: %v", err),
						Timestamp: time.Now().Unix(),
					})
				default:
					summary = s
				}
			}

			compacted, removed := tokens.CompactWithSummary(messagesWithCache, l.budget, systemTokens, toolTokens, summary)
			if removed > 0 {
				messagesWithCache = compacted
				// Re-add cache control after compaction
				messagesWithCache = addMessageCacheControl(messagesWithCache)
				historyTokens = tokens.EstimateHistory(messagesWithCache)

				detail := "removed"
				if summary != "" {
					detail = "summarized and removed"
				}
				emit(types.OutboundEvent{
					ID:        uuid.New().String(),
					SessionID: l.sessionID,
					Type:      "compact",
					Content:   fmt.Sprintf("Compacted conversation: %s %d messages (now ~%d tokens)", detail, removed, systemTokens+historyTokens+toolTokens),
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

		// Hash the cacheable components before sending so a cache break
		// (detected later via the usage delta) can report what changed.
		// Side effects: writes the curr* hash fields and, on a marshal failure,
		// emits a "warning" OutboundEvent, logs to stderr, and bumps the
		// CacheHashFailures counter. It never returns an error or aborts the turn
		// (hashing is diagnostics-only, never on the critical path).
		l.hashCacheComponents(systemBlocks, toolSchemas, messagePrefix(messagesWithCache), cacheParams{Model: req.Model, MaxTokens: req.MaxTokens}, emit)

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

					// Cache-health detection only runs on the cache-bearing
					// usage delta (message_start). The output-only message_delta
					// reports cache_read=0, which would otherwise look like a
					// 100% cache eviction and corrupt the baseline.
					if hasCacheSignal(delta.Usage) {
						l.checkCacheHealth(delta.Usage, emit)
					}

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

// hasCacheSignal reports whether a usage delta carries the cache/input numbers
// emitted on message_start, as opposed to the output-only numbers on
// message_delta (where cache_read/cache_creation/input are all zero). Only
// cache-bearing deltas may drive cache-health detection — feeding the
// output-only delta in would register a spurious 100% cache eviction and reset
// the baseline to zero.
func hasCacheSignal(u *types.TokenUsage) bool {
	return u.CacheReadTokens > 0 || u.CacheCreationTokens > 0 || u.InputTokens > 0
}

// checkCacheHealth detects unexpected cache invalidation.
// Logs a warning when cache_read_tokens drops significantly (>5% and >2K tokens),
// naming which request component changed (system, tools, or messages) and
// writing a full diff to a temp file for manual inspection.
func (l *Loop) checkCacheHealth(usage *types.TokenUsage, emit func(types.OutboundEvent)) {
	l.callCount++

	// First call - just record baseline (tokens + component hashes).
	if l.lastCacheRead == 0 {
		l.lastCacheRead = usage.CacheReadTokens
		l.promoteCacheHashes()
		return
	}

	// Check for cache break (>5% drop and >2K tokens)
	tokenDrop := l.lastCacheRead - usage.CacheReadTokens
	percentDrop := float64(l.lastCacheRead-usage.CacheReadTokens) / float64(l.lastCacheRead)

	if percentDrop > 0.05 && tokenDrop > 2000 {
		changes := l.diffCacheComponents()

		detail := fmt.Sprintf(
			"changed: none (TTL expiry or provider eviction?) — hashes: system=%s, tools=%s, msgs=%s, params=%s",
			l.currSystemHash, l.currToolsHash, l.currMsgsHash, l.currParamsHash,
		)
		if len(changes) > 0 {
			parts := make([]string, len(changes))
			for i, c := range changes {
				parts[i] = fmt.Sprintf("%s (%s→%s)", c.Name, c.OldHash, c.NewHash)
			}
			detail = "changed: " + strings.Join(parts, ", ")

			if path, err := writeCacheBreakDiff(changes); err != nil {
				// Non-fatal: surface why the diff is missing instead of
				// swallowing the error (disk full, permissions, etc.).
				detail += fmt.Sprintf(" — diff write failed: %v", err)
			} else if path != "" {
				detail += " — diff: " + path
			}
		}

		emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			SessionID: l.sessionID,
			Type:      "warning",
			Content: fmt.Sprintf(
				"[CACHE BREAK] Call #%d: %d → %d tokens (-%d, -%.0f%%) - %s",
				l.callCount,
				l.lastCacheRead,
				usage.CacheReadTokens,
				tokenDrop,
				percentDrop*100,
				detail,
			),
			Timestamp: time.Now().Unix(),
		})
	}

	l.lastCacheRead = usage.CacheReadTokens
	l.promoteCacheHashes()
}

// cacheComponentChange records a single changed request component.
type cacheComponentChange struct {
	Name    string // "system" | "tools" | "messages" | "params"
	OldHash string
	NewHash string
	OldRaw  string
	NewRaw  string
}

// diffCacheComponents compares the previous request's component hashes against
// the in-flight request's and returns the set that changed.
func (l *Loop) diffCacheComponents() []cacheComponentChange {
	var changes []cacheComponentChange
	if l.currSystemHash != l.lastSystemHash {
		changes = append(changes, cacheComponentChange{"system", l.lastSystemHash, l.currSystemHash, l.lastSystemRaw, l.currSystemRaw})
	}
	if l.currToolsHash != l.lastToolsHash {
		changes = append(changes, cacheComponentChange{"tools", l.lastToolsHash, l.currToolsHash, l.lastToolsRaw, l.currToolsRaw})
	}
	if l.currMsgsHash != l.lastMsgsHash {
		changes = append(changes, cacheComponentChange{"messages", l.lastMsgsHash, l.currMsgsHash, l.lastMsgsRaw, l.currMsgsRaw})
	}
	if l.currParamsHash != l.lastParamsHash {
		changes = append(changes, cacheComponentChange{"params", l.lastParamsHash, l.currParamsHash, l.lastParamsRaw, l.currParamsRaw})
	}
	return changes
}

// promoteCacheHashes copies the in-flight request's component hashes into the
// last* fields so the next call diffs against this one.
func (l *Loop) promoteCacheHashes() {
	l.lastSystemHash, l.lastSystemRaw = l.currSystemHash, l.currSystemRaw
	l.lastToolsHash, l.lastToolsRaw = l.currToolsHash, l.currToolsRaw
	l.lastMsgsHash, l.lastMsgsRaw = l.currMsgsHash, l.currMsgsRaw
	l.lastParamsHash, l.lastParamsRaw = l.currParamsHash, l.currParamsRaw
}

// cacheParams is the typed view of the request parameters that participate in
// the prompt cache key. Using an explicit struct (instead of a map[string]any
// with magic keys) keeps the serialized set closed: only Model and MaxTokens —
// both forge-controlled scalars, never free-form user input — are ever hashed or
// written to the diff file, so the temp dump cannot leak arbitrary request data.
type cacheParams struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"maxTokens"`
}

// cacheComponent names a single hashable request component plus a setter that
// writes the computed hash/raw into the matching curr* fields. A setter (rather
// than *string pointers) keeps the writes explicit and lets the caller skip the
// write entirely on a marshal error.
type cacheComponent struct {
	name  string
	value any
	set   func(hash, raw string)
}

// cacheHashFailures counts component marshal failures across the process so
// operators can monitor them (a hash failure silently degrades cache-break
// diagnostics). Exposed via CacheHashFailures.
var cacheHashFailures atomic.Uint64

// CacheHashFailures returns the cumulative number of cache-component hashing
// failures observed since process start, for metrics/monitoring.
func CacheHashFailures() uint64 { return cacheHashFailures.Load() }

// hashCacheComponents hashes each cacheable request component and stores the
// results into the curr* fields for later break diffing. On a marshal error the
// curr* fields are left untouched (no invalid/sentinel data is written) and the
// failure is surfaced three ways: an ephemeral warning event, a persistent
// stderr log line (so a missed event doesn't lose operational context), and an
// incremented CacheHashFailures metric counter. Errors keep their per-component
// association by name so debugging a join isn't ambiguous.
func (l *Loop) hashCacheComponents(system, tools, messages, params any, emit func(types.OutboundEvent)) {
	components := []cacheComponent{
		{"system", system, func(h, r string) { l.currSystemHash, l.currSystemRaw = h, r }},
		{"tools", tools, func(h, r string) { l.currToolsHash, l.currToolsRaw = h, r }},
		{"messages", messages, func(h, r string) { l.currMsgsHash, l.currMsgsRaw = h, r }},
		{"params", params, func(h, r string) { l.currParamsHash, l.currParamsRaw = h, r }},
	}

	var hashErrs []error
	for _, c := range components {
		hash, raw, err := hashComponent(c.value)
		if err != nil {
			hashErrs = append(hashErrs, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		c.set(hash, raw)
	}

	if len(hashErrs) == 0 {
		return
	}

	cacheHashFailures.Add(uint64(len(hashErrs)))
	joined := errors.Join(hashErrs...)
	// Persistent record: an SSE warning can be missed, a log line on stderr
	// survives for post-hoc inspection / log aggregation.
	log.Printf("[CACHE HASH] session=%s failed to hash %d request component(s): %v", l.sessionID, len(hashErrs), joined)
	emit(types.OutboundEvent{
		ID:        uuid.New().String(),
		SessionID: l.sessionID,
		Type:      "warning",
		Content:   fmt.Sprintf("[CACHE HASH] failed to hash %d request component(s): %v", len(hashErrs), joined),
		Timestamp: time.Now().Unix(),
	})
}

// hashComponent serializes v to JSON and returns a short sha256 hash (first 6
// hex chars), the raw JSON for diffing, and any marshal error. Serialization is
// deterministic for identical content: encoding/json sorts struct fields by
// declaration order and map keys lexically (guaranteed since Go 1.12), so equal
// values always produce byte-identical JSON and therefore identical hashes — no
// spurious cache-break reports from ordering.
//
// On marshal failure the error is returned explicitly (callers surface it via a
// warning event rather than ignoring it); the hash falls back to "error" and the
// raw to the error text so the failure is still visible in any diff dump.
func hashComponent(v any) (hash string, raw string, err error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "error", fmt.Sprintf("hashComponent marshal error: %v", err), err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:6], string(b), nil
}

// messagePrefix returns the messages up to and including the cache-breakpoint
// message, with CacheControl stripped, so hashing reflects content only and not
// the positional breakpoint metadata (which moves every call by design).
//
// The breakpoint is message-level, not block-level: addMessageCacheControl only
// ever tags a single block (the last) of a single message, so a message is the
// breakpoint if ANY of its blocks carries CacheControl. All blocks of that
// message are retained (they are part of the cached prefix); enumeration stops
// after that message. This intentionally does not cut blocks mid-message — the
// cache prefix granularity is the message boundary.
func messagePrefix(messages []types.ChatMessage) []types.ChatMessage {
	prefix := make([]types.ChatMessage, 0, len(messages))
	for _, msg := range messages {
		stripped := types.ChatMessage{Role: msg.Role}
		stripped.Content = make([]types.ChatContentBlock, len(msg.Content))
		breakpoint := false
		for i, block := range msg.Content {
			if block.CacheControl != nil {
				breakpoint = true
			}
			block.CacheControl = nil
			stripped.Content[i] = block
		}
		prefix = append(prefix, stripped)
		if breakpoint {
			break
		}
	}
	return prefix
}

// writeCacheBreakDiff dumps the old and new serialized content for each changed
// component to a temp file and returns its path. Returns ("", nil) when there is
// nothing to diff, or ("", err) when the file write fails — callers must treat
// the diff as best-effort and surface (not swallow) any error.
func writeCacheBreakDiff(changes []cacheComponentChange) (string, error) {
	if len(changes) == 0 {
		return "", nil
	}

	var b strings.Builder
	for _, c := range changes {
		fmt.Fprintf(&b, "=== %s: %s → %s ===\n", c.Name, c.OldHash, c.NewHash)
		fmt.Fprintf(&b, "--- old\n%s\n", c.OldRaw)
		fmt.Fprintf(&b, "+++ new\n%s\n\n", c.NewRaw)
	}

	// 0600: the dump contains raw system/tool/message content that may be
	// sensitive. Restrict to the owner so other users on a shared host or
	// shared TMPDIR cannot read it.
	path := filepath.Join(os.TempDir(), fmt.Sprintf("forge-cache-break-%d.diff", time.Now().UnixNano()))
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", fmt.Errorf("write cache-break diff to %s: %w", path, err)
	}
	return path, nil
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
