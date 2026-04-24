---
id: multi-provider-support
status: draft
---
# Full multi-provider LLM support (OpenAI, etc.)

## Description

Forge currently has an `LLMProvider` interface and a working OpenAI provider,
but the agent is tightly coupled to Anthropic in ~8 concrete locations: model
validation, model defaults, cost tracking, hardcoded Haiku model lists, and the
WebSearch tool which calls the Anthropic SDK directly. This spec covers removing
all Anthropic-specific assumptions so the entire agent can run against any
provider that implements `types.LLMProvider`.

## Context

Key files and their Anthropic coupling:

| File | Coupling |
|------|----------|
| `internal/agent/worker.go:87-94` | Default model `claude-opus-4-6`; `strings.HasPrefix(model, "claude-")` filter rejects all non-Anthropic models from settings |
| `internal/agent/worker.go:620-626` | Same `claude-` prefix filter for sub-agent model selection |
| `internal/agent/worker.go:704-727` | `providerFromName()` falls back to Anthropic for unknown names |
| `internal/agent/phase/classify.go:33-36` | `classificationModels` hardcodes two Claude Haiku model IDs |
| `internal/agent/phase/pr.go:157` | PR generation reuses `classificationModels` (Haiku-only) |
| `internal/runtime/cost/cost.go:20-84` | `modelPricing` map contains only Claude models; returns `$0.00` for everything else |
| `internal/tools/websearch.go:67-158` | Directly imports and calls `anthropic-sdk-go`; uses `anthropic.ModelClaudeHaiku4_5` and `WebSearchTool20260209Param` |
| `cmd/forge/session_name.go:24-44` | Provider auto-detection priority hardcodes Anthropic > OpenAI > Claude CLI |
| `internal/types/types.go:58-66` | `CacheControl` struct models Anthropic-specific ephemeral caching; no equivalent for OpenAI |
| `internal/review/orchestrator.go:305-310` | `modelForProvider()` maps provider name to model; defaults to Claude |
| `internal/runtime/provider/openai.go` | Exists and works, but `CacheControl` fields are silently ignored |

Settings and configuration:
- `.forge/settings.json` / `.forge/settings.local.json` — `model` field; no `provider` field exists
- `MergedSettings` struct (`internal/types/types.go:324-328`) — has `Model` but no `Provider`

## Behavior

### 1. Provider selection via settings
- `.forge/settings.json` gains a `"provider"` key: `"anthropic"` (default), `"openai"`, `"claude-cli"`.
- `MergedSettings` gains a `Provider string` field.
- The model validation filter (`strings.HasPrefix(model, "claude-")`) is removed. Any non-empty model string is passed through to the selected provider. The provider itself is responsible for rejecting invalid models.
- Default model depends on provider: `"claude-opus-4-6"` for Anthropic/Claude CLI, `"gpt-4.1"` for OpenAI.

### 2. Lightweight model lists are provider-aware
- `classificationModels` (intent classification) and its reuse in PR generation become provider-dependent: Anthropic uses Haiku models, OpenAI uses `"gpt-4.1-mini"` (or similar cheap model).
- A single function `cheapModels(providerName string) []string` centralizes this mapping.
- `ClassifyIntent` and `generatePRContent` accept or derive the provider name so they can pick the right model list.

### 3. Cost tracking supports multiple providers
- `modelPricing` map in `internal/runtime/cost/cost.go` gains entries for OpenAI models (`gpt-4.1`, `gpt-4.1-mini`, `gpt-4o`, `gpt-4o-mini`, etc.) with their published pricing.
- `Pricing` struct gains no new fields — OpenAI doesn't have separate cache token pricing, so `CacheWrite`/`CacheRead` stay at `0.0` for OpenAI models.
- `Calculate()` continues to return `0.0` for unknown models (no behavior change).

### 4. WebSearch tool is provider-agnostic
- The `WebSearch` tool handler is refactored to accept a `types.LLMProvider` (or a search-specific interface) instead of directly constructing an Anthropic client.
- When the active provider is Anthropic, continue using the existing `web_search_20260209` server tool approach.
- When the active provider is OpenAI, use OpenAI's `web_search` tool (available on `gpt-4.1` and newer models) or fall back to a simple HTTP search approach.
- The tool must not import `anthropic-sdk-go` if the active provider is not Anthropic.

