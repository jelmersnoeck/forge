package loop

import (
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAddMessageCacheControl(t *testing.T) {
	tests := map[string]struct {
		input []types.ChatMessage
		check func(*require.Assertions, []types.ChatMessage)
	}{
		"empty messages": {
			input: []types.ChatMessage{},
			check: func(r *require.Assertions, result []types.ChatMessage) {
				r.Empty(result)
			},
		},
		"single user message falls back to last": {
			input: []types.ChatMessage{
				{
					Role: "user",
					Content: []types.ChatContentBlock{
						{Type: "text", Text: "Hello"},
					},
				},
			},
			check: func(r *require.Assertions, result []types.ChatMessage) {
				r.Len(result, 1)
				r.Len(result[0].Content, 1)

				lastBlock := result[0].Content[0]
				r.NotNil(lastBlock.CacheControl)
				r.Equal("ephemeral", lastBlock.CacheControl.Type)
				r.Equal("1h", lastBlock.CacheControl.TTL)
			},
		},
		"two user messages tags the first user message": {
			// user → assistant → user: breakpoint on first user (second-to-last user msg)
			input: []types.ChatMessage{
				{
					Role:    "user",
					Content: []types.ChatContentBlock{{Type: "text", Text: "First"}},
				},
				{
					Role:    "assistant",
					Content: []types.ChatContentBlock{{Type: "text", Text: "Reply"}},
				},
				{
					Role:    "user",
					Content: []types.ChatContentBlock{{Type: "text", Text: "Second"}},
				},
			},
			check: func(r *require.Assertions, result []types.ChatMessage) {
				r.Len(result, 3)

				// First user message (second-to-last user) gets the breakpoint
				r.NotNil(result[0].Content[0].CacheControl, "first user msg should have cache control")
				r.Equal("ephemeral", result[0].Content[0].CacheControl.Type)
				r.Equal("1h", result[0].Content[0].CacheControl.TTL)

				// Assistant and last user message should NOT
				r.Nil(result[1].Content[0].CacheControl, "assistant msg should not have cache control")
				r.Nil(result[2].Content[0].CacheControl, "last user msg should not have cache control")
			},
		},
		"agentic loop: user + assistant + tool_results": {
			// Typical tool-use turn: user, assistant(tool_use), user(tool_results)
			// Only 1 user msg in the "old" part — falls back to last message.
			input: []types.ChatMessage{
				{
					Role:    "user",
					Content: []types.ChatContentBlock{{Type: "text", Text: "Do something"}},
				},
				{
					Role: "assistant",
					Content: []types.ChatContentBlock{
						{Type: "tool_use", ID: "tu_01", Name: "Read", Input: map[string]any{"file_path": "/paintball.txt"}},
					},
				},
				{
					Role: "user",
					Content: []types.ChatContentBlock{
						{Type: "tool_result", ToolUseID: "tu_01", Content: []types.ToolResultContent{{Type: "text", Text: "streets ahead"}}},
					},
				},
			},
			check: func(r *require.Assertions, result []types.ChatMessage) {
				r.Len(result, 3)

				// Two user messages → breakpoint on first user msg
				r.NotNil(result[0].Content[0].CacheControl, "first user msg should get breakpoint")
				r.Nil(result[1].Content[0].CacheControl, "assistant msg should not")
				r.Nil(result[2].Content[0].CacheControl, "tool_result msg should not")
			},
		},
		"multi-turn conversation caches completed turns": {
			// user → assistant → user(tool_results) → assistant → user(new question)
			// Three user messages: breakpoint on the second-to-last = tool_results msg
			input: []types.ChatMessage{
				{Role: "user", Content: []types.ChatContentBlock{{Type: "text", Text: "Turn 1"}}},
				{Role: "assistant", Content: []types.ChatContentBlock{{Type: "tool_use", ID: "t1", Name: "Read"}}},
				{Role: "user", Content: []types.ChatContentBlock{{Type: "tool_result", ToolUseID: "t1"}}},
				{Role: "assistant", Content: []types.ChatContentBlock{{Type: "text", Text: "Done"}}},
				{Role: "user", Content: []types.ChatContentBlock{{Type: "text", Text: "Turn 2"}}},
			},
			check: func(r *require.Assertions, result []types.ChatMessage) {
				r.Len(result, 5)

				// Second-to-last user message is result[2] (tool_result)
				r.Nil(result[0].Content[0].CacheControl, "first user msg")
				r.Nil(result[1].Content[0].CacheControl, "first assistant msg")
				r.NotNil(result[2].Content[0].CacheControl, "tool_result user msg should get breakpoint")
				r.Nil(result[3].Content[0].CacheControl, "second assistant msg")
				r.Nil(result[4].Content[0].CacheControl, "last user msg should NOT get breakpoint")
			},
		},
		"target message with multiple blocks tags last block": {
			input: []types.ChatMessage{
				{
					Role: "user",
					Content: []types.ChatContentBlock{
						{Type: "text", Text: "Hello"},
						{Type: "text", Text: "World"},
						{Type: "text", Text: "!"},
					},
				},
			},
			check: func(r *require.Assertions, result []types.ChatMessage) {
				r.Len(result, 1)
				r.Len(result[0].Content, 3)

				// Falls back to last message (only 1 user msg). Tags last block.
				r.Nil(result[0].Content[0].CacheControl, "first block should not have cache control")
				r.Nil(result[0].Content[1].CacheControl, "second block should not have cache control")
				r.NotNil(result[0].Content[2].CacheControl, "last block should have cache control")

				lastBlock := result[0].Content[2]
				r.Equal("ephemeral", lastBlock.CacheControl.Type)
				r.Equal("1h", lastBlock.CacheControl.TTL)
			},
		},
		"does not mutate original": {
			input: []types.ChatMessage{
				{
					Role: "user",
					Content: []types.ChatContentBlock{
						{Type: "text", Text: "Original"},
					},
				},
			},
			check: func(r *require.Assertions, result []types.ChatMessage) {
				r.NotNil(result[0].Content[0].CacheControl, "result should have cache control added")
			},
		},
		"assistant-only messages are ignored for user counting": {
			// assistant → user: only 1 user msg, falls back to last
			input: []types.ChatMessage{
				{Role: "assistant", Content: []types.ChatContentBlock{{Type: "text", Text: "Hi"}}},
				{Role: "user", Content: []types.ChatContentBlock{{Type: "text", Text: "Hey"}}},
			},
			check: func(r *require.Assertions, result []types.ChatMessage) {
				r.Len(result, 2)
				r.Nil(result[0].Content[0].CacheControl, "assistant should not get breakpoint")
				r.NotNil(result[1].Content[0].CacheControl, "only user msg falls back to last")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			// Deep copy input for mutation check
			original := make([]types.ChatMessage, len(tc.input))
			for i, msg := range tc.input {
				original[i] = types.ChatMessage{
					Role:    msg.Role,
					Content: make([]types.ChatContentBlock, len(msg.Content)),
				}
				copy(original[i].Content, msg.Content)
			}

			result := addMessageCacheControl(tc.input)

			tc.check(r, result)

			// Verify original was not mutated
			if name != "does not mutate original" {
				for i, msg := range tc.input {
					for j, block := range msg.Content {
						r.Nil(block.CacheControl, "original message[%d].content[%d] should not be mutated", i, j)
					}
				}
			}
		})
	}
}
