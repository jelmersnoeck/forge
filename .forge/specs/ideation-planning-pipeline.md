---
id: ideation-planning-pipeline
status: implemented
---
# Multi-agent ideation and planning pipeline with debate pattern

## Description
Replace the current single-agent spec-creator phase with a multi-agent
ideation→clarification→planning pipeline that uses a structured debate pattern
to explore multiple design options. The pipeline is input-driven: different
message sources (TUI, Linear webhook, PR monitor, etc.) declare whether they
need ideation or should skip straight to coding. The planner generates multiple
candidate approaches, weighs them against repo context (patterns, learnings,
historic decisions), selects one, and records the rejected alternatives with
rationale in the spec. The coder phase is enhanced with TDD enforcement and
automatic staleness checks before implementation begins.

## Context
Files and systems affected:

- `internal/types/types.go` — `InboundMessage.Metadata` already has
  `map[string]any`; well-known key `"pipeline_hint"` with values
  `"ideate"`, `"code"`, `"auto"` (default). Added `Alternatives string`
  to `SpecDocument`. Updated `OutboundEvent` type docs with new events.
- `internal/agent/phase/phase.go` — added `Ideator()`, `Clarifier()`, `Planner()`
  phase definitions; existing `SpecCreator()` preserved as fallback for
  non-ideation path
- `internal/agent/phase/prompts.go` — added `plannerPrompt` and TDD-enforcing
  `coderPrompt` update; `PromptForPhase()` handles `"plan"`, `"ideate"`,
  `"clarify"` names
- `internal/agent/phase/orchestrator.go` — `runSWEPipeline()` now branches on
  `shouldIdeate()`; added `PipelineHint` to `OrchestratorOpts`; integrated
  `CheckStaleness()` before coder phase
- `internal/agent/phase/debate.go` — NEW: multi-agent debate runner (ideate →
  clarify → plan) with `RunDebate()`, candidate/clarified result types, JSON
  parsing, context summary builder
- `internal/agent/phase/debate_test.go` — NEW: 13 test cases covering parsing,
  ideation, clarification, events, personalities, edge cases
- `internal/agent/phase/staleness.go` — NEW: `CheckStaleness()` with git
  fetch/rev-list/pull logic
- `internal/agent/phase/staleness_test.go` — NEW: 4 test cases with real git repos
- `internal/agent/phase/phase_test.go` — added test cases for Ideator, Clarifier,
  Planner phases and their prompt mappings
- `internal/agent/worker.go` — `extractPipelineHint()` reads from message
  metadata; passes `PipelineHint` to orchestrator
- `internal/agent/worker_test.go` — 9 test cases for `extractPipelineHint()`
- `internal/spec/spec.go` — parses `## Alternatives` section into
  `SpecDocument.Alternatives`
- `internal/spec/spec_test.go` — test for Alternatives section parsing
- `cmd/forge/cli.go` — CLI handlers for ideation/clarification/planning/
  staleness event types
- `.forge/specs/` — specs now support optional `## Alternatives` section

## Behavior

### Pipeline hint (input-driven routing)

Each `InboundMessage` can carry a `pipeline_hint` in its `Metadata` map that
declares the desired pipeline behavior:

| Hint       | Behavior                                  | Source examples              |
|------------|-------------------------------------------|------------------------------|
| `"ideate"` | Always run ideation pipeline              | TUI default                  |
| `"code"`   | Skip ideation, go straight to coding      | Linear webhook, PR monitor   |
| `"auto"`   | Use `ClassifyIntent` + complexity gate    | API callers, default         |

When no hint is present, `"auto"` is assumed. The `"auto"` path preserves
today's behavior: classify intent → question goes to Q&A, task goes through
a lightweight complexity check (separate spec) that decides whether to ideate
or code directly.

The orchestrator reads the hint via `opts.PipelineHint` which the worker
extracts from `msg.Metadata["pipeline_hint"]`.

### Ideation pipeline (3 sub-agents, debate pattern)

When ideation is triggered, the spec-creator phase is replaced by three
coordinated sub-agents:

