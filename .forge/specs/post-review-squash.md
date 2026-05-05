---
id: post-review-squash
status: implemented
---
# Squash commits after SWE review loop completes

## Description
The SWE pipeline's coder→reviewer→coder loop produces many small commits
(initial implementation, review fix 1, review fix 2, …). Before the pipeline
hands off to PR creation, it should squash all branch commits into a single
clean commit. This happens deterministically in the orchestrator — no LLM
involvement — right after the review loop exits and before `EnsurePR` runs.

## Context
- `internal/agent/phase/orchestrator.go` — `runSWEPipeline()` controls the
  coder→reviewer cycle (lines 212–418). The squash step inserts between the
  review loop exit (line 416) and the return.
- `internal/agent/phase/pr.go` — `EnsurePR()` / `createNewPR()` run
  fetch+rebase+push. The squash must happen _before_ this so the rebased
  history is already clean.
- `internal/agent/worker.go` — `ensurePR()` is called from the `emit` closure
  on the `"done"` event (line 162). No changes needed here; the squash is
  upstream in the orchestrator.
- `internal/tools/squash.go` — `SquashBranchCommits()` and helpers. Uses
  `GitOutputCtx`/`GitOutputFullCtx` from `internal/tools/git.go`.
- `internal/tools/squash_test.go` — full test coverage (multi-commit, single
  commit, zero commits, dirty worktree, base branch detection).
- `internal/tools/git.go` — git helper functions (`GitOutputCtx`,
  `GitOutputFullCtx`, `RunGitCmd`).
- `internal/review/diff.go` — `detectBaseBranch()` resolves the merge-base
  ref. Same resolution order duplicated in `detectSquashBase` to avoid
  cross-package dependency.

## Behavior
1. After the review→fix loop completes in `runSWEPipeline`, the orchestrator
   calls a deterministic squash step.
2. The squash step:
   a. Detects the base branch using the same resolution as `detectBaseBranch`
      (origin/main → origin/master → main → master).
   b. Counts commits on the branch: `git rev-list --count <base>..HEAD`.
   c. If ≤ 1 commit exists, the step is a no-op (nothing to squash).
   d. Computes a merge-base: `git merge-base <base> HEAD`.
   e. Performs a soft reset to the merge-base: `git reset --soft <merge-base>`.
   f. Creates a single commit with a generated message: `git commit -m <msg>`.
3. The squash commit message is generated from the pre-squash commit log:
   - First line: the first commit's message (the original implementation commit).
   - If there were review-fix commits, append a blank line and a bulleted list
     of the other commit messages under a "Also includes:" header.
4. The orchestrator emits a `phase_event` with type `"squash"` containing the
   number of commits squashed (e.g., `"squashed 4 commits into 1"`).
5. If the squash fails at any point (reset fails, commit fails), the
   orchestrator logs the error, emits a warning event, and continues without
   squashing. The pipeline must not abort on squash failure — it's cosmetic.
6. The squash only runs when the pipeline was in SWE mode (full
   coder→reviewer→coder loop). Single-phase runs (`--mode code`) and Q&A/
   investigate intents do not squash.

## Constraints
- Must not use `git rebase -i` — that requires interactive input.
- Must not invoke the LLM to generate the squash message. Deterministic only.
- Must not squash if there are uncommitted changes (check `git status --porcelain`
  first; if dirty, skip with a warning).
- Must not modify commits that exist on the remote base branch — only squash
  commits unique to the feature branch.
- Must not lose any file changes. The working tree after squash must be
  identical to before (soft reset + re-commit preserves this).
- Must use context-aware git commands (`GitOutputCtx`/`GitOutputFullCtx`)
  to respect cancellation.

## Interfaces

```go
// SquashResult holds the outcome of the post-review squash step.
type SquashResult struct {
    Squashed     bool   // true if commits were squashed
    CommitCount  int    // number of commits before squash (0 if skipped)
    CommitSHA    string // new HEAD SHA after squash (empty if skipped)
    Error        error  // non-nil if squash failed (non-fatal)
}

// SquashBranchCommits squashes all commits on the current branch above
// the merge-base with the given base ref into a single commit.
// Returns SquashResult. Never fatal — errors are captured in the result.
func SquashBranchCommits(ctx context.Context, cwd string) SquashResult
```

In `orchestrator.go`, called at the end of `runSWEPipeline`:
```go
// After review loop, before return:
squashResult := SquashBranchCommits(ctx, opts.CWD)
if squashResult.Squashed {
    o.emitPhaseEvent(opts, "squash",
        fmt.Sprintf("squashed %d commits into 1", squashResult.CommitCount))
}
if squashResult.Error != nil {
    log.Printf("[orchestrator:%s] squash warning: %v", opts.SessionID, squashResult.Error)
}
```

## Edge Cases

1. **Single commit on branch** — `rev-list --count` returns 1. Squash is a
   no-op. No event emitted.

2. **No commits on branch** — `rev-list --count` returns 0. Squash is a
   no-op. (Shouldn't happen in normal flow, but defensive.)

3. **Uncommitted/staged changes** — `git status --porcelain` is non-empty.
   Squash skipped with warning. This protects against a coder phase that
   left work uncommitted.

4. **Merge-base detection fails** — base branch doesn't exist or
   `merge-base` command fails. Squash skipped with error in result.

5. **Soft reset succeeds but commit fails** — Dangerous state: HEAD is now
   at merge-base with staged changes. Recovery: attempt `git commit -m
   "squash recovery"`. If that also fails, log error loudly — the working
   tree still has the changes staged but history is broken. This is a
   "should never happen" path since commit after soft reset only fails on
   truly exotic errors (full disk, corrupt repo).

6. **Context cancelled mid-squash** — Git commands use `GitOutputCtx` which
   respects context cancellation. Partial squash may leave the repo in
   soft-reset state; the subsequent `EnsurePR` rebase step will likely
   fail, which is already handled as non-fatal.

7. **Branch is behind remote** — Squash only looks at local commits above
   merge-base. If the branch has been pushed already, the force-push in
   `EnsurePR` handles the rewrite.
