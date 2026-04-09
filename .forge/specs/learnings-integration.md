---
id: learnings-integration
status: draft
---
# Improve learnings integration: auto-AGENTS.md, prompt guidance, learnings section

## Description
Learnings are written by the Reflect tool and loaded into context, but the
system prompt doesn't actively guide the agent to consult them. Additionally,
when no AGENTS.md exists in a project, the Reflect tool should auto-generate a
minimal one that references `.forge/learnings/` so future sessions (from any
tool) know to look there.

## Context
- `internal/tools/reflect.go` — Reflect tool writes `.forge/learnings/*.md`
- `internal/runtime/context/loader.go` — loads learnings into `ContextBundle.AgentsMD`
- `internal/runtime/prompt/prompt.go` — assembles system prompt; learnings in dynamic block
- `AGENTS.md` — project-level agent instructions; currently hand-written

## Behavior
1. **Auto-generate AGENTS.md on first Reflect call**: When the Reflect tool writes
   a learning and no `AGENTS.md` exists at the project root OR `.forge/AGENTS.md`,
   create a minimal `.forge/AGENTS.md` that contains:
   - A `# Agent Learnings` header
   - A note that `.forge/learnings/` contains actionable discoveries from past sessions
   - A directive for agents to consult learnings when they're relevant to the task
2. **Prompt guidance for learnings**: Enhance the system prompt (in `prompt.go`)
   to include a brief directive when learnings are present, instructing the agent to:
   - Scan learnings for relevance to the current task before diving in
   - If a learning directly relates to the work at hand, factor it into the approach
3. **Append learnings section to existing AGENTS.md**: If an `AGENTS.md` already
   exists but doesn't contain an `# Agent Learnings` section, append one that
   references the `.forge/learnings/` directory. This only happens during Reflect,
   not on every load.

## Constraints
- Do NOT modify a user's existing AGENTS.md content — only append
- Do NOT create AGENTS.md at project root if one already exists at `.forge/AGENTS.md`
- The auto-generated `.forge/AGENTS.md` should be minimal (< 10 lines)
- The prompt guidance should be 2-3 sentences max — not a wall of text
- The learnings section append is idempotent (check before appending)
- Include the auto-generated `.forge/AGENTS.md` in the git commit alongside the learning

## Interfaces

```go
// In reflect.go — called after writing the learning file
func ensureAgentsMD(cwd string) error
```

## Edge Cases
- AGENTS.md exists at root, `.forge/AGENTS.md` does not: append section to root AGENTS.md
- `.forge/AGENTS.md` exists, root does not: append section to `.forge/AGENTS.md`
- Both exist: append to root AGENTS.md (primary)
- Neither exists: create `.forge/AGENTS.md`
- AGENTS.md already has `# Agent Learnings` section: no-op
- Not a git repo: still create/update AGENTS.md, just don't commit
- CLAUDE.md exists (legacy): do NOT append to it; create `.forge/AGENTS.md` instead