```
  User prompt
      │
      ▼
  ┌──────────────────┐
  │  od-ideate (×3)  │  3 parallel ideation agents
  │  different temps  │  each produces 1-2 candidate approaches
  └────────┬─────────┘
           │ 6-9 raw candidates
           ▼
  ┌──────────────────┐
  │  od-clarifier    │  dedupes, asks follow-up questions,
  │                  │  identifies gaps & conflicts
  └────────┬─────────┘
           │ refined candidates + questions
           ▼
  ┌──────────────────┐
  │  od-planner      │  weighs candidates against repo context,
  │                  │  selects winner, writes spec with
  │                  │  Alternatives section
  └────────┬─────────┘
           │ spec file (.forge/specs/<id>.md)
           ▼
       Coder phase
```

#### od-ideate (Ideation agents)

- 3 agents run in parallel using direct LLM calls (same pattern as the review
  orchestrator — goroutines + WaitGroup, NOT the task.Manager sub-agent infra)
- Each agent has a different personality to encourage diverse ideas:
  - Agent A: conservative — minimal changes, reuse existing patterns
  - Agent B: pragmatic — balanced approach, moderate refactoring ok
  - Agent C: ambitious — clean-slate thinking, architectural improvements
- Each agent receives: the user prompt, AGENTS.md, project structure, active
  specs, and `.forge/learnings/`
- Each agent produces 1-2 candidate approaches in structured JSON:
  ```json
  {
    "candidates": [
      {
        "name": "short-name",
        "summary": "2-3 sentence description",
        "approach": "detailed technical approach (files, types, flow)",
        "tradeoffs": ["pro1", "pro2"],
        "risks": ["risk1", "risk2"],
        "effort": "S|M|L|XL",
        "reuses": ["existing-pattern-or-type-it-builds-on"]
      }
    ]
  }
  ```
- Tools: none (direct LLM call with project context injected into prompt)
- Max turns: n/a (single LLM call, not a conversation loop)
- Model: same as session default (not Haiku — ideation needs reasoning power)

#### od-clarifier (Clarification agent)

- Single agent that receives all candidate outputs
- Responsibilities:
  1. Deduplicate similar approaches (merge if >80% overlap)
  2. Identify gaps: does any candidate miss a key constraint from AGENTS.md?
  3. Identify conflicts: do candidates make incompatible assumptions?
  4. Generate clarifying questions if ambiguity exists (surfaced to user
     only in interactive mode; in webhook mode, the clarifier resolves
     ambiguity by choosing the safer interpretation)
  5. Produce a refined candidate list with normalized structure
- Tools: none (direct LLM call with candidates injected)
- Max turns: n/a (single LLM call)
- Output: refined candidates JSON + optional questions list

#### od-planner (Planning agent)

- Single agent that receives refined candidates + full repo context
- Responsibilities:
  1. Score each candidate against:
     - **Repo patterns**: does it follow existing conventions? (reads code)
     - **Historic decisions**: does it align with past specs? (reads
       `.forge/specs/`)
     - **Learnings**: does it avoid known pitfalls? (reads
       `.forge/learnings/`)
     - **Effort vs value**: is the effort proportional?
     - **Risk**: failure modes, rollback complexity
  2. Select the winning approach with explicit reasoning
  3. Write the spec to `.forge/specs/<id>.md` including:
     - Standard spec sections (Description, Context, Behavior, etc.)
     - NEW `## Alternatives` section listing rejected candidates with:
       - Candidate name
       - Brief description
       - Why it was not selected (specific, falsifiable reason)
  4. Set spec status to `draft`
- Tools: Read, Grep, Glob, Bash (read-only), Write (specs only), WebSearch
- Max turns: 150
- Model: same as session default

### Spec Alternatives section

The spec format gains a new optional section `## Alternatives`. The planner
writes it; the coder respects it (doesn't accidentally implement a rejected
approach). Format:

```markdown
## Alternatives

### <candidate-name>
<2-3 sentence description>

**Not selected because:** <specific, falsifiable reason — e.g., "requires
adding a new dependency (lib-x) which conflicts with the no-new-deps
constraint" or "duplicates logic already in internal/tools/registry.go
rather than extending it">
```

The spec parser (`internal/spec/spec.go`) is updated to parse this section
into `SpecDocument.Alternatives string`.

### TDD enforcement in coder phase

The coder phase prompt is updated to enforce TDD:

1. Before writing implementation code, write a failing test for the first
   Behavior point in the spec
