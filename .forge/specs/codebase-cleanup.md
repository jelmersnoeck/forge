---
id: codebase-cleanup
status: implemented
---
# Remove dead code, reduce duplication, simplify

## Description
Comprehensive codebase cleanup: removed dead packages and functions, extracted
shared helpers to eliminate repetition, and simplified unnecessarily complex code.
Net result: -2917 lines across 40 files. Goal was smaller surface area for
better maintainability and extensibility.

## Context
Files and packages changed:
- `internal/terminal/` — entire package deleted (dead, 552 lines)
- `internal/runtime/compact/` — entire package deleted (dead, 370 lines)
- `internal/tools/progress.go` — deleted (51 lines, zero callers)
- `internal/runtime/errors/classifier.go` — dead Retry/RetryConfig/DefaultRetryConfig removed
- `internal/runtime/errors/classifier_test.go` — dead retry tests removed
- `internal/types/types.go` — dead ContentBlock alias removed
- `internal/runtime/loop/loop.go` — isStreamError replaced with errors.As, token guards removed, toolUseSummary collapsed to map
- `internal/tools/queue_immediate.go` + `queue_on_complete.go` — merged into `queue.go`
- `internal/tools/helpers.go` — new file with shared helpers
- `internal/tools/*.go` — all tool handlers refactored to use helpers
- `internal/agent/audit.go` — toolCallSummary collapsed to map
- `cmd/cli/main.go`, `cmd/agent/main.go`, `cmd/server/main.go` — deleted (legacy, 1030 lines)
- `cmd/forge/agent.go` + `cmd/forge/server.go` — use shared envutil.LoadEnv
- `internal/envutil/env.go` — new shared .env loading package
- `internal/runtime/session/jsonl.go` + test — deleted (dead, 446 lines)
- `.github/workflows/ci.yml` — removed legacy binary builds
- `justfile` — removed legacy build targets
- `AGENTS.md` — updated to reflect removed packages

## Behavior
- Removed `internal/terminal/` package entirely (zero imports anywhere)
- Removed `internal/runtime/compact/` package entirely (superseded by tokens.Compact)
- Removed `internal/tools/progress.go` (ProgressEvent/BashProgress/GrepProgress never called)
- Removed dead retry logic from errors/classifier.go (Retry, RetryConfig, DefaultRetryConfig — superseded by retry.Do)
- Removed `ContentBlock` alias from types.go (zero usages)
- Removed session/jsonl.go (Reader, Writer, ValidateChain, all entry types — superseded by session.Store)
- Replaced `isStreamError` with `errors.As` in loop.go (also handles wrapped errors)
- Removed unnecessary `if > 0` guards on token accumulation in loop.go
- Removed dead `turnCount == 0` conditional in loop.go
- Merged QueueImmediate and QueueOnComplete tools via shared makeQueueTool factory
- Added `textResult`/`errResult`/`errResultf` helpers for ToolResult construction
- Added `requireString`/`optionalString`/`optionalFloat`/`optionalBool` input helpers
- Collapsed `toolUseSummary` switch to map lookup in loop.go and audit.go
- Extracted shared `loadEnv` to `internal/envutil/env.go`
- Removed legacy binaries `cmd/{cli,agent,server}/`
- Simplified `edit.go` if-else to single `strings.Replace` call with count parameter
- Refactored all tool handlers (agent, task, bash, read, grep, glob, write, reflect, mcp_gateway) to use shared helpers
- Updated CI and justfile to remove legacy references
- Updated AGENTS.md to reflect current architecture

## Constraints
- Did not break any existing tests (only pre-existing timezone failure in cost tests)
- Did not change tool behavior or user-facing output (except minor message wording: removed emoji prefixes from queue tool messages)
- Did not change API contracts (types, interfaces)
- Did not move global state (taskMgr, mcpGatewayStore) to ToolContext — deferred as separate concern
- Did not refactor checkInteractiveCommand — deferred as separate concern

## Interfaces
```go
// internal/tools/helpers.go
func textResult(text string) types.ToolResult
func errResult(text string) (types.ToolResult, error)
func errResultf(format string, args ...any) (types.ToolResult, error)
func requireString(input map[string]any, key string) (string, error)
func optionalString(input map[string]any, key, fallback string) string
func optionalFloat(input map[string]any, key string, fallback float64) float64
func optionalBool(input map[string]any, key string, fallback bool) bool

// internal/envutil/env.go
func LoadEnv(dirs ...string)
```

## Edge Cases
- Removing legacy binaries: justfile and CI updated accordingly
- SessionReflection type kept — still referenced by SessionMessage struct for JSON compatibility
- errResult returns (ToolResult, error) with nil error, matching the handler signature pattern where errors are surfaced to the LLM not the loop
- The MCP gateway's old errResult (format+args, returns ToolResult) was replaced with errResultf (returns ToolResult+error) — call sites updated
- Queue tool tests updated to call Handler through the ToolDefinition struct since the handler functions are now anonymous

