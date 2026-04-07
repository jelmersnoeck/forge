---
id: automated-review
status: draft
---
# Automated multi-agent code review system

## Description
An extensible code review system that spawns parallel review agents across
multiple LLM providers (Anthropic, OpenAI) to reduce bias. Triggered via
`/review` in the TUI or `POST /review` on the agent/gateway HTTP API. Each
review agent gets fresh context (git diff, spec, project rules) and returns
structured findings. Results are aggregated and streamed back to the user.

## Context
Files and systems that change:

- `internal/review/` — new package: reviewer registry, orchestrator, reviewer definitions
- `internal/review/reviewer.go` — Reviewer interface and built-in reviewers
- `internal/review/orchestrator.go` — runs reviewers in parallel across providers
- `internal/review/diff.go` — git diff extraction
- `internal/runtime/provider/openai.go` — new OpenAI LLMProvider implementation
- `internal/agent/server.go` — new `POST /review` endpoint
- `internal/agent/worker.go` — review execution support
- `internal/server/gateway/gateway.go` — new `POST /sessions/{id}/review` endpoint
- `cmd/forge/cli.go` — `/review` command handling
- `internal/types/types.go` — ReviewRequest, ReviewResult, ReviewFinding types

## Behavior
- `/review` in TUI triggers a review of the current git diff (staged + unstaged vs base branch)
- `/review --base main` allows specifying a different base branch
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
- Final `review_summary` event aggregates all findings by severity
- Severity levels: `critical`, `warning`, `suggestion`, `praise`
- Review can be re-run multiple times in the same session
- Agent HTTP API: `POST /review` with optional `{"base": "main"}`
- Gateway HTTP API: `POST /sessions/{id}/review` with optional `{"base": "main"}`
- TUI shows findings grouped by reviewer, with severity indicators
- Each finding includes: reviewer name, provider, severity, file path (if applicable), line range, description

## Constraints
- Review agents must not have write access (no Bash, Write, Edit, PRCreate)
- Review agents get fresh context — no shared conversation history
- OpenAI provider must implement the same `LLMProvider` interface
- Do not add external dependencies for the OpenAI SDK — use raw HTTP (like Anthropic SDK pattern, but we'll use stdlib since there's no official Go SDK we want)
- Provider API keys come from environment: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`
- If a provider's API key is missing, skip that provider (don't fail the review)
- Individual reviewer failures must not abort the entire review
- Review must work without a spec (spec validation reviewer is skipped)
- The review system must be testable without live API keys

## Interfaces

```go
// internal/review/reviewer.go

// Severity levels for review findings.
type Severity string

const (
    SeverityCritical   Severity = "critical"
    SeverityWarning    Severity = "warning"
    SeveritySuggestion Severity = "suggestion"
    SeverityPraise     Severity = "praise"
)

// Finding is a single review observation.
type Finding struct {
    Reviewer    string   `json:"reviewer"`
    Provider    string   `json:"provider"`
    Severity    Severity `json:"severity"`
    File        string   `json:"file,omitempty"`
    StartLine   int      `json:"startLine,omitempty"`
    EndLine     int      `json:"endLine,omitempty"`
    Description string   `json:"description"`
}

// ReviewResult holds the output of a single reviewer+provider run.
type ReviewResult struct {
    Reviewer string    `json:"reviewer"`
    Provider string    `json:"provider"`
    Findings []Finding `json:"findings"`
    Error    string    `json:"error,omitempty"`
}

// Reviewer analyzes a diff and returns findings.
type Reviewer interface {
    Name() string
    SystemPrompt() string
}

// ReviewRequest is the input to the review orchestrator.
type ReviewRequest struct {
    Diff     string
    Specs    []types.SpecEntry
    Context  types.ContextBundle
    BaseBranch string
    CWD      string
}

// Orchestrator runs all reviewers across all providers.
type Orchestrator struct { ... }

func NewOrchestrator(providers map[string]types.LLMProvider, reviewers []Reviewer) *Orchestrator
func (o *Orchestrator) Run(ctx context.Context, req ReviewRequest, emit func(types.OutboundEvent)) []ReviewResult
```

```go
// internal/runtime/provider/openai.go

type OpenAIProvider struct { ... }

func NewOpenAI(apiKey string) *OpenAIProvider
func (p *OpenAIProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error)
```

## Edge Cases
- No git diff available (not a git repo, or no changes) → return early with informative message
- Missing ANTHROPIC_API_KEY → skip Anthropic provider, continue with others
- Missing OPENAI_API_KEY → skip OpenAI provider, continue with others
- All providers unavailable → return error "no providers available for review"
- One reviewer times out → include error in results, continue with others
- Very large diff (>100KB) → truncate with head/tail strategy, warn user
- No active spec → skip spec validation reviewer, run remaining 4
- Review triggered while agent is busy → queue it (same as messages)
- Network errors mid-review → individual failures reported, others continue
