---
id: pipeline-session-reuse
status: implemented
---
# Preserve phase session context across SWE pipeline restarts

## Description
When the SWE orchestrator completes and the user sends a follow-up message, the
worker drops into a plain `loop.Send` / `loop.Resume` path that has no
relationship to the orchestrator's internal phase history IDs. This means all
context from the spec-creator, planner, and coder phases is lost — the follow-up
starts a fresh conversation. This spec threads the coder's `historyID` back to
the worker so that post-pipeline messages resume the coder conversation,
preserving full implementation context. It also ensures any re-invocation of the
SWE pipeline (e.g., a second task in the same session) carries forward the
relevant phase history IDs for the spec and coder agents. Reviewers remain
stateless — they always get a fresh context with just the diff and spec.

## Context
Files affected:

- `internal/agent/phase/orchestrator.go` — `OrchestratorResult` gains
  `CoderHistoryID` and `SpecHistoryID` fields; `Run()` and `runSWEPipeline()`
  propagate them; `runSWEPipeline` signature changes to return
  `(OrchestratorResult, error)` instead of `error`.
- `internal/agent/worker.go` — `Run()` message loop stores
  `result.CoderHistoryID` into the outer `historyID` variable so the
  `loop.Resume` path fires for follow-up messages. The worker also stores
  `result.SpecHistoryID` for potential re-use in future orchestrator runs.
- `internal/agent/phase/phase.go` — no changes needed; `Result.HistoryID`
  already exists.
- `internal/agent/phase/debate.go` — `runDebate` return type updated from
  `DebateResult` to `*DebateResult` for nil-safety.
- `internal/agent/phase/phase_test.go` — updated `runDebate` mock return type
  to `*DebateResult`.
- `internal/agent/phase/session_continuity_test.go` — add test verifying
  `OrchestratorResult.CoderHistoryID` is non-empty after a pipeline run, and
  that using it with `loop.Resume` loads the coder's conversation history.

## Behavior
- After the SWE pipeline finishes (spec → code → review), the
  `OrchestratorResult` returned to the worker includes a non-empty
  `CoderHistoryID`.
- The worker stores `result.CoderHistoryID` as its `historyID` for the session
  message loop, so subsequent user messages resume the coder's conversation via
  `loop.Resume(ctx, historyID, msg.Text, emit)`.
- The resumed conversation carries the full coder phase history: the original
  spec prompt, all tool calls, all assistant responses, and all review-fix
  cycles. The user's follow-up message appears naturally as the next turn.
- If the pipeline used the ideation path (debate → planner → coder), the
  planner's history ID is also stored in `OrchestratorResult.SpecHistoryID` for
  potential future use (e.g., re-running the spec phase). Currently unused by
  the worker but available for extension.
- If the pipeline skipped spec creation (`--spec` provided), `SpecHistoryID` is
  empty.
- The Q&A → task transition path is unchanged: `qaHistoryID` is used for Q&A
  turns, and once the orchestrator completes the task pipeline, `historyID`
  switches to the coder's history.
- Reviewers continue to start fresh every cycle — they receive only the diff
  and spec, never conversation history from previous reviews or phases.
- The `--mode spec`, `--mode code`, and `--mode review` standalone paths are
  unchanged.

## Constraints
- Must not change `loop.Loop` API — the existing `Send`/`Resume`/`HistoryID`
  contract is sufficient.
- Must not pass phase history IDs to reviewers — they must remain stateless.
- Must not break the existing Q&A history resumption (`qaHistoryID` flow).
- Must not add persistence for history IDs beyond process lifetime — a worker
  restart means a new session, these are in-memory only.
- `OrchestratorResult` is the only conduit for passing history IDs out of the
  orchestrator to the worker — no globals, no side channels.
- The plain loop fallback (when `orchestratorDone` is true and `historyID` is
  set) uses the worker's default `loop.New(opts)` configuration. The coder
  phase's history is preserved via `loop.Resume`, so the model sees the full
  prior conversation. The worker's loop already has the same tools and model
  as the coder phase. Phase-specific system prompt injection is not added —
  the resumed history carries sufficient context.

## Interfaces

```go
// internal/agent/phase/orchestrator.go

type OrchestratorResult struct {
    Intent         Intent
    QAHistoryID    string // set when Intent == IntentQuestion
    CoderHistoryID string // set after SWE pipeline completes
    SpecHistoryID  string // set after spec creation (planner or spec-creator)
}
```

```go
// internal/agent/worker.go — Run() message loop (pseudo-code for key change)

// After orchestrator returns:
result, err := w.runOrchestrator(...)
if result.CoderHistoryID != "" {
    historyID = result.CoderHistoryID
}
// Now the existing `case historyID != "":` branch
// picks up and uses loop.Resume for follow-ups.
```

## Edge Cases
- **Pipeline completes with no review findings** — coder historyID is still set
  and returned. Follow-up messages resume the coder conversation.
- **Pipeline completes after multiple review→fix cycles** — coder historyID
  evolves through each `runCoderResume` call; the final historyID is returned.
  Follow-up resumes from the last fix cycle's conversation state.
- **User sends follow-up that's a question, not a task** — the resumed coder
  loop handles it conversationally (same model, same tools). No re-classification
  occurs since `orchestratorDone` is true. This is acceptable — the coder agent
  can answer questions about what it built.
- **Coder phase errors out mid-pipeline** — `runSWEPipeline` returns an error
  before setting `CoderHistoryID`. The worker falls through to the error
  handling path; `historyID` stays empty. Next message starts fresh.
- **Coder loop hits token budget on resume** — compaction kicks in (existing
  behavior). Oldest turns are dropped; the most recent context and the user's
  follow-up are preserved.
- **Q&A → task transition → follow-up** — first messages use `qaHistoryID`,
  then the orchestrator runs the SWE pipeline, then `historyID` is set to the
  coder's history. The Q&A context is not in the coder's history (different
  conversation), but the augmented prompt carries the gist.
- **`--spec` mode (no spec creator)** — `SpecHistoryID` is empty;
  `CoderHistoryID` is set normally.
- **Ideation pipeline (debate path)** — `SpecHistoryID` gets the planner's
  history ID; `CoderHistoryID` gets the coder's history ID. Both are returned.
