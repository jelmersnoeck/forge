---
id: eliminate-provider-coupling
status: implemented
---
# Eliminate Anthropic-specific coupling so any LLM provider works end-to-end

## Description

Forge has a `types.LLMProvider` interface and three implementations (Anthropic,
OpenAI, Claude CLI), but Anthropic assumptions are hardcoded across ~9 subsystems:
default model, a `claude-` prefix validation gate, the lightweight-model list,
WebSearch, cost pricing, review provider construction, error messages, the agent
startup warning, and cache-control budgeting. A non-Anthropic user hits 404s on
the default model, silent settings drops, broken WebSearch, and $0.00 cost
tracking. This spec makes each leak provider-aware.

Extends `provider-agnostic-lightweight-calls` (classify/session-naming flow already
provider-agnostic; this spec adds per-provider lightweight model lists). Touches
`credential-providers` (env-var name resolution) and `dynamic-model-pricing`
(cost table) — see those specs for related context.

The work is incremental: each Phase below is a standalone PR. Phases 1–2 are
high-impact/low-risk and land first.

Follow-up: `provider-router` (issue #232) adds a model→provider router that
re-routes mid-session `/model` switches to the owning provider and folds the
duplicated provider-map construction (`collectProviders`,
`CollectReviewProviders`, `selectProvider`) into one place.

## Context

Files that change:
- `internal/agent/worker.go` — two `const defaultModel = "claude-opus-4-6"`
  (lines ~366, ~1057); `resolveDefaultModel` `claude-` prefix gate (~1379);
  `reviewListener`/`runReview` env-sniffing provider construction (~942-952);
  `collectProviders` (~1184); `selectProvider`/`providerFromName` (~1125-1180).
- `internal/runtime/provider/anthropic.go` — `AnthropicProvider`,
  `maxCacheBreakpoints`, `buildRequest` cache budgeting.
- `internal/runtime/provider/openai.go` — `OpenAIProvider`, `openAIDefaultModel`.
- `internal/runtime/provider/claude_cli.go` — `ClaudeCLIProvider`.
- `internal/types/types.go` — `LightweightModels` (153-156), `CacheControl`
  (69-73), `SystemBlock`/`ChatContentBlock`/`ToolSchema` cache fields.
- `internal/tools/websearch.go` — direct Anthropic SDK client + `web_search_20260209`.
- `internal/runtime/cost/cost.go` — Claude-only `modelPricing` map; `Calculate`
  returns 0.0 silently for unknown models.
- `internal/review/orchestrator.go` — `modelForProvider` (483-491).
- `internal/agent/phase/orchestrator.go` — review provider construction (~632-648).
- `internal/runtime/errors/classifier.go` — auth message (172) hardcodes
  `ANTHROPIC_API_KEY`.
- `cmd/forge/agent.go` — startup warning (40-42) only checks Anthropic key.
- `internal/tools/registry.go` — single cache breakpoint on last tool schema (~98-100).
- `internal/agent/phase/classify.go` — iterates `types.LightweightModels` (117).
- `cmd/forge/session_name.go` — iterates `types.LightweightModels` (98).
- `internal/runtime/tokens/summarize.go` — iterates `types.LightweightModels` (61).

Files touched during implementation but not in the original list:
- `internal/runtime/provider/capabilities.go` — NEW: `DefaultModel`/`LightweightModels`
  helpers + their test (`capabilities_test.go`).
- `internal/agent/phase/decompose.go`, `internal/agent/phase/pr.go` — also
  iterated `types.LightweightModels`; migrated to the provider helper.
- `internal/agent/phase/orchestrator.go` — added `CollectReviewProviders`.
- `internal/agent/provider_warning_test.go` — NEW: tests for
  `resolveProviderName`/`StartupKeyWarning`.
- `internal/types/models_test.go` — DELETED (only covered the removed
  `LightweightModels` var).
- Test fakes in `classify_test.go`, `decompose_test.go`, `summarize_test.go`,
  `session_name_test.go`, `transition_history_test.go`,
  `session_continuity_test.go` gained `LightweightModels()` methods.

Reference:
- `internal/credentials/credentials.go` — logical keys `AnthropicAPIKey`,
  `OpenAIAPIKey` → env var names. `Default().Get(key)` returns `(value, ok)`.

## Behavior

### Phase 1 — Provider-aware defaults

1. Each provider exposes its own default model via a `DefaultModel() string`
   method on the `LLMProvider` (or a narrow optional interface — see Interfaces).
   - Anthropic: `claude-opus-4-6`.
   - OpenAI: `gpt-4.1` (already `openAIDefaultModel`).
   - Claude CLI: `""` (empty — the CLI resolves its own default).
2. `worker.go` removes both `const defaultModel = "claude-opus-4-6"` occurrences
   and instead reads the active provider's default. The sub-agent runner
   (`makeAgentRunner`) does the same.
3. The `claude-`-prefix gate in `resolveDefaultModel` is removed. A non-empty
   `bundle.Settings.Model` is passed through to whatever provider is active,
   regardless of prefix. (Claude CLI already had a pass-through branch; that
   collapses into the general case.)
4. `cmd/forge/agent.go` startup warning names the env var for the configured/
   detected provider, not always `ANTHROPIC_API_KEY`. With `FORGE_PROVIDER=openai`
   and no `OPENAI_API_KEY`, the warning must mention `OPENAI_API_KEY`. With no
   provider configured and no keys, the warning may list all candidates.
5. `classifier.go` auth message is provider-aware: the message names the env var
   for the provider that produced the error, or a generic "your provider's API
   key" when the provider is unknown.
   IMPLEMENTED: the classifier has no provider handle, so `authMessage` sniffs
   the lowercased error text for "openai"/"anthropic" and names that env var,
   falling back to a generic message naming both candidates. Best-effort but
   never hardcodes Anthropic.

### Phase 2 — Provider-aware lightweight models

6. Each provider supplies its own ordered cheap-model list via a
   `LightweightModels() []string` method (or optional interface). Anthropic:
   `["claude-haiku-4-5", "claude-haiku-4-5-20251001"]`. OpenAI:
   `["gpt-4.1-mini"]`. Claude CLI: `[""]` (let CLI pick).
7. `classify.go`, `session_name.go`, and `tokens/summarize.go` source their
   model list from the active provider instead of the package-level
   `types.LightweightModels`. When the provider exposes no list, fall back to
   `[""]` (provider default model).
8. The fallback chain semantics are unchanged: try each model in order; on total
   failure, classify returns `IntentTask`, naming returns `fallbackSessionName()`,
   summarize returns its existing no-summary path.
9. `types.LightweightModels` is removed (no remaining references) OR retained
   only if a callsite genuinely has no provider available; prefer removal.
   IMPLEMENTED: removed entirely. `internal/types/models_test.go` (its only
   dedicated test) was deleted; production refs in `decompose.go` and `pr.go`
   (not in the original Context list) were also migrated to
   `provider.LightweightModels(prov)`. Test fakes that key responses on model
   names now implement `types.LightweightModeler` so the helper resolves the
   expected names.

### Phase 3 — WebSearch decoupling

10. `WebSearch` no longer hard-requires `ANTHROPIC_API_KEY`. When the Anthropic
    key is present, it keeps using the Anthropic `web_search_20260209` server
    tool (current behavior). When absent but another provider is active, it must
    not return "WebSearch requires ANTHROPIC_API_KEY"; instead it returns a clear
    `IsError` ToolResult stating web search is unavailable for the active provider
    (until a provider-native or standalone backend is added).
11. The Anthropic search path remains a self-contained backend selected only when
    an Anthropic key exists — WebSearch must work as a backend even when the main
    conversation provider is OpenAI, as long as `ANTHROPIC_API_KEY` is set.

### Phase 4 — Cost tracking for all providers

12. `modelPricing` gains OpenAI entries: at minimum `gpt-4.1`, `gpt-4.1-mini`,
    `o3` (input/output per-1M USD; OpenAI has no cache-write/cache-read split —
    set `CacheWrite`/`CacheRead` to 0 or the cached-input rate where applicable).
13. `Calculate` logs a one-time warning (deduped per model, like `aliasLogOnce`)
    when it returns `0.0` for an unknown model, instead of silently zeroing.
14. `forge stats` shows non-zero costs for OpenAI usage of priced models.

### Phase 5 — Review provider centralization

15. A single helper builds the "available providers" map once and is reused by
    `selectProvider`-adjacent code, `worker.runReview`, and
    `phase/orchestrator.go`. The three independent env-sniffing blocks collapse
    into one. `collectProviders()` (worker.go) is the natural home; review and
    phase orchestrator consume the same map.
    IMPLEMENTED: the review map uses canonical lowercase keys, while
    `collectProviders()` (model listing) uses display names — so the shared
    helper is `phase.CollectReviewProviders()` (canonical keys, includes
    claude-cli), consumed by both `worker.runReview` and the phase orchestrator.
    `collectProviders()` (display names, model-listing only) is left untouched.
16. `review.modelForProvider` queries each provider's `DefaultModel()` (via the
    registry/map) instead of the hardcoded `switch`. Anthropic provider returns
    its review model, OpenAI returns `gpt-4.1`, etc. A provider absent from the
    map yields no review entry (skipped), not a Claude fallback.

