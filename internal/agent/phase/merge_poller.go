package phase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/tools"
)

// Default merge-poll timing. Both are overridable via WaitForMergeOpts.
const (
	defaultMergePollInterval = 30 * time.Second
	defaultMergeTimeout      = 2 * time.Hour
)

// PRState is the parsed subset of `gh pr view --json state,mergedAt` output.
// State is one of "OPEN", "MERGED", "CLOSED" (GitHub's uppercase enum).
type PRState struct {
	State    string `json:"state"`
	MergedAt string `json:"mergedAt"`
}

// merged reports whether the PR has been merged. GitHub reports a merged PR as
// state "MERGED"; mergedAt is a belt-and-suspenders fallback for older `gh`
// versions that report "CLOSED" with a non-empty mergedAt.
func (s PRState) merged() bool {
	return strings.EqualFold(s.State, "MERGED") || strings.TrimSpace(s.MergedAt) != ""
}

// closed reports whether the PR is closed without being merged.
func (s PRState) closed() bool {
	return strings.EqualFold(s.State, "CLOSED") && !s.merged()
}

// known reports whether State is a recognized GitHub PR state enum value
// ("OPEN", "MERGED", "CLOSED"). An unrecognized value (empty or from an
// evolving API) is treated as OPEN by the poller but logged via this check.
func (s PRState) known() bool {
	switch strings.ToUpper(strings.TrimSpace(s.State)) {
	case "OPEN", "MERGED", "CLOSED":
		return true
	default:
		return false
	}
}

// PRStateFunc fetches the current state of a PR. The real implementation calls
// `gh pr view <number> --json state,mergedAt`; tests inject a fake that returns
// canned states. cwd is the worktree the PR's branch lives in.
type PRStateFunc func(ctx context.Context, cwd string, prNumber int) (PRState, error)

// RebaseFunc attempts to bring a PR's branch up to date with its base (fetch +
// rebase + force-push). It is invoked when a PR is detected as not mergeable.
// The real implementation reuses the fetch/rebase/push logic; tests inject a
// fake. A nil error means the rebase succeeded and polling continues. base is
// the PR's base branch (empty → the rebase falls back to the repo default).
type RebaseFunc func(ctx context.Context, cwd, base string) error

// WaitForMergeOpts configures a single PR merge wait.
type WaitForMergeOpts struct {
	// CWD is the worktree directory the PR's branch lives in (for gh/git).
	CWD string

	// PRNumber is the GitHub PR number to poll.
	PRNumber int

	// BaseBranch is the PR's base branch, used by the rebase step to fetch and
	// rebase onto the correct origin ref. Empty → rebasePRBranch falls back to
	// the repo's detected default branch.
	BaseBranch string

	// PollInterval is the gap between state polls. Defaults to 30s when zero.
	PollInterval time.Duration

	// Timeout is the maximum total wait before halting. Defaults to 2h when zero.
	Timeout time.Duration

	// QueryState fetches the PR state. Defaults to ghPRState when nil.
	QueryState PRStateFunc

	// Rebase attempts to resolve a stale/conflicted branch. Defaults to
	// rebasePRBranch when nil. Called at most once per detected conflict.
	Rebase RebaseFunc

	// Emit receives progress events (best-effort; may be nil).
	Emit func(content string)
}

// MergeOutcome is the terminal result of WaitForMerge.
type MergeOutcome int

const (
	// MergeOutcomeMerged means the PR was merged — the pipeline may continue.
	MergeOutcomeMerged MergeOutcome = iota
	// MergeOutcomeClosed means the PR was closed unmerged — the pipeline halts.
	MergeOutcomeClosed
	// MergeOutcomeTimeout means the timeout elapsed before merge — halt.
	MergeOutcomeTimeout
)

