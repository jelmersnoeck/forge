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

// FileUnchangedStub is returned when a file has already been read with the
// same parameters and hasn't been modified on disk since. The earlier
// tool_result is still in the conversation context, so re-sending the full
// content would waste tokens (especially cache_creation tokens).
const FileUnchangedStub = "File unchanged since last read. The content from the earlier Read tool_result in this conversation is still current — refer to that instead of re-reading."

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
	filePath, err := requireString(input, "file_path")
	if err != nil {
		return types.ToolResult{IsError: true}, err
	}

	offset := int(optionalFloat(input, "offset", 1))
	if offset < 1 {
		offset = 1
	}
	limit := int(optionalFloat(input, "limit", 2000))

	if isEnvFile(filePath) {
		return envFileError(filePath), nil
	}

	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return errResultf("file not found: %s", filePath)
		}
		return types.ToolResult{IsError: true}, err
	}

	if info.IsDir() {
		return errResultf("path is a directory: %s", filePath)
	}

	// Check if it's an image (images are never deduped — they're binary blobs
	// and the model can't "refer back" to a previous image tool_result)
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

	// ── Dedup: return stub if file unchanged since last read ──
	//
	//   Read("foo.go")  → 25K tokens of content, store mtime
	//   Read("foo.go")  → ~30 tokens stub (same params, same mtime)
	//
	// Only applies to text reads. Edit/Write invalidate the entry
	// so the next Read after a mutation always returns fresh content.
	if ctx.ReadState != nil {
		if entry, exists := ctx.ReadState[filePath]; exists {
			if entry.Offset == offset && entry.Limit == limit {
				if info.ModTime().Unix() == entry.MtimeUnix {
					return types.ToolResult{
						Content: []types.ToolResultContent{{
							Type: "text",
							Text: FileUnchangedStub,
						}},
					}, nil
				}
			}
		}
	}

	// Read text file with line numbers
	file, err := os.Open(filePath)
	if err != nil {
		return types.ToolResult{IsError: true}, err
	}
	defer func() { _ = file.Close() }()

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

	// Store state for dedup on subsequent reads.
	if ctx.ReadState != nil {
		ctx.ReadState[filePath] = types.ReadFileEntry{
			MtimeUnix: info.ModTime().Unix(),
			Offset:    offset,
			Limit:     limit,
		}
	}

	return textResult(strings.Join(lines, "\n")), nil
}
