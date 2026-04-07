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
	pattern, err := requireString(input, "pattern")
	if err != nil {
		return types.ToolResult{IsError: true}, err
	}

	searchPath := optionalString(input, "path", ctx.CWD)

	if isEnvFile(searchPath) {
		return envFileError(searchPath), nil
	}

	outputMode := optionalString(input, "output_mode", "files_with_matches")

	args := []string{}

	switch outputMode {
	case "files_with_matches":
		args = append(args, "-l")
	case "count":
		args = append(args, "-c")
	case "content":
		args = append(args, "-n")
		if c := optionalFloat(input, "-C", 0); c > 0 {
			args = append(args, fmt.Sprintf("-C%d", int(c)))
		}
	}

	if optionalBool(input, "-i", false) {
		args = append(args, "-i")
	}

	if glob := optionalString(input, "glob", ""); glob != "" {
		args = append(args, "--glob", glob)
	}

	// Defense-in-depth: exclude .env files even if hidden file search is enabled
	args = append(args, "--glob", "!.env", "--glob", "!.env.*")
	args = append(args, pattern, searchPath)

	execCtx, cancel := context.WithTimeout(ctx.Ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "rg", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()

	output := stdout.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return textResult("(no matches)"), nil
		}

		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = fmt.Sprintf("ripgrep error: %v", err)
		}
		return errResult(errMsg)
	}

	if limit := optionalFloat(input, "head_limit", 0); limit > 0 {
		lines := strings.Split(output, "\n")
		if len(lines) > int(limit) {
			lines = lines[:int(limit)]
		}
		output = strings.Join(lines, "\n")
	}

	output = strings.TrimRight(output, "\n")

	if output == "" {
		return textResult("(no matches)"), nil
	}

	return textResult(output), nil
}
