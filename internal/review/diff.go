package review

import (
	"fmt"
	"os/exec"
	"strings"
)

const maxDiffBytes = 100 * 1024 // 100KB

// GetDiff extracts the committed changes on the current branch relative to
// the base branch. Only committed work (base...HEAD) is included — uncommitted
// and untracked changes are ignored so reviews stay focused on the PR delta.
//
// Resolution order for baseBranch:
//   - explicit value if non-empty
//   - "origin/main"  (remote tracking — always up to date)
//   - "origin/master"
//   - "main"  (local fallback)
//   - "master"
//   - empty string (no base found — returns empty diff)
//
// Remote refs are preferred because local main/master may be stale in worktrees,
// causing the review to include already-merged commits.
func GetDiff(cwd, baseBranch string) (string, error) {
	if !isGitRepo(cwd) {
		return "", fmt.Errorf("not a git repository: %s", cwd)
	}

	if baseBranch == "" {
		baseBranch = detectBaseBranch(cwd)
	}

	if baseBranch == "" {
		return "", nil
	}

	diff, err := gitDiff(cwd, baseBranch+"...HEAD")
	if err != nil {
		return "", err
	}

	return truncateDiff(diff), nil
}

// detectBaseBranch picks the best available base ref.
//
//	origin/main → origin/master → main → master → ""
func detectBaseBranch(cwd string) string {
	candidates := []string{"origin/main", "origin/master", "main", "master"}
	for _, c := range candidates {
		if branchExists(cwd, c) {
			return c
		}
	}
	return ""
}

func isGitRepo(cwd string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = cwd
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func branchExists(cwd, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", branch)
	cmd.Dir = cwd
	return cmd.Run() == nil
}

func gitDiff(cwd, diffSpec string) (string, error) {
	args := []string{"diff", "--no-color"}
	args = append(args, strings.Fields(diffSpec)...)
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff %s: %w", diffSpec, err)
	}
	return string(out), nil
}

// GetHeadSHA returns the current HEAD commit SHA.
func GetHeadSHA(cwd string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// GetIncrementalDiff returns the diff between a previous SHA and HEAD.
// Used for review cycles after the first. Uses two-dot diff (prevSHA..HEAD)
// to get exactly what changed between the two points.
func GetIncrementalDiff(cwd, prevSHA string) (string, error) {
	diff, err := gitDiff(cwd, prevSHA+"..HEAD")
	if err != nil {
		return "", err
	}
	return truncateDiff(diff), nil
}

// truncateDiff keeps head and tail of a diff if it exceeds maxDiffBytes.
//
//	┌──────────── head (60%) ────────────┐
//	│ ...truncated N bytes...            │
//	└──────────── tail (40%) ────────────┘
func truncateDiff(diff string) string {
	if len(diff) <= maxDiffBytes {
		return diff
	}

	headSize := maxDiffBytes * 60 / 100
	tailSize := maxDiffBytes * 40 / 100
	truncated := len(diff) - headSize - tailSize

	return diff[:headSize] +
		fmt.Sprintf("\n\n... [truncated %d bytes] ...\n\n", truncated) +
		diff[len(diff)-tailSize:]
}