func (o MergeOutcome) String() string {
	switch o {
	case MergeOutcomeMerged:
		return "merged"
	case MergeOutcomeClosed:
		return "closed"
	case MergeOutcomeTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}

// WaitForMerge polls a PR until it merges, closes, or the timeout elapses.
//
//	┌─────────────────────────────┐
//	│ query state (gh pr view)    │◄──────────┐
//	└──────────────┬──────────────┘           │
//	      MERGED   │   CLOSED   OPEN          │ tick (PollInterval)
//	   ┌───────────┼───────────┐               │
//	   ▼           ▼           ▼               │
//	 merged     halt(closed)  mergeable? ──no──► rebase+force-push
//	   │                       │  yes            │
//	   ▼                       └─────────────────┘
//	 continue
//
// A MERGED PR returns (MergeOutcomeMerged, nil). A CLOSED-unmerged PR returns
// (MergeOutcomeClosed, error). Timeout returns (MergeOutcomeTimeout, error).
// Context cancellation returns (MergeOutcomeTimeout, ctx.Err()). Query errors
// are logged and retried on the next tick (transient gh/network failures must
// not abort a long wait); only a terminal state or the timeout ends the loop.
func WaitForMerge(ctx context.Context, opts WaitForMergeOpts) (MergeOutcome, error) {
	interval := opts.PollInterval
	if interval <= 0 {
		interval = defaultMergePollInterval
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultMergeTimeout
	}
	query := opts.QueryState
	if query == nil {
		query = ghPRState
	}
	rebase := opts.Rebase
	if rebase == nil {
		rebase = rebasePRBranch
	}

	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	emitMerge(opts.Emit, fmt.Sprintf("Waiting for PR #%d to merge (interval %s, timeout %s)\n",
		opts.PRNumber, interval, timeout))

	// checkAndMaybeRebase queries the PR state once. On a terminal state it
	// returns (outcome, true). On an OPEN poll it rebases (best-effort, per
	// spec: rebase + force-push on every OPEN poll) and returns (0, false).
	checkAndMaybeRebase := func() (MergeOutcome, bool) {
		st, err := query(deadlineCtx, opts.CWD, opts.PRNumber)
		if err != nil {
			slog.Warn("merge-poller: query PR state failed; will retry",
				"pr", opts.PRNumber, "error", err)
			return 0, false
		}
		switch {
		case st.merged():
			slog.Info("merge-poller: PR reached terminal state",
				"pr", opts.PRNumber, "outcome", MergeOutcomeMerged.String())
			emitMerge(opts.Emit, fmt.Sprintf("PR #%d merged\n", opts.PRNumber))
			return MergeOutcomeMerged, true
		case st.closed():
			slog.Info("merge-poller: PR reached terminal state",
				"pr", opts.PRNumber, "outcome", MergeOutcomeClosed.String())
			emitMerge(opts.Emit, fmt.Sprintf("PR #%d closed without merging\n", opts.PRNumber))
			return MergeOutcomeClosed, true
		default:
			// Still OPEN — or an unexpected/unknown state from an evolving
			// API, which we treat as OPEN. Log the latter so upstream API
			// changes surface instead of silently looping until timeout.
			if !st.known() {
				slog.Warn("merge-poller: unrecognized PR state, treating as OPEN",
					"pr", opts.PRNumber, "state", st.State)
			}
			// Attempt a rebase to clear any merge conflict so the PR can
			// become mergeable. Best-effort: failures are logged and we keep
			// polling.
			if err := rebase(deadlineCtx, opts.CWD, opts.BaseBranch); err != nil {
				slog.Warn("merge-poller: rebase attempt failed; continuing to poll",
					"pr", opts.PRNumber, "error", err)
			}
			return 0, false
		}
	}

	// Probe once immediately so an already-merged PR returns without waiting a
	// full interval.
	if outcome, done := checkAndMaybeRebase(); done {
		return mergeResult(outcome, opts.PRNumber)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-deadlineCtx.Done():
			// Distinguish a genuine timeout (the internal deadline expired)
			// from a parent cancellation. A parent cancel propagates to
			// ctx.Err(); a pure timeout leaves ctx.Err() nil and surfaces as
			// deadlineCtx.Err() == context.DeadlineExceeded.
			if ctx.Err() != nil {
				slog.Info("merge-poller: wait cancelled by parent context",
					"pr", opts.PRNumber, "outcome", MergeOutcomeTimeout.String())
				return MergeOutcomeTimeout, fmt.Errorf("merge-poller: cancelled waiting for PR #%d: %w", opts.PRNumber, ctx.Err())
			}
			slog.Info("merge-poller: PR merge wait timed out",
				"pr", opts.PRNumber, "outcome", MergeOutcomeTimeout.String(), "timeout", timeout)
			emitMerge(opts.Emit, fmt.Sprintf("PR #%d merge wait timed out after %s\n", opts.PRNumber, timeout))
			return MergeOutcomeTimeout, fmt.Errorf("merge-poller: timed out after %s waiting for PR #%d to merge", timeout, opts.PRNumber)
		case <-ticker.C:
			if outcome, done := checkAndMaybeRebase(); done {
				return mergeResult(outcome, opts.PRNumber)
			}
		}
	}
}

