package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTruncateDiff(t *testing.T) {
	tests := map[string]struct {
		input      string
		wantSame   bool
		wantMarker bool
	}{
		"truncateDiff short": {
			input:    strings.Repeat("a", 1024), // 1KB — well under 100KB
			wantSame: true,
		},
		"truncateDiff long": {
			input:      strings.Repeat("b", 200*1024), // 200KB — over 100KB
			wantSame:   false,
			wantMarker: true,
		},
		"exactly at limit": {
			input:    strings.Repeat("c", maxDiffBytes),
			wantSame: true,
		},
		"one byte over": {
			// With only 1 byte over, the truncation marker adds more
			// characters than it removes — just check the marker is present.
			input:      strings.Repeat("d", maxDiffBytes+1),
			wantSame:   false,
			wantMarker: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			got := truncateDiff(tc.input)
			if tc.wantSame {
				r.Equal(tc.input, got)
				return
			}

			r.NotEqual(tc.input, got)

			if tc.wantMarker {
				r.Contains(got, "truncated")
				r.Contains(got, "bytes")
			}

			// Verify head + tail structure: head is 60% of maxDiffBytes,
			// tail is 40%.
			headSize := maxDiffBytes * 60 / 100
			tailSize := maxDiffBytes * 40 / 100
			r.Equal(string(tc.input[:headSize]), got[:headSize], "head should be preserved")

			tailStart := len(got) - tailSize
			wantTail := tc.input[len(tc.input)-tailSize:]
			r.Equal(wantTail, got[tailStart:], "tail should be preserved")
		})
	}
}

// initGitRepo creates a minimal git repo in dir with an initial commit.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "dean@greendale.edu"},
		{"git", "config", "user.name", "Craig Pelton"},
		{"git", "commit", "--allow-empty", "-m", "Welcome to Greendale, you're already accepted"},
	}

	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2024-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2024-01-01T00:00:00Z")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "command %v failed: %s", args, string(out))
	}
}

// currentBranchName returns the name of the current branch in a git repo.
func currentBranchName(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err, "git rev-parse --abbrev-ref HEAD failed")
	return strings.TrimSpace(string(out))
}

func TestIsGitRepo(t *testing.T) {
	tests := map[string]struct {
		setup func(t *testing.T) string
		want  bool
	}{
		"valid git repo": {
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				initGitRepo(t, dir)
				return dir
			},
			want: true,
		},
		"not a git repo": {
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			dir := tc.setup(t)
			r.Equal(tc.want, isGitRepo(dir))
		})
	}
}

func TestBranchExists(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Current default branch exists (could be "main" or "master" depending on git config).
	// Create an explicit branch to test.
	cmd := exec.Command("git", "branch", "jelmer/pillow-fort")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git branch failed: %s", string(out))

	tests := map[string]struct {
		branch string
		want   bool
	}{
		"existing branch": {
			branch: "jelmer/pillow-fort",
			want:   true,
		},
		"nonexistent branch": {
			branch: "evil-timeline",
			want:   false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, branchExists(dir, tc.branch))
		})
	}
}

func TestGetDiff(t *testing.T) {
	tests := map[string]struct {
		setup      func(t *testing.T) (dir string, baseBranch string)
		wantErr    bool
		wantErrMsg string
		check      func(t *testing.T, diff string)
	}{
		"not a git repo": {
			setup: func(t *testing.T) (string, string) {
				return t.TempDir(), ""
			},
			wantErr:    true,
			wantErrMsg: "not a git repository",
		},
		"repo with no changes": {
			setup: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				initGitRepo(t, dir)
				return dir, ""
			},
			check: func(t *testing.T, diff string) {
				r := require.New(t)
				// No changes, diff should be empty (or just whitespace).
				r.Empty(strings.TrimSpace(diff))
			},
		},
		"repo with staged changes": {
			setup: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				initGitRepo(t, dir)

				// Detect the default branch name (could be "main" or "master").
				defBranch := currentBranchName(t, dir)

				// Create a feature branch.
				cmd := exec.Command("git", "checkout", "-b", "jelmer/human-being")
				cmd.Dir = dir
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, "git checkout: %s", string(out))

				// Write a file and commit it.
				err = os.WriteFile(filepath.Join(dir, "mascot.go"), []byte("package greendale\n"), 0o644)
				require.NoError(t, err)

				addCommit := [][]string{
					{"git", "add", "mascot.go"},
					{"git", "commit", "-m", "Add Human Being mascot"},
				}
				for _, args := range addCommit {
					cmd := exec.Command(args[0], args[1:]...)
					cmd.Dir = dir
					cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2024-01-02T00:00:00Z", "GIT_COMMITTER_DATE=2024-01-02T00:00:00Z")
					out, err := cmd.CombinedOutput()
					require.NoError(t, err, "cmd %v: %s", args, string(out))
				}

				return dir, defBranch
			},
			check: func(t *testing.T, diff string) {
				r := require.New(t)
				r.Contains(diff, "mascot.go")
				r.Contains(diff, "package greendale")
			},
		},
		"uncommitted changes ignored": {
			setup: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				initGitRepo(t, dir)

				defBranch := currentBranchName(t, dir)

				// Create a feature branch (HEAD == main, no commits ahead).
				cmd := exec.Command("git", "checkout", "-b", "jelmer/senor-chang")
				cmd.Dir = dir
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, "git checkout: %s", string(out))

				// Write a file but do NOT commit — just leave it unstaged.
				err = os.WriteFile(filepath.Join(dir, "chang.go"), []byte("package greendale\n\nfunc ElTigre() {}\n"), 0o644)
				require.NoError(t, err)

				return dir, defBranch
			},
			check: func(t *testing.T, diff string) {
				r := require.New(t)
				r.Empty(strings.TrimSpace(diff), "uncommitted files should NOT appear in review diff")
			},
		},
		"only committed changes appear": {
			setup: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				initGitRepo(t, dir)

				defBranch := currentBranchName(t, dir)

				cmd := exec.Command("git", "checkout", "-b", "jelmer/paintball")
				cmd.Dir = dir
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, "git checkout: %s", string(out))

				// Committed change.
				err = os.WriteFile(filepath.Join(dir, "fort.go"), []byte("package greendale\n"), 0o644)
				require.NoError(t, err)

				for _, args := range [][]string{
					{"git", "add", "fort.go"},
					{"git", "commit", "-m", "Build the blanket fort"},
				} {
					cmd := exec.Command(args[0], args[1:]...)
					cmd.Dir = dir
					cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2024-01-02T00:00:00Z", "GIT_COMMITTER_DATE=2024-01-02T00:00:00Z")
					out, err := cmd.CombinedOutput()
					require.NoError(t, err, "cmd %v: %s", args, string(out))
				}

				// Uncommitted change (different file).
				err = os.WriteFile(filepath.Join(dir, "pillow.go"), []byte("package greendale\n\nfunc PillowFight() {}\n"), 0o644)
				require.NoError(t, err)

				return dir, defBranch
			},
			check: func(t *testing.T, diff string) {
				r := require.New(t)
				r.Contains(diff, "fort.go", "committed changes should be in diff")
				r.NotContains(diff, "pillow.go", "uncommitted changes should NOT be in diff")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			dir, baseBranch := tc.setup(t)
			diff, err := GetDiff(dir, baseBranch)
			if tc.wantErr {
				r.Error(err)
				if tc.wantErrMsg != "" {
					r.Contains(err.Error(), tc.wantErrMsg)
				}
				return
			}
			r.NoError(err)
			if tc.check != nil {
				tc.check(t, diff)
			}
		})
	}
}
