package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMultiEditTool(t *testing.T) {
	tests := map[string]struct {
		setup func(t *testing.T, dir string) map[string]any
		want  func(*testing.T, string, types.ToolResult, error)
	}{
		"multiple non-adjacent edits apply atomically": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "greendale.txt")
				require.NoError(t, os.WriteFile(path, []byte("Troy\nAbed\nBritta"), 0644))
				return map[string]any{
					"file_path": path,
					"edits": []any{
						map[string]any{"old_string": "Troy", "new_string": "Jeff"},
						map[string]any{"old_string": "Britta", "new_string": "Annie"},
					},
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "applied 2 edit(s)")
				data, _ := os.ReadFile(filepath.Join(dir, "greendale.txt"))
				r.Equal("Jeff\nAbed\nAnnie", string(data))
			},
		},
		"sequential dependency: later op matches earlier result": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "chant.txt")
				require.NoError(t, os.WriteFile(path, []byte("foo"), 0644))
				return map[string]any{
					"file_path": path,
					"edits": []any{
						map[string]any{"old_string": "foo", "new_string": "bar"},
						map[string]any{"old_string": "bar", "new_string": "baz"},
					},
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				data, _ := os.ReadFile(filepath.Join(dir, "chant.txt"))
				r.Equal("baz", string(data))
			},
		},
		"replace_all across ops": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "pop.txt")
				require.NoError(t, os.WriteFile(path, []byte("pop pop pop"), 0644))
				return map[string]any{
					"file_path": path,
					"edits": []any{
						map[string]any{"old_string": "pop", "new_string": "bang", "replace_all": true},
					},
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "replaced 3 occurrence")
				data, _ := os.ReadFile(filepath.Join(dir, "pop.txt"))
				r.Equal("bang bang bang", string(data))
			},
		},
		"failing op leaves file unchanged": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "dean.txt")
				require.NoError(t, os.WriteFile(path, []byte("Dean Pelton"), 0644))
				return map[string]any{
					"file_path": path,
					"edits": []any{
						map[string]any{"old_string": "Dean", "new_string": "Deansie"},
						map[string]any{"old_string": "Señor Chang", "new_string": "Chang"},
					},
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "edit 2")
				data, _ := os.ReadFile(filepath.Join(dir, "dean.txt"))
				r.Equal("Dean Pelton", string(data), "file must be unchanged on failure")
			},
		},
		"ambiguous match without replace_all fails, no write": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "amb.txt")
				require.NoError(t, os.WriteFile(path, []byte("pop pop"), 0644))
				return map[string]any{
					"file_path": path,
					"edits": []any{
						map[string]any{"old_string": "pop", "new_string": "bang"},
					},
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "appears 2 times")
				data, _ := os.ReadFile(filepath.Join(dir, "amb.txt"))
				r.Equal("pop pop", string(data))
			},
		},
		"same old_string in two ops without replace_all fails on second": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "dup.txt")
				require.NoError(t, os.WriteFile(path, []byte("x"), 0644))
				return map[string]any{
					"file_path": path,
					"edits": []any{
						map[string]any{"old_string": "x", "new_string": "y"},
						map[string]any{"old_string": "x", "new_string": "z"},
					},
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "edit 2")
				data, _ := os.ReadFile(filepath.Join(dir, "dup.txt"))
				r.Equal("x", string(data))
			},
		},
		"empty edits array": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "empty.txt")
				require.NoError(t, os.WriteFile(path, []byte("data"), 0644))
				return map[string]any{"file_path": path, "edits": []any{}}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "at least one edit")
			},
		},
		"missing file": {
			setup: func(t *testing.T, dir string) map[string]any {
				return map[string]any{
					"file_path": filepath.Join(dir, "ghost.txt"),
					"edits":     []any{map[string]any{"old_string": "a", "new_string": "b"}},
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "file not found")
			},
		},
		"whitespace-normalized match notes suffix": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "ws.txt")
				require.NoError(t, os.WriteFile(path, []byte("func f() {\n    return 42\n}"), 0644))
				return map[string]any{
					"file_path": path,
					"edits": []any{
						map[string]any{"old_string": "func f() {\n\treturn 42\n}", "new_string": "func f() { return 7 }"},
					},
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "whitespace-normalized match")
			},
		},
		"malformed op is not an object": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "bad.txt")
				require.NoError(t, os.WriteFile(path, []byte("data"), 0644))
				return map[string]any{
					"file_path": path,
					"edits":     []any{"not-an-object"},
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "edit 1 is not an object")
			},
		},
		"env file rejected": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, ".env")
				require.NoError(t, os.WriteFile(path, []byte("SECRET=1"), 0644))
				return map[string]any{
					"file_path": path,
					"edits":     []any{map[string]any{"old_string": "1", "new_string": "2"}},
				}
			},
			want: func(t *testing.T, dir string, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
			},
		},
	}

	tool := MultiEditTool()
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input := tc.setup(t, dir)
			result, err := tool.Handler(input, types.ToolContext{})
			tc.want(t, dir, result, err)
		})
	}
}

func TestMultiEditToolMissingParams(t *testing.T) {
	tool := MultiEditTool()
	tests := map[string]map[string]any{
		"no file_path": {"edits": []any{}},
		"edits not array": {
			"file_path": "/tmp/x",
			"edits":     "nope",
		},
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			result, err := tool.Handler(input, types.ToolContext{})
			r.Error(err)
			r.True(result.IsError)
		})
	}
}
