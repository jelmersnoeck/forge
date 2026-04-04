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

// countCacheBreakpoints counts all cache_control blocks in built request params.
func countCacheBreakpoints(params anthropic.MessageNewParams) int {
	count := 0

	for _, block := range params.System {
		if block.CacheControl.Type != "" {
			count++
		}
	}

	for _, tool := range params.Tools {
		if cc := tool.GetCacheControl(); cc != nil && cc.Type != "" {
			count++
		}
	}

	for _, msg := range params.Messages {
		for _, block := range msg.Content {
			if cc := block.GetCacheControl(); cc != nil && cc.Type != "" {
				count++
			}
		}
	}

	return count
}

func TestBuildRequest_CacheBreakpointLimit(t *testing.T) {
	cc := &types.CacheControl{Type: "ephemeral", TTL: "1h"}

	tests := map[string]struct {
		req       types.ChatRequest
		wantMax   int // upper bound on breakpoints (always <= 4)
		wantExact int // exact expected count (-1 to skip exact check)
	}{
		"normal case 2 system + 1 tool + 1 message": {
			req: types.ChatRequest{
				Model: "claude-sonnet-4-5-20250929",
				System: []types.SystemBlock{
					{Type: "text", Text: "Greendale Community College rules", CacheControl: cc},
					{Type: "text", Text: "Senor Chang's teachings", CacheControl: cc},
				},
				Tools: []types.ToolSchema{
					{Name: "Read", Description: "Read", InputSchema: map[string]any{"type": "object"}, CacheControl: cc},
				},
				Messages: []types.ChatMessage{
					{Role: "user", Content: []types.ChatContentBlock{
						{Type: "text", Text: "Tell me about Troy Barnes", CacheControl: cc},
					}},
				},
				MaxTokens: 1024,
			},
			wantMax:   4,
			wantExact: 4,
		},
		"overflow: 6 tagged blocks capped to 4": {
			req: types.ChatRequest{
				Model: "claude-sonnet-4-5-20250929",
				System: []types.SystemBlock{
					{Type: "text", Text: "block 1", CacheControl: cc},
					{Type: "text", Text: "block 2", CacheControl: cc},
					{Type: "text", Text: "block 3", CacheControl: cc},
				},
				Tools: []types.ToolSchema{
					{Name: "Read", Description: "Read", InputSchema: map[string]any{"type": "object"}, CacheControl: cc},
					{Name: "Write", Description: "Write", InputSchema: map[string]any{"type": "object"}, CacheControl: cc},
				},
				Messages: []types.ChatMessage{
					{Role: "user", Content: []types.ChatContentBlock{
						{Type: "text", Text: "Human Being mascot sighting", CacheControl: cc},
					}},
				},
				MaxTokens: 1024,
			},
			wantMax:   4,
			wantExact: 4,
		},
		"priority: system and tools before messages": {
			req: types.ChatRequest{
				Model: "claude-sonnet-4-5-20250929",
				System: []types.SystemBlock{
					{Type: "text", Text: "sys1", CacheControl: cc},
					{Type: "text", Text: "sys2", CacheControl: cc},
				},
				Tools: []types.ToolSchema{
					{Name: "Read", Description: "Read", InputSchema: map[string]any{"type": "object"}, CacheControl: cc},
					{Name: "Write", Description: "Write", InputSchema: map[string]any{"type": "object"}, CacheControl: cc},
				},
				Messages: []types.ChatMessage{
					{Role: "user", Content: []types.ChatContentBlock{
						{Type: "text", Text: "this should NOT get cache_control", CacheControl: cc},
					}},
				},
				MaxTokens: 1024,
			},
			wantMax:   4,
			wantExact: 4, // 2 system + 2 tools = 4, message gets nothing
		},
		"no cache control at all": {
			req: types.ChatRequest{
				Model: "claude-sonnet-4-5-20250929",
				System: []types.SystemBlock{
					{Type: "text", Text: "plain"},
				},
				Messages: []types.ChatMessage{
					{Role: "user", Content: []types.ChatContentBlock{
						{Type: "text", Text: "no caching here"},
					}},
				},
				MaxTokens: 1024,
			},
			wantMax:   4,
			wantExact: 0,
		},
		"tool_result and tool_use get cache control": {
			req: types.ChatRequest{
				Model: "claude-sonnet-4-5-20250929",
				System: []types.SystemBlock{
					{Type: "text", Text: "sys", CacheControl: cc},
				},
				Tools: []types.ToolSchema{
					{Name: "Read", Description: "Read", InputSchema: map[string]any{"type": "object"}, CacheControl: cc},
				},
				Messages: []types.ChatMessage{
					{Role: "assistant", Content: []types.ChatContentBlock{
						{Type: "tool_use", ID: "tu_01", Name: "Read", Input: map[string]any{"file_path": "/paintball.txt"}, CacheControl: cc},
					}},
					{Role: "user", Content: []types.ChatContentBlock{
						{Type: "tool_result", ToolUseID: "tu_01", Content: []types.ToolResultContent{{Type: "text", Text: "streets ahead"}}, CacheControl: cc},
					}},
				},
				MaxTokens: 1024,
			},
			wantMax:   4,
			wantExact: 4, // 1 system + 1 tool + 1 tool_use + 1 tool_result
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			params, err := buildRequest(tc.req)
			r.NoError(err)

			count := countCacheBreakpoints(params)
			r.LessOrEqual(count, tc.wantMax, "exceeded max cache breakpoints")
			if tc.wantExact >= 0 {
				r.Equal(tc.wantExact, count, "unexpected cache breakpoint count")
			}
		})
	}
}

