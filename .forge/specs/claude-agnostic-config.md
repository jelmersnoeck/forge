---
id: claude-agnostic-config
status: draft
---
# Remove Claude-specific configuration, use forge-native paths

## Description
Make forge configuration claude-agnostic. All `CLAUDE.md` becomes `AGENTS.md`.
All `.claude/` directory references become `.forge/`. The `ClaudeMD` type and
field are removed; its content merges into `AgentsMD`. Anthropic API
implementation stays unchanged — this is config/context only.

## Context
- `internal/types/types.go` — `ContextBundle`, `ClaudeMDEntry` type
- `internal/runtime/context/loader.go` — file discovery (`CLAUDE.md`, `.claude/`)
- `internal/runtime/context/loader_test.go` — tests for CLAUDE.md loading
- `internal/runtime/context/loader_agents_test.go` — AGENTS.md tests
- `internal/runtime/prompt/prompt.go` — `Assemble()`, system prompt blocks
- `internal/runtime/prompt/prompt_test.go` — prompt assembly tests
- `CLAUDE.md` — project-level instructions (becomes `AGENTS.md` content)
- `AGENTS.md` — existing learnings file

## Behavior
- `CLAUDE.md` at any level (user/project/parent/local) is no longer loaded
- `ClaudeMDEntry` type is removed; `ContextBundle.ClaudeMD` field is removed
- All content that was in `ClaudeMD` entries is now loaded as `AgentsMD` entries
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
- `CLAUDE.local.md` → `AGENTS.local.md` (already supported)
- Parent directory walk loads `AGENTS.md` (already does), stops loading `CLAUDE.md`
- `prompt.go` Assemble: uses `AgentsMD` for both project instructions and learnings in the same system-reminder block
- Reflect tool continues writing to `.forge/learnings/` (unchanged)
- `LoadSkillContent` searches `.forge/skills/` instead of `.claude/skills/`

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
// prompt.Assemble — merge what was ClaudeMD into AgentsMD handling
// The static block includes project instructions from AgentsMD
// The dynamic block includes learnings from AgentsMD
```

## Edge Cases
- Project has only `CLAUDE.md`, no `AGENTS.md` → `CLAUDE.md` is NOT loaded (breaking change — intentional)
- Project has `.claude/` but no `.forge/` → `.claude/` paths are used as fallback
- Project has both `.forge/settings.json` and `.claude/settings.json` → `.forge/` wins
- `AGENTS.md` at project root AND `.forge/AGENTS.md` → project root wins (same as current CLAUDE.md behavior)
- User home has `~/AGENTS.md` → loaded as user-level instructions (replaces `~/CLAUDE.md`)
- User home has `~/.forge/` but no `~/.claude/` → works fine
- `LoadSkillContent` checks `.forge/skills/` then `.claude/skills/` for backward compat
