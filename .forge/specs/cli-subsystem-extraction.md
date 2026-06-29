---
id: cli-subsystem-extraction
status: implemented
---
# Extract cmd/forge/cli.go monolith into focused, testable subsystems

## Description
`cmd/forge/cli.go` is a 2,351-line file whose `model` struct mixes five unrelated
concerns: TUI state, event handling, output/scrollback, task-tracker rendering,
session/worktree lifecycle, and cost tracking. Extract the self-contained pieces
into dedicated types so each is independently testable and the Bubble Tea model
shrinks to roughly model definition + `Init`/`Update`/`View` glue. Tracks GitHub
issue #190. Part of the broader architecture defined in `.forge/specs/forge-architecture.md`.

This is a structural refactor: no user-visible behavior changes. The TUI must
render and behave identically before and after.

## Context
All current code lives in `cmd/forge/` as `package main`:
- `cmd/forge/cli.go` — the monolith. Holds `model`, `taskTracker`, `handleEvent`,
  `handleTaskStatus`, `finalizeTaskTracker`, `renderTaskTrackers`, `flushText`,
  `flushRawText`, `getOutputHeight`, `taskTrackerHeight`, `spawnLocalAgent`,
  `findRepoRoot`, `findWorktreeForBranch`, `isInWorktree`, `isDefaultBranch`,
  `createSession`, `detectCurrentPR`, `extractPRURL`, `listenEvents`.
- `cmd/forge/streaming_test.go` — tests `flushText`/`flushRawText` via `newTestModel()`
  (builds `model{}` directly with unexported fields).
- `cmd/forge/task_tracker_test.go` — tests `handleTaskStatus`/`finalizeTaskTracker`/
  `renderTaskTrackers` against `model{}` unexported fields.
- `cmd/forge/worktree_test.go` — tests `findRepoRoot`, `findWorktreeForBranch`,
  `isInWorktree`, `isDefaultBranch`.
- `cmd/forge/cli_test.go` — broader model/Update tests.
- `cmd/forge/worktree_session.go` — `readSessionFile`/`writeSessionFile`/`SessionInfo`
  (already separate; `spawnLocalAgent` depends on it).
- Styles (`headerStyle`, `dimStyle`, `toolStyle`, `errorStyle`, `queueStyle`,
  `thinkingStyle`, etc.) are package-level vars in `cli.go` used by every renderer.
- `cost.Tracker`, `cost.Calculate`, `cost.FormatCost` from `internal/runtime/cost`.
- `types.OutboundEvent`, `types.TokenUsage` from `internal/types`.

## Behavior
Extraction targets, each in its own file under `cmd/forge/` (NOT `internal/cli/`
— see Constraints for rationale), with its own `_test.go`:

1. **`cmd/forge/output.go` — `OutputBuffer`**: owns `lines []string`, scroll
   `offset`, and `autoScroll bool`. Methods `Append(...string)`, `Lines() []string`,
   `Len() int`, `ScrollUp(n, maxOffset int)`, `ScrollDown(n int)`, `Offset() int`,
   `SetOffset(int)`, `AutoScroll() bool`, `View(height int) string`. The scroll
   clamping math currently duplicated across `View()`, mouse handlers, and key
   handlers lives here once.

2. **`cmd/forge/tasks.go` — `TaskTrackerSet`**: owns `trackers map[string]*taskTracker`
   and `order []string`. Methods `HandleStatus(content string) (summaryLine string, ok bool)`
   where a non-empty `summaryLine` is appended to the OutputBuffer by the caller on
   terminal status; `FinalizeAll() []string`; `Render(spinner string, width int) string`;
   `Height() int`; `Len() int`. `finalizeTaskTracker` returns the summary line instead
   of mutating `model.output` directly.

3. **`cmd/forge/events.go` — `EventHandler`**: owns references to the `OutputBuffer`,
   `TaskTrackerSet`, `CostAccumulator`, `*cost.Tracker`, `*glamour.TermRenderer`,
   and current `width`/`sessionID`. **It also owns the streaming-text state**
   (`textBuf`, `streamBuf`, `streamStartIdx`) and the `flushText`/`flushRawText`
   methods, because the spec's constraint requires the stream-replace logic to read
   and adjust `OutputBuffer`'s scroll offset in place — keeping that state and the
   flush methods together in one type is the cleanest expression of that coupling.
   `Handle(event types.OutboundEvent) EventResult` replaces the 280-line
   `handleEvent` switch. `EventResult` carries `ModelName` and `PRURL`; the model
   still derives working/thinking transitions from `event.Type` in `Update` (the
   spec allowed either). The `tool_progress` case sets no output — the model reads
   `event.Content` into its `toolProgress` field in `Update` as before. The model
   keeps a `prURL` field synced from `EventResult.PRURL` after each `Handle`.
   `EventHandler` also exposes `Streaming() bool` (replaces the old
   `m.streamBuf == ""` checks), `HasPendingText() bool` (replaces `m.textBuf != ""`
   on tick), `SetWidth`/`SetRenderer` (window-resize), and `PRURL()`.