func TestBuildRequest_PriorityOrder(t *testing.T) {
	// Verify system blocks get priority over tools, tools over messages.
	// With 3 system blocks tagged, only 1 slot left for tools, 0 for messages.
	r := require.New(t)
	cc := &types.CacheControl{Type: "ephemeral", TTL: "1h"}

	params, err := buildRequest(types.ChatRequest{
		Model: "claude-sonnet-4-5-20250929",
		System: []types.SystemBlock{
			{Type: "text", Text: "s1", CacheControl: cc},
			{Type: "text", Text: "s2", CacheControl: cc},
			{Type: "text", Text: "s3", CacheControl: cc},
		},
		Tools: []types.ToolSchema{
			{Name: "Read", Description: "Read", InputSchema: map[string]any{"type": "object"}, CacheControl: cc},
			{Name: "Write", Description: "Write", InputSchema: map[string]any{"type": "object"}, CacheControl: cc},
		},
		Messages: []types.ChatMessage{
			{Role: "user", Content: []types.ChatContentBlock{
				{Type: "text", Text: "Abed Nadir", CacheControl: cc},
			}},
		},
		MaxTokens: 1024,
	})
	r.NoError(err)

	// All 3 system blocks should have cache_control
	for i, block := range params.System {
		r.NotEmpty(string(block.CacheControl.Type), "system block %d should have cache_control", i)
	}

	// First tool should have it (slot 4), second should not
	cc1 := params.Tools[0].GetCacheControl()
	r.NotNil(cc1, "first tool should have cache_control")
	r.NotEmpty(string(cc1.Type))

	cc2 := params.Tools[1].GetCacheControl()
	hasCacheControl := cc2 != nil && cc2.Type != ""
	r.False(hasCacheControl, "second tool should NOT have cache_control (budget exhausted)")

	// Message should definitely not have it
	for _, msg := range params.Messages {
		for _, block := range msg.Content {
			msgCC := block.GetCacheControl()
			hasMsgCC := msgCC != nil && msgCC.Type != ""
			r.False(hasMsgCC, "message block should NOT have cache_control (budget exhausted)")
		}
	}

	r.LessOrEqual(countCacheBreakpoints(params), maxCacheBreakpoints)
}

func TestAnthropicProvider_Chat(t *testing.T) {
	r := require.New(t)

	p := NewAnthropic("test-key")
	r.NotNil(p)

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
}