### Phase 6 — Cache-control abstraction

17. Cache-breakpoint budgeting moves behind the provider. The Anthropic provider
    keeps its 4-breakpoint `maxCacheBreakpoints` strategy in `buildRequest`. The
    OpenAI provider already strips cache hints during conversion — keep that.
18. The prompt-assembly layer (`prompt.go`, `loop.go`, `registry.go`) continues
    to set `CacheControl` hints; providers that don't support caching ignore them.
    This phase is the lowest priority and may be deferred — the hints are inert
    for OpenAI today. If implemented, document that `CacheControl` is an
    advisory hint, not a contract.
    DEFERRED: not implemented. The Anthropic provider already owns its
    4-breakpoint budgeting in `buildRequest`, and the OpenAI provider already
    drops cache hints during conversion (it never reads `CacheControl`), so the
    hints are already inert for non-Anthropic providers. No code change was
    needed to satisfy the constraint; a dedicated abstraction can land later.

## Constraints

- Must not change the `LLMProvider.Chat` signature. New capabilities go on
  optional interfaces (type-assert), mirroring the existing `ModelLister`.
- Must not require `ANTHROPIC_API_KEY` for any code path when the active provider
  is OpenAI — except WebSearch's optional Anthropic backend (Phase 3).
- Must not send a `claude-` model to a non-Anthropic provider via the default
  path. Settings models pass through, but the default must come from the active
  provider.
