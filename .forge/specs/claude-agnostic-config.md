---
id: claude-agnostic-config
status: implemented
---
# Remove Claude-specific configuration, use forge-native paths

## Description
Make forge configuration claude-agnostic. All `CLAUDE.md` becomes `AGENTS.md`.
All `.claude/` directory references become `.forge/`. The `ClaudeMD` type and
field are removed; its content merges into `AgentsMD`. Anthropic API
implementation stays unchanged — this is config/context only.

## Context
- `internal/types/types.go` — `ContextBundle.ClaudeMD` field and `ClaudeMDEntry` type removed
- `internal/runtime/context/loader.go` — file discovery rewritten: .forge/ preferred, .claude/ fallback
- `internal/runtime/context/loader_test.go` — tests rewritten for AGENTS.md and .forge/ paths
- `internal/runtime/context/loader_agents_test.go` — updated .claude dir test to use .forge
- `internal/runtime/prompt/prompt.go` — `Assemble()` splits AgentsMD into instructions vs learnings
- `internal/runtime/prompt/prompt_test.go` — tests updated for AgentsMD-only world
- `CLAUDE.md` → deleted, content merged into `AGENTS.md`
- `AGENTS.md` — now contains project docs + learnings (single file)
- `mcp-server/src/server.ts` — resource URI forge://claude → forge://agents
- `mcp-server/README.md` — updated resource docs
- `README.md`, `CACHE_STRATEGY.md`, `CACHE_IMPROVEMENTS.md`, `IMPLEMENTATION-COMPLETE.md` — refs updated
- `.forge/specs/forge-architecture.md` — updated context loader description
- `docs/` — multiple doc files updated

## Behavior
- `CLAUDE.md` at any level (user/project/parent/local) is still loaded into `AgentsMD`
- `ClaudeMDEntry` type is removed; `ContextBundle.ClaudeMD` field is removed
- All content that was in `ClaudeMD` entries is now loaded as `AgentsMD` entries
- Both `AGENTS.md` and `CLAUDE.md` are loaded when present (both go into `AgentsMD`)
- `.claude/` directory references become `.forge/`:
  - `~/.claude/rules/` → `~/.forge/rules/`
  - `~/.claude/settings.json` → `~/.forge/settings.json`
  - `.claude/settings.json` → `.forge/settings.json`
  - `.claude/settings.local.json` → `.forge/settings.local.json`
  - `.claude/skills/` → `.forge/skills/`
  - `.claude/agents/` → `.forge/agents/`
  - `.claude/CLAUDE.md` fallback → removed (use `AGENTS.md` at project root)
  - `.claude/AGENTS.md` fallback → `.forge/AGENTS.md` fallback
- Backward compat: also check legacy `.claude/` paths as fallback, but prefer `.forge/`
- `CLAUDE.local.md` also loaded alongside `AGENTS.local.md`
- `~/CLAUDE.md` also loaded alongside `~/AGENTS.md`
- Parent directory walk loads both `AGENTS.md` and `CLAUDE.md`
- `prompt.go` Assemble: splits AgentsMD into instructions (static block) and learnings (dynamic block) based on `.forge/learnings/` path prefix
- Reflect tool continues writing to `.forge/learnings/` (unchanged)
- `LoadSkillContent` searches `.forge/skills/` then `.claude/skills/` for backward compat
- `~/CLAUDE.md` is no longer loaded; replaced by `~/AGENTS.md`

## Constraints
- Do NOT change the Anthropic API implementation (provider, models, etc.)
- Do NOT change `internal/agent/worker.go` model defaults (those are Anthropic model IDs, not config)
- Do NOT change `internal/runtime/compact/engine.go` model defaults
- Do NOT remove backward compat for `.claude/` directory (check `.forge/` first, fall back to `.claude/`)
- The `CLAUDE.md` file in this repo must be deleted, its content merged into `AGENTS.md`
- Package doc comment in loader.go must be updated

## Interfaces

```go
// ContextBundle — remove ClaudeMD, keep AgentsMD
type ContextBundle struct {
    AgentsMD          []AgentsMDEntry
    Rules             []RuleEntry
    SkillDescriptions []SkillDescription
    AgentDefinitions  map[string]AgentDefinition
    Specs             []SpecEntry
    Settings          MergedSettings
}

// ClaudeMDEntry — deleted entirely
// AgentsMDEntry — unchanged, now carries both instructions and learnings
```

```go
// prompt.Assemble — instructions go in static block, learnings in dynamic
// Split based on path: entries from .forge/learnings/ → dynamic block
// Everything else (AGENTS.md at root, .forge/AGENTS.md, etc.) → static block
```

## Edge Cases
- Project has only `CLAUDE.md`, no `AGENTS.md` → `CLAUDE.md` is loaded into `AgentsMD`
- Project has both `AGENTS.md` and `CLAUDE.md` → both loaded into `AgentsMD`
- Project has `.claude/` but no `.forge/` → `.claude/` paths are used as fallback
- Project has both `.forge/settings.json` and `.claude/settings.json` → `.forge/` wins
- `AGENTS.md` at project root AND `.forge/AGENTS.md` → project root wins (same as current CLAUDE.md behavior)
- User home has `~/AGENTS.md` and `~/CLAUDE.md` → both loaded as user-level
- User home has `~/.forge/` but no `~/.claude/` → works fine
- `LoadSkillContent` checks `.forge/skills/` then `.claude/skills/` for backward compat
