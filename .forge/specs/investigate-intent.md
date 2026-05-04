---
id: investigate-intent
status: implemented
---
# Add investigation intent to classifier

## Description
The intent classifier only distinguishes `question` (read-only Q&A) from `task`
(full SWE pipeline: spec → code → review). When a user asks to "dig into an
issue," "investigate why X is broken," or "understand the repo structure," the
classifier sees imperative verbs and defaults to `task` — which launches the
spec-creator phase and produces a spec nobody asked for.

A third intent, `investigate`, fills the gap: active exploration with full tool
access (including file writes for notes/scratch if needed) but no spec creation,
no PR, no review pipeline. Think of it as "Q&A with a mandate to go deep."

Related spec: `intent-classification` (implemented) — this extends it with a
third intent category.

## Context
- `internal/agent/phase/classify.go` — `Intent` type, `ClassifyIntent()`, `classificationSystemPrompt`, `parseIntent()`
- `internal/agent/phase/classify_test.go` — classification unit tests
- `internal/agent/phase/phase.go` — `Phase` struct, `QA()` definition (investigate phase will be similar but with broader tool access)
- `internal/agent/phase/prompts.go` — `qaPrompt`, `PromptForPhase()`
- `internal/agent/phase/orchestrator.go` — `Orchestrator.Run()`, `OrchestratorResult`, `runQA()`, intent routing switch
- `internal/agent/worker.go` — `Run()` message loop, `qaHistoryID`/`qaActive` state tracking, intent switch on `result.Intent`
- `internal/types/types.go` — `LightweightModels`, `OutboundEvent`
- `cmd/forge/cli.go` — `handleEvent()` for `intent_classified` display

## Behavior

### New intent: `investigate`
- The classifier gains a third intent value: `investigate`.
- Classification prompt updated with clear examples:
  - `question`: informational, "how does X work?", "what files handle Y?", "explain Z"
  - `investigate`: active exploration or debugging, "dig into this issue," "figure out why X fails," "look into the test failures," "analyze the performance of Y," "understand the codebase structure and report back"
  - `task`: actionable change request, "add a flag," "fix the nil pointer," "implement retry logic"
- Ambiguous cases between `investigate` and `task` default to `investigate` — it's cheaper to explore first and transition to task later than to waste a spec cycle.
- Ambiguous cases between `question` and `investigate` default to `investigate` — a deeper answer is better than a shallow one.

### Investigation phase
- When classified as `investigate`, the orchestrator runs a conversation loop with:
  - **Full read tools**: Read, Grep, Glob, Bash, WebSearch
  - **Write tools allowed**: Write, Edit (the agent may want to create scratch notes or prototype)
  - **Disallowed**: PRCreate, Agent/AgentGet/AgentList/AgentStop, TaskCreate/TaskGet/TaskList/TaskStop/TaskOutput, QueueImmediate/QueueOnComplete, UseMCPTool, Reflect
  - An investigation-specific system prompt that emphasizes thorough exploration, root-cause analysis, and clear reporting — but no spec writing.
- Investigation phase uses the same model as the rest of the session (not Haiku).
- MaxTurns: 200 (same as Q&A — explorations can be extensive).
- The investigation loop returns a historyID, preserved for follow-up messages.

### Transition: investigate → task
- After an investigation turn completes, subsequent messages go through classification again.
- If the follow-up is classified as `task`, the SWE pipeline launches with context: "Based on our previous investigation, the user now wants to implement: {message}. Use the context from the investigation to inform the spec."
- If the follow-up is classified as `investigate` again, the investigation loop resumes (same as Q&A multi-round behavior).

### State tracking in worker
- The worker tracks investigation state the same way it tracks Q&A: `investigateHistoryID` and `investigateActive` flags.
- Both `qaActive` and `investigateActive` feed back into the orchestrator on subsequent messages. Only one can be active at a time.
- Transition from Q&A to investigate (or vice versa) starts a fresh loop — they're different conversation modes with different tool sets.

### Events
- `intent_classified` event content gains `"investigate"` as a possible value.
- CLI display: when `"investigate"` is received, show subtle indicator (e.g., dimmed "investigating...").

### OrchestratorResult
- `OrchestratorResult` gains `InvestigateHistoryID string` field, set when `Intent == IntentInvestigate`.

## Constraints
- Must not modify the core `loop.Loop` implementation.
- Must not change behavior of `--mode spec`, `--mode code`, `--mode review`.
- Must not change behavior when `--spec` is provided (that's still unambiguously `task`).
- Classification must still complete within the existing latency budget (<500ms target, 2s timeout per attempt).
- The classification prompt must stay within ~200 input tokens + ~20 output tokens.
- Default-to-task on classification failure is preserved (not default-to-investigate).
- Investigation phase must not create specs, create PRs, or spawn sub-agents.
- Backward-compatible: if an external system only knows `question`/`task`, an `investigate` result should be gracefully handled (it's a superset of `question` in terms of capabilities).

## Interfaces

```go
// internal/agent/phase/classify.go

const (
    IntentQuestion    Intent = "question"
    IntentInvestigate Intent = "investigate"
    IntentTask        Intent = "task"
)
```

```go
// internal/agent/phase/phase.go

// Investigate returns the investigation phase configuration.
// Full exploration with tool access — no specs, no PRs, no sub-agents.
func Investigate() Phase
```

```go
// internal/agent/phase/prompts.go

const investigatePrompt = `...` // investigation-specific system prompt

func PromptForPhase(name string) string // updated to handle "investigate"
```

```go
// internal/agent/phase/orchestrator.go

type OrchestratorResult struct {
    Intent               Intent
    QAHistoryID          string
    InvestigateHistoryID string // new
    CoderHistoryID       string
    SpecHistoryID        string
}
```

```go
// internal/agent/worker.go — updated state tracking

// Worker gains:
// - investigateHistoryID string
// - investigateActive bool
// Same lifecycle as qaHistoryID/qaActive.
```

## Edge Cases
- User says "dig into this issue and fix it" — classifier should return `task` (explicit "fix" signals a change request). The classification prompt must include examples of mixed intent defaulting to `task` when a change verb is present.
- User says "investigate the test failures" then follows up with "ok fix them" — investigation loop completes, follow-up classified as `task`, SWE pipeline launches with investigation context.
- User says "investigate" then "actually just tell me how it works" — investigation is active, follow-up classified as `question`, starts fresh Q&A loop (different tool set).
- Classification returns `investigate` but the current provider doesn't support the model — same fallback chain as today (try all LightweightModels, default to `task` on total failure).
- User in investigation mode uses Ctrl+C — same behavior as Q&A (cancels turn, preserves historyID).
- User sends empty prompt after investigation — same as today, skip classification.
- Long investigation prompt (>1000 chars) — truncated at word boundary same as today, full prompt goes to investigation loop.
