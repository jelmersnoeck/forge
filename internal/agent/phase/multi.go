package phase

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

// MultiPhaseOpts configures a sequential multi-phase run. One sub-issue is run
// per phase, in slice order; each phase gets its own worktree + branch, runs a
// full-access sub-agent, then has a PR ensured for it. A failed phase halts the
// pipeline (later phases are not started).
//
// Worktree creation, agent spawning, and PR ensuring are injected as function
// fields so the coordinator is testable without git, a live LLM, or `gh`. The
// caller (the agent worker) wires in the real implementations; tests substitute
// fakes. When a field is nil a sensible default is used (real git for worktrees,
// EnsurePR for PRs); SpawnAgent has no default and MUST be supplied.
type MultiPhaseOpts struct {
	// Provider is passed to EnsurePR for PR title/body generation.
	Provider types.LLMProvider

	// RepoRoot is the git repository the phase worktrees branch from.
	RepoRoot string

	// BaseBranch is the branch each phase worktree is created from
	// (typically the default branch / main).
	BaseBranch string

	// ParentSlug is the slugified parent-issue title used in branch names:
	//   jelmer/<ParentSlug>-<sub-issue-number>
	ParentSlug string

	// WorktreeBase is the directory under which per-phase worktrees are
	// created (typically /tmp/forge/worktrees).
	WorktreeBase string

	// Phases are the ordered sub-issues to run, one per phase.
	Phases []SubIssue

	// PRAttr is forwarded to EnsurePR for each phase's PR.
	PRAttr PRAttributionOpts

	// Emit receives progress events (best-effort; may be nil).
	Emit func(types.OutboundEvent)

	// SpawnAgent runs a sub-agent to completion in cwd with the given prompt.
	// REQUIRED. It must block until the agent finishes and return any error.
	SpawnAgent SpawnPhaseAgent

	// CreateWorktree creates a worktree + branch and returns the worktree path.
	// Defaults to gitCreateWorktree when nil.
	CreateWorktree CreateWorktreeFunc

	// RemoveWorktree tears down a completed phase's worktree.
	// Defaults to gitRemoveWorktree when nil.
	RemoveWorktree RemoveWorktreeFunc

	// EnsurePRFn ensures a PR for a phase worktree.
	// Defaults to a thin wrapper around EnsurePR when nil.
	EnsurePRFn EnsurePRFunc

	// WaitForMergeEnabled turns on the merge-wait step between phases. When
	// false (the default) RunMultiPhase behaves as before: each phase branches
	// independently from BaseBranch with no cross-phase merge coordination.
	WaitForMergeEnabled bool

	// MergePollInterval / MergeTimeout configure WaitForMerge. Zero values fall
	// back to the package defaults (30s / 2h).
	MergePollInterval time.Duration
	MergeTimeout      time.Duration

	// WaitForMergeFn waits for a phase's PR to merge. Defaults to a wrapper
	// around WaitForMerge when nil (only used when WaitForMergeEnabled is true).
	WaitForMergeFn WaitForMergeFunc

	// PullMainFn fast-forwards the local base branch after a phase merges, so
	// the next phase branches off the updated base. Defaults to pullMain.
	PullMainFn PullMainFunc
}

// SubIssue is one sub-issue (decomposed phase) to run. Number is the GitHub
// issue number used in the branch name; Title/Body are formatted into the
// sub-agent prompt like an --issue session.
type SubIssue struct {
	Number int
	Title  string
	Body   string
}

// SpawnPhaseAgent runs a full-access sub-agent in cwd with prompt, blocking
// until it completes. The branch is the worktree's branch (for logging/PR).
type SpawnPhaseAgent func(ctx context.Context, cwd, branch, prompt string) error

// CreateWorktreeFunc creates a worktree at a path branched from baseBranch as
// branch, returning the worktree path it created.
type CreateWorktreeFunc func(ctx context.Context, repoRoot, baseBranch, branch, worktreePath string) (string, error)

// RemoveWorktreeFunc removes a worktree by path.
type RemoveWorktreeFunc func(ctx context.Context, repoRoot, worktreePath string) error

// EnsurePRFunc ensures a PR for a phase worktree.
type EnsurePRFunc func(ctx context.Context, cwd string) PRResult

// WaitForMergeFunc blocks until a phase's PR (by number) merges, is closed, or
// times out. Returns nil on merge, an error on close/timeout/cancel.
type WaitForMergeFunc func(ctx context.Context, prNumber int, cwd, base string) error

// PullMainFunc fast-forwards the local base branch in repoRoot after a merge.
type PullMainFunc func(ctx context.Context, repoRoot, base string) error

