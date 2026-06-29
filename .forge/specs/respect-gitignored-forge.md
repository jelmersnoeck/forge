---
id: respect-gitignored-forge
status: implemented
---
# Honor gitignored .forge/ by suppressing git staging, not local writes

## Description
When a user gitignores `.forge/` (via `.gitignore` or `.forge/.gitignore` with `*`),
forge currently still tries to `git add` `.forge/learnings/*.md` during reflection.
Git exits 1 ("paths ignored") but stages the other valid pathspecs (`.gitattributes`,
`AGENTS.md`) atomically per-path first, leaving the user's index half-staged with
files they never asked for. Fix: introduce gitignore awareness in the reflect tool's
commit path so ignored files are filtered out before `git add`, and the commit/push
dance is skipped entirely when the learning file itself is ignored.

Policy (confirmed): **write/commit suppression only, NOT load suppression.** Gitignoring
`.forge` means "don't put this in the repo," not "don't use my local config." Local
files are still written and read; they're just never forced into git. The read side
(`internal/runtime/context/loader.go`) is intentionally left unchanged — suppressing
loads would break the intended `settings.local.json` workflow (gitignored = "don't
commit," not "don't use").

## Context
- `internal/tools/gitignore.go` — NEW: houses `IsGitIgnored` helper.
- `internal/tools/gitignore_test.go` — NEW: tests `IsGitIgnored` + the index-clean
  regression for issue #268.
- `internal/tools/reflect.go`:
  - `writeReflection` (~L78-113) — writes the learning file + `.gitattributes` +
    `AGENTS.md`, then calls `commitLearning`. Local writes stay unconditional.
  - `commitLearning` (~L125-173) — builds `filesToAdd := []string{rel, ".gitattributes"[, agentsRel]}`,
    runs `git add -- <those>`. This is the primary fix site.
  - `ensureGitattributes` (~L245-265) — appends `.forge/learnings/** linguist-generated=true`.
- `internal/tools/reflect_test.go` — existing tests; add gitignore cases here.
- `internal/runtime/context/loader.go` — read paths. NO CHANGE (documented decision).
- `internal/agent/worker.go` `commitPendingChanges` (~L842-852) — uses `git add -A`,
  already skips ignored paths. NO CHANGE (reference only).

## Behavior
- New helper `IsGitIgnored(cwd, relPath string) bool` in `internal/tools`:
  - Runs `git check-ignore -q -- <relPath>` with `Dir = cwd`.
  - Exit 0 → ignored → returns `true`.
  - Exit 1 → not ignored → returns `false`.
  - Exit 2 (or any other error, e.g. not a git repo) → returns `false` (treat as not ignored).
- `commitLearning`:
  - If the learning file (`rel`) is gitignored, skip the entire add/commit/push
    sequence and return. The file was already written locally by `writeReflection`;
    no git side effects, no log spam, no error.
  - Otherwise, filter `filesToAdd` to drop any path where `IsGitIgnored(cwd, path)`
    is true, **before** running `git add`. This prevents the half-staged
    `.gitattributes`/`AGENTS.md` problem.
  - If filtering leaves `filesToAdd` empty, skip add/commit/push.
- `writeReflection`: when `.forge/learnings` (the learning destination) is gitignored,
  skip `ensureGitattributes` — the `.forge/learnings/** linguist-generated=true` entry
  only documents a path that won't be tracked. Still write the learning file and still
  attempt `ensureAgentsMD` (AGENTS.md may live at repo root and not be ignored).
- Reproduction scenario (`.gitignore` contains `.forge/`) results in a **clean index**:
  `git status --short` shows no `A  .gitattributes` from forge.
- Read side: gitignored `.forge/settings.local.json`, rules, skills, agents, specs,
  and learnings are STILL loaded and applied. No `git check-ignore` on load.

## Constraints
- Must not call `git check-ignore` in any `internal/runtime/context/loader.go` load path.
- Must not block or error the reflection when `.forge` is ignored — local file write
  still succeeds and the tool returns success.
- Must not stage `.gitattributes` or `AGENTS.md` when the learning file is gitignored.
- Must not introduce a new dependency; shell out to `git` via `os/exec` like the
  rest of `commitLearning`.
- `IsGitIgnored` must not treat "not a git repo" (exit 2) as ignored — that would
  wrongly suppress commits in non-repo dirs.
- Must not change `commitPendingChanges` in `worker.go`.

## Interfaces
```go
// IsGitIgnored reports whether relPath is excluded by a .gitignore rule in the
// git repository rooted at (or above) cwd. Returns false when cwd is not a git
// repository or when git cannot determine ignore status.
func IsGitIgnored(cwd, relPath string) bool
```

## Edge Cases
- **Not a git repo**: `git check-ignore` exits 2 → `IsGitIgnored` returns false →
  `commitLearning` proceeds as today (the `git rev-parse --git-dir` guard then bails
  on the non-repo, unchanged behavior).
- **`.forge/` fully gitignored**: learning file is written locally, `ensureGitattributes`
  is skipped, `commitLearning` returns immediately — index stays clean, no commit.
- **`.gitattributes` gitignored but learning file tracked** (unusual): learning file
  is committed; `.gitattributes` is filtered out of `filesToAdd` so it isn't staged.
- **AGENTS.md at repo root, `.forge/` ignored**: learning file ignored → whole commit
  skipped, so AGENTS.md is not auto-committed by reflect (it's still written to disk
  and may be committed by the normal pipeline). Acceptable: reflect's commit is
  best-effort.
- **Empty `filesToAdd` after filtering**: skip add/commit/push, return silently.
- **git missing from PATH**: `exec.Command("git", ...)` Run() errors → `IsGitIgnored`
  returns false; `commitLearning`'s existing `git rev-parse` guard also fails and bails.
