---
id: learnings-directory
status: implemented
---
# Move learnings from AGENTS.md to .forge/learnings/

## Description
The Reflect tool currently appends learnings to `AGENTS.md` in the project root.
This changes it to write individual learning files into `.forge/learnings/` and
marks that directory as generated in `.gitattributes`. AGENTS.md remains loaded
as context but is never written to by the agent.

## Context
- `internal/tools/reflect.go` — Reflect tool handler (writes learnings)
- `internal/tools/reflect_test.go` — tests for reflect tool
- `internal/runtime/context/loader.go` — loads AGENTS.md and learnings into ContextBundle
- `internal/runtime/context/loader_agents_test.go` — loader tests
- `internal/runtime/prompt/prompt.go` — assembles learnings into system prompt
- `internal/runtime/prompt/prompt_test.go` — prompt tests
- `internal/types/types.go` — ContextBundle, AgentsMDEntry types

## Behavior
- Reflect tool writes each reflection as an individual `.md` file in `<CWD>/.forge/learnings/`.
- Filename format: `<timestamp>-<slugified-summary>.md` (e.g. `20260404-134500-implemented-feature-x.md`).
- The `.forge/learnings/` directory is created on first write if missing.
- On first write, ensure `.gitattributes` contains a line marking `.forge/learnings/**` as `linguist-generated=true`.
- If `.gitattributes` already has the line, don't duplicate it.
- The context loader discovers all `.md` files in `.forge/learnings/` and loads them as learnings (level "project").
- AGENTS.md files continue to be loaded as context (read-only) — they just aren't written to.
- The base prompt's self-improvement line changes from "AGENTS.md" to ".forge/learnings/".
- The Reflect tool description updates to reference `.forge/learnings/` not `AGENTS.md`.

## Constraints
- Do NOT remove AGENTS.md reading from the context loader — it's still valid input.
- Do NOT write to AGENTS.md from the Reflect tool.
- Do NOT break existing AGENTS.md loading (user, parent, project, local levels).
- Individual learning files, not one big append file — keeps git diffs clean.
- The `.gitattributes` update is idempotent.

## Interfaces
```go
// File path for a learning: .forge/learnings/20260404-134500-implemented-feature-x.md
// Slugification: lowercase, non-alphanum replaced with hyphens, trimmed, max ~50 chars

// No new types needed — learning files loaded as AgentsMDEntry with Level "project"
```

## Edge Cases
- CWD has no `.forge/` directory → created automatically.
- CWD has no `.gitattributes` → created with the single line.
- `.gitattributes` exists but has no forge learnings line → line appended.
- `.gitattributes` already has the line → no change.
- Summary contains special characters / is very long → slugified and truncated.
- Multiple reflections in same second → filename collision resolved with suffix or uniqueness from summary.
- Empty summary → rejected (existing behavior).
