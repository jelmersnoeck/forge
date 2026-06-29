---
id: provider-router
status: draft
---
# Route each request to the provider that owns the selected model

## Description

The worker binds to a single provider at session start (`selectProvider()`,
`worker.go:321`) and never re-routes when the model changes. `/model` and
`--model` can switch to a model owned by a *different* provider (e.g. `gpt-4.1`),
but the worker keeps calling the startup provider — so the request hits the wrong
API and fails or silently uses the wrong model. Provider availability is also
built three times from env vars (`worker.collectProviders`,
`phase.CollectReviewProviders`, `worker.selectProvider`).

This spec introduces a **provider router** in `internal/runtime/provider` that
(1) owns the available-provider set, built once from env/config, and (2) resolves
a model name to the provider that serves it. The worker's main loop consults the
router each turn so cross-provider `/model` switches route correctly. The router
replaces the duplicated map-building.

Extends `eliminate-provider-coupling` (which added `DefaultModel`/
`LightweightModels` optional interfaces and centralized review providers into
`phase.CollectReviewProviders`). This spec reuses those interfaces and folds the
duplicated provider construction into the router. GitHub issue #232.

## Context

Files that change:
- `internal/runtime/provider/router.go` — NEW: `Router` type. Builds available
  providers from env/config (mirrors `selectProvider`/`collectProviders`/
  `CollectReviewProviders` logic), resolves model→provider, exposes the default
  provider. Lives in the provider package so worker, phase, and review consume it.
- `internal/runtime/provider/router_test.go` — NEW: table-driven router tests.
- `internal/agent/worker.go`:
  - `Worker` struct (`~60`): replace the `providers map[string]types.LLMProvider`
    field with a `*provider.Router` (or hold the router alongside).
  - `Run()` (`321`–`328`): build one `*provider.Router`; derive `prov` from it.
  - Main loop (`385`–`407`): after `model := w.resolveModel(...)`, route the model
    to its owning provider via the router and use that `prov` for the turn,
    sub-agent runner, review, and PR steps within that turn.
  - `selectProvider()` (`1128`), `providerFromName()` (`1159`),
    `collectProviders()` (`1240`): collapse into router construction; keep
    `providerFromName` only if the router reuses it internally.
  - `resolveProviderName()`/`canonicalProviderName()`/`StartupKeyWarning()`
    (`1189`–`1236`): unchanged behavior, but may source the canonical name from
    the router.
- `internal/agent/phase/orchestrator.go` — `CollectReviewProviders()` (`30`):
  delegate to (or be replaced by) the router's provider set.
- `internal/agent/server.go` — `handleSetModel` (`141`): unchanged signature; the
  re-route happens lazily in the worker loop, not in the handler.
- `internal/review/orchestrator.go` — `modelForProvider` already
  registry-driven; consumes the router's map.

Reference (do not modify behavior):
- `internal/runtime/provider/capabilities.go` — `DefaultModel(p)` /
  `LightweightModels(p)` helpers.
- `internal/types/types.go` — `LLMProvider`, `ModelDefaulter`, `ModelLister`.
- `internal/credentials/credentials.go` — `AnthropicAPIKey`, `OpenAIAPIKey`.
- `internal/agent/worker.go:1390` — `modelAliases` (`opus`/`sonnet`/`haiku`).

## Behavior

1. A `provider.Router` is constructed once per worker `Run()` from the same
   inputs `selectProvider` uses today: `FORGE_PROVIDER`, `~/.forge/config.toml`
   `[provider].default`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, and `claude` on
   PATH. The router records which providers are available and which is the
   configured/auto-detected **default**.

2. `Router.Resolve(model string) (types.LLMProvider, error)` maps a model name
   to the provider that serves it:
   - `claude-*` and the aliases `opus`/`sonnet`/`haiku` → the Anthropic-family
     default among capable providers (Anthropic API if its key is set, else
     Claude CLI if available).
   - `gpt-*`, `o1*`, `o3*`, `o4*` (OpenAI naming) → the OpenAI provider.
   - Empty string → the router's default provider (a `/model` clear or a
     provider that resolves its own default, e.g. Claude CLI).
   - A model whose owning provider is **not available** (e.g. `gpt-4.1` with no
     `OPENAI_API_KEY`) → returns a non-nil error naming the missing provider/key;
     the caller surfaces it, never silently routes to the wrong provider.

3. `Router.Default() types.LLMProvider` returns the default provider (the one
   `selectProvider` would pick). With only `ANTHROPIC_API_KEY` set, this is the
   Anthropic provider — identical to today.