- Must not break the existing Anthropic-only happy path: with only
  `ANTHROPIC_API_KEY` set, behavior is byte-identical to today (same default
  model, same lightweight list, same cache breakpoints, same costs).
- Must not introduce a `switch p.(type)` to pick models in classify/session-name/
  summarize — they read from the provider's optional interface, provider-agnostic.
- Must not remove `types.LightweightModels` until every reference is migrated and
  `go build ./...` is clean.
- `Calculate` must still return `0.0` for unknown models (the warning is
  additive; the return value is unchanged for backward compat).
- Each Phase must compile and pass tests independently (standalone PRs).

## Interfaces

```go
// internal/types/types.go — new optional provider capabilities.

// ModelDefaulter is an optional interface for providers that supply a default
// model when none is configured. Mirrors ModelLister.
type ModelDefaulter interface {
    DefaultModel() string
}

// LightweightModeler is an optional interface for providers that supply an
// ordered list of cheap/fast models for auxiliary calls (classification,
// session naming, summarization). Callers try each in order, falling through
// on error, and use the provider's default model when the list is empty.
type LightweightModeler interface {
    LightweightModels() []string
}
```

```go
// Provider methods (each provider file).
func (*AnthropicProvider) DefaultModel() string       // "claude-opus-4-6"
func (*AnthropicProvider) LightweightModels() []string // ["claude-haiku-4-5", "claude-haiku-4-5-20251001"]
func (*OpenAIProvider) DefaultModel() string          // "gpt-4.1"
func (*OpenAIProvider) LightweightModels() []string    // ["gpt-4.1-mini"]
func (*ClaudeCLIProvider) DefaultModel() string        // ""
func (*ClaudeCLIProvider) LightweightModels() []string // [""]
```

