---
id: agent-phases
status: implemented
---
# Split agent loop into spec-creator, coder, and reviewer phases

## Description
Forge's current agent loop is a monolith that mixes spec creation, coding, and
reviewing into a single conversation. This spec splits them into three
composable phases — each with its own system prompt, tool set, and
personality — while introducing a "software engineer" orchestrator that chains
all three as forge's default mode.

## Context
Files and systems affected:

- `internal/agent/worker.go` — currently owns the one-loop-fits-all approach; must learn to run phase sequences
- `internal/agent/phase/` — NEW package: defines Phase interface + 3 built-in phases + orchestrator
- `internal/runtime/prompt/prompt.go` — base prompt is currently hardcoded; phases need their own system prompts
- `internal/runtime/loop/loop.go` — Loop stays as-is (generic agentic loop); phases configure it
- `internal/tools/registry.go` — Filtered() already exists; phases declare their allowed/disallowed tools
- `internal/review/` — existing reviewer code; reviewer phase delegates to this
- `internal/types/types.go` — may need PhaseResult or PhaseEvent types
- `cmd/forge/cli.go` — `--mode` flag to select phase or orchestrator
- `cmd/forge/commands.go` — possible new slash commands for phase switching
- `.forge/specs/` — the spec-creator phase writes here

## Behavior

### Phase definitions

Each phase is a self-contained configuration for a conversation loop:
system prompt, tool subset, model preference, max turns, and a completion
condition.

**1. Spec Creator** (`spec`)
- Purpose: analyze user request, explore the codebase, produce a high-quality
  feature spec.
- Tools: Read, Grep, Glob, WebSearch, Write (specs only), Bash (read-only
  commands). No Edit, no PRCreate, no Agent.
- Personality: product thinker + technical analyst. Asks clarifying questions.
  Calculates effort. Identifies customer benefits. Writes to `.forge/specs/`.
- Completion: a spec file exists in `.forge/specs/` with status `draft` or
  `active`.
- Max turns: 200 (generous — spec creation can involve significant exploration).

**2. Coder** (`code`)
- Purpose: take a spec and implement it flawlessly.
- Tools: all tools (Read, Write, Edit, Bash, Grep, Glob, WebSearch, PRCreate,
  Agent, Task*, Reflect, Queue*, UseMCPTool).
- Personality: expert coder. Thinks about abstraction, scaling, performance,
  data models, testing. Follows the spec precisely.
- Input: the spec (either from the previous phase or via `--spec`).
- Completion: the agent emits `done` and all tool executions are finished.
- Max turns: 0 (unlimited — coding can be extensive).

**3. Reviewer** (`review`)
- Purpose: review the implementation against the spec across multiple domains.
- Tools: Read, Grep, Glob, Bash (read-only). No Write, Edit, PRCreate.
- This is a multi-agent phase: spins up N reviewer sub-agents concurrently
  (security, code-quality, maintainability, operational, spec-validation).
- Input: git diff + spec.
- Completion: all reviewer sub-agents finish; findings aggregated.
- If actionable findings exist → feeds them back to the coder phase for
  remediation (creates a review→code feedback loop).

### Orchestrator: Software Engineer (`swe`)

The default mode. Chains all three phases:

```
  User prompt
      │
      ▼
  ┌─────────────────┐
  │  Spec Creator    │──▶ writes .forge/specs/<id>.md
  └────────┬────────┘
           │ spec
           ▼
  ┌─────────────────┐
  │  Coder           │──▶ implements the spec
  └────────┬────────┘
           │ diff
           ▼
  ┌─────────────────┐
  │  Reviewer        │──▶ parallel review agents
  └────────┬────────┘
           │
           ├─ no findings ──▶ done
           │
           └─ findings ──▶ back to Coder (max 2 review cycles)
```

### CLI flags

- `forge` — default, runs the `swe` orchestrator (all 3 phases)
- `forge --mode spec` — runs only the spec creator phase
- `forge --mode code` — runs only the coder phase (requires `--spec`)
- `forge --mode review` — runs only the reviewer phase
- `forge --mode swe` — explicit software engineer mode (same as default)
- `forge --spec path/to/spec.md` — in `swe` mode, skips the spec-creator phase

### Phase handoffs

