package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWebSearchTool(t *testing.T) {
	tests := map[string]struct {
		provider string
	}{
		"anthropic": {provider: "anthropic"},
		"openai":    {provider: "openai"},
		"empty":     {provider: ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			tool := WebSearchTool(tc.provider)
			r.Equal("WebSearch", tool.Name)
			r.NotEmpty(tool.Description)
			r.True(tool.ReadOnly)
			r.False(tool.Destructive)
		})
	}
}

func TestWebSearchHandler_Validation(t *testing.T) {
	tool := WebSearchTool("anthropic")
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

func TestWebSearchHandler_NoAPIKey_Anthropic(t *testing.T) {
	r := require.New(t)
	tool := WebSearchTool("anthropic")
	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: t.TempDir(),
	}

	t.Setenv("ANTHROPIC_API_KEY", "")

	result, err := tool.Handler(map[string]any{"query": "Greendale Community College"}, ctx)
	r.NoError(err) // handler returns error in result, not as Go error
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "ANTHROPIC_API_KEY")
}

func TestWebSearchHandler_NoAPIKey_OpenAI(t *testing.T) {
	r := require.New(t)
	tool := WebSearchTool("openai")
	ctx := types.ToolContext{
		Ctx: context.Background(),
		CWD: t.TempDir(),
	}

	t.Setenv("OPENAI_API_KEY", "")

	result, err := tool.Handler(map[string]any{"query": "Human Being mascot"}, ctx)
	r.NoError(err)
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "OPENAI_API_KEY")
}

func TestDispatchSearch_NoAPIKey(t *testing.T) {
	tests := map[string]struct {
		provider string
		envKey   string
		envVal   string
		wantErr  string
	}{
		"anthropic missing key": {
			provider: "anthropic",
			envKey:   "ANTHROPIC_API_KEY",
			envVal:   "",
			wantErr:  "ANTHROPIC_API_KEY",
		},
		"openai missing key": {
			provider: "openai",
			envKey:   "OPENAI_API_KEY",
			envVal:   "",
			wantErr:  "OPENAI_API_KEY",
		},
		"anthropic whitespace-only key": {
			provider: "anthropic",
			envKey:   "ANTHROPIC_API_KEY",
			envVal:   "   \t  ",
			wantErr:  "ANTHROPIC_API_KEY",
		},
		"openai whitespace-only key": {
			provider: "openai",
			envKey:   "OPENAI_API_KEY",
			envVal:   "   \t  ",
			wantErr:  "OPENAI_API_KEY",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			t.Setenv(tc.envKey, tc.envVal)

			_, err := dispatchSearch(context.Background(), tc.provider, "Troy Barnes", 5)
			r.Error(err)
			r.Contains(err.Error(), tc.wantErr)
		})
	}
}

func TestFormatSearchResponse_Empty(t *testing.T) {
	r := require.New(t)

	result := formatSearchResponse(nil, "Troy Barnes")
	r.Equal("No results found for: Troy Barnes", result)
}

func TestFormatOpenAISearchResponse(t *testing.T) {
	tests := map[string]struct {
		body    string
		query   string
		wantStr string
		wantErr bool
	}{
		"valid response with citations": {
			body: mustMarshal(oaiResponsesResult{
				Output: []oaiResponsesOutput{
					{
						Type: "message",
						Content: []oaiResponsesContent{
							{
								Type: "output_text",
								Text: "Greendale Community College is a fictional school.",
								Annotations: []oaiResponsesAnnotation{
									{Type: "url_citation", Title: "Greendale CC", URL: "https://greendale.edu"},
									{Type: "url_citation", Title: "Community Wiki", URL: "https://community.wiki"},
								},
							},
						},
					},
				},
			}),
			query:   "Greendale Community College",
			wantStr: "Found 2 result(s).",
		},
		"deduplicate URLs": {
			body: mustMarshal(oaiResponsesResult{
				Output: []oaiResponsesOutput{
					{
						Type: "message",
						Content: []oaiResponsesContent{
							{
								Type: "output_text",
								Text: "Results",
								Annotations: []oaiResponsesAnnotation{
									{Type: "url_citation", Title: "Same Page", URL: "https://same.url"},
									{Type: "url_citation", Title: "Same Page Again", URL: "https://same.url"},
								},
							},
						},
					},
				},
			}),
			query:   "test",
			wantStr: "Found 1 result(s).",
		},
		"empty response": {
			body:    mustMarshal(oaiResponsesResult{Output: []oaiResponsesOutput{}}),
			query:   "Señor Chang",
			wantStr: "No results found for: Señor Chang",
		},
		"invalid JSON": {
			body:    "not json",
			query:   "test",
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			result, err := formatOpenAISearchResponse([]byte(tc.body), tc.query)
			if tc.wantErr {
				r.Error(err)
				return
			}
			r.NoError(err)
			r.Contains(result, tc.wantStr)
		})
	}
}

func TestSearchContextSize(t *testing.T) {
	tests := map[string]struct {
		numResults int
		want       string
	}{
		"few results":    {numResults: 2, want: "low"},
		"medium results": {numResults: 5, want: "medium"},
		"many results":   {numResults: 10, want: "high"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, searchContextSize(tc.numResults))
		})
	}
}

func mustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