// mergeResult turns a terminal outcome into the (outcome, error) return pair.
func mergeResult(outcome MergeOutcome, prNumber int) (MergeOutcome, error) {
	switch outcome {
	case MergeOutcomeMerged:
		return MergeOutcomeMerged, nil
	case MergeOutcomeClosed:
		return MergeOutcomeClosed, fmt.Errorf("merge-poller: PR #%d was closed without merging", prNumber)
	default:
		return outcome, fmt.Errorf("merge-poller: PR #%d ended in state %s", prNumber, outcome)
	}
}

// ghPRState is the production PRStateFunc: it runs
// `gh pr view <number> --json state,mergedAt` and parses the JSON.
func ghPRState(ctx context.Context, cwd string, prNumber int) (PRState, error) {
	if !tools.GHAvailable() {
		return PRState{}, fmt.Errorf("gh CLI not installed (https://cli.github.com/)")
	}
	out, err := tools.GHOutputCtx(ctx, cwd, "pr", "view", fmt.Sprintf("%d", prNumber),
		"--json", "state,mergedAt")
	if err != nil {
		return PRState{}, fmt.Errorf("gh pr view %d: %w", prNumber, err)
	}
	return parsePRState([]byte(out))
}

// parsePRState unmarshals `gh pr view --json state,mergedAt` output. Split out
// so tests can exercise parsing against fixture JSON without invoking gh.
func parsePRState(raw []byte) (PRState, error) {
	var st PRState
	if err := json.Unmarshal(raw, &st); err != nil {
		return PRState{}, fmt.Errorf("parse PR state: %w — raw: %q", err, truncateForLog(string(raw), 200))
	}
	if strings.TrimSpace(st.State) == "" && strings.TrimSpace(st.MergedAt) == "" {
		return PRState{}, fmt.Errorf("PR state response missing state field: %q", truncateForLog(string(raw), 200))
	}
	return st, nil
}

// rebasePRBranch is the production RebaseFunc: fetch the base branch, rebase the
// current branch onto it, and force-push with lease. Aborts a failed rebase so
// the worktree is left clean for the next poll attempt. An empty base falls back
// to the repo's detected default branch.
func rebasePRBranch(ctx context.Context, cwd, base string) error {
	if strings.TrimSpace(base) == "" {
		base = detectDefaultBranchSafe(cwd)
	}
	if _, stderr, err := tools.GitOutputFullCtx(ctx, cwd, "fetch", "origin", base); err != nil {
		return fmt.Errorf("fetch origin/%s: %s", base, sanitizeStderr(stderr))
	}
	if _, stderr, err := tools.GitOutputFullCtx(ctx, cwd, "rebase", "origin/"+base); err != nil {
		_ = tools.RunGitCmd(cwd, "rebase", "--abort")
		return fmt.Errorf("rebase onto origin/%s: %s", base, sanitizeStderr(stderr))
	}
	if _, stderr, err := tools.GitOutputFullCtx(ctx, cwd, "push", "--force-with-lease", "origin", "HEAD"); err != nil {
		return fmt.Errorf("force-push: %s", sanitizeStderr(stderr))
	}
	return nil
}

// emitMerge calls fn with content if fn is non-nil.
func emitMerge(fn func(string), content string) {
	if fn != nil {
		fn(content)
	}
}