### 5. CacheControl is gracefully ignored by non-Anthropic providers
- No changes to the `CacheControl` type or how the prompt builder attaches it.
- `OpenAIProvider.buildOpenAIRequest()` already ignores `CacheControl` — this is the correct behavior. Document it explicitly.

### 6. Session name generation respects configured provider
- `newLightweightProvider()` in `cmd/forge/session_name.go` should respect the `provider` setting from `MergedSettings` if available, rather than always preferring Anthropic.
- Falls back to current auto-detection if settings are not loaded yet (acceptable for interactive mode startup).

### 7. Sub-agent and review provider selection
- `internal/agent/worker.go` sub-agent creation (line ~620) uses the same provider as the parent agent, not a hardcoded Anthropic default.
- `internal/review/orchestrator.go` `modelForProvider()` continues to work as-is (already maps provider name to model).
- `internal/agent/phase/orchestrator.go:483-489` review phase provider collection remains multi-provider (already correct).

## Constraints

- Must not break existing Anthropic-only setups. Users with only `ANTHROPIC_API_KEY` set and no `"provider"` in settings must see zero behavior change.
- Must not add new Go module dependencies. OpenAI provider is already pure `net/http`.
- Must not remove the Anthropic SDK dependency — it's still used for the Anthropic provider and potentially for WebSearch when Anthropic is active.
- Must not change the `LLMProvider` interface. The `Chat(ctx, ChatRequest) (<-chan ChatDelta, error)` signature is provider-neutral and stays as-is.
- `CacheControl` remains in `ChatRequest`/`ChatContentBlock`. Non-Anthropic providers ignore it — they must not error on its presence.
- The `"provider"` settings key is optional. Omission defaults to `"anthropic"`.

## Interfaces

```go
// types.go — addition to MergedSettings
type MergedSettings struct {
    Provider    string            `json:"provider,omitempty"` // "anthropic", "openai", "claude-cli"
    Model       string            `json:"model,omitempty"`
    Permissions *PermissionConfig `json:"permissions,omitempty"`
    Env         map[string]string `json:"env,omitempty"`
}

// phase/models.go (new or in classify.go) — provider-aware cheap model lookup
func CheapModels(providerName string) []string

// cost/cost.go — extended modelPricing map (no type changes)
// Adds entries like:
//   "gpt-4.1":      {Input: 2.00, Output: 8.00}
//   "gpt-4.1-mini": {Input: 0.40, Output: 1.60}
//   "gpt-4o":       {Input: 2.50, Output: 10.00}
//   "gpt-4o-mini":  {Input: 0.15, Output: 0.60}

// tools/websearch.go — search function signature change
// Option A: Accept provider interface
func WebSearchTool(provider types.LLMProvider, providerName string) types.ToolDefinition
// Option B: Strategy pattern with a SearchBackend interface
type SearchBackend interface {
    Search(ctx context.Context, query string, numResults int) (string, error)
}
```

## Edge Cases

1. **Unknown provider name in settings** — `providerFromName()` currently falls back to Anthropic with a warning. Should continue to do so, but log the actual unknown name.

2. **OpenAI model in settings but provider="anthropic"** — Model is passed through to Anthropic API, which will return an error. This is the correct behavior (provider validates its own models). No special handling needed.

3. **WebSearch with no Anthropic key** — When provider is OpenAI and `ANTHROPIC_API_KEY` is not set, WebSearch should use an OpenAI-based search (or a fallback). Must not fail with "requires ANTHROPIC_API_KEY."

4. **Cost tracking for unknown models** — Already returns `$0.00`. No change. But if a user configures a custom/fine-tuned model name, cost tracking silently reports zero. Consider logging a one-time warning.

5. **CacheControl with OpenAI** — Already ignored in `buildOpenAIRequest()`. No behavior change. But system prompt builder (`internal/runtime/prompt/prompt.go`) attaches `CacheControl` blocks unconditionally — verify these don't cause issues when passed to OpenAI's request builder.

6. **Session resume across provider changes** — JSONL history uses Anthropic content block format (text, tool_use, tool_result). OpenAI provider already translates these in `convertMessage()`. Resuming a session that started on Anthropic with OpenAI (or vice versa) should work if the history format is the canonical forge format. Verify no provider-specific block types leak into JSONL.

7. **Mixed provider review** — The review orchestrator already supports `map[string]types.LLMProvider`. A review that uses both Anthropic and OpenAI continues to work.
