---
id: atomic-file-write
status: implemented
---
# Write/Edit tools use atomic temp-file-and-rename for crash safety

## Description
The Write tool (`internal/tools/write.go`) and Edit tool (`internal/tools/edit.go`)
call `os.WriteFile` directly, leaving files in corrupted partial states if the
process crashes, the disk fills, or power is lost mid-write. Replace direct writes
with an atomic write-temp-then-rename pattern via a shared helper. Resolves issue #255.

## Context
- `internal/tools/write.go` — `writeHandler` currently calls `os.WriteFile(filePath, ..., 0644)`.
- `internal/tools/edit.go` — line 97 calls `os.WriteFile(filePath, ..., 0644)`, clobbering existing file mode.
- `internal/tools/atomic_write.go` (new) — shared `atomicWrite` helper + orphan cleanup.
- `internal/tools/atomic_write_test.go` (new) — crash-safety + helper unit tests.
- `internal/tools/write_test.go` — existing table tests; add atomicity/perm cases.
- `internal/tools/edit_test.go` — existing table tests; add Edit permission-preservation case.
- `internal/tools/helpers.go` — existing `errResultf`, `textResult` helpers.

## Behavior
- `atomicWrite(path, content, perm)` writes `content` to a temp file in the same
  directory as `path`, `Sync`s it, `Chmod`s to `perm`, then `os.Rename`s over `path`.
- On any failure before rename, the temp file is removed and the original `path`
  is left untouched (unchanged bytes, unchanged mode).
- Temp files are named with prefix `.forge-write-` so they are identifiable and
  hidden from typical globs.
- Write tool: when overwriting an existing file, the existing file's permission
  bits are preserved; for a new file the default mode is `0644`.
- Edit tool: always preserves the existing file's permission bits (it only edits
  files that already exist).
- `cleanupOrphanTempFiles(dir)` removes any `.forge-write-*` files directly in
  `dir` (best-effort; returns count removed, ignores individual remove errors).
- Successful Write still returns `wrote N bytes to <path>` and calls
  `ctx.ReadState.Delete(filePath)`; Edit behavior/return is unchanged except the
  underlying write is now atomic.

## Constraints
- Must not use `os.WriteFile` for the final target in `writeHandler` or the edit handler.
- Temp file must be created in `filepath.Dir(path)`, never in the system temp dir,
  so `os.Rename` stays on the same filesystem (rename across filesystems errors).
- Must not leave a temp file behind on the error path.
- Must not change the Write/Edit tool input schemas or success message formats.
- Must not silently swallow the real write error — surface it via `errResultf`.

## Interfaces
```go
// atomicWrite writes content to path atomically: temp file in the same
// directory, fsync, chmod, then rename over the target.
func atomicWrite(path string, content []byte, perm os.FileMode) error

// cleanupOrphanTempFiles removes leftover .forge-write-* temp files in dir.
// Returns the number removed. Best-effort; per-file remove errors are ignored.
func cleanupOrphanTempFiles(dir string) (int, error)

// targetPerm returns the permission bits to apply to path: the existing file's
// current mode if path exists, otherwise fallback. Used by both Write and Edit
// to drive perm preservation through atomicWrite.
func targetPerm(path string, fallback os.FileMode) os.FileMode

const atomicTempPrefix = ".forge-write-"
```

## Edge Cases
- Empty content → atomicWrite creates a 0-byte file via rename; reader sees empty file, never a missing file.
- Target does not exist yet → Write uses perm `0644`; rename creates the file atomically.
- Target exists with mode `0600` → Write/Edit preserve `0600` after the rename.
- Disk full during temp write → `tmp.Write`/`Sync` errors; temp removed; original file unchanged; handler returns error result.
- Rename fails (e.g. target dir removed mid-op) → temp removed; error surfaced.
- Concurrent reader of `path` during write → reader sees either the old complete file or the new complete file, never a partial one (rename is atomic on POSIX).
- Orphan temp file from a previous crash → `cleanupOrphanTempFiles` removes it; does not touch non-matching files or subdirectories.
- `path` in a directory where temp creation is denied (read-only dir) → `os.CreateTemp` errors; original untouched.
