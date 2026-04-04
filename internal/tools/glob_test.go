package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestGlobTool(t *testing.T) {
	tests := map[string]struct {
		setup func(t *testing.T, dir string) (map[string]any, types.ToolContext)
		want  func(*testing.T, types.ToolResult, error)
	}{
		"simple pattern": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				_ = os.WriteFile(filepath.Join(dir, "troy.txt"), []byte("content"), 0644)
				_ = os.WriteFile(filepath.Join(dir, "abed.txt"), []byte("content"), 0644)
				_ = os.WriteFile(filepath.Join(dir, "jeff.go"), []byte("content"), 0644)

				return map[string]any{"pattern": "*.txt"}, types.ToolContext{CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "troy.txt")
				r.Contains(result.Content[0].Text, "abed.txt")
				r.NotContains(result.Content[0].Text, "jeff.go")
			},
		},
		"recursive pattern": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				_ = os.MkdirAll(filepath.Join(dir, "greendale", "library"), 0755)
				_ = os.WriteFile(filepath.Join(dir, "root.go"), []byte("content"), 0644)
				_ = os.WriteFile(filepath.Join(dir, "greendale", "study.go"), []byte("content"), 0644)
				_ = os.WriteFile(filepath.Join(dir, "greendale", "library", "books.go"), []byte("content"), 0644)

				return map[string]any{"pattern": "**/*.go"}, types.ToolContext{CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "root.go")
				r.Contains(result.Content[0].Text, "study.go")
				r.Contains(result.Content[0].Text, "books.go")
			},
		},
		"no matches": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				_ = os.WriteFile(filepath.Join(dir, "file.txt"), []byte("content"), 0644)
				return map[string]any{"pattern": "*.go"}, types.ToolContext{CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Equal("(no matches)", result.Content[0].Text)
			},
		},
		"sorted by mtime": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				// Create files with different modification times
				old := filepath.Join(dir, "old.txt")
				_ = os.WriteFile(old, []byte("old"), 0644)
				oldTime := time.Now().Add(-2 * time.Hour)
				_ = os.Chtimes(old, oldTime, oldTime)

				time.Sleep(10 * time.Millisecond)

				new := filepath.Join(dir, "new.txt")
				_ = os.WriteFile(new, []byte("new"), 0644)

				return map[string]any{"pattern": "*.txt"}, types.ToolContext{CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				lines := strings.Split(result.Content[0].Text, "\n")
				r.Len(lines, 2)
				// Newest first
				r.Contains(lines[0], "new.txt")
				r.Contains(lines[1], "old.txt")
			},
		},
		"custom path": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				subdir := filepath.Join(dir, "subdir")
				_ = os.MkdirAll(subdir, 0755)
				_ = os.WriteFile(filepath.Join(subdir, "file.txt"), []byte("content"), 0644)

				return map[string]any{
					"pattern": "*.txt",
					"path":    subdir,
				}, types.ToolContext{CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "file.txt")
			},
		},
		"skip directories": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				_ = os.MkdirAll(filepath.Join(dir, "troy"), 0755)
				_ = os.WriteFile(filepath.Join(dir, "troy.txt"), []byte("content"), 0644)

				return map[string]any{"pattern": "troy*"}, types.ToolContext{CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				// Should only match the file, not the directory
				r.Equal("troy.txt", strings.TrimSpace(result.Content[0].Text))
			},
		},
		"complex pattern": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				_ = os.MkdirAll(filepath.Join(dir, "src", "tools"), 0755)
				_ = os.MkdirAll(filepath.Join(dir, "pkg", "tools"), 0755)
				_ = os.WriteFile(filepath.Join(dir, "src", "tools", "read.go"), []byte("content"), 0644)
				_ = os.WriteFile(filepath.Join(dir, "pkg", "tools", "write.go"), []byte("content"), 0644)
				_ = os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("content"), 0644)

				return map[string]any{"pattern": "**/tools/*.go"}, types.ToolContext{CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "read.go")
				r.Contains(result.Content[0].Text, "write.go")
				r.NotContains(result.Content[0].Text, "main.go")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input, ctx := tc.setup(t, dir)
			tool := GlobTool()
			result, err := tool.Handler(input, ctx)
			tc.want(t, result, err)
		})
	}
}
