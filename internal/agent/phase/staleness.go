package phase

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// StalenessResult describes the freshness state of the current branch.
type StalenessResult struct {
	Stale  bool  // true if behind upstream
	Behind int   // number of commits behind
	Pulled bool  // true if auto-pull succeeded
	Error  error // non-nil if pull failed (conflicts)
}

// CheckStaleness fetches from remote and reports how far behind HEAD is.
// Safe to call when no upstream exists (returns not stale).
// Safe to call with uncommitted changes (skips pull, warns only).
func CheckStaleness(cwd string) StalenessResult {
	// Step 1: fetch quietly. If it fails (no remote, offline), not stale.
	fetch := exec.Command("git", "fetch", "--quiet")
	fetch.Dir = cwd
	if err := fetch.Run(); err != nil {
		return StalenessResult{}
	}

	// Step 2: count commits behind upstream
	revList := exec.Command("git", "rev-list", "--count", "HEAD..@{upstream}")
	revList.Dir = cwd
	out, err := revList.Output()
	if err != nil {
		// No upstream tracking branch — not stale.
		return StalenessResult{}
	}

	behind, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || behind == 0 {
		return StalenessResult{}
	}

	result := StalenessResult{
		Stale:  true,
		Behind: behind,
	}

	// Step 3: check for uncommitted changes before pulling
	if hasUncommittedChanges(cwd) {
		result.Error = fmt.Errorf("branch is %d commits behind upstream but has uncommitted changes — skipping auto-pull", behind)
		return result
	}

	// Step 4: pull --rebase
	pull := exec.Command("git", "pull", "--rebase", "--quiet")
	pull.Dir = cwd
	if err := pull.Run(); err != nil {
		result.Error = fmt.Errorf("pull --rebase failed: %w (resolve conflicts manually)", err)
		return result
	}

	result.Pulled = true
	return result
}

// hasUncommittedChanges returns true if the working tree has staged or unstaged changes.
func hasUncommittedChanges(cwd string) bool {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}
