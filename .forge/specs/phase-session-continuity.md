---
id: phase-session-continuity
status: implemented
---
# Cross-phase context preservation for QA/Investigate → Spec transitions

## Description
The SWE orchestrator has two context-preservation problems, at different layers:

**Solved (coder ↔ review loop):** The coder phase preserves its conversation
across review-fix cycles via `loop.Resume()`. The spec creator and planner
store historyIDs for future resumption. Reviewers remain stateless. This was
the original scope of this spec and is already implemented.

**New (QA/Investigate → Spec):** When a user transitions from QA or Investigate
intent to a Task (triggering the SWE pipeline), the orchestrator replaces the
prior conversation with a text hint: *"use the context from the conversation"*
— without passing the actual conversation. The spec creator starts fresh,
knowing nothing about what was discussed. This spec extends cross-phase context
to cover QA→Spec and Investigate→Spec transitions.

Related: issue #205.

## Context
Files that change or are relevant:

- `internal/runtime/loop/loop.go` — Add `SendWithContext` method: loads messages
  from a prior historyID into `l.history` (as read-only context) but keeps the
  loop's own fresh `historyID`. Does not overwrite `l.historyID` like `Resume` does.
- `internal/agent/phase/orchestrator.go` — Lines 154-168 (`switch` block for
  QA/Investigate transition): replace text-only prompt augmentation with
  `SendWithContext` call that passes the prior `historyID` into the spec-creator
  or ideation pipeline. `runSpecCreator` gains `priorHistoryID` parameter.
- `internal/agent/phase/debate.go` — `DebateOpts` gains `PriorHistoryID` field.
  `runPlanner` uses `SendWithContext` when `PriorHistoryID` is set.
- `internal/agent/phase/session_continuity_test.go` — New tests for
  QA→Spec and Investigate→Spec context propagation.

Already implemented (no changes needed):
- `internal/agent/phase/phase.go` — `Result.HistoryID` (already exists).
- `internal/agent/phase/debate.go` — `DebateResult.PlannerHistoryID` (exists).
- Coder resume via `loop.Resume()` (exists, unchanged).
- Reviewer statelessness (exists, unchanged).

## Behavior

### Cross-phase context (new)
- **QA → Spec**: When `opts.QAHistoryID` is set and intent classifies as `task`,
  the orchestrator passes `QAHistoryID` to `runSpecCreator` (or `RunDebate` for
  the ideation path). The spec-creator loop loads the QA conversation history
  into its context via `loop.SendWithContext`, then sends the user's new prompt.
  The spec creator sees the full QA exchange — questions asked, answers given,
  files explored — and uses it to write a better-informed spec.
- **Investigate → Spec**: Same mechanism as QA → Spec, using
  `opts.InvestigateHistoryID`. The investigation conversation (tool calls,
  findings, analysis) becomes context for spec creation.
- **Prompt still augmented**: The text prefix ("Based on our previous
  discussion...") is still prepended to the prompt as a signal to the LLM, but
  now the actual conversation history backs it up.
- **Context is read-only**: The spec-creator loop gets its own fresh `historyID`.
  Prior history is loaded into `l.history` for LLM context but persisted under
  the new historyID, not appended to the QA/Investigate session file. This
  means the QA session file stays clean, and the spec session is independently
  resumable.
- **Ideation pipeline**: When the ideation pipeline runs (large tasks), the
  prior historyID flows through `DebateOpts.PriorHistoryID` to the planner
  phase. Ideator agents do NOT receive prior context (they work from the prompt
  alone); only the planner (which writes the spec) gets it.

### Coder session continuity (already implemented)
- Coder resume via `loop.Resume()` with stored `coderHistoryID`.
- Spec creator returns `historyID` in `Result` for future resumption.
- Reviewer remains stateless — fresh `ChatRequest` each cycle.

## Constraints
- `loop.Resume` semantics must not change — it still overwrites `l.historyID`
  for same-session continuation. The new `SendWithContext` is a separate method.
- Must not duplicate message persistence — prior messages are loaded into
  `l.history` but persisted under the new historyID only when the loop runs
  (the loop already persists all of `l.history` on first Send).
- Prior context must not be persisted twice. `SendWithContext` loads messages
  into `l.history` for LLM context but does NOT call `persistMessage` for
  the loaded messages. Only new messages (user prompt, assistant responses)
  are persisted under the new historyID.
- Token budget: prior context + spec-creator conversation must stay within
  the loop's compaction budget. If the QA conversation was very long,
  compaction will trim the oldest prior turns first. This is acceptable —
  recent context is more relevant.
- Reviewers must never receive cross-phase context.
- `RunSinglePhase` and `RunReviewOnly` behavior must not change.
- The `QAHistoryID` / `InvestigateHistoryID` fields on `OrchestratorOpts` must
  still be cleared after the transition to prevent double-loading on retry.

## Interfaces

```go
// internal/runtime/loop/loop.go

// SendWithContext loads prior conversation history from priorHistoryID
// into the loop's message history (for LLM context), then sends promptText
// as a new user message. Unlike Resume, the loop keeps its own fresh
// historyID — the prior messages are context, not continuation.
//
// If priorHistoryID is empty or the session cannot be loaded, falls back
// to a plain Send (no error — missing context is degraded, not fatal).
func (l *Loop) SendWithContext(ctx context.Context, priorHistoryID string, promptText string, emit func(types.OutboundEvent)) error

// internal/agent/phase/orchestrator.go

// runSpecCreator gains priorHistoryID parameter.
func (o *Orchestrator) runSpecCreator(ctx context.Context, opts OrchestratorOpts, priorHistoryID string) (Result, error)

// internal/agent/phase/debate.go

// DebateOpts gains PriorHistoryID.
type DebateOpts struct {
    // ... existing fields ...
    PriorHistoryID string // loaded into planner context for QA/Investigate→Spec
}
```

## Edge Cases
- **Prior session file missing or corrupted** — `SendWithContext` logs a
  warning and falls back to `Send`. The spec creator runs without prior
  context (same as current behavior). Not fatal.
- **Very long QA conversation (>100k tokens context)** — The loop's compaction
  mechanism trims the oldest turns. Prior QA context is oldest, so it gets
  trimmed first. The spec creator's own tool calls and reasoning are preserved.
  Acceptable degradation.
- **QA session had tool_use/tool_result blocks** — These are valid
  `ChatMessage` entries and load correctly. The spec creator sees what files
  the QA agent read, which is useful context for spec writing.
- **Small task route (skipSpec=true)** — `runCoderDirect` is called instead of
  `runSpecCreator`. The prior historyID should still flow to `runCoderDirect`
  so small tasks benefit from QA context too.
- **Ideation pipeline + prior context** — Prior context goes to the planner
  only, not the ideator agents. Ideators work from the user prompt alone —
  they don't need QA context, and adding it would bloat their already-large
  prompts.
- **First review cycle has no findings** — Coder historyID stored but never
  used for Resume. No harm (existing behavior, unchanged).
- **Empty prior history (QA session with 0 messages)** — `SendWithContext`
  loads an empty slice, effectively behaving like `Send`. No special case needed.
- **Context cancelled mid-SendWithContext** — Same as mid-Send: context
  cancelled, loop returns error, worker emits interrupted event.
