package loop

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jelmersnoeck/forge/internal/runtime/retry"
	"github.com/jelmersnoeck/forge/internal/runtime/session"
	"github.com/jelmersnoeck/forge/internal/runtime/tokens"
	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

// ── Mock providers ────────────────────────────────────────────

// MockTextProvider returns a simple text response.
type MockTextProvider struct{}

func (m *MockTextProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	ch := make(chan types.ChatDelta, 4)
	go func() {
		defer close(ch)
		ch <- types.ChatDelta{Type: "text_delta", Text: "Hello from "}
		ch <- types.ChatDelta{Type: "text_delta", Text: "Greendale!"}
		ch <- types.ChatDelta{Type: "message_stop", StopReason: "end_turn"}
	}()
	return ch, nil
}

// MockToolProvider returns a tool_use on first call, text on second.
type MockToolProvider struct {
	callCount int
	mu        sync.Mutex
}

func (m *MockToolProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	m.mu.Lock()
	m.callCount++
	count := m.callCount
	m.mu.Unlock()

	ch := make(chan types.ChatDelta, 8)

	go func() {
		defer close(ch)

		switch count {
		case 1:
			ch <- types.ChatDelta{Type: "tool_use_start", ID: "tool-1", Name: "read"}
			ch <- types.ChatDelta{Type: "tool_use_delta", PartialJSON: `{"file_path":"/tmp/test.txt"}`}
			ch <- types.ChatDelta{Type: "tool_use_end"}
			ch <- types.ChatDelta{Type: "message_stop", StopReason: "tool_use"}

		default:
			ch <- types.ChatDelta{Type: "text_delta", Text: "File read successfully"}
			ch <- types.ChatDelta{Type: "message_stop", StopReason: "end_turn"}
		}
	}()

	return ch, nil
}

// MockUsageProvider returns text with usage deltas.
type MockUsageProvider struct{}

func (m *MockUsageProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	ch := make(chan types.ChatDelta, 8)
	go func() {
		defer close(ch)
		ch <- types.ChatDelta{Type: "usage", Usage: &types.TokenUsage{InputTokens: 1500, OutputTokens: 0}}
		ch <- types.ChatDelta{Type: "text_delta", Text: "Streets ahead!"}
		ch <- types.ChatDelta{Type: "usage", Usage: &types.TokenUsage{OutputTokens: 42}}
		ch <- types.ChatDelta{Type: "message_stop", StopReason: "end_turn"}
	}()
	return ch, nil
}

// MockRetryProvider fails N times then succeeds.
type MockRetryProvider struct {
	failCount int
	callCount int
	mu        sync.Mutex
}

func (m *MockRetryProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	m.mu.Lock()
	m.callCount++
	count := m.callCount
	m.mu.Unlock()

	if count <= m.failCount {
		return nil, fmt.Errorf("status 529: overloaded")
	}

	ch := make(chan types.ChatDelta, 4)
	go func() {
		defer close(ch)
		ch <- types.ChatDelta{Type: "text_delta", Text: "Pop pop!"}
		ch <- types.ChatDelta{Type: "message_stop", StopReason: "end_turn"}
	}()
	return ch, nil
}

// MockMultiToolProvider returns multiple tool_use blocks on first call.
type MockMultiToolProvider struct {
	callCount int
	mu        sync.Mutex
}

func (m *MockMultiToolProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	m.mu.Lock()
	m.callCount++
	count := m.callCount
	m.mu.Unlock()

	ch := make(chan types.ChatDelta, 16)
	go func() {
		defer close(ch)

		switch count {
		case 1:
			// Two read-only + one mutating tool
			ch <- types.ChatDelta{Type: "tool_use_start", ID: "t1", Name: "Glob"}
			ch <- types.ChatDelta{Type: "tool_use_delta", PartialJSON: `{"pattern":"*.go"}`}
			ch <- types.ChatDelta{Type: "tool_use_end"}

			ch <- types.ChatDelta{Type: "tool_use_start", ID: "t2", Name: "Read"}
			ch <- types.ChatDelta{Type: "tool_use_delta", PartialJSON: `{"file_path":"/tmp/test.go"}`}
			ch <- types.ChatDelta{Type: "tool_use_end"}

			ch <- types.ChatDelta{Type: "tool_use_start", ID: "t3", Name: "Bash"}
			ch <- types.ChatDelta{Type: "tool_use_delta", PartialJSON: `{"command":"echo hello"}`}
			ch <- types.ChatDelta{Type: "tool_use_end"}

			ch <- types.ChatDelta{Type: "message_stop", StopReason: "tool_use"}
		default:
			ch <- types.ChatDelta{Type: "text_delta", Text: "Done"}
			ch <- types.ChatDelta{Type: "message_stop", StopReason: "end_turn"}
		}
	}()
	return ch, nil
}

