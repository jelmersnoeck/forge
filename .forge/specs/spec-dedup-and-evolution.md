---
id: spec-dedup-and-evolution
status: implemented
---
# Spec agent checks for existing specs before creating new ones

## Description
When a session starts and the spec-creator (or planner) phase runs, the agent
must check existing specs for relevance before writing a new one. If an existing
spec covers the same feature area, the agent updates it in place rather than
creating a new file. This makes specs a progressive, evolving source of truth
instead of a sprawling archive of point-in-time snapshots.

## Context
- `internal/agent/phase/prompts.go` — `specCreatorPrompt`, `plannerPrompt` (system prompts with dedup instructions)
- `internal/agent/phase/phase.go` — `SpecCreator()` (Edit removed from DisallowedTools)
- `internal/agent/phase/phase_test.go` — tests for SpecCreator phase config
- `internal/agent/phase/debate.go` — `runPlanner()`, `buildContextSummary()` (includes implemented specs for dedup)
- `internal/agent/phase/debate_test.go` — tests for buildContextSummary with implemented specs
- `internal/runtime/prompt/prompt.go` — `specPrompt`, `BuildSpecIndex()` (spec index injection)
- `internal/runtime/prompt/prompt_test.go` — tests for BuildSpecIndex()
- `internal/spec/spec.go` — `LoadSpecs()`, `ParseSpec()` (spec loading and parsing)
- `internal/spec/spec_test.go` — tests for LoadSpecs
- `internal/types/types.go` — `SpecEntry` (added Summary field)
- `.forge/specs/` — existing spec files

## Behavior
- Before writing a new spec, the spec agent reads existing specs in `.forge/specs/` and evaluates relevance to the current request.
- The agent compares the user's request against each existing spec's ID, header, and description to determine overlap.
- If an existing spec covers the same feature area (same subsystem, same capability, same user-facing behavior), the agent updates that spec in place using the Edit tool rather than creating a new file.
- When updating an existing spec, the agent preserves the original ID and file path, updates the status back to `active` (or `draft` if it was `implemented`), and amends sections as needed.
- If the request is genuinely new — different subsystem, unrelated capability — the agent creates a new spec as before.
- If the request extends or builds on an existing spec but is substantially different in scope, the agent creates a new spec and references the related spec in its Description section.
- The spec agent's final output always states whether it created a new spec or updated an existing one, and why.
- The planner agent (ideation pipeline) follows the same dedup logic: checks existing specs before writing, updates when appropriate.
- The existing spec inventory (all specs, not just active ones) is provided to the spec agent via the system prompt so it can make an informed decision without needing to Glob/Read every spec file manually.

## Constraints
- Must not change the spec file format or frontmatter schema.
- Must not remove or archive existing specs automatically — only update them.
- Must not merge two existing specs together — scope is limited to "update existing" vs "create new."
- The spec agent must not need to Read every spec file to decide — a summary index in the prompt is sufficient for the decision.
- Must not increase the system prompt size by more than ~2000 tokens for the spec index.
- The Edit tool must be available to the spec-creator phase (currently disallowed).

## Interfaces

The spec-creator phase tool restrictions in `internal/agent/phase/phase.go`:

```go
// SpecCreator returns the spec-creator phase configuration.
func SpecCreator() Phase {
	return Phase{
		Name: "spec",
		DisallowedTools: []string{
			"Agent", "AgentGet", "AgentList", "AgentStop",
			"TaskCreate", "TaskGet", "TaskList", "TaskStop", "TaskOutput",
			"QueueImmediate", "QueueOnComplete",
			"UseMCPTool",
		},
		MaxTurns: 200,
	}
}
```

The `BuildSpecIndex()` function in `internal/runtime/prompt/prompt.go`:

```go
func BuildSpecIndex(specs []types.SpecEntry) string
```

The `SpecEntry.Summary` field added in `internal/types/types.go`:

```go
type SpecEntry struct {
	ID      string
	Status  string
	Path    string
	Content string
	Summary string // first H1 heading from spec content
}
```

The spec index format injected into the system prompt:

```markdown
## Existing Specs

Review these before creating a new spec:

- **agent-phases** (implemented): Split agent loop into spec-creator, coder, and reviewer phases
- **automated-review** (active): Automated multi-agent code review system
...
```

Updated `specCreatorPrompt` in `internal/agent/phase/prompts.go` includes a
"Spec Deduplication" section instructing the agent to check the spec index.

Updated `plannerPrompt` in `internal/agent/phase/prompts.go` includes the
same dedup instructions.

`buildContextSummary()` in `internal/agent/phase/debate.go` now includes
implemented specs alongside active/draft specs for dedup visibility.

## Edge Cases
- User request maps to a `superseded` spec — agent creates a new spec (superseded specs are dead).
- User request maps to an `implemented` spec — agent updates it, sets status back to `active`.
- User request spans two existing specs — agent picks the most relevant one and updates it; mentions the other in Description.
- User request is ambiguous (could be update or new) — agent defaults to creating new (safer to have one extra spec than to corrupt an existing one).
- Spec index is empty (brand new project) — agent creates new spec as before; no dedup check needed. `BuildSpecIndex()` returns empty string.
- Existing spec has an Alternatives section from ideation — preserved during update.
- The planner receives spec index via `buildContextSummary()` — already includes active/draft specs; now also includes implemented specs for dedup.
- Specs without an H1 heading — `BuildSpecIndex()` uses the spec ID as fallback summary text.
- `BuildSpecIndex()` sorts specs alphabetically by ID for stable output.
