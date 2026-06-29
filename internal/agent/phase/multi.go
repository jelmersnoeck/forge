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

	"github.com/jelmersnoeck/forge/internal/tools"
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

	// WaitForMerge enables cross-phase merge coordination (#223). When true,
	// after a phase's PR is ensured the coordinator blocks until that PR
	// merges before starting the next phase, then pulls the updated base
	// branch so the next phase branches from a codebase including all prior
	// changes. A PR that is closed-unmerged or times out halts the pipeline.
	// When false the legacy behavior is preserved: phases branch from
	// BaseBranch independently with no merge wait.
	WaitForMerge bool

	// MergePollInterval overrides the merge poll interval (default 30s).
	MergePollInterval time.Duration

	// MergeTimeout overrides the per-PR merge timeout (default 2h).
	MergeTimeout time.Duration

	// WaitForMergeFn polls a PR until it merges. Defaults to WaitForMerge when
	// nil. Injected for tests.
	WaitForMergeFn WaitForMergeFunc

	// PullBase brings RepoRoot's local BaseBranch up to date with origin
	// after a phase merges. Defaults to gitPullBase when nil.
	PullBase PullBaseFunc

	// CloseSubIssue closes a merged phase's sub-issue. Defaults to
	// ghCloseIssue when nil. A close failure is non-fatal.
	CloseSubIssue CloseSubIssueFunc
}

// WaitForMergeFunc blocks until a PR reaches a terminal state, returning the
// outcome and a non-nil error on closed/timeout/cancel.
type WaitForMergeFunc func(ctx context.Context, opts WaitForMergeOpts) (MergeOutcome, error)

// PullBaseFunc updates repoRoot's local baseBranch from origin (fetch + pull).
type PullBaseFunc func(ctx context.Context, repoRoot, baseBranch string) error

// CloseSubIssueFunc closes a GitHub sub-issue by number in repoRoot's repo.
type CloseSubIssueFunc func(ctx context.Context, repoRoot string, number int) error

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
//	  │ remove worktree              │  (best-effort)
//	  └──────────────────────────────┘
//
// A phase failure (worktree creation or sub-agent error) returns immediately;
// remaining phases are not started. Merge coordination between phases is #223.
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
		waitMerge = WaitForMerge
	}
	pullBase := opts.PullBase
	if pullBase == nil {
		pullBase = gitPullBase
	}
	closeIssue := opts.CloseSubIssue
	if closeIssue == nil {
		closeIssue = ghCloseIssue
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

		// Cross-phase merge coordination (#223). Block until this phase's PR
		// merges, then pull the updated base so the next phase branches from a
		// codebase that includes these changes. A closed/timed-out PR halts.
		if opts.WaitForMerge {
			prNumber := extractPRNumberFromURL(pr.URL)
			switch {
			case pr.Error != nil || prNumber == 0:
				// No mergeable PR was created — halt rather than silently
				// running the next phase on a stale base.
				return fmt.Errorf("multi-phase: phase %d (%q): cannot wait for merge, no PR was created", i, ph.Title)
			default:
				outcome, err := waitMerge(ctx, WaitForMergeOpts{
					CWD:          cwd,
					PRNumber:     prNumber,
					PollInterval: opts.MergePollInterval,
					Timeout:      opts.MergeTimeout,
					Emit: func(content string) {
						emit(opts.Emit, types.OutboundEvent{Type: "text", Content: content})
					},
				})
				if err != nil {
					return fmt.Errorf("multi-phase: phase %d (%q): merge wait (%s): %w", i, ph.Title, outcome, err)
				}

				// Close the merged sub-issue (non-fatal).
				if ph.Number > 0 {
					if cerr := closeIssue(ctx, opts.RepoRoot, ph.Number); cerr != nil {
						slog.Warn("multi-phase: close sub-issue failed",
							"phase", i, "issue", ph.Number, "error", cerr)
					}
				}

				// Pull the merged changes into the local base branch so the
				// next phase's worktree includes them (non-fatal — log only).
				if perr := pullBase(ctx, opts.RepoRoot, opts.BaseBranch); perr != nil {
					slog.Warn("multi-phase: pull base branch failed",
						"phase", i, "base", opts.BaseBranch, "error", perr)
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

// prURLNumberRe extracts the numeric PR id from a PR/MR URL path segment such as
// ".../pull/42" or ".../merge_requests/42".
var prURLNumberRe = regexp.MustCompile(`/(?:pull|merge_requests|pull-requests|pullrequest)/(\d+)`)

// extractPRNumberFromURL parses the PR number from a PR URL, returning 0 when no
// number can be found (e.g. an empty URL).
func extractPRNumberFromURL(prURL string) int {
	m := prURLNumberRe.FindStringSubmatch(prURL)
	if len(m) < 2 {
		return 0
	}
	n := 0
	for _, c := range m[1] {
		n = n*10 + int(c-'0')
	}
	return n
}

// gitPullBase brings repoRoot's local baseBranch up to date with origin so the
// next phase worktree branches from a base that includes prior merged phases.
// Runs `git fetch origin <base>` then `git pull origin <base>`.
func gitPullBase(ctx context.Context, repoRoot, baseBranch string) error {
	if !tools.ValidateBranchName(baseBranch) {
		return fmt.Errorf("invalid base branch %q", baseBranch)
	}
	if _, stderr, err := tools.GitOutputFullCtx(ctx, repoRoot, "fetch", "origin", baseBranch); err != nil {
		return fmt.Errorf("fetch origin/%s: %s", baseBranch, sanitizeStderr(stderr))
	}
	if _, stderr, err := tools.GitOutputFullCtx(ctx, repoRoot, "pull", "origin", baseBranch); err != nil {
		return fmt.Errorf("pull origin/%s: %s", baseBranch, sanitizeStderr(stderr))
	}
	return nil
}

// ghCloseIssue closes a GitHub issue/sub-issue by number via `gh issue close`.
func ghCloseIssue(ctx context.Context, repoRoot string, number int) error {
	if !tools.GHAvailable() {
		return fmt.Errorf("gh CLI not installed (https://cli.github.com/)")
	}
	if _, err := tools.GHOutputCtx(ctx, repoRoot, "issue", "close", fmt.Sprintf("%d", number)); err != nil {
		return fmt.Errorf("gh issue close %d: %w", number, err)
	}
	return nil
}
