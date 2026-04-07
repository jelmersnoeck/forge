---
id: codebase-cleanup
status: active
---
# Remove dead code, reduce duplication, simplify

## Description
Comprehensive codebase cleanup: remove dead packages and functions, extract
shared helpers to eliminate repetition, and simplify unnecessarily complex code.
Goal is smaller surface area for better maintainability and extensibility.

## Context
Files and packages that change:
- `internal/terminal/` — entire package (dead, unused)
- `internal/runtime/compact/` — entire package (dead, unused)
- `internal/tools/progress.go` — entirely dead
- `internal/runtime/errors/classifier.go` — dead retry logic (Retry, RetryConfig, DefaultRetryConfig)
- `internal/runtime/errors/classifier_test.go` — dead test code for above
- `internal/types/types.go` — dead `ContentBlock` alias, dead `SessionReflection`
- `internal/runtime/loop/loop.go` — dead `isStreamError`, unnecessary conditionals
- `internal/tools/queue_immediate.go` + `queue_on_complete.go` — near-identical, merge
- `internal/tools/*.go` — repeated input parsing / result construction boilerplate
- `internal/agent/audit.go` — `toolCallSummary` duplicated in loop.go
- `cmd/cli/main.go`, `cmd/agent/main.go`, `cmd/server/main.go` — legacy binaries (deprecated)
- `cmd/forge/agent.go` + `cmd/forge/server.go` — duplicated `loadEnv` logic

## Behavior
- Remove `internal/terminal/` package entirely
- Remove `internal/runtime/compact/` package entirely
- Remove `internal/tools/progress.go`
- Remove dead retry logic from errors/classifier.go (keep Classify and ClassifiedError)
- Remove `ContentBlock` alias from types.go
- Replace `isStreamError` with `errors.As` in loop.go
- Remove unnecessary `if > 0` guards on token accumulation in loop.go
- Merge QueueImmediate and QueueOnComplete tools via shared factory
- Add `TextResult`/`ErrorResult` helpers to reduce tool handler ceremony
- Add `requireString`/`optionalString`/`optionalFloat`/`optionalBool` input helpers
- Collapse `toolUseSummary` switch to map lookup in loop.go and audit.go
- Extract shared `loadEnv` to `internal/envutil/`
- Remove legacy binaries `cmd/{cli,agent,server}/`
- Simplify `edit.go` if-else to single `strings.Replace` call

## Constraints
- Must not break any existing tests
- Must not change tool behavior or user-facing output
- Must not change API contracts (types, interfaces)
- Do not move global state to ToolContext in this PR (structural, higher risk)
- Do not refactor checkInteractiveCommand in this PR (complex, separate concern)

## Interfaces
```go
// internal/tools/helpers.go
func TextResult(text string) types.ToolResult
func ErrorResult(text string) (types.ToolResult, error)
func requireString(input map[string]any, key string) (string, error)
func optionalString(input map[string]any, key, fallback string) string
func optionalFloat(input map[string]any, key string, fallback float64) float64
func optionalBool(input map[string]any, key string, fallback bool) bool

// internal/envutil/env.go
func LoadEnv(dirs ...string)
```

## Edge Cases
- Removing legacy binaries: justfile/Makefile may reference them — update build targets
- Removing compact package: ensure no transitive import exists
- `SessionReflection` is referenced only inside `SessionMessage` struct — keep if SessionMessage needs it for JSON compat
