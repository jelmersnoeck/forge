package phase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/tools"
)

// Default poll interval and timeout for waiting on a PR to merge.
const (
	defaultMergePollInterval = 30 * time.Second
	defaultMergeTimeout      = 2 * time.Hour
)

// PR review decision states returned by `gh pr view --json state`.
const (
	prStateOpen   = "OPEN"
	prStateMerged = "MERGED"
	prStateClosed = "CLOSED"
)

// prURLNumberRe matches /pull/<number> in a GitHub PR URL.
var prURLNumberRe = regexp.MustCompile(`/pull/(\d+)`)

// extractPRNumberFromURL parses the PR number from a GitHub PR URL.
// Returns 0 if no number is found.
func extractPRNumberFromURL(s string) int {
	m := prURLNumberRe.FindStringSubmatch(strings.TrimSpace(s))
	if len(m) != 2 {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// prStatus is the subset of `gh pr view --json state,mergeable,mergeStateStatus`
// that the merge poller cares about.
type prStatus struct {
	State            string `json:"state"`            // OPEN | MERGED | CLOSED
	Mergeable        string `json:"mergeable"`        // MERGEABLE | CONFLICTING | UNKNOWN
	MergeStateStatus string `json:"mergeStateStatus"` // CLEAN | DIRTY | BLOCKED | BEHIND | ...
}

// PRStatusFunc queries the status of a PR by number in cwd. Injectable so the
// poller is testable without a live `gh`.
type PRStatusFunc func(ctx context.Context, cwd string, prNumber int) (prStatus, error)

// RebaseFunc rebases the PR branch in cwd onto origin/<base> and force-pushes.
// Injectable so conflict-resolution behavior is testable without git.
type RebaseFunc func(ctx context.Context, cwd, base string) error

// MergeWaitOpts configures WaitForMerge. PRNumber and Cwd are required; the
// remaining fields default to real implementations / sensible values when zero.
type MergeWaitOpts struct {
	// PRNumber is the GitHub PR number to poll. REQUIRED (> 0).
	PRNumber int

	// Cwd is the worktree directory the PR branch lives in (for rebase + gh).
	Cwd string

	// BaseBranch is the branch the PR targets (for conflict rebase).
	BaseBranch string

	// PollInterval is the gap between status polls. Defaults to 30s when <= 0.
	PollInterval time.Duration

	// Timeout is the overall deadline. Defaults to 2h when <= 0.
	Timeout time.Duration

	// PRStatus queries PR status. Defaults to ghPRStatus when nil.
	PRStatus PRStatusFunc

	// Rebase resolves merge conflicts. Defaults to gitRebaseAndForcePush when nil.
	Rebase RebaseFunc
}

// WaitForMerge polls a PR until it merges, closes, or the timeout elapses.
//
//	┌──────────────┐  poll every interval
//	│ gh pr view   │◄────────────────┐
//	└──────┬───────┘                 │
//	       │                         │
//	   ┌───┴────┬─────────┐          │
//	   ▼        ▼         ▼          │
//	 MERGED   CLOSED   OPEN ─────────┘
//	   │        │         │
//	   ▼        ▼         └─ if conflicting: rebase + force-push, keep polling
//	  nil     error
//
// Returns:
//   - nil when the PR reaches MERGED.
//   - an error when the PR is CLOSED (not merged), the timeout elapses, the
//     context is cancelled, or the PR number is invalid.
//
// On each poll, if the PR is OPEN but has merge conflicts (Mergeable ==
// CONFLICTING or mergeStateStatus == DIRTY), WaitForMerge attempts a rebase +
// force-push to clear them; a rebase failure is logged but does not halt the
// wait (a maintainer may resolve it manually before the next poll).
func WaitForMerge(ctx context.Context, opts MergeWaitOpts) error {
	if opts.PRNumber <= 0 {
		return fmt.Errorf("merge-wait: invalid PR number %d", opts.PRNumber)
	}

	interval := opts.PollInterval
	if interval <= 0 {
		interval = defaultMergePollInterval
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultMergeTimeout
	}
	statusFn := opts.PRStatus
	if statusFn == nil {
		statusFn = ghPRStatus
	}
	rebaseFn := opts.Rebase
	if rebaseFn == nil {
		rebaseFn = gitRebaseAndForcePush
	}

	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Poll once immediately, then on each tick.
	for {
		st, err := statusFn(deadlineCtx, opts.Cwd, opts.PRNumber)
		if err != nil {
			slog.Warn("merge-wait: status query failed; will retry",
				"pr", opts.PRNumber, "error", err)
		} else {
			switch st.State {
			case prStateMerged:
				return nil
			case prStateClosed:
				return fmt.Errorf("merge-wait: PR #%d was closed without merging", opts.PRNumber)
			case prStateOpen:
				if hasMergeConflict(st) {
					if rerr := rebaseFn(deadlineCtx, opts.Cwd, opts.BaseBranch); rerr != nil {
						slog.Warn("merge-wait: conflict rebase failed; awaiting manual resolution",
							"pr", opts.PRNumber, "error", rerr)
					}
				}
			default:
				slog.Warn("merge-wait: unknown PR state; will retry",
					"pr", opts.PRNumber, "state", st.State)
			}
		}

		select {
		case <-deadlineCtx.Done():
			if cerr := ctx.Err(); cerr != nil {
				return fmt.Errorf("merge-wait: PR #%d cancelled: %w", opts.PRNumber, cerr)
			}
			return fmt.Errorf("merge-wait: PR #%d not merged within %s", opts.PRNumber, timeout)
		case <-ticker.C:
		}
	}
}

// hasMergeConflict reports whether a PR's status indicates merge conflicts.
func hasMergeConflict(st prStatus) bool {
	return st.Mergeable == "CONFLICTING" || st.MergeStateStatus == "DIRTY"
}

// ghPRStatus queries a PR's merge status via `gh pr view`.
func ghPRStatus(ctx context.Context, cwd string, prNumber int) (prStatus, error) {
	out, err := tools.GHOutputCtx(ctx, cwd, "pr", "view", strconv.Itoa(prNumber),
		"--json", "state,mergeable,mergeStateStatus")
	if err != nil {
		return prStatus{}, fmt.Errorf("gh pr view %d: %w", prNumber, err)
	}
	var st prStatus
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		return prStatus{}, fmt.Errorf("parse gh pr view output: %w", err)
	}
	return st, nil
}

// gitRebaseAndForcePush fetches origin, rebases the current branch onto
// origin/<base>, and force-pushes with --force-with-lease. Aborts the rebase on
// conflict so the working tree is left clean for the next attempt.
func gitRebaseAndForcePush(ctx context.Context, cwd, base string) error {
	if base == "" {
		base = detectDefaultBranchSafe(cwd)
	}

	if _, stderr, err := tools.GitOutputFullCtx(ctx, cwd, "fetch", "origin", base); err != nil {
		return fmt.Errorf("fetch origin/%s: %s", base, stderr)
	}

	if _, stderr, err := tools.GitOutputFullCtx(ctx, cwd, "rebase", "origin/"+base); err != nil {
		_ = tools.RunGitCmd(cwd, "rebase", "--abort")
		return fmt.Errorf("rebase onto origin/%s: %s", base, stderr)
	}

	if _, stderr, err := tools.GitOutputFullCtx(ctx, cwd, "push", "--force-with-lease", "origin", "HEAD"); err != nil {
		return fmt.Errorf("force-push: %s", stderr)
	}
	return nil
}

// pullMain fetches origin and fast-forwards the local base branch in repoRoot.
// Used between phases so each subsequent phase branches from a base that
// includes all prior merged work.
func pullMain(ctx context.Context, repoRoot, base string) error {
	if base == "" {
		base = detectDefaultBranchSafe(repoRoot)
	}
	if _, stderr, err := tools.GitOutputFullCtx(ctx, repoRoot, "fetch", "origin", base); err != nil {
		return fmt.Errorf("fetch origin/%s: %s", base, stderr)
	}
	// Update the local base ref to match origin without requiring a checkout.
	// A worktree may have the base branch checked out elsewhere, so update the
	// ref directly rather than `git pull` (which needs the branch checked out).
	if _, stderr, err := tools.GitOutputFullCtx(ctx, repoRoot, "fetch", "origin", base+":"+base); err != nil {
		// Non-fatal: the next worktree still branches off origin/<base> via
		// CreateWorktree, but log so a divergence is visible.
		slog.Warn("pull-main: could not fast-forward local base ref",
			"base", base, "error", stderr)
	}
	return nil
}
