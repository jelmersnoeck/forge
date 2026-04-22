---
id: finalize-error-recovery
status: implemented
---
# Recover from finalize errors via LLM

## Description

The orchestrator's finalize phase (PR creation) runs deterministically outside the
conversation loop. When it fails — e.g., rebase onto origin/main fails due to
unstaged changes — the error is logged and swallowed. The LLM never sees the error
and cannot take corrective action (committing, stashing, resolving conflicts). This
spec adds error recovery: on finalize failure, the error is sent back to a coder
loop so the LLM can fix the issue, then finalize is retried.

## Context

- `internal/agent/phase/orchestrator.go` — `runSWEPipeline()`, `runFinalize()`, `recoverFinalize()`, `buildFinalizeRecoveryPrompt()`, `emitPhaseError()`
- `internal/agent/phase/pr.go` — `CreatePR()` deterministic workflow
- `internal/agent/phase/orchestrator.go` — `runCoderWithMessage()` (existing pattern for feeding errors to coder)
- `internal/agent/phase/staleness.go` — `CheckStaleness()`, `hasUncommittedChanges()`
- `internal/agent/phase/finalize_recovery_test.go` — tests for recovery prompt, constant, and emitPhaseError
- `internal/types/types.go` — `OutboundEvent.Type` doc updated to include `phase_error`

## Behavior

1. When `runFinalize` returns an error, the orchestrator sends the error message
   to a coder loop via `runCoderWithMessage` with a prompt that includes:
   - The exact error message
   - Instruction to fix the issue (e.g., "commit your changes", "resolve conflicts")
   - Instruction NOT to make functional code changes — only fix the git state
2. After the coder loop completes, `runFinalize` is retried exactly once.
3. If the retry also fails, the error is emitted as a `phase_error` event and
   the pipeline returns successfully (finalize is still non-fatal to the overall
   pipeline — the code is written, reviewed, and on disk).
4. The `phase_error` event contains the final error message so the CLI can
   display it to the user.
5. Maximum 1 recovery attempt per finalize failure (no infinite loops).

## Constraints

- Must not change the tool result / conversation loop error handling (that already
  works correctly — tool errors flow back to the LLM via `tool_result`).
- Must not make finalize failure fatal to the pipeline. If recovery also fails,
  the pipeline still completes — the user has their code and can finalize manually.
- The recovery coder loop must use a focused prompt that prevents scope creep —
  it should only fix git state, not refactor code.
- Must not add new dependencies.

## Interfaces

```go
// maxFinalizeRecoveries limits retry attempts for finalize.
const maxFinalizeRecoveries = 1

// recoverFinalize attempts to fix git state via a coder loop and retry finalize.
// If recovery or retry fails, a phase_error event is emitted and the pipeline continues.
func (o *Orchestrator) recoverFinalize(ctx context.Context, opts OrchestratorOpts, specPath string, finalizeErr error)

// buildFinalizeRecoveryPrompt creates a focused prompt for the coder to fix git state.
func buildFinalizeRecoveryPrompt(err error) string

// emitPhaseError emits a phase_error event for unrecoverable finalize errors.
func (o *Orchestrator) emitPhaseError(opts OrchestratorOpts, err error)

// New event type emitted on unrecoverable finalize error:
types.OutboundEvent{
    Type:    "phase_error",
    Content: "finalize: <error details>",
}
```

## Edge Cases

1. **Coder recovery loop itself fails** (e.g., context cancelled, API error) —
   emit `phase_error` with the original finalize error, do not propagate the
   coder loop error. Pipeline completes.

2. **Finalize fails for non-git reasons** (e.g., `gh` CLI not installed, no
   network) — still attempts recovery via coder loop. The LLM will likely fail to
   fix it, retry will fail, `phase_error` emitted. Acceptable: the coder might
   at least inform via its output what's wrong.

3. **Coder makes functional code changes during recovery** — mitigated by prompt
   design ("fix git state only, do not modify code"). The coder phase has all
   tools available, so this is a soft constraint. Acceptable risk: review already
   passed.

4. **Unstaged changes from a tool that crashed mid-write** — the coder loop can
   inspect `git status`, `git diff`, and decide whether to commit or revert the
   partial changes. This is the primary motivating use case.

5. **Context already cancelled when finalize fails** — skip recovery entirely
   (check `ctx.Err()` before spawning coder loop). Emit `phase_error` and return.
