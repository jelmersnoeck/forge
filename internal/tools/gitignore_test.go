package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jelmersnoeck/forge/internal/types"
)

func TestIsGitIgnored(t *testing.T) {
	r := require.New(t)

	dir := t.TempDir()
	initGitRepo(t, dir, "main")
	writeFile(t, dir, ".gitignore", ".forge/\nsecret.txt\n")

	r.True(IsGitIgnored(dir, ".forge/learnings/x.md"), "path under ignored dir is ignored")
	r.True(IsGitIgnored(dir, "secret.txt"), "explicitly ignored file")
	r.False(IsGitIgnored(dir, "README.md"), "tracked file is not ignored")
	r.False(IsGitIgnored(dir, "AGENTS.md"), "untracked-but-not-ignored file")
}

func TestIsGitIgnored_NotARepo(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	// No git init: check-ignore exits 128/2 → treat as not ignored.
	r.False(IsGitIgnored(dir, ".forge/learnings/x.md"))
}

// TestReflectGitignoredForgeKeepsIndexClean is the core regression test for
// issue #268: when .forge/ is gitignored, reflect must write the learning file
// locally but never stage .gitattributes/AGENTS.md, leaving a clean index.
func TestReflectGitignoredForgeKeepsIndexClean(t *testing.T) {
	r := require.New(t)

	dir := t.TempDir()
	initGitRepo(t, dir, "main")
	writeFile(t, dir, ".gitignore", ".forge/\n")
	gitExec(t, dir, "add", ".gitignore")
	gitExec(t, dir, "commit", "-m", "ignore forge")

	ctx := types.ToolContext{CWD: dir}
	tool := ReflectTool()
	result, err := tool.Handler(map[string]any{
		"summary":   "Abed Nadir filmed a documentary about the index",
		"learnings": []any{"git add stages valid pathspecs even when a sibling path is ignored"},
	}, ctx)
	r.NoError(err)
	r.False(result.IsError)

	// Learning file still written locally.
	entries, err := os.ReadDir(filepath.Join(dir, ".forge", "learnings"))
	r.NoError(err)
	r.Len(entries, 1, "learning file should still be written locally")

	// Index must be clean — no STAGED files. The bug was git add staging
	// .gitattributes/AGENTS.md (column-1 status) before aborting on the ignored
	// path. Untracked files (?? ...) on disk are fine; only staging is wrong.
	out, err := GitOutput(dir, "status", "--porcelain")
	r.NoError(err)
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		// Column 1 is the staged (index) status. '?' = untracked (fine),
		// ' ' = unmodified-in-index. Anything else (A/M/D/R/C) means staged.
		staged := line[0]
		r.Contains([]byte{' ', '?'}, staged, "no file should be staged, got: %q", line)
	}

	// .gitattributes should not have been created at all (ensureGitattributes skipped).
	_, statErr := os.Stat(filepath.Join(dir, ".gitattributes"))
	r.True(os.IsNotExist(statErr), ".gitattributes should not be created when .forge is ignored")
}
