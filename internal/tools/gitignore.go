package tools

import (
	"errors"
	"log"
	"os/exec"
	"strings"
)

// IsGitIgnored reports whether relPath is excluded by a .gitignore rule in the
// git repository rooted at (or above) cwd. It shells out to
// `git check-ignore -q -- <relPath>`:
//
//	exit 0 → path is ignored        → true
//	exit 1 → path is not ignored    → false
//	exit 2 → error (or not a repo)  → false (treated as not ignored)
//
// Returning false on error means a missing git, a non-repo dir, or any other
// failure never wrongly suppresses commits. Unexpected errors (exit 2, git
// missing, etc.) are logged at debug level to aid troubleshooting; the routine
// exit-1 "not ignored" case is silent.
func IsGitIgnored(cwd, relPath string) bool {
	// Defensive input validation. relPath is internally controlled, but reject
	// empty/NUL-bearing values so a malformed path never reaches git. The `--`
	// separator below already prevents argument injection (anything after it is
	// treated as a pathspec, not a flag).
	if relPath == "" || strings.ContainsRune(relPath, '\x00') {
		return false
	}

	cmd := exec.Command("git", "check-ignore", "-q", "--", relPath)
	cmd.Dir = cwd
	err := cmd.Run()
	if err == nil {
		return true // exit 0: ignored
	}

	// exit 1 means "not ignored" — the normal, expected case. Anything else
	// (exit 2, git missing, etc.) is an unexpected failure worth surfacing.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false
	}
	log.Printf("[gitignore] check-ignore for %q in %q failed (treating as not ignored): %v", relPath, cwd, err)
	return false
}
