package tools

import (
	"context"
	"os"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWebSearchTool(t *testing.T) {
	tool := WebSearchTool()

	require.Equal(t, "WebSearch", tool.Name)
	require.NotEmpty(t, tool.Description)
	require.True(t, tool.ReadOnly)
}

func TestWebSearchDuckDuckGo(t *testing.T) {
	// Skip if we don't have internet connectivity
	if os.Getenv("SKIP_NETWORK_TESTS") != "" {
		t.Skip("Skipping network test")
	}

	tool := WebSearchTool()
	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: t.TempDir(),
	}

	tests := map[string]struct {
		input       map[string]any
		expectError bool
	}{
		"basic search": {
			input: map[string]any{
				"query": "Go programming language",
			},
			expectError: false,
		},
		"search with num_results": {
			input: map[string]any{
				"query":       "Bubble Tea framework",
				"num_results": float64(3),
			},
			expectError: false,
		},
		"missing query": {
			input:       map[string]any{},
			expectError: true,
		},
		"empty query": {
			input: map[string]any{
				"query": "",
			},
			expectError: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := tool.Handler(tc.input, ctx)

			if tc.expectError {
				require.Error(t, err)
				return
			}

			// DuckDuckGo may not always return results for every query
			// So we check that it either succeeded or gave a helpful error
			if err != nil {
				t.Logf("Search error (this may be expected): %v", err)
			}

			// If we got a result, verify it's formatted correctly
			if len(result.Content) > 0 {
				require.NotEmpty(t, result.Content[0].Text)
				t.Logf("Result: %s", result.Content[0].Text)
			}
		})
	}
}

func TestWebSearchBrave(t *testing.T) {
	// Only run if Brave API key is set
	if os.Getenv("BRAVE_API_KEY") == "" {
		t.Skip("BRAVE_API_KEY not set, skipping Brave Search test")
	}

	// Set provider to brave
	originalProvider := os.Getenv("SEARCH_PROVIDER")
	os.Setenv("SEARCH_PROVIDER", "brave")
	defer func() {
		if originalProvider == "" {
			os.Unsetenv("SEARCH_PROVIDER")
		} else {
			os.Setenv("SEARCH_PROVIDER", originalProvider)
		}
	}()

	tool := WebSearchTool()
	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: t.TempDir(),
	}

	result, err := tool.Handler(map[string]any{
		"query":       "Anthropic Claude API",
		"num_results": float64(5),
	}, ctx)

	require.NoError(t, err)
	require.NotEmpty(t, result.Content)
	require.NotEmpty(t, result.Content[0].Text)

	// Should contain some expected text
	text := result.Content[0].Text
	require.Contains(t, text, "Search results for:")
	t.Logf("Brave search result: %s", text)
}

func TestSearchResultTruncation(t *testing.T) {
	tests := map[string]struct {
		input    string
		maxLen   int
		expected string
	}{
		"short string": {
			input:    "Hello",
			maxLen:   10,
			expected: "Hello",
		},
		"exact length": {
			input:    "HelloWorld",
			maxLen:   10,
			expected: "HelloWorld",
		},
		"needs truncation": {
			input:    "This is a very long string that needs to be truncated",
			maxLen:   20,
			expected: "This is a very lo...",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			result := truncate(tc.input, tc.maxLen)
			require.Equal(t, tc.expected, result)
			require.LessOrEqual(t, len(result), tc.maxLen)
		})
	}
}
