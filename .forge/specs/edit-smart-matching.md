---
id: edit-smart-matching
status: implemented
---
# Edit tool: whitespace-tolerant matching with diagnostic errors

## Description
The Edit tool (`internal/tools/edit.go`) uses naive exact string matching, the
single most common source of agent edit failures (issue #251). This is the
single spec for the Edit tool. The implemented scope (Phase 1) is a tiered
matching strategy (exact → whitespace-normalized) plus diagnostic error
messages that show the closest near-miss. AST-aware editing and unified-diff /
multi-hunk patch application are out of scope here and captured as future work
in Alternatives.

## Context
- `internal/tools/edit.go` — `EditTool()`, `editHandler`, plus new
  `spliceRanges` helper. Now uses `findMatches` + range splicing instead of
  `strings.Count`/`strings.Replace`.
- `internal/tools/edit_match.go` (new) — `findMatches`, `exactMatches`,
  `whitespaceMatches`, `splitLines`, `normalizeLine`, `nearMissDiagnostic`,
  and helpers (`blockEqual`, `allEmpty`, `blockSimilarity`, `formatDiff`).
- `internal/tools/edit_test.go` — original table-driven tests (loop var `tc`).
- `internal/tools/edit_match_test.go` (new) — tests for `findMatches`,
  `nearMissDiagnostic`, and whitespace-tier editHandler behavior.
- `internal/tools/helpers.go` — `textResult`, `errResultf`, `requireString`,
  `optionalBool`.
- `internal/tools/dotenv.go` — `isEnvFile`, `envFileError` (preserved guard).
- `internal/types/types.go` — `ToolContext.ReadState` (nil-safe). `editHandler`
  calls `ctx.ReadState.Delete(filePath)` after a successful write.

## Behavior
- Matching is tiered, attempted in order; the first tier that yields a usable
  match wins:
  1. **Exact**: byte-identical substring match (current behavior).
  2. **Whitespace-normalized line match**: match where each line of `old_string`
     equals a contiguous run of file lines after trimming trailing whitespace
     and normalizing leading-indentation differences (tabs/spaces) and
     collapsing internal runs of spaces/tabs between non-space tokens to the
     file's actual run. The replacement preserves the file's original text for
     the matched region's surrounding indentation where unambiguous.
- The matched region of the file (the actual bytes found, not `old_string`) is
  what gets replaced with `new_string`. Implementation: `spliceRanges` replaces
  each matched byte range verbatim with `new_string`. This is the safe
  realization of "preserve the file's original indentation" — bytes outside the
  matched span are untouched, and `new_string` carries its own indentation.
- Uniqueness rules are unchanged in spirit: if a tier finds more than one match
  and `replace_all` is false, return an error stating how many matches were
  found and that `replace_all: true` is required. `replace_all` replaces every
  match found by the winning tier.
- Tiers do not mix: matches are only counted within a single winning tier. If
  the exact tier finds ≥1 match, the normalized tier is never consulted.
- Success result text states which strategy matched when it was not exact, e.g.
  `replaced 1 occurrence(s) in /p (whitespace-normalized match)`. Exact matches
  keep the existing message `replaced N occurrence(s) in /p`.
- On zero matches across all tiers, the error message includes a diagnostic:
  the closest near-miss line block in the file (by line-based similarity) shown
  as a unified-diff-style snippet of expected (`old_string`) vs found, capped at
  ~40 lines total. Message ends with the existing `old_string not found in <path>`
  substring so existing assertions/tooling still match.
- `.env` files still rejected via `envFileError` before any matching.
- `ctx.ReadState.Delete(filePath)` is still called after a successful write.
- A new optional boolean input `disable_fuzzy` (default false) forces exact-only
  matching, restoring strict legacy behavior for callers that need it.

## Constraints
- Must not introduce new direct module dependencies; use the standard library
  only for matching (no tree-sitter, no external diff lib in this spec).
- Must not change the existing success message format for exact matches.
- Must not replace a region the normalized tier matched ambiguously: if
  normalization would map `old_string` onto overlapping or differently-indented
  regions such that the replacement indentation is undecidable, fall through to
  treating it as "not found" with a diagnostic, never guess silently.
- Must not perform AST parsing or language-specific logic.
- Must not write the file if zero matches or an ambiguous match occurs.
- Must keep `editHandler` returning `(result, nil)` for user-facing failures
  (errors surface to the LLM, not the loop) per `errResult` convention.

## Interfaces
```go
// EditTool input schema gains:
//   "disable_fuzzy": { "type": "boolean", "description": "Force exact matching only (default false)" }

// matchStrategy identifies which tier produced a match (for result messaging).
type matchStrategy int

const (
	matchExact matchStrategy = iota
	matchWhitespace
)

// findMatches returns the byte ranges in content matching old according to the
// first successful tier, the strategy used, and ok=false if no tier matched.
func findMatches(content, old string, disableFuzzy bool) (ranges [][2]int, strat matchStrategy, ok bool)

// nearMissDiagnostic builds a capped unified-diff-style snippet comparing old
// against the most similar line-block in content. Used in not-found errors.
func nearMissDiagnostic(content, old string) string
```

## Edge Cases
- **Empty file, non-empty old_string**: zero matches → not-found error with
  diagnostic; `nearMissDiagnostic` returns `"file is empty"` for blank content;
  file untouched.
- **old_string differs only by trailing whitespace on one line**: exact tier
  fails, whitespace tier matches exactly one region → replaced, message notes
  whitespace-normalized match.
- **old_string differs by leading indentation (tabs vs spaces)**: whitespace
  tier matches. Replacement is verbatim splice of `new_string` over the matched
  file bytes — `new_string` carries its own indentation, so no silent guessing.
- **Two whitespace-normalized matches, replace_all=false**: error reporting
  count ≥2 and requiring `replace_all: true`; file untouched.
- **disable_fuzzy=true with only a whitespace-variant present**: exact tier
  fails, normalized tier skipped → not-found error with diagnostic.
- **old_string present exactly once AND as a whitespace variant elsewhere**:
  exact tier wins (count=1) and replaces only the exact occurrence; normalized
  matches ignored. NOTE: exact substring matching can also pick up an
  occurrence embedded inside an indented line (e.g. `a b` inside `  a b`), so a
  variant is only "whitespace-only" if its literal bytes appear nowhere.
- **Ambiguous indentation mapping**: handled by the all-blank-block guard
  (`allEmpty`) — a normalized block of only empty lines carries no
  distinguishing content and is refused (treated as not-found), never guessed.
- **CRLF vs LF line endings**: `splitLines` strips a trailing `\r` before
  normalizing, so `\r\n` and `\n` compare equal. The written region is a
  verbatim splice, preserving the file's original line endings outside the
  replaced span.

## Alternatives
- Levenshtein/character-level fuzzy matching with a tolerance threshold: rejected
  for Phase 1 as too prone to silent wrong-region edits. Line-based whitespace
  normalization is the safe, high-value subset. Character fuzzing can be a later
  tier if needed.
- tree-sitter AST-aware edits: deferred. Large dependency + per-language grammar
  surface. Noted as a future extension point only.

## Future work (not implemented)
These were previously tracked as a separate `edit-patch-support` spec, now folded
here so the Edit tool has a single source of truth. Not built; left as deferred
scope.
- **Unified-diff / `git apply`-style patches** so the agent can make many edits
  across one or more files in a single tool call. Likely a distinct `Patch`
  tool (`internal/tools/patch.go`) rather than overloading `Edit`'s
  `old_string`/`new_string` contract — keeps each tool's schema and error
  surface focused.
  - Input: a single `patch` string in unified diff format; multiple file
    sections and multi-hunk per file.
  - All-or-nothing per call: stage in memory, validate every hunk, then write;
    never leave a partial multi-file/multi-hunk apply on disk.
  - Implement parsing/application in Go (no shelling out to `git apply`/`patch`)
    for determinism; tolerate small line-number drift via a bounded fuzz window.
  - Support file creation (`--- /dev/null`) and deletion (`+++ /dev/null`).
  - Reuse the `.env` guard (`isEnvFile`/`envFileError`) and call
    `ctx.ReadState.Delete` for every modified/created/deleted path.
