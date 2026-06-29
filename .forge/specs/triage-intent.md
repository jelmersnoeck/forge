---
id: triage-intent
status: implemented
---
# Add triage intent that investigates a problem and files a GitHub issue

## Description
Forge classifies user intent into `question`, `investigate`, `review`, and
`task`. None of these capture "something is broken but I don't know what" — a
user describing a symptom ("there's a bug in xyz", "why isn't this working?")
who wants the agent to find the root cause and file a trackable issue, not fix
it inline. This spec adds a fifth intent, `triage`: the agent investigates like
`investigate`, then files a GitHub issue (via `gh issue create`) capturing the
root cause and a clear implementation brief. The filed issue can then be worked
on asynchronously by another forge worker via `forge --issue <url>`, closing the
loop with the existing `issue-driven-sessions` spec.

Related specs: `investigate-intent` (implemented) — triage reuses its
investigation phase shape but adds an issue-filing deliverable.
`issue-driven-sessions` (implemented) — consumes the issues triage produces.

## Context
- `internal/agent/phase/classify.go` — `Intent` type + consts, `parseIntent()`, `parseClassification()`, `classificationSystemPromptTmpl`
- `internal/agent/phase/classify_test.go` — classification unit tests
- `internal/agent/phase/phase.go` — `Phase` struct, `Investigate()`; add `Triage()`
- `internal/agent/phase/prompts.go` — `investigatePrompt`, `PromptForPhase()`; add `triagePrompt`
- `internal/agent/phase/orchestrator.go` — `Orchestrator.Run()` intent switch, `OrchestratorOpts`, `OrchestratorResult`, `runInvestigate()`/`runConversationPhase()`; add `runTriage()`
- `internal/agent/worker.go` — `WorkerPhase` consts, `WorkerState`, `Transition()`, `phaseName()`/`phaseFromName()`, `ShouldRunOrchestrator()`, `runOrchestrator()` signature
- `internal/agent/worker_state_persist_test.go` — state round-trip tests
- `internal/types/types.go` — `OutboundEvent` docs (`intent_classified` content values)
- `cmd/forge/events.go` — `intent_classified` display switch (line ~237)
- `cmd/forge/issue.go` — existing `gh issue view` ingestion; triage is the inverse (`gh issue create`)
- `internal/tools/registry.go` — `NewDefaultRegistry()`; triage phase uses Bash + `gh`, no new tool registered
- `internal/sessionstate/sessionstate.go` — `State` struct gains `TriageID string` (`json:"triageID"`) so the triage history persists across restarts. Schema Version stays 1 — the new field is additive and absent old files decode to "".
- `cmd/forge/events.go` — added `parseClassifiedIntent(content string) string` helper that parses the `intent` field out of the JSON `intent_classified` payload (falling back to the raw trimmed string for bare-string callers). This fixed the pre-existing bug where the display switch string-matched the whole JSON object and so never rendered `question`/`investigate`.
- `cmd/forge/events_test.go` — new unit tests for `parseClassifiedIntent`.

## Behavior

