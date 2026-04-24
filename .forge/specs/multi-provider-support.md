---
id: multi-provider-support
status: implemented
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

Files modified:

| File | Change |
|------|--------|
| `internal/types/types.go` | Added `Provider` field to `MergedSettings` |
| `internal/agent/worker.go` | Removed `claude-` prefix model filter; added `defaultModelForProvider()`; updated `selectProvider()` with user config priority; sub-agent uses parent provider |
| `internal/agent/phase/models.go` | New file: `CheapModels()` for provider-aware lightweight model lookup |
| `internal/agent/phase/classify.go` | `ClassifyIntent()` accepts `providerName` param; uses `CheapModels()` |
| `internal/agent/phase/pr.go` | `EnsurePR()`/`CreatePR()`/`generatePRContent()` accept `providerName`; use `CheapModels()` |
| `internal/agent/phase/orchestrator.go` | Pass provider name through to classify/PR functions |
| `internal/runtime/cost/cost.go` | Added OpenAI model pricing (gpt-4.1, gpt-4.1-mini, gpt-4.1-nano, gpt-4o, gpt-4o-mini, o3, o3-mini, o4-mini) |
| `internal/tools/websearch.go` | Refactored to provider-name dispatch: Anthropic uses SDK, OpenAI uses Responses API |
| `internal/tools/registry.go` | `NewDefaultRegistry()` accepts `providerName` to pass to WebSearch |
| `cmd/forge/session_name.go` | `newLightweightProvider()` respects `FORGE_PROVIDER` env and user config |
| `internal/review/orchestrator.go` | Already correct — `modelForProvider()` maps provider name to model |
| `internal/runtime/provider/openai.go` | Unchanged — already ignores `CacheControl` |
| `AGENTS.md` | Updated docs for multi-provider env vars and gotchas |

Test files updated:
- `internal/runtime/cost/cost_test.go` — OpenAI pricing tests
- `internal/tools/websearch_test.go` — Provider-specific handler tests, OpenAI response format tests
- `internal/agent/phase/classify_test.go` — Updated for new `ClassifyIntent` signature
- `internal/agent/phase/pr_test.go`, `pr_ensure_test.go` — Updated for new `EnsurePR`/`CreatePR` signatures
- `internal/agent/ensure_pr_test.go` — Updated for new `ensurePR` signature

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
- The `WebSearch` tool handler is refactored to accept a `providerName string` and dispatch to the appropriate search backend.
- When the active provider is Anthropic or Claude CLI, continues using the existing `web_search_20260209` server tool via Anthropic SDK.
- When the active provider is OpenAI, uses OpenAI's Responses API with `web_search` tool (via `gpt-4.1-mini`).
- The OpenAI search path maps `numResults` to `search_context_size` ("low"/"medium"/"high") and deduplicates URL citations.
- The tool still imports `anthropic-sdk-go` unconditionally (used for the Anthropic code path), but the OpenAI path is pure `net/http`.
- WebSearch requires the appropriate API key for the active provider (`ANTHROPIC_API_KEY` or `OPENAI_API_KEY`); returns an error result if missing.

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
// types.go — MergedSettings with Provider field
type MergedSettings struct {
    Provider    string            `json:"provider,omitempty"` // "anthropic", "openai", "claude-cli"
    Model       string            `json:"model,omitempty"`
    Permissions *PermissionConfig `json:"permissions,omitempty"`
    Env         map[string]string `json:"env,omitempty"`
}

// phase/models.go — provider-aware cheap model lookup
func CheapModels(providerName string) []string

// phase/classify.go — updated signature
func ClassifyIntent(ctx context.Context, provider types.LLMProvider, prompt string, providerName string) (Intent, error)

// phase/pr.go — updated signatures
func EnsurePR(ctx context.Context, prov types.LLMProvider, providerName, cwd, specPath string) PRResult
func CreatePR(ctx context.Context, prov types.LLMProvider, providerName, cwd, specPath string) PRResult

// agent/worker.go — provider-specific default model
func defaultModelForProvider(providerName string) string

// tools/websearch.go — provider-name based dispatch
func WebSearchTool(providerName string) types.ToolDefinition

// tools/registry.go — accepts provider name
func NewDefaultRegistry(providerName string) *Registry

// cost/cost.go — extended modelPricing map (no type changes)
// Added entries:
//   "gpt-4.1":      {Input: 2.00, Output: 8.00}
//   "gpt-4.1-mini": {Input: 0.40, Output: 1.60}
//   "gpt-4.1-nano": {Input: 0.10, Output: 0.40}
//   "gpt-4o":       {Input: 2.50, Output: 10.00}
//   "gpt-4o-mini":  {Input: 0.15, Output: 0.60}
//   "o3":           {Input: 2.00, Output: 8.00}
//   "o3-mini":      {Input: 1.10, Output: 4.40}
//   "o4-mini":      {Input: 1.10, Output: 4.40}
```

## Edge Cases

1. **Unknown provider name in settings** — `providerFromName()` currently falls back to Anthropic with a warning. Should continue to do so, but log the actual unknown name.

2. **OpenAI model in settings but provider="anthropic"** — Model is passed through to Anthropic API, which will return an error. This is the correct behavior (provider validates its own models). No special handling needed.

3. **WebSearch with no Anthropic key** — When provider is OpenAI and `ANTHROPIC_API_KEY` is not set, WebSearch should use an OpenAI-based search (or a fallback). Must not fail with "requires ANTHROPIC_API_KEY."

4. **Cost tracking for unknown models** — Already returns `$0.00`. No change. But if a user configures a custom/fine-tuned model name, cost tracking silently reports zero. Consider logging a one-time warning.

5. **CacheControl with OpenAI** — Already ignored in `buildOpenAIRequest()`. No behavior change. But system prompt builder (`internal/runtime/prompt/prompt.go`) attaches `CacheControl` blocks unconditionally — verify these don't cause issues when passed to OpenAI's request builder.

6. **Session resume across provider changes** — JSONL history uses Anthropic content block format (text, tool_use, tool_result). OpenAI provider already translates these in `convertMessage()`. Resuming a session that started on Anthropic with OpenAI (or vice versa) should work if the history format is the canonical forge format. Verify no provider-specific block types leak into JSONL.

7. **Mixed provider review** — The review orchestrator already supports `map[string]types.LLMProvider`. A review that uses both Anthropic and OpenAI continues to work.
