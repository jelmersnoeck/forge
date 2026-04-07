package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestReflectTool(t *testing.T) {
	tests := map[string]struct {
		input       map[string]any
		wantErr     bool
		wantContain string
	}{
		"missing summary": {
			input:   map[string]any{},
			wantErr: true,
		},
		"missing learnings": {
			input: map[string]any{
				"summary": "Implemented feature X",
			},
			wantErr: true,
		},
		"empty learnings array": {
			input: map[string]any{
				"summary":   "Implemented feature X",
				"learnings": []any{},
			},
			wantErr: true,
		},
		"valid reflection with learnings": {
			input: map[string]any{
				"summary": "Greendale paintball tournament debugging",
				"learnings": []any{
					"The Greendale API returns 503 during paintball season — retry with exponential backoff",
					"Dean Pelton's costume endpoint ignores Content-Type headers; always send multipart/form-data",
				},
			},
			wantContain: "Greendale API returns 503",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

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
			r.Contains(string(content), "# Learnings -")

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
		"summary":   "First session at Greendale",
		"learnings": []any{"The AC repair annex has its own auth system"},
	}, ctx)
	r.NoError(err)

	// Second reflection
	_, err = tool.Handler(map[string]any{
		"summary":   "Second session with Troy Barnes",
		"learnings": []any{"Troy's monkey gas leak detector is a real endpoint"},
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
	r.Contains(allContent.String(), "AC repair annex has its own auth system")
	r.Contains(allContent.String(), "Troy's monkey gas leak detector")
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
				"summary":   "Testing gitattributes at Greendale",
				"learnings": []any{"The .gitattributes file needs a trailing newline"},
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

func TestReflectFileFormat(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	ctx := types.ToolContext{CWD: tmpDir}
	tool := ReflectTool()

	_, err := tool.Handler(map[string]any{
		"summary": "Debugging Senor Chang's keylogger",
		"learnings": []any{
			"Chang's API rate-limits at 3 req/s, not 10 as documented",
			"The /surveillance endpoint requires Basic auth even though docs say Bearer",
		},
	}, ctx)
	r.NoError(err)

	entries, err := os.ReadDir(filepath.Join(tmpDir, ".forge", "learnings"))
	r.NoError(err)
	r.Len(entries, 1)

	content, err := os.ReadFile(filepath.Join(tmpDir, ".forge", "learnings", entries[0].Name()))
	r.NoError(err)
	s := string(content)

	// File should be a simple bullet list with a header, no diary fluff
	r.Contains(s, "# Learnings -")
	r.Contains(s, "- Chang's API rate-limits at 3 req/s")
	r.Contains(s, "- The /surveillance endpoint requires Basic auth")
	r.NotContains(s, "Summary")
	r.NotContains(s, "Mistakes")
	r.NotContains(s, "Successful Patterns")
}

// initGitRepo is defined in pr_create_test.go (same package).

func TestReflectCommitsInGitRepo(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir, "main")

	ctx := types.ToolContext{CWD: tmpDir}
	tool := ReflectTool()
	_, err := tool.Handler(map[string]any{
		"summary":   "Dean Pelton approved the Greendale mascot redesign",
		"learnings": []any{"The Human Being mascot endpoint returns SVG, not PNG"},
	}, ctx)
	r.NoError(err)

	// Verify the learning file is committed (not just staged)
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = tmpDir
	out, err := cmd.Output()
	r.NoError(err)
	r.Empty(strings.TrimSpace(string(out)), "working tree should be clean after reflect commits")

	// Verify the commit message
	cmd = exec.Command("git", "log", "--oneline", "-1", "--format=%s")
	cmd.Dir = tmpDir
	out, err = cmd.Output()
	r.NoError(err)
	r.Equal("forge: save session reflection", strings.TrimSpace(string(out)))
}

func TestReflectNoCommitWithoutGitRepo(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	// No git init — should still write file, just not commit

	ctx := types.ToolContext{CWD: tmpDir}
	tool := ReflectTool()
	result, err := tool.Handler(map[string]any{
		"summary":   "Senor Chang infiltrated the study group",
		"learnings": []any{"Chang uses a predictable session token format: chang-YYYYMMDD"},
	}, ctx)
	r.NoError(err)
	r.False(result.IsError)

	// File should still exist
	entries, err := os.ReadDir(filepath.Join(tmpDir, ".forge", "learnings"))
	r.NoError(err)
	r.Len(entries, 1)
}

func TestReflectPushesWhenRemoteExists(t *testing.T) {
	r := require.New(t)

	remote, local := initGitRepoWithRemote(t, "main")

	ctx := types.ToolContext{CWD: local}
	tool := ReflectTool()
	_, err := tool.Handler(map[string]any{
		"summary":   "Jeff Winger aced the bar exam on his second try",
		"learnings": []any{"The bar exam API caches results for 24h — use ?nocache=1 for retakes"},
	}, ctx)
	r.NoError(err)

	// Local working tree should be clean
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = local
	out, err := cmd.Output()
	r.NoError(err)
	r.Empty(strings.TrimSpace(string(out)), "working tree should be clean")

	// Remote should have the commit (clone the bare repo and check)
	verify := t.TempDir()
	gitExec(t, verify, "clone", remote, ".")
	cmd = exec.Command("git", "log", "--oneline", "-1", "--format=%s")
	cmd.Dir = verify
	out, err = cmd.Output()
	r.NoError(err)
	r.Equal("forge: save session reflection", strings.TrimSpace(string(out)))

	// The learning file should exist in the cloned repo
	entries, err := os.ReadDir(filepath.Join(verify, ".forge", "learnings"))
	r.NoError(err)
	r.Len(entries, 1)
}
