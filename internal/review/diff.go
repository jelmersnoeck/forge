package review

import (
	"fmt"
	"os/exec"
	"strings"
)

const maxDiffBytes = 100 * 1024 // 100KB

// GetDiff extracts the git diff for review.
//
// Combines two diffs:
//  1. committed changes: base...HEAD (what's been committed on the branch)
//  2. uncommitted changes: working tree vs HEAD (staged + unstaged + untracked)
//
// Resolution order for baseBranch:
//   - explicit value if non-empty
//   - "main" (if it exists)
//   - "master" (if it exists)
//   - falls back to HEAD only (uncommitted changes)
func GetDiff(cwd, baseBranch string) (string, error) {
	if !isGitRepo(cwd) {
		return "", fmt.Errorf("not a git repository: %s", cwd)
	}

	var committedDiff string
	var err error

	switch {
	case baseBranch != "":
		committedDiff, err = gitDiff(cwd, baseBranch+"...HEAD")
	case branchExists(cwd, "main"):
		committedDiff, err = gitDiff(cwd, "main...HEAD")
	case branchExists(cwd, "master"):
		committedDiff, err = gitDiff(cwd, "master...HEAD")
	}
	if err != nil {
		return "", err
	}

	// Also grab uncommitted changes (staged + unstaged on tracked files).
	uncommittedDiff, err := gitDiff(cwd, "HEAD")
	if err != nil {
		return "", err
	}

	// Grab untracked files and generate diffs for them too.
	untrackedDiff, err := untrackedFilesDiff(cwd)
	if err != nil {
		return "", err
	}

	diff := committedDiff
	for _, extra := range []string{uncommittedDiff, untrackedDiff} {
		if extra == "" {
			continue
		}
		if diff != "" {
			diff += "\n"
		}
		diff += extra
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

// untrackedFilesDiff generates a unified diff for all untracked files
// (excluding gitignored ones) so they appear in review output.
func untrackedFilesDiff(cwd string) (string, error) {
	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git ls-files: %w", err)
	}

	files := strings.TrimSpace(string(out))
	if files == "" {
		return "", nil
	}

	// Use git diff --no-index to generate proper unified diffs for each file.
	var sb strings.Builder
	for _, f := range strings.Split(files, "\n") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		diffCmd := exec.Command("git", "diff", "--no-color", "--no-index", "--", "/dev/null", f)
		diffCmd.Dir = cwd
		// git diff --no-index exits 1 when files differ, which is always the case here.
		diffOut, _ := diffCmd.Output()
		if len(diffOut) > 0 {
			sb.Write(diffOut)
		}
	}
	return sb.String(), nil
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
