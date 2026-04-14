---
id: automated-review
status: active
---
# Automated multi-agent code review system

## Description
An extensible code review system that spawns parallel review agents across
multiple LLM providers (Anthropic, OpenAI) to reduce bias. Triggered via
`/review` in the TUI or `POST /review` on the agent/gateway HTTP API. Each
review agent gets fresh context (git diff, spec, project rules) and returns
structured findings. Results are aggregated and streamed back to the user.

The TUI supports slash-command autocomplete: typing `/` activates a suggestion
dropdown above the input showing all available commands with descriptions.
Commands filter as the user types (e.g. `/r` narrows to `/review`).

## Context
Files and systems that change:

- `internal/review/review.go` — types: Finding, ReviewResult, Reviewer interface, Severity constants
- `internal/review/reviewers.go` — 5 built-in reviewer implementations + DefaultReviewers/DefaultReviewersWithSpec
- `internal/review/orchestrator.go` — Orchestrator, ReviewRequest, parallel execution, JSON parsing, summary formatting
- `internal/review/diff.go` — GetDiff, detectBaseBranch, truncation, git helpers
- `internal/review/orchestrator_test.go` — orchestrator, parser, reviewer, summary, diff tests (49 test cases)
- `internal/review/diff_test.go` — diff truncation and git repo tests
- `internal/runtime/provider/openai.go` — OpenAI LLMProvider via raw HTTP streaming SSE
- `internal/runtime/provider/openai_test.go` — OpenAI provider tests with httptest (13 test cases)
- `internal/agent/server.go` — new `POST /review` endpoint
- `internal/agent/hub.go` — review trigger channel + TriggerReview/ReviewChannel methods
- `internal/agent/worker.go` — reviewListener goroutine, runReview orchestration, provider assembly
- `internal/server/gateway/gateway.go` — new `POST /sessions/{id}/review` endpoint
- `cmd/forge/cli.go` — `/review` command parsing, sendReview, review event display, finding formatting, slash-command autocomplete with dropdown, command registry
- `cmd/forge/commands.go` — slash command definitions (name, description, hidden flag)
- `cmd/forge/cli_test.go` — isReviewCommand, parseReviewBase, and slash command autocomplete tests

## Behavior
- `/review` in TUI triggers a review of committed changes on the current branch vs base branch
- `/review --base main` allows specifying a different base branch
- Only committed changes are reviewed (base...HEAD) — uncommitted and untracked files are excluded
- Base branch auto-detection prefers remote tracking refs (`origin/main` → `origin/master` → `main` → `master`) to avoid stale local branches in worktrees
- The review spawns 5 reviewer agents in parallel:
  1. **Security** — vulnerabilities, injection, auth issues, secret leaks
  2. **Code Quality & Tests** — correctness, test coverage, error handling, edge cases
  3. **Maintainability & Readability** — naming, complexity, dead code, consistency
  4. **Operational Readiness** — observability, logging, docs, config, error messages
  5. **Spec Validation** — verifies changes match the active spec (skipped if no spec)
- Each reviewer runs against every configured provider (Anthropic + OpenAI by default)
  - Total agents: 5 reviewers × 2 providers = 10 parallel agents
  - Each agent gets: git diff, project AGENTS.md, active specs, reviewer-specific prompt
  - Each agent is read-only (no tool access — just analysis)
- Results stream back as `review_finding` events
- Per-provider summaries emitted as `review_provider_summary` events after all reviewers for that provider complete
- Final `review_summary` event aggregates all findings by severity
- After review completes, actionable findings (non-praise) are automatically sent to the main conversation loop for remediation
- If only praise findings exist, review emits `done` without triggering remediation
- Severity levels: `critical`, `warning`, `suggestion`, `praise`
- Review can be re-run multiple times in the same session
- Agent HTTP API: `POST /review` with optional `{"base": "main"}`
- Gateway HTTP API: `POST /sessions/{id}/review` with optional `{"base": "main"}`
- TUI shows findings grouped by reviewer, with severity indicators
- Each finding includes: reviewer name, provider, severity, file path (if applicable), line range, description

### Slash-command autocomplete
- Typing `/` activates slash-command mode in the input
- A dropdown/dialog appears above the input showing matching commands
- Each entry shows the command name and a short description, e.g.:
  ```
  /review    Run multi-agent code review on current diff
  ```
- As the user types more characters (e.g. `/r`), the list filters to matching commands
- The currently selected suggestion is highlighted (distinct style from unselected)
- `Tab` accepts the currently selected suggestion (fills the input)
- `Up`/`Down` (or `Ctrl+P`/`Ctrl+N`) cycles through matched suggestions
- When input does not start with `/`, no autocomplete dropdown is shown
- The dropdown disappears once the user accepts a suggestion or clears the `/` prefix
- Commands are registered in a central command registry (`cmd/forge/commands.go`) with:
  - Name (e.g. `/review`)
  - Description (e.g. "Run multi-agent code review")
- Ghost text (inline completion) is shown via the built-in bubbles textinput suggestion rendering

