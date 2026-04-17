---
id: provider-agnostic-lightweight-calls
status: draft
---
# Make intent classification and session naming provider-agnostic

## Description

Intent classification (`classify.go`) and session naming (`session_name.go`) both
make lightweight LLM calls that currently fail in practice. Classification uses a
nonexistent model ID (`claude-haiku-4-20250414`), and session naming bypasses the
provider abstraction entirely by importing the Anthropic SDK directly. Both must
work with any configured provider (Anthropic API, Claude CLI, OpenAI).

## Context

Files that change:
- `internal/agent/phase/classify.go` — intent classifier, hardcoded Anthropic model IDs
- `internal/agent/phase/classify_test.go` — tests for intent classification
- `cmd/forge/session_name.go` — session slug generation, directly uses Anthropic SDK
- `cmd/forge/session_name_test.go` — new/updated tests for session naming

Files for reference:
- `internal/types/types.go` — `LLMProvider` interface, `ChatRequest`, `ChatDelta`
- `internal/runtime/provider/anthropic.go` — Anthropic provider impl
- `internal/runtime/provider/openai.go` — OpenAI provider impl
- `internal/runtime/provider/claude_cli.go` — Claude CLI provider impl
- `internal/agent/worker.go` — `selectProvider()` determines active provider
- `cmd/forge/cli.go:1394` — calls `generateSessionName()` before provider is available

## Behavior

### Intent Classification (`classify.go`)

1. `ClassifyIntent` uses the provider passed by the orchestrator (no separate client creation).
2. The model passed to the provider is a lightweight/cheap model appropriate for
   the provider type. Classification does NOT need to know which provider it's
   talking to — it sends a request with a suggested model, and the provider
   handles it.
3. The model list is updated to use real, existing Anthropic model IDs:
   `claude-haiku-4-5-20251001`, `claude-3-5-haiku-20241022`.
   The fictional `claude-haiku-4-20250414` is removed.
4. For non-Anthropic providers, the model field in the ChatRequest is left empty
   (or set to empty string), letting the provider use its default model.
   The classifier itself is model-agnostic — it sends a system prompt and parses
   JSON from the response.
5. Fallback chain: try the configured models in order. If all fail, return
   `IntentTask` (safe default, same as today).
6. The 2-second per-attempt timeout is preserved.

### Session Naming (`session_name.go`)

1. `generateSessionName` accepts an `LLMProvider` parameter instead of directly
   constructing an Anthropic client.
2. Uses the `LLMProvider.Chat()` interface with streaming, same as classification.
3. When provider is nil or the call fails, falls back to `fallbackSessionName()`
   (random adjective-noun pair), same as today.
4. The Anthropic SDK import is removed from `session_name.go`.
5. The caller (`spawnLocalAgent` in `cli.go`) passes the result of
   `selectProvider()` — but since `selectProvider` is in the `agent` package,
   the CLI should use a similar provider selection or call a shared helper.
   Alternatively, `generateSessionName` constructs a provider internally using
   the same env-var logic (check ANTHROPIC_API_KEY, then OPENAI_API_KEY, then
   claude CLI), keeping the function self-contained.
6. The 3-second timeout is preserved.
7. Model selection: send empty model string, letting the provider use its default.
   For a naming call, any model works — we don't need to specify Haiku.
   The cost difference is negligible for 32 output tokens.

### Shared Concerns

- Both classify and session naming send `MaxTokens: 32` — keep this.
- Both use `Stream: true` — keep this for consistency with the provider interface.
- Error handling: both default to safe fallbacks on any error (no user-visible failures).

## Constraints

- Must not import `github.com/anthropics/anthropic-sdk-go` in `session_name.go`.
- Must not reference any nonexistent model IDs (no `claude-haiku-4-20250414`).
- Must not change the `LLMProvider` interface.
- Must not add provider-type-sniffing (e.g., no `switch p.(type)` to choose models).
  The classifier sends a request; the provider handles model resolution.
- Classification timeout stays at 2s per attempt. Session naming timeout stays at 3s.
- Must not break the random fallback path (no provider / no API key = still works).

## Interfaces

```go
// classify.go — updated model list
var classificationModels = []string{
    "claude-haiku-4-5-20251001",
    "claude-3-5-haiku-20241022",
}

// ClassifyIntent signature stays the same
func ClassifyIntent(ctx context.Context, provider types.LLMProvider, prompt string) (Intent, error)

// session_name.go — updated signature
func generateSessionName(provider types.LLMProvider, prompt string) string
```

```go
// cli.go call site — provider passed to generateSessionName
// Before: slug := generateSessionName(initialPrompt)
// After:  slug := generateSessionName(prov, initialPrompt)
```

## Edge Cases

1. **No provider available** (nil provider, no API keys, no CLI):
   - Classification: returns `IntentTask` with error.
   - Session naming: returns `fallbackSessionName()`.

2. **OpenAI provider with Anthropic model IDs**:
   - The Anthropic model IDs will fail with OpenAI. The fallback chain exhausts
     all models, then returns `IntentTask`. This is acceptable because the
     fallback is fast (<2s × 2 models) and the safe default is correct.
   - Future improvement: add provider-specific cheap model lists. Out of scope.

3. **Claude CLI provider**:
   - Claude CLI accepts model names but handles them differently. If the Haiku
     model is passed, it may work (CLI supports `--model`). If it fails,
     fallback to `IntentTask`.

4. **Provider returns garbage JSON**:
   - `parseIntent` already handles this — returns `IntentTask` with error.

5. **Context cancelled mid-classification**:
   - The per-attempt timeout context handles this. Context cancellation propagates
     through the provider's `Chat()` call.

6. **Session naming called before provider init (gateway mode)**:
   - `generateSessionName` should handle nil provider gracefully → fallback.
