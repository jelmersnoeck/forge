package tools

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	//   Read("foo.go")  → 25K tokens of content, store mtime + content hash
	//   Read("foo.go")  → ~30 tokens stub (same params, same mtime)        [fast path]
	//   Read("foo.go")  → ~30 tokens stub (new mtime, identical bytes)     [hash path]
	//
	// Only applies to text reads. Edit/Write invalidate the entry
	// so the next Read after a mutation always returns fresh content.
	var dedupEntry types.ReadFileEntry
	var haveDedupEntry bool
	if ctx.ReadState != nil {
		if entry, exists := ctx.ReadState.Get(filePath); exists &&
			entry.Offset == offset && entry.Limit == limit {
			dedupEntry = entry
			haveDedupEntry = true
			// Fast path: stat proves the file is untouched, no need to read it.
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

	// content is the line-numbered, normalized window returned to the model:
	// each scanned line (CR/LF stripped by bufio.Scanner) re-joined with "\n".
	// The hash is therefore over this normalized representation, NOT the raw
	// file bytes — two files differing only in line endings (e.g. CRLF vs LF)
	// produce the same content here and dedup as identical. This is intended:
	// dedup operates on exactly the bytes the model saw, which is the same
	// normalized form on every read, so dedup is internally consistent.
	content := strings.Join(lines, "\n")
	contentHash := hashContent(content)

	// Hash fallback: mtime moved but the windowed content is identical
	// (touch, no-op save, git checkout round-trip, idempotent formatter).
	// Return the stub and refresh the stored mtime so the next identical read
	// takes the cheap fast path again.
	if haveDedupEntry && dedupEntry.ContentHash == contentHash {
		dedupEntry.MtimeUnix = info.ModTime().Unix()
		ctx.ReadState.Set(filePath, dedupEntry)
		if ctx.Emit != nil {
			ctx.Emit(types.OutboundEvent{
				Type:      "debug",
				SessionID: ctx.SessionID,
				Content: fmt.Sprintf(
					"Read dedup (hash path): %s unchanged content despite new mtime; returning stub",
					filePath),
			})
		}
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: FileUnchangedStub,
			}},
		}, nil
	}

	// Store state for dedup on subsequent reads.
	ctx.ReadState.Set(filePath, types.ReadFileEntry{
		MtimeUnix:   info.ModTime().Unix(),
		Offset:      offset,
		Limit:       limit,
		ContentHash: contentHash,
	})

	return textResult(content), nil
}

// hashContent returns the sha256 hex digest of s, used to detect identical
// re-reads whose mtime changed. s is the normalized, line-numbered window
// (not the raw file bytes), so the hash is line-ending-agnostic.
func hashContent(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
