---
id: edit-patch-support
status: draft
---
# Patch tool: apply unified diffs with multi-hunk batch edits

## Description
Phase 2 of issue #251. Add a way to apply unified-diff / `git apply`-style
patches so the agent can make many edits across one or more files in a single
tool call instead of one Edit call per change. Implemented as a new `Patch` tool
rather than overloading `Edit`. Complements the `edit-smart-matching` spec
(Phase 1), which sharpens single-region replacements.

## Context
- `internal/tools/edit.go` — existing Edit tool (unchanged by this spec except
  description cross-reference if useful).
- New file `internal/tools/patch.go` — `PatchTool()`, `patchHandler`.
- New file `internal/tools/patch_test.go` — table-driven tests, loop var `tc`.
- `internal/tools/registry.go` — `NewDefaultRegistry()` registers built-ins;
  add `r.Register(PatchTool())`.
- `internal/tools/helpers.go` — `textResult`, `errResultf`, `requireString`.
- `internal/tools/dotenv.go` — `isEnvFile`, `envFileError` (apply per-file guard).
- `internal/types/types.go` — `ToolContext.ReadState`; call
  `ReadState.Delete(path)` for each file the patch modifies.
- `AGENTS.md` "Key files" tool list — add Patch.

## Behavior
- New tool `Patch` registered in `NewDefaultRegistry()`.
- Input: a single `patch` string in unified diff format. Supports standard
  headers (`--- a/path`, `+++ b/path`, `@@ -l,s +l,s @@` hunks) and multiple
  file sections in one patch.
- Application is **all-or-nothing per call**: if any hunk in any file fails to
  apply cleanly, no files are written and an error is returned naming the first
  failing file + hunk and why (context mismatch, line offset, etc.).
- File paths come from the diff headers. The `b/` (new) path is the target;
  strip a leading `a/` or `b/` prefix when present (git style). Relative paths
  resolve against the tool's working directory.
- Hunk application tolerates small line-number drift: locate hunk context within
  a bounded fuzz window (like `patch --fuzz`) rather than trusting `@@` line
  numbers blindly; if context cannot be located within the window, the hunk fails.
- Supports file creation (`--- /dev/null` → new file) and file deletion
  (`+++ /dev/null` → remove file) when the diff expresses them.
- On success, returns a summary: number of files changed and, per file,
  +added/-removed line counts, e.g.
  `applied patch: 2 file(s) changed (+12 -4)`.
- `.env` files are rejected: if any file section targets an env file, the whole
  patch is refused with `envFileError`-style messaging and nothing is written.
- After a successful apply, `ctx.ReadState.Delete(path)` is called for every
  modified/created/deleted path.
- Empty or non-diff `patch` input returns a clear parse error; nothing written.

## Constraints
- Must be atomic per call: never leave a partial multi-file/multi-hunk apply on
  disk. Stage all results in memory, validate every hunk, then write.
- Must not shell out to `git apply` or `patch` — implement parsing/application
  in Go so behavior is deterministic and dependency-free at the OS level.
- Prefer the standard library; if a unified-diff parsing dependency is needed,
  use one already present in `go.sum` (e.g. `github.com/aymanbagabas/go-udiff`
  or `github.com/hexops/gotextdiff`) and promote it to a direct require rather
  than adding a brand-new module. Confirm choice before adding.
- Must not modify `Edit` tool matching behavior (that is `edit-smart-matching`).
- Must reject patches that target `.env`-class files via existing `isEnvFile`.
- Handler returns `(result, nil)` for user-facing failures per `errResult`
  convention.

## Interfaces
```go
// PatchTool returns the Patch tool definition.
func PatchTool() types.ToolDefinition
// Input schema:
//   patch: string (required) — unified diff text, one or more file sections.

func patchHandler(input map[string]any, ctx types.ToolContext) (types.ToolResult, error)

// parsedPatch is the in-memory representation prior to applying.
type filePatch struct {
	OldPath string // from --- header (may be /dev/null)
	NewPath string // from +++ header (may be /dev/null)
	Hunks   []hunk
	Create  bool
	Delete  bool
}

type hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	Lines    []string // each prefixed with ' ', '+', or '-'
}

// parsePatch parses unified diff text into per-file patches.
func parsePatch(patch string) ([]filePatch, error)

// applyHunks applies fp's hunks to original content with bounded fuzz,
// returning new content or an error identifying the failing hunk.
func applyHunks(original string, fp filePatch) (string, error)
```

## Edge Cases
- **Patch references a file that does not exist (and is not a creation)**:
  error naming the missing file; nothing written.
- **One hunk applies, a later hunk in the same file fails**: whole call fails;
  the earlier hunk's change is NOT written (atomic).
- **Multi-file patch where the second file fails**: first file is not written;
  error names the second file/hunk.
- **Context lines drifted by a few lines from the `@@` numbers**: hunk still
  applies via fuzz window; success.
- **Context cannot be found within fuzz window**: hunk fails with a clear
  context-mismatch message including the expected context.
- **Patch creates a new file (`--- /dev/null`)**: file created with the added
  lines; parent dirs created if needed; ReadState updated.
- **Patch deletes a file (`+++ /dev/null`)**: file removed; ReadState updated.
- **Patch targets a `.env` file among others**: entire patch refused; no file
  (including non-env files in the same patch) is modified.
- **Malformed / empty / non-diff input**: parse error; nothing written.
- **CRLF line endings in target file**: hunks match against normalized line
  breaks; written output preserves the file's original line endings.

## Alternatives
- Extend `Edit` to accept a `patch` field instead of a separate tool: rejected;
  a distinct tool keeps each tool's schema and error surface focused and avoids
  overloading Edit's `old_string`/`new_string` contract.
- Shell out to `git apply`: rejected for non-determinism, repo-state coupling
  (index/worktree), and the no-silent-failure requirement.
