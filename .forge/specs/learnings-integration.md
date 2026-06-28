---
id: learnings-integration
status: implemented
---
# Consolidate learnings into AGENTS.md Gotchas and clean up stale files

## Description
Learnings are scattered across three locations (AGENTS.md "Gotchas", AGENTS.md
"Agent Learnings", `.forge/learnings/` files) causing duplication, wasted prompt
tokens, and cache inefficiency. Consolidate into a single `## Gotchas` section
in AGENTS.md. Treat `.forge/learnings/` as a staging area: the Reflect tool
still writes there, but the `ensureAgentsMD` function now references "Gotchas"
instead of "Agent Learnings".

## Context
- `AGENTS.md` — lines 304-315 (`## Gotchas`), lines 337-349 (`# Agent Learnings`)
- `.forge/learnings/` — 13 files (10 stale diary entries, 3 useful)
- `internal/tools/reflect.go` — `ensureAgentsMD()`, `agentsMDLearningsSection` constant
- `internal/tools/reflect_test.go` — `TestEnsureAgentsMD`, `TestReflectTool`
- `internal/runtime/prompt/prompt.go` — `Assemble()` splits learnings into dynamic block
- `internal/runtime/prompt/prompt_test.go` — `TestAssemble_AgentsMD_Learnings`, `TestAssemble_CacheControlTTL`
- `internal/runtime/context/loader.go` — `loadLearnings()`
- `internal/runtime/context/loader_agents_test.go` — `TestLoader_LoadLearnings*`

## Behavior
1. **AGENTS.md "Gotchas" section absorbs "Agent Learnings"**: The existing
   `## Gotchas` section (L304) keeps its original bullets. The 9 bullets from
   `# Agent Learnings` (L337-349) are appended below. The `# Agent Learnings`
   heading and its reference to `.forge/learnings/` are removed.

2. **Graduate useful learnings from files**: The 3 useful `.forge/learnings/`
   files have their bullet-point learnings appended to `## Gotchas`:
   - `20260408-*`: 3 bullets on `exec.CommandContext`/process groups/`cmd.WaitDelay`
   - `20260413-*`: 2 bullets on `cmd.Stderr` data race / Claude CLI NDJSON format
   - `20260522-*`: 2 bullets on gateway daemon re-exec bug / TmuxBackend stale agent

3. **Delete all `.forge/learnings/` files**: All 13 markdown files are removed.
   The directory itself stays (Reflect tool creates files here).

4. **`ensureAgentsMD` now targets "Gotchas"**: The sentinel check changes from
   `# Agent Learnings` to `## Gotchas`. The appended section content changes to
   a Gotchas-flavored version directing users to `.forge/learnings/`.

5. **Tests updated**: All tests referencing `# Agent Learnings` check for
   `## Gotchas` instead. Prompt tests for learnings in the dynamic block still
   pass (the loader/prompt code is unchanged — learnings files that exist are
   still loaded into the dynamic block).

## Constraints
- Do NOT remove the `loadLearnings()` function or the dynamic-block learnings
  injection — `.forge/learnings/` remains a valid staging area.
- Do NOT change the Reflect tool's write behavior — it still writes to
  `.forge/learnings/`.
- Do NOT restructure `## Gotchas` content — keep it as a flat bullet list.
- The `ensureAgentsMD` detection must be case-insensitive for the heading
  (handles `## Gotchas`, `## gotchas`, etc.) — actually no, keep it simple:
  just check for `## Gotchas` literally.

## Interfaces

```go
// Updated constant in reflect.go
const agentsMDLearningsSection = `
## Gotchas

Actionable discoveries from past sessions are stored in ` + "`.forge/learnings/`" + `.
Consult them when starting a task — if a learning is relevant, factor it into
your approach to avoid repeating past mistakes.
`

// ensureAgentsMD sentinel check changes from:
//   strings.Contains(string(content), "# Agent Learnings")
// to:
//   strings.Contains(string(content), "## Gotchas")
```

## Edge Cases
- AGENTS.md already has `## Gotchas` but no `# Agent Learnings`: `ensureAgentsMD`
  returns noop (correct — the section exists).
- AGENTS.md has neither section: appends `## Gotchas` section.
- `.forge/learnings/` is empty after cleanup: `loadLearnings()` returns no entries,
  dynamic block omits the learnings `<system-reminder>` — no wasted tokens.
- Future Reflect calls write new files to `.forge/learnings/`: still loaded into
  dynamic block until manually graduated. No behavior change.
