---
id: auto-pr-creation
status: implemented
---
# Automatically create PR at end of SWE pipeline

## Description
The SWE orchestrator completes spec → code → review cycles but never ensures a
PR is created. The agent has the PRCreate tool available but no instruction or
mechanism to guarantee it runs. This adds an explicit "finalize" step that runs
after the review cycle to create a draft PR.

## Context
- `internal/agent/phase/orchestrator.go` — `runSWEPipeline` needs a finalize step
- `internal/agent/phase/prompts.go` — coder prompt should mention PR creation
- `internal/agent/phase/orchestrator_test.go` — test the finalize step

## Behavior
- After the review cycle completes in `runSWEPipeline`, run a finalize step.
- The finalize step uses the coder phase (all tools available) with a prompt
  instructing it to: review the full diff, commit any uncommitted changes,
  create a draft PR using PRCreate, and reconcile the spec.
- The finalize step only runs when:
  - The current branch is not `main` or `master`
  - There are changes (committed or uncommitted) relative to `origin/<base>`
- If the finalize step fails (e.g., `gh` not installed, not a git repo), log
  the error and continue — don't fail the whole pipeline.
- The coder phase prompt is updated to list PR creation as step 7 in the
  workflow.

## Constraints
- Do not call PRCreate programmatically — it must go through the LLM so it
  reads the diff and writes a proper title/description.
- Do not fail the pipeline if PR creation fails.
- Do not create a PR if there are no changes.
- Do not create a PR if we're on main/master.

## Interfaces
```go
// Added to orchestrator.go:
func (o *Orchestrator) runFinalize(ctx context.Context, opts OrchestratorOpts, specPath string) error
```

## Edge Cases
- No changes after code+review: finalize step skipped, no PR created.
- On main branch (e.g., `--skip-worktree`): finalize step skipped.
- `gh` not installed: PRCreate tool returns error, agent sees it, pipeline
  continues.
- PR already exists for the branch: `gh pr create` fails, agent sees it,
  pipeline continues.
- Context cancelled mid-finalize: standard context cancellation handling.