// ── Helper ────────────────────────────────────────────────────

func makeLoop(t *testing.T, provider types.LLMProvider, registry *tools.Registry, opts ...func(*Options)) *Loop {
	t.Helper()
	dir := t.TempDir()
	store := session.NewStore(dir)
	o := Options{
		Provider:     provider,
		Tools:        registry,
		Context:      types.ContextBundle{},
		CWD:          "/home/dean/greendale",
		SessionStore: store,
		SessionID:    "session-test",
		Model:        "claude-sonnet-4-5-20250929",
		MaxTurns:     100,
	}
	for _, fn := range opts {
		fn(&o)
	}
	return New(o)
}

func collectEvents(t *testing.T, l *Loop, prompt string) []types.OutboundEvent {
	t.Helper()
	var events []types.OutboundEvent
	var mu sync.Mutex
	emit := func(e types.OutboundEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}
	err := l.Send(context.Background(), prompt, emit)
	require.New(t).NoError(err)
	return events
}

// ── Tests ─────────────────────────────────────────────────────

func TestLoop_Send_TextResponse(t *testing.T) {
	r := require.New(t)

	dir := t.TempDir()
	store := session.NewStore(dir)
	provider := &MockTextProvider{}
	registry := tools.NewRegistry()

	loop := New(Options{
		Provider:     provider,
		Tools:        registry,
		Context:      types.ContextBundle{},
		CWD:          "/home/troy/greendale",
		SessionStore: store,
		SessionID:    "session-1",
		Model:        "claude-sonnet-4-5-20250929",
		MaxTurns:     10,
	})

	var events []types.OutboundEvent
	emit := func(e types.OutboundEvent) {
		events = append(events, e)
	}

	err := loop.Send(context.Background(), "Hello", emit)
	r.NoError(err)

	r.Greater(len(events), 2)

	var textContent string
	var hasDone bool
	for _, e := range events {
		switch e.Type {
		case "text":
			textContent += e.Content
		case "done":
			hasDone = true
		}
	}

	r.Equal("Hello from Greendale!", textContent)
	r.True(hasDone)

	r.Len(loop.history, 2)
	r.Equal("user", loop.history[0].Role)
	r.Equal("assistant", loop.history[1].Role)

	messages, err := store.Load(loop.HistoryID())
	r.NoError(err)
	r.Len(messages, 2)
}

func TestLoop_Send_ToolUse(t *testing.T) {
	r := require.New(t)

	dir := t.TempDir()
	store := session.NewStore(dir)
	provider := &MockToolProvider{}
	registry := tools.NewRegistry()

	registry.Register(types.ToolDefinition{
		Name:        "read",
		Description: "Read a file",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string"},
			},
		},
		Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
			return types.ToolResult{
				Content: []types.ToolResultContent{
					{Type: "text", Text: "File contents: Troy and Abed in the morning!"},
				},
			}, nil
		},
	})

	loop := New(Options{
		Provider:     provider,
		Tools:        registry,
		Context:      types.ContextBundle{},
		CWD:          "/home/abed/greendale",
		SessionStore: store,
		SessionID:    "session-2",
		Model:        "claude-sonnet-4-5-20250929",
		MaxTurns:     10,
	})

	var events []types.OutboundEvent
	emit := func(e types.OutboundEvent) {
		events = append(events, e)
	}

	err := loop.Send(context.Background(), "Read the file", emit)
	r.NoError(err)

	r.Greater(len(events), 2)

	var hasToolUse bool
	var hasDone bool
	for _, e := range events {
		switch e.Type {
		case "tool_use":
			hasToolUse = true
			r.Equal("read", e.ToolName)
		case "done":
			hasDone = true
		}
	}

	r.True(hasToolUse)
	r.True(hasDone)

	r.Len(loop.history, 4)
	r.Equal("user", loop.history[0].Role)
	r.Equal("assistant", loop.history[1].Role)
	r.Equal("tool_use", loop.history[1].Content[0].Type)
	r.Equal("user", loop.history[2].Role)
	r.Equal("tool_result", loop.history[2].Content[0].Type)
	r.Equal("assistant", loop.history[3].Role)
}

