package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWriteTool(t *testing.T) {
	tests := map[string]struct {
		setup func(t *testing.T, dir string) map[string]any
		want  func(*testing.T, string, types.ToolResult, error)
	}{
		"write new file": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "troy.txt")
				return map[string]any{
					"file_path": path,
					"content":   "Troy and Abed in the morning",
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "wrote")

				// Verify file was created
				path := filepath.Join(dir, "troy.txt")
				data, readErr := os.ReadFile(path)
				r.NoError(readErr)
				r.Equal("Troy and Abed in the morning", string(data))
			},
		},
		"write with nested directories": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "greendale", "study_room", "notes.txt")
				return map[string]any{
					"file_path": path,
					"content":   "Señor Chang was here",
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)

				// Verify nested file was created
				path := filepath.Join(dir, "greendale", "study_room", "notes.txt")
				data, readErr := os.ReadFile(path)
				r.NoError(readErr)
				r.Equal("Señor Chang was here", string(data))
			},
		},
		"overwrite existing file": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "dean.txt")
				err := os.WriteFile(path, []byte("Old content"), 0644)
				require.NoError(t, err)
				return map[string]any{
					"file_path": path,
					"content":   "Dean Pelton approves",
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)

				// Verify file was overwritten
				path := filepath.Join(dir, "dean.txt")
				data, readErr := os.ReadFile(path)
				r.NoError(readErr)
				r.Equal("Dean Pelton approves", string(data))
			},
		},
		"empty content": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "empty.txt")
				return map[string]any{
					"file_path": path,
					"content":   "",
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)

				// Verify empty file was created
				path := filepath.Join(dir, "empty.txt")
				data, readErr := os.ReadFile(path)
				r.NoError(readErr)
				r.Len(data, 0)
			},
		},
		"missing file_path": {
			setup: func(t *testing.T, dir string) map[string]any {
				return map[string]any{"content": "some content"}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.Error(err)
				r.True(result.IsError)
			},
		},
		"missing content": {
			setup: func(t *testing.T, dir string) map[string]any {
				return map[string]any{"file_path": filepath.Join(dir, "test.txt")}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.Error(err)
				r.True(result.IsError)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input := tc.setup(t, dir)
			tool := WriteTool()
			result, err := tool.Handler(input, types.ToolContext{})
			tc.want(t, dir, result, err)
		})
	}
}
