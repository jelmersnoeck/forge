---
id: enforce-pr-creation
status: implemented
---
# Enforce PR creation as a deterministic loop post-condition

## Description
PR creation at the end of a coding session is unreliable. The current
`runFinalize` only runs at the happy-path end of `runSWEPipeline`, and it
silently fails if a PR already exists for the branch. Additionally, plain-loop
sessions (post-orchestrator follow-ups, non-SWE mode) never attempt PR
creation at all. This makes PR creation a best-effort afterthought rather than
a guaranteed post-condition of any session that produces git changes.

Move PR creation/update to a deterministic post-condition that runs after
**every** conversation turn, gated on **git ground truth** (dirty working tree
or branch ahead of base) — NOT on per-turn `tool_use` event flags. The original
implementation gated on `turnToolsUsed`, which silently skipped PR creation when
the mutating work happened in an earlier turn or in a phase the worker loop
never observed a `tool_use` event for (issue #234). If a PR already exists, push
and update it instead of trying to create a new one.

## Context
- `internal/agent/phase/pr.go` — `EnsurePR`, `existingPRURL`, `ensureExistingPR`,
  `CreatePR` (deprecated), `shouldCreatePR`, `ghCreatePR`,
  `generatePRContent`, `fallbackPRContent`
- `internal/agent/phase/pr_ensure_test.go` — tests for EnsurePR and existingPRURL
- `internal/agent/phase/orchestrator.go` — removed `runFinalize` and its call in
  `runSWEPipeline`; `detectDefaultBranchSafe` + exported `DetectDefaultBranchSafe`
- `internal/agent/worker.go` — `Worker.reconcilePR` (post-condition entry point),
  `Worker.repoHasPendingWork`, `Worker.workingTreeDirty`,
  `Worker.commitPendingChanges`, `Worker.ensurePR`, `ghAvailable` field,
  `done` event interception hook. `turnToolsUsed` tracking REMOVED (issue #234).
- `internal/agent/ensure_pr_test.go` — tests for worker-level ensurePR
- `internal/agent/reconcile_pr_test.go` — tests for git-ground-truth enforcement
- `internal/agent/pr_monitor.go` — `getPRInfo`, `PRInfo` struct (unchanged,
  preserved per spec constraint)
- `internal/tools/git.go` — `GHAvailable` helper
- `cmd/forge/cli.go` — `warning` event case renders the no-PR / commit-failure
  notices visibly

## Behavior
- After every conversation turn completes (the loop emits `done`), the worker
  runs `reconcilePR` — a deterministic PR post-condition.
- `reconcilePR` keys enforcement off **git ground truth** via
  `repoHasPendingWork`: the working tree is dirty (`git status --porcelain`
  non-empty, including untracked files) OR the current branch has commits ahead
  of `origin/<base>`. This is independent of `turnToolsUsed` and of which phase
  produced the changes.
- `repoHasPendingWork` returns `(false, "")` when: not a git repo, on
  `main`/`master`, or the tree is clean and the branch is not ahead of base.
- On a normal (non-interrupted) `done` with pending work:
  1. If the working tree is dirty, `commitPendingChanges` stages everything
     (`git add -A`) and commits with `forge: commit outstanding session
     changes`. The prepare-commit-msg hook adds attribution trailers.
  2. `ensurePR` runs (creates a new draft PR or pushes/updates an existing one).
- On an **interrupted** `done` with pending work: no PR is created (Ctrl+C means
  stop) and no auto-commit happens, but a `warning` event is emitted
  ("interrupted with uncommitted/unpushed changes ...; no PR created") so the
  user is never left silently without a PR.
- If `commitPendingChanges` fails, a `warning` event is emitted and `ensurePR`
  still runs against whatever is committed — never a silent drop.
- `EnsurePR` is the single entry point for creation+update:
  1. Check preconditions: git repo, feature branch (not main/master), has
     changes relative to `origin/<base>`.
  2. Check if a PR already exists for the current branch (`gh pr view`).
  3. If no PR exists: fetch, rebase, push, generate title+body via Haiku,
     `gh pr create --draft`.
  4. If a PR already exists: push with `--force-with-lease`.
  5. Emit `pr_url` event with the PR URL (new or existing).
- `reconcilePR` covers all execution paths (SWE orchestrator, single-phase,
  plain loop) because it runs from the single `done` interception hook.
- `runFinalize` in the orchestrator is removed. PR creation is a worker-level
  post-condition, not a phase.
- Failures in `EnsurePR` are non-fatal: logged, `pr_url` not emitted, user sees
  no error. The session continues normally.
- The `pr_url` event is idempotent — the CLI already handles receiving it
  multiple times (sets `m.prURL`).
- The CLI renders `warning` events visibly (prefixed `warning:`).

## Constraints
- Do not fail the session or emit an error event if PR creation/update fails.
- Do not create a PR if there are no changes relative to the base branch.
- Do not create a PR when on main/master.
- Do not gate PR enforcement on `turnToolsUsed` or any other conversation-event
  flag — gate only on git ground truth (dirty tree / branch ahead of base).
- Do not auto-commit or create a PR on an interrupted turn; warn instead.
- Do not let a dirty working tree exit silently — commit it or emit a `warning`.
- Do not attempt PR operations if `gh` is not installed (check once, cache).
- Do not remove the existing `pr_monitor` — it serves a different purpose
  (health monitoring of an existing PR over time).
- The `reconcilePR` call must not block the `done` event indefinitely. `EnsurePR`
  runs synchronously with a 30s timeout.
- Use the existing `LightweightModels` (Haiku) for title/body generation,
  not the main conversation model.

## Interfaces
```go
// internal/agent/worker.go

// reconcilePR enforces PR creation as a loop post-condition based on git
// ground truth. Runs after every done event, independent of tool-use flags
// or which phase produced changes.
func (w *Worker) reconcilePR(ctx context.Context, prov types.LLMProvider, interrupted bool, emit func(types.OutboundEvent))

// repoHasPendingWork reports whether the working tree is dirty OR the branch
// is ahead of origin/<base>. Returns (false, "") for non-git, main/master,
// or clean+not-ahead. Returns (true, reason) otherwise.
func (w *Worker) repoHasPendingWork(ctx context.Context) (bool, string)

// workingTreeDirty reports staged/unstaged/untracked changes via --porcelain.
func (w *Worker) workingTreeDirty(ctx context.Context) bool

// commitPendingChanges stages all and commits with a deterministic message.
func (w *Worker) commitPendingChanges(ctx context.Context) error

// ensurePR runs the deterministic PR creation/update step. Non-fatal.
func (w *Worker) ensurePR(ctx context.Context, prov types.LLMProvider, specPath string, emit func(types.OutboundEvent))
```

```go
// internal/agent/phase/pr.go

// EnsurePR creates a new PR or updates an existing one.
// Returns the PR URL on success. Non-fatal: errors are logged, not propagated.
func EnsurePR(ctx context.Context, prov types.LLMProvider, cwd, specPath string, attr PRAttributionOpts) PRResult

// existingPRURL checks if a PR already exists for the current branch.
func existingPRURL(ctx context.Context, cwd string) string

// ensureExistingPR pushes new commits to an existing PR.
func ensureExistingPR(ctx context.Context, cwd, prURL string) PRResult
```

```go
// internal/agent/phase/orchestrator.go

// DetectDefaultBranchSafe returns the base branch ("main"/"master") by checking
// which origin ref exists. Matches the base used by EnsurePR's preconditions.
func DetectDefaultBranchSafe(cwd string) string
```

```go
// internal/tools/git.go

// GHAvailable reports whether the gh CLI is on PATH.
func GHAvailable() bool
```

## Edge Cases
- Changes made in an earlier turn, final `done` turn has no tool_use:
  `repoHasPendingWork` still detects the dirty tree / ahead branch and
  reconcilePR runs. (Root cause of issue #234 — was previously skipped.)
- Dirty working tree at `done`: auto-committed via `commitPendingChanges`
  before `EnsurePR`, so uncommitted changes are never silently dropped.
- Interrupted turn with pending changes: no commit, no PR; a `warning` event
  is emitted so the user knows changes were left unshipped.
- `commitPendingChanges` fails (e.g., nothing staged, hook error): `warning`
  emitted, `EnsurePR` still runs against committed history.
- On main/master with a dirty tree: `repoHasPendingWork` returns false — never
  enforce PR on the base branch.
- PR already exists, no new commits: `EnsurePR` detects existing PR, skips push,
  emits `pr_url` with existing URL.
- PR already exists, new commits: pushes with `--force-with-lease`, emits
  `pr_url`.
- Rebase conflict during push prep: rebase aborted, push skipped, existing PR
  URL still emitted if PR exists. Logged as warning.
- `gh` not installed: `ensurePR` bails (cached `ghAvailable`), but the dirty
  tree is still committed by `reconcilePR` so work isn't lost.
- Context cancelled mid-`EnsurePR`: standard cancellation; `reconcilePR` bails
  early via the parent-context check in `ensurePR`.
- Q&A-only / clean repo: `repoHasPendingWork` returns false, `reconcilePR` is a
  no-op. No events.
- Not a git repo: `repoHasPendingWork` returns false immediately. No error.
- `runSWEPipeline` errors mid-pipeline with partial committed changes:
  `reconcilePR` still runs (worker-level post-condition) and a draft PR captures
  the partial work.