func TestLoop_MaxTurns(t *testing.T) {
	r := require.New(t)

	dir := t.TempDir()
	store := session.NewStore(dir)
	provider := &MockToolProvider{}
	registry := tools.NewRegistry()

	registry.Register(types.ToolDefinition{
		Name:        "read",
		Description: "Read a file",
		InputSchema: map[string]any{"type": "object"},
		Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
			return types.ToolResult{
				Content: []types.ToolResultContent{
					{Type: "text", Text: "Content"},
				},
			}, nil
		},
	})

	loop := New(Options{
		Provider:     provider,
		Tools:        registry,
		Context:      types.ContextBundle{},
		CWD:          "/home/dean/greendale",
		SessionStore: store,
		SessionID:    "session-3",
		Model:        "claude-sonnet-4-5-20250929",
		MaxTurns:     1,
	})

	var events []types.OutboundEvent
	emit := func(e types.OutboundEvent) {
		events = append(events, e)
	}

	err := loop.Send(context.Background(), "Test", emit)
	r.NoError(err)

	var hasError bool
	for _, e := range events {
		if e.Type == "error" {
			hasError = true
			r.Contains(e.Content, "Max turns")
		}
	}

	r.True(hasError)
}

func TestLoop_Resume(t *testing.T) {
	r := require.New(t)

	dir := t.TempDir()
	store := session.NewStore(dir)
	provider := &MockTextProvider{}
	registry := tools.NewRegistry()

	loop1 := New(Options{
		Provider:     provider,
		Tools:        registry,
		Context:      types.ContextBundle{},
		CWD:          "/home/shirley/greendale",
		SessionStore: store,
		SessionID:    "session-4",
		Model:        "claude-sonnet-4-5-20250929",
		MaxTurns:     10,
	})

	emit := func(e types.OutboundEvent) {}

	err := loop1.Send(context.Background(), "First message", emit)
	r.NoError(err)

	historyID := loop1.HistoryID()

	loop2 := New(Options{
		Provider:     provider,
		Tools:        registry,
		Context:      types.ContextBundle{},
		CWD:          "/home/shirley/greendale",
		SessionStore: store,
		SessionID:    "session-4",
		Model:        "claude-sonnet-4-5-20250929",
		MaxTurns:     10,
	})

	err = loop2.Resume(context.Background(), historyID, "Second message", emit)
	r.NoError(err)

	r.Greater(len(loop2.history), 2)

	messages, err := store.Load(historyID)
	r.NoError(err)
	r.Greater(len(messages), 3)
}

func TestLoop_UsageTracking(t *testing.T) {
	r := require.New(t)

	l := makeLoop(t, &MockUsageProvider{}, tools.NewRegistry())
	events := collectEvents(t, l, "What's streets ahead?")

	var usageEvents []types.OutboundEvent
	for _, e := range events {
		if e.Type == "usage" {
			usageEvents = append(usageEvents, e)
		}
	}

	r.GreaterOrEqual(len(usageEvents), 1)
	// Usage should include token data across events
	var totalInput, totalOutput int
	for _, e := range usageEvents {
		r.NotNil(e.Usage)
		totalInput += e.Usage.InputTokens
		totalOutput += e.Usage.OutputTokens
	}
	r.Greater(totalInput, 0)
	r.Greater(totalOutput, 0)
}

func TestLoop_RetryOnTransientError(t *testing.T) {
	r := require.New(t)

	provider := &MockRetryProvider{failCount: 2}
	fastRetry := retry.Policy{MaxRetries: 5, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}

	l := makeLoop(t, provider, tools.NewRegistry(), func(o *Options) {
		o.RetryPolicy = &fastRetry
	})

	events := collectEvents(t, l, "Pop pop!")

	// Should have retry events.
	var retryCount int
	var hasText bool
	for _, e := range events {
		switch e.Type {
		case "retry":
			retryCount++
		case "text":
			hasText = true
		}
	}

	r.Equal(2, retryCount) // 2 failures before success
	r.True(hasText)
	r.Equal(3, provider.callCount)
}

