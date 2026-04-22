---
id: docs-auto-update
status: draft
---
# Auto-update AGENTS.md and README.md after every coding session

## Description
After every coding session that changes code, the agent must review and update
AGENTS.md and README.md to reflect the changes, then commit the documentation
updates as a separate commit. This ensures project documentation never drifts
from the codebase.

## Context
- `AGENTS.md` — project-level agent instructions, architecture docs, key files,
  repository layout, gotchas, learnings, conventions
- `README.md` — user-facing project overview, features, architecture diagrams,
  tools table, API endpoints, environment variables, project structure
- `.forge/rules/` — directory for rules loaded into system prompt by
  `internal/runtime/context/loader.go` via `loadRules()`
- `internal/runtime/prompt/prompt.go` — assembles rules into system prompt blocks
- `~/CLAUDE.md` — user-level instructions including "Final Handoff" section

## Behavior
- After completing implementation work (and before final handoff), the agent
  reviews changes made during the session.
- Agent checks whether AGENTS.md sections are still accurate: Architecture,
  Repository layout, Key files, Build & run, Environment variables, Gotchas,
  Conventions.
- Agent checks whether README.md sections are still accurate: Features, Project
  Structure, Tools table, API Endpoints, Environment Variables.
- If either file needs updates, the agent edits them to reflect the session's
  changes.
- Documentation updates are committed as a separate commit with message
  `docs: update AGENTS.md and README.md`.
- If no documentation changes are needed (e.g. refactor with no architectural
  impact), the agent skips the commit but explicitly states "no doc updates
  needed."

## Constraints
- Must not rewrite entire files — only update sections affected by the session's
  changes.
- Must not add speculative documentation for unimplemented features.
- Must not remove existing sections without explicit reason.
- The documentation commit must be separate from implementation commits.
- Must not update docs if the session only involved reading/reviewing code
  (no code changes).

## Interfaces
Implementation is a `.forge/rules/` markdown file — no code changes required.

```
.forge/rules/docs-auto-update.md
```

The rule is plain markdown, loaded by `loadRules()` and injected into the system
prompt's "Additional rules and guidelines" block.

## Edge Cases
- **Session only adds tests**: Agent checks if new test patterns or requirements
  should be documented in AGENTS.md Testing section. Usually no update needed.
- **Session adds a new tool**: Both AGENTS.md (Key files, tool list) and
  README.md (Tools table) must be updated.
- **Session changes environment variables**: Both files have env var sections
  that must stay in sync.
- **Session is read-only review**: No doc update commit. Agent states "no doc
  updates needed."
- **Concurrent agent edits**: Agent runs `git diff` before editing docs to
  avoid conflicts with other agents' changes.
