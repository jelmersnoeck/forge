package tools

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/jelmersnoeck/forge/internal/types"
)

// GlobTool returns the Glob tool definition.
func GlobTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "Glob",
		Description: "Fast file pattern matching using glob patterns. Returns paths sorted by modification time (newest first).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern (e.g., **/*.go, src/**/*.ts)",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Base directory to search (defaults to CWD)",
				},
			},
			"required": []string{"pattern"},
		},
		Handler:  globHandler,
		ReadOnly: true,
	}
}

func globHandler(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	pattern, ok := input["pattern"].(string)
	if !ok {
		return types.ToolResult{IsError: true}, fmt.Errorf("pattern is required")
	}

	basePath := ctx.CWD
	if p, ok := input["path"].(string); ok && p != "" {
		basePath = p
	}

	// Use doublestar to glob from the base path
	fsys := os.DirFS(basePath)
	matches, err := doublestar.Glob(fsys, pattern)
	if err != nil {
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: fmt.Sprintf("glob error: %v", err),
			}},
			IsError: true,
		}, nil
	}

	if len(matches) == 0 {
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: "(no matches)",
			}},
		}, nil
	}

	// Get modification times and sort by newest first
	type fileInfo struct {
		path    string
		modTime int64
	}

	files := make([]fileInfo, 0, len(matches))
	for _, match := range matches {
		fullPath := match
		if basePath != "." {
			fullPath = basePath + "/" + match
		}

		info, err := os.Stat(fullPath)
		if err != nil {
			// File might have been deleted, skip it
			continue
		}

		// Skip directories
		if info.IsDir() {
			continue
		}

		// Never expose .env files
		if isEnvFile(match) {
			continue
		}

		files = append(files, fileInfo{
			path:    match, // Return relative path from the match
			modTime: info.ModTime().Unix(),
		})
	}

	// Sort by modification time, newest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime > files[j].modTime
	})

	// Build output
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}

	return types.ToolResult{
		Content: []types.ToolResultContent{{
			Type: "text",
			Text: strings.Join(paths, "\n"),
		}},
	}, nil
}