- Spec Creator → Coder: the spec file path is passed to the coder's initial
  prompt (same as current `--spec` behavior).
- Coder → Reviewer: reviewer gets the git diff (committed changes) and the
  spec. Same mechanism as current `/review`.
- Reviewer → Coder (feedback loop): actionable findings formatted as a message,
  sent to a new coder loop iteration. Max 2 review cycles to prevent infinite
  loops.

### Events

Each phase emits standard OutboundEvents. New event types:
- `phase_start` — emitted when a phase begins (content: phase name)
- `phase_complete` — emitted when a phase finishes (content: phase name +
  summary)
- `phase_handoff` — emitted when one phase hands off to the next

### User interaction during phases

In `swe` mode, the user's initial prompt goes to the spec creator. Subsequent
user messages during the spec-creator phase are handled normally (conversation
continues). Once the spec is created, the orchestrator automatically transitions
to the coder phase — the user can still send messages, which get routed to
whichever phase is currently active.

## Constraints
- Do not remove or break the existing `/review` slash command — it must continue
  to work as a standalone review trigger.
- Do not modify the core `loop.Loop` — phases configure loops, they don't
  change the loop implementation.
- Each phase must be independently testable without the orchestrator.
- The reviewer phase must reuse the existing `internal/review/` package — no
  duplication.
- Phase system prompts must not include prompts from other phases (no bloat).
- A phase that's run standalone (`--mode spec`) must work end-to-end without
  requiring the other phases.
- Max review cycles in orchestrator: 2 (prevents infinite review→fix loops).
- The `--mode` flag is optional; omitting it defaults to `swe`.

## Interfaces

```go
// internal/agent/phase/phase.go

// Phase configures a conversation loop for a specific purpose.
type Phase struct {
    Name           string           // "spec", "code", "review"
    SystemPrompt   string           // phase-specific system prompt
    AllowedTools   []string         // tools this phase can use (empty = all)
    DisallowedTools []string        // tools this phase cannot use
    Model          string           // model override (empty = default)
    MaxTurns       int              // 0 = unlimited
}

// PhaseResult is the output of a completed phase.
type PhaseResult struct {
    Phase     string   // phase name
    SpecPath  string   // path to spec file (spec-creator output)
    Diff      string   // git diff (coder output)
    Findings  []review.Finding // review findings (reviewer output)
}
```

```go
// internal/agent/phase/orchestrator.go

// Orchestrator chains phases together.
type Orchestrator struct {
    phases       []Phase
    maxReviewCycles int
}

func NewSWEOrchestrator() *Orchestrator
func (o *Orchestrator) Run(ctx context.Context, opts OrchestratorOpts) error

type OrchestratorOpts struct {
    Provider     types.LLMProvider
    Registry     *tools.Registry
    Bundle       types.ContextBundle
    CWD          string
    SessionStore *session.Store
    SessionID    string
    Emit         func(types.OutboundEvent)
    InitialPrompt string
    SpecPath     string // if set, skip spec-creator phase
}
```

```go
// internal/agent/phase/prompts.go

func SpecCreatorPrompt() string   // product thinking + code analysis
func CoderPrompt() string         // implementation-focused
func ReviewerPrompt() string      // review coordination
```

## Edge Cases
- User sends `--mode code` without `--spec` → error: "coder mode requires a
  spec (use --spec path/to/spec.md)"
- User sends `--mode review` with no git diff → early exit with "nothing to
  review" message
- Spec creator can't produce a spec within max turns → orchestrator stops and
  surfaces the partial state to the user
- Reviewer finds no issues → orchestrator skips feedback loop, moves to done
- Reviewer finds issues but max review cycles (2) reached → orchestrator emits
  warning and completes anyway
- User interrupts mid-phase → same interrupt behavior as today (Ctrl+C cancels
  the current turn, twice exits)
- Network error in one reviewer sub-agent → same as today: other reviewers
  continue, error logged
- User provides both `--mode spec` and `--spec` → error: "cannot use --spec
  with --mode spec (spec creator writes specs, it doesn't consume them)"
- Existing `/review` command → continues to work, triggers only the review
  phase (backward compatible)
- `--mode swe` with `--spec` → skips spec-creator, starts with coder phase
  directly
