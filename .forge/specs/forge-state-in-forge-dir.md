---
id: forge-state-in-forge-dir
status: implemented
---
# Store .forge-state inside .forge/ and gitignore it

## Description
The orchestrator routing-metadata file (`.forge-state`) is currently written to
the worktree root. Move it into the `.forge/` directory so it lives alongside the
rest of forge's per-project state and inherits `.forge/`'s gitignore directives.
Additionally, forge must guarantee `.forge/.gitignore` always ignores
`.forge-state`, so the transient routing file is never accidentally committed even
when `.forge/` itself is tracked. Legacy root-level `.forge-state` files must still
be read (and migrated) so existing worktrees resume cleanly.

Related: `respect-gitignored-forge` (reflect/learnings honor gitignored `.forge/`)
covers a different concern — that spec governs git staging in the reflect tool;
this spec relocates the state file and seeds `.forge/.gitignore`.

## Context
- `internal/sessionstate/sessionstate.go`:
  - `StateFile = ".forge-state"` (L20) — the bare filename, unchanged.
  - `path(worktreeRoot)` (L40-43) — currently `filepath.Join(worktreeRoot, StateFile)`.
    Must become `filepath.Join(worktreeRoot, ".forge", StateFile)`.
  - `Write(worktreeRoot, s)` (L48-66) — atomic temp-file-and-rename. Must
    `MkdirAll(worktreeRoot/.forge)` before writing and seed `.forge/.gitignore`.
  - `Read(worktreeRoot)` (L71-84) — reads the new path; must fall back to the
    legacy root path when the new one is absent.
- `internal/agent/worker.go`:
  - `initialState()` (~L78-104) — calls `sessionstate.Read(w.cwd)`. No signature
    change (still passes worktree root).
  - `persistState()` (~L106-122) — calls `sessionstate.Write(w.cwd, st)`. No change.
- `internal/agent/worker_state_persist_test.go`:
  - `TestWorker_InitialState_CorruptIsFresh` (L62-71) writes to
    `filepath.Join(cwd, sessionstate.StateFile)` — must target the new
    `.forge/` location (or a new exported path helper).
- `cmd/forge/worktree_session.go`:
  - `warnIfStateStale(worktreePath)` (L82-105) — calls `sessionstate.Read`. No change.
- `internal/sessionstate/sessionstate_test.go` — extend/add: round-trip in
  `.forge/`, gitignore seeding, legacy-fallback read.

## Behavior
- `sessionstate.Write(worktreeRoot, s)`:
  - Creates `worktreeRoot/.forge/` (mode 0o755) if absent before writing.
  - Writes state atomically to `worktreeRoot/.forge/.forge-state` via
    temp-file-and-rename (the temp file lives in `.forge/` too).
  - Ensures `worktreeRoot/.forge/.gitignore` contains a line `.forge-state`:
    - If `.forge/.gitignore` is absent, create it containing `.forge-state\n`.
    - If present but lacks an exact `.forge-state` line, append `.forge-state\n`.
    - If it already ignores `.forge-state` (exact line match, ignoring surrounding
      whitespace), leave it untouched (idempotent — no duplicate lines).
  - Remains best-effort: a `.gitignore` seeding failure is non-fatal and does not
    prevent the state write (state write is the primary operation). gitignore
    seeding errors are returned only if the state write itself also failed; a
    successful state write with a failed gitignore seed still returns nil but logs.
- `sessionstate.Read(worktreeRoot)`:
  - Reads `worktreeRoot/.forge/.forge-state` first.
  - If that path does not exist (`os.ErrNotExist`), falls back to the legacy
    `worktreeRoot/.forge-state`. A successful legacy read returns the parsed state.
  - If neither exists, returns the wrapped `os.ErrNotExist` (callers treat as fresh).
  - Version-mismatch and parse errors behave exactly as today.
- Migration: the next `Write` after a legacy-only read places the file in the new
  location. The legacy root file is left in place (not deleted) to avoid surprising
  destructive behavior; it becomes stale but harmless. (See Constraints.)
- `StateFile` constant stays `.forge-state` (bare name). A new exported helper
  surfaces the relative path for tests: see Interfaces.
- End-to-end: after one full turn in a fresh worktree, `git status --short` from
  the worktree shows no untracked/added `.forge-state` (because `.forge/.gitignore`
  ignores it), regardless of whether `.forge/` is itself tracked or ignored.

## Constraints
- Must not change the signatures of `sessionstate.Read` / `sessionstate.Write`
  (callers in `worker.go` keep passing the worktree root).
