package repomap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// initRepo creates a git repo in dir and commits the given files.
func initRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Troy Barnes", "GIT_AUTHOR_EMAIL=troy@greendale.edu",
			"GIT_COMMITTER_NAME=Troy Barnes", "GIT_COMMITTER_EMAIL=troy@greendale.edu")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("init")
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	run("add", "-A")
	run("commit", "-m", "greendale")
	return dir
}

func TestBuild_Go(t *testing.T) {
	r := require.New(t)
	dir := initRepo(t, map[string]string{
		"greendale.go": `package greendale

type Dean struct{}

func (d *Dean) Announce() {}

const Mascot = "Human Being"

var campus = "Greendale"

func Study() {}
`,
	})

	m, err := Build(dir, Options{})
	r.NoError(err)
	r.Len(m.Files, 1)
	r.Equal("greendale.go", m.Files[0].Path)

	kinds := map[string]string{}
	for _, s := range m.Files[0].Symbols {
		kinds[s.Name] = s.Kind
	}
	r.Equal("type", kinds["Dean"])
	r.Equal("method", kinds["Dean.Announce"])
	r.Equal("const", kinds["Mascot"])
	r.Equal("var", kinds["campus"])
	r.Equal("func", kinds["Study"])
}

func TestBuild_NotGitRepo(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	m, err := Build(dir, Options{})
	r.NoError(err)
	r.Empty(m.Files)
	r.Equal("", Render(m))
}

func TestBuild_Deterministic(t *testing.T) {
	r := require.New(t)
	dir := initRepo(t, map[string]string{
		"a.go": "package a\nfunc Alpha() {}\nfunc Beta() {}\n",
		"b.go": "package b\nfunc Gamma() {}\n",
		"c.py": "class Señor:\n    def chang(self):\n        pass\n",
	})
	first := Render(mustBuild(t, dir))
	for i := 0; i < 5; i++ {
		r.Equal(first, Render(mustBuild(t, dir)))
	}
	r.Contains(first, "a.go")
	r.Contains(first, "func Alpha")
}

func TestBuild_UnparseableGoSkipped(t *testing.T) {
	r := require.New(t)
	dir := initRepo(t, map[string]string{
		"broken.go": "package x\nfunc (",
		"ok.go":     "package x\nfunc Fine() {}\n",
	})
	m := mustBuild(t, dir)
	paths := map[string]bool{}
	for _, f := range m.Files {
		paths[f.Path] = true
	}
	r.True(paths["ok.go"])
	r.False(paths["broken.go"])
}

func TestBuild_NonGoLanguages(t *testing.T) {
	r := require.New(t)
	dir := initRepo(t, map[string]string{
		"study.py": "class StudyGroup:\n    def convene(self):\n        pass\ndef helper():\n    pass\n",
		"app.ts":   "export interface Dean {}\nexport function announce() {}\nexport const CAMPUS = 1\n",
	})
	out := Render(mustBuild(t, dir))
	r.Contains(out, "type StudyGroup")
	r.Contains(out, "func helper")
	r.Contains(out, "type Dean")
	r.Contains(out, "func announce")
	r.Contains(out, "const CAMPUS")
}

func TestBuild_BudgetOmits(t *testing.T) {
	r := require.New(t)
	files := map[string]string{}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		files[name+".go"] = "package p\nfunc " + strings.ToUpper(name) + "unc() {}\n"
	}
	dir := initRepo(t, files)
	m := mustBuild(t, dir)
	r.Len(m.Files, 5)

	// Tiny budget forces omission but keeps at least one file.
	tight := mustBuildOpts(t, dir, Options{TokenBudget: 3})
	r.GreaterOrEqual(len(tight.Files), 1)
	r.Less(len(tight.Files), 5)
	r.Positive(tight.OmittedFiles)
	r.Contains(Render(tight), "omitted")
}

func TestBuild_MaxFiles(t *testing.T) {
	r := require.New(t)
	files := map[string]string{}
	for _, name := range []string{"a", "b", "c", "d"} {
		files[name+".go"] = "package p\nfunc F() {}\n"
	}
	dir := initRepo(t, files)
	m := mustBuildOpts(t, dir, Options{MaxFiles: 2})
	r.LessOrEqual(len(m.Files), 2)
}

