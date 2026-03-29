package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestEditTool(t *testing.T) {
	tests := map[string]struct {
		setup func(t *testing.T, dir string) map[string]any
		want  func(*testing.T, string, types.ToolResult, error)
	}{
		"single replacement": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "script.txt")
				err := os.WriteFile(path, []byte("Troy: Cool beans"), 0644)
				require.NoError(t, err)
				return map[string]any{
					"file_path":  path,
					"old_string": "Cool",
					"new_string": "Awesome",
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "replaced 1 occurrence")

				path := filepath.Join(dir, "script.txt")
				data, readErr := os.ReadFile(path)
				r.NoError(readErr)
				r.Equal("Troy: Awesome beans", string(data))
			},
		},
		"replace all": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "chant.txt")
				err := os.WriteFile(path, []byte("pop pop pop"), 0644)
				require.NoError(t, err)
				return map[string]any{
					"file_path":   path,
					"old_string":  "pop",
					"new_string":  "bang",
					"replace_all": true,
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "replaced 3 occurrence")

				path := filepath.Join(dir, "chant.txt")
				data, readErr := os.ReadFile(path)
				r.NoError(readErr)
				r.Equal("bang bang bang", string(data))
			},
		},
		"multiple occurrences without replace_all": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "dean.txt")
				err := os.WriteFile(path, []byte("Dean Dean Dean"), 0644)
				require.NoError(t, err)
				return map[string]any{
					"file_path":  path,
					"old_string": "Dean",
					"new_string": "Pelton",
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "appears 3 times")
				r.Contains(result.Content[0].Text, "replace_all")
			},
		},
		"old_string not found": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "study.txt")
				err := os.WriteFile(path, []byte("Study group meeting"), 0644)
				require.NoError(t, err)
				return map[string]any{
					"file_path":  path,
					"old_string": "Pierce",
					"new_string": "Leonard",
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "not found")
			},
		},
		"file not found": {
			setup: func(t *testing.T, dir string) map[string]any {
				return map[string]any{
					"file_path":  filepath.Join(dir, "missing.txt"),
					"old_string": "foo",
					"new_string": "bar",
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "file not found")
			},
		},
		"multiline replacement": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "lyrics.txt")
				content := "Troy and Abed\nin the morning"
				err := os.WriteFile(path, []byte(content), 0644)
				require.NoError(t, err)
				return map[string]any{
					"file_path":  path,
					"old_string": "Troy and Abed\nin the morning",
					"new_string": "Troy and Abed\nin the evening",
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)

				path := filepath.Join(dir, "lyrics.txt")
				data, readErr := os.ReadFile(path)
				r.NoError(readErr)
				r.Equal("Troy and Abed\nin the evening", string(data))
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input := tc.setup(t, dir)
			tool := EditTool()
			result, err := tool.Handler(input, types.ToolContext{})
			tc.want(t, dir, result, err)
		})
	}
}