```go
// internal/runtime/provider/capabilities.go — package-level helpers (NOT
// free functions near each callsite, as originally sketched). They live in the
// provider package so every consumer (classify, session_name, summarize,
// decompose, pr, worker) imports one place. Both type-assert the optional
// interface and supply safe fallbacks.
func DefaultModel(p types.LLMProvider) string        // "" when no ModelDefaulter
func LightweightModels(p types.LLMProvider) []string // [""] when no/empty list

// internal/runtime/cost/cost.go — Calculate signature unchanged; adds an
// internal one-time unknown-model warning (deduped via sync.Map +
// sync.Once, mirroring aliasLogOnce).
func Calculate(model string, usage types.TokenUsage) float64

// internal/credentials/credentials.go — exposes the env-var spelling for a
// logical key so provider-aware messages don't hardcode names.
func EnvVarName(logicalKey string) string

// internal/agent/worker.go — startup-warning helpers.
func resolveProviderName() string  // canonical name selectProvider would pick
func StartupKeyWarning() string     // "" or provider-aware missing-key warning

// internal/agent/phase/orchestrator.go — single review-provider collector,
// canonical lowercase keys (anthropic/openai/claude-cli), consumed by both
// worker.runReview and the phase orchestrator's reviewer.
func CollectReviewProviders() map[string]types.LLMProvider
```

```go
// internal/review/orchestrator.go — modelForProvider is registry-driven.
// Before: func modelForProvider(name string) string  (hardcoded switch)
// After:  func modelForProvider(name string, prov types.LLMProvider) string
//   Prefers prov.DefaultModel() (types.ModelDefaulter); falls back to the
//   name heuristic only when the provider doesn't implement the interface
//   (e.g. a test fake). An empty DefaultModel() (Claude CLI) passes through.
```

## Edge Cases

1. **Provider doesn't implement `ModelDefaulter`** (e.g. a future provider):
   `providerDefaultModel` returns `""`, letting the provider's own API resolve a
   default. Expected: no panic, no `claude-` leak.

2. **`bundle.Settings.Model` set to an Anthropic ID while OpenAI is active**:
   the settings model passes through (gate removed) and OpenAI's API 404s on the
   unknown model. Expected: a clear API error surfaced via the error classifier —
   not a silent fallback to a Claude default. (User misconfiguration; surfaced,
   not hidden.)

3. **No API keys and no `claude` CLI**: `selectProvider` falls back to Anthropic
   with empty key (today's behavior). Startup warning lists the missing keys.
   First API call fails with a clear auth error naming the provider's env var.

4. **OpenAI usage of an unpriced model** (e.g. a brand-new `gpt-5`): `Calculate`
   returns `0.0` and logs the unknown-model warning exactly once for that model
   per process. `forge stats` shows `$0.00` for that model's rows.

5. **WebSearch invoked with OpenAI active and no `ANTHROPIC_API_KEY`**: returns an
   `IsError` ToolResult: "WebSearch is unavailable for the active provider"
   (no panic, no Anthropic requirement). The conversation continues.

6. **WebSearch invoked with OpenAI active but `ANTHROPIC_API_KEY` also set**:
   uses the Anthropic backend and returns results (backend independence).

7. **Lightweight list empty (`[""]`) for Claude CLI**: classify/session-name send
   an empty model string; the CLI resolves its own model. On failure, the
   existing safe fallbacks fire.

8. **Concurrent `Calculate` calls for the same unknown model**: the one-time
   warning fires exactly once (sync.Map + sync.Once), no duplicate log spam,
   no data race.

9. **`FORGE_PROVIDER=openai` set but provider name typo (`opena1`)**:
   `providerFromName` already falls back to Anthropic with a warning — unchanged.
   The startup warning still names the resolved provider's env var.