### New intent: `triage`
- The classifier gains a fifth intent value: `triage`.
- Classification prompt updated with examples distinguishing it from neighbors:
  - `investigate`: explore/understand, report back to the user in-session.
    "dig into this," "figure out why X fails," "analyze the perf of Y."
  - `triage`: user reports a problem/symptom and wants it tracked, not answered
    inline. "there's a bug in the auth flow," "why isn't the cache working?
    file an issue," "the export is broken — track it."
  - The presence of a tracking signal ("file an issue," "track this," "log a
    bug," "open a ticket") or a clear bug-report framing routes to `triage`.
- Ambiguity rules (additive to existing rules):
  - `investigate` vs `triage` → `investigate` (default to the cheaper,
    no-side-effect path; a user who wants an issue can say so or follow up).
  - `triage` vs `task` with a change verb ("fix the bug in xyz") → `task`
    (explicit fix request still wins; existing rule unchanged).

### Triage phase
- When classified as `triage`, the orchestrator runs a conversation loop with:
  - **Read + explore tools**: Read, Grep, Glob, Bash, WebSearch.
  - **Bash allowed** (needed for `gh issue create` and repro commands).
  - **Write/Edit allowed** for scratch notes (same as investigate).
  - **Disallowed**: same set as `Investigate()` minus nothing new — i.e.
    PRCreate, Agent/AgentGet/AgentList/AgentStop, TaskCreate/TaskGet/TaskList/
    TaskStop/TaskOutput, QueueImmediate/QueueOnComplete, UseMCPTool, Reflect.
  - A triage-specific system prompt that mandates: investigate root cause, then
    file a GitHub issue via `gh issue create` with a title, root-cause summary,
    affected files, reproduction steps, and a suggested implementation
    approach. The prompt must instruct the agent to print the created issue URL
    in its final message.
- Triage phase uses the same model as the session (not Haiku).
- MaxTurns: 200 (same as investigate — root-causing can be extensive).
- The triage loop returns a historyID, preserved for follow-up messages.

### Issue filing mechanism
- The agent files the issue using the `gh` CLI through the Bash tool:
  `gh issue create --title "..." --body "..."`. No new Go tool is added —
  `gh` is already a project dependency (see `cmd/forge/issue.go`).
- The triage prompt instructs the agent to verify `gh` availability first
  (`gh --version` or `command -v gh`); if absent, the agent reports the issue
  body inline to the user instead of failing silently, and states that `gh` is
  required to file automatically.
- The issue body must be structured so a downstream `forge --issue` session can
  implement it directly: a `## Problem`, `## Root Cause`, `## Affected Files`,
  `## Reproduction`, and `## Suggested Approach` section.

### Transition: triage → task
- After a triage turn completes, subsequent messages re-classify.
- If the follow-up is classified as `task`, the SWE pipeline launches with
  context: "Based on our previous triage, the user now wants to implement:
  {message}. Use the context from the triage to inform the spec." (mirrors the
  investigate→task augmentation in `orchestrator.go`).
- If the follow-up is classified as `triage` again, the triage loop resumes
  (same multi-round behavior as Q&A/investigate).

### State tracking in worker
- `WorkerPhase` gains `PhaseTriage`.
- `WorkerState` gains `TriageHistoryID string`.
- `Transition()` handles `IntentTriage`: sets `Phase = PhaseTriage`,
  `TriageHistoryID = result.TriageHistoryID`, clears `QAHistoryID` and
  `InvestigateHistoryID`.
- `phaseName()`/`phaseFromName()` map `PhaseTriage` ↔ `"triage"`.
- `ShouldRunOrchestrator()` returns true for `PhaseTriage` when `mode == "swe"`
  (same as PhaseQA/PhaseInvestigate).
- `runOrchestrator()` signature gains a `triageHistoryID string` parameter,
  threaded into `OrchestratorOpts.TriageHistoryID`.
- Only one of qa/investigate/triage history IDs is non-empty at a time.

### Events
- `intent_classified` event content gains `"triage"` as a possible value.
  (Note: orchestrator currently emits a JSON object
  `{"intent":..,"size":..,"spec_match":..}` for this event; preserve that
  format — the CLI display switch must parse intent from it. If the existing
  CLI switch compares the raw string against `"question"`/`"investigate"`, fix
  it to extract the `intent` field so `triage` displays correctly. See Edge
  Cases.)
- CLI display: when intent is `triage`, show dimmed indicator
  `"triaging — investigating and filing an issue..."`.

### OrchestratorResult / OrchestratorOpts
- `OrchestratorResult` gains `TriageHistoryID string`, set when
  `Intent == IntentTriage`.
- `OrchestratorOpts` gains `TriageHistoryID string`, resumes an existing triage
  conversation when set.

## Constraints
- Must not modify the core `loop.Loop` implementation.
- Must not change behavior of `--mode spec`, `--mode code`, `--mode review`.
- Must not change behavior when `--spec` is provided (still unambiguously `task`).
- Must not change behavior for issue-driven sessions (`ForceTask` still wins —
  a session started from `forge --issue` never routes to triage).
