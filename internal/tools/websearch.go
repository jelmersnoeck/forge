package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

// WebSearchTool returns the WebSearch tool definition.
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
		if numResults > 10 {
			numResults = 10
		}
		if numResults < 1 {
			numResults = 1
		}
	}

	// Check which search provider is configured
	provider := os.Getenv("SEARCH_PROVIDER")
	if provider == "" {
		provider = "duckduckgo" // default
	}

	var results []SearchResult
	var err error

	switch provider {
	case "brave":
		results, err = searchBrave(query, numResults)
	case "duckduckgo":
		results, err = searchDuckDuckGo(query, numResults)
	default:
		return types.ToolResult{IsError: true}, fmt.Errorf("unknown search provider: %s", provider)
	}

	if err != nil {
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: fmt.Sprintf("Search failed: %v\n\nTip: Set SEARCH_PROVIDER=brave and BRAVE_API_KEY if you have a Brave Search API key.", err),
			}},
			IsError: true,
		}, nil
	}

	if len(results) == 0 {
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: fmt.Sprintf("No results found for: %s", query),
			}},
		}, nil
	}

	// Format results
	var output strings.Builder
	output.WriteString(fmt.Sprintf("Search results for: %s\n\n", query))

	for i, result := range results {
		output.WriteString(fmt.Sprintf("%d. %s\n", i+1, result.Title))
		output.WriteString(fmt.Sprintf("   %s\n", result.URL))
		if result.Description != "" {
			output.WriteString(fmt.Sprintf("   %s\n", result.Description))
		}
		output.WriteString("\n")
	}

	output.WriteString(fmt.Sprintf("Found %d result(s). Use the URLs to get more detailed information if needed.", len(results)))

	return types.ToolResult{
		Content: []types.ToolResultContent{{
			Type: "text",
			Text: output.String(),
		}},
	}, nil
}

type SearchResult struct {
	Title       string
	URL         string
	Description string
}

// searchDuckDuckGo uses DuckDuckGo's instant answer API
func searchDuckDuckGo(query string, numResults int) ([]SearchResult, error) {
	// Use DuckDuckGo's instant answer API
	// Note: This is limited compared to full search, but doesn't require API key
	apiURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1&skip_disambig=1",
		url.QueryEscape(query))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var data struct {
		Abstract       string `json:"Abstract"`
		AbstractText   string `json:"AbstractText"`
		AbstractSource string `json:"AbstractSource"`
		AbstractURL    string `json:"AbstractURL"`
		RelatedTopics  []struct {
			Text     string `json:"Text"`
			FirstURL string `json:"FirstURL"`
		} `json:"RelatedTopics"`
		Results []struct {
			Text     string `json:"Text"`
			FirstURL string `json:"FirstURL"`
		} `json:"Results"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var results []SearchResult

	// Add abstract if available
	if data.AbstractText != "" {
		results = append(results, SearchResult{
			Title:       data.AbstractSource,
			URL:         data.AbstractURL,
			Description: truncate(data.AbstractText, 200),
		})
	}

	// Add related topics
	for _, topic := range data.RelatedTopics {
		if len(results) >= numResults {
			break
		}
		if topic.FirstURL != "" {
			title := topic.Text
			if len(title) > 100 {
				parts := strings.Split(title, " - ")
				if len(parts) > 0 {
					title = parts[0]
				}
			}
			results = append(results, SearchResult{
				Title:       title,
				URL:         topic.FirstURL,
				Description: truncate(topic.Text, 200),
			})
		}
	}

	// If we still don't have enough results, suggest using Brave
	if len(results) == 0 {
		return nil, fmt.Errorf("no results from DuckDuckGo API. For better search results, configure Brave Search API:\n" +
			"1. Get API key from https://brave.com/search/api/\n" +
			"2. Set SEARCH_PROVIDER=brave\n" +
			"3. Set BRAVE_API_KEY=your_key")
	}

	return results, nil
}

// searchBrave uses Brave Search API (requires API key)
func searchBrave(query string, numResults int) ([]SearchResult, error) {
	apiKey := os.Getenv("BRAVE_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("BRAVE_API_KEY environment variable not set.\n\n" +
			"To use Brave Search:\n" +
			"1. Get a free API key from https://brave.com/search/api/\n" +
			"2. Set BRAVE_API_KEY=your_key_here\n" +
			"3. Free tier includes 2,000 queries/month")
	}

	apiURL := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(query), numResults)

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid BRAVE_API_KEY. Get a key from https://brave.com/search/api/")
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var data struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var results []SearchResult
	for _, item := range data.Web.Results {
		results = append(results, SearchResult{
			Title:       item.Title,
			URL:         item.URL,
			Description: truncate(item.Description, 200),
		})
	}

	return results, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
