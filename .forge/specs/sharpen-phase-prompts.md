---
id: sharpen-phase-prompts
status: implemented
---
# Sharpen spec-creator and coder phase system prompts

## Description
The spec-creator and coder phase prompts are too generic compared to the
reviewer sub-agent prompts, which have clear focus areas, severity guides,
and concrete instructions. Beef up both phase prompts with specific,
actionable guidance so the agents produce better output without relying
solely on project-level AGENTS.md for direction. Effort: S

## Context
- `internal/agent/phase/prompts.go` — `specCreatorPrompt`, `coderPrompt`
- `internal/agent/phase/phase_test.go` — prompt content assertions
- `internal/runtime/prompt/prompt.go` — `Assemble()` merges phase prompts into system blocks
- `internal/review/reviewers.go` — exemplar: well-structured reviewer prompts with focus areas

The base prompt (`basePrompt`) and spec-driven development prompt (`specPrompt`)
in `prompt.go` are **not in scope** — they apply to all agents and should stay generic.
Project-specific conventions (Go style, test patterns, shell scripting rules) come
from AGENTS.md / `.forge/rules/` and are already injected by the context loader.

## Behavior
- Spec creator prompt includes explicit exploration strategy:
  - Read AGENTS.md and project structure first
  - Targeted exploration: grep for related types/functions, read key files, stop
  - Time-box exploration: don't read more than ~15-20 files before writing
  - When in doubt about scope, prefer smaller scope with clear extension points
- Spec creator prompt includes quality gates for each spec section:
  - Context: must list concrete file paths discovered during exploration
  - Behavior: each point phrased as a testable assertion
  - Constraints: each point falsifiable ("must not X" not "be careful with X")
  - Edge Cases: minimum 3, each with scenario + expected outcome
  - Interfaces: code blocks with actual type signatures, not prose descriptions
- Coder prompt includes explicit workflow steps:
  - Read the spec, then read every file listed in Context
  - Plan architecture in a think block before writing code
  - Implement in dependency order (types → core logic → integration → tests)
  - Run tests after each logical unit, not just at the end
  - Run linter/formatter before declaring done
  - Commit with meaningful messages tied to what changed
- Coder prompt includes anti-patterns to avoid:
  - Don't gold-plate: implement the spec, not adjacent features
  - Don't leave broken intermediate states: each commit should compile + pass tests
  - Don't skip error paths: every error return needs a test
  - Don't copy-paste: extract shared logic immediately, not "refactor later"
- Coder prompt includes testing mandate:
  - Test every Behavior point from the spec
  - Test every Edge Case from the spec
  - Test error paths explicitly
  - Prefer real filesystem/exec over mocks
- Existing tests in `phase_test.go` still pass (prompt content assertions may need updating)

## Constraints
- Do not modify `basePrompt` or `specPrompt` in `prompt.go`
- Do not modify reviewer prompts in `reviewers.go`
- Do not add project-specific conventions (Go, shell, etc.) to phase prompts —
  those belong in AGENTS.md / `.forge/rules/`
- Keep prompts under ~2KB each to avoid bloating the system prompt token count.
  Final sizes: specCreator 1648 bytes (~412 tokens), coder 1688 bytes (~422 tokens).
- Phase prompts should be language-agnostic (the agent works on any codebase)
- Do not duplicate the spec format already present in `specPrompt` — the spec
  creator prompt should reference it, not redefine it

## Interfaces
No new types or signatures. Changes are prompt string constants only.

```go
// Same constants, better content:
const specCreatorPrompt = `...`  // in internal/agent/phase/prompts.go
const coderPrompt = `...`        // in internal/agent/phase/prompts.go
```

## Edge Cases
- Spec creator prompt had a duplicate spec format definition (already in base
  `specPrompt`). Removed it — prompt now references the format without redefining.
- First draft exceeded 2KB; trimmed by condensing prose while keeping all
  actionable guidance. Final versions are information-dense, not wordy.
- Coder prompt must not conflict with base prompt's response format rules. Phase
  prompt adds *implementation methodology*, base prompt handles communication style.
- If AGENTS.md has conflicting advice (e.g., "always use mocks"), the phase prompt
  should lose — project-level config takes precedence. Phase prompts should use
  "prefer" language, not absolute mandates, for anything that might be project-specific.
