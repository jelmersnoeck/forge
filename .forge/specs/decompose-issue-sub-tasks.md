---
id: decompose-issue-sub-tasks
status: implemented
---
# Decompose a large issue into ordered GitHub sub-issues

## Description
Add a lightweight LLM call that decomposes a large parent issue body into
ordered, independently-shippable sub-tasks, creates each as a GitHub issue,
and attaches it to the parent as a sub-issue. Also add a fetcher that reads
back the parent's sub-issues in position order. This is Phase 1 of 5 of the
multi-phase orchestrator (issue #220); later phases consume these primitives
to run each sub-issue as a sequential sub-agent pipeline. Scope here is the
decompose call, sub-issue creation, and sub-issue fetch — no orchestration,
no worktrees, no CWD overrides.

## Context
- `internal/agent/phase/decompose.go` — new: decompose LLM call + sub-issue creation
- `internal/agent/phase/decompose_test.go` — new: decompose + parse tests
- `internal/agent/phase/classify.go` — pattern to mirror (lightweight model loop,
  `stripCodeFences`, JSON parse, `truncateAtWordBoundary`, per-attempt timeout)
- `cmd/forge/issue.go` — add `fetchSubIssues`; reuse `ghIssue`, `normalizeIssueRef`
- `cmd/forge/issue_test.go` — add sub-issue parse tests
- `internal/types/types.go` — `LightweightModels`, `LLMProvider`, `ChatRequest`/`ChatDelta`

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
