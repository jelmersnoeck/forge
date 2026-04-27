---
id: agents-md-location
status: implemented
---
# Auto-created AGENTS.md goes to project root, respects CLAUDE.md

## Description
When the Reflect tool auto-creates an AGENTS.md (for the learnings section),
it should create it at the project root instead of `.forge/AGENTS.md`. If a
top-level `CLAUDE.md` already exists, skip creation entirely since CLAUDE.md
serves as the agents file.

## Context
- `internal/tools/reflect.go` — `ensureAgentsMD()` function
- `internal/tools/reflect_test.go` — `TestEnsureAgentsMD`, `TestReflectTool`, `TestReflectCommitsAgentsMD`

## Behavior
- When no AGENTS.md exists anywhere and no CLAUDE.md exists: create `AGENTS.md` at project root (not `.forge/AGENTS.md`).
- When a root `AGENTS.md` exists without the learnings section: append section to it.
- When `.forge/AGENTS.md` exists without the learnings section: append section to it (backward compat for existing projects).
- When a top-level `CLAUDE.md` exists but no AGENTS.md exists: do nothing (return empty path, no error).
- When any AGENTS.md already has `# Agent Learnings`: noop.

## Constraints
- Must not create `.forge/AGENTS.md` when nothing exists — root is the target.
- Must not create AGENTS.md when CLAUDE.md is present at root.
- Must not break existing projects that already have `.forge/AGENTS.md`.

## Interfaces
```go
// ensureAgentsMD creates or appends a learnings section to the project's AGENTS.md.
// Returns the path of the created/modified file (empty string if no change was needed).
func ensureAgentsMD(cwd string) (string, error)
```

## Edge Cases
- CLAUDE.md exists at root, no AGENTS.md anywhere → returns ("", nil), no file created.
- Neither CLAUDE.md nor AGENTS.md exists → creates root `AGENTS.md` with learnings section.
- `.forge/AGENTS.md` exists from before this change → still appended to (no migration).
- Root AGENTS.md without trailing newline → newline inserted before appending section.