func TestLoop_ReadOnlyGatedExecution(t *testing.T) {
	r := require.New(t)

	// Track execution order to verify read-only tools run before mutating.
	var executionOrder []string
	var mu sync.Mutex

	registry := tools.NewRegistry()

	registry.Register(types.ToolDefinition{
		Name:     "Glob",
		ReadOnly: true,
		Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
			mu.Lock()
			executionOrder = append(executionOrder, "Glob")
			mu.Unlock()
			time.Sleep(10 * time.Millisecond) // simulate work
			return types.ToolResult{Content: []types.ToolResultContent{{Type: "text", Text: "*.go"}}}, nil
		},
	})

	registry.Register(types.ToolDefinition{
		Name:     "Read",
		ReadOnly: true,
		Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
			mu.Lock()
			executionOrder = append(executionOrder, "Read")
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			return types.ToolResult{Content: []types.ToolResultContent{{Type: "text", Text: "file contents"}}}, nil
		},
	})

	registry.Register(types.ToolDefinition{
		Name:     "Bash",
		ReadOnly: false,
		Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
			mu.Lock()
			executionOrder = append(executionOrder, "Bash")
			mu.Unlock()
			return types.ToolResult{Content: []types.ToolResultContent{{Type: "text", Text: "hello"}}}, nil
		},
	})

	l := makeLoop(t, &MockMultiToolProvider{}, registry)
	collectEvents(t, l, "Do some stuff")

	mu.Lock()
	defer mu.Unlock()

	// Bash (mutating) must come after both read-only tools.
	r.Len(executionOrder, 3)

	bashIdx := -1
	for i, name := range executionOrder {
		if name == "Bash" {
			bashIdx = i
		}
	}
	r.Equal(2, bashIdx, "Bash should execute after both read-only tools, got order: %v", executionOrder)
}

// MockRepeatToolProvider returns tool_use N times before a final text response.
type MockRepeatToolProvider struct {
	repeatCount int
	callCount   int
	mu          sync.Mutex
}

func (m *MockRepeatToolProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	m.mu.Lock()
	m.callCount++
	count := m.callCount
	m.mu.Unlock()

	ch := make(chan types.ChatDelta, 8)
	go func() {
		defer close(ch)
		if count <= m.repeatCount {
			ch <- types.ChatDelta{Type: "tool_use_start", ID: fmt.Sprintf("t-%d", count), Name: "read"}
			ch <- types.ChatDelta{Type: "tool_use_delta", PartialJSON: `{"file_path":"/tmp/test.txt"}`}
			ch <- types.ChatDelta{Type: "tool_use_end"}
			ch <- types.ChatDelta{Type: "message_stop", StopReason: "tool_use"}
		} else {
			ch <- types.ChatDelta{Type: "text_delta", Text: "All done"}
			ch <- types.ChatDelta{Type: "message_stop", StopReason: "end_turn"}
		}
	}()
	return ch, nil
}

func TestLoop_CompactLargeHistory(t *testing.T) {
	r := require.New(t)

	// Tiny budget so compaction triggers after a few tool rounds.
	// System prompt (~150 tok) + tool schema (~60 tok) = ~210 overhead.
	// Threshold = 800 - 200 - 100 = 500.
	// Each tool round adds ~300 tokens (tool_use + large tool_result).
	// After 2 rounds (~600 tok history), we exceed 500 and compact.
	tinyBudget := tokens.Budget{
		ContextWindow: 800,
		OutputReserve: 200,
		Buffer:        100,
	}

	// Use enough rounds for history to grow past the threshold.
	provider := &MockRepeatToolProvider{repeatCount: 6}
	registry := tools.NewRegistry()
	registry.Register(types.ToolDefinition{
		Name:     "read",
		ReadOnly: true,
		Handler: func(map[string]any, types.ToolContext) (types.ToolResult, error) {
			// ~250 tokens of tool result to grow history fast.
			return types.ToolResult{
				Content: []types.ToolResultContent{{Type: "text", Text: strings.Repeat("Greendale Community College is the finest institution ", 20)}},
			}, nil
		},
	})

	l := makeLoop(t, provider, registry, func(o *Options) {
		o.Budget = &tinyBudget
	})

	events := collectEvents(t, l, strings.Repeat("Read the files at Greendale ", 5))

	var compactEvents int
	for _, e := range events {
		if e.Type == "compact" {
			compactEvents++
		}
	}

	r.GreaterOrEqual(compactEvents, 1)
}

