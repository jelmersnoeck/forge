package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// SquashResult holds the outcome of the post-review squash step.
type SquashResult struct {
	Squashed    bool   // true if commits were squashed
	CommitCount int    // number of commits before squash (0 if skipped)
	CommitSHA   string // new HEAD SHA after squash (empty if skipped)
	Error       error  // non-nil if squash failed (non-fatal)
}

// SquashBranchCommits squashes all commits on the current branch above
// the merge-base with the given base ref into a single commit.
// Returns SquashResult. Never fatal — errors are captured in the result.
func SquashBranchCommits(ctx context.Context, cwd string) SquashResult {
	// Detect base branch: origin/main → origin/master → main → master.
	baseBranch := detectSquashBase(ctx, cwd)
	if baseBranch == "" {
		return SquashResult{Error: fmt.Errorf("no base branch found (tried origin/main, origin/master, main, master)")}
	}

	// Check for uncommitted/staged changes.
	status, err := GitOutputCtx(ctx, cwd, "status", "--porcelain")
	if err != nil {
		return SquashResult{Error: fmt.Errorf("git status: %w", err)}
	}
	if status != "" {
		return SquashResult{Error: fmt.Errorf("uncommitted changes present, skipping squash")}
	}

	// Count commits on the branch.
	countStr, err := GitOutputCtx(ctx, cwd, "rev-list", "--count", baseBranch+"..HEAD")
	if err != nil {
		return SquashResult{Error: fmt.Errorf("rev-list --count: %w", err)}
	}
	count, err := strconv.Atoi(countStr)
	if err != nil {
		return SquashResult{Error: fmt.Errorf("parse commit count %q: %w", countStr, err)}
	}
	if count <= 1 {
		return SquashResult{} // no-op
	}

	// Collect commit messages (oldest first) for the squash message.
	logOutput, err := GitOutputCtx(ctx, cwd, "log", "--reverse", "--format=%s", baseBranch+"..HEAD")
	if err != nil {
		return SquashResult{Error: fmt.Errorf("git log: %w", err)}
	}
	messages := splitNonEmpty(logOutput)

	// Compute merge-base.
	mergeBase, err := GitOutputCtx(ctx, cwd, "merge-base", baseBranch, "HEAD")
	if err != nil {
		return SquashResult{Error: fmt.Errorf("merge-base %s HEAD: %w", baseBranch, err)}
	}

	// Soft reset to merge-base.
	if _, _, err := GitOutputFullCtx(ctx, cwd, "reset", "--soft", mergeBase); err != nil {
		return SquashResult{Error: fmt.Errorf("reset --soft %s: %w", mergeBase, err)}
	}

	// Build squash commit message and commit.
	msg := buildSquashMessage(messages)
	if _, stderr, err := GitOutputFullCtx(ctx, cwd, "commit", "-m", msg); err != nil {
		// Recovery: try to commit with a generic message so we don't leave
		// the repo in a half-reset state.
		if _, _, err2 := GitOutputFullCtx(ctx, cwd, "commit", "-m", "squash recovery"); err2 != nil {
			return SquashResult{
				CommitCount: count,
				Error:       fmt.Errorf("commit after soft reset failed (recovery also failed): %s: %w", stderr, err),
			}
		}
		return SquashResult{
			CommitCount: count,
			Error:       fmt.Errorf("commit failed, used recovery message: %s: %w", stderr, err),
		}
	}

	// Read new HEAD SHA.
	sha, err := GitOutputCtx(ctx, cwd, "rev-parse", "HEAD")
	if err != nil {
		// Squash succeeded but can't read SHA — still report success.
		return SquashResult{Squashed: true, CommitCount: count, Error: fmt.Errorf("rev-parse HEAD after squash: %w", err)}
	}

	return SquashResult{
		Squashed:    true,
		CommitCount: count,
		CommitSHA:   sha,
	}
}

// detectSquashBase finds the base ref using the same resolution order as
// the review system: origin/main → origin/master → main → master.
func detectSquashBase(ctx context.Context, cwd string) string {
	candidates := []string{"origin/main", "origin/master", "main", "master"}
	for _, c := range candidates {
		if _, err := GitOutputCtx(ctx, cwd, "rev-parse", "--verify", c); err == nil {
			return c
		}
	}
	return ""
}

// buildSquashMessage constructs the squash commit message.
//
//	First commit message on line 1.
//	If >1 message, appends "Also includes:" with a bulleted list.
func buildSquashMessage(messages []string) string {
	if len(messages) == 0 {
		return "squash"
	}
	if len(messages) == 1 {
		return messages[0]
	}

	var b strings.Builder
	b.WriteString(messages[0])
	b.WriteString("\n\nAlso includes:")
	for _, m := range messages[1:] {
		b.WriteString("\n- ")
		b.WriteString(m)
	}
	return b.String()
}

// splitNonEmpty splits on newlines and drops empty entries.
func splitNonEmpty(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
