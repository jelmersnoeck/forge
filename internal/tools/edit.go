package tools

import (
	"fmt"
	"os"
	"strings"

	"github.com/jelmersnoeck/forge/internal/types"
)

// EditTool returns the Edit tool definition.
func EditTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "Edit",
		Description: "Performs exact string replacement in a file. old_string must be unique unless replace_all is true.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the file",
				},
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
			"required": []string{"file_path", "old_string", "new_string"},
		},
		Handler:     editHandler,
		ReadOnly:    false,
		Destructive: false,
	}
}

func editHandler(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	filePath, err := requireString(input, "file_path")
	if err != nil {
		return types.ToolResult{IsError: true}, err
	}
	oldString, err := requireString(input, "old_string")
	if err != nil {
		return types.ToolResult{IsError: true}, err
	}
	newString, ok := input["new_string"].(string)
	if !ok {
		return types.ToolResult{IsError: true}, fmt.Errorf("new_string is required")
	}

	if isEnvFile(filePath) {
		return envFileError(filePath), nil
	}

	replaceAll := optionalBool(input, "replace_all", false)
	disableFuzzy := optionalBool(input, "disable_fuzzy", false)

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return errResultf("file not found: %s", filePath)
		}
		return types.ToolResult{IsError: true}, err
	}

	content := string(data)
	ranges, strat, ok := findMatches(content, oldString, disableFuzzy)

	switch {
	case !ok:
		diag := nearMissDiagnostic(content, oldString)
		if diag != "" {
			return errResultf("%s\n\nold_string not found in %s", diag, filePath)
		}
		return errResultf("old_string not found in %s", filePath)
	case len(ranges) > 1 && !replaceAll:
		return errResultf("old_string appears %d times in %s; use replace_all: true to replace all occurrences", len(ranges), filePath)
	}

	if !replaceAll {
		ranges = ranges[:1]
	}

	newContent := spliceRanges(content, ranges, newString)

	if err := atomicWrite(filePath, []byte(newContent), targetPerm(filePath, 0644)); err != nil {
		return errResultf("failed to write file: %v", err)
	}

	ctx.ReadState.Delete(filePath)

	suffix := ""
	if strat == matchWhitespace {
		suffix = " (whitespace-normalized match)"
	}

	return textResult(fmt.Sprintf("replaced %d occurrence(s) in %s%s", len(ranges), filePath, suffix)), nil
}

// spliceRanges replaces each byte range in content (ascending, non-overlapping)
// with replacement and returns the result.
func spliceRanges(content string, ranges [][2]int, replacement string) string {
	var b strings.Builder
	prev := 0
	for _, r := range ranges {
		b.WriteString(content[prev:r[0]])
		b.WriteString(replacement)
		prev = r[1]
	}
	b.WriteString(content[prev:])
	return b.String()
}
