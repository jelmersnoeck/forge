# Learnings - 2026-06-28 17:40

- The negative-token cost bug was that CostAccumulator.Record's skip guard ANDed all four token fields (delta.X<=0 && ...), letting partially-negative deltas through. Fix: clamp each field at 0 independently (clampNonNegative), skip only when ALL four clamped fields are 0. Constraint: never skip a record just because one field is non-positive while another has a positive clamped delta.
- CostAccumulator.lastTracked must be advanced to *ev.Usage UNCONDITIONALLY (even on skipped writes and usage drops), not max(lastTracked,usage). A per-field max would keep a stale high-water mark after a resume/subagent reset and cause perpetual negative deltas that swallow later growth.
