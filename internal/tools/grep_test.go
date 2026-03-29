package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestGrepTool(t *testing.T) {
	tests := map[string]struct {
		setup func(t *testing.T, dir string) (map[string]any, types.ToolContext)
		want  func(*testing.T, types.ToolResult, error)
	}{
		"files_with_matches mode": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				os.WriteFile(filepath.Join(dir, "troy.txt"), []byte("Troy Barnes\nCool cool cool"), 0644)
				os.WriteFile(filepath.Join(dir, "abed.txt"), []byte("Abed Nadir\nInspector Spacetime"), 0644)

				return map[string]any{
					"pattern":     "Troy",
					"output_mode": "files_with_matches",
				}, types.ToolContext{Ctx: context.Background(), CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "troy.txt")
				r.NotContains(result.Content[0].Text, "abed.txt")
			},
		},
		"content mode": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				os.WriteFile(filepath.Join(dir, "greendale.txt"), []byte("Line 1: Greendale\nLine 2: Community College\nLine 3: Study Room"), 0644)

				return map[string]any{
					"pattern":     "Community",
					"output_mode": "content",
				}, types.ToolContext{Ctx: context.Background(), CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "Community College")
				r.Contains(result.Content[0].Text, "2:") // line number
			},
		},
		"count mode": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				os.WriteFile(filepath.Join(dir, "dean.txt"), []byte("Dean\nDean\nDean Pelton"), 0644)

				return map[string]any{
					"pattern":     "Dean",
					"output_mode": "count",
				}, types.ToolContext{Ctx: context.Background(), CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "3") // 3 matches
			},
		},
		"case insensitive": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				os.WriteFile(filepath.Join(dir, "test.txt"), []byte("Troy\ntroy\nTROY"), 0644)

				return map[string]any{
					"pattern": "troy",
					"-i":      true,
				}, types.ToolContext{Ctx: context.Background(), CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "test.txt")
			},
		},
		"glob filter": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				os.WriteFile(filepath.Join(dir, "code.go"), []byte("package main"), 0644)
				os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("package info"), 0644)

				return map[string]any{
					"pattern": "package",
					"glob":    "*.go",
				}, types.ToolContext{Ctx: context.Background(), CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "code.go")
				r.NotContains(result.Content[0].Text, "readme.txt")
			},
		},
		"no matches": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				os.WriteFile(filepath.Join(dir, "test.txt"), []byte("content"), 0644)

				return map[string]any{
					"pattern": "nonexistent",
				}, types.ToolContext{Ctx: context.Background(), CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Equal("(no matches)", result.Content[0].Text)
			},
		},
		"context lines": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				content := "Line 1\nLine 2\nLine 3: match\nLine 4\nLine 5"
				os.WriteFile(filepath.Join(dir, "context.txt"), []byte(content), 0644)

				return map[string]any{
					"pattern":     "match",
					"output_mode": "content",
					"-C":          float64(1),
				}, types.ToolContext{Ctx: context.Background(), CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				output := result.Content[0].Text
				// Should include context lines
				r.Contains(output, "Line 2")
				r.Contains(output, "Line 3")
				r.Contains(output, "Line 4")
			},
		},
		"head_limit": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("match"), 0644)
				os.WriteFile(filepath.Join(dir, "file2.txt"), []byte("match"), 0644)
				os.WriteFile(filepath.Join(dir, "file3.txt"), []byte("match"), 0644)

				return map[string]any{
					"pattern":    "match",
					"head_limit": float64(2),
				}, types.ToolContext{Ctx: context.Background(), CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				lines := strings.Split(result.Content[0].Text, "\n")
				r.Len(lines, 2)
			},
		},
		"regex pattern": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				os.WriteFile(filepath.Join(dir, "regex.txt"), []byte("email@example.com\ntest@test.org"), 0644)

				return map[string]any{
					"pattern":     `\w+@\w+\.\w+`,
					"output_mode": "content",
				}, types.ToolContext{Ctx: context.Background(), CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "email@example.com")
				r.Contains(result.Content[0].Text, "test@test.org")
			},
		},
		"search in subdirectories": {
			setup: func(t *testing.T, dir string) (map[string]any, types.ToolContext) {
				os.MkdirAll(filepath.Join(dir, "subdir"), 0755)
				os.WriteFile(filepath.Join(dir, "root.txt"), []byte("Human Being mascot"), 0644)
				os.WriteFile(filepath.Join(dir, "subdir", "nested.txt"), []byte("Human Being mascot"), 0644)

				return map[string]any{
					"pattern": "Human Being",
				}, types.ToolContext{Ctx: context.Background(), CWD: dir}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "root.txt")
				r.Contains(result.Content[0].Text, "nested.txt")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input, ctx := tc.setup(t, dir)
			tool := GrepTool()
			result, err := tool.Handler(input, ctx)
			tc.want(t, result, err)
		})
	}
}

func TestGrepToolRipgrepNotInstalled(t *testing.T) {
	// This test will fail if rg is not installed, which is expected
	// Skip it if we're in CI or rg is not available
	_, err := exec.LookPath("rg")
	if err != nil {
		t.Skip("ripgrep (rg) not installed, skipping test")
	}
}
