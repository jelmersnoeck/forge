package tools

import (
	"fmt"
	"os"

	"github.com/jelmersnoeck/forge/internal/types"
)

// MultiEditTool returns the MultiEdit tool definition.
func MultiEditTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "MultiEdit",
		Description: "Applies an ordered list of string replacements to a single file atomically. Each edit sees the result of the previous edits. All edits must apply or the whole call fails with no file change.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the file (must already exist)",
				},
				"edits": map[string]any{
					"type":        "array",
					"minItems":    1,
					"description": "Ordered list of edits applied sequentially in-memory",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"old_string": map[string]any{
								"type":        "string",
								"description": "Text to replace",
							},
							"new_string": map[string]any{
								"type":        "string",
								"description": "Replacement text",
							},
							"replace_all": map[string]any{
								"type":        "boolean",
								"description": "Replace all occurrences (default false)",
							},
							"disable_fuzzy": map[string]any{
								"type":        "boolean",
								"description": "Force exact matching only (default false)",
							},
						},
						"required": []string{"old_string", "new_string"},
					},
				},
			},
			"required": []string{"file_path", "edits"},
		},
		Handler:     multiEditHandler,
		ReadOnly:    false,
		Destructive: false,
	}
}

func multiEditHandler(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	filePath, err := requireString(input, "file_path")
	if err != nil {
		return types.ToolResult{IsError: true}, err
	}

	rawEdits, ok := input["edits"].([]any)
	if !ok {
		return types.ToolResult{IsError: true}, fmt.Errorf("edits must be an array")
	}
	if len(rawEdits) == 0 {
		return errResultf("edits must contain at least one edit")
	}

	if isEnvFile(filePath) {
		return envFileError(filePath), nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return errResultf("file not found: %s", filePath)
		}
		return types.ToolResult{IsError: true}, err
	}

	content := string(data)
	totalReplaced := 0
	usedWhitespace := false

	for i, raw := range rawEdits {
		op, ok := raw.(map[string]any)
		if !ok {
			return errResultf("edit %d is not an object", i+1)
		}
		oldString, err := requireString(op, "old_string")
		if err != nil {
			return errResultf("edit %d: old_string is required", i+1)
		}
		newString, ok := op["new_string"].(string)
		if !ok {
			return errResultf("edit %d: new_string is required", i+1)
		}
		replaceAll := optionalBool(op, "replace_all", false)
		disableFuzzy := optionalBool(op, "disable_fuzzy", false)

		ranges, strat, ok := findMatches(content, oldString, disableFuzzy)
		switch {
		case !ok:
			diag := nearMissDiagnostic(content, oldString)
			if diag != "" {
				return errResultf("edit %d: %s\n\nold_string not found in %s", i+1, diag, filePath)
			}
			return errResultf("edit %d: old_string not found in %s", i+1, filePath)
		case len(ranges) > 1 && !replaceAll:
			return errResultf("edit %d: old_string appears %d times in %s; use replace_all: true to replace all occurrences", i+1, len(ranges), filePath)
		}

		if !replaceAll {
			ranges = ranges[:1]
		}
		if strat == matchWhitespace {
			usedWhitespace = true
		}

		content = spliceRanges(content, ranges, newString)
		totalReplaced += len(ranges)
	}

	if err := atomicWrite(filePath, []byte(content), targetPerm(filePath, 0644)); err != nil {
		return errResultf("failed to write file: %v", err)
	}

	ctx.ReadState.Delete(filePath)

	suffix := ""
	if usedWhitespace {
		suffix = " (whitespace-normalized match)"
	}

	return textResult(fmt.Sprintf("applied %d edit(s), replaced %d occurrence(s) in %s%s", len(rawEdits), totalReplaced, filePath, suffix)), nil
}
