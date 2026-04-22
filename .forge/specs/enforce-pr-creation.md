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
**every** conversation turn that produced tool use, regardless of execution
path (orchestrator, single-phase, or plain loop). If a PR already exists,
push and update it instead of trying to create a new one.

## Context
- `internal/agent/phase/pr.go` — `EnsurePR`, `existingPRURL`, `ensureExistingPR`,
  `CreatePR` (deprecated), `shouldCreatePR`, `ghCreatePR`,
  `generatePRContent`, `fallbackPRContent`
- `internal/agent/phase/pr_ensure_test.go` — tests for EnsurePR and existingPRURL
- `internal/agent/phase/orchestrator.go` — removed `runFinalize` and its call in
  `runSWEPipeline`
- `internal/agent/worker.go` — `Worker.ensurePR`, `ghAvailable` field,
  `turnToolsUsed` tracking, `done` event interception hook
- `internal/agent/ensure_pr_test.go` — tests for worker-level ensurePR
- `internal/agent/pr_monitor.go` — `getPRInfo`, `PRInfo` struct (unchanged,
  preserved per spec constraint)
- `internal/tools/git.go` — `GHAvailable` helper
- `internal/runtime/loop/loop.go` — `Loop.ToolsUsed()` getter

## Behavior
- After every conversation turn completes (the LLM stops requesting tools
  and the loop emits `done`), the worker runs a deterministic PR
  ensure step — `EnsurePR`.
- `EnsurePR` replaces `CreatePR` as the single entry point and handles both
  creation and update:
  1. Check preconditions: git repo, feature branch (not main/master), has
     changes relative to `origin/<base>`.
  2. Check if a PR already exists for the current branch (`gh pr view`).
  3. If no PR exists: fetch, rebase, push, generate title+body via Haiku,
     `gh pr create --draft` (existing `CreatePR` flow).
  4. If a PR already exists: push with `--force-with-lease`. Optionally
     update the PR title/body if they were auto-generated (not user-edited).
  5. Emit `pr_url` event with the PR URL (new or existing).
- The worker calls `EnsurePR` from a single place, after the emit-done
  logic, covering all execution paths:
  - SWE orchestrator (replaces the current `runFinalize` call in
    `runSWEPipeline`)
  - Single-phase mode (spec, code, review)
  - Plain loop (follow-up messages after orchestrator completes)
- `runFinalize` in the orchestrator is removed. PR creation is no longer a
  phase — it's a worker-level post-condition.
- The worker only calls `EnsurePR` when at least one tool was executed
  during the turn (no-op for pure Q&A or empty turns). The worker tracks
  `turnToolsUsed` by intercepting `tool_use` events in the emit closure.
  `Loop.ToolsUsed()` is also exposed but the worker-level tracking is
  simpler since the worker already intercepts all events.
- Failures in `EnsurePR` are non-fatal: logged, `pr_url` not emitted, user
  sees no error. The session continues normally.
- The `pr_url` event is idempotent — the CLI already handles receiving it
  multiple times (sets `m.prURL`).

## Constraints
- Do not fail the session or emit an error event if PR creation/update fails.
- Do not create a PR if there are no changes relative to the base branch.
- Do not create a PR when on main/master.
- Do not attempt PR operations if `gh` is not installed (check once, cache).
- Do not remove the existing `pr_monitor` — it serves a different purpose
  (health monitoring of an existing PR over time).
- The `EnsurePR` call must not block the `done` event from reaching the CLI.
  Run it synchronously before `done`, but with a reasonable timeout (30s).
- Use the existing `classificationModels` (Haiku) for title/body generation,
  not the main conversation model.
- Do not update PR title/body if the user has manually edited them on GitHub
  (check via `gh pr view` — compare title with what we'd generate).

## Interfaces
```go
// internal/agent/phase/pr.go

// EnsurePR creates a new PR or updates an existing one.
// Returns the PR URL on success. Non-fatal: errors are logged, not propagated.
func EnsurePR(ctx context.Context, prov types.LLMProvider, cwd, specPath string) PRResult

// existingPRURL checks if a PR already exists for the current branch.
// Returns the PR URL or "" if none exists.
func existingPRURL(cwd string) string

// ensureExistingPR pushes new commits to an existing PR.
// Skips push if there's nothing new to push.
func ensureExistingPR(ctx context.Context, cwd, prURL string) PRResult
```

```go
// internal/agent/worker.go

// ensurePR runs the deterministic PR creation/update step.
// Called after every turn that executed tools.
func (w *Worker) ensurePR(ctx context.Context, prov types.LLMProvider, specPath string, emit func(types.OutboundEvent))
```

```go
// internal/runtime/loop/loop.go

// ToolsUsed returns whether any tool was executed during this loop's lifetime.
func (l *Loop) ToolsUsed() bool
```

```go
// internal/tools/git.go

// GHAvailable reports whether the gh CLI is on PATH.
func GHAvailable() bool
```

## Edge Cases
- PR already exists, no new commits since last push: `EnsurePR` detects
  existing PR, skips push (nothing to push), emits `pr_url` with existing
  URL. No error.
- PR already exists, new commits: pushes with `--force-with-lease`, emits
  `pr_url`. Title/body left alone (user may have edited).
- Rebase conflict during push prep: rebase aborted, push skipped, existing
  PR URL still emitted if PR exists. Logged as warning.
- `gh` not installed: detected once at worker startup, `EnsurePR` skipped
  for all turns. No repeated exec.LookPath calls.
- Context cancelled mid-`EnsurePR`: standard cancellation, `done` event
  already emitted or about to be.
- Q&A-only session (no tools used): `EnsurePR` not called. No PR attempted.
- Agent interrupted (Ctrl+C): the interrupt cancels the turn context before
  `EnsurePR` would run. No PR attempted on interrupted turns.
- Not a git repo: `shouldCreatePR` returns false immediately. No error.
- Multiple rapid turns: each turn's `EnsurePR` is idempotent. Creating a PR
  that already exists just returns the existing URL.
- `runSWEPipeline` errors mid-pipeline (e.g., coder phase fails): the worker
  still runs `EnsurePR` after the error because it's a worker-level
  post-condition, not a pipeline step. If there are partial changes committed,
  a draft PR captures them.