## Constraints
- Review agents must not have write access (no Bash, Write, Edit, PRCreate) — implemented: review agents have no tools at all, they only analyze text
- Review agents get fresh context — no shared conversation history — implemented: each gets a fresh ChatRequest with diff only
- OpenAI provider implements the same `LLMProvider` interface — implemented: `OpenAIProvider.Chat()` returns `<-chan ChatDelta`
- OpenAI provider uses raw net/http (no SDK dependency) — implemented: pure stdlib + SSE parsing
- Provider API keys come from environment: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` — implemented
- If a provider's API key is missing, skip that provider (don't fail the review) — implemented
- Individual reviewer failures must not abort the entire review — implemented: errors captured per-result
- Review must work without a spec (spec validation reviewer is skipped) — implemented
- The review system must be testable without live API keys — implemented: mock providers in tests
- Review types (Finding, ReviewResult) live in internal/review/ package, not in types/ — decision: keeps review self-contained
- Slash-command autocomplete must use bubbles textinput's built-in `SetSuggestions`/`ShowSuggestions` — no custom suggestion engine
- Suggestions only activate when input starts with `/` — normal messages must not trigger autocomplete
- The dropdown must render above the input (between output area and input box), not obscure content
- Command registry must be easy to extend — adding a new command = one struct literal in the slice
- Dropdown must show the command description alongside the name so users know what each command does

## Interfaces

```go
// cmd/forge/commands.go

type slashCommand struct {
    Name        string // e.g. "/review"
    Description string // e.g. "Run multi-agent code review"
}

// slashCommands is the registry of all available slash commands.
var slashCommands = []slashCommand{
    {Name: "/review", Description: "Run multi-agent code review on current diff"},
}

// slashCommandNames returns just the command names for textinput.SetSuggestions.
func slashCommandNames() []string
```

```go
// internal/review/review.go

type Severity string

const (
    SeverityCritical   Severity = "critical"
    SeverityWarning    Severity = "warning"
    SeveritySuggestion Severity = "suggestion"
    SeverityPraise     Severity = "praise"
)

type Finding struct {
    Reviewer    string   `json:"reviewer"`
    Provider    string   `json:"provider"`
    Severity    Severity `json:"severity"`
    File        string   `json:"file,omitempty"`
    StartLine   int      `json:"startLine,omitempty"`
    EndLine     int      `json:"endLine,omitempty"`
    Description string   `json:"description"`
}

type ReviewResult struct {
    Reviewer string    `json:"reviewer"`
    Provider string    `json:"provider"`
    Findings []Finding `json:"findings"`
    Error    string    `json:"error,omitempty"`
}

type Reviewer interface {
    Name() string
    SystemPrompt() string
}

type ReviewRequest struct {
    Diff       string
    Specs      []types.SpecEntry
    Context    types.ContextBundle
    BaseBranch string
    CWD        string
}

func NewOrchestrator(providers map[string]types.LLMProvider, reviewers []Reviewer) *Orchestrator
func (o *Orchestrator) Run(ctx context.Context, req ReviewRequest, emit func(types.OutboundEvent)) []ReviewResult
func DefaultReviewers() []Reviewer          // 4 reviewers
func DefaultReviewersWithSpec() []Reviewer   // 5 reviewers (includes spec validation)
func GetDiff(cwd, baseBranch string) (string, error)
func FormatFindingsMessage(results []ReviewResult) string
func HasActionableFindings(results []ReviewResult) bool
```

```go
// internal/runtime/provider/openai.go

type OpenAIProvider struct { ... }

func NewOpenAI(apiKey string) *OpenAIProvider
func (p *OpenAIProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error)
```

```go
// internal/agent/hub.go

func (h *Hub) TriggerReview(baseBranch string)
func (h *Hub) ReviewChannel() <-chan string
```

## Edge Cases
- No git diff available (not a git repo, or no changes) → return early with informative message
- Uncommitted/untracked changes exist but no committed branch delta → empty diff, no review
- Missing ANTHROPIC_API_KEY → skip Anthropic provider, continue with others
- Missing OPENAI_API_KEY → skip OpenAI provider, continue with others
- All providers unavailable → return error "no providers available for review"
- One reviewer times out → include error in results, continue with others
- Very large diff (>100KB) → truncate with head/tail strategy, warn user
- No active spec → skip spec validation reviewer, run remaining 4
- Review triggered while agent is busy → queue it (same as messages)
- Network errors mid-review → individual failures reported, others continue
- Typing `/` with no further characters → show all commands in dropdown
- Typing `/xyz` where no command matches → dropdown disappears (no matches)
- Typing a normal message (no `/` prefix) → no dropdown, no suggestions
- Deleting back to empty after typing `/review` → suggestions cleared, dropdown hidden
- Accepting a suggestion then continuing to type (e.g. `/review --base`) → dropdown hidden after accept since input no longer matches a pure command prefix
- Up/Down arrow while dropdown is visible → cycles suggestions (does not scroll output)
- All findings are praise → review emits done, no remediation message sent
- Remediation message sent to main loop → agent implements fixes, emits its own done event
