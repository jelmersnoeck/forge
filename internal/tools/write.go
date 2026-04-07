package tools

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jelmersnoeck/forge/internal/types"
)

// WriteTool returns the Write tool definition.
func WriteTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "Write",
		Description: "Writes content to a file, creating parent directories if needed. Overwrites existing files.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the file",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Content to write",
				},
			},
			"required": []string{"file_path", "content"},
		},
		Handler:     writeHandler,
		ReadOnly:    false,
		Destructive: false,
	}
}

func writeHandler(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	filePath, err := requireString(input, "file_path")
	if err != nil {
		return types.ToolResult{IsError: true}, err
	}
	content, ok := input["content"].(string)
	if !ok {
		return types.ToolResult{IsError: true}, fmt.Errorf("content is required")
	}

	if isEnvFile(filePath) {
		return envFileError(filePath), nil
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return errResultf("failed to create parent directories: %v", err)
	}

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return errResultf("failed to write file: %v", err)
	}

	if ctx.ReadState != nil {
		delete(ctx.ReadState, filePath)
	}

	return textResult(fmt.Sprintf("wrote %d bytes to %s", len(content), filePath)), nil
}
