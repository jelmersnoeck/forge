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
	filePath, ok := input["file_path"].(string)
	if !ok {
		return types.ToolResult{IsError: true}, fmt.Errorf("file_path is required")
	}

	oldString, ok := input["old_string"].(string)
	if !ok {
		return types.ToolResult{IsError: true}, fmt.Errorf("old_string is required")
	}

	newString, ok := input["new_string"].(string)
	if !ok {
		return types.ToolResult{IsError: true}, fmt.Errorf("new_string is required")
	}

	if isEnvFile(filePath) {
		return envFileError(filePath), nil
	}

	replaceAll := false
	if ra, ok := input["replace_all"].(bool); ok {
		replaceAll = ra
	}

	// Read file
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return types.ToolResult{
				Content: []types.ToolResultContent{{
					Type: "text",
					Text: fmt.Sprintf("file not found: %s", filePath),
				}},
				IsError: true,
			}, nil
		}
		return types.ToolResult{IsError: true}, err
	}

	content := string(data)

	// Count occurrences
	count := strings.Count(content, oldString)

	switch {
	case count == 0:
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: fmt.Sprintf("old_string not found in %s", filePath),
			}},
			IsError: true,
		}, nil
	case count > 1 && !replaceAll:
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: fmt.Sprintf("old_string appears %d times in %s; use replace_all: true to replace all occurrences", count, filePath),
			}},
			IsError: true,
		}, nil
	}

	// Perform replacement
	var newContent string
	if replaceAll {
		newContent = strings.ReplaceAll(content, oldString, newString)
	} else {
		newContent = strings.Replace(content, oldString, newString, 1)
	}

	// Write back
	if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: fmt.Sprintf("failed to write file: %v", err),
			}},
			IsError: true,
		}, nil
	}

	// Invalidate read dedup — next Read must return fresh content.
	if ctx.ReadState != nil {
		delete(ctx.ReadState, filePath)
	}

	replacedCount := count
	if !replaceAll {
		replacedCount = 1
	}

	return types.ToolResult{
		Content: []types.ToolResultContent{{
			Type: "text",
			Text: fmt.Sprintf("replaced %d occurrence(s) in %s", replacedCount, filePath),
		}},
	}, nil
}