2. Run the test — confirm it fails
3. Write the minimum implementation to pass the test
4. Run the test — confirm it passes
5. Repeat for each Behavior point and Edge Case
6. Refactor after tests pass (red-green-refactor)

This is prompt-level enforcement, not code-level. The coder prompt includes
explicit instructions and the anti-pattern: "Do not write implementation
before the corresponding test exists."

### Staleness check before implementation

Before the coder phase begins, the orchestrator runs a staleness check:

```go
func checkStaleness(cwd string) (stale bool, behind int, err error)
```

- Runs `git fetch --quiet` then `git rev-list --count HEAD..@{upstream}`
- If `behind > 0`, the orchestrator emits a `staleness_warning` event and
  runs `git pull --rebase` to bring the branch up to date
- If the pull fails (conflicts), the orchestrator emits a `staleness_error`
  event and halts — the user must resolve conflicts manually
- The check also runs before each commit (orchestrator-level, not tool-level):
  the coder phase's `OnComplete` callback triggers a staleness check before
  the commit is finalized

Staleness check is a fast, non-LLM operation. It adds <2s in the common case
(no changes to pull).

### Events

New event types:
- `ideation_start` — emitted when the ideation pipeline begins
- `ideation_candidate` — emitted per candidate approach (content: JSON)
- `clarification_start` — emitted when clarifier begins
- `clarification_question` — emitted per clarifying question (interactive mode)
- `planning_start` — emitted when planner begins
- `planning_selection` — emitted when winner is chosen (content: candidate name
  + one-line reason)
- `staleness_warning` — emitted when branch is behind upstream
- `staleness_error` — emitted when pull/rebase fails

### Pipeline hint propagation

The worker currently reads `msg.Text` and passes it to the orchestrator. The
change: also read `msg.Metadata["pipeline_hint"]` and pass it as
`OrchestratorOpts.PipelineHint`:

```go
type OrchestratorOpts struct {
    // ... existing fields ...
    PipelineHint string // "ideate", "code", "auto", or ""
}
```

Sources that skip ideation (Linear webhook, PR monitor) set
`Metadata["pipeline_hint"] = "code"` when pushing messages. The TUI sets
`"ideate"` by default (or could be configurable).

## Constraints
- Do not remove the existing `ClassifyIntent` — it still gates question vs
  task. The pipeline hint is a separate, downstream gate for task routing.
- Do not modify `loop.Loop` — the ideation and clarification agents use direct
  LLM calls (same pattern as review agents); only the planner uses a loop.
- The ideation agents must not write files — they produce structured JSON
  output via direct LLM calls. Only the planner writes the spec.
- The existing `SpecCreator()` phase is preserved as the fallback for
  `pipeline_hint="code"` and `pipeline_hint="auto"` — it is NOT retired.
- The debate pattern must be testable without live API keys — mock providers
  in tests (same pattern as review orchestrator tests).
- The `## Alternatives` section is optional — specs without it are valid.
  The parser must not fail on specs missing this section.
- Staleness check must not fail if there's no upstream (e.g., new branch
  with no remote tracking) — treat as not stale.
- Staleness check must not modify the working tree if there are uncommitted
  changes — skip the pull, emit warning only.
- The 3 ideation agents run concurrently (like review agents), not
  sequentially — wall-clock time should be ~1× a single agent, not 3×.
- Pipeline hint values are stringly-typed in metadata; invalid values
  default to `"auto"`.
- TDD enforcement is prompt-level only — no code-level tooling changes.
  The coder prompt update is the entire implementation.
- Max total ideation pipeline time (all 3 phases): 10 minutes. Individual
  agent timeouts: 5 minutes (same as review agents).

## Interfaces

```go
// internal/agent/phase/phase.go

// Ideator returns the ideation agent phase configuration.
func Ideator() Phase

// Clarifier returns the clarification agent phase configuration.
func Clarifier() Phase

// Planner returns the planning agent phase configuration.
func Planner() Phase
```

