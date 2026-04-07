package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestReflectTool(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		input       map[string]any
		wantErr     bool
		wantContain string
	}{
		"missing summary": {
			input:   map[string]any{},
			wantErr: true,
		},
		"basic reflection": {
			input: map[string]any{
				"summary": "Implemented feature X",
			},
			wantContain: "Session Reflection",
		},
		"full reflection": {
			input: map[string]any{
				"summary":     "Added learnings support for Greendale",
				"mistakes":    []any{"Forgot to check nil case", "Used wrong type"},
				"successes":   []any{"Tests passed", "Code is clean"},
				"suggestions": []any{"Add more tests", "Improve documentation"},
			},
			wantContain: "Mistakes & Improvements",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tmpDir := t.TempDir()
			ctx := types.ToolContext{
				CWD: tmpDir,
			}

			tool := ReflectTool()
			result, err := tool.Handler(tc.input, ctx)

			if tc.wantErr {
				r.True(result.IsError || err != nil)
				return
			}

			r.NoError(err)
			r.False(result.IsError)

			// Check that a learning file was created in .forge/learnings/
			entries, err := os.ReadDir(filepath.Join(tmpDir, ".forge", "learnings"))
			r.NoError(err)
			r.Len(entries, 1)

			content, err := os.ReadFile(filepath.Join(tmpDir, ".forge", "learnings", entries[0].Name()))
			r.NoError(err)
			r.Contains(string(content), tc.wantContain)

			// No AGENTS.md should be created
			_, err = os.Stat(filepath.Join(tmpDir, "AGENTS.md"))
			r.True(os.IsNotExist(err), "AGENTS.md should not be created")
		})
	}
}

func TestReflectToolMultipleReflections(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	ctx := types.ToolContext{
		CWD: tmpDir,
	}

	tool := ReflectTool()

	// First reflection
	_, err := tool.Handler(map[string]any{
		"summary": "First session at Greendale",
	}, ctx)
	r.NoError(err)

	// Second reflection
	_, err = tool.Handler(map[string]any{
		"summary": "Second session with Troy Barnes",
	}, ctx)
	r.NoError(err)

	// Should have two separate files
	entries, err := os.ReadDir(filepath.Join(tmpDir, ".forge", "learnings"))
	r.NoError(err)
	r.Len(entries, 2)

	// Verify content in separate files
	var allContent strings.Builder
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(tmpDir, ".forge", "learnings", e.Name()))
		r.NoError(err)
		allContent.Write(data)
	}
	r.Contains(allContent.String(), "First session at Greendale")
	r.Contains(allContent.String(), "Second session with Troy Barnes")
}

func TestReflectToolGitattributes(t *testing.T) {
	tests := map[string]struct {
		existingContent string
		wantContains    string
		wantCount       int // how many times the line appears
	}{
		"no existing gitattributes": {
			existingContent: "",
			wantContains:    ".forge/learnings/** linguist-generated=true",
			wantCount:       1,
		},
		"existing gitattributes without learnings": {
			existingContent: "*.go text\n*.md text\n",
			wantContains:    ".forge/learnings/** linguist-generated=true",
			wantCount:       1,
		},
		"existing gitattributes with learnings already": {
			existingContent: "*.go text\n.forge/learnings/** linguist-generated=true\n",
			wantContains:    ".forge/learnings/** linguist-generated=true",
			wantCount:       1,
		},
		"existing without trailing newline": {
			existingContent: "*.go text",
			wantContains:    "*.go text\n.forge/learnings/** linguist-generated=true\n",
			wantCount:       1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			tmpDir := t.TempDir()

			if tc.existingContent != "" {
				err := os.WriteFile(filepath.Join(tmpDir, ".gitattributes"), []byte(tc.existingContent), 0644)
				r.NoError(err)
			}

			ctx := types.ToolContext{CWD: tmpDir}
			tool := ReflectTool()
			_, err := tool.Handler(map[string]any{
				"summary": "Testing gitattributes at Greendale",
			}, ctx)
			r.NoError(err)

			content, err := os.ReadFile(filepath.Join(tmpDir, ".gitattributes"))
			r.NoError(err)
			r.Contains(string(content), tc.wantContains)
			r.Equal(tc.wantCount, strings.Count(string(content), ".forge/learnings/** linguist-generated=true"))
		})
	}
}

func TestSlugify(t *testing.T) {
	tests := map[string]struct {
		input  string
		maxLen int
		want   string
	}{
		"simple": {
			input: "Implemented feature X", maxLen: 50,
			want: "implemented-feature-x",
		},
		"special chars": {
			input: "Fixed bug #42 (memory leak)", maxLen: 50,
			want: "fixed-bug-42-memory-leak",
		},
		"long summary truncated": {
			input: "This is a very long summary that should be truncated to fit", maxLen: 20,
			want: "this-is-a-very-long",
		},
		"empty becomes reflection": {
			input: "", maxLen: 50,
			want: "reflection",
		},
		"only special chars": {
			input: "!@#$%", maxLen: 50,
			want: "reflection",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, slugify(tc.input, tc.maxLen))
		})
	}
}

func TestSaveReflection(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	err := SaveReflection(tmpDir, "Troy Barnes joined the AC repair school")
	r.NoError(err)

	entries, err := os.ReadDir(filepath.Join(tmpDir, ".forge", "learnings"))
	r.NoError(err)
	r.Len(entries, 1)

	content, err := os.ReadFile(filepath.Join(tmpDir, ".forge", "learnings", entries[0].Name()))
	r.NoError(err)
	r.Contains(string(content), "Troy Barnes joined the AC repair school")
	r.Contains(string(content), "Session Reflection")

	// .gitattributes should exist
	ga, err := os.ReadFile(filepath.Join(tmpDir, ".gitattributes"))
	r.NoError(err)
	r.Contains(string(ga), ".forge/learnings/**")
}
