package phase

import (
	"context"
	"fmt"
	"strings"

	"github.com/jelmersnoeck/forge/internal/tools"
)

// CommentIssueFunc posts a comment on a GitHub issue by number in repoRoot's repo.
type CommentIssueFunc func(ctx context.Context, repoRoot string, number int, body string) error

// CloseParentFunc closes the parent GitHub issue by number in repoRoot's repo.
type CloseParentFunc func(ctx context.Context, repoRoot string, number int) error

// phaseRecord captures the outcome of a single phase for the completion summary
// and progress comments. PRURL is empty when no PR was produced.
type phaseRecord struct {
	Number int    // sub-issue number (0 = unknown)
	Title  string // sub-issue title
	PRURL  string // PR URL, empty if none
	Merged bool   // whether the PR merged (only set when WaitForMerge is on)
}

// formatProgressComment renders the per-phase progress comment posted on the
// parent issue after a phase's PR merges, e.g.:
//
//	Phase 2/6 complete: **provider-aware-lightweight** — PR #217 merged.
//
// When the PR URL is unknown the PR clause is omitted.
func formatProgressComment(index, total int, rec phaseRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Phase %d/%d complete: **%s**", index, total, rec.Title)
	if prNum := extractPRNumberFromURL(rec.PRURL); prNum > 0 {
		fmt.Fprintf(&b, " — PR #%d merged.", prNum)
	} else {
		b.WriteString(".")
	}
	return b.String()
}

// formatCompletionSummary renders the final summary comment posted on the parent
// issue once every phase is done, e.g.:
//
//	All 6 phases complete:
//	- #221 → PR #215 (merged)
//	- #222 → PR #216 (merged)
func formatCompletionSummary(records []phaseRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "All %d phases complete:\n", len(records))
	for _, rec := range records {
		b.WriteString("- ")
		if rec.Number > 0 {
			fmt.Fprintf(&b, "#%d", rec.Number)
		} else {
			fmt.Fprintf(&b, "%s", rec.Title)
		}
		if prNum := extractPRNumberFromURL(rec.PRURL); prNum > 0 {
			state := "open"
			if rec.Merged {
				state = "merged"
			}
			fmt.Fprintf(&b, " → PR #%d (%s)", prNum, state)
		} else {
			b.WriteString(" → no PR")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatPhaseFailureComment renders the comment posted on the parent issue when
// a phase fails, naming the sub-issue and linking the PR when one exists.
func formatPhaseFailureComment(index, total int, rec phaseRecord, cause error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Phase %d/%d failed: **%s**", index, total, rec.Title)
	if rec.Number > 0 {
		fmt.Fprintf(&b, " (#%d)", rec.Number)
	}
	b.WriteString(".\n\n")
	if cause != nil {
		fmt.Fprintf(&b, "Error: %s\n", cause.Error())
	}
	if rec.PRURL != "" {
		fmt.Fprintf(&b, "\nPR: %s\n", rec.PRURL)
	}
	return strings.TrimRight(b.String(), "\n")
}

// ghCommentIssue posts a comment on a GitHub issue via `gh issue comment`.
func ghCommentIssue(ctx context.Context, repoRoot string, number int, body string) error {
	if !tools.GHAvailable() {
		return fmt.Errorf("gh CLI not installed (https://cli.github.com/)")
	}
	if _, err := tools.GHOutputCtx(ctx, repoRoot, "issue", "comment", fmt.Sprintf("%d", number), "--body", body); err != nil {
		return fmt.Errorf("gh issue comment %d: %w", number, err)
	}
	return nil
}

// ghCloseParent closes the parent GitHub issue via `gh issue close`. It is a
// distinct func from ghCloseIssue only for clarity at the call site; behavior is
// identical.
func ghCloseParent(ctx context.Context, repoRoot string, number int) error {
	return ghCloseIssue(ctx, repoRoot, number)
}
