---
id: phase-session-continuity
status: implemented
---
# Persistent sessions for spec and coder phases across review cycles

## Description
The SWE orchestrator currently creates a fresh conversation loop for every phase
invocation — spec creator, coder, and coder-fix all start from scratch. This
wastes context: the coder phase loses its entire implementation history when
asked to fix review findings. The spec creator similarly loses context if
the user provides follow-up feedback. This spec adds session continuity for
the spec-creator and coder phases while keeping reviewers stateless.

## Context
Files changed:

- `internal/agent/phase/orchestrator.go` — `runSWEPipeline`, `runCoder`, `runCoderResume`: `runCoder` now returns `(historyID, error)`. New `runCoderResume` uses `loop.Resume()` with the stored historyID. `runCoderWithMessage` deleted (replaced by the two methods). `runSpecCreator` returns historyID in Result.
- `internal/agent/phase/phase.go` — `Result` struct gains `HistoryID` field for session resumption plumbing.
- `internal/agent/phase/debate.go` — `DebateResult` struct gains `PlannerHistoryID` field. `runPlanner` stores `l.HistoryID()` in the result.
- `internal/runtime/loop/loop.go` — `Loop.Send()`, `Loop.Resume()`, `Loop.HistoryID()`: unchanged (existing API is sufficient).
- `internal/agent/phase/session_continuity_test.go` — new test file with 5 tests covering coder resume, historyID propagation, session store continuity, spec creator historyID, and reviewer statelessness.

## Behavior
- **Coder session continuity**: The first `runCoder` call creates a new conversation loop and stores its `historyID`. Subsequent `runCoderWithMessage` calls (triggered by review findings) use `loop.Resume()` with the stored historyID instead of creating a fresh loop. The fix message is injected as the new user prompt into the existing conversation, so the coder retains full context of what it built and why.
- **Spec creator session continuity**: If the spec creator needs to be re-invoked (future extension point), it resumes from its stored historyID rather than starting from scratch. Currently the spec creator runs once, so this is plumbing for when user feedback loops are added to the spec phase.
- **Reviewer statelessness**: The review orchestrator (`runReviewerWithDiff`) continues to start fresh every cycle. Each review run gets only the diff + specs + reviewer prompt — no conversation history from previous review rounds. This is intentional: reviewers should evaluate the current state of the code without bias from previous review context.
- **historyID ownership**: The orchestrator's `runSWEPipeline` method owns the historyIDs for each resumable phase. These are local variables scoped to the pipeline run — not persisted across separate orchestrator invocations.
- **Session persistence**: Each `loop.Send()` and `loop.Resume()` already persists messages via the session store. The historyID connects the JSONL entries across Resume calls, so the full conversation (initial coder prompt + implementation + fix prompt 1 + fixes + fix prompt 2 + ...) is recoverable from a single historyID.
- **Debate/ideation pipeline**: When the ideation pipeline is used (`shouldIdeate` returns true), the planner phase (which writes the spec) should also store its historyID for potential resumption, though currently only the coder historyID is used in the review-fix loop.

## Constraints
- Must not change the `loop.Loop` API — the existing `Send`/`Resume`/`HistoryID` contract is sufficient.
- Must not leak historyIDs outside the pipeline run — they are not returned in `OrchestratorResult` (except for QA, which already does this).
- Reviewers must never receive conversation history from previous rounds — they always get a fresh `ChatRequest` with only the diff.
- Must not change the behavior of `RunSinglePhase` or `RunReviewOnly` — those are standalone invocations that correctly start fresh.
- Token budget is managed by the loop's existing compaction mechanism — long conversations from many review cycles will be compacted automatically.
- The `ReadState` (file read dedup) should carry over within the same loop instance — this is already the case since the loop is reused.

## Interfaces
```go
// internal/agent/phase/orchestrator.go

// runSWEPipeline gains local historyID tracking.
// No new types or exported functions — the change is internal to the method.

func (o *Orchestrator) runSWEPipeline(ctx context.Context, opts OrchestratorOpts, specPath string) error {
    // ... spec creation (stores specHistoryID for future use) ...

    var coderHistoryID string

    // Phase 2: Coder — first invocation uses Send
    coderHistoryID, err = o.runCoder(ctx, opts, specPath)

    // Phase 3: Review → Fix loop
    for cycle := 0; cycle < o.maxReviewCycles; cycle++ {
        // ... review (always fresh) ...

        // Fix: Resume the coder's conversation
        coderHistoryID, err = o.runCoderResume(ctx, opts, coderHistoryID, fixMsg)
    }
}

// runCoder creates a new loop, returns its historyID.
func (o *Orchestrator) runCoder(ctx context.Context, opts OrchestratorOpts, specPath string) (string, error)

// runCoderResume resumes an existing coder conversation with a new message.
func (o *Orchestrator) runCoderResume(ctx context.Context, opts OrchestratorOpts, historyID, message string) (string, error)

// internal/agent/phase/phase.go

// Result gains HistoryID for session resumption plumbing.
type Result struct {
    Phase     string
    SpecPath  string
    Diff      string
    Findings  []review.Finding
    HistoryID string // conversation historyID for session resumption
}

// internal/agent/phase/debate.go

// DebateResult gains PlannerHistoryID for future planner resumption.
type DebateResult struct {
    SpecPath         string
    Winner           Candidate
    Alternatives     []Candidate
    PlannerHistoryID string // planner conversation historyID
}
```

## Edge Cases
- **First review cycle has no findings** — coder historyID is stored but never used for Resume. No harm; the JSONL file exists with the initial conversation.
- **Coder loop hits token budget during Resume** — the loop's compaction kicks in, removing oldest turns. The coder loses some early context but the most recent work and the current fix request are preserved. This is already handled by `tokens.Compact`.
- **Context cancelled mid-Resume** — same behavior as current mid-Send: turn context is cancelled, loop returns error, worker emits interrupted event.
- **Max review cycles reached with critical findings** — the cycle counter resets (existing behavior). The coder conversation continues growing via Resume, but compaction keeps it within bounds.
- **Debate pipeline + coder resume** — the planner phase in the debate pipeline creates the spec but runs in its own loop. The coder phase still starts fresh (its own loop), then gets resumed on fix cycles. The planner's historyID is stored but unused in the current flow.
- **Empty fix message from reviewer** — `formatFindingsForCoder` produces an empty-ish message if there are no high-severity findings. The guard `HasHighSeverityFindings` prevents this case — the loop breaks before calling `runCoderResume`.
