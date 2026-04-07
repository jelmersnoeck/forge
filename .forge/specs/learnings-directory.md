---
id: learnings-directory
status: implemented
---
# Automatic session reflection via .forge/learnings/

## Description
Learnings are written to `.forge/learnings/` as individual markdown files. The
Reflect tool handles writing, and the context loader reads them back into the
system prompt. Previously, reflection was purely opt-in (LLM had to call the
Reflect tool). Now the loop automatically invokes reflection when a conversation
turn completes with meaningful work (i.e., at least one tool was used).

## Context
- `internal/tools/reflect.go` — Reflect tool handler + `SaveReflection` exported function
- `internal/tools/reflect_test.go` — tests for reflect tool
- `internal/runtime/loop/loop.go` — conversation loop, calls OnComplete callback
- `internal/runtime/loop/loop_test.go` — loop tests including auto-reflect
- `internal/runtime/context/loader.go` — loads AGENTS.md and learnings into ContextBundle
- `internal/runtime/context/loader_agents_test.go` — loader tests
- `internal/runtime/prompt/prompt.go` — assembles learnings into system prompt
- `internal/runtime/prompt/prompt_test.go` — prompt tests
- `internal/types/types.go` — ContextBundle, AgentsMDEntry types
- `internal/agent/worker.go` — sets up OnComplete to auto-reflect

## Behavior
- Reflect tool writes each reflection as an individual `.md` file in `<CWD>/.forge/learnings/`.
- Filename format: `<timestamp>-<slugified-summary>.md` (e.g. `20260404-134500-implemented-feature-x.md`).
- The `.forge/learnings/` directory is created on first write if missing.
- On first write, ensure `.gitattributes` contains a line marking `.forge/learnings/**` as `linguist-generated=true`.
- If `.gitattributes` already has the line, don't duplicate it.
- **Auto-commit**: After writing a learning file, automatically `git add` + `git commit` the learning file and `.gitattributes`. Uses `--no-verify` to skip hooks. Best-effort: silently skips if not in a git repo or if the commit fails.
- **Auto-push**: After committing, pushes to remote if the current branch has an upstream tracking branch. Uses `--no-verify`. Silently skips if no remote or push fails.
- The context loader discovers all `.md` files in `.forge/learnings/` and loads them as learnings (level "project").
- AGENTS.md files continue to be loaded as context (read-only) — they just aren't written to.
- The base prompt's self-improvement line changes from "AGENTS.md" to ".forge/learnings/".
- The Reflect tool description updates to reference `.forge/learnings/` not `AGENTS.md`.
- **Automatic reflection**: When the loop's `runLoop` completes and at least one tool was
  executed during the session, the `OnComplete` callback fires. The worker sets this
  callback to invoke `SaveReflection` with a summary built from conversation history
  (user prompt + list of tools used).
- Sub-agents do NOT auto-reflect (no OnComplete callback set).
- Conversations with zero tool use (pure text Q&A) do NOT trigger auto-reflection.
- The LLM can still call Reflect explicitly for richer, LLM-authored reflections.

## Constraints
- Do NOT remove AGENTS.md reading from the context loader — it's still valid input.
- Do NOT write to AGENTS.md from the Reflect tool.
- Do NOT break existing AGENTS.md loading (user, parent, project, local levels).
- Individual learning files, not one big append file — keeps git diffs clean.
- The `.gitattributes` update is idempotent.
- Auto-reflection must not block on errors — log and continue.
- Auto-reflection must not add an LLM call — it's a pure file-write operation.

## Interfaces
```go
// Loop.Options gains an OnComplete callback:
type Options struct {
    // ... existing fields ...
    OnComplete func(history []types.ChatMessage)
}

// Exported function for direct reflection (bypasses tool registry):
func SaveReflection(cwd, summary string) error

// File path for a learning: .forge/learnings/20260404-134500-implemented-feature-x.md
// Slugification: lowercase, non-alphanum replaced with hyphens, trimmed, max ~50 chars
```

## Edge Cases
- CWD has no `.forge/` directory → created automatically.
- CWD has no `.gitattributes` → created with the single line.
- `.gitattributes` exists but has no forge learnings line → line appended.
- `.gitattributes` already has the line → no change.
- Summary contains special characters / is very long → slugified and truncated.
- Multiple reflections in same second → filename collision resolved with suffix or uniqueness from summary.
- Empty summary → rejected (existing behavior).
- Loop exits with error → OnComplete still fires if tools were used (partial work is worth saving).
- Context cancelled → OnComplete does NOT fire (session was killed).
- OnComplete callback panics → recovered, logged, does not crash the agent.
- Not a git repo → learning file written, commit silently skipped.
- Git commit fails (e.g. locked index) → logged, does not fail the tool.
- No remote / no upstream tracking branch → commit created locally, push silently skipped.
- Push fails (e.g. network error, auth) → logged, does not fail the tool.