4. **`cmd/forge/session.go` — session & worktree lifecycle**: move `spawnLocalAgent`,
   `findRepoRoot`, `findWorktreeForBranch`, `isInWorktree`, `isDefaultBranch`,
   `createSession`, `detectCurrentPR`, `extractPRURL`, `prURLRe`. These are pure
   functions / IO with no TUI dependency.

5. **`cmd/forge/cost.go` — `CostAccumulator`**: owns `total`, `lastTracked`
   `types.TokenUsage`, and `modelName`. Method `Record(event types.OutboundEvent,
   tracker *cost.Tracker, sessionID string) (warning string)` performs the delta
   calculation + `tracker.Track` call currently inlined in the `usage` case of
   `handleEvent`, returning a non-empty `warning` only on a tracking error. The
   returned warning is the raw message ("  ⚠  cost tracking error: ..."); the
   caller (EventHandler) wraps it in `dimStyle` before appending, matching the
   original output byte-for-byte. Method `Summary() (total types.TokenUsage,
   model string)` for the status line, plus `SetModel(name string)`.

6. **`cmd/forge/cli.go`** after extraction: holds `model` (now composed of
   `*OutputBuffer`, `*TaskTrackerSet`, `*CostAccumulator`, `*EventHandler` plus
   pure TUI fields), `runCLI`, `Init`, `Update`, `View`, `tick`, and the small
   TUI helpers (`spinner`, `resizeTextArea`, `trySlashComplete`, `getOutputHeight`).
   Result: cli.go shrank from 2,351 → ~1,460 lines. The remaining bulk is the
   model-selector overlay, `/model` and `/review` slash-command handling, model-list
   rendering, the `send*` command builders, and review-finding formatting — none of
   which the spec enumerated as extraction targets (they are TUI/main-only helpers).
   The "~600 lines" target was aspirational; the five named subsystems were all
   extracted. `taskTrackerHeight` and `renderTaskTrackers` are gone from cli.go
   (now `TaskTrackerSet.Height`/`Render`).

7. **Existing tests migrate** to construct the new types directly.
   `streaming_test.go` now builds an `*EventHandler` via a `newTestHandler()` helper
   and asserts against `h.out.Lines()` / `h.streamBuf` / `h.textBuf` / `h.prURL` /
   `h.out.Offset()` (replaces the old `newTestModel()` and `model{}` field access).
   `task_tracker_test.go` builds a `*TaskTrackerSet` and asserts the
   `HandleStatus(...) (summary, terminal)` return value instead of reading
   `model.output`. `worktree_test.go` is unchanged — `findRepoRoot`,
   `findWorktreeForBranch`, `isInWorktree`, `isDefaultBranch` moved to `session.go`
   with identical signatures. The three `applyModelSwitch` tests in `cli_test.go`
   now init `model{... out: NewOutputBuffer()}` and read `newM.out.Lines()`. All
   pre-existing assertions still pass.

8. `just test` (or `go test ./cmd/forge/...`) passes. `just vet` clean.
   `golangci-lint run ./cmd/forge/...` clean.

## Constraints
- Keep everything in `package main` under `cmd/forge/`. Do NOT create `internal/cli/`:
  the model and helpers depend on `package main` siblings (`generateSessionName`,
  `newLightweightProvider`, slash-command helpers, `fetchModelList`, `applyModelSwitch`,
  `reviewProviderSummary`, `formatReviewFinding`, model-selector code, `readSessionFile`),
  and the existing test suite lives in `package main`. Moving to `internal/cli`
  forces exporting ~30 identifiers and a circular-ish dependency on main-only helpers.
  Same-package file split achieves the issue's testability goal without that churn.
  If the user explicitly wants `internal/cli/`, that is a larger follow-up.
- No behavior change: TUI output, scroll behavior, cost display, worktree messages,
  and event rendering must be byte-identical to pre-refactor for the same inputs.
- Do NOT change `model`'s public-facing Bubble Tea contract (`Init`/`Update`/`View`
  signatures, message types `serverEvent`/`errMsg`/`tickMsg`/`sessionTitleMsg`).
- Do NOT leave breadcrumb comments ("moved to X") at extraction sites.
- Cost tracking failures must remain non-fatal (surface as a dim warning line, never
  abort the session) — same as today.
- `flushText`'s scroll-offset adjustment (keeps viewport stable when scrolled up
  while glamour replaces raw lines) must be preserved; it couples `OutputBuffer`
  state with stream indices, so the stream-replace logic stays where it can read
  `OutputBuffer.offset`.
- Shared lipgloss style vars stay package-level (one definition); extracted files
  reference them, not redefine.