4. The worker main loop, per turn, calls
   `prov, err := router.Resolve(model)`. On success the turn (and its sub-agent
   runner, review, PR steps) uses that `prov`. On error the turn falls back to
   `router.Default()` AND emits a `warning` event explaining the model couldn't
   be routed (so it is never a silent wrong-provider call). The same routing
   failure is ALSO written to the structured server log (`slog.Warn`) with
   stable key/value attributes (`model`, `resolved_provider`, `reason`) so
   misrouting is observable for operational monitoring, not only as an
   end-user event. The user-facing `warning` event and the log line are emitted
   from the same code path so they never diverge.

5. Switching to a cross-provider model mid-session via `/model gpt-4.1` (with
   `OPENAI_API_KEY` set) routes the *next* turn to OpenAI. Switching back to a
   `claude-*` model routes the following turn back to Anthropic. No restart.

6. `--model gpt-4.1` at startup (with `OPENAI_API_KEY` set) resolves to the
   OpenAI provider on the first turn.

7. Provider availability is built in exactly one place (the router) and reused by
   the main loop, the review path (`worker.runReview`), and the phase
   orchestrator. The three independent env-sniffing blocks
   (`collectProviders`, `CollectReviewProviders`, `selectProvider`) collapse:
   the router exposes the maps each consumer needs.

8. The Claude CLI provider's persistent process must still be `Close()`d on
   worker shutdown. If the router constructs multiple providers, the worker
   closes every `io.Closer` provider the router built, not just the startup one.

## Constraints

- Must not change the `LLMProvider.Chat` signature; routing keys off model-name
  rules + the existing optional interfaces, not a new method on `Chat`.
- Must not route a `claude-*` model to OpenAI or a `gpt-*`/`o*` model to
  Anthropic. An unroutable/unavailable model returns an error or falls back to
  the default with a warning — never a silent wrong-API call.
- Must not require `OPENAI_API_KEY` when the active model is Anthropic, nor
  `ANTHROPIC_API_KEY` when the active model is OpenAI.
- Must not break the Anthropic-only happy path: with only `ANTHROPIC_API_KEY`
  set, `router.Default()` is the Anthropic provider and every turn behaves
  byte-identically to today (same model resolution, same single provider).
- Must construct each available provider at most once per worker run (no
  per-turn provider allocation churn); `Resolve` returns cached instances.
- Must resolve aliases (`opus`/`sonnet`/`haiku`) to `claude-*` before/while
  classifying ownership — an alias must route to the Anthropic family, not be
  treated as unknown.
- `handleSetModel` must not block on or perform routing — it only stores the
  model string; the loop re-routes lazily next turn (keeps the HTTP handler fast
  and avoids constructing providers on the request goroutine).
- Model-family classification rules must live in ONE table-driven definition,
  not be duplicated as inline string literals across `modelFamily`, the worker,
  and review code. The aliases (`opus`/`sonnet`/`haiku`) reuse the existing
  `modelAliases` map (`worker.go:1390`) — do not re-spell them in the router.
  Prefix sets (`claude-`, `gpt-`, `o1`, `o3`, `o4`) live in a single
  package-level slice/map in `router.go`. Adding a new family or prefix is a
  one-line table edit, touching no other code.
- `modelFamily` must match only on a normalized model string: lowercase the
  input and match against the prefix table / alias map exactly. It must NOT do
  loose substring matching (`strings.Contains`), which would let a spoofed name
  like `my-claude-jailbreak` or `not-gpt-real` map to a privileged family. An
  alias matches only as a whole-string equality; a prefix matches only at
  position 0 (`strings.HasPrefix` on the normalized string). Anything that does
  not match a known alias or a position-0 prefix is `unknownFamily` (routed to
  the default with a warning), never coerced into a family by partial overlap.
- Classification is for ROUTING only — it selects which configured provider's
  API receives the call. It is not a trust/authorization boundary: a user who
  can set the model can already reach any configured provider, so a misclassified
  name causes at most a wrong-but-surfaced API error (404/auth), never access to
  a provider whose credentials are absent (an unavailable provider yields an
  error or default fallback, never a silent privileged call).

## Interfaces

```go
// internal/runtime/provider/router.go

// Router owns the set of available LLM providers and resolves a model name to
// the provider that serves it. Constructed once per session from env/config.
type Router struct {
    // unexported: cached provider instances + the canonical default name.
}

// NewRouter builds the router from FORGE_PROVIDER, user config, and detected
// credentials/CLI, mirroring selectProvider's priority for the default.
func NewRouter() *Router

// Resolve returns the provider that owns the given model. An empty model
// returns the default provider. Returns a non-nil error when the model maps to
// a provider that is not available (e.g. gpt-4.1 with no OPENAI_API_KEY).
func (r *Router) Resolve(model string) (types.LLMProvider, error)

// Default returns the configured/auto-detected default provider (the provider
// selectProvider would have picked).
func (r *Router) Default() types.LLMProvider

// Providers returns available providers keyed by canonical name
// (anthropic, openai, claude-cli) — consumed by the review path and the phase
// orchestrator, replacing CollectReviewProviders' own env-sniffing.
func (r *Router) Providers() map[string]types.LLMProvider

// Closers returns every constructed provider implementing io.Closer so the
// worker can shut down persistent processes (e.g. Claude CLI) on exit.
func (r *Router) Closers() []io.Closer
```

