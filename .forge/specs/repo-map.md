---
id: repo-map
status: implemented
---
# Optional repo map: token-budgeted symbol index injected into context

## Description
Forge has no structural overview of the codebase — the agent blind-greps to
discover files, symbols, and dependencies. Add an optional repo-map builder that
walks the tracked source tree, extracts top-level declarations per file, ranks
them by cross-file reference importance (PageRank-style, like aider), and emits a
deterministic, token-budgeted summary injected as a dedicated context-bundle
section. Gated behind config, off by default, hard token cap. Determinism (fully
sorted output) preserves prompt-cache warmth per the cache learnings.

## Context
- `internal/runtime/repomap/` (new package): tree walk, symbol extraction, ranking, rendering.
  - `repomap.go` — `Builder`, `Build`, `Map`, `FileEntry`, `Symbol`, `Render`.
  - `gosymbols.go` — Go extractor via stdlib `go/parser` + `go/ast`.
  - `generic.go` — regex-based extractor for non-Go languages.
  - `rank.go` — PageRank over the symbol-reference graph.
- `internal/config/config.go` — extend `ForgeConfig` with a `RepoMap` block (gate + budget + maxFiles).
- `internal/types/types.go` — add `RepoMap string` field to `ContextBundle`.
- `internal/runtime/context/loader.go` — `loadProjectContext` builds the map (when enabled) and stores rendered text on the bundle.
- `internal/runtime/prompt/prompt.go` — `Assemble` renders the repo-map section into the dynamic block (before the current-date line).

## Behavior
- Disabled by default. `ContextBundle.RepoMap` stays empty and no prompt section
  is emitted unless `RepoMap.Enabled` is true in `.forge/config.json` (or user config).
- When enabled, `loadProjectContext` calls `repomap.Build(cwd, opts)` and assigns
  the rendered string to `bundle.RepoMap`.
- File discovery uses `git ls-files` in `cwd` to enumerate tracked files — this
  respects `.gitignore` for free and is deterministic. If `cwd` is not a git repo
  or `git` is unavailable, the map is skipped (empty, no error).
- Only files with recognized extensions are parsed. Go files (`.go`) use
  `go/parser` to extract package-level: funcs, methods (with receiver), types,
  consts, vars. Other languages (`.py`, `.js`, `.ts`, `.tsx`, `.jsx`, `.rb`,
  `.rs`, `.java`, `.go`) use a per-language regex extractor for top-level
  declaration names. Unrecognized extensions are counted as files but contribute
  no symbols.
- Symbols are ranked via PageRank over a cross-file reference graph. To honor
  the no-body-read constraint, the reference surface is each file's *declared
  symbol names* (identifiers within declaration names), not full file bodies:
  file A → file B when A's declarations reference a symbol name defined in B.
  Power iteration (damping 0.85, 20 iters, uniform dangling redistribution)
  produces per-file scores; a tiny symbol-count bonus breaks ties. Files are
  ordered by score desc; within a file, symbols sort by name asc. Ties across
  files break by path ascending for determinism.
- Rendering is token-budgeted. Default budget 2000 tokens (approximated as
  `len(text)/4`). Emit highest-ranked entries first, grouped by file (file path
  header, then its included symbols), until the next entry would exceed the
  budget. A trailing line reports how many files/symbols were omitted:
  `... (N more files, M more symbols omitted)`.
- Output is fully deterministic for a fixed tree: files sorted by path, symbols
  within a file sorted by (rank desc, name asc), stable across runs. No timestamps.
- `prompt.Assemble` wraps the map in a `<system-reminder>` block titled
  `Repository map (structural overview):` placed in the dynamic block, appended
  after the spec index and before the `Current date:` line, so it never busts the
  large global static block and keeps the dynamic prefix stable within a run.
- `config.ForgeConfig.RepoMap` fields: `Enabled bool`, `TokenBudget int`
  (default 2000 when zero/unset and enabled), `MaxFiles int` (default 0 = no cap).
  Project config overrides user config, matching existing merge semantics.

## Constraints
- Must not add third-party dependencies (no tree-sitter, no gitignore lib, no
  go-git). Use stdlib `go/parser`/`go/ast`, `os/exec` for `git ls-files`, and
  regex for non-Go languages. If a tree-sitter/ctags approach is later desired,
  it requires explicit user approval first.
- Must not emit the repo-map section when disabled or when the map is empty.
- Must not read file bodies into the prompt — only declaration names/signatures.
- Must not exceed `TokenBudget` (approx `len/4`) in rendered output.
- Must not introduce non-determinism: no map iteration without sorting, no
  timestamps, no mtime, no absolute paths (emit paths relative to `cwd`).
