---
id: agent-test-coverage
status: implemented
---
# Increase internal/agent statement coverage to 60%+

## Description
The `internal/agent` package sat at 38.6% statement coverage (the issue's
22.7% figure predates intervening work; the worker state machine extraction and
test-sync improvements had already landed). This change added targeted unit and
integration tests for the Hub queue/review methods, the `POST /review` and
`POST /interrupt` server endpoints, and the worker queue execution paths,
raising coverage to 46.7%. Tracks GitHub issue #195.

The planned `mockProvider`-driven worker loop test was NOT implemented: the
`Run`/`runOrchestrator`/`runSinglePhase` paths delegate into the `phase`
orchestrator, which needs a session store, context bundle, and live git state,
making an end-to-end unit test brittle for marginal coverage gain. This was
anticipated by the spec's final Edge Case and is left as a documented gap.

## Context
- `internal/agent/hub.go` — `EnqueueImmediate`, `EnqueueCompletion`,
  `GetImmediateQueue`, `PullCompletionQueue`, `TriggerReview`, `ReviewChannel`,
  `IsIdle` (all 0% covered).
- `internal/agent/server.go` — `handleReview` (line 118), `handleInterrupt`
  (line 175) (both 0% covered).
- `internal/agent/worker.go` — `executeImmediateQueue` (580),
  `executeCompletionQueue` (593), `executeQueuedCommand` (606),
  `Run` (209), `runOrchestrator` (447), `runSinglePhase` (493) (all 0%).
- `internal/agent/hub_test.go` — existing hub tests + `waitForWaiter` helper.
- `internal/agent/server_test.go` — `newTestServer` helper + httptest pattern.
- `internal/agent/worker_test.go` — `TestWorkerStateTransition` (state machine
  already extracted and tested).
- `internal/types/types.go` — `LLMProvider` interface (single method `Chat`),
  `ChatRequest`, `ChatDelta` — trivial to mock.
- `internal/tools/registry.go` — `NewDefaultRegistry()` for real Bash execution
  in queue tests.

## Behavior
- Hub queue tests:
  - `EnqueueImmediate("cmd")` then `GetImmediateQueue()` returns `["cmd"]`.
  - `EnqueueImmediate("")` clears the immediate queue to empty.
  - Multiple `EnqueueImmediate` calls accumulate in FIFO order.
  - `GetImmediateQueue()` returns a copy: mutating the returned slice does not
    affect subsequent calls.
  - `EnqueueCompletion("cmd")` accumulates; `PullCompletionQueue()` returns all
    queued commands and leaves the queue empty on the next pull.
  - `TriggerReview("main")` makes `ReviewChannel()` receive `"main"`.
  - `TriggerReview("")` makes `ReviewChannel()` receive `""` (auto-detect).
  - Second `TriggerReview` while one is pending is dropped (buffered size 1).
  - `IsIdle()` returns true only when a waiter is registered (worker pulling).
- Server endpoint tests (httptest pattern via extended `newTestServer` or
  per-test mux):
  - `POST /review` with body `{"base":"main"}` returns 202 with
    `{"status":"review_started"}` and `ReviewChannel()` receives `"main"`.
  - `POST /review` with empty body returns 202 and `ReviewChannel()` receives
    `""`.
  - `POST /interrupt` returns 202 with `{"status":"interrupted"}` and
    `InterruptChannel()` receives a signal.
- Worker queue execution tests (real Bash registry, `t.TempDir()` cwd):
  - `executeQueuedCommand` running `echo` emits a `queued_task_result` event
    whose Content contains the echoed text and the queue label.
  - `executeQueuedCommand` running a failing command (e.g. `exit 1` or a missing
    binary) emits a `queued_task_error` event.
  - `executeImmediateQueue` runs every command in `GetImmediateQueue()` and does
    NOT clear it (immediate queue persists across turns).
  - `executeCompletionQueue` runs every command and clears the completion queue
    (a subsequent call emits nothing).
  - Empty queues are no-ops (no events emitted).
- Worker loop test with `mockProvider`:
  - A `mockProvider` implementing `types.LLMProvider` returns a canned
    `ChatDelta` stream (text delta + stop) so a single turn completes without
    network access.
  - Drive at least one orchestrator path (QA/question intent is cheapest) and
    assert the worker emits a `done` event and transitions state out of
    `PhaseIdle`, OR — if `Run()` proves too entangled with git/PR/MCP setup —
    cover `runSinglePhase`/`runOrchestrator` indirectly and document the gap in
    Edge Cases.

## Constraints
- Must not use mocks for Bash execution — queue tests use the real
  `tools.NewDefaultRegistry()` and a `t.TempDir()` working directory.
- Must not introduce `time.Sleep` for synchronization — use channels, the
  existing `OnWaiterReady`/`OnSubscribe` hooks, or `select` with a timeout
  guard (matching existing test style).
- Must not call the real Anthropic API — worker loop tests use `mockProvider`.
- Must not modify production code purely to enable tests unless a minimal,
  justified seam is required (e.g. injecting a provider); if so, keep the change
  small and reviewable and note it here.
- Test fake data uses Community references (Troy Barnes, Greendale, etc.).
- Loop variable in table tests is `tc`; tests use `r := require.New(t)`; tables
  are `map[string]struct{...}`.
- Must not run the full `go test ./...` for `internal/tools` (idle watchdog
  hangs per learnings) — run `go test ./internal/agent/...` targeted.

## Interfaces
```go
// Test helpers added to worker_test.go:

// collectEvents returns an emit func and a pointer to captured events.
func collectEvents() (func(types.OutboundEvent), *[]types.OutboundEvent)

// newQueueWorker builds a Worker with a fresh Hub and t.TempDir() dirs.
func newQueueWorker(t *testing.T) *Worker
```
The `mockProvider` from the original plan was not built (see Description /
Edge Cases). `newTestServer` in server_test.go was extended to register the
`POST /review` and `POST /interrupt` routes.

## Edge Cases
- Empty immediate/completion queue → `executeImmediateQueue` /
  `executeCompletionQueue` return immediately, emit nothing.
  (TestWorker_ExecuteQueues_EmptyAreNoops)
- `GetImmediateQueue()` returned slice mutated by caller → internal queue
  unchanged on next read. (TestHub_GetImmediateQueue_ReturnsCopy)
- `TriggerReview` called twice before `ReviewChannel` is drained → only the
  first value is delivered (buffer size 1). (TestHub_TriggerReview_SecondDropped)
- `POST /review` with malformed JSON body → handler ignores the decode error
  (body is optional), triggers review with empty base, returns 202.
  (TestPostReview_Endpoint/malformed_JSON)
- A queued command that exits nonzero is surfaced by the Bash tool as an
  IsError ToolResult, NOT a Go error — so it hits the `queued_task_result`
  branch, not `queued_task_error`. To exercise the error branch, the test uses
  a registry with no Bash tool (Execute returns "tool not found").
  (TestWorker_ExecuteQueuedCommand_Failure)
- `Run()`/`runOrchestrator`/`runSinglePhase` remain uncovered: they require the
  full phase pipeline + session store + git state. Driving them with a
  `mockProvider` was judged too brittle for the coverage gained; documented gap.
