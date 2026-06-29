---
id: fix-negative-token-counts
status: implemented
---
# Prevent negative token deltas from being written to cost DB

## Description
The cost DB records negative token counts because the skip guard in
`CostAccumulator.Record` uses AND across all four token fields: a single
positive field lets the whole (partially-negative) delta through. Negative
deltas occur when cumulative usage decreases between events (session resume,
subagent usage reset, or non-monotonic API values). Fix by clamping each delta
field at zero, advancing the per-field baseline correctly even when usage drops,
and skipping writes when the clamped delta is all-zero. Also provide a one-time
cleanup of existing garbage rows.

## Context
- `cmd/forge/cost.go` — `CostAccumulator.Record` computes per-call deltas and
  persists via `cost.Tracker.Track`. The `delta.X <= 0 && ...` AND guard at
  lines 49-52 is the bug.
- `cmd/forge/cost_test.go` — new test file (does not exist yet).
- `internal/runtime/cost/tracker.go` — `Tracker.Track(...)` writes rows;
  `Tracker` opens `~/.forge/costs.db`.
- `internal/runtime/cost/cost.go` — `Calculate(model, usage)` computes call cost.
- `internal/runtime/cost/tracker_test.go` — `TestPurgeNegativeRowsOnOpen`
  covers the legacy-row cleanup on tracker reopen.
- `cmd/forge/events.go:178` — `"usage"` event case calls
  `h.cost.Record(event, h.track, h.sessionID)` and surfaces any warning dimmed.

## Behavior
- Each delta field (`InputTokens`, `OutputTokens`, `CacheCreationTokens`,
  `CacheReadTokens`) is clamped to a minimum of 0 before being recorded. No
  negative value is ever passed to `Track`.
- The baseline `lastTracked` is unconditionally advanced to the latest
  `ev.Usage` (even when the write is skipped — `c.lastTracked = *ev.Usage`). This
  is a straight re-base to the observed cumulative, NOT a per-field
  `max(lastTracked, usage)`. On a usage drop (resume / subagent reset) this
  re-bases every field down so subsequent growth is measured against the fresh
  lower cumulative count instead of a stale high value that would swallow it.
- After clamping, if all four delta fields are zero the record is skipped (no DB
  write, returns `""`), and `lastTracked` is still advanced.
- `callCost` is computed from the clamped delta, so cost never goes negative.
- When usage increases monotonically (normal case), behavior is unchanged:
  delta = current − lastTracked, recorded as before.
- DB write failures still return the `"⚠ cost tracking error: ..."` warning and
  are non-fatal.
- Cleanup of existing garbage: `cost.NewTracker` runs an idempotent
  `DELETE FROM cost_records WHERE <any token column> < 0` immediately after
  schema creation, purging legacy negative rows on the next `forge`/`forge stats`
  invocation. No manual SQL required.

## Constraints
- Must not pass a negative value to `Tracker.Track`.
- Must not skip a record solely because one field is non-positive while another
  field has a positive clamped delta.
- Must not let `lastTracked` retain a value higher than the latest observed
  usage for any field (would cause perpetual negative deltas).
- Must not make cost-tracking failures fatal to the session.
- Must not change `Calculate` or `Track` signatures.

## Interfaces
```go
// clampNonNegative returns u with every token field floored at 0.
func clampNonNegative(u types.TokenUsage) types.TokenUsage

func (c *CostAccumulator) Record(ev types.OutboundEvent, t *cost.Tracker, sessionID string) (warning string)

// In internal/runtime/cost/tracker.go, run on NewTracker after schema:
const purgeNegativeRows = `DELETE FROM cost_records WHERE input_tokens < 0 OR ...;`
```

## Edge Cases
- All fields negative delta (full usage reset on resume): clamped to all-zero →
  skipped, `lastTracked` advanced down to the new lower values.
- Mixed delta (input=-288, output=+89): input clamped to 0, output recorded as
  89 → row written with input=0, output=89, never -288.
- Reset then growth: after a drop to low cumulative values, the next higher event
  records the full delta against the re-based-down baseline (not swallowed).
- All-zero delta (duplicate usage event): skipped, no row.
- `ev.Usage == nil` or `ev.Model == ""` or `t == nil`: no DB write, returns "".
- Existing garbage rows already in DB: purged once on tracker open; purge is a
  no-op on subsequent runs.
