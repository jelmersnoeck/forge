---
id: multiedit-atomic-tool
status: implemented
---
# MultiEdit tool applies ordered edits to one file atomically

## Description
Add a `MultiEdit` tool that applies an ordered list of string-replacement ops to
a single file in-memory, then writes the result once via the existing atomic
temp-file-and-rename path. All ops must apply or the whole call fails with a
diagnostic IsError ToolResult and zero file change. Reuses the smart
whitespace-tolerant matching from `edit.go`. Closes issue #293. Related:
`atomic-file-write` (atomic write path), `edit-smart-matching` (match tiers).

## Context
- `internal/tools/edit.go` — single `Edit` tool; source of `editHandler`,
  `spliceRanges`, and the find→splice→atomicWrite flow to mirror.
- `internal/tools/edit_match.go` — `findMatches(content, old, disableFuzzy)`
  returning `(ranges [][2]int, strat matchStrategy, ok bool)`; `matchExact` /
  `matchWhitespace` constants; `nearMissDiagnostic(content, old)`.
- `internal/tools/atomic_write.go` — `atomicWrite(path, content, perm)` and
  `targetPerm(path, fallback)`.
- `internal/tools/helpers.go` (or equivalent) — `requireString`, `optionalBool`,
  `errResultf`, `textResult`, `isEnvFile`, `envFileError`.
- `internal/tools/registry.go` — `NewDefaultRegistry()` registers each tool;
  add `r.Register(MultiEditTool())` after `EditTool()`.
- new: `internal/tools/multiedit.go` — tool definition + `multiEditHandler`.
- new: `internal/tools/multiedit_test.go` — tests.
- `types.ToolContext` carries `ReadState` (call `ReadState.Delete(path)` on success).

## Behavior
- Tool name `MultiEdit`; `ReadOnly: false`, `Destructive: false`.
- Input: `file_path` (string, required) and `edits` (array, required, min 1).
  Each edit is an object `{old_string, new_string, replace_all?, disable_fuzzy?}`.
- Ops apply sequentially against an in-memory string: edit N sees the result of
  edits 0..N-1. This lets a later op match text an earlier op produced.
- Per-op matching reuses `findMatches`: exact tier first, whitespace-tolerant
  tier as fallback, unless the op's `disable_fuzzy` is true (exact only).
- Per-op `replace_all` default false. When false and the op's `old_string`
  matches >1 range in the current buffer, the whole call fails (before writing)
  with `old_string appears N times ... use replace_all: true` naming the op index.
- When false, exactly one occurrence is replaced (first/only match).
- If any op's `old_string` is not found in the current buffer, the whole call
  fails with the `nearMissDiagnostic` snippet (when non-empty) plus which op
  index failed; no file is written.
- On total success: write the final buffer once via `atomicWrite` with
  `targetPerm(file_path, 0644)`, call `ctx.ReadState.Delete(file_path)`, and
  return a text result summarizing ops applied, total occurrences replaced, and
  a `(whitespace-normalized match)` note if any op used the whitespace tier.
- Reject `.env`-style paths via `isEnvFile`/`envFileError`, same as `Edit`/`Write`.
- Missing file → IsError result `file not found: <path>` (matches `Edit`).
- `MultiEdit` does NOT create new files (unlike `Write`); target must exist.

## Constraints
- Must not write the file if any op fails (all-or-nothing; validate+apply fully
  in-memory before the single `atomicWrite`).
- Must not perform more than one `atomicWrite` per call.
- Must not duplicate the match/splice logic — reuse `findMatches` and
  `spliceRanges` from the `tools` package.
- Must not create the file if it does not exist.
- Must return failures as IsError `ToolResult` (not Go `error`) so the loop
  continues, except for schema-shape failures (missing `file_path`, `edits` not
  an array) which follow `Edit`'s pattern of returning a Go error with
  `IsError: true`.
- Must not register `MultiEdit` in any restricted role preset changes — no
  edits to `AgentRolePermissions`; `coder` already allows `*`.
- Empty `edits` array → IsError, do not write.

## Interfaces
```go
// MultiEditTool returns the MultiEdit tool definition.
func MultiEditTool() types.ToolDefinition

func multiEditHandler(input map[string]any, ctx types.ToolContext) (types.ToolResult, error)

// Input schema (JSON):
// {
//   "file_path": "string (required)",
//   "edits": [
//     {
//       "old_string":    "string (required)",
//       "new_string":    "string (required)",
//       "replace_all":   "boolean (default false)",
//       "disable_fuzzy": "boolean (default false)"
//     }
//   ]  // required, minItems 1
// }
```

## Edge Cases
- Empty `edits: []` → IsError `edits must contain at least one edit`; no write.
- Op 3 of 5 fails to match → IsError naming op with 1-based index (`edit 3:`);
  file unchanged on disk.
- Sequential dependency: op 0 replaces `foo`→`bar`, op 1 matches `bar` (produced
  by op 0) → succeeds, both applied.
- Same `old_string` in two ops without `replace_all`: op 0 replaces the sole
  occurrence; op 1's `old_string` now not found → IsError, no write.
- `old_string` occurs twice, `replace_all` false → IsError `appears 2 times`,
  no write, even if later ops would succeed.
- Whitespace-only match on one op → success with `(whitespace-normalized match)`
  note; exact matches on others don't suppress the note.
- File missing → IsError `file not found`; `.env` path → env-file error result.
- `new_string` equals `old_string` (no-op edit) → applies (0 net change) and
  still counts as an applied op; file still written once (idempotent bytes).
- `edits` entry missing `old_string` or `new_string`, or not an object →
  IsError describing the malformed op with 1-based index (`edit 1 is not an
  object`, `edit 2: old_string is required`); no write.
- Note: `edits` present but not a JSON array (e.g. a string) is a schema-shape
  failure → Go `error` + `IsError: true` (like `Edit`'s required-param checks),
  distinct from the in-band IsError results above.
