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
PR and cleanup failures are non-fatal. Merge coordination between phases is #223.
This is part of the multi-phase orchestrator (issue #220).

Phase 3 (#223): Add cross-phase merge coordination. A `phase.WaitForMerge` poller
blocks on a phase's PR until it merges (continue), closes (halt), or times out
(halt), rebasing + force-pushing on each OPEN poll to clear merge conflicts.
`RunMultiPhase` gains an opt-in `WaitForMerge` flag that inserts a merge-wait +
sub-issue-close + base-pull step between phases, so each subsequent phase
branches from a base that includes all prior merged changes. Wiring the flag into
the real orchestrator/worker is deferred to a later phase of #220.

Phase 4 (#224): Wire multi-phase detection into the CLI and classifier so
`forge --issue` with sub-issues automatically enters multi-phase mode. When
`--issue` is provided, the CLI fetches the issue then checks for sub-issues via
the GitHub sub-issues API; if any exist (and `--no-plan` is not set), the session
runs in multi-phase mode (each sub-issue → its own worktree/branch/PR via the
Phase-2/3 `RunMultiPhase` coordinator). Also adds a classifier path: when intent
is `task` with size `large` and no sub-issues exist, the orchestrator decomposes
the parent (Phase 1) into sub-issues, then enters multi-phase mode. `--no-plan`
forces the legacy single-pipeline path even when sub-issues exist.

Phase 5 (#225): Manage sub-issue lifecycle on the parent issue as phases
complete. `RunMultiPhase` gains a `ParentNumber` plus injected `CommentIssue` /
`CloseParent` funcs. After each merged phase it posts a progress comment to the
parent; on full completion it posts a summary and closes the parent; on any
phase failure (worktree-create, sub-agent, or merge-wait halt) it posts a
failure comment naming the sub-issue and linking the PR. Sub-issue closure
already shipped in Phase 3. All parent lifecycle updates are non-fatal and are
skipped entirely when `ParentNumber == 0`.

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
  `PRState` parse, `ghPRState`, `rebasePRBranch`
- (Phase 3) `internal/agent/phase/merge_poller_test.go` — new: poller + parse tests
- (Phase 3) `internal/agent/phase/multi.go` — merge-wait integration in
  `RunMultiPhase` (opt-in `WaitForMerge` flag), `gitPullBase`, `ghCloseIssue`,
  `extractPRNumberFromURL` helpers
- (Phase 3) `internal/agent/phase/multi_test.go` — merge-wait integration tests
- (Phase 5) `internal/agent/phase/issue_lifecycle.go` — new: parent-comment +
  parent-close gh wrappers (`ghCommentIssue`, `ghCloseParent`) and pure comment
  formatters (`formatProgressComment`, `formatCompletionSummary`,
  `formatPhaseFailureComment`); `phaseRecord` struct
- (Phase 5) `internal/agent/phase/issue_lifecycle_test.go` — new: formatter +
  lifecycle integration tests
- (Phase 5) `internal/agent/phase/multi.go` — `ParentNumber`, `CommentIssue`,
  `CloseParent` opts; per-phase `phaseRecord` tracking; progress/summary/failure
  comment posting + parent close wired into `RunMultiPhase`

### Phase 4 (#224) — CLI integration + classifier routing
- `cmd/forge/cli.go` — sub-issue detection on `--issue`; `--no-plan` flag +
  validation; pass a `multi_phase` signal into the agent (new `--multi-phase`
  agent flag + metadata) when sub-issues exist and `--no-plan` is unset.
- `cmd/forge/cli_test.go` — flag-validation + sub-issue-detection routing tests.
- `cmd/forge/agent.go` — parse `--multi-phase` flag into `agent.Config`.
- `cmd/forge/session.go` — `spawnLocalAgent` forwards `--multi-phase` to the
  agent subprocess.
- `cmd/forge/main.go` — `--no-plan` help text + example.
- `internal/agent/server.go` / `internal/agent/worker.go` — thread a
  `MultiPhase bool` from `agent.Config` to the worker; gate orchestrator
  multi-phase routing on it.
- `internal/agent/phase/orchestrator.go` — large-task routing: when
  `classification.Size == TaskSizeLarge` and the multi-phase signal is set,
  decompose (if no sub-issues pre-exist) and run `RunMultiPhase`.
- `internal/agent/phase/orchestrator_test.go` — routing tests (large→decompose→
  multi-phase; sub-issues→skip-decompose→multi-phase).

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

### Phase 3 (#223) — Cross-phase merge coordination
- `WaitForMerge(ctx, opts)` polls a PR until terminal: probes once immediately
  (an already-merged PR returns with no wait), then on each `PollInterval` tick
  (default 30s) until `Timeout` (default 2h).
- State is fetched via `QueryState` (default `ghPRState`, which runs
  `gh pr view <number> --json state,mergedAt` and parses JSON). A PR is treated
  as merged when `state == "MERGED"` OR `mergedAt` is non-empty (legacy `gh`
  reports merged PRs as `CLOSED` + non-empty `mergedAt`).
- `MERGED` → `(MergeOutcomeMerged, nil)`; `CLOSED`-unmerged →
  `(MergeOutcomeClosed, err)`; timeout → `(MergeOutcomeTimeout, err)`; parent
  context cancel → `(MergeOutcomeTimeout, ctx.Err())` (error contains "cancelled",
  distinct from the timeout message).
- On every `OPEN` poll the poller calls `Rebase` (default `rebasePRBranch`:
  `git fetch origin <base>` → `git rebase origin/<base>` → `git push
  --force-with-lease`, aborting a failed rebase) to clear merge conflicts.
  A rebase error is logged at warn and polling continues (non-fatal).
- A `QueryState` error is logged at warn and retried on the next tick — transient
  gh/network failures must not abort a long wait. Only a terminal state or the
  timeout/cancel ends the loop.
- `RunMultiPhase` gains an opt-in `WaitForMerge bool`. When false the legacy
  Phase-2 behavior is unchanged (no merge wait; each phase branches from
  `BaseBranch` independently). When true, after `EnsurePRFn` the coordinator:
  1. Extracts the PR number from the PR URL (`extractPRNumberFromURL`).
  2. If no PR was created (`pr.Error != nil` or number 0) → halts the pipeline
     (don't run the next phase on a stale base).
  3. Calls `WaitForMergeFn` (default `WaitForMerge`); a non-nil error halts the
     pipeline naming the phase index, title, and outcome.
  4. On merge, closes the sub-issue via `CloseSubIssue` (default `ghCloseIssue`,
     `gh issue close <n>`; skipped when `Number == 0`) — non-fatal.
  5. Pulls the merged changes into the local base via `PullBase` (default
     `gitPullBase`: `git fetch origin <base>` + `git pull origin <base>`) —
     non-fatal — so the next phase's worktree (still branched from `BaseBranch`)
     includes them.
- `MergePollInterval`/`MergeTimeout` on `MultiPhaseOpts` override the poller's
  interval/timeout; zero uses the poller defaults.

### Phase 4 (#224) — CLI integration + classifier routing
- New CLI flag `--no-plan` (bool, default false): forces single-pipeline
  execution even when sub-issues exist. Valid ONLY with `--issue`; otherwise the
  CLI prints `--no-plan is only valid with --issue` to stderr and exits non-zero.
- When `--issue` is provided, after `fetchGitHubIssue` succeeds the CLI calls
  `fetchSubIssues(*issue, cwd)`:
  - sub-issues present AND `--no-plan` unset → multi-phase mode: the CLI sets a
    `multiPhase` flag passed through `spawnLocalAgent` → `--multi-phase` agent
    flag → `agent.Config.MultiPhase` → worker → orchestrator.
  - no sub-issues (`(nil, nil)`) OR `--no-plan` set → existing single-pipeline
    behavior (no multi-phase signal).
  - `fetchSubIssues` error → treated as no sub-issues (single-pipeline); the
    error is logged to stderr as a warning, NOT fatal. A missing-sub-issues
    repo/permission hiccup must not abort an otherwise-valid `--issue` session.
- The `--multi-phase` agent flag defaults false; `agent.Config` gains
  `MultiPhase bool`; `NewWorker` gains a `multiPhase bool` parameter stored on
  the worker; the worker forwards it into `OrchestratorOpts.MultiPhase`.
- `OrchestratorOpts` gains `MultiPhase bool`. When set AND the run reaches the
  task path with `classification.Size == TaskSizeLarge`, the orchestrator routes
  to multi-phase instead of the normal large→ideate pipeline:
  1. Re-fetch the parent's sub-issues (via an injected `SubIssuesFn`, default a
     `gh`-backed fetcher) for the session's issue number.
  2. If sub-issues exist → map them to `[]phase.SubIssue` (Number/Title/Body),
     skip decomposition.
  3. If none exist → `Decompose(ctx, Provider, parentBody)` then
     `CreateSubIssues(ctx, parentNum, repo, tasks)`, then re-fetch to build the
     `[]SubIssue`.
  4. Call `RunMultiPhase` with `SpawnAgent` wired to the worker's sub-agent
     runner, `RepoRoot`/`BaseBranch`/`WorktreeBase` from the session, and
     `ParentSlug = SlugifyTitle(parentTitle, maxParentSlugLen)`.
- Multi-phase routing only triggers on the task path; question/investigate/
  triage/review intents are unaffected (issue-driven sessions already
  `ForceTask`, so a large issue with sub-issues lands on the task path).
- When `MultiPhase` is false, orchestrator behavior is byte-for-byte the
  existing Phase-0 behavior (no decompose, no `RunMultiPhase`).
- The orchestrator needs the parent issue number/title/body to decompose and
  slugify. These are carried on `OrchestratorOpts` (`IssueNumber int`,
  `IssueTitle string`, `IssueBody string`). As implemented, the worker derives
  `IssueNumber` from `w.issueURL` via `extractIssueNumberFromURL`, sets
  `IssueBody` to the first-turn prompt (the fetched issue text), and leaves
  `IssueTitle` empty — so phase branches degrade to `jelmer/phase-<n>`
  (graceful Phase-2 fallback). Plumbing a real title for nicer slugs is a future
  refinement, not required here.
- The worker wires `OrchestratorOpts.SpawnPhaseAgentFn = w.spawnPhaseAgent(...)`,
  which runs a full-access conversation loop in the phase worktree (cwd) with a
  per-phase session ID `<parentSessionID>-<branch-with-slashes-as-hyphens>`.

### Phase 5 (#225) — Sub-issue lifecycle on the parent
- `MultiPhaseOpts` gains `ParentNumber int`, `CommentIssue CommentIssueFunc`
  (default `ghCommentIssue`: `gh issue comment <n> --body <body>`), and
  `CloseParent CloseParentFunc` (default `ghCloseParent`, a thin alias of
  `ghCloseIssue`). All parent lifecycle updates are skipped when
  `ParentNumber == 0`.
- After a phase's PR merges (only in the `WaitForMerge` path), `RunMultiPhase`
  posts a progress comment to the parent via `formatProgressComment`:
  `Phase <i>/<n> complete: **<title>** — PR #<num> merged.` (PR clause omitted
  when no PR number is known). The existing sub-issue close (`CloseSubIssue`)
  fires before the progress comment.
- After all phases complete (loop exits with no error), `RunMultiPhase` posts a
  completion summary via `formatCompletionSummary` (`All <n> phases complete:`
  followed by one `- #<sub> → PR #<pr> (merged|open)` line per phase; `→ no PR`
  when no PR), then closes the parent via `CloseParent`.
- On any phase failure that halts the pipeline (worktree-create error,
  sub-agent error, or merge-wait halt — closed/timeout/cancel or missing PR),
  `RunMultiPhase` posts a failure comment via `formatPhaseFailureComment`
  naming the phase + sub-issue and linking the PR when one exists, then returns
  the error. No summary is posted and the parent is NOT closed on failure.
- Per-phase outcomes are tracked in a `phaseRecord{Number, Title, PRURL, Merged}`
  slice; `Merged` is set true only when the merge wait succeeds.
- All parent comment/close calls are non-fatal: a `CommentIssue`/`CloseParent`
  error is logged at warn and never halts the pipeline.

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
- `WaitForMerge` (the flag) must be opt-in: with it false, `RunMultiPhase` is
  byte-for-byte the Phase-2 behavior (no merge wait, no sub-issue close, no
  base pull). All Phase-2 tests must keep passing unchanged.
- The poller must NOT abort on a transient `QueryState` error — log + retry on
  the next tick. Only `MERGED`/`CLOSED`/timeout/cancel are terminal.
- A rebase, sub-issue-close, or base-pull failure must NOT halt the pipeline —
  only a closed/timed-out/cancelled merge wait (or a missing PR) halts it.
- Tests must NOT invoke `gh` or hit the network: poller state is fed via injected
  `QueryState`/`WaitForMergeFn`/`Rebase`/`PullBase`/`CloseSubIssue` fakes;
  `parsePRState` is tested against fixture JSON strings.
- A merged-PR detection must accept the legacy `gh` shape (`CLOSED` + non-empty
  `mergedAt`), not only `state == "MERGED"`.

### Phase 4 constraints (#224)
- `--no-plan` MUST be rejected (non-zero exit) when `--issue` is absent.
- A `fetchSubIssues` error during CLI detection MUST NOT abort the session — fall
  back to single-pipeline and warn. Only `fetchGitHubIssue` failures are fatal.
- Multi-phase routing MUST be gated on the `MultiPhase` flag being explicitly
  set by the CLI. The orchestrator must not auto-enter multi-phase merely because
  classification returns `large` — `large` without the flag keeps the existing
  ideate pipeline (preserves all Phase-0 large-task tests).
- The orchestrator MUST NOT import `cmd/forge` (cycle). Sub-issue fetching inside
  the orchestrator uses an injected `SubIssuesFn` (default `gh`-backed) defined in
  the `phase` package, not `cmd/forge.fetchSubIssues`.
- No new plan manifest/state file — GitHub sub-issues remain the only state.
- Tests MUST NOT invoke `gh`/git/network: CLI tests cover flag validation +
  routing decisions with injected/fixture sub-issue results; orchestrator routing
  tests inject `SubIssuesFn`, `Decompose`-equivalent, and a fake `SpawnAgent`.
- `--multi-phase` must default false end-to-end so a non-issue or `--no-plan`
  session never accidentally enters multi-phase.

### Phase 5 constraints (#225)
- Parent lifecycle updates (progress comment, summary, parent close, failure
  comment) must be no-ops when `ParentNumber == 0` — existing callers passing
  no parent are unchanged.
- A `CommentIssue` or `CloseParent` failure must NOT halt the pipeline — log at
  warn and continue.
- The completion summary and parent close must fire only on a clean run (loop
  exits with nil error). A halt must post a failure comment and must NOT post a
  summary or close the parent.
- Progress comments fire only on the merge path (`WaitForMerge` true). With
  `WaitForMerge` false no progress comments are posted (no merge signal exists).
- Comment formatters must be pure (no `gh`, no network) and unit-tested against
  expected strings; lifecycle integration tests inject `CommentIssue` /
  `CloseParent` / `CloseSubIssue` fakes — never invoke `gh`.

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
type PRState struct {
    State    string `json:"state"`    // "OPEN" | "MERGED" | "CLOSED"
    MergedAt string `json:"mergedAt"`
}

type MergeOutcome int
const (
    MergeOutcomeMerged MergeOutcome = iota
    MergeOutcomeClosed
    MergeOutcomeTimeout
)

type PRStateFunc func(ctx context.Context, cwd string, prNumber int) (PRState, error)
type RebaseFunc func(ctx context.Context, cwd string) error

type WaitForMergeOpts struct {
    CWD          string
    PRNumber     int
    PollInterval time.Duration // default 30s
    Timeout      time.Duration // default 2h
    QueryState   PRStateFunc   // nil → ghPRState
    Rebase       RebaseFunc    // nil → rebasePRBranch
    Emit         func(content string)
}

func WaitForMerge(ctx context.Context, opts WaitForMergeOpts) (MergeOutcome, error)

// internal/agent/phase/multi.go (Phase 3 additions to MultiPhaseOpts)
type WaitForMergeFunc func(ctx context.Context, opts WaitForMergeOpts) (MergeOutcome, error)
type PullBaseFunc func(ctx context.Context, repoRoot, baseBranch string) error
type CloseSubIssueFunc func(ctx context.Context, repoRoot string, number int) error

// MultiPhaseOpts gains:
//   WaitForMerge      bool             // opt-in cross-phase merge coordination
//   MergePollInterval time.Duration    // override poller interval
//   MergeTimeout      time.Duration    // override poller timeout
//   WaitForMergeFn    WaitForMergeFunc // nil → WaitForMerge
//   PullBase          PullBaseFunc     // nil → gitPullBase
//   CloseSubIssue     CloseSubIssueFunc// nil → ghCloseIssue

func extractPRNumberFromURL(prURL string) int // 0 when none found
```

### Phase 4 interfaces (#224)
```go
// cmd/forge/cli.go — new flag, parsed in runCLI's FlagSet.
noPlan := fs.Bool("no-plan", false, "force single-pipeline even when sub-issues exist (requires --issue)")

// Validation (after parse):
//   if *noPlan && *issue == "" -> stderr "--no-plan is only valid with --issue"; exit 1

// detectMultiPhase decides whether an --issue session enters multi-phase mode.
// Returns true when sub-issues exist and noPlan is false. A fetchSubIssues
// error is non-fatal: returns false + the error for the caller to warn on.
func detectMultiPhase(issueRef, cwd string, noPlan bool) (multiPhase bool, err error)

// cmd/forge/session.go — spawnLocalAgent gains a multiPhase bool param;
// appends "--multi-phase" to agentArgs when true.

// cmd/forge/agent.go — new flag:
multiPhase := fs.Bool("multi-phase", false, "run sub-issues as sequential multi-phase pipeline")
// -> agent.Config.MultiPhase = *multiPhase

// internal/agent — Config + worker plumbing:
type Config struct {
    // ...existing...
    MultiPhase bool
}
func NewWorker(hub *Hub, sessionID, cwd, sessionsDir, mode, specPath, modelOverride, issueURL string, multiPhase bool) *Worker

// internal/agent/phase/orchestrator.go — OrchestratorOpts gains:
//   MultiPhase  bool   // enter multi-phase routing on the large-task path
//   IssueNumber int    // parent issue number (for decompose + branch naming)
//   IssueTitle  string // parent issue title (slugified into branch names)
//   IssueBody   string // parent issue body (decompose input)
//   SubIssuesFn SubIssuesFunc // nil -> gh-backed fetcher

// SubIssuesFunc fetches a parent's sub-issues as phase.SubIssue, in position
// order; (nil, nil) when none.
type SubIssuesFunc func(ctx context.Context, cwd string, parentNumber int) ([]SubIssue, error)

// CreateSubIssuesFunc creates GitHub sub-issues from decomposed tasks (default
// CreateSubIssues; injected in tests).
type CreateSubIssuesFunc func(ctx context.Context, parent int, repo string, tasks []SubTask) ([]int, error)

// OrchestratorOpts also gains CreateSubIssuesFn CreateSubIssuesFunc.
// Orchestrator gains an unexported `runMultiPhase func(ctx, MultiPhaseOpts) error`
// test seam (nil → RunMultiPhase), accessed via runMultiPhaseFn().

// runMultiPhasePipeline is the large-task multi-phase branch invoked from
// runSWEPipeline when opts.MultiPhase is set. As implemented it returns the
// OrchestratorResult (so a decompose-failure ideate fallback preserves session
// continuity such as CoderHistoryID).
func (o *Orchestrator) runMultiPhasePipeline(ctx context.Context, opts OrchestratorOpts) (OrchestratorResult, error)

// ghSubIssues is the default SubIssuesFunc (gh api repos/{owner}/{repo}/issues/
// {number}/sub_issues). parseGHSubIssues unmarshals the response into
// []SubIssue (empty → nil,nil).
func ghSubIssues(ctx context.Context, cwd string, parentNumber int) ([]SubIssue, error)
func parseGHSubIssues(raw []byte) ([]SubIssue, error)

// internal/agent/worker.go — phase-agent spawner + issue-number parser.
func (w *Worker) spawnPhaseAgent(prov types.LLMProvider, registry *tools.Registry, bundle types.ContextBundle, store *session.Store) phase.SpawnPhaseAgent
func extractIssueNumberFromURL(url string) int // 0 when none found
func branchToSessionSuffix(branch string) string

const maxParentSlugLen = 40
```

### Phase 5 interfaces (#225)
```go
// internal/agent/phase/issue_lifecycle.go
type CommentIssueFunc func(ctx context.Context, repoRoot string, number int, body string) error
type CloseParentFunc func(ctx context.Context, repoRoot string, number int) error

type phaseRecord struct {
    Number int    // sub-issue number (0 = unknown)
    Title  string // sub-issue title
    PRURL  string // PR URL, empty if none
    Merged bool   // PR merged (only set when WaitForMerge is on)
}

func formatProgressComment(index, total int, rec phaseRecord) string
func formatCompletionSummary(records []phaseRecord) string
func formatPhaseFailureComment(index, total int, rec phaseRecord, cause error) string

func ghCommentIssue(ctx context.Context, repoRoot string, number int, body string) error
func ghCloseParent(ctx context.Context, repoRoot string, number int) error

// internal/agent/phase/multi.go (Phase 5 additions to MultiPhaseOpts)
//   ParentNumber int              // 0 → skip all parent lifecycle updates
//   CommentIssue CommentIssueFunc // nil → ghCommentIssue
//   CloseParent  CloseParentFunc  // nil → ghCloseParent
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

### Phase 3 edge cases (#223)
- PR already merged on the first probe → `WaitForMerge` returns
  `(MergeOutcomeMerged, nil)` after a single query, no interval wait.
- PR `OPEN` for several polls then `MERGED` → poller loops, rebases each OPEN
  poll, returns merged.
- PR `CLOSED` unmerged → `(MergeOutcomeClosed, err)` containing
  "closed without merging".
- Legacy `gh` reports `CLOSED` + non-empty `mergedAt` → treated as merged.
- `Timeout` elapses while `OPEN` → `(MergeOutcomeTimeout, err)` containing
  "timed out".
- Parent context cancelled mid-wait → `(MergeOutcomeTimeout, err)` containing
  "cancelled" (distinct from timeout).
- Transient `QueryState` error → logged, retried next tick; a later `MERGED`
  still succeeds.
- `Rebase` returns an error on an OPEN poll → logged, polling continues.
- `parsePRState` on malformed JSON or an empty `{}` object → error.
- `extractPRNumberFromURL` on empty/non-PR URL → 0.
- `RunMultiPhase` with `WaitForMerge` true but a phase produced no PR
  (`pr.Error != nil` or number 0) → pipeline halts with "no PR was created".
- `RunMultiPhase` merge wait returns closed/timeout → pipeline halts naming the
  phase + outcome; later phases never start.
- `CloseSubIssue` or `PullBase` failure after a merge → logged, pipeline
  continues to the next phase.
- `RunMultiPhase` with `WaitForMerge` false → no merge wait, close, or pull
  occurs (Phase-2 behavior preserved).

### Phase 4 edge cases (#224)
- `--no-plan` without `--issue` → stderr error, exit 1, no session started.
- `--no-plan` with `--issue` and sub-issues present → single-pipeline (the issue
  is implemented as one session; sub-issues ignored).
- `--issue` with sub-issues, `--no-plan` unset → multi-phase mode; CLI forwards
  `--multi-phase` to the agent.
- `--issue` with NO sub-issues → single-pipeline (no `--multi-phase`).
- `fetchSubIssues` returns an error (e.g. repo lacks the sub-issues API,
  permission denied, `gh` quirk) → CLI warns to stderr, proceeds single-pipeline,
  exit code unaffected.
- `--multi-phase` set but classification returns `small`/`standard` (not
  `large`) → existing size-based pipeline runs; multi-phase routing is skipped.
  (CLI sub-issue detection already implies large work, but the orchestrator must
  not crash on a small classification — it falls through to normal routing.)
- Orchestrator multi-phase path, sub-issues already exist → `SubIssuesFn` returns
  them, decomposition is skipped, `RunMultiPhase` runs them in position order.
- Orchestrator multi-phase path, no sub-issues yet → `Decompose` +
  `CreateSubIssues` create them, then re-fetch → `RunMultiPhase`.
- `Decompose` returns `(nil, err)` (empty body / all models fail) during the
  large-task path → orchestrator falls back to the normal ideate pipeline,
  emits a user-facing warning event, and returns the fallback's
  OrchestratorResult (do not hard-fail the session on a decompose error).
- `CreateSubIssues` partially creates then errors → orchestrator surfaces the
  error and halts the task, emitting a warning naming the parent issue so the
  user can review/close partially-created sub-issues (no half-run multi-phase on
  an incomplete plan).
- Multi-phase requested but `IssueNumber <= 0` (missing/malformed issue URL) →
  worker logs and skips multi-phase, running the single-pipeline path.
- Sub-agent events from a phase are forwarded to the parent hub, prefixed with a
  `[phase <branch>]` marker (skipped for empty/already-tagged/structured-JSON
  content); a nil hub drops them with a single per-spawn log line.
- Multi-phase run with `IssueTitle` empty → `ParentSlug` blank →
  `phaseBranchName` degrades to `jelmer/phase-<n>` (Phase-2 behavior).
- `MultiPhase` false → orchestrator never calls `SubIssuesFn`/`Decompose`/
  `RunMultiPhase`; all existing routing tests pass unchanged.

### Phase 5 edge cases (#225)
- `ParentNumber == 0` → no progress comments, no summary, no parent close, no
  failure comment.
- All phases merge cleanly → N progress comments + 1 summary; parent closed once.
- Progress comment with a known PR → `Phase i/n complete: **title** — PR #x merged.`
- Progress comment with no PR number → `Phase i/n complete: **title**.`
- Completion summary line with no sub-issue number → falls back to the title.
- Completion summary line with an un-merged PR (open) → `(open)`; merged → `(merged)`;
  no PR → `→ no PR`.
- Sub-agent failure on phase i → one failure comment naming `#<num>` + error,
  no summary, parent not closed; later phases never start.
- Worktree-create failure → failure comment posted before the error returns.
- Merge-wait halt (closed/timeout) → failure comment includes the error and PR
  link; pipeline halts.
- `CommentIssue` returns an error → logged at warn, pipeline continues (verified
  across progress + summary calls).
- `CloseParent` returns an error → logged at warn, pipeline still returns nil.
