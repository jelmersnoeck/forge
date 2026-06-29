package tools

import (
	"context"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWebSearchTool(t *testing.T) {
	tool := WebSearchTool()

	require.Equal(t, "WebSearch", tool.Name)
	require.NotEmpty(t, tool.Description)
	require.True(t, tool.ReadOnly)
	require.False(t, tool.Destructive)
}

func TestWebSearchHandler_Validation(t *testing.T) {
	tool := WebSearchTool()
	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: t.TempDir(),
	}

	tests := map[string]struct {
		input   map[string]any
		wantErr bool
	}{
		"missing query": {
			input:   map[string]any{},
			wantErr: true,
		},
		"empty query": {
			input:   map[string]any{"query": ""},
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			_, err := tool.Handler(tc.input, ctx)
			if tc.wantErr {
				r.Error(err)
			}
		})
	}
}

func TestWebSearchHandler_NoAPIKey(t *testing.T) {
	r := require.New(t)
	tool := WebSearchTool()
	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: t.TempDir(),
	}

	// Ensure ANTHROPIC_API_KEY is unset for this test
	t.Setenv("ANTHROPIC_API_KEY", "")

	result, err := tool.Handler(map[string]any{"query": "Greendale Community College"}, ctx)
	r.NoError(err) // handler returns error in result, not as Go error
	r.True(result.IsError)
	// Provider-agnostic message: web search is unavailable, not a hard
	// "requires ANTHROPIC_API_KEY" failure, but still names the key that
	// would enable the Anthropic backend.
	r.Contains(result.Content[0].Text, "unavailable for the active provider")
	r.Contains(result.Content[0].Text, "ANTHROPIC_API_KEY")
}

func TestFormatSearchResponse_Empty(t *testing.T) {
	r := require.New(t)

	result := formatSearchResponse(nil, "Troy Barnes")
	r.Equal("No results found for: Troy Barnes", result)
}