```go
// internal/agent/phase/debate.go

// Candidate is a proposed approach from an ideation agent.
type Candidate struct {
    Name      string   `json:"name"`
    Summary   string   `json:"summary"`
    Approach  string   `json:"approach"`
    Tradeoffs []string `json:"tradeoffs"`
    Risks     []string `json:"risks"`
    Effort    string   `json:"effort"`
    Reuses    []string `json:"reuses"`
    Source    string   `json:"source"` // which ideator produced it
}

// ClarifiedResult is the output of the clarifier.
type ClarifiedResult struct {
    Candidates []Candidate `json:"candidates"`
    Questions  []string    `json:"questions,omitempty"`
    Merged     []string    `json:"merged,omitempty"` // names of merged candidates
}

// DebateResult is the output of the full ideation pipeline.
type DebateResult struct {
    SpecPath     string      `json:"spec_path"`
    Winner       Candidate   `json:"winner"`
    Alternatives []Candidate `json:"alternatives"`
}

// RunDebate executes the ideation → clarification → planning pipeline.
func RunDebate(ctx context.Context, opts DebateOpts) (DebateResult, error)

// DebateOpts configures a debate run.
type DebateOpts struct {
    Provider     types.LLMProvider
    Registry     *tools.Registry
    Bundle       types.ContextBundle
    CWD          string
    SessionStore *session.Store
    SessionID    string
    Model        string
    Emit         func(types.OutboundEvent)
    AuditLogger  types.AuditLogger
    Prompt       string // user's original request
}
```

```go
// internal/agent/phase/staleness.go

// StalenessResult describes the freshness state of the current branch.
type StalenessResult struct {
    Stale   bool   // true if behind upstream
    Behind  int    // number of commits behind
    Pulled  bool   // true if auto-pull succeeded
    Error   error  // non-nil if pull failed (conflicts)
}

// CheckStaleness fetches from remote and reports how far behind HEAD is.
// Safe to call when no upstream exists (returns not stale).
// Safe to call with uncommitted changes (skips pull, warns only).
func CheckStaleness(cwd string) StalenessResult
```

```go
// internal/agent/phase/orchestrator.go — updated

type OrchestratorOpts struct {
    // ... existing fields ...
    PipelineHint string // "ideate", "code", "auto", or "" (defaults to "auto")
}

// shouldIdeate determines whether to run the ideation pipeline.
// Returns true for hint="ideate", false for hint="code",
// and defers to complexity gate for hint="auto".
func shouldIdeate(hint string) bool
```

```go
// internal/spec/spec.go — updated

type SpecDocument struct {
    // ... existing fields ...
    Alternatives string // content of ## Alternatives section
}
```

## Edge Cases
- User sends `pipeline_hint: "code"` but no `--spec` — the orchestrator
  skips ideation and runs the existing single-agent spec creator as
  fallback (backward compatible)
- User sends `pipeline_hint: "ideate"` for a trivial change ("fix typo") —
  the ideation pipeline runs anyway (hint is explicit, overrides complexity)
- All 3 ideation agents produce identical approaches — the clarifier merges
  them into a single candidate; the planner has fewer options but still
  writes the spec
- An ideation agent times out — its candidates are excluded; pipeline
  continues with remaining agents' output. Minimum 1 agent must succeed.
- Clarifier produces questions but the message source is non-interactive
  (webhook) — questions are logged but not surfaced; clarifier resolves
  ambiguity autonomously using the safer interpretation
- Staleness check finds branch is 50+ commits behind — pull proceeds
  normally (no special handling for large deltas)
- Staleness check runs but no remote exists (offline, or no upstream set)
  — returns `Stale: false`, no error, no warning
- Staleness check finds uncommitted changes — skips pull, emits warning:
  "Branch is N commits behind upstream but has uncommitted changes —
  skipping auto-pull"
- Pull/rebase fails due to conflicts — emits `staleness_error`, orchestrator
  halts before coder phase, user must resolve manually
- Spec already exists for the feature (e.g., `--spec` provided alongside
  `pipeline_hint: "ideate"`) — ideation is skipped (spec takes precedence,
  same as today)
- Pipeline hint has invalid value (e.g., `"yolo"`) — treated as `"auto"`,
  logged as warning
- One ideation agent produces invalid JSON — its output is discarded with
  a warning; other agents' candidates are used
- Planner can't decide between two candidates — it must pick one anyway
  (the prompt mandates a selection). "Too close to call" is not a valid
  output.
- User interrupts mid-ideation (Ctrl+C) — same behavior as today: cancels
  the current turn, sub-agents receive cancelled context
