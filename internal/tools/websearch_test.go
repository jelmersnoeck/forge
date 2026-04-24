package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestClassifySearchError(t *testing.T) {
	tests := map[string]struct {
		err  error
		want string
	}{
		"auth missing": {
			err:  ErrAuthMissing,
			want: "auth_missing",
		},
		"wrapped auth missing": {
			err:  fmt.Errorf("setup: %w", ErrAuthMissing),
			want: "auth_missing",
		},
		"auth rejected 401": {
			err:  &HTTPStatusError{StatusCode: 401, Body: "unauthorized"},
			want: "auth_rejected",
		},
		"auth rejected 403": {
			err:  &HTTPStatusError{StatusCode: 403, Body: "forbidden"},
			want: "auth_rejected",
		},
		"rate limit": {
			err:  &HTTPStatusError{StatusCode: 429, Body: "too many requests"},
			want: "rate_limit",
		},
		"server error 500": {
			err:  &HTTPStatusError{StatusCode: 500, Body: "internal"},
			want: "server_error",
		},
		"server error 503": {
			err:  &HTTPStatusError{StatusCode: 503, Body: "unavailable"},
			want: "server_error",
		},
		"wrapped HTTP status": {
			err:  fmt.Errorf("openai: %w", &HTTPStatusError{StatusCode: 429, Body: "slow down"}),
			want: "rate_limit",
		},
		"response truncated": {
			err:  ErrResponseTruncated,
			want: "response_truncated",
		},
		"wrapped response truncated": {
			err:  fmt.Errorf("read: %w", ErrResponseTruncated),
			want: "response_truncated",
		},
		"context deadline exceeded": {
			err:  context.DeadlineExceeded,
			want: "timeout",
		},
		"context canceled": {
			err:  context.Canceled,
			want: "timeout",
		},
		"wrapped deadline": {
			err:  fmt.Errorf("HTTP request: %w", context.DeadlineExceeded),
			want: "timeout",
		},
		"unknown error": {
			err:  fmt.Errorf("something weird at Greendale"),
			want: "unknown",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, classifySearchError(tc.err))
		})
	}
}

func TestHTTPStatusError(t *testing.T) {
	r := require.New(t)

	err := &HTTPStatusError{StatusCode: 500, Body: "Dean Pelton broke the server"}
	r.Contains(err.Error(), "500")
	r.Contains(err.Error(), "Dean Pelton broke the server")

	// Verify it works with errors.As
	wrapped := fmt.Errorf("API call: %w", err)
	var httpErr *HTTPStatusError
	r.True(errors.As(wrapped, &httpErr))
	r.Equal(500, httpErr.StatusCode)
}

func TestSearchViaOpenAI_ResponseTruncation(t *testing.T) {
	r := require.New(t)

	// Restore original values after test
	origTimeout := OpenAIHTTPTimeout
	origMax := MaxResponseBodySize
	t.Cleanup(func() {
		OpenAIHTTPTimeout = origTimeout
		MaxResponseBodySize = origMax
	})

	// Set a tiny limit so we can test truncation without building a 5MB body
	MaxResponseBodySize = 64

	t.Run("body exactly at limit is not truncated", func(t *testing.T) {
		// Body is exactly MaxResponseBodySize bytes — NOT truncated
		exactBody := strings.Repeat("x", 64)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, exactBody)
		}))
		defer srv.Close()

		// We can't easily redirect searchViaOpenAI to use our server,
		// so test the core logic directly: read + truncation check
		resp, err := http.Get(srv.URL)
		r.NoError(err)
		defer func() { _ = resp.Body.Close() }()

		body, truncated, readErr := readLimitedBody(resp.Body, MaxResponseBodySize)
		r.NoError(readErr)
		r.False(truncated, "exact-size body should not be considered truncated")
		r.Equal(exactBody, string(body))
	})

	t.Run("body exceeding limit is truncated", func(t *testing.T) {
		// Body is bigger than MaxResponseBodySize — SHOULD be truncated
		bigBody := strings.Repeat("y", 128)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, bigBody)
		}))
		defer srv.Close()

		resp, err := http.Get(srv.URL)
		r.NoError(err)
		defer func() { _ = resp.Body.Close() }()

		_, truncated, readErr := readLimitedBody(resp.Body, MaxResponseBodySize)
		r.NoError(readErr)
		r.True(truncated, "oversized body should be detected as truncated")
	})

	t.Run("body smaller than limit is fine", func(t *testing.T) {
		smallBody := `{"output":[]}`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, smallBody)
		}))
		defer srv.Close()

		resp, err := http.Get(srv.URL)
		r.NoError(err)
		defer func() { _ = resp.Body.Close() }()

		body, truncated, readErr := readLimitedBody(resp.Body, MaxResponseBodySize)
		r.NoError(readErr)
		r.False(truncated)
		r.Equal(smallBody, string(body))
	})
}

func mustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
