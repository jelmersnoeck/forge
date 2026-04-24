package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/jelmersnoeck/forge/internal/types"
)

// WebSearchTool returns the WebSearch tool definition.
// The providerName determines which search backend to use:
//   - "anthropic" / "claude-cli" / "": Anthropic's web_search_20260209 server tool
//   - "openai": OpenAI's Responses API with web_search tool
//
// Under the hood it makes a sub-call to the appropriate API with a server-side
// web search tool, then reformats the results as plain text. This keeps the
// main conversation free of opaque server_tool_use / web_search_tool_result
// blocks and lets us use a cheaper model for the search.
func WebSearchTool(providerName string) types.ToolDefinition {
	return types.ToolDefinition{
		Name: "WebSearch",
		Description: `Search the web for information. Use this when you need to look up current information, documentation, API references, error messages, or anything not in your training data. Returns titles, snippets, and URLs.

Examples of when to use:
- "What's the latest stable version of Go?"
- "How to use Bubble Tea key bindings?"
- "Error: module not found - what does this mean?"
- "Anthropic API rate limits documentation"
- "Best practices for Go error handling 2024"`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The search query. Be specific and include relevant keywords.",
				},
				"num_results": map[string]any{
					"type":        "number",
					"description": "Number of results to return (default: 5, max: 10)",
				},
			},
			"required": []string{"query"},
		},
		Handler:     makeWebSearchHandler(providerName),
		ReadOnly:    true,
		Destructive: false,
	}
}

func makeWebSearchHandler(providerName string) types.ToolHandler {
	return func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
		query, ok := input["query"].(string)
		if !ok || query == "" {
			return types.ToolResult{IsError: true}, fmt.Errorf("query is required")
		}

		numResults := 5
		if n, ok := input["num_results"].(float64); ok {
			numResults = int(n)
			switch {
			case numResults > 10:
				numResults = 10
			case numResults < 1:
				numResults = 1
			}
		}

		results, err := dispatchSearch(ctx.Ctx, providerName, query, numResults)
		if err != nil {
			return types.ToolResult{
				Content: []types.ToolResultContent{{
					Type: "text",
					Text: fmt.Sprintf("Search failed: %v", err),
				}},
				IsError: true,
			}, nil
		}

		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: results,
			}},
		}, nil
	}
}

// dispatchSearch routes the query to the appropriate provider backend.
func dispatchSearch(ctx context.Context, providerName, query string, numResults int) (string, error) {
	switch providerName {
	case "openai":
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			return "", fmt.Errorf("WebSearch requires OPENAI_API_KEY when provider is openai")
		}
		return searchViaOpenAI(ctx, apiKey, query, numResults)
	default:
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return "", fmt.Errorf("WebSearch requires ANTHROPIC_API_KEY to be set")
		}
		return searchViaAnthropic(ctx, apiKey, query, numResults)
	}
}

// searchResult is a single hit from web search.
type searchResult struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// searchViaAnthropic makes a sub-call to the Anthropic API with the server-side
// web_search tool, collects the results, and formats them as plain text.
//
//	┌──────────────────────┐
//	│   Main conversation  │
//	│ (model asks to       │
//	│  use WebSearch tool) │
//	└────────┬─────────────┘
//	         │ tool handler
//	         ▼
//	┌──────────────────────┐
//	│  Sub-call to API     │
//	│  with web_search     │
//	│  server tool         │
//	└────────┬─────────────┘
//	         │ response contains:
//	         │  server_tool_use + web_search_tool_result + text
//	         ▼
//	┌──────────────────────┐
//	│  Extract results,    │
//	│  format as text      │
//	│  → tool_result       │
//	└──────────────────────┘
func searchViaAnthropic(ctx context.Context, apiKey, query string, numResults int) (string, error) {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))

	// Use haiku for the search sub-call — fast and cheap.
	// AllowedCallers must include "direct" because most models don't support
	// "programmatic" tool calling for server-side tools like web_search.
	msg, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeHaiku4_5,
		MaxTokens: 1024,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(
				anthropic.NewTextBlock("Perform a web search for: " + query),
			),
		},
		System: []anthropic.TextBlockParam{
			{Text: "You are a web search assistant. Search for the query and present the results."},
		},
		Tools: []anthropic.ToolUnionParam{
			{OfWebSearchTool20260209: &anthropic.WebSearchTool20260209Param{
				MaxUses:        anthropic.Int(int64(numResults)),
				AllowedCallers: []string{"direct"},
			}},
		},
		ToolChoice: anthropic.ToolChoiceUnionParam{
			OfAny: &anthropic.ToolChoiceAnyParam{},
		},
	})
	if err != nil {
		return "", fmt.Errorf("API call: %w", err)
	}

	return formatSearchResponse(msg.Content, query), nil
}

