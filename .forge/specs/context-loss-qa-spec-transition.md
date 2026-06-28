---
id: context-loss-qa-spec-transition
status: implemented
---
# Carry conversation history into spec phase on Q&A/investigate→task transition

## Description

When a user starts with a Q&A or investigation session and then asks to
implement something, the orchestrator transitions to the SWE pipeline but
discards the prior conversation history. The spec-creator only receives a
text-augmented prompt ("Based on our previous discussion...") with no actual
context from the conversation. The spec it produces is uninformed.

Fix: resume the spec-creator phase from the Q&A/investigate history so it
has full context of what was discussed.

## Context

- `internal/agent/phase/orchestrator.go` — transition logic (lines 154-168),
  `runSWEPipeline`, `runSpecCreator`
- `internal/agent/phase/phase.go` — phase definitions (spec-creator, coder)
- `internal/agent/worker.go` — worker message loop, Q&A/investigate state
- `internal/runtime/loop/loop.go` — `Send` vs `Resume` for history continuity
- `internal/runtime/session/` — JSONL session persistence

## Behavior

1. When the orchestrator detects a Q&A→task transition (QAHistoryID is set and
   intent is task), the spec-creator phase MUST resume from the Q&A history
   rather than starting fresh.
2. When the orchestrator detects an investigate→task transition
   (InvestigateHistoryID is set and intent is task), the spec-creator phase
   MUST resume from the investigation history rather than starting fresh.
3. The augmented prompt text ("Based on our previous discussion...") is still
   sent as the new user message when resuming — it acts as the transition
   instruction.
4. For the small-task path (direct coder, no spec), the same history carry-over
   applies: resume the coder from the Q&A/investigate history.
5. The spec-creator phase may use different tools and system prompt than Q&A —
   the resume should inject the spec-creator phase prompt, not the Q&A prompt.
6. After spec creation completes, the coder phase starts fresh (from the spec)
   as it does today — only the spec-creator benefits from the history carry-over.

## Constraints

- Do NOT change the behavior when there is no prior history (fresh task requests
  must work identically to today).
- Do NOT carry Q&A/investigate history into the coder phase — the coder gets
  its context from the spec.
- Do NOT change the session persistence format.
- The history carry-over must work with both `session.Store` JSONL files and
  in-memory history.

## Interfaces

```go
// OrchestratorOpts gains a new field:
type OrchestratorOpts struct {
    // ... existing fields ...

    // TransitionHistoryID carries the conversation history from a prior
    // Q&A or investigation phase into the next phase (spec-creator or
    // direct coder). Set during Q&A→task or investigate→task transitions
    // so the spec-creator can Resume() with full prior context.
    TransitionHistoryID string
}

// runSpecCreator and runCoderDirect check opts.TransitionHistoryID:
// - Non-empty: call l.Resume(ctx, transitionHistoryID, prompt, emit)
// - Empty: call l.Send(ctx, prompt, emit) (current behavior)
// - Resume failure: log warning, fall back to l.Send()
```

## Edge Cases

- **No prior history**: QAHistoryID and InvestigateHistoryID are both empty →
  spec-creator starts fresh (current behavior, unchanged).
- **History file missing/corrupt**: If the JSONL file for the Q&A session is
  gone or unreadable, Resume() should fail gracefully and fall back to Send().
- **Small task after Q&A**: The direct coder path should also resume from Q&A
  history so context isn't lost for one-liners either.
- **Large task after investigation**: Ideation pipeline (debate) should NOT
  receive the investigation history — it's a multi-agent process that doesn't
  support Resume(). Only the single-agent spec-creator path benefits.
  (Implemented: TransitionHistoryID is set but the debate path doesn't use it.)
- **Multiple transitions**: User does Q&A → investigate → task. Only the most
  recent history (investigate) should be carried forward.
  (Implemented: the switch/case picks the first non-empty ID.)
