package provider

import (
	"context"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestNewAnthropic(t *testing.T) {
	r := require.New(t)

	p := NewAnthropic("test-key")
	r.NotNil(p)
	r.NotNil(p.client)
}

func TestAnthropicProvider_Chat(t *testing.T) {
	r := require.New(t)

	p := NewAnthropic("test-key")
	r.NotNil(p)

	// Just verify the method is callable and returns a channel
	// We won't actually consume from the channel since we don't have a valid API key
	ctx := context.Background()
	req := types.ChatRequest{
		Model: "claude-sonnet-4-5-20250929",
		System: []types.SystemBlock{
			{Type: "text", Text: "You are a helpful assistant."},
		},
		Messages: []types.ChatMessage{
			{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: "Hello"},
				},
			},
		},
		MaxTokens: 1024,
		Stream:    true,
	}

	ch, err := p.Chat(ctx, req)
	r.NotNil(ch)
	r.NoError(err)
	// Don't consume from the channel as it will try to hit the real API
}