// RunMultiPhase runs each sub-issue sequentially: create worktree, spawn a
// full-access sub-agent, ensure a PR, clean up, then move to the next.
//
//	for each phase i:
//	  ┌──────────────────────────────┐
//	  │ create worktree + branch     │  jelmer/<parent>-<num>
//	  └──────────────┬───────────────┘
//	                 ▼
//	  ┌──────────────────────────────┐
//	  │ spawn sub-agent (CWD=tree)   │  prompt = formatted sub-issue body
//	  └──────────────┬───────────────┘
//	                 ▼
//	  ┌──────────────────────────────┐
//	  │ ensure PR in worktree        │
//	  └──────────────┬───────────────┘
//	                 ▼
//	  ┌──────────────────────────────┐
//	  │ wait for PR merge (optional) │  WaitForMergeEnabled
//	  └──────────────┬───────────────┘  → pull base after merge
//	                 ▼
//	  ┌──────────────────────────────┐
//	  │ remove worktree              │  (best-effort)
//	  └──────────────────────────────┘
//
// A phase failure (worktree creation, sub-agent error, or — when merge waiting
// is enabled — a closed PR / merge timeout) returns immediately; remaining
// phases are not started.
func RunMultiPhase(ctx context.Context, opts MultiPhaseOpts) error {
	if opts.SpawnAgent == nil {
		return fmt.Errorf("multi-phase: SpawnAgent is required")
	}
	if len(opts.Phases) == 0 {
		return nil
	}

	createWT := opts.CreateWorktree
	if createWT == nil {
		createWT = gitCreateWorktree
	}
	removeWT := opts.RemoveWorktree
	if removeWT == nil {
		removeWT = gitRemoveWorktree
	}
	ensurePR := opts.EnsurePRFn
	if ensurePR == nil {
		ensurePR = func(ctx context.Context, cwd string) PRResult {
			return EnsurePR(ctx, opts.Provider, cwd, "", opts.PRAttr)
		}
	}
	waitMerge := opts.WaitForMergeFn
	if waitMerge == nil {
		waitMerge = func(ctx context.Context, prNumber int, cwd, base string) error {
			return WaitForMerge(ctx, MergeWaitOpts{
				PRNumber:     prNumber,
				Cwd:          cwd,
				BaseBranch:   base,
				PollInterval: opts.MergePollInterval,
				Timeout:      opts.MergeTimeout,
			})
		}
	}
	pullMainFn := opts.PullMainFn
	if pullMainFn == nil {
		pullMainFn = pullMain
	}

	for i, ph := range opts.Phases {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("multi-phase: cancelled before phase %d: %w", i, err)
		}

		branch := phaseBranchName(opts.ParentSlug, ph.Number)
		worktreePath := filepath.Join(opts.WorktreeBase, branchToDir(branch))

		emit(opts.Emit, types.OutboundEvent{
			Type:    "text",
			Content: fmt.Sprintf("Phase %d/%d: %s (branch %s)\n", i+1, len(opts.Phases), ph.Title, branch),
		})

		cwd, err := createWT(ctx, opts.RepoRoot, opts.BaseBranch, branch, worktreePath)
		if err != nil {
			return fmt.Errorf("multi-phase: phase %d (%q): create worktree: %w", i, ph.Title, err)
		}

		prompt := formatSubIssuePrompt(ph)
		if err := opts.SpawnAgent(ctx, cwd, branch, prompt); err != nil {
			// Leave the worktree in place for inspection on failure.
			return fmt.Errorf("multi-phase: phase %d (%q): sub-agent failed: %w", i, ph.Title, err)
		}

		pr := ensurePR(ctx, cwd)
		switch {
		case pr.Error != nil:
			// PR failure is non-fatal — surface it but keep the pipeline going.
			slog.Warn("multi-phase: ensure PR failed",
				"phase", i, "title", ph.Title, "error", pr.Error)
			emit(opts.Emit, types.OutboundEvent{
				Type:    "text",
				Content: fmt.Sprintf("Phase %d: PR skipped: %v\n", i+1, pr.Error),
			})
		case pr.URL != "":
			emit(opts.Emit, types.OutboundEvent{
				Type:    "text",
				Content: fmt.Sprintf("Phase %d: PR %s\n", i+1, pr.URL),
			})
		}

		// Wait for the PR to merge before the next phase, so later phases build
		// on a base that includes this phase's changes. A closed (un-merged) PR
		// or timeout halts the pipeline; a successful merge then fast-forwards
		// the local base ref.
		if opts.WaitForMergeEnabled {
			prNum := extractPRNumberFromURL(pr.URL)
			switch {
			case prNum <= 0:
				// No PR to wait on (creation skipped/failed). Don't halt — the
				// PR failure was already surfaced above as non-fatal.
				slog.Warn("multi-phase: no PR number to wait on; skipping merge wait",
					"phase", i, "title", ph.Title, "url", pr.URL)
			default:
				emit(opts.Emit, types.OutboundEvent{
					Type:    "text",
					Content: fmt.Sprintf("Phase %d: waiting for PR #%d to merge\n", i+1, prNum),
				})
				if err := waitMerge(ctx, prNum, cwd, opts.BaseBranch); err != nil {
					// Leave the worktree in place for inspection on halt.
					return fmt.Errorf("multi-phase: phase %d (%q): merge wait: %w", i, ph.Title, err)
				}
				emit(opts.Emit, types.OutboundEvent{
					Type:    "text",
					Content: fmt.Sprintf("Phase %d: PR #%d merged\n", i+1, prNum),
				})
				if err := pullMainFn(ctx, opts.RepoRoot, opts.BaseBranch); err != nil {
					// Non-fatal: CreateWorktree still branches off origin/<base>.
					slog.Warn("multi-phase: pull base after merge failed",
						"phase", i, "error", err)
				}
			}
		}

		if err := removeWT(ctx, opts.RepoRoot, cwd); err != nil {
			// Cleanup failure is non-fatal — log and continue.
			slog.Warn("multi-phase: worktree cleanup failed",
				"phase", i, "path", cwd, "error", err)
		}
	}

	return nil
}

