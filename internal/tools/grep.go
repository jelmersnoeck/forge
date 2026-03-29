package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

// GrepTool returns the Grep tool definition.
func GrepTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "Grep",
		Description: "Search tool built on ripgrep. Supports regex patterns, glob filters, and multiple output modes.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Regular expression pattern to search for",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "File or directory to search (defaults to CWD)",
				},
				"output_mode": map[string]any{
					"type":        "string",
					"enum":        []string{"files_with_matches", "content", "count"},
					"description": "Output mode (default: files_with_matches)",
				},
				"glob": map[string]any{
					"type":        "string",
					"description": "Glob pattern to filter files (e.g., *.js, **/*.ts)",
				},
				"-i": map[string]any{
					"type":        "boolean",
					"description": "Case insensitive search",
				},
				"-C": map[string]any{
					"type":        "number",
					"description": "Number of context lines (only for content mode)",
				},
				"head_limit": map[string]any{
					"type":        "number",
					"description": "Limit output to first N lines/entries",
				},
			},
			"required": []string{"pattern"},
		},
		Handler:  grepHandler,
		ReadOnly: true,
	}
}

func grepHandler(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	pattern, ok := input["pattern"].(string)
	if !ok {
		return types.ToolResult{IsError: true}, fmt.Errorf("pattern is required")
	}

	searchPath := ctx.CWD
	if p, ok := input["path"].(string); ok && p != "" {
		searchPath = p
	}

	outputMode := "files_with_matches"
	if om, ok := input["output_mode"].(string); ok {
		outputMode = om
	}

	// Build rg arguments
	args := []string{}

	// Output mode flags
	switch outputMode {
	case "files_with_matches":
		args = append(args, "-l")
	case "count":
		args = append(args, "-c")
	case "content":
		args = append(args, "-n") // line numbers
		// Context lines
		if c, ok := input["-C"].(float64); ok {
			args = append(args, fmt.Sprintf("-C%d", int(c)))
		}
	}

	// Case insensitive
	if i, ok := input["-i"].(bool); ok && i {
		args = append(args, "-i")
	}

	// Glob filter
	if glob, ok := input["glob"].(string); ok && glob != "" {
		args = append(args, "--glob", glob)
	}

	// Pattern and path
	args = append(args, pattern, searchPath)

	// Execute ripgrep
	execCtx, cancel := context.WithTimeout(ctx.Ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "rg", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// rg exit codes:
	// 0 = matches found
	// 1 = no matches
	// 2+ = error
	output := stdout.String()

	if err != nil {
		// Check for exit code
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode := exitErr.ExitCode()
			if exitCode == 1 {
				// No matches found, not an error
				return types.ToolResult{
					Content: []types.ToolResultContent{{
						Type: "text",
						Text: "(no matches)",
					}},
				}, nil
			}
		}

		// Real error
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = fmt.Sprintf("ripgrep error: %v", err)
		}
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: errMsg,
			}},
			IsError: true,
		}, nil
	}

	// Apply head_limit if specified
	if limit, ok := input["head_limit"].(float64); ok && limit > 0 {
		lines := strings.Split(output, "\n")
		limitInt := int(limit)
		if len(lines) > limitInt {
			lines = lines[:limitInt]
		}
		output = strings.Join(lines, "\n")
	}

	// Trim trailing newline
	output = strings.TrimRight(output, "\n")

	if output == "" {
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: "(no matches)",
			}},
		}, nil
	}

	return types.ToolResult{
		Content: []types.ToolResultContent{{
			Type: "text",
			Text: output,
		}},
	}, nil
}
