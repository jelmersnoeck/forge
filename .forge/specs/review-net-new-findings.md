---
id: review-net-new-findings
status: implemented
---
# Net-new review findings with severity-gated loop termination

## Description
The review→code→review loop gets stuck because reviewers see the full branch
diff every cycle, so they keep re-flagging pre-existing issues. Additionally,
all severity levels are treated as actionable, meaning warnings and suggestions
can keep the loop spinning forever. This spec addresses both: reviewers must only
flag issues in code that changed since the last review, and the loop terminates
early when only medium/low-severity findings remain after enough passes.

## Context
Files and systems that change:

- `internal/agent/phase/orchestrator.go` — `runSWEPipeline` review loop, `runReviewer`, `runReviewerWithDiff`, `formatFindingsForCoder`
- `internal/agent/phase/orchestrator_test.go` — tests for review loop termination, severity filtering, coder prompt formatting
- `internal/review/orchestrator.go` — `ReviewRequest` (added `Incremental` field), `buildUserMessage` (incremental instruction injection), `HasHighSeverityFindings`, `FilterHighSeverity`
- `internal/review/orchestrator_test.go` — tests for filtering, message building, incremental instruction
- `internal/review/diff.go` — `GetIncrementalDiff`, `GetHeadSHA`
- `internal/review/diff_test.go` — tests for incremental diff and HEAD SHA retrieval

## Behavior

### Incremental diff for review cycles
- On the **first** review cycle (cycle 0), reviewers receive the full branch diff (`base...HEAD`) as today.
- On **subsequent** review cycles (cycle 1+), reviewers receive only the diff since the previous review's commit snapshot. The orchestrator captures the HEAD SHA before each review and uses `<prev-sha>..HEAD` for the next cycle.
- If no new commits exist since the last review (the coder made no changes), the review loop exits immediately — nothing new to review.

### Reviewer prompt changes
- Each reviewer's system prompt gains an explicit instruction: "You are reviewing an **incremental diff** — only changes since the last review cycle. Flag only issues visible in this diff. Do not speculate about issues in code you cannot see."
- On the first review cycle, this instruction is omitted (full diff = full review).
- The instruction is injected by the orchestrator into the user message, not baked into the static system prompts, so the system prompts remain the same for `/review` (manual, one-shot review).

### Severity-gated loop termination
- Only `critical` and `warning` findings are treated as actionable for the review loop. Findings with `suggestion` severity (or unknown severity) do not trigger another fix cycle.
- `HasActionableFindings` is replaced by a new `HasHighSeverityFindings(results []ReviewResult) bool` that returns true only when at least one finding has severity `critical` or `warning`.
- After **5** consecutive review cycles with no `critical` or `warning` findings (only suggestions/unknown remain), the loop terminates regardless of remaining findings.
- The existing critical-reset behavior (reset counter when criticals remain at cycle limit) continues to apply, but now the cycle count threshold is reduced from 10 to 5 since the loop should converge faster with incremental diffs.

### What gets sent to the coder
- `formatFindingsForCoder` filters to only `critical` and `warning` findings. Suggestions are excluded from the fix prompt to avoid scope creep.
- The coder prompt explicitly states: "Fix ONLY the issues listed below. Do not refactor unrelated code or address issues not in this list."

## Constraints
- Must not change behavior of `/review` (manual, one-shot review from TUI/API) — that always uses the full branch diff and shows all severities.
- Must not change the `Finding` or `ReviewResult` types.
- Must not change reviewer system prompts — the incremental instruction is injected at call site.
- `maxReviewCycles` must be reduced from 10 to 5.
- The incremental diff must use `git diff <sha>..HEAD` (two-dot, not three-dot) to get exactly what changed between the two points.
- Must not store persistent state between review cycles — the previous SHA is held in a local variable inside `runSWEPipeline`.

## Interfaces

```go
// internal/review/orchestrator.go

// HasHighSeverityFindings returns true if any result contains a critical or warning finding.
func HasHighSeverityFindings(results []ReviewResult) bool

// FilterHighSeverity returns only critical and warning findings from results.
func FilterHighSeverity(results []ReviewResult) []ReviewResult
```

```go
// internal/review/diff.go

// GetIncrementalDiff returns the diff between a previous SHA and HEAD.
// Used for review cycles after the first.
func GetIncrementalDiff(cwd, prevSHA string) (string, error)

// GetHeadSHA returns the current HEAD commit SHA.
func GetHeadSHA(cwd string) (string, error)
```

```go
// internal/agent/phase/orchestrator.go

const maxReviewCycles = 5

// In runSWEPipeline, the review loop tracks:
//   prevReviewSHA string  — captured before each review cycle
//   cycle         int     — loop counter (reset on criticals, as before)
```

## Edge Cases
- Coder makes no new commits between review cycles → `GetIncrementalDiff` returns empty diff → loop exits with "no new changes to review" event.
- Coder amends a commit instead of creating a new one → SHA changes, `GetIncrementalDiff` still works because it diffs against the old SHA, which Git still has in its object store (within a single session, GC won't run).
- All findings are suggestions → `HasHighSeverityFindings` returns false on first cycle → loop exits after first review (no fix cycle triggered).
- Reviewer returns findings with unrecognized severity (e.g., "info", "medium") → treated as non-actionable for loop purposes (only `critical` and `warning` match).
- First review cycle finds only warnings → coder fixes → second review finds only suggestions → loop exits (no high-severity findings).
- `git diff` fails (e.g., prev SHA was garbage-collected in a long-running session) → treat as error, break the review loop, log the error.
- Manual `/review` command → uses `GetDiff` (full branch diff) as today, unaffected by this change.
