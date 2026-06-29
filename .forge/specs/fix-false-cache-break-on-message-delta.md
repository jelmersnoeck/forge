---
id: fix-false-cache-break-on-message-delta
status: implemented
---
# Stop false CACHE BREAK warnings caused by output-only message_delta usage

## Description
The agentic loop emits bogus `[CACHE BREAK] X → 0 tokens` warnings on every API
call. Anthropic streams two `usage` deltas per call: `message_start` (carries
cache_read/cache_creation/input) and `message_delta` (output tokens only,
cache_read defaults to 0). Both reach `checkCacheHealth`, so the zero-cache
`message_delta` always looks like a 100% cache drop. It also resets the baseline
to 0, so a *real* cache break can never be detected. Fix: only run cache-health
detection on the usage delta that actually carries cache numbers.

## Context
- `internal/runtime/loop/loop.go:565-573` — `"usage"` case dispatches every
  usage delta to `checkCacheHealth`.
- `internal/runtime/loop/loop.go:797-857` — `checkCacheHealth` logic + baseline
  reset at line 855.
- `internal/runtime/provider/anthropic.go:201-212` — `message_start` usage delta
  (cache-bearing).
- `internal/runtime/provider/anthropic.go:250-257` — `message_delta` usage delta
  (output-only, cache_read=0).
- `internal/runtime/provider/claude_cli.go:625-635` — claude CLI usage emission
  (verify single cache-bearing delta).
- `internal/runtime/loop/cache_test.go` — test home for regression coverage.
- `internal/types/types.go:115-119` — `TokenUsage` struct.

Implemented: `hasCacheSignal` added in `loop.go` just above `checkCacheHealth`;
the `"usage"` case now gates the `checkCacheHealth` call behind it. Tests added
in `cache_test.go` (`TestHasCacheSignal`,
`TestCheckCacheHealth_MessageDeltaDoesNotFalseBreak`,
`TestCheckCacheHealth_RealBreakStillFires`).

## Behavior
- A `message_delta` usage event (OutputTokens set, all cache/input fields 0)
  MUST NOT trigger a CACHE BREAK warning.
- Cost accumulation (`totalUsage`) is unchanged — output tokens from
  `message_delta` still accumulate.
- `checkCacheHealth` only consumes cache-bearing usage deltas, so `callCount`
  reflects real API calls (one increment per call, not two).
- A genuine cache break (cache_read drops >5% and >2K between two real calls)
  still fires the warning with the changed-component diff.
- The baseline (`lastCacheRead`) is only updated from cache-bearing deltas, so it
  is never clobbered to 0 by an output-only delta.

## Constraints
- Don't change cost/token accounting numbers.
- Don't move cache-health detection out of the loop into the provider.
- Don't suppress warnings by raising thresholds — fix the dispatch, not the gate.
- No new exported API unless required.

## Interfaces
Gate in the loop's `"usage"` case (loop.go), keyed on the delta carrying cache
or input signal:

```go
// hasCacheSignal reports whether a usage delta carries the cache/input numbers
// emitted on message_start (vs the output-only numbers on message_delta).
func hasCacheSignal(u *types.TokenUsage) bool {
    return u.CacheReadTokens > 0 || u.CacheCreationTokens > 0 || u.InputTokens > 0
}
```

`checkCacheHealth` is only called when `hasCacheSignal(delta.Usage)` is true.

## Edge Cases
- First real call with cache_read=0 but input>0 (cold cache): hasCacheSignal true
  → baseline recorded, no warning. Correct.
- A real call that legitimately has cache_read=0 AND input=0 AND creation=0:
  impossible for a non-empty prompt (input is always >0), so the gate never
  drops a real call.
- message_delta with OutputTokens>0 only: hasCacheSignal false → skipped for
  health, still accumulated for cost.
- Two consecutive real calls, second genuinely evicted (cache_read 20000→0 but
  input>0): hasCacheSignal true on both → warning fires legitimately.
