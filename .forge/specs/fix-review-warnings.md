---
id: fix-review-warnings
status: implemented
---
# Fix review warnings and critical issues

## Description
Address a batch of code quality issues found during review: nil-safety in session naming,
structured error logging in web search, configurable timeouts/limits, log spam prevention
in API key validation, and structured logging for provider credential failures.

## Context
- `cmd/forge/session_name.go` — generateSlug, newLightweightProvider
- `internal/tools/websearch.go` — WebSearchTool, dispatchSearch, searchViaOpenAI
- `internal/runtime/provider/detect.go` — validateAPIKey, FromName, FromNameOrFallback
- `internal/agent/worker.go` — selectProvider
- `internal/runtime/cost/cost.go` — modelPricing map

## Behavior
- `generateSlug` returns fallback name immediately when provider creation fails (no nil provider passed)
- `newLightweightProvider` returns error for config corruption (already does — just noted as resolved)
- WebSearch handler logs structured error info (provider, query, error type) before returning IsError result
- OpenAI dispatch in websearch validates whitespace-only API keys (not just empty)
- `searchViaOpenAI` detects when io.LimitReader may have truncated the response body
- WebSearch HTTP timeout and response body size limit are configurable via exported package vars
- `validateAPIKey` logs whitespace warning at most once per key to prevent log spam
- Provider credential validation in detect.go uses structured log format with key=value pairs
- `selectProvider` in worker.go uses structured warning with key=value for monitoring visibility

## Constraints
- Do not change public API signatures that callers depend on
- Do not refactor unrelated code
- False-positive "critical" issues (classify.go, pr.go, cost.go) already correct — skip

## Interfaces
```go
// websearch.go — configurable via package vars
var OpenAIHTTPTimeout = 30 * time.Second
var MaxResponseBodySize int64 = 5 * 1024 * 1024

// openAIHTTPClient() returns a new *http.Client per call so runtime
// changes to OpenAIHTTPTimeout take effect (not frozen at init).
func openAIHTTPClient() *http.Client

// classifySearchError categorizes errors via errors.Is / errors.As
// (no string matching). Returns: auth_missing, response_truncated,
// timeout, auth_rejected, rate_limit, server_error, unknown.
func classifySearchError(err error) string

// readLimitedBody reads limit+1 bytes to detect truncation without
// false positives at the exact boundary.
func readLimitedBody(r io.Reader, limit int64) (body []byte, truncated bool, err error)

// detect.go — sync.Once-guarded whitespace warning
var whitespaceKeyWarningOnce sync.Once
```

## Edge Cases
- Whitespace-only API keys should be treated as empty (already handled, adding log-once)
- LimitReader hitting exact limit boundary: `readLimitedBody` reads `limit+1` bytes — if all `limit+1` are read, the body was truncated; if `<= limit` bytes were read, the body fit. No false positives for exact-size responses.
- generateSlug with provider error: should still return a valid slug (fallback name)
- Config error in newLightweightProvider: returns sanitized "configuration file is corrupted or unreadable" — no file paths leaked
