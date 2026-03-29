package tools

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestReadTool(t *testing.T) {
	tests := map[string]struct {
		setup func(t *testing.T, dir string) map[string]any
		want  func(*testing.T, types.ToolResult, error)
	}{
		"read text file": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "troy.txt")
				err := os.WriteFile(path, []byte("Troy Barnes\nAbed Nadir\nJeff Winger"), 0644)
				require.NoError(t, err)
				return map[string]any{"file_path": path}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Len(result.Content, 1)
				r.Equal("text", result.Content[0].Type)
				r.Contains(result.Content[0].Text, "1\tTroy Barnes")
				r.Contains(result.Content[0].Text, "2\tAbed Nadir")
				r.Contains(result.Content[0].Text, "3\tJeff Winger")
			},
		},
		"read with offset": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "greendale.txt")
				err := os.WriteFile(path, []byte("Line 1\nLine 2\nLine 3\nLine 4"), 0644)
				require.NoError(t, err)
				return map[string]any{"file_path": path, "offset": float64(2)}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				lines := strings.Split(result.Content[0].Text, "\n")
				r.True(len(lines) >= 2)
				r.Contains(lines[0], "2\tLine 2")
			},
		},
		"read with limit": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "study_room.txt")
				err := os.WriteFile(path, []byte("A\nB\nC\nD\nE"), 0644)
				require.NoError(t, err)
				return map[string]any{"file_path": path, "limit": float64(2)}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				lines := strings.Split(result.Content[0].Text, "\n")
				r.Len(lines, 2)
			},
		},
		"nonexistent file": {
			setup: func(t *testing.T, dir string) map[string]any {
				return map[string]any{"file_path": filepath.Join(dir, "missing.txt")}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "file not found")
			},
		},
		"read image file": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "human_being.png")
				// Create a tiny PNG (1x1 red pixel)
				pngData := []byte{
					0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
					0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
					0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
					0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
					0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41,
					0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
					0x00, 0x03, 0x01, 0x01, 0x00, 0x18, 0xDD, 0x8D,
					0xB4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E,
					0x44, 0xAE, 0x42, 0x60, 0x82,
				}
				err := os.WriteFile(path, pngData, 0644)
				require.NoError(t, err)
				return map[string]any{"file_path": path}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Len(result.Content, 1)
				r.Equal("image", result.Content[0].Type)
				r.NotNil(result.Content[0].Source)
				r.Equal("base64", result.Content[0].Source.Type)
				r.Equal("image/png", result.Content[0].Source.MediaType)
				// Verify it's valid base64
				_, decErr := base64.StdEncoding.DecodeString(result.Content[0].Source.Data)
				r.NoError(decErr)
			},
		},
		"truncate long lines": {
			setup: func(t *testing.T, dir string) map[string]any {
				path := filepath.Join(dir, "long.txt")
				longLine := strings.Repeat("a", 2500)
				err := os.WriteFile(path, []byte(longLine), 0644)
				require.NoError(t, err)
				return map[string]any{"file_path": path}
			},
			want: func(t *testing.T, result types.ToolResult, err error) {
				r := require.New(t)
				r.NoError(err)
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "...")
				// Line number + tab + 2000 chars + "..." = 2007ish
				r.True(len(result.Content[0].Text) < 2100)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input := tc.setup(t, dir)
			tool := ReadTool()
			result, err := tool.Handler(input, types.ToolContext{})
			tc.want(t, result, err)
		})
	}
}
