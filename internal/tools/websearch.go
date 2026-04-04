package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/jelmersnoeck/forge/internal/types"
)

// WebSearchTool returns the WebSearch tool definition.
// Under the hood it makes a sub-call to the Anthropic API with the server-side
// web_search tool, then reformats the results as plain text. This keeps the
// main conversation free of opaque server_tool_use / web_search_tool_result
// blocks and lets us use a cheaper model for the search.
func WebSearchTool() types.ToolDefinition {
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
		Handler:     webSearchHandler,
		ReadOnly:    true,
		Destructive: false,
	}
}

func webSearchHandler(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
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

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: "WebSearch requires ANTHROPIC_API_KEY to be set.",
			}},
			IsError: true,
		}, nil
	}

	results, err := searchViaAnthropic(ctx.Ctx, apiKey, query, numResults)
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
	out.WriteString(fmt.Sprintf("Search results for: %s\n\n", query))

	for i, r := range allResults {
		out.WriteString(fmt.Sprintf("%d. %s\n   %s\n\n", i+1, r.Title, r.URL))
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
		out.WriteString(fmt.Sprintf("Found %d result(s).", len(allResults)))
	}

	return out.String()
}
