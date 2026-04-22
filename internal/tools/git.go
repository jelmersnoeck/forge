package tools

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync"
)

var (
	ghOnce      sync.Once
	ghAvailable bool
)

// GHAvailable reports whether the gh CLI is on PATH.
// Result is cached after the first call.
func GHAvailable() bool {
	ghOnce.Do(func() {
		_, err := exec.LookPath("gh")
		ghAvailable = err == nil
	})
	return ghAvailable
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

// GitOutputCtx runs a git command with context and returns trimmed stdout.
func GitOutputCtx(ctx context.Context, cwd string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// GitOutputFullCtx runs a git command with context and returns stdout, stderr, and error.
func GitOutputFullCtx(ctx context.Context, cwd string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

// GHOutputCtx runs a gh command with context and returns trimmed stdout.
func GHOutputCtx(ctx context.Context, cwd string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = cwd
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ValidateBranchName checks that a git branch name is safe to use in commands.
// Rejects names containing shell metacharacters or suspicious patterns.
func ValidateBranchName(branch string) bool {
	if branch == "" {
		return false
	}
	// Reject any branch name containing characters that could be
	// used for argument injection or shell metacharacters.
	for _, c := range branch {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '/' || c == '-' || c == '_' || c == '.':
		default:
			return false
		}
	}
	// Reject names starting with '-' (argument injection).
	if strings.HasPrefix(branch, "-") {
		return false
	}
	return true
}
