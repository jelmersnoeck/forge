---
id: worktree-session-lifecycle
status: implemented
---
# Preserve worktrees on exit; auto-resume by branch

## Description
Worktrees created by `forge` in interactive mode are currently nuked on session
exit. This loses work when the user quits for unrelated reasons (Ctrl-C, crash,
lunch). The only safe path is `--branch`, which users don't always think to use.

This spec changes the default lifecycle: worktrees survive exit unless the
branch's PR was merged into the default branch. It also adds automatic resume
by detecting the branch → session mapping, so users don't need `--resume`.

Issue #211 extends this: in addition to preserving the worktree and JSONL, the
agent now persists a small `.forge-state` routing-metadata file so a restart
resumes the correct orchestrator phase and conversation history IDs instead of
starting cold. Conversation turns themselves continue to replay from JSONL via
`loop.Resume()`.

## Context
- `cmd/forge/cli.go` — `spawnLocalAgent`, cleanup func, `runCLI` flag handling
- `cmd/forge/session.go` — `spawnLocalAgent`, `--branch` reuse path, stale-state warning call
- `cmd/forge/worktree_session.go` — `SessionInfo`, read/write `.forge-session`, `warnIfStateStale` (issue #211)
- `cmd/forge/worktree_session_test.go` — tests for session metadata and resumable session scanning
- `cmd/forge/worktree_test.go` — worktree helper tests
- `internal/sessionstate/` — `.forge-state` State type + atomic Write/Read (issue #211)
- `internal/agent/worker.go` — `initialState`/`persistState`, phase↔name mapping, persist every turn (issue #211)
- `.gitignore` — added `.forge-session` and `.forge-state` entries
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

### B3: Fresh worktree on every default launch
When `forge` starts without `--resume` or `--branch`, and the user is on
`main`/`master`, always create a new worktree with a unique session ID.
No session scanning or auto-resume occurs in the default path — this
prevents multiple forge invocations from the same branch silently reusing
the same worktree. Use `--branch` to explicitly resume an existing session.

### B4: Resume via --branch
When `--branch jelmer/some-session` is passed and a worktree already exists
for that branch, reuse it AND reload the session JSONL (using the sessionID
from `.forge-session`). The agent gets the full conversation history.

### B5: Cleanup on merged PR (deferred)
Not implemented in this iteration. Merged-PR cleanup is a server-level
concern and should not block session startup with synchronous network calls.
Future work: a background server process or async CLI hook can handle this.

### B6: Manual cleanup command (deferred)
Not implemented in this iteration. Users can manually run
`git worktree remove <path>` and `git branch -D <branch>` to clean up.

### B7: Resume hint on exit
When the session exits and a worktree is preserved, print a message:
```
Worktree preserved: /tmp/forge/worktrees/20260409-quick-flame
Resume: forge --branch jelmer/20260409-quick-flame
```

### B8: Persist orchestrator routing state across restarts (issue #211)
On every turn, the worker writes a `.forge-state` JSON file to the worktree
root holding the orchestrator routing metadata: which phase it was in, the
coder/QA/investigate history IDs, whether the orchestrator ran, and the
worktree HEAD at write time. The conversation history itself is NOT stored
here — it stays in the session JSONL and is replayed via `loop.Resume()`.

### B9: Restore routing state on resume
On worker startup (every agent launch), the worker reads `.forge-state` from
its cwd (the worktree root) and initializes its `WorkerState` from it. A
missing file means a fresh session. A corrupt or version-mismatched file is
logged and treated as fresh — never fatal. Because resume only happens via
`--branch` (B4) for an existing worktree, this effectively restores phase +
history IDs only on explicit resume.

### B10: Stale-state warning
On `--branch` resume the CLI compares the `headCommit` stored in `.forge-state`
against the worktree's current `git rev-parse --short HEAD`. If they diverge,
it prints a warning that the resumed context may be stale. Missing state,
unreadable state, or a non-git worktree is silent.

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

```go
type SessionInfo struct {
    SessionID    string
    Branch       string
    WorktreePath string
    CreatedAt    time.Time
}
```

### `.forge-state` file (in worktree root) — issue #211
Routing metadata only (~200 bytes). Lives in `internal/sessionstate`.
```json
{
  "version": 1,
  "sessionID": "20260409-quick-flame",
  "phase": "orchestrator",
  "coderHistoryID": "uuid-of-coder-conversation",
  "qaHistoryID": "uuid-of-qa-conversation",
  "investigateID": "uuid-of-investigate-conversation",
  "orchestratorDone": true,
  "headCommit": "abc1234",
  "updatedAt": "2026-04-09T15:30:00Z"
}
```

```go
package sessionstate

const StateFile = ".forge-state"
const Version = 1

type State struct {
    Version          int
    SessionID        string
    Phase            string // "idle"|"qa"|"investigate"|"orchestrator"|"done"
    HistoryID        string // coderHistoryID
    QAHistoryID      string
    InvestigateID    string
    OrchestratorDone bool
    HeadCommit       string
    UpdatedAt        time.Time
}

func Write(worktreeRoot string, s State) error // atomic (tmp + rename)
func Read(worktreeRoot string) (State, error)  // os.ErrNotExist when absent
```

Worker (`internal/agent/worker.go`):
```go
func (w *Worker) initialState() WorkerState // reads .forge-state on startup
func (w *Worker) persistState(state WorkerState) // writes every turn
func phaseName(WorkerPhase) string
func phaseFromName(string) WorkerPhase
```

CLI (`cmd/forge/worktree_session.go`):
```go
func warnIfStateStale(worktreePath string) // B10 HEAD divergence warning
```

## Edge Cases

### E1: Multiple worktrees for same repo
Each `forge` invocation from a default branch creates a new worktree with a
unique session ID. No conflict. Use `--branch` to resume a specific one.

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

### E7: .forge-state absent on resume (issue #211)
First resume of a worktree created before this feature, or any worktree whose
state file was deleted. `initialState()` returns a zero `WorkerState` — the
agent starts fresh but still replays JSONL history if `state.HistoryID` is
later set. No error, no warning beyond B10 silence.

### E8: .forge-state corrupt or wrong version
`Read` returns an error; `initialState()` logs and returns a zero state.
`warnIfStateStale` returns silently. The session proceeds as fresh — never
fatal.

### E9: Interrupted/errored turn (issue #211)
`persistState` runs after `turnCancel()` regardless of `runErr`, so even a
crashed or interrupted turn leaves a `.forge-state` pointing at the latest
history IDs captured before the failure.

### E10: HEAD diverged from saved state (issue #211)
User (or another agent) committed/rebased in the worktree between sessions.
`warnIfStateStale` prints a warning on `--branch` resume but does NOT block —
the conversation history still replays; the user is just told it may be stale.

### Constraints (issue #211 additions)
- `.forge-state` must be gitignored (added alongside `.forge-session`).
- Routing state must NOT duplicate conversation history — JSONL stays the
  single source of truth for turns; `.forge-state` holds only routing metadata.
- A missing/corrupt/version-mismatched `.forge-state` must never be fatal.
- Writes must be atomic (tmp file + rename) to avoid torn reads on crash.
