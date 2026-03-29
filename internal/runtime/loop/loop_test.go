package loop

import (
	"context"
	"sync"
	"testing"

	"github.com/jelmersnoeck/forge/internal/runtime/session"
	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

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
			// First call: return tool use
			ch <- types.ChatDelta{Type: "tool_use_start", ID: "tool-1", Name: "read"}
			ch <- types.ChatDelta{Type: "tool_use_delta", PartialJSON: `{"file_path":"/tmp/test.txt"}`}
			ch <- types.ChatDelta{Type: "tool_use_end"}
			ch <- types.ChatDelta{Type: "message_stop", StopReason: "tool_use"}

		default:
			// Second call: return text response
			ch <- types.ChatDelta{Type: "text_delta", Text: "File read successfully"}
			ch <- types.ChatDelta{Type: "message_stop", StopReason: "end_turn"}
		}
	}()

	return ch, nil
}

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

	// Should have text events and a done event
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

	// Verify history
	r.Len(loop.history, 2)
	r.Equal("user", loop.history[0].Role)
	r.Equal("assistant", loop.history[1].Role)

	// Verify session was persisted
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

	// Register a mock read tool
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

	// Should have tool_use event, text event, and done event
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

	// Verify history: user message, assistant tool_use, tool_result, assistant response
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

	// Provider that always returns tool use
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
		MaxTurns:     1, // Only allow 1 turn
	})

	var events []types.OutboundEvent
	emit := func(e types.OutboundEvent) {
		events = append(events, e)
	}

	err := loop.Send(context.Background(), "Test", emit)
	r.NoError(err)

	// Should have error about max turns
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

	// First conversation
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

	// Resume with new loop
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

	// Should have original history plus new messages
	r.Greater(len(loop2.history), 2)

	// Session should have all messages
	messages, err := store.Load(historyID)
	r.NoError(err)
	r.Greater(len(messages), 3)
}
