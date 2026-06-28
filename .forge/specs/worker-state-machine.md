---
id: worker-state-machine
status: implemented
---
# Replace worker state booleans with explicit state machine

## Description

The `Worker.Run()` loop in `internal/agent/worker.go` tracks phase state across
six independent variables (`orchestratorDone`, `qaActive`, `investigateActive`,
`qaHistoryID`, `investigateHistoryID`, `historyID`). This is replaced with an
explicit `WorkerState` struct and `WorkerPhase` enum that encapsulates all phase
transitions as a single method.

## Context

- `internal/agent/worker.go` — `Worker.Run()` lines 143-311 (state variables + switch)
- `internal/agent/phase/orchestrator.go` — `OrchestratorResult`, `Intent` types
- `internal/agent/phase/classify.go` — `Intent` constants (`IntentQuestion`, `IntentTask`, `IntentInvestigate`, `IntentReview`)
- `internal/agent/worker_test.go` — existing tests (pipeline hint, model alias, resolve model)

## Behavior

- A new `WorkerPhase` type is defined with constants: `PhaseIdle`, `PhaseQA`,
  `PhaseInvestigate`, `PhaseOrchestrator`, `PhaseDone`.
- A `WorkerState` struct holds `Phase`, `HistoryID`, `QAHistoryID`, and
  `InvestigateHistoryID`.
- `WorkerState.Transition(result OrchestratorResult)` applies orchestrator
  results to produce the next state. It is a pure function (no side effects).
- `WorkerState.ShouldRunOrchestrator(mode string) bool` replaces the inline
  `useOrchestrator` + `qaActive` + `investigateActive` check.
- The `Run()` loop replaces all six variables with a single `WorkerState` value
  and dispatches via `state.Phase` after transitions.
- `runOrchestrator`'s `qaHistoryID` and `investigateHistoryID` params come from
  `state.QAHistoryID` and `state.InvestigateHistoryID`.
- All existing behavior is preserved — this is a pure refactor.
- `prTerminal` remains a separate boolean (it's unrelated to phase state).

## Constraints

- Must not change any external API or public types.
- Must not change behavior — identical execution paths before and after.
- Transition logic must be a method on `WorkerState`, not inline in `Run()`.
- `WorkerState` and `WorkerPhase` live in `internal/agent/worker.go` (not a new file) — they're internal to the worker.
- No new dependencies.

## Interfaces

```go
type WorkerPhase int

const (
    PhaseIdle         WorkerPhase = iota // initial state, orchestrator not yet run
    PhaseQA                              // Q&A conversation active
    PhaseInvestigate                     // investigation conversation active
    PhaseOrchestrator                    // orchestrator completed, coder history available
    PhaseDone                            // single-phase mode completed
)

type WorkerState struct {
    Phase              WorkerPhase
    HistoryID          string // coder/plain loop history
    QAHistoryID        string // Q&A conversation history
    InvestigateHistoryID string // investigation conversation history
}

// Transition applies an OrchestratorResult and returns the new state.
func (s WorkerState) Transition(result phase.OrchestratorResult) WorkerState

// ShouldRunOrchestrator reports whether the next message should go through
// the orchestrator (vs plain loop or resume).
func (s WorkerState) ShouldRunOrchestrator(mode string) bool
```

## Edge Cases

- Q&A → Task transition: `Transition` clears `QAHistoryID` and `InvestigateHistoryID`,
  sets `Phase` to `PhaseOrchestrator`, populates `HistoryID` from `CoderHistoryID`.
- Investigate → Task transition: same as Q&A but clears investigate state.
- Review intent: sets `Phase` to `PhaseOrchestrator` (done), clears conversational state.
- Sequential Q&A then investigate: switching from Q&A to investigate clears Q&A state
  and vice versa (existing behavior).
- `mode` is not `"swe"`: `ShouldRunOrchestrator` returns true only for the first
  message (while phase is `PhaseIdle`), then `PhaseDone` is set and subsequent
  messages fall through to the plain loop.
