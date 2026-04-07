---
id: improve-learnings
status: implemented
---
# Make learnings actionable gotchas, not session diaries

## Description
The Reflect tool and auto-reflection system produced useless learning files
containing only "User asked: X. Tools used: Y (N calls)". Restructured the
entire learnings pipeline so only actionable, reusable insights get saved.

## Context
- `internal/tools/reflect.go` — Reflect tool definition, handler, file writer
- `internal/agent/worker.go` — autoReflect callback, buildReflectionSummary
- `internal/runtime/prompt/prompt.go` — system prompt self-improvement line
- `AGENTS.md` — Agent Learnings section, Self-Improvement Loop docs

## Behavior
- Reflect tool requires `summary` (string, for filename) and `learnings` (array of strings, min 1 entry)
- Empty learnings array returns an error
- Learning files are formatted as `# Learnings - <date>` with a bullet list — no diary sections
- No auto-reflection on session completion — agents must explicitly call Reflect
- System prompt instructs agents to reflect only when genuinely novel insights exist
- Existing useless learning files deleted

## Constraints
- Do not remove the `OnComplete` hook from loop.Options — it's used by queue execution
- Do not change the git commit/push behavior for learning files
- Do not change `.gitattributes` handling

## Interfaces
```go
// Reflect tool input schema
{
  "summary":   string,   // required, for filename slug
  "learnings": []string, // required, min 1, actionable gotchas
}

// File format
# Learnings - 2026-04-06 19:15
- Gotcha one
- Gotcha two
```

## Edge Cases
- Empty learnings array: returns error "learnings array is required and must contain at least one entry"
- Missing summary: returns existing "required field" error
- Non-git directory: file written, no commit attempted (unchanged behavior)
- Git repo with remote: commits and pushes (unchanged behavior)