- Must not place the repo-map section in the global static system block.
- Must not fail context loading if the tree walk, git, or a parser errors —
  degrade to an empty map and continue (no silent swallow of the underlying
  error beyond skipping the optional feature; log at most a debug line).
- Building the map must not block on huge repos unbounded: respect `MaxFiles`
  and skip files larger than 1 MiB.

## Interfaces
```go
// internal/config/config.go
type ForgeConfig struct {
    SpecsDir string       `json:"specsDir,omitempty"`
    RepoMap  RepoMapConfig `json:"repoMap,omitempty"`
}

type RepoMapConfig struct {
    Enabled     bool `json:"enabled,omitempty"`
    TokenBudget int  `json:"tokenBudget,omitempty"` // default 2000 when enabled
    MaxFiles    int  `json:"maxFiles,omitempty"`     // 0 = unlimited
}

// internal/runtime/repomap/repomap.go
type Options struct {
    TokenBudget int // <=0 → default 2000
    MaxFiles    int // 0 → unlimited
}

type Symbol struct {
    Name string
    Kind string // "func", "method", "type", "const", "var"
    Line int
}

type FileEntry struct {
    Path    string   // relative to cwd
    Symbols []Symbol
}

type Map struct {
    Files          []FileEntry
    OmittedFiles   int
    OmittedSymbols int
}

// Build walks the git-tracked tree under cwd, extracts and ranks symbols,
// and returns a token-budgeted Map. Returns an empty Map (not an error) when
// cwd is not a git repo or git is unavailable.
func Build(cwd string, opts Options) (Map, error)

// Render turns a Map into deterministic prompt text (empty string for empty Map).
func Render(m Map) string

// internal/types/types.go
type ContextBundle struct {
    // ... existing fields ...
    RepoMap string // rendered repo map, empty when disabled
}
```

## Edge Cases
- Not a git repo / `git` missing: `Build` returns empty `Map`, `bundle.RepoMap`
  stays empty, no prompt section, no error.
- Empty repo (no tracked files): empty map, no section emitted.
- File with no top-level symbols (e.g. a data-only JSON or a `.txt`): counted
  toward file total but contributes zero symbols; not rendered as a header.
- Unparseable Go file (syntax error): skip that file's symbols, continue; do not
  abort the whole build.
- Budget smaller than the first file's header: the highest-ranked file is always
  emitted (the budget guard only trips once at least one file is included, so the
  map is never empty when parseable files exist); remaining files/symbols are
  reported in the omitted-count trailer. This "keep at least one file" behavior
  supersedes the earlier "emit nothing" draft intent — an empty-but-nonzero map
  is useless, and `TestBuild_BudgetOmits` asserts `len(files) >= 1`.
- Very large file (>1 MiB): skipped from parsing, counted as omitted.
- Config enabled but `TokenBudget` unset/zero: use default 2000.
- Two symbols with identical rank and name in different files: ordered by file
  path ascending — stable across runs to keep cache warm.
- Repo changes between sessions: map changes and busts only the dynamic block,
  never the global static block (verified by placement in `Assemble`).
- Non-Go extractors match top-level declarations only (column-0 / minimally
  indented). Nested methods (e.g. a Python method inside a class) are not
  captured — this is intentional to keep the map to a structural overview.
- Callers must pass an absolute `cwd`. `git -C <relativePath>` from a differing
  process cwd can fail (exit 128); the agent worker passes the worktree root as
  an absolute path, so production is unaffected.

## Implementation Notes
- Files: `internal/runtime/repomap/{repomap,render,gosymbols,generic,rank}.go`
  plus `repomap_test.go`. Wired via `loader.go` (build when enabled) and
  `prompt.go` (render section). Config in `config.go`, bundle field in `types.go`.
- Ranking heuristic tends to surface heavily-referenced files (including test
  files with many shared identifier names) first — acceptable for a v1
  structural overview; tune the reference surface later if needed.
- Reference surface as implemented (`rank.go`): each file's mention surface is its
  own *declared symbol names* tokenized into identifiers, matched against the
  symbol names defined in other files. No file bodies are read for ranking —
  this honors the no-body-read constraint but means references only via call
  sites (not via a declaration name) are not counted. Method names are matched on
  their base identifier (the part after the last `.`).
- Rendering format: `renderFile` emits `<relpath>\n` then `  <kind> <name>\n` per
  symbol (two-space indent). The omitted trailer is
  `... (N more files, M more symbols omitted)`.
- Config merge (`config.go`): `RepoMap.Enabled` ORs true across levels;
  `TokenBudget`/`MaxFiles` overwrite only when non-zero — matching existing
  project-over-user semantics.
