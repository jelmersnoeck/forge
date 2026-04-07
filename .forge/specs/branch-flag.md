---
id: branch-flag
status: implemented
---
# Add --branch flag to reuse or create worktrees by branch name

## Description
Allow users to specify a branch name via `forge --branch <name>`. If an existing
worktree is already checked out on that branch, reuse it. Otherwise, create a new
worktree that checks out (or creates) the branch. This replaces the default
session-ID-based worktree/branch naming when specified.

## Context
- `cmd/forge/cli.go` — `runCLI()` flag parsing, `spawnLocalAgent()` worktree creation
- `cmd/forge/main.go` — help text
- `cmd/forge/worktree_test.go` — worktree-related tests

## Behavior
- New flag: `--branch <name>` on the CLI (interactive mode only).
- When `--branch` is set:
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
- Cleanup behavior: when `--branch` is used, the worktree and branch are
  NOT deleted on exit (the user intentionally named this branch).
- Help text updated.

## Constraints
- Do not modify `internal/server/backend/worktree.go` — that's server-mode only.
- Do not change the default (no-flag) worktree behavior.
- Branch name is used as-is; do not prepend `jelmer/` automatically.

## Interfaces
```go
// in spawnLocalAgent or a helper:
// findWorktreeForBranch returns the worktree path for a branch, or "" if none.
func findWorktreeForBranch(repoRoot, branch string) (string, error)
```

## Edge Cases
- `--branch` + `--skip-worktree`: error with message "cannot use --branch with --skip-worktree".
- `--branch` outside a git repo: error with message "not in a git repo".
- `--branch` when already in a worktree: works fine; searches from the main repo's
  worktree list.
- Branch exists as a remote tracking branch but no local branch: `git worktree add`
  handles this natively (auto-creates local tracking branch).
- Branch name contains `/` (e.g. `jelmer/feature`): handled naturally by git.
