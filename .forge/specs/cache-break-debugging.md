---
id: cache-break-debugging
status: implemented
---
# Log which request component changed when prompt cache breaks

## Description
The `[CACHE BREAK]` warning reports token deltas but not what changed, making
it useless for diagnosis. This spec captures hashes of the three cacheable
request components (system blocks, tool schemas, message prefix) before each
API call and, on cache break, reports which component's hash changed plus an
optional full diff file for manual inspection. Companion to #240 (root-cause
fix for non-deterministic prompt assembly).

## Context
- `internal/runtime/loop/loop.go`:
  - `Loop` struct (~line 24) — add hash-tracking fields.
  - `runLoop` (~line 187) — compute hashes after assembling `systemBlocks`,
    `toolSchemas`, `messagesWithCache` and before the provider call.
  - `checkCacheHealth` (~line 763) — diff stored vs current hashes, enrich the
    warning, write a debug diff file.
- `internal/runtime/loop/cache_test.go` — add tests for the hashing/diff helpers.
- `internal/types/types.go` — `ChatRequest`, `ChatContentBlock`, `Tool` shapes
  used for serialization (read-only reference).

## Behavior
- Before each provider call, the loop computes a stable hash of each component:
  - system blocks (serialized, cache_control ignored)
  - tool schemas (serialized)
  - message prefix up to and including the cache breakpoint block
- The hashes plus the serialized content are stored on the `Loop` for the next
  iteration's comparison.
- When `checkCacheHealth` detects a break (>5% drop AND >2K tokens), the warning
  names each changed component and shows short hashes, e.g.:
  `[CACHE BREAK] Call #2: 2128 → 0 tokens (-2128, -100%) - changed: system (abc123→def456), tools (111aaa→222bbb)`
- If no component hash changed, the warning says `changed: none (cache break with
  identical request prefix — likely TTL expiry or provider-side eviction)`.
- On a break where content is available for changed components, the loop writes a
  unified-style diff of old vs new serialized content to
  `${TMPDIR}/forge-cache-break-{unixnano}.diff` and appends
  ` — diff: <path>` to the warning. File-write failure is non-fatal: the warning
  still emits, the diff suffix is omitted.
- First call records baselines only (no warning, no diff), matching current
  behavior keyed on `lastCacheRead == 0`.

## Constraints
- Must not change the cache breakpoint placement logic in
  `addMessageCacheControl`.
- Must not include `CacheControl` fields in component hashes (they are positional
  metadata, not content — including them would create false-positive diffs).
- Must not block or fail the loop if the diff file cannot be written.
- Must not add new external dependencies; use `crypto/sha256`, `encoding/json`,
  `os`, `path/filepath` from stdlib.
- Hashing must be deterministic for identical content (stable JSON
  serialization — rely on Go's `encoding/json` map-key sorting).
- Must not emit the diff file or per-component detail on the first call.

## Interfaces
```go
// Loop struct additions: last* = previous request, curr* = in-flight request.
type Loop struct {
    // ... existing ...
    lastSystemHash, lastToolsHash, lastMsgsHash string
    lastSystemRaw, lastToolsRaw, lastMsgsRaw    string
    currSystemHash, currToolsHash, currMsgsHash string
    currSystemRaw, currToolsRaw, currMsgsRaw    string
}

// hashComponent returns (shortHash, rawJSON). shortHash = first 6 hex chars of
// sha256(rawJSON). Returns ("error", "") if marshaling fails.
func hashComponent(v any) (hash string, raw string)

// messagePrefix returns messages up to and including the cache-breakpoint block
// (the one carrying CacheControl), with CacheControl stripped, for stable hashing.
func messagePrefix(messages []types.ChatMessage) []types.ChatMessage

// diffCacheComponents compares curr* vs last* and returns changed components.
func (l *Loop) diffCacheComponents() []cacheComponentChange

// promoteCacheHashes copies curr* into last* after each call.
func (l *Loop) promoteCacheHashes()

// writeCacheBreakDiff dumps old/new content for changed components to a temp
// file and returns the path, or "" on failure / no changes.
func writeCacheBreakDiff(changes []cacheComponentChange) string

type cacheComponentChange struct {
    Name    string // "system" | "tools" | "messages"
    OldHash string
    NewHash string
    OldRaw  string
    NewRaw  string
}
```

## Edge Cases
- First API call (`lastCacheRead == 0`): record hashes/baselines, no warning, no
  diff file.
- Cache break with all hashes identical: warning says `changed: none` and writes
  no diff file (nothing to diff).
- Empty message history (`messagePrefix` returns empty slice): hash of `[]`
  serializes deterministically; no panic.
- Diff file write fails (read-only TMPDIR, permission denied): warning emits
  without the ` — diff:` suffix; loop continues.
- Component changed but only `CacheControl` differs: hashes match (CacheControl
  stripped), so it is NOT reported as a change — avoids false positives from the
  breakpoint moving between calls.
- Hashes are updated every call (even when no break), so a break is always
  diffed against the immediately preceding request.