// emit calls fn if non-nil.
func emit(fn func(types.OutboundEvent), ev types.OutboundEvent) {
	if fn != nil {
		fn(ev)
	}
}

// phaseBranchName builds the per-phase branch name. A blank parent slug
// degrades to jelmer/phase-<num> so the name is always valid.
func phaseBranchName(parentSlug string, number int) string {
	if parentSlug == "" {
		parentSlug = "phase"
	}
	return fmt.Sprintf("jelmer/%s-%d", parentSlug, number)
}

// branchToDir turns a branch name into a filesystem-safe directory leaf by
// replacing slashes with hyphens (jelmer/foo-3 → jelmer-foo-3).
func branchToDir(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}

// formatSubIssuePrompt renders a sub-issue into a prompt, mirroring the
// formatting an --issue session uses (title header + body).
func formatSubIssuePrompt(ph SubIssue) string {
	var b strings.Builder
	b.WriteString("Implement the following GitHub issue.\n\n")
	if ph.Number > 0 {
		fmt.Fprintf(&b, "Issue #%d: %s\n\n", ph.Number, ph.Title)
	} else {
		fmt.Fprintf(&b, "Issue: %s\n\n", ph.Title)
	}
	b.WriteString(strings.TrimSpace(ph.Body))
	b.WriteString("\n")
	return b.String()
}

// slugTitleRe matches runs of non-alphanumeric characters for slugifying titles.
var slugTitleRe = regexp.MustCompile(`[^a-z0-9]+`)

// SlugifyTitle lowercases a title, collapses non-alphanumeric runs to single
// hyphens, trims hyphens, and truncates to maxLen. Used to build the parent
// slug component of phase branch names.
func SlugifyTitle(title string, maxLen int) string {
	s := strings.ToLower(title)
	s = slugTitleRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if maxLen > 0 && len(s) > maxLen {
		s = strings.TrimRight(s[:maxLen], "-")
	}
	return s
}

// gitCreateWorktree creates a worktree + new branch from baseBranch. If the
// branch already exists it is checked out into the worktree instead.
func gitCreateWorktree(ctx context.Context, repoRoot, baseBranch, branch, worktreePath string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", branch, worktreePath, baseBranch)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		// Branch may already exist — retry checking it out.
		retry := exec.CommandContext(ctx, "git", "worktree", "add", worktreePath, branch)
		retry.Dir = repoRoot
		if out2, err2 := retry.CombinedOutput(); err2 != nil {
			return "", fmt.Errorf("git worktree add: %s\n%s", err, string(append(out, out2...)))
		}
	}
	return worktreePath, nil
}

// gitRemoveWorktree removes a worktree by path (force, to drop any uncommitted
// changes left over after the PR is pushed).
func gitRemoveWorktree(ctx context.Context, repoRoot, worktreePath string) error {
	cmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", worktreePath)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %s\n%s", err, string(out))
	}
	return nil
}
