// Package gitutil provides git operations for the governed build loop.
//
// All operations shell out to the git CLI rather than using a Go git library,
// keeping dependencies minimal and behavior consistent with developer workflows.
package gitutil

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Git provides git operations scoped to a working directory.
type Git struct {
	WorkDir string
}

// New creates a new Git instance for the given working directory.
func New(workDir string) *Git {
	return &Git{WorkDir: workDir}
}

// run executes a git command in the working directory and returns its output.
func (g *Git) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.WorkDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// CreateBranch creates and checks out a new git branch.
func (g *Git) CreateBranch(ctx context.Context, name string) error {
	_, err := g.run(ctx, "checkout", "-b", name)
	if err != nil {
		return fmt.Errorf("creating branch %s: %w", name, err)
	}
	return nil
}

// CurrentBranch returns the name of the current git branch.
func (g *Git) CurrentBranch(ctx context.Context) (string, error) {
	branch, err := g.run(ctx, "branch", "--show-current")
	if err != nil {
		return "", fmt.Errorf("getting current branch: %w", err)
	}
	return branch, nil
}

// Diff returns the diff between the working tree and the given ref.
func (g *Git) Diff(ctx context.Context, ref string) (string, error) {
	diff, err := g.run(ctx, "diff", ref)
	if err != nil {
		return "", fmt.Errorf("diffing against %s: %w", ref, err)
	}
	return diff, nil
}

// DiffStaged returns the diff of staged changes.
func (g *Git) DiffStaged(ctx context.Context) (string, error) {
	diff, err := g.run(ctx, "diff", "--staged")
	if err != nil {
		return "", fmt.Errorf("getting staged diff: %w", err)
	}
	return diff, nil
}

// DiffRef returns the diff between two refs (e.g., "main...HEAD").
func (g *Git) DiffRef(ctx context.Context, ref string) (string, error) {
	diff, err := g.run(ctx, "diff", ref)
	if err != nil {
		return "", fmt.Errorf("diffing ref %s: %w", ref, err)
	}
	return diff, nil
}

// AddAll stages all changes in the working tree.
func (g *Git) AddAll(ctx context.Context) error {
	_, err := g.run(ctx, "add", "-A")
	if err != nil {
		return fmt.Errorf("staging all changes: %w", err)
	}
	return nil
}

// Commit creates a new commit with the given message.
func (g *Git) Commit(ctx context.Context, msg string) error {
	_, err := g.run(ctx, "commit", "-m", msg)
	if err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}

// Push pushes the given branch to the origin remote.
func (g *Git) Push(ctx context.Context, branch string) error {
	_, err := g.run(ctx, "push", "-u", "origin", branch)
	if err != nil {
		return fmt.Errorf("pushing branch %s: %w", branch, err)
	}
	return nil
}

// FormatBranch formats a branch name using a template pattern and data.
// The pattern uses Go template syntax, e.g., "forge/{{.Tracker}}-{{.IssueID}}".
func FormatBranch(pattern string, data map[string]string) string {
	result := pattern
	for key, value := range data {
		result = strings.ReplaceAll(result, "{{."+key+"}}", value)
	}
	return result
}
