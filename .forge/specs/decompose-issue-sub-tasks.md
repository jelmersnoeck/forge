---
id: decompose-issue-sub-tasks
status: implemented
---
# Decompose a large issue into ordered GitHub sub-issues + sequential multi-phase runner

## Description
Phase 1 (#221): Add a lightweight LLM call that decomposes a large parent issue
body into ordered, independently-shippable sub-tasks, creates each as a GitHub
issue, and attaches it to the parent as a sub-issue. Also add a fetcher that
reads back the parent's sub-issues in position order.

Phase 2 (#222): Enable sub-agents to run in their own worktree (a `CWD` override
on `SubAgent`, honored by the worker's `makeAgentRunner`) and add
`phase.RunMultiPhase`, a sequential coordinator that, for each sub-issue, creates
a worktree + branch, spawns a full-access sub-agent in that worktree, ensures a
PR, and cleans up before moving to the next. A phase failure halts the pipeline;
PR and cleanup failures are non-fatal. Merge coordination between phases is #223
(out of scope here). This is part of the multi-phase orchestrator (issue #220).

Phase 3 (#223): Add `phase.WaitForMerge`, a merge poller that polls
`gh pr view <n> --json state,mergeable,mergeStateStatus` at a configurable
interval (default 30s) until the PR is MERGED (return nil), CLOSED (halt), or a
timeout (default 2h) elapses (halt). On a CONFLICTING/DIRTY open PR it rebases
onto origin/base and force-pushes. `RunMultiPhase` gains an opt-in merge-wait
step (`WaitForMergeEnabled`) inserted between PR ensure and worktree removal,
followed by a base fast-forward (`pullMain`) so the next phase branches off the
updated base. A closed PR or merge timeout halts the pipeline.

## Context
- `internal/agent/phase/decompose.go` — new: decompose LLM call + sub-issue creation
- `internal/agent/phase/decompose_test.go` — new: decompose + parse tests
- `internal/agent/phase/classify.go` — pattern to mirror (lightweight model loop,
  `stripCodeFences`, JSON parse, `truncateAtWordBoundary`, per-attempt timeout)
- `cmd/forge/issue.go` — add `fetchSubIssues`; reuse `ghIssue`, `normalizeIssueRef`
- `cmd/forge/issue_test.go` — add sub-issue parse tests
- `internal/types/types.go` — `LightweightModels`, `LLMProvider`, `ChatRequest`/`ChatDelta`
- (Phase 2) `internal/types/task.go` — add `CWD` field to `SubAgent`
- (Phase 2) `internal/agent/worker.go` — `subAgentCWD` helper + CWD override in
  `makeAgentRunner` (`loop.Options.CWD` uses the override when set)
- (Phase 2) `internal/agent/subagent_cwd_test.go` — new: `subAgentCWD` table test
- (Phase 2) `internal/agent/phase/multi.go` — new: `RunMultiPhase` coordinator,
  worktree helpers, branch/slug helpers, sub-issue prompt formatting
- (Phase 2) `internal/agent/phase/multi_test.go` — new: coordinator + git worktree tests
- (Phase 2) `internal/agent/phase/pr.go` — `EnsurePR` reused per phase (unchanged)
- (Phase 3) `internal/agent/phase/merge_poller.go` — new: `WaitForMerge` poller,
  `ghPRStatus`, `gitRebaseAndForcePush`, `pullMain`, `extractPRNumberFromURL`
- (Phase 3) `internal/agent/phase/merge_poller_test.go` — new: poller + rebase tests
- (Phase 3) `internal/agent/phase/multi.go` — merge-wait + pull-base step wired
  into `RunMultiPhase` behind `WaitForMergeEnabled`

## Behavior
- `Decompose(ctx, provider, issueBody) ([]SubTask, error)` runs a lightweight LLM
  call over `types.LightweightModels`, trying each model in order, falling through
  on error (same loop structure as `Classify`).
- System prompt instructs the LLM to: break the issue into independently-shippable
  phases; order by dependency (foundational changes first); output ONLY a JSON
  array `[{"title":"...","body":"...","depends_on":[<int>...]}]`.
- Response is run through `stripCodeFences` then `json.Unmarshal` into `[]SubTask`.
- A `SubTask` with an empty `title` after trimming is invalid; `Decompose` returns
  an error naming the offending index. `depends_on` entries are zero-based indices
  into the returned slice; out-of-range indices are dropped (logged), not fatal.
- The issue body sent to the LLM is truncated at a word boundary to a fixed cap
  (`maxDecomposePromptLen`) to bound input tokens.
- `CreateSubIssues(ctx, parent, repo, tasks []SubTask) ([]int, error)` creates one
  GitHub issue per task via `gh issue create --title --body --repo <repo>` (the
  `--repo` flag is omitted when `repo` is empty, letting `gh` resolve the cwd
  repo), parses
  the new issue number from the returned URL, then attaches it to the parent via
  `gh api repos/<repo>/issues/<parent>/sub_issues -X POST -F sub_issue_id=<id>`.
  Returns created sub-issue numbers in task order.
- If creating or attaching any sub-issue fails, `CreateSubIssues` returns the
  numbers created so far plus the error (no rollback; partial creation surfaced).
- `fetchSubIssues(ref, cwd) ([]ghIssue, error)` in `cmd/forge` calls
  `gh api repos/{owner}/{repo}/issues/{number}/sub_issues` for the normalized ref,
  unmarshals into `[]ghIssue` via `parseSubIssues`, and returns them in the order
  the API returns (position order). Empty/no sub-issues returns `(nil, nil)`.
- A full-URL ref is reduced to its bare issue number (via `extractIssueNumber`)
  before substitution, since `gh api` only resolves `{owner}/{repo}` placeholders
  against the cwd repo and won't accept a URL as the issue segment.
- `fetchSubIssues` requires `gh` on PATH; absence returns the same guidance error
  string used by `fetchGitHubIssue`.

### Phase 2 (#222) — Sub-agent CWD override + sequential multi-phase runner
- `SubAgent` gains a `CWD string` field (`json:"cwd,omitempty"`). When non-empty
  it overrides the working directory the sub-agent's conversation loop runs in.
- `subAgentCWD(parentCWD, agent)` returns `agent.CWD` when set, else `parentCWD`.
  `makeAgentRunner` sets `loop.Options.CWD` from this helper (was hardcoded `w.cwd`).
- `RunMultiPhase(ctx, opts)` runs `opts.Phases` (a `[]SubIssue`) sequentially in
  slice order. For each phase i:
  1. Build branch `jelmer/<ParentSlug>-<Number>` (blank slug → `jelmer/phase-<n>`).
  2. Build worktree dir `<WorktreeBase>/<branch with / → ->`.
  3. `CreateWorktree(ctx, RepoRoot, BaseBranch, branch, path)` → worktree path.
  4. `SpawnAgent(ctx, cwd, branch, prompt)` where prompt is the formatted
     sub-issue body; blocks until the sub-agent completes.
  5. `EnsurePRFn(ctx, cwd)` (defaults to `EnsurePR(ctx, Provider, cwd, "", PRAttr)`).
  6. `RemoveWorktree(ctx, RepoRoot, cwd)` to clean up.
- A nil `SpawnAgent` is a programmer error: `RunMultiPhase` returns an error
  immediately. Empty `Phases` returns nil (no-op).
- `CreateWorktree`/`RemoveWorktree`/`EnsurePRFn` default to real implementations
  (`gitCreateWorktree`, `gitRemoveWorktree`, an `EnsurePR` wrapper) when nil, so
  the worker wires nothing extra and tests inject fakes.
- A phase's worktree-creation error OR sub-agent error halts the pipeline: later
  phases are not started, and the error names the phase index + sub-issue title.
- A PR error (`PRResult.Error != nil`) is logged at warn and emitted as text, but
  does NOT halt the pipeline. A successful PR URL is emitted as text.
- A worktree-cleanup error is logged at warn and does NOT halt the pipeline.
- On sub-agent failure the worktree is left in place (not removed) for inspection.
- `ctx` cancellation is checked at the top of each iteration; a cancelled context
  returns an error before the next phase's worktree is created.
- `gitCreateWorktree` runs `git worktree add -b <branch> <path> <base>`; if that
  fails (branch exists) it retries `git worktree add <path> <branch>`.
- `gitRemoveWorktree` runs `git worktree remove --force <path>`.
- `formatSubIssuePrompt` renders `Implement the following GitHub issue.` + an
  `Issue #<n>: <title>` (or `Issue: <title>` when number is 0) header + trimmed body.
- `SlugifyTitle(title, maxLen)` lowercases, collapses non-alphanumeric runs to
  single hyphens, trims hyphens, and truncates to `maxLen` (0 = no truncation).

### Phase 3 (#223) — Merge waiting + branch management
- `WaitForMerge(ctx, MergeWaitOpts)` polls a PR until it merges or halts:
  - Polls immediately, then every `PollInterval` (default 30s) via the injectable
    `PRStatus` func (real impl: `gh pr view <n> --json state,mergeable,mergeStateStatus`).
  - `MERGED` → returns nil. `CLOSED` → returns an error ("closed without merging").
  - `OPEN` with `Mergeable == "CONFLICTING"` OR `MergeStateStatus == "DIRTY"` →
    calls the injectable `Rebase` func (real impl: fetch + rebase onto
    origin/base + `push --force-with-lease`); a rebase error is logged, NOT
    fatal — polling continues so a human can resolve.
  - A `PRStatus` query error is logged and retried on the next tick (transient
    gh/network errors do not halt).
  - After `Timeout` (default 2h) with no merge → returns a "not merged within"
    error. A cancelled parent ctx → returns a "cancelled" error (distinct text).
  - `PRNumber <= 0` → returns an "invalid PR number" error immediately.
- `RunMultiPhase` gains `WaitForMergeEnabled` (default false → legacy behavior,
  no merge coordination). When true, after the PR-ensure step and before
  worktree removal, for each phase:
  1. Extract the PR number from `PRResult.URL` via `extractPRNumberFromURL`.
     If `<= 0` (PR skipped/failed), skip the merge wait (non-fatal) — the PR
     failure was already surfaced.
  2. Call `WaitForMergeFn(ctx, prNumber, cwd, BaseBranch)`. An error (closed PR
     or timeout) halts the pipeline; the error names "phase i" + title +
     "merge wait"; the worktree is left in place.
  3. On merge, call `PullMainFn(ctx, RepoRoot, BaseBranch)` to fast-forward the
     local base ref; a pull error is logged, NOT fatal (CreateWorktree still
     branches off origin/base).
- `MergePollInterval` / `MergeTimeout` on `MultiPhaseOpts` configure the default
  `WaitForMergeFn`; zero values fall back to the package defaults.
- `WaitForMergeFn` / `PullMainFn` default to wrappers around `WaitForMerge` /
  `pullMain` when nil, so the worker wires nothing extra and tests inject fakes.

## Constraints
- Do not add orchestration, worktree creation, CWD overrides, or multi-phase
  routing — those are later phases of #220. Only the three primitives ship here.
- Do not add a YAML/JSON plan manifest. GitHub sub-issues are the only state.
- Decompose must not fail the whole call on a single bad `depends_on` index —
  drop the index and continue. It MUST fail on a missing/empty `title`.
- `Decompose` returns `(nil, err)` for an empty/whitespace-only issue body
  (no LLM call made) — caller decides fallback.
- Must reuse existing `ghIssue` type; do not introduce a parallel struct.
- No mocks for `gh`; tests that touch `gh` must parse fixture JSON, not invoke it.
  LLM tests use the existing `mockProvider` from the phase package test files.

### Phase 2 constraints (#222)
- `RunMultiPhase` must NOT implement cross-phase merging or rebasing — that is
  #223. Each phase branches from `BaseBranch` independently.
- `RunMultiPhase` must NOT depend on `internal/agent` or `internal/runtime/task`
  (import cycle). Sub-agent spawning is injected via the `SpawnPhaseAgent` func.
- A PR or worktree-cleanup failure must NOT halt the pipeline. Only a
  worktree-creation or sub-agent failure halts it.
- The CWD override must default to the parent worker's cwd when `SubAgent.CWD`
  is empty — existing single-worktree sub-agent behavior is unchanged.

### Phase 3 constraints (#223)
- A `PRStatus` query error or a `Rebase` error must NOT halt `WaitForMerge` —
  log and keep polling. Only `CLOSED`, timeout, cancellation, or an invalid PR
  number halt it.
- `WaitForMergeEnabled` defaults to false; when false `RunMultiPhase` must be
  byte-for-byte the legacy behavior (no merge wait, no base pull).
- A skipped/failed PR (no number in `PRResult.URL`) must NOT halt the pipeline
  and must NOT call `WaitForMergeFn`.
- A `PullMainFn` error must NOT halt the pipeline (logged only).
- `WaitForMerge` must use injectable `PRStatus`/`Rebase` funcs so tests never
  invoke real `gh` or git; the `merge_poller_test.go` poller tests pass closures.

## Interfaces
```go
// internal/agent/phase/decompose.go
type SubTask struct {
    Title     string `json:"title"`
    Body      string `json:"body"`
    DependsOn []int  `json:"depends_on"`
}

func Decompose(ctx context.Context, provider types.LLMProvider, issueBody string) ([]SubTask, error)
func CreateSubIssues(ctx context.Context, parent int, repo string, tasks []SubTask) ([]int, error)

const maxDecomposePromptLen = 8000
const decomposeTimeout = 30 * time.Second

// internal/agent/phase/decompose.go (unexported helpers)
// extractIssueNumberFromURL is a phase-local copy of the /issues/(\d+) parser;
// cmd/forge's extractIssueNumber can't be reused across the package boundary.
func extractIssueNumberFromURL(s string) int

// cmd/forge/issue.go
func fetchSubIssues(ref string, cwd string) ([]ghIssue, error)
func parseSubIssues(raw []byte) ([]ghIssue, error) // testable parse split out of fetchSubIssues
```

### Phase 2 interfaces (#222)
```go
// internal/types/task.go
type SubAgent struct {
    // ...existing fields...
    CWD string `json:"cwd,omitempty"` // working-directory override; empty → parent cwd
}

// internal/agent/worker.go
func subAgentCWD(parentCWD string, agent *types.SubAgent) string

// internal/agent/phase/multi.go
type SubIssue struct {
    Number int
    Title  string
    Body   string
}

type SpawnPhaseAgent func(ctx context.Context, cwd, branch, prompt string) error
type CreateWorktreeFunc func(ctx context.Context, repoRoot, baseBranch, branch, worktreePath string) (string, error)
type RemoveWorktreeFunc func(ctx context.Context, repoRoot, worktreePath string) error
type EnsurePRFunc func(ctx context.Context, cwd string) PRResult

type MultiPhaseOpts struct {
    Provider       types.LLMProvider
    RepoRoot       string
    BaseBranch     string
    ParentSlug     string
    WorktreeBase   string
    Phases         []SubIssue
    PRAttr         PRAttributionOpts
    Emit           func(types.OutboundEvent)
    SpawnAgent     SpawnPhaseAgent    // REQUIRED
    CreateWorktree CreateWorktreeFunc // nil → gitCreateWorktree
    RemoveWorktree RemoveWorktreeFunc // nil → gitRemoveWorktree
    EnsurePRFn     EnsurePRFunc       // nil → EnsurePR wrapper
}

func RunMultiPhase(ctx context.Context, opts MultiPhaseOpts) error
func SlugifyTitle(title string, maxLen int) string
```

### Phase 3 interfaces (#223)
```go
// internal/agent/phase/merge_poller.go
type PRStatusFunc func(ctx context.Context, cwd string, prNumber int) (prStatus, error)
type RebaseFunc func(ctx context.Context, cwd, base string) error

type MergeWaitOpts struct {
    PRNumber     int           // REQUIRED (> 0)
    Cwd          string
    BaseBranch   string
    PollInterval time.Duration // <= 0 → 30s
    Timeout      time.Duration // <= 0 → 2h
    PRStatus     PRStatusFunc  // nil → ghPRStatus
    Rebase       RebaseFunc    // nil → gitRebaseAndForcePush
}

func WaitForMerge(ctx context.Context, opts MergeWaitOpts) error
func extractPRNumberFromURL(s string) int // /pull/(\d+) parser; 0 if none

// internal/agent/phase/multi.go (added to MultiPhaseOpts)
type WaitForMergeFunc func(ctx context.Context, prNumber int, cwd, base string) error
type PullMainFunc func(ctx context.Context, repoRoot, base string) error
// MultiPhaseOpts += WaitForMergeEnabled bool, MergePollInterval/MergeTimeout
//   time.Duration, WaitForMergeFn WaitForMergeFunc, PullMainFn PullMainFunc
```

## Edge Cases
- Empty / whitespace-only issue body → `Decompose` returns `(nil, err)`, no LLM call.
- LLM returns a single-element array (single-phase decomposition) → returned as a
  one-element slice, valid.
- LLM returns `[]` (empty array) → `(nil, nil)`; caller treats as "no decomposition".
- LLM wraps JSON in ```json fences → stripped via `stripCodeFences` before parse.
- LLM returns malformed JSON → `(nil, err)` with the raw text in the error.
- LLM returns a JSON object instead of an array → `(nil, err)`.
- A task has empty/whitespace `title` → `(nil, err)` naming the index.
- `depends_on` contains an index >= len(tasks) or < 0 → that index is dropped,
  logged at info; the rest of the task is kept (valid indices preserved).
- All `LightweightModels` fail → `(nil, err)` wrapping the last model error.
- No models configured (empty `LightweightModels`) → `(nil, err)`.
- `fetchSubIssues` on an issue with no sub-issues → `(nil, nil)`.
- `fetchSubIssues` when `gh` missing (empty PATH) → error containing "GitHub CLI".
- `fetchSubIssues` with a full-URL ref → number extracted, placeholders resolved.
- `CreateSubIssues` when `gh` missing → `(nil, err)` before any creation.
- `CreateSubIssues` where the Nth attach fails → returns the first N-1 numbers + err.
- `gh issue create` output URL with trailing whitespace/newline → number parsed
  via the `/issues/(\d+)` regex (`extractIssueNumberFromURL`, phase-local copy).

### Phase 2 edge cases (#222)
- `SubAgent.CWD` empty → sub-agent runs in the parent worker's cwd (unchanged).
- `SubAgent.CWD` set → sub-agent's `loop.Options.CWD` is the override.
- `RunMultiPhase` with `SpawnAgent == nil` → returns "SpawnAgent is required" error.
- `RunMultiPhase` with empty `Phases` → returns nil, no work done.
- Sub-agent of phase i fails → pipeline halts; phases >i never start; worktree of
  phase i is left in place; error names "phase i" + title.
- Worktree creation of phase i fails → pipeline halts before spawning; no
  sub-agent runs for that phase.
- `EnsurePRFn` returns an error → logged + emitted, pipeline continues.
- `RemoveWorktree` returns an error → logged, pipeline continues.
- Context cancelled between phases → returns a "cancelled" error before the next
  worktree is created.
- Blank `ParentSlug` → branch falls back to `jelmer/phase-<number>`.
- Branch name with a slash → worktree directory uses hyphens (`jelmer/x-1` →
  `<base>/jelmer-x-1`).
- `gitCreateWorktree` when the branch already exists → falls back to
  `git worktree add <path> <branch>` (checkout existing branch).
