package review

import (
	"fmt"
	"os/exec"
	"strings"
)

const maxDiffBytes = 100 * 1024 // 100KB

// GetDiff extracts the git diff for review.
//
// Resolution order for baseBranch:
//   - explicit value if non-empty
//   - "main" (if it exists)
//   - "master" (if it exists)
//   - falls back to "git diff HEAD"
func GetDiff(cwd, baseBranch string) (string, error) {
	if !isGitRepo(cwd) {
		return "", fmt.Errorf("not a git repository: %s", cwd)
	}

	var diff string
	var err error

	switch {
	case baseBranch != "":
		diff, err = gitDiff(cwd, baseBranch+"...HEAD")
	case branchExists(cwd, "main"):
		diff, err = gitDiff(cwd, "main...HEAD")
	case branchExists(cwd, "master"):
		diff, err = gitDiff(cwd, "master...HEAD")
	default:
		diff, err = gitDiff(cwd, "HEAD")
	}

	if err != nil {
		return "", err
	}

	return truncateDiff(diff), nil
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