// formatSearchResponse extracts search results from the API response content
// blocks and formats them as readable text.
func formatSearchResponse(blocks []anthropic.ContentBlockUnion, query string) string {
	var allResults []searchResult
	var textParts []string

	for _, block := range blocks {
		switch block.Type {
		case "web_search_tool_result":
			ws := block.AsWebSearchToolResult()
			results := ws.Content.AsWebSearchResultBlockArray()
			for _, r := range results {
				allResults = append(allResults, searchResult{
					Title: r.Title,
					URL:   r.URL,
				})
			}

		case "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				textParts = append(textParts, text)
			}
		}
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Search results for: %s\n\n", query)

	for i, r := range allResults {
		fmt.Fprintf(&out, "%d. %s\n   %s\n\n", i+1, r.Title, r.URL)
	}

	if len(textParts) > 0 {
		out.WriteString("Summary:\n")
		out.WriteString(strings.Join(textParts, "\n\n"))
		out.WriteString("\n\n")
	}

	switch {
	case len(allResults) == 0 && len(textParts) == 0:
		return fmt.Sprintf("No results found for: %s", query)
	case len(allResults) > 0:
		fmt.Fprintf(&out, "Found %d result(s).", len(allResults))
	}

	return out.String()
}

// ── OpenAI Responses API web search ─────────────────────────

// openAIHTTPClient is used for OpenAI search API calls. Separate from
// http.DefaultClient so we can set a reasonable timeout without affecting
// other HTTP callers in the process.
var openAIHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// maxResponseBodySize caps how much of an HTTP response we'll read into
// memory. 5 MB is generous for a search API response; anything larger is
// almost certainly a bug on the server side.
const maxResponseBodySize = 5 * 1024 * 1024 // 5 MB

// searchViaOpenAI calls OpenAI's Responses API with web_search tool to
// get search results, then formats them as plain text.
//
//	┌──────────────────────┐
//	│   WebSearch handler  │
//	└────────┬─────────────┘
//	         │ POST /v1/responses
//	         ▼
//	┌──────────────────────┐
//	│  OpenAI Responses    │
//	│  API + web_search    │
//	└────────┬─────────────┘
//	         │ output[]: web_search_call + message w/ annotations
//	         ▼
//	┌──────────────────────┐
//	│  Extract citations,  │
//	│  format as text      │
//	└──────────────────────┘
func searchViaOpenAI(ctx context.Context, apiKey, query string, numResults int) (string, error) {
	payload := map[string]any{
		"model": "gpt-4.1-mini",
		"tools": []map[string]any{
			{
				"type": "web_search",
				"web_search": map[string]any{
					"search_context_size": searchContextSize(numResults),
				},
			},
		},
		"input": "Search the web for: " + query,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.openai.com/v1/responses", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := openAIHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OpenAI API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	return formatOpenAISearchResponse(respBody, query)
}

// searchContextSize maps numResults to OpenAI's search_context_size.
// OpenAI supports "low", "medium", "high" — we map numResults ranges.
func searchContextSize(numResults int) string {
	switch {
	case numResults <= 3:
		return "low"
	case numResults <= 6:
		return "medium"
	default:
		return "high"
	}
}

// ── OpenAI Responses API response types ─────────────────────

// oaiResponsesResult is the top-level response from /v1/responses.
type oaiResponsesResult struct {
	Output []oaiResponsesOutput `json:"output"`
}

type oaiResponsesOutput struct {
	Type    string                `json:"type"`
	Content []oaiResponsesContent `json:"content,omitempty"`
	Action  *oaiResponsesAction   `json:"action,omitempty"`
}

type oaiResponsesAction struct {
	Query string `json:"query"`
}

type oaiResponsesContent struct {
	Type        string                   `json:"type"`
	Text        string                   `json:"text"`
	Annotations []oaiResponsesAnnotation `json:"annotations,omitempty"`
}

type oaiResponsesAnnotation struct {
	Type  string `json:"type"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

// formatOpenAISearchResponse parses the Responses API JSON and formats
// search results as plain text matching the Anthropic format.
func formatOpenAISearchResponse(body []byte, query string) (string, error) {
	var result oaiResponsesResult
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	var allResults []searchResult
	var textParts []string

	for _, output := range result.Output {
		switch output.Type {
		case "message":
			for _, content := range output.Content {
				if content.Type == "output_text" && content.Text != "" {
					textParts = append(textParts, content.Text)
				}
				for _, ann := range content.Annotations {
					if ann.Type == "url_citation" {
						allResults = append(allResults, searchResult{
							Title: ann.Title,
							URL:   ann.URL,
						})
					}
				}
			}
		}
	}

	// Deduplicate URLs (OpenAI often cites the same URL multiple times).
	seen := map[string]bool{}
	deduped := make([]searchResult, 0, len(allResults))
	for _, r := range allResults {
		if !seen[r.URL] {
			seen[r.URL] = true
			deduped = append(deduped, r)
		}
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Search results for: %s\n\n", query)

	for i, r := range deduped {
		fmt.Fprintf(&out, "%d. %s\n   %s\n\n", i+1, r.Title, r.URL)
	}

	if len(textParts) > 0 {
		out.WriteString("Summary:\n")
		out.WriteString(strings.Join(textParts, "\n\n"))
		out.WriteString("\n\n")
	}

	switch {
	case len(deduped) == 0 && len(textParts) == 0:
		return fmt.Sprintf("No results found for: %s", query), nil
	case len(deduped) > 0:
		fmt.Fprintf(&out, "Found %d result(s).", len(deduped))
	}

	return out.String(), nil
}
