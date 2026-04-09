---
id: worktree-session-lifecycle
status: draft
---
# Preserve worktrees on exit; auto-resume by branch

## Description
Worktrees created by `forge` in interactive mode are currently nuked on session
exit. This loses work when the user quits for unrelated reasons (Ctrl-C, crash,
lunch). The only safe path is `--branch`, which users don't always think to use.

This spec changes the default lifecycle: worktrees survive exit unless the
branch's PR was merged into the default branch. It also adds automatic resume
by detecting the branch → session mapping, so users don't need `--resume`.

## Context
- `cmd/forge/cli.go` — `spawnLocalAgent`, cleanup func, `runCLI` flag handling
- `cmd/forge/worktree_test.go` — worktree helper tests
- `internal/server/backend/worktree.go` — `WorktreeManager` (server-mode)
- `internal/server/backend/worktree_test.go` — server-mode tests
- `internal/runtime/session/store.go` — JSONL session persistence
- Session JSONL lives at `/tmp/forge/sessions/<sessionID>.jsonl`
- Worktrees live at `/tmp/forge/worktrees/<sessionID>/`
- Branch convention: `jelmer/<sessionID>` for ephemeral sessions
- Session ID format: `YYYYMMDD-<slug>` (e.g. `20260409-quick-flame`)

## Behavior

### B1: Worktrees survive exit by default
When a session ends (Ctrl-C, quit, crash), the worktree and branch are
preserved. The cleanup function no longer removes the worktree or deletes
the branch in the default (ephemeral) path.

### B2: Session metadata file
On worktree creation, write a `.forge-session` JSON file inside the worktree
root containing `{ "sessionID": "...", "branch": "...", "repoRoot": "..." }`.
This links the worktree back to its session for resume.

### B3: Auto-resume from branch detection
When `forge` starts without `--resume` or `--branch`, and the user is on
`main`/`master`, scan existing worktrees (`/tmp/forge/worktrees/`) for
`.forge-session` files belonging to this repo. If exactly one active worktree
is found, offer to resume it (or just resume it — no prompting). If multiple
are found, list them and ask the user to pick one or start fresh.

### B4: Resume via --branch
When `--branch jelmer/some-session` is passed and a worktree already exists
for that branch, reuse it AND reload the session JSONL (using the sessionID
from `.forge-session`). The agent gets the full conversation history.

### B5: Cleanup on merged PR
Before creating a new worktree for a session, check any existing worktrees
for this repo. For each one, check if its branch's PR was merged into the
default branch (using `gh pr view <branch> --json state --jq .state`). If
merged, clean up the worktree and branch. This is the ONLY automatic cleanup.

### B6: Manual cleanup command
Add a `/cleanup` slash command that lists all worktrees for the current repo
and lets the user delete specific ones or all stale ones. For now, just
print a message telling the user to run `git worktree remove <path>`.

### B7: Resume hint on exit
When the session exits and a worktree is preserved, print a message:
```
Worktree preserved: /tmp/forge/worktrees/20260409-quick-flame
Resume: forge --branch jelmer/20260409-quick-flame
```

## Constraints
- Do NOT delete worktrees on normal exit (the whole point)
- Do NOT prompt the user during automated flows (spec mode, server mode)
- Do NOT change the server-mode `WorktreeManager` — that has its own lifecycle
- Session JSONL location must not change
- `--skip-worktree` must still work as before (no worktree, no cleanup)
- The `.forge-session` file must be gitignored (add to `.gitignore` if not)

## Interfaces

### `.forge-session` file (in worktree root)
```json
{
  "sessionID": "20260409-quick-flame",
  "branch": "jelmer/20260409-quick-flame",
  "repoRoot": "/Users/jelmersnoeck/Projects/forge",
  "createdAt": "2026-04-09T14:30:00Z"
}
```

### Modified `spawnLocalAgent` signature
No change — but the cleanup func it returns will no longer remove the
worktree in the ephemeral path (only kills the agent process).

### `cleanupMergedWorktrees(repoRoot, worktreeBase string)`
New function. Scans worktreeBase for `.forge-session` files, checks PR
merge status, removes merged ones.

### `findResumableSession(repoRoot, worktreeBase string) ([]SessionInfo, error)`
New function. Returns sessions that can be resumed for this repo.

```go
type SessionInfo struct {
    SessionID    string
    Branch       string
    WorktreePath string
    CreatedAt    time.Time
}
```

## Edge Cases

### E1: Multiple worktrees for same repo
`findResumableSession` returns all of them. If >1 and no `--branch` flag,
print the list and start a fresh session (don't block on user input since
the TUI isn't started yet).

### E2: Worktree directory deleted but branch still exists
If `.forge-session` references a path that no longer exists, skip it.
The user can `git branch -D` the orphaned branch.

### E3: `gh` not installed
`cleanupMergedWorktrees` silently skips PR checks. Worktrees accumulate
until manual cleanup. This is fine — better than deleting work.

### E4: Session JSONL missing for a worktree
Resume works (creates new session in existing worktree) but conversation
history is lost. Print a warning.

### E5: Crashed session left .forge-session but agent is still running
Check if the port from the previous session is still active before spawning
a new agent. (Out of scope for this spec — just spawn a new agent.)

### E6: User is already in a worktree
Current behavior is preserved: no new worktree creation, no cleanup scanning.