// ── Stream error mock providers ──────────────────────────────

// MockStreamErrorProvider sends error deltas through the channel (like the real SDK).
// Fails failCount times via channel error, then succeeds.
type MockStreamErrorProvider struct {
	failCount  int
	errorText  string
	statusCode int
	callCount  int
	mu         sync.Mutex
}

func (m *MockStreamErrorProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	m.mu.Lock()
	m.callCount++
	count := m.callCount
	m.mu.Unlock()

	ch := make(chan types.ChatDelta, 4)
	go func() {
		defer close(ch)
		if count <= m.failCount {
			ch <- types.ChatDelta{
				Type:       "error",
				Text:       m.errorText,
				StatusCode: m.statusCode,
			}
			return
		}
		ch <- types.ChatDelta{Type: "text_delta", Text: "Cool. Cool cool cool."}
		ch <- types.ChatDelta{Type: "message_stop", StopReason: "end_turn"}
	}()
	return ch, nil
}

// MockAlwaysStreamErrorProvider always sends the same error delta. Never succeeds.
type MockAlwaysStreamErrorProvider struct {
	errorText  string
	statusCode int
}

func (m *MockAlwaysStreamErrorProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	ch := make(chan types.ChatDelta, 1)
	go func() {
		defer close(ch)
		ch <- types.ChatDelta{
			Type:       "error",
			Text:       m.errorText,
			StatusCode: m.statusCode,
		}
	}()
	return ch, nil
}

// ── Stream error tests ──────────────────────────────────────