## Interfaces
```go
// output.go
type OutputBuffer struct {
    lines      []string
    offset     int
    autoScroll bool
}
func NewOutputBuffer() *OutputBuffer            // starts autoScroll=true
func (b *OutputBuffer) Append(lines ...string)
func (b *OutputBuffer) Lines() []string
func (b *OutputBuffer) SetLines(lines []string) // used by flushText stream-replace
func (b *OutputBuffer) Len() int
func (b *OutputBuffer) Offset() int
func (b *OutputBuffer) SetOffset(n int)
func (b *OutputBuffer) AutoScroll() bool
func (b *OutputBuffer) ResetScrollIfAuto()      // offset=0 when autoScroll
func (b *OutputBuffer) ScrollUp(n, maxOffset int)   // sets autoScroll=false
func (b *OutputBuffer) ScrollDown(n int)            // autoScroll=true when offset hits 0
func (b *OutputBuffer) View(height int) string      // bottom-anchored slice join

// tasks.go
type TaskTrackerSet struct {
    trackers map[string]*taskTracker
    order    []string
}
func NewTaskTrackerSet() *TaskTrackerSet
func (s *TaskTrackerSet) HandleStatus(content string) (summary string, terminal bool)
func (s *TaskTrackerSet) FinalizeAll() []string
func (s *TaskTrackerSet) Render(spinner string, width int) string
func (s *TaskTrackerSet) Height() int
func (s *TaskTrackerSet) Len() int

// cost.go
type CostAccumulator struct {
    total       types.TokenUsage
    lastTracked types.TokenUsage
    modelName   string
}
func (c *CostAccumulator) Record(ev types.OutboundEvent, t *cost.Tracker, sessionID string) (warning string)
func (c *CostAccumulator) Summary() (total types.TokenUsage, model string)
func (c *CostAccumulator) SetModel(name string)

// events.go
type EventResult struct {
    ModelName string // non-empty if a model/usage event set it
    PRURL     string // non-empty if a PR URL was detected/emitted
}
type EventHandler struct {
    out       *OutputBuffer
    tasks     *TaskTrackerSet
    cost      *CostAccumulator
    track     *cost.Tracker
    rend      *glamour.TermRenderer
    width     int
    sessionID string
    prURL     string
    textBuf        string
    streamBuf      string
    streamStartIdx int
}
func NewEventHandler(out *OutputBuffer, tasks *TaskTrackerSet, costAcc *CostAccumulator, track *cost.Tracker, rend *glamour.TermRenderer, width int, sessionID string) *EventHandler
func (h *EventHandler) Handle(ev types.OutboundEvent) EventResult
func (h *EventHandler) SetWidth(w int)
func (h *EventHandler) SetRenderer(r *glamour.TermRenderer)
func (h *EventHandler) HasPendingText() bool
func (h *EventHandler) Streaming() bool
func (h *EventHandler) PRURL() string
// flushText/flushRawText are unexported methods on *EventHandler

// session.go (signatures unchanged from current cli.go)
func spawnLocalAgent(cwd string, skipWorktree bool, branchName, initialPrompt, mode, specPath, modelName, namingHint string, issueNum int, issueURL string) (sessionID, serverURL, worktreePath, worktreeBranch string, cleanup func(), err error)
func findRepoRoot(dir string) string
func findWorktreeForBranch(repoRoot, branch string) (string, error)
func isInWorktree(dir string) bool
func isDefaultBranch(branch string) bool
func createSession(gatewayURL, cwd string) (string, error)
func detectCurrentPR(cwd string) string
func extractPRURL(text string) string
```

## Edge Cases
- Empty `OutputBuffer` (`Len()==0`): `View(h)` returns "" without slice panic;
  `ScrollUp`/`ScrollDown` are no-ops.
- `View(height)` where `height <= 0` or `height > Len()`: returns all lines joined,
  never an out-of-range slice (matches current `getOutputHeight` returning small/negative).
- `HandleStatus` with malformed JSON content: returns `("", false)`, no tracker
  created (matches current silent `json.Unmarshal` error return).
- `HandleStatus` terminal status for an unknown ID: creates then immediately
  finalizes the tracker, returns its summary line (current behavior).
- `CostAccumulator.Record` with `event.Usage == nil` or `event.Model == ""`:
  updates `total`/`modelName` from whatever is present but skips the `Track` DB
  call; returns "".
- `CostAccumulator.Record` with zero or negative delta (usage went backwards on
  resume): skips `Track`, does not advance `lastTracked` — same guard as today.
- `cost.Tracker == nil` (init failed): `Record` skips DB write, never panics.
- `spawnLocalAgent` not in a git repo: falls back to current dir, no worktree
  (current behavior preserved).
- Glamour render error during `flushText`: raw streaming lines remain in the buffer
  (no replacement), no panic — current fallback preserved.
- Concurrent SSE events arriving via `listenEvents` while `Update` mutates the
  buffer: Bubble Tea serializes `Update` calls, so the buffer is single-threaded;
  do not add locking (would be dead weight).

## Alternatives
- **Move to `internal/cli/` as the issue literally requests**: rejected for this
  pass because it forces exporting ~30 identifiers and pulls main-only helpers
  (session naming, model selector, slash commands) across the package boundary,
  ballooning scope far beyond a mechanical extraction. The same-package file split
  delivers the testability and separation the issue actually wants. Revisit if a
  second consumer of the CLI subsystems ever appears.
- **Background goroutine for glamour rendering** (issue's stuttering remedy):
  out of scope here. The extraction is the prerequisite; deferred rendering and
  per-frame event batching are a follow-up once subsystems are isolated and profilable.
