package compact

import (
	"context"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

type mockProvider struct {
	response string
}

func (m *mockProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	ch := make(chan types.ChatDelta, 1)
	ch <- types.ChatDelta{
		Type: "text_delta",
		Text: m.response,
	}
	close(ch)
	return ch, nil
}

func TestEngine_ShouldCompact(t *testing.T) {
	r := require.New(t)
	
	provider := &mockProvider{response: "Summary"}
	engine := NewEngine(DefaultConfig(provider))
	
	r.False(engine.ShouldCompact(50_000))
	r.True(engine.ShouldCompact(100_000))
	r.True(engine.ShouldCompact(150_000))
}

func TestEngine_Compact_BelowThreshold(t *testing.T) {
	r := require.New(t)
	
	provider := &mockProvider{response: "Summary"}
	engine := NewEngine(DefaultConfig(provider))
	
	messages := []types.ChatMessage{
		{Role: "user", Content: []types.ChatContentBlock{{Type: "text", Text: "Hi"}}},
	}
	
	result, err := engine.Compact(context.Background(), messages)
	r.NoError(err)
	r.Equal(messages, result)
}

func TestEngine_Compact_CreatesCompactBoundary(t *testing.T) {
	r := require.New(t)
	
	provider := &mockProvider{response: "This is a summary."}
	
	config := DefaultConfig(provider)
	config.TokenThreshold = 100
	engine := NewEngine(config)
	
	var messages []types.ChatMessage
	for i := 0; i < 20; i++ {
		messages = append(messages, types.ChatMessage{
			Role: "user",
			Content: []types.ChatContentBlock{
				{Type: "text", Text: "Long message with lots of text to increase token count"},
			},
		})
	}
	
	result, err := engine.Compact(context.Background(), messages)
	r.NoError(err)
	r.Greater(len(result), 0)
	r.Contains(result[0].Content[0].Text, "[Conversation summary")
	r.Contains(result[0].Content[0].Text, "This is a summary")
}