- Classification must stay within the existing latency budget (6s timeout per
  attempt) and the ~200 input / ~64 output token bound. Adding one intent line
  and one ambiguity rule to the prompt must not push it over.
- Default-to-task on classification failure is preserved (not default-to-triage).
- The triage phase must not create PRs, spawn sub-agents, or queue background
  tasks. Its only side effect is creating a GitHub issue via `gh`.
- No new built-in Go tool is added; issue creation goes through Bash + `gh`.
- If `gh` is unavailable, the agent must surface the issue body inline rather
  than fail silently (no swallowed errors).

## Interfaces

```go
// internal/agent/phase/classify.go
const (
    IntentQuestion    Intent = "question"
    IntentInvestigate Intent = "investigate"
    IntentReview      Intent = "review"
    IntentTriage      Intent = "triage" // new
    IntentTask        Intent = "task"
)
// parseIntent and parseClassification gain an IntentTriage case in their
// validation switches.
```

```go
// internal/agent/phase/phase.go

// Triage returns the triage phase configuration. Investigates a reported
// problem and files a GitHub issue — no specs, no PRs, no sub-agents.
func Triage() Phase
```

```go
// internal/agent/phase/prompts.go
const triagePrompt = `...` // investigate + mandate to file a gh issue
func PromptForPhase(name string) string // handles "triage"
```

```go
// internal/agent/phase/orchestrator.go
type OrchestratorOpts struct {
    // ... existing fields ...
    TriageHistoryID string // resumes an existing triage conversation
}

type OrchestratorResult struct {
    Intent               Intent
    QAHistoryID          string
    InvestigateHistoryID string
    TriageHistoryID      string // new
    CoderHistoryID       string
    SpecHistoryID        string
}

func (o *Orchestrator) runTriage(ctx context.Context, opts OrchestratorOpts) (string, error)
```

```go
// internal/agent/worker.go
const (
    PhaseIdle WorkerPhase = iota
    PhaseQA
    PhaseInvestigate
    PhaseTriage // new
    PhaseOrchestrator
    PhaseDone
)

type WorkerState struct {
    Phase                WorkerPhase
    HistoryID            string
    QAHistoryID          string
    InvestigateHistoryID string
    TriageHistoryID      string // new
}
```

## Edge Cases
- User says "fix the bug in the auth flow" — change verb present → classified as
  `task`, not `triage`. The classification prompt must keep the
  "change verb wins → task" rule.
- User says "there's a bug somewhere in the export, can you look into it?" —
  no tracking signal, no fix verb → ambiguous between investigate and triage;
  defaults to `investigate`. Acceptable; user can follow up "file an issue."
- User says "investigate the cache misses and file an issue" — tracking signal
  ("file an issue") → `triage`.
- `gh` is not installed / not authenticated — the triage agent detects this,
  prints the full issue body inline, and tells the user `gh` is required to
  file automatically. The phase still returns success (no error), historyID
  preserved.
- `gh issue create` fails (network, no repo, permissions) — the agent surfaces
  the actual `gh` stderr to the user, does not pretend it succeeded, and leaves
  the issue body in the conversation so nothing is lost.
- User in triage mode hits Ctrl+C mid-investigation — same as investigate:
  cancels the turn, preserves `TriageHistoryID` for the next message; no
  partial issue is filed (issue creation is the final step).
- Empty prompt after a triage turn — skip classification (existing behavior),
  resume the plain/triage loop.
- Classifier returns `triage` but the model is unavailable — same fallback
  chain as today (try all `LightweightModels`, default to `task` on total
  failure).
- Persisted state with `phase=triage` from a prior run resumes into PhaseTriage
  with TriageHistoryID restored; an unknown/corrupt phase label degrades to
  PhaseIdle (existing `phaseFromName` default).
- `intent_classified` event carries JSON content — the CLI display switch must
  extract the `intent` field (not string-match the whole payload) so `triage`,
  `question`, and `investigate` all render. If the current switch is already
  broken for the JSON format, this spec's implementation fixes it for triage at
  minimum.
- Follow-up after triage is itself a `triage` (second problem reported) — the
  triage loop resumes with accumulated context, same as investigate multi-round.
