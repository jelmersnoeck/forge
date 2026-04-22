package tools

import (
	"bytes"
	"os/exec"
	"strings"
)

// GHAvailable reports whether the gh CLI is on PATH.
func GHAvailable() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// DetectDefaultBranch figures out the repo's default branch.
func DetectDefaultBranch(cwd string) string {
	// Try gh first — it knows the remote default
	if branch, err := GHOutput(cwd, "repo", "view", "--json", "defaultBranchRef", "--jq", ".defaultBranchRef.name"); err == nil && branch != "" {
		return branch
	}

	// Fallback: check common names
	for _, candidate := range []string{"main", "master"} {
		if RunGitCmd(cwd, "rev-parse", "--verify", candidate) == nil {
			return candidate
		}
	}

	return "main"
}

// RunGitCmd runs a git command and returns an error if it fails.
func RunGitCmd(cwd string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	return cmd.Run()
}

// GitOutput runs a git command and returns trimmed stdout.
func GitOutput(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// GitOutputFull runs a git command and returns stdout, stderr, and error.
func GitOutputFull(cwd string, args ...string) (string, string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

// GHOutput runs a gh command and returns trimmed stdout.
func GHOutput(cwd string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = cwd
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}