```go
// internal/runtime/provider/router.go — model-ownership classification.
// Family is an internal enum: anthropicFamily | openAIFamily | unknownFamily.
//
// Classification is table-driven, NOT a chain of inline string checks. Two
// package-level definitions are the single source of truth; adding a model
// family or prefix is a one-line edit here and nowhere else:
//
//   // familyPrefixes maps a lowercase model-name prefix to its family.
//   // Matched with strings.HasPrefix at position 0 only — never substring.
//   var familyPrefixes = []struct {
//       prefix string
//       fam    family
//   }{
//       {"claude-", anthropicFamily},
//       {"gpt-", openAIFamily},
//       {"o1", openAIFamily},
//       {"o3", openAIFamily},
//       {"o4", openAIFamily},
//   }
//   // Aliases reuse worker.modelAliases (opus/sonnet/haiku -> claude-*); the
//   // router resolves the alias to its claude-* target first, then prefixes
//   // apply. The router does NOT re-spell the alias list.
//
// modelFamily lowercases model, resolves any whole-string alias, then matches
// the prefix table at position 0. No strings.Contains. A spoofed name such as
// "my-claude-x" or "not-gpt" does not match (the privileged prefix is not at
// position 0) and falls through to unknownFamily.
func modelFamily(model string) family
//   - "" -> unknownFamily (caller uses default)
//   - whole-string opus/sonnet/haiku alias, or position-0 "claude-" prefix
//     -> anthropicFamily
//   - position-0 "gpt-", "o1", "o3", "o4" prefix -> openAIFamily
//   - anything else (incl. substring-only matches) -> unknownFamily
//     (route to default + warn)
```

## Edge Cases

1. **`/model gpt-4.1` with `OPENAI_API_KEY` unset**: `Resolve("gpt-4.1")` returns
   an error naming `OPENAI_API_KEY`. The worker emits a `warning` event and runs
   the turn on `router.Default()` (Anthropic) — no silent OpenAI call, no panic.

2. **`/model gpt-4.1` with `OPENAI_API_KEY` set, started on Anthropic**: the next
   turn routes to OpenAI; cost tracking, lightweight calls, and review use the
   OpenAI provider for that turn. Switching back to `sonnet` routes the following
   turn to Anthropic.

3. **Both Anthropic API key and `claude` CLI available, model `claude-opus-4-6`**:
   routes to the configured/auto-detected default among the two (Anthropic API
   when its key is set; honors `FORGE_PROVIDER=claude-cli` / config override).
   Never errors just because two providers can serve it.

4. **Empty model string (`/model` cleared or first turn with no override)**:
   `Resolve("")` returns `router.Default()`. With Claude CLI as default, the CLI
   resolves its own model (empty model string passed through).

5. **Unknown model family (e.g. `mistral-large`)**: `modelFamily` returns
   unknown; `Resolve` returns the default provider with a warning, NOT an error
   — the model string still passes through to the default provider's API, which
   surfaces any 404 itself (user misconfiguration, surfaced not hidden).

6. **Only `ANTHROPIC_API_KEY` set (status-quo session)**: router has one
   provider; `Default()` and every `Resolve` of a claude model return it.
   Behavior is byte-identical to pre-router code — no extra providers built, no
   new warnings.

7. **No keys and no `claude` CLI**: router's default is Anthropic with an empty
   key (today's `selectProvider` fallback). First API call fails with the
   existing provider-aware auth error; `StartupKeyWarning` still fires.

8. **Concurrent `SetModel` (HTTP goroutine) + `Resolve` (worker loop)**: model
   read is already guarded by `modelMu` in `ModelOverride()`; routing happens on
   the worker goroutine using the snapshot read at turn start — no new data race.

9. **Claude CLI default + an OpenAI model switch + shutdown**: both providers
   were constructed; `router.Closers()` closes the CLI process exactly once on
   worker exit. No leaked process, no double-close.

10. **Spoofed/substring model name (e.g. `my-claude-jailbreak`, `not-gpt-4`,
    `claude` inside a longer token)**: `modelFamily` matches aliases only as a
    whole string and prefixes only at position 0, so these do NOT match a
    privileged family — they fall to `unknownFamily` and route to the default
    provider with a warning. No coercion into the Anthropic/OpenAI family via
    partial overlap, no call to a provider the name merely resembles.

11. **Case variants (`Claude-Opus-4-6`, `GPT-4.1`, `OPUS`)**: the input is
    lowercased before matching, so casing never changes the resolved family.
