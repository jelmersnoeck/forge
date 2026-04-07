package tools

import (
	"fmt"

	"github.com/jelmersnoeck/forge/internal/types"
)

// textResult builds a successful ToolResult containing a single text block.
func textResult(text string) types.ToolResult {
	return types.ToolResult{
		Content: []types.ToolResultContent{
			{Type: "text", Text: text},
		},
	}
}

// errResult builds an error ToolResult. Returns (result, nil) so handlers
// can return it directly — the error is surfaced to the LLM, not the loop.
func errResult(text string) (types.ToolResult, error) {
	return types.ToolResult{
		Content: []types.ToolResultContent{
			{Type: "text", Text: text},
		},
		IsError: true,
	}, nil
}

// errResultf is errResult with fmt.Sprintf formatting.
func errResultf(format string, args ...any) (types.ToolResult, error) {
	return errResult(fmt.Sprintf(format, args...))
}

// requireString extracts a required string parameter from tool input.
// Returns ("", error) if the key is missing or not a string.
func requireString(input map[string]any, key string) (string, error) {
	v, ok := input[key].(string)
	if !ok || v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return v, nil
}

// optionalString extracts an optional string parameter, returning fallback if absent.
func optionalString(input map[string]any, key, fallback string) string {
	if v, ok := input[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

// optionalFloat extracts an optional float64 parameter, returning fallback if absent.
func optionalFloat(input map[string]any, key string, fallback float64) float64 {
	if v, ok := input[key].(float64); ok {
		return v
	}
	return fallback
}

// optionalBool extracts an optional bool parameter, returning fallback if absent.
func optionalBool(input map[string]any, key string, fallback bool) bool {
	if v, ok := input[key].(bool); ok {
		return v
	}
	return fallback
}
