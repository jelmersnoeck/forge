package tools

import "os/exec"

// IsGitIgnored reports whether relPath is excluded by a .gitignore rule in the
// git repository rooted at (or above) cwd. It shells out to
// `git check-ignore -q -- <relPath>`:
//
//	exit 0 → path is ignored        → true
//	exit 1 → path is not ignored    → false
//	exit 2 → error (or not a repo)  → false (treated as not ignored)
//
// Returning false on error means a missing git, a non-repo dir, or any other
// failure never wrongly suppresses commits.
func IsGitIgnored(cwd, relPath string) bool {
	cmd := exec.Command("git", "check-ignore", "-q", "--", relPath)
	cmd.Dir = cwd
	err := cmd.Run()
	if err == nil {
		return true // exit 0: ignored
	}
	// exit 1 (not ignored), exit 2 (error/not a repo), or git missing → false.
	return false
}
