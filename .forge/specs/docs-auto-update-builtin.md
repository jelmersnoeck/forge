---
id: docs-auto-update-builtin
status: draft
---
# Bake documentation auto-update into agent core behavior

## Description
Move the documentation auto-update behavior from a forge-specific project rule
(`.forge/rules/docs-auto-update.md`) into the agent's core coder phase prompt,
making it a universal behavior across all sessions regardless of repository.

## Context
- `internal/runtime/prompt/prompt.go` — base and spec prompts assembled here
- `internal/agent/phase/prompts.go` — phase-specific prompts (`coderPrompt`)
- `internal/agent/phase/prompts.go:PromptForPhase()` — dispatch function
- `internal/runtime/prompt/prompt_test.go` — tests for prompt assembly
- `.forge/rules/docs-auto-update.md` — current forge-specific rule (to be deleted)

## Behavior
1. The coder phase prompt (`coderPrompt` in `internal/agent/phase/prompts.go`)
   includes a "Documentation" section instructing the agent to update project
   documentation after code changes.
2. The instruction is generalized — not forge-specific. It tells the agent to:
   - After implementation, check `git diff --stat` for what changed
   - Identify project documentation files (README.md, AGENTS.md, CONTRIBUTING.md,
     docs/ directory, any relevant `.md` at project root)
   - Update only the sections affected by the changes
   - Commit documentation updates as a separate commit
   - Skip doc updates for read-only sessions, spec/learning-only changes,
     or pure test-only changes that don't affect documented behavior
   - Explicitly state "No doc updates needed" when skipping
3. The forge-specific `.forge/rules/docs-auto-update.md` is deleted.
4. The base prompt (`basePrompt`) and spec prompt (`specPrompt`) are NOT modified.

## Constraints
- Must not add token overhead to non-coder phases (spec, review, QA, ideate, etc.)
- Must not reference forge-specific files (AGENTS.md sections like "Architecture",
  "Repository layout", etc.) — keep it generic
- Must not break existing prompt cache behavior (no structural changes to system
  blocks)
- Must not duplicate the existing spec-reconciliation instruction already in
  `specPrompt`

## Interfaces
The change is entirely in string constants — no type signatures change.

```go
// internal/agent/phase/prompts.go
const coderPrompt = `... existing content ...

## Documentation

After implementation is complete and all tests pass:

1. Run ` + "`git diff main...HEAD --stat`" + ` (or equivalent) to see what changed.
2. Identify documentation files in the project root and docs directories
   (README.md, AGENTS.md, CONTRIBUTING.md, etc.).
3. For each documentation file, review sections relevant to the code changes.
   Update architecture, API, configuration, or usage sections as needed.
4. Commit documentation changes separately:
   ` + "`" + `git add <docs> && git commit -m "docs: update documentation"` + "`" + `
5. If no documentation updates are needed, state explicitly: "No doc updates
   needed — changes don't affect documented behavior."

Skip documentation updates for:
- Read-only sessions (investigation, code review)
- Changes only to specs or learnings
- Pure test-only changes that don't affect documented behavior`
```

## Edge Cases
1. **Project has no documentation files** — agent states "No doc updates needed"
   and moves on. No error.
2. **Changes are test-only** — agent detects test-only files in diff stat and
   skips docs update. States skip reason.
3. **Multiple documentation files affected** — agent updates each relevant file
   and includes all in a single docs commit.
4. **Agent is running in non-orchestrated mode (plain loop)** — the docs-update
   instruction is only in the coder phase prompt, which is injected via
   `PromptForPhase("code")`. Plain loop mode doesn't inject phase prompts, so
   this instruction does NOT apply there. This is acceptable because
   plain loop mode is for follow-up messages after the orchestrator completes.
