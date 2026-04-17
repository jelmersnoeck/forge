package phase

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// gitIn runs a git command in the given directory and fails the test on error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Troy Barnes",
		"GIT_AUTHOR_EMAIL=troy@greendale.edu",
		"GIT_COMMITTER_NAME=Troy Barnes",
		"GIT_COMMITTER_EMAIL=troy@greendale.edu",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, string(out))
	return string(out)
}

func TestCheckStaleness(t *testing.T) {
	tests := map[string]struct {
		setup     func(t *testing.T) string // returns cwd for CheckStaleness
		wantStale bool
		wantPull  bool
		wantErr   bool
	}{
		"not a git repo": {
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			wantStale: false,
		},
		"no upstream": {
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				gitIn(t, dir, "init", "-b", "main")
				// Need a commit so HEAD exists
				greendale := filepath.Join(dir, "motto.txt")
				require.NoError(t, os.WriteFile(greendale, []byte("E Pluribus Anus"), 0644))
				gitIn(t, dir, "add", ".")
				gitIn(t, dir, "commit", "-m", "Go Human Beings!")
				return dir
			},
			wantStale: false,
		},
		"up to date": {
			setup: func(t *testing.T) string {
				root := t.TempDir()

				// bare "Greendale" remote
				bare := filepath.Join(root, "greendale.git")
				require.NoError(t, os.MkdirAll(bare, 0755))
				gitIn(t, bare, "init", "--bare", "-b", "main")

				// clone to local
				local := filepath.Join(root, "study-group")
				gitIn(t, root, "clone", bare, local)

				// make a commit and push so upstream has something
				require.NoError(t, os.WriteFile(
					filepath.Join(local, "paintball.txt"),
					[]byte("Streets ahead!"), 0644,
				))
				gitIn(t, local, "add", ".")
				gitIn(t, local, "commit", "-m", "Pierce says streets ahead")
				gitIn(t, local, "push")

				return local
			},
			wantStale: false,
		},
		"behind upstream": {
			setup: func(t *testing.T) string {
				root := t.TempDir()

				// bare "Greendale" remote
				bare := filepath.Join(root, "greendale.git")
				require.NoError(t, os.MkdirAll(bare, 0755))
				gitIn(t, bare, "init", "--bare", "-b", "main")

				// first clone — the one we'll test
				local := filepath.Join(root, "study-room-f")
				gitIn(t, root, "clone", bare, local)

				// seed commit from local so main exists on remote
				require.NoError(t, os.WriteFile(
					filepath.Join(local, "coolcoolcool.txt"),
					[]byte("Cool. Cool cool cool."), 0644,
				))
				gitIn(t, local, "add", ".")
				gitIn(t, local, "commit", "-m", "Abed's catchphrase")
				gitIn(t, local, "push")

				// second clone simulates another contributor
				local2 := filepath.Join(root, "dreamatorium")
				gitIn(t, root, "clone", bare, local2)

				// push a new commit from local2
				require.NoError(t, os.WriteFile(
					filepath.Join(local2, "timeline.txt"),
					[]byte("There are six seasons and a movie"), 0644,
				))
				gitIn(t, local2, "add", ".")
				gitIn(t, local2, "commit", "-m", "The darkest timeline")
				gitIn(t, local2, "push")

				// local is now 1 behind upstream
				return local
			},
			wantStale: true,
			wantPull:  true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			cwd := tc.setup(t)

			result := CheckStaleness(cwd)

			r.Equal(tc.wantStale, result.Stale, "Stale")
			r.Equal(tc.wantPull, result.Pulled, "Pulled")

			if tc.wantErr {
				r.Error(result.Error)
			} else {
				r.NoError(result.Error)
			}

			if tc.wantPull {
				r.Greater(result.Behind, 0, "Behind should be > 0 when pulled")
			}
		})
	}
}