func TestBuild_LargeFileSkipped(t *testing.T) {
	r := require.New(t)
	big := "package p\n" + strings.Repeat("// x\n", (maxFileBytes/5)+10) + "func Big() {}\n"
	dir := initRepo(t, map[string]string{
		"big.go":   big,
		"small.go": "package p\nfunc Small() {}\n",
	})
	out := Render(mustBuild(t, dir))
	r.Contains(out, "func Small")
	r.NotContains(out, "func Big")
}

func TestBuild_FileWithoutSymbols(t *testing.T) {
	r := require.New(t)
	dir := initRepo(t, map[string]string{
		"data.go":   "package p\n",
		"real.go":   "package p\nfunc Real() {}\n",
		"notes.txt": "just prose, no code",
	})
	m := mustBuild(t, dir)
	r.Len(m.Files, 1)
	r.Equal("real.go", m.Files[0].Path)
}

// TestBuild_OmittedCountExcludesUnrecognized verifies files with unrecognized
// extensions are NOT counted toward the omitted total (they were never symbol
// candidates), while parseable-but-symbol-less files ARE counted.
func TestBuild_OmittedCountExcludesUnrecognized(t *testing.T) {
	r := require.New(t)
	dir := initRepo(t, map[string]string{
		"real.go":   "package p\nfunc Real() {}\n",
		"notes.txt": "just prose",
		"data.json": "{}",
		"empty.go":  "package p\n",
	})
	m := mustBuild(t, dir)
	r.Len(m.Files, 1)
	// notes.txt + data.json (unrecognized) are not counted; only empty.go
	// (recognized, no symbols) is omitted.
	r.Equal(1, m.OmittedFiles)
}

// TestBuild_MaxFilesCountsOmitted verifies files skipped by the MaxFiles cap are
// reported in OmittedFiles rather than silently dropped.
func TestBuild_MaxFilesCountsOmitted(t *testing.T) {
	r := require.New(t)
	files := map[string]string{}
	for _, name := range []string{"a", "b", "c", "d"} {
		files[name+".go"] = "package p\nfunc F() {}\n"
	}
	dir := initRepo(t, files)
	m := mustBuildOpts(t, dir, Options{MaxFiles: 2})
	r.LessOrEqual(len(m.Files), 2)
	r.Positive(m.OmittedFiles)
}

func mustBuild(t *testing.T, dir string) Map {
	t.Helper()
	return mustBuildOpts(t, dir, Options{})
}

func mustBuildOpts(t *testing.T, dir string, opts Options) Map {
	t.Helper()
	m, err := Build(dir, opts)
	require.NoError(t, err)
	return m
}

func TestWithin(t *testing.T) {
	tests := map[string]struct {
		cwd  string
		rel  string
		want bool
	}{
		"simple child":        {"/tmp/repo", "foo.go", true},
		"nested child":        {"/tmp/repo", "pkg/foo.go", true},
		"trailing slash cwd":  {"/tmp/repo/", "foo.go", true},
		"root cwd child":      {"/", "etc/hosts", true},
		"parent escape":       {"/tmp/repo", "../secret", false},
		"deep escape":         {"/tmp/repo", "../../etc/passwd", false},
		"absolute rel joined": {"/tmp/repo", "/etc/passwd", true}, // Join treats leading / as relative → confined
		"sibling prefix":      {"/tmp/rep", "../reposecret/file", false},
		"self":                {"/tmp/repo", ".", false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, within(tc.cwd, tc.rel))
		})
	}
}

func TestResolveGitTimeout(t *testing.T) {
	tests := map[string]struct {
		in   string
		want time.Duration
	}{
		"empty uses default":        {"", defaultGitTimeout},
		"valid override":            {"45s", 45 * time.Second},
		"unparsable uses default":   {"garbage", defaultGitTimeout},
		"non-positive uses default": {"0s", defaultGitTimeout},
		"negative uses default":     {"-5s", defaultGitTimeout},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, resolveGitTimeout(tc.in))
		})
	}
}

func TestGitTimeout_EnvOverride(t *testing.T) {
	r := require.New(t)
	// gitTimeout reads the env var on each call (no process-lifetime caching),
	// so changes take effect immediately.
	r.Equal(defaultGitTimeout, gitTimeout())

	t.Setenv("FORGE_REPOMAP_GIT_TIMEOUT", "99s")
	r.Equal(99*time.Second, gitTimeout())

	t.Setenv("FORGE_REPOMAP_GIT_TIMEOUT", "garbage")
	r.Equal(defaultGitTimeout, gitTimeout())
}
