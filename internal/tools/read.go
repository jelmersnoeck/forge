package tools

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jelmersnoeck/forge/internal/types"
)

// ReadTool returns the Read tool definition.
func ReadTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "Read",
		Description: "Reads a file from the filesystem with line numbers. Supports text files and images (returns base64).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the file",
				},
				"offset": map[string]any{
					"type":        "number",
					"description": "Starting line number (1-based, default 1)",
				},
				"limit": map[string]any{
					"type":        "number",
					"description": "Number of lines to read (default 2000)",
				},
			},
			"required": []string{"file_path"},
		},
		Handler:  readHandler,
		ReadOnly: true,
	}
}

func readHandler(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	filePath, ok := input["file_path"].(string)
	if !ok {
		return types.ToolResult{IsError: true}, fmt.Errorf("file_path is required")
	}

	offset := 1
	if o, ok := input["offset"].(float64); ok {
		offset = int(o)
	}
	if offset < 1 {
		offset = 1
	}

	limit := 2000
	if l, ok := input["limit"].(float64); ok {
		limit = int(l)
	}

	// Check if file exists
	info, err := os.Stat(filePath)
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

	if info.IsDir() {
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: fmt.Sprintf("path is a directory: %s", filePath),
			}},
			IsError: true,
		}, nil
	}

	// Check if it's an image
	ext := strings.ToLower(filepath.Ext(filePath))
	imageExts := map[string]string{
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".webp": "image/webp",
		".svg":  "image/svg+xml",
	}

	if mediaType, isImage := imageExts[ext]; isImage {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return types.ToolResult{IsError: true}, err
		}
		encoded := base64.StdEncoding.EncodeToString(data)
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "image",
				Source: &types.ImageSource{
					Type:      "base64",
					MediaType: mediaType,
					Data:      encoded,
				},
			}},
		}, nil
	}

	// Read text file with line numbers
	file, err := os.Open(filePath)
	if err != nil {
		return types.ToolResult{IsError: true}, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	lineNum := 1

	for scanner.Scan() {
		if lineNum >= offset && lineNum < offset+limit {
			line := scanner.Text()
			if len(line) > 2000 {
				line = line[:2000] + "..."
			}
			lines = append(lines, fmt.Sprintf("%d\t%s", lineNum, line))
		}
		lineNum++
		if lineNum >= offset+limit {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return types.ToolResult{IsError: true}, err
	}

	return types.ToolResult{
		Content: []types.ToolResultContent{{
			Type: "text",
			Text: strings.Join(lines, "\n"),
		}},
	}, nil
}
