---
id: content-hash-read-dedup
status: implemented
---
# Content-hash fallback for Read dedup to catch identical reads across turns

## Description
The Read tool already deduplicates re-reads of unchanged files within a session
(`internal/tools/read.go` + `internal/types/ReadState`), but the freshness check
is purely mtime-based: an identical file with a newer mtime (git checkout
round-trip, `touch`, a formatter that produces byte-identical output, a save with
no edits) defeats dedup and re-sends the full content — exactly the token waste
GitHub issue #253 calls out. Add a content-hash fallback: when mtime differs,
hash the file and return the unchanged-stub if the hash still matches the
previously read content. This makes dedup content-addressed rather than
mtime-addressed without changing the tool's external contract.

This is a refinement of issue #253 Option A (reference-based dedup). Options B
(session content store) and C (summarization) are recorded under Alternatives and
intentionally out of scope.

## Context
- `internal/tools/read.go` — `readHandler`; dedup block at lines ~106-126 compares
  `entry.Offset/Limit` and `info.ModTime().Unix() == entry.MtimeUnix`, returns
  `FileUnchangedStub`. Stores `types.ReadFileEntry` at lines ~157-162.
- `internal/types/types.go` — `ReadFileEntry` struct (MtimeUnix, Offset, Limit),
  `ReadState` (Get/Set/Delete, RWMutex-guarded `entries map[string]ReadFileEntry`).
- `internal/types/readstate_test.go` — existing ReadState unit tests.
- `internal/tools/read_dedup_test.go` — existing dedup behavior tests.
- `internal/tools/edit.go` (line ~101) and `internal/tools/write.go` —
  call `ctx.ReadState.Delete(filePath)` to invalidate after mutation.
- `internal/runtime/loop/loop.go` — owns one `*types.ReadState` per session
  (`readState: types.NewReadState()`), passed via `ToolContext.ReadState`.

## Behavior
- `ReadFileEntry` gains a `ContentHash string` field holding the hash of the exact
  bytes that were line-numbered and returned (the windowed offset/limit slice, not
  the whole file), computed with SHA-256, hex-encoded.
- On a Read whose `Offset`/`Limit` match a stored entry:
  - If `info.ModTime().Unix() == entry.MtimeUnix` → return `FileUnchangedStub`
    (fast path, unchanged from today; no file read).
  - Else (mtime differs) → read the windowed content, compute its hash; if the
    hash equals `entry.ContentHash` → return `FileUnchangedStub` and refresh the
    stored entry's `MtimeUnix` to the new mtime (so the next identical read takes
    the fast path again). Otherwise return the fresh line-numbered content.
- Every full (non-stub) text read stores `ReadFileEntry{MtimeUnix, Offset, Limit,
  ContentHash}` with the hash of the bytes just returned.
- Offset/Limit mismatch still skips dedup entirely (a different window is a
  different result).
- Images remain never deduped and never stored in ReadState.
- Edit and Write continue to invalidate via `ReadState.Delete`; no change.
- Stub text constant `FileUnchangedStub` is unchanged.

## Constraints
- Must not change the Read tool's input schema, name, description, or the
  `FileUnchangedStub` text.
- Must not hash the file on the mtime-equal fast path (avoid I/O when stat already
  proves freshness).
- Must hash only the windowed bytes actually returned, not the entire file, so the
  hash matches the dedup granularity (offset/limit).
- Must remain nil-safe: a nil `ReadState` disables dedup with no panic.
- Must not persist ReadState to disk or session JSONL — it stays in-memory
  per-session.
- Must not break `ReadState` thread-safety; Get/Set/Delete keep their RWMutex.
- Must not introduce a new dependency; use `crypto/sha256` + `encoding/hex` from
  the standard library.

## Interfaces
```go
// internal/types/types.go
type ReadFileEntry struct {
    MtimeUnix   int64  // from os.Stat, seconds
    Offset      int
    Limit       int
    ContentHash string // sha256 hex of the windowed bytes returned to the model
}
```
No signature changes to `Get`, `Set`, `Delete`, `NewReadState`. read.go computes
the hash inline (e.g. a small `hashContent(s string) string` helper local to the
tools package, or inline `hex.EncodeToString(sha256.Sum256(...)[:])`).

## Edge Cases
- mtime newer, content byte-identical (touch / no-op save / checkout round-trip):
  hash matches → return stub, refresh stored mtime. No token waste.
- mtime newer, content actually changed: hash differs → return fresh content,
  store new hash + mtime.
- mtime equal: fast path returns stub without reading the file (existing behavior).
- Different offset or limit on a previously read file: dedup skipped, full content
  returned, new entry stored for that window (entries are keyed by path, so a new
  window overwrites the prior entry — same as today; acceptable since the model
  rarely interleaves windows).
- File truncated/grew but the requested window yields identical bytes (e.g. lines
  appended beyond the limit): hash of the window matches → stub returned. This is
  correct: the returned content is genuinely identical.
- Empty file: hash of empty window is stable; second read returns stub as expected.
- nil ReadState: no dedup, full content every read, no panic.
- File deleted between reads: `os.Stat` fails before the dedup block →
  existing "file not found" error path, entry left stale but never matched again.
- Concurrent reads of the same path from parallel tool calls: ReadState RWMutex
  serializes Get/Set; last writer wins on the entry, which is benign for dedup.
- Pre-existing test "bash-modified file detected via mtime" (read_dedup_test.go)
  simulated external modification by rewinding the stored mtime while leaving the
  file's bytes identical. Under content-hash dedup that now correctly returns the
  stub, so the test was updated to also change the file's content — exercising the
  real "external process changed the file" path (mtime + hash mismatch).

## Alternatives
- Option B (session-level content-addressed store with compaction-aware dedup):
  larger change touching loop history and session persistence; deferred.
- Option C (smart summarization after N reads): lossy, risks hiding content the
  model needs; deferred. The stub already lets the model re-request by reading
  with different params if needed.