- Must not delete the legacy root `.forge-state` file (no destructive migration).
- Must not write duplicate `.forge-state` lines into `.forge/.gitignore` on
  repeated `Write` calls (idempotent seeding).
- Must not fail or abort a turn when `.forge/.gitignore` seeding fails — log only.
- Must not place the atomic temp file outside `.forge/` (rename must stay on the
  same filesystem/dir as the destination).
- Must not touch `respect-gitignored-forge`'s reflect/learnings logic.
- Must not seed any gitignore entry other than `.forge-state` (don't presume to
  ignore the whole `.forge/` — the user controls that).

## Interfaces
```go
// StateFile is the bare metadata filename (unchanged): ".forge-state".
const StateFile = ".forge-state"

// StateDir is the subdirectory under the worktree root where state lives.
const StateDir = ".forge"

// RelPath returns the worktree-relative path to the state file
// (".forge/.forge-state"), for callers/tests that need to locate it.
func RelPath() string

// path returns the absolute state-file path inside worktreeRoot/.forge.
func path(worktreeRoot string) string

// legacyPath returns the pre-migration root-level path used as a read fallback.
func legacyPath(worktreeRoot string) string

// Write atomically persists state to worktreeRoot/.forge/.forge-state, creating
// .forge/ if needed and ensuring .forge/.gitignore ignores .forge-state.
func Write(worktreeRoot string, s State) error

// Read loads state from worktreeRoot/.forge/.forge-state, falling back to the
// legacy worktreeRoot/.forge-state when the new path is absent.
func Read(worktreeRoot string) (State, error)
```

## Edge Cases
- **Fresh worktree, no `.forge/`**: `Write` creates `.forge/`, writes state, and
  creates `.forge/.gitignore` with `.forge-state`. Round-trip `Read` returns it.
- **`.forge/.gitignore` already has `.forge-state`**: `Write` leaves the file
  byte-identical (no duplicate line, no trailing-newline churn).
- **`.forge/.gitignore` has other entries** (e.g. `settings.local.json`): `Write`
  appends `.forge-state` on its own line, preserving existing entries.
- **Legacy-only state** (`worktreeRoot/.forge-state` exists, `.forge/.forge-state`
  absent): `Read` returns the legacy state; the next `Write` migrates to `.forge/`;
  legacy file remains on disk untouched.
- **Both new and legacy present**: `Read` returns the new-location state (legacy
  ignored). Resolves any ambiguity deterministically toward the new path.
- **`.forge/` exists as a file, not a directory** (pathological): `MkdirAll`
  errors; `Write` returns that error (state write fails loudly — not silently
  swallowed).
- **gitignore seed fails but state write succeeds**: `Write` returns nil, logs the
  seed failure; state is still persisted and resumable.
- **Corrupt new-location file**: `Read` returns a parse error (does NOT fall back
  to legacy on parse failure — only `os.ErrNotExist` triggers fallback), matching
  today's "corrupt is fresh" handling in `worker.initialState`.
- **Version mismatch in new file**: returns the version error as today; no legacy
  fallback (the file exists, it's just unsupported).

## Implementation Notes
- `Read` uses `errors.Is(err, os.ErrNotExist)` on the new-path read to decide
  fallback; any other read error (including a present-but-corrupt new file) is
  returned directly, so parse/version errors never trigger legacy fallback.
- `seedGitignore` is a private helper in `sessionstate.go`; it scans existing
  lines with `bufio.Scanner` + `strings.TrimSpace` for an exact `.forge-state`
  match (idempotent), appends a leading `\n` only when the file lacks a trailing
  newline. Seed failures are logged via the stdlib `log` package (non-fatal).
- Tests added in `sessionstate_test.go`: `TestWrite_SeedsGitignore`,
  `TestWrite_GitignoreIdempotent`, `TestWrite_GitignorePreservesExisting`,
  `TestWrite_GitignoreAppendsNewlineWhenMissing`, `TestRead_LegacyFallback`,
  `TestRead_LegacyMigratesOnWrite`, `TestRead_PrefersNewOverLegacy`,
  `TestWrite_ForgeIsFile_Errors`. `TestRead_UnknownVersion`/`TestRead_Corrupt`
  now write to `RelPath()` (new location) to assert no-fallback-on-parse-error.
- No caller signatures changed; comments in `worker.go`/`worktree_session.go`
  still reference `.forge-state` (the bare filename is unchanged) and remain
  accurate.