func TestLoop_StreamError_PromptTooLong_Compacts(t *testing.T) {
	r := require.New(t)

	// First call: prompt-too-long error via channel. Second call: success.
	provider := &MockStreamErrorProvider{
		failCount:  1,
		errorText:  "prompt is too long: 250000 tokens > 200000",
		statusCode: 400,
	}

	// Need some history so compact has something to remove.
	registry := tools.NewRegistry()
	l := makeLoop(t, provider, registry, func(o *Options) {
		fastRetry := retry.Policy{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
		o.RetryPolicy = &fastRetry
	})

	// Seed history with several messages so compact can drop some.
	for i := 0; i < 10; i++ {
		l.history = append(l.history,
			types.ChatMessage{Role: "user", Content: []types.ChatContentBlock{{Type: "text", Text: strings.Repeat("Señor Chang teaches Spanish at Greendale ", 20)}}},
			types.ChatMessage{Role: "assistant", Content: []types.ChatContentBlock{{Type: "text", Text: strings.Repeat("That's a normal amount of Spanish ", 20)}}},
		)
	}

	var events []types.OutboundEvent
	var mu sync.Mutex
	emit := func(e types.OutboundEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}

	err := l.Send(context.Background(), "Tell me about Greendale", emit)
	r.NoError(err)

	var compactCount int
	var hasText bool
	for _, e := range events {
		switch e.Type {
		case "compact":
			compactCount++
		case "text":
			hasText = true
		}
	}

	r.GreaterOrEqual(compactCount, 1, "should have compacted on prompt-too-long")
	r.True(hasText, "should have succeeded after compaction")
	r.Equal(2, provider.callCount, "should have called provider twice (fail + success)")
}

func TestLoop_StreamError_Retryable_Retries(t *testing.T) {
	r := require.New(t)

	provider := &MockStreamErrorProvider{
		failCount:  2,
		errorText:  "529: overloaded",
		statusCode: 529,
	}

	fastRetry := retry.Policy{MaxRetries: 5, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	l := makeLoop(t, provider, tools.NewRegistry(), func(o *Options) {
		o.RetryPolicy = &fastRetry
	})

	var events []types.OutboundEvent
	var mu sync.Mutex
	emit := func(e types.OutboundEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}

	err := l.Send(context.Background(), "Pop pop!", emit)
	r.NoError(err)

	var retryCount int
	var hasText bool
	for _, e := range events {
		switch e.Type {
		case "retry":
			retryCount++
		case "text":
			hasText = true
		}
	}

	r.Equal(2, retryCount, "should have retried twice")
	r.True(hasText, "should have succeeded after retries")
	r.Equal(3, provider.callCount, "2 failures + 1 success")
}

func TestLoop_StreamError_Fatal_Returns(t *testing.T) {
	r := require.New(t)

	provider := &MockAlwaysStreamErrorProvider{
		errorText:  "invalid api key",
		statusCode: 401,
	}

	l := makeLoop(t, provider, tools.NewRegistry())

	emit := func(e types.OutboundEvent) {}
	err := l.Send(context.Background(), "Should fail immediately", emit)

	r.Error(err)
	r.Contains(err.Error(), "invalid api key")
}

func TestLoop_StreamError_MaxRetries(t *testing.T) {
	r := require.New(t)

	provider := &MockAlwaysStreamErrorProvider{
		errorText:  "529: overloaded",
		statusCode: 529,
	}

	fastRetry := retry.Policy{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	l := makeLoop(t, provider, tools.NewRegistry(), func(o *Options) {
		o.RetryPolicy = &fastRetry
	})

	var events []types.OutboundEvent
	var mu sync.Mutex
	emit := func(e types.OutboundEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}

	err := l.Send(context.Background(), "Never gonna work", emit)
	r.Error(err)
	r.Contains(err.Error(), "stream error after 3 retries")

	var retryCount int
	for _, e := range events {
		if e.Type == "retry" {
			retryCount++
		}
	}
	r.Equal(3, retryCount, "should have retried max times before bailing")
}

func TestLoop_StreamError_MaxCompact(t *testing.T) {
	r := require.New(t)

	// Always returns prompt-too-long — compaction can't help because history is tiny.
	provider := &MockAlwaysStreamErrorProvider{
		errorText:  "prompt is too long: 250000 tokens > 200000",
		statusCode: 400,
	}

	l := makeLoop(t, provider, tools.NewRegistry())

	emit := func(e types.OutboundEvent) {}
	err := l.Send(context.Background(), "This will never fit", emit)

	r.Error(err)
	r.Contains(err.Error(), "prompt too long after 3 compaction attempts")
}

func TestLoop_OnComplete_CalledAfterToolUse(t *testing.T) {
	r := require.New(t)

	var callbackHistory []types.ChatMessage
	registry := tools.NewRegistry()
	registry.Register(types.ToolDefinition{
		Name:     "read",
		ReadOnly: true,
		Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
			return types.ToolResult{
				Content: []types.ToolResultContent{{Type: "text", Text: "Cool cool cool"}},
			}, nil
		},
	})

	l := makeLoop(t, &MockToolProvider{}, registry, func(o *Options) {
		o.OnComplete = func(history []types.ChatMessage) {
			callbackHistory = make([]types.ChatMessage, len(history))
			copy(callbackHistory, history)
		}
	})

	emit := func(e types.OutboundEvent) {}
	err := l.Send(context.Background(), "Read something for Abed", emit)
	r.NoError(err)

	r.NotEmpty(callbackHistory, "OnComplete should have been called")
	r.Equal("user", callbackHistory[0].Role)
}

func TestLoop_OnComplete_NotCalledWithoutToolUse(t *testing.T) {
	r := require.New(t)

	called := false
	l := makeLoop(t, &MockTextProvider{}, tools.NewRegistry(), func(o *Options) {
		o.OnComplete = func(history []types.ChatMessage) {
			called = true
		}
	})

	emit := func(e types.OutboundEvent) {}
	err := l.Send(context.Background(), "Just a chat, no tools", emit)
	r.NoError(err)

	r.False(called, "OnComplete should NOT fire when no tools were used")
}

func TestLoop_OnComplete_PanicRecovery(t *testing.T) {
	r := require.New(t)

	registry := tools.NewRegistry()
	registry.Register(types.ToolDefinition{
		Name:     "read",
		ReadOnly: true,
		Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
			return types.ToolResult{
				Content: []types.ToolResultContent{{Type: "text", Text: "data"}},
			}, nil
		},
	})

	l := makeLoop(t, &MockToolProvider{}, registry, func(o *Options) {
		o.OnComplete = func(history []types.ChatMessage) {
			panic("Senor Chang lost it again")
		}
	})

	emit := func(e types.OutboundEvent) {}
	// Should not panic — fireOnComplete recovers.
	err := l.Send(context.Background(), "Trigger a tool call", emit)
	r.NoError(err)
}
