---
id: branch-flag
status: implemented
---
# Add --branch flag and auto-detect current branch for worktrees

## Description
Allow users to specify a branch name via `forge --branch <name>`. If an existing
worktree is already checked out on that branch, reuse it. Otherwise, create a new
worktree that checks out (or creates) the branch. This replaces the default
session-ID-based worktree/branch naming when specified.

Additionally, when no `--branch` flag is given and the user is already on a
non-default branch (not `main`, `master`, or detached `HEAD`), auto-detect it
and behave as if `--branch <current-branch>` was passed. This ensures forge
works on the user's current feature branch instead of creating an ephemeral one.

## Context
- `cmd/forge/cli.go` — `runCLI()` flag parsing, `spawnLocalAgent()` worktree creation, `isDefaultBranch()` helper
- `cmd/forge/main.go` — help text
- `cmd/forge/worktree_test.go` — worktree-related tests including `TestIsDefaultBranch`

## Behavior
- New flag: `--branch <name>` on the CLI (interactive mode only).
- **Auto-detection** (no `--branch` flag, not `--skip-worktree`, not in a worktree):
  1. Detect current branch via `git rev-parse --abbrev-ref HEAD`.
  2. If the branch is `main`, `master`, or `HEAD` (detached), fall through to
     default ephemeral worktree mode.
  3. Otherwise, treat it as if `--branch <detected-branch>` was passed.
- When `--branch` is set (explicitly or auto-detected):
  1. Run `git worktree list --porcelain` from the repo root.
  2. Parse output looking for a worktree whose `branch` matches `refs/heads/<name>`.
  3. If found: reuse that worktree path as CWD. Do NOT create a new worktree.
     Do NOT clean it up on exit (it's persistent).
  4. If not found: create a new worktree at the standard temp location, checking
     out the branch if it exists (`git worktree add <path> <branch>`) or creating
     it (`git worktree add -b <branch> <path> HEAD`). Do NOT clean it up on exit
     (user explicitly named it).
- `--branch` implies worktree mode; `--skip-worktree` and `--branch` together is
  an error.
- The session ID is still auto-generated (`cli-<timestamp>`), only the
  branch/worktree path changes.
- Cleanup behavior: when `--branch` is used (or auto-detected), the worktree and
  branch are NOT deleted on exit (the user intentionally named this branch).
- Help text updated.

## Constraints
- Do not modify `internal/server/backend/worktree.go` — that's server-mode only.
- Do not change the default (no-flag) worktree behavior when on `main`/`master`.
- Branch name is used as-is; do not prepend `jelmer/` automatically.
- Default branches are exactly: `main`, `master`, `HEAD`. No config, no heuristics.

## Interfaces
```go
// in spawnLocalAgent or a helper:
// findWorktreeForBranch returns the worktree path for a branch, or "" if none.
func findWorktreeForBranch(repoRoot, branch string) (string, error)

// isDefaultBranch returns true for branches that should trigger ephemeral
// worktree mode rather than branch-reuse mode (main, master, HEAD/detached).
func isDefaultBranch(branch string) bool
```

## Edge Cases
- `--branch` + `--skip-worktree`: error with message "cannot use --branch with --skip-worktree".
- `--branch` outside a git repo: error with message "not in a git repo".
- `--branch` when already in a worktree: works fine; searches from the main repo's
  worktree list.
- Branch exists as a remote tracking branch but no local branch: `git worktree add`
  handles this natively (auto-creates local tracking branch).
- Branch name contains `/` (e.g. `jelmer/feature`): handled naturally by git.
- On `main`/`master` with no `--branch`: default ephemeral worktree behavior, unchanged.
- On detached `HEAD` (no branch): `git rev-parse --abbrev-ref HEAD` returns `HEAD`,
  which is a default branch, so ephemeral mode kicks in.
- On a feature branch with no `--branch`: auto-detected, treated as `--branch <feature-branch>`.
