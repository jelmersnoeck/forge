---
id: cache-break-debugging
status: implemented
---
# Log which request component changed when prompt cache breaks

## Description
The `[CACHE BREAK]` warning reports token deltas but not what changed, making
it useless for diagnosis. This spec captures hashes of four cacheable
request components (system blocks, tool schemas, message prefix, request
params=model+maxTokens) before each API call and, on cache break, reports which
component's hash changed plus an optional full diff file for manual inspection.
When nothing changed, it prints all four hash values so they can be verified by
hand. Companion to #240 (root-cause fix for non-deterministic prompt assembly).

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
  - request params: `map[string]any{"model": req.Model, "maxTokens": req.MaxTokens}`
- The hashes plus the serialized content are stored on the `Loop` for the next
  iteration's comparison.
- When `checkCacheHealth` detects a break (>5% drop AND >2K tokens), the warning
  names each changed component and shows short hashes, e.g.:
  `[CACHE BREAK] Call #2: 2128 → 0 tokens (-2128, -100%) - changed: system (abc123→def456), tools (111aaa→222bbb)`
- If no component hash changed, the warning shows all four current hashes so they
  can be manually verified, e.g.:
  `[CACHE BREAK] Call #2: 2128 → 0 tokens (-2128, -100%) - changed: none (TTL expiry or provider eviction?) — hashes: system=abc123, tools=def456, msgs=789abc, params=fedcba`
- On a break where content is available for changed components, the loop writes a
  unified-style diff of old vs new serialized content to
  `${TMPDIR}/forge-cache-break-{unixnano}.diff` (mode 0600 — the dump may contain
  sensitive system/tool/message content) and appends ` — diff: <path>` to the
  warning. File-write failure is non-fatal: the warning still emits. Per the
  project's "no silent failures" rule, the warning appends
  ` — diff write failed: <err>` instead of silently omitting the suffix, so the
  reason the diff is missing is surfaced.
- First call records baselines only (no warning, no diff), matching current
  behavior keyed on `lastCacheRead == 0`.

### Operational risk: diff files in TMPDIR
The diff dump contains raw system/tool/message content. It is written with mode
`0600` (owner read/write only), and the only request params serialized are the
forge-controlled `model` + `maxTokens` scalars — never free-form user input — so
the params component cannot leak arbitrary data. The residual risk is the
location: `os.TempDir()` resolves to `$TMPDIR` (or `/tmp`), which on a shared or
multi-tenant host may be world-readable as a directory and is not auto-cleaned by
forge. Operators on such hosts should: (1) run forge with a private, per-user
`TMPDIR`; (2) treat `forge-cache-break-*.diff` files as sensitive and prune them;
and (3) note these files are only ever written on an actual cache break, so they
are rare. A configurable/dedicated diff directory is a possible future
enhancement (not implemented).

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
    lastSystemHash, lastToolsHash, lastMsgsHash, lastParamsHash string
    lastSystemRaw, lastToolsRaw, lastMsgsRaw, lastParamsRaw     string
    currSystemHash, currToolsHash, currMsgsHash, currParamsHash string
    currSystemRaw, currToolsRaw, currMsgsRaw, currParamsRaw     string
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
// file (mode 0600) and returns the path. Returns ("", nil) when there is nothing
// to diff, or ("", err) when the file write fails. Callers treat the diff as
// best-effort: on err they append a "diff write failed" note to the warning
// rather than swallowing the error, and never block the loop.
func writeCacheBreakDiff(changes []cacheComponentChange) (string, error)

type cacheComponentChange struct {
    Name    string // "system" | "tools" | "messages" | "params"
    OldHash string
    NewHash string
    OldRaw  string
    NewRaw  string
}
```

## Edge Cases
- First API call (`lastCacheRead == 0`): record hashes/baselines, no warning, no
  diff file.
- Cache break with all hashes identical: warning says `changed: none` followed by
  all four current hash values (system/tools/msgs/params) and writes no diff file.
- Cache break caused only by `model` or `maxTokens` change: the `params` component
  is reported as changed even when system/tools/messages are identical.
- Empty message history (`messagePrefix` returns empty slice): hash of `[]`
  serializes deterministically; no panic.
- Diff file write fails (read-only TMPDIR, permission denied): the warning still
  emits and the loop continues; per the no-silent-failures rule it appends a
  ` — diff write failed: <err>` note instead of dropping the suffix silently.
- Component changed but only `CacheControl` differs: hashes match (CacheControl
  stripped), so it is NOT reported as a change — avoids false positives from the
  breakpoint moving between calls.
- Hashes are updated every call (even when no break), so a break is always
  diffed against the immediately preceding request.
