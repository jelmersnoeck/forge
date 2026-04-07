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

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return errResultf("file not found: %s", filePath)
		}
		return types.ToolResult{IsError: true}, err
	}

	content := string(data)
	count := strings.Count(content, oldString)

	switch {
	case count == 0:
		return errResultf("old_string not found in %s", filePath)
	case count > 1 && !replaceAll:
		return errResultf("old_string appears %d times in %s; use replace_all: true to replace all occurrences", count, filePath)
	}

	n := 1
	if replaceAll {
		n = -1
	}
	newContent := strings.Replace(content, oldString, newString, n)

	if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
		return errResultf("failed to write file: %v", err)
	}

	if ctx.ReadState != nil {
		delete(ctx.ReadState, filePath)
	}

	replacedCount := count
	if !replaceAll {
		replacedCount = 1
	}

	return textResult(fmt.Sprintf("replaced %d occurrence(s) in %s", replacedCount, filePath)), nil
}
