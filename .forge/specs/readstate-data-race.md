---
id: readstate-data-race
status: implemented
---
# Fix concurrent map access data race in ReadState

## Description
`ReadState` (`map[string]ReadFileEntry`) is shared across concurrent read-only tool
executions with no synchronization. Multiple goroutines reading and writing the
map simultaneously causes Go runtime panics, hangs, or silent corruption —
manifesting as agents stuck on "working..." indefinitely.

## Context
- `internal/types/types.go` — `ReadState` struct with sync.RWMutex + entries map
- `internal/types/types.go` — `ToolContext` struct (carries `*ReadState`)
- `internal/types/readstate_test.go` — unit tests for ReadState (nil safety, concurrent access)
- `internal/runtime/loop/loop.go` — `executeToolsGated()` fans out read-only tools
  concurrently, all sharing the same `*ReadState` via `ToolContext`; also
  `newToolContext()` updated to use `NewReadState()`
- `internal/tools/read.go` — `readHandler()` uses `ctx.ReadState.Get()` / `.Set()`
- `internal/tools/write.go` — `ctx.ReadState.Delete()`
- `internal/tools/edit.go` — `ctx.ReadState.Delete()`
- `internal/tools/read_dedup_test.go` — existing dedup tests updated for struct API
- `internal/tools/dotenv_integration_test.go` — updated `NewReadState()` call

## Behavior
1. Concurrent Read tool calls accessing the same `ReadState` map must not panic,
   hang, or corrupt data.
2. `ReadState` operations (get, set, delete) are protected by a mutex.
3. The `ReadState` type exposes typed methods (`Get`, `Set`, `Delete`) instead of
   raw map access — callers cannot accidentally bypass synchronization.
4. Existing dedup behavior is preserved: same-file, same-params, same-mtime
   returns the stub; mutations (Edit/Write) invalidate the entry.
5. No measurable performance regression for sequential tool calls (the mutex is
   uncontended in that case).

## Constraints
- Must not change the `ToolContext` struct layout beyond replacing the `ReadState`
  field type (pointer to struct instead of map alias).
- Must not add a mutex to `ToolContext` itself — the lock belongs to `ReadState`.
- Must not change the `ToolHandler` function signature.
- Edit and Write tools only run in the sequential (mutating) phase, so they won't
  race with each other, but the type must still be safe for future use.

## Interfaces

```go
// ReadState tracks per-file read state for dedup within a session.
// Thread-safe for concurrent access from multiple tool goroutines.
type ReadState struct {
    mu      sync.RWMutex
    entries map[string]ReadFileEntry
}

func NewReadState() *ReadState

// Get returns the entry and whether it exists.
func (rs *ReadState) Get(path string) (ReadFileEntry, bool)

// Set stores a dedup entry for the given path.
func (rs *ReadState) Set(path string, entry ReadFileEntry)

// Delete removes the entry for the given path (used by Edit/Write).
func (rs *ReadState) Delete(path string)
```

`ToolContext.ReadState` changes from `ReadState` (map alias) to `*ReadState` (pointer to struct).

Tool handlers call `ctx.ReadState.Get(...)`, `.Set(...)`, `.Delete(...)` instead
of direct map indexing.

## Edge Cases
- **Nil ReadState**: `Get` returns `(zero, false)`, `Set` and `Delete` are no-ops.
  Tests confirm nil-safe behavior is preserved.
- **Concurrent reads of same file**: Two goroutines Read the same file simultaneously.
  Both may write the same entry — last-writer-wins is fine since the entry value
  is identical.
- **Concurrent reads of different files**: No contention beyond the lock. RWMutex
  allows concurrent `Get` calls.
- **Race detector**: `go test -race` must pass on `read_dedup_test.go` and a new
  concurrent-access test.
