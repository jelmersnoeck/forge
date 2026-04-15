---
id: intent-classification
status: implemented
---
# Classify user intent to route between Q&A and SWE pipeline

## Description
Forge currently routes every prompt through the SWE pipeline (spec → code →
review). When a user asks an informational question ("how does the caching
work?", "what files handle MCP?"), the spec-creator phase runs anyway and tries
to produce a spec — wasting tokens and confusing the user. This spec adds an
intent classification step that distinguishes "informational" prompts from
"actionable" ones, and introduces a lightweight Q&A mode that can transition
into the SWE pipeline when the user follows up with an implementation request.

## Context
- `internal/agent/worker.go` — `Run()` message loop, `runOrchestrator()`, `orchestratorDone` flag, `historyID` tracking, new `qaHistoryID`/`qaActive` state
- `internal/agent/phase/orchestrator.go` — `Orchestrator.Run()` returns `OrchestratorResult`, `runQA()`, `runSWEPipeline()`, prompt augmentation for Q&A→task transition
- `internal/agent/phase/phase.go` — `Phase` struct, `SpecCreator()`, `Coder()`, `QA()`, `Reviewer()`
- `internal/agent/phase/prompts.go` — `specCreatorPrompt`, `coderPrompt`, `qaPrompt`, `PromptForPhase()`
- `internal/agent/phase/classify.go` — `Intent`, `ClassifyIntent()`, `parseIntent()`
- `internal/agent/phase/classify_test.go` — classification unit tests
- `internal/runtime/loop/loop.go` — `Loop.Send()`, `Loop.Resume()`, `Loop.HistoryID()` (unchanged, used by Q&A)
- `internal/runtime/prompt/prompt.go` — `Assemble()`, `basePrompt`, `specPrompt` (unchanged)
- `internal/types/types.go` — `OutboundEvent` type docs updated with `intent_classified`
- `cmd/forge/cli.go` — `handleEvent()` updated with `intent_classified` case

## Behavior

### Intent classification
- Before entering the SWE pipeline, the orchestrator classifies the user's
  initial prompt into one of two intents: `question` or `task`.
- Classification is performed by the LLM via a lightweight, zero-tool call: a
  short system prompt + the user's message, asking the model to respond with
  a JSON object: `{"intent": "question"}` or `{"intent": "task"}`.
- The classification call uses a small/fast model (e.g., `claude-haiku-4-20250414`)
  to minimize latency and cost — same pattern as the existing session-naming call.
- If classification fails (network error, parse error, ambiguous), default to
  `task` — this preserves current behavior and is the safer fallback.
- Classification is **only** performed in `swe` mode (the default). Explicit
  `--mode spec`, `--mode code`, and `--mode review` bypass classification
  entirely — the user already declared intent.
- If `--spec` is provided, skip classification — the user's intent is
  unambiguously `task`.

### Q&A mode (question intent)
- When classified as `question`, the orchestrator runs a single conversation
  loop with a Q&A-specific system prompt and the full tool set minus mutating
  tools (no Write, Edit, PRCreate — same restriction philosophy as the
  spec-creator phase but without the mandate to produce a spec).
- The Q&A phase uses the same model as the rest of the session (not Haiku).
- The Q&A phase has a max turns of 200 (same as spec-creator — exploration
  can be extensive).
- The Q&A loop runs, the agent answers the question, emits `done`.
- **The historyID from the Q&A loop is preserved** so follow-up messages
  carry full conversation context.

### Transition: question → task
- After a Q&A turn completes, subsequent user messages go through
  classification again. If the follow-up is classified as `task`, the
  orchestrator launches the SWE pipeline.
- The SWE pipeline's spec-creator phase receives a prompt that includes context
  from the previous Q&A conversation: "Based on our previous discussion, the
  user now wants to implement: {user message}. Use the context from the
  conversation to inform the spec."
- The spec-creator runs as a **new loop** (fresh history) but the prompt
  references the prior Q&A. The Q&A history is NOT loaded into the spec-creator
  loop — that would bloat the context. Instead, a summary reference is enough
  since the spec-creator can re-read any files the Q&A phase explored.
- After the SWE pipeline finishes, the session behaves as it does today:
  subsequent messages go to the plain loop.

### Multiple Q&A rounds
- If the user asks multiple questions in a row (all classified as `question`),
  each one resumes the same Q&A loop (using `Loop.Resume()` with the preserved
  historyID). This maintains conversation context across questions.
- There is no limit on the number of Q&A rounds before transitioning to a task.

### Events
- New event: `intent_classified` — emitted after classification with content
  `"question"` or `"task"`. Allows the CLI to display what mode the agent is in.
- The Q&A phase emits standard events (`thinking`, `text`, `tool_use`, `done`).
- No new `phase_start`/`phase_complete` events for Q&A — it's not a "phase" in
  the SWE pipeline sense; it's a pre-pipeline interaction mode.

### CLI display
- When an `intent_classified` event arrives with `"question"`, the CLI shows a
  subtle indicator (e.g., dimmed text: "answering question...").
- When classified as `"task"`, the existing phase progress display kicks in as
  it does today.

## Constraints
- Classification must add <500ms of latency for typical prompts (Haiku is fast
  enough for this).
- Classification must not consume more than ~200 input tokens + ~20 output tokens
  per call.
- Do not modify the core `loop.Loop` implementation — the Q&A mode uses the
  existing loop, just with different configuration.
- Do not change behavior when `--mode` is explicitly set — classification only
  applies to the default `swe` mode.
- Do not remove or break existing `--mode spec`, `--mode code`, `--mode review`
  behavior.
- The Q&A phase must not write specs, create PRs, or make file edits. Its tool
  restrictions are: disallow Write, Edit, PRCreate, Agent, AgentGet, AgentList,
  AgentStop, TaskCreate, TaskGet, TaskList, TaskStop, TaskOutput,
  QueueImmediate, QueueOnComplete, UseMCPTool, Reflect.
- If the ANTHROPIC_API_KEY is not set (e.g., using Claude CLI provider),
  classification should still work — use whatever provider is active, just with
  a cheaper model parameter if possible.

## Interfaces

```go
// internal/agent/phase/classify.go

// Intent represents the classified user intent.
type Intent string

const (
    IntentQuestion Intent = "question"
    IntentTask     Intent = "task"
)

// ClassifyIntent uses a lightweight LLM call to determine whether the user's
// prompt is an informational question or an actionable task request.
// Returns IntentTask on any error (safe default).
func ClassifyIntent(ctx context.Context, provider types.LLMProvider, prompt string) Intent
```

```go
// internal/agent/phase/phase.go

// QA returns the Q&A phase configuration.
func QA() Phase
```

```go
// internal/agent/phase/prompts.go

// qaPrompt is the system prompt for the Q&A phase.
const qaPrompt = `...`

func PromptForPhase(name string) string // updated to handle "qa"
```

```go
// internal/agent/phase/orchestrator.go — updated Run signature stays the same,
// but internal logic adds classification step before phase selection.

// OrchestratorOpts gains QAHistoryID field.
type OrchestratorOpts struct {
    // ... existing fields ...
    QAHistoryID string // resumes existing Q&A conversation when set
}

// OrchestratorResult is the return value from Orchestrator.Run.
type OrchestratorResult struct {
    // Intent is the classified intent for this run.
    Intent Intent
    // QAHistoryID is set when Intent == IntentQuestion.
    // The caller uses this to resume the Q&A loop on follow-up.
    QAHistoryID string
}

// Run returns OrchestratorResult instead of error.
func (o *Orchestrator) Run(ctx context.Context, opts OrchestratorOpts) (OrchestratorResult, error)
```

```go
// internal/agent/worker.go — updated to handle Q&A state

// Worker tracks Q&A state:
// - qaHistoryID string: preserved across Q&A rounds for Resume()
// - qaActive bool: true while in Q&A mode (not yet transitioned to task)
```

## Edge Cases
- User sends a prompt that's ambiguous ("the caching could be improved") —
  classification may return either intent. Defaulting to `task` is acceptable;
  the user can always just answer the spec-creator's questions to clarify.
- User sends an empty prompt — skip classification, let the existing loop
  handle it (it'll ask for input).
- User sends a very long prompt (>4000 tokens) — classification should still
  work; the prompt is truncated to the first ~1000 chars for the classification
  call to stay within the ~200 input token budget. The full prompt goes to
  whichever phase runs.
- Classification LLM returns unexpected JSON / garbage — parse error → default
  to `task`.
- User explicitly sends "implement X" after several Q&A rounds — classified as
  `task`, SWE pipeline launches, Q&A context referenced in spec-creator prompt.
- User in Q&A mode uses Ctrl+C to interrupt — same behavior as today (cancels
  current turn, not the session). Q&A historyID preserved for next message.
- Claude CLI provider is active (no ANTHROPIC_API_KEY) — classification uses
  the CLI provider. Model parameter for classification may be ignored by CLI
  (it uses its own model selection), which is acceptable — classification
  prompt is simple enough for any model.
- `--mode swe` explicit — same as default, classification runs.
- Network error during classification — default to `task`, log warning.
- User starts with a question, gets answer, then sends another question, then
  finally sends a task — Q&A history accumulates across question rounds, SWE
  pipeline launches fresh with reference to prior discussion.
