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
		"single message with one block": {
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
		"multiple messages": {
			input: []types.ChatMessage{
				{
					Role: "user",
					Content: []types.ChatContentBlock{
						{Type: "text", Text: "First"},
					},
				},
				{
					Role: "assistant",
					Content: []types.ChatContentBlock{
						{Type: "text", Text: "Second"},
					},
				},
				{
					Role: "user",
					Content: []types.ChatContentBlock{
						{Type: "text", Text: "Third"},
					},
				},
			},
			check: func(r *require.Assertions, result []types.ChatMessage) {
				r.Len(result, 3)

				// Only last message should have cache control
				r.Nil(result[0].Content[0].CacheControl, "first message should not have cache control")
				r.Nil(result[1].Content[0].CacheControl, "second message should not have cache control")
				r.NotNil(result[2].Content[0].CacheControl, "last message should have cache control")

				lastBlock := result[2].Content[0]
				r.Equal("ephemeral", lastBlock.CacheControl.Type)
				r.Equal("1h", lastBlock.CacheControl.TTL)
			},
		},
		"last message with multiple blocks": {
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

				// Only the last block should have cache control
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
				// Result should have cache control
				r.NotNil(result[0].Content[0].CacheControl, "result should have cache control added")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			// Make a deep copy of input for mutation check
			original := make([]types.ChatMessage, len(tc.input))
			for i, msg := range tc.input {
				original[i] = types.ChatMessage{
					Role:    msg.Role,
					Content: make([]types.ChatContentBlock, len(msg.Content)),
				}
				copy(original[i].Content, msg.Content)
			}

			result := addMessageCacheControl(tc.input)

			// Run the test check
			tc.check(r, result)

			// Verify original was not mutated (except for the "does not mutate" test which expects mutation)
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
