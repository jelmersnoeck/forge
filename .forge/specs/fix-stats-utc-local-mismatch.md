---
id: fix-stats-utc-local-mismatch
status: implemented
---
# Fix cost stats hiding today's usage from UTC/local query-range mismatch

## Description
`forge stats` showed "No data for this period" in the daily/session breakdown
while the top-line Total was nonzero. Cost rows are stored in UTC but the daily
and session queries filtered with raw local-time boundaries, so evening usage in
negative-offset zones (already "tomorrow" in UTC) fell outside the window.
Fixes issue #300.

## Context
- `internal/runtime/cost/tracker.go` — `GetDailySummaries` and
  `GetSessionBreakdown` bound raw local `start`/`end` into a `timestamp >= ? AND
  timestamp < ?` WHERE clause. `MonthlyTotal` already bound `.UTC()`.
- `internal/runtime/cost/tracker_test.go` — regression coverage.
- `cmd/forge/stats.go` — builds local-time boundaries; unchanged (fix lives in
  tracker so all callers benefit, mirroring `MonthlyTotal`).

## Behavior
- `GetDailySummaries(start, end)` binds `start.UTC()` and `end.UTC()` into the
  WHERE clause; `DATE(timestamp, 'localtime')` GROUP BY is unchanged so daily
  buckets stay in the user's local day.
- `GetSessionBreakdown(start, end)` binds `start.UTC()` and `end.UTC()`.
- A row recorded at local June 30 21:15 PDT (UTC July 1 04:15) appears in both
  the daily breakdown (bucketed as 2026-06-30) and session breakdown when queried
  with local-time June boundaries.

## Constraints
- Must not change the GROUP BY axis to UTC — filter window and display bucket are
  distinct; only the WHERE window is normalized to UTC.
- Must not double-shift the timestamp.
- Must not alter `Track` (still stores `time.Now().UTC()`) or `MonthlyTotal`.

## Interfaces
```go
func (t *Tracker) GetDailySummaries(start, end time.Time) ([]DailySummary, error)
func (t *Tracker) GetSessionBreakdown(start, end time.Time) ([]SessionBreakdown, error)
```

## Edge Cases
- Evening local, next-day UTC (PDT -0700): row now counted in daily/session views.
- Empty database: unchanged; returns empty slices.
- CI in UTC zone: `TestEveningLocalCrossesUTCDay` uses an explicit
  `America/Los_Angeles` location for its boundaries, so it is deterministic
  regardless of the machine timezone.
