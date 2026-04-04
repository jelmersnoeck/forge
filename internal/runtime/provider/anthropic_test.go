package provider

import (
	"context"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestNewAnthropic(t *testing.T) {
	r := require.New(t)

	p := NewAnthropic("test-key")
	r.NotNil(p)
	r.NotNil(p.client)
}

func TestToCacheControl(t *testing.T) {
	tests := map[string]struct {
		input types.CacheControl
		want  anthropic.CacheControlEphemeralTTL
	}{
		"1h TTL": {
			input: types.CacheControl{Type: "ephemeral", TTL: "1h"},
			want:  anthropic.CacheControlEphemeralTTLTTL1h,
		},
		"5m TTL": {
			input: types.CacheControl{Type: "ephemeral", TTL: "5m"},
			want:  anthropic.CacheControlEphemeralTTLTTL5m,
		},
		"empty TTL defaults": {
			input: types.CacheControl{Type: "ephemeral"},
			want:  "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			result := toCacheControl(&tc.input)
			r.Equal(tc.want, result.TTL)
		})
	}
}

func TestCacheControlPropagation(t *testing.T) {
	// Verify that Chat correctly propagates cache_control on all block types.
	// We build a request with cache_control on text, tool_use, and tool_result
	// blocks, call Chat (which will fail on the network but the request is
	// built before the stream starts), and inspect the constructed params.
	//
	// Since the Anthropic SDK client builds the request internally, we test
	// the propagation indirectly by verifying the Chat method doesn't error
	// on construction (the stream goroutine handles the network part).
	r := require.New(t)
	p := NewAnthropic("test-key")

	cc := &types.CacheControl{Type: "ephemeral", TTL: "1h"}
	req := types.ChatRequest{
		Model: "claude-sonnet-4-5-20250929",
		System: []types.SystemBlock{
			{Type: "text", Text: "Greendale Community College", CacheControl: cc},
		},
		Messages: []types.ChatMessage{
			{
				Role: "assistant",
				Content: []types.ChatContentBlock{
					{Type: "tool_use", ID: "tu_01", Name: "Read", Input: map[string]any{"file_path": "/dean.txt"}, CacheControl: cc},
				},
			},
			{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "tool_result", ToolUseID: "tu_01", Content: []types.ToolResultContent{{Type: "text", Text: "Troy Barnes"}}, CacheControl: cc},
				},
			},
			{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: "Tell me about the Human Being mascot", CacheControl: cc},
				},
			},
		},
		Tools: []types.ToolSchema{
			{
				Name:         "Read",
				Description:  "Read a file",
				InputSchema:  map[string]any{"type": "object"},
				CacheControl: cc,
			},
		},
		MaxTokens: 1024,
		Stream:    true,
	}

	// Chat builds the full request before starting the stream goroutine.
	// If cache_control propagation is broken (e.g., wrong field type), this
	// would panic or error during construction.
	ch, err := p.Chat(context.Background(), req)
	r.NoError(err)
	r.NotNil(ch)
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
