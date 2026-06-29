---
id: dynamic-model-pricing
status: implemented
---
# Dynamic litellm-sourced model pricing; stop silent $0 cost on alias models

## Description
`forge stats` and the status line reported `$0.00` despite millions of tokens
because the model-alias map and the hardcoded cost-pricing map were out of sync:
the `sonnet`/`haiku` aliases resolved to model IDs absent from `modelPricing`, so
`cost.Calculate` fell through to its unknown-model branch and returned `0.0`
(silent failure — warning went only to the agent log). Fix (issue #278): source
pricing from the maintained litellm table (`go:embed` snapshot + best-effort
network refresh cached at `~/.forge/model_prices.json`), keep a tiny supplemental
map only for models litellm omits, fix the bogus `haiku` alias target, and make a
$0-on-nonzero-tokens state user-visible. Supersedes the original draft's
opus-4-6→4-8 bump and `--recompute` backfill — neither was needed for #278.

## Context
- `internal/runtime/cost/cost.go` — replaced the static `modelPricing` map with
  `supplementalPricing` (litellm-gap fallback only) + `LookupPricing` (litellm
  table first, supplemental second) + `Calculate` (signature unchanged). Kept
  `Pricing`, `isAliasModel`, `logAliasOnce`, `logUnknownModelOnce`, `Format*`.
- `internal/runtime/cost/pricing.go` (NEW) — litellm resolver: `resolvePricing`
  (sync.Once-guarded load), `fetchLiteLLM`, `parseLiteLLM`, cache load/write,
  TTL check. Holds resolved table in `resolvedPrices`.
- `internal/runtime/cost/model_prices.json` (NEW) — embedded litellm snapshot
  (~1.5MB, 2918 keys), offline source of truth via `//go:embed`.
- `internal/runtime/cost/pricing_test.go` (NEW) — parse/fetch/cache/TTL/fallback
  tests using `httptest.Server` + `t.TempDir()`, fully offline.
- `internal/runtime/cost/cost_test.go` — added `TestMain` forcing offline
  resolution, sonnet-priced regression, alias-target CI guard.
- `cmd/forge/cost.go` — `CostAccumulator.Record` surfaces a one-time
  user-visible warning when cost is $0 on non-zero tokens for an unpriced model.
- `cmd/forge/cost_test.go` — warning-path coverage.
- `internal/agent/worker.go` — `modelAliases` haiku target fixed
  `claude-haiku-4-20250506` → `claude-haiku-4-5-20251001`.
- `cmd/forge/cli.go` — `modelAliasesForDisplay` haiku target same fix.
- `internal/agent/worker_test.go` — updated haiku expectation; added
  `TestModelAliasesArePriced` CI guard.
- `internal/review/orchestrator.go` — default `claude-sonnet-4-20250514`
  unchanged (now priced via litellm, so no longer invisible).

## Behavior
- litellm source URL:
  `https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json`.
  Per-model costs are per-token USD; converted to per-1M by ×1_000_000 on load.
- URL overridable via the `LITELLM_MODEL_COST_MAP_URL` env var (mirrors litellm).
- Pricing resolution order (first hit wins), loaded once per process via
  `sync.Once`:
  1. Fresh local cache `~/.forge/model_prices.json` (< 24h by mtime).
  2. Network fetch (3s timeout) → on success, write cache, use it.
  3. Stale local cache (any age) if fetch failed.
  4. Embedded snapshot (offline last resort; always valid).
- `LookupPricing(model)` consults the loaded litellm table first, then
  `supplementalPricing` (litellm-gap models like the retired Claude 3/3.5 dated
  IDs). Returns `(Pricing, found bool)`.
- `Calculate(model, usage)` — unchanged signature; resolves via `LookupPricing`,
  computes additive cost (input+output+cacheWrite+cacheRead). On a complete miss
  it logs once per model and returns `0.0` (tracking never blocks).
- `claude-sonnet-4-20250514` (sonnet alias + review default) now resolves to a
  non-zero cost (the exact #278 repro), both online and offline.
- `haiku` alias fixed to `claude-haiku-4-5-20251001` (the prior
  `claude-haiku-4-20250506` exists in neither worker nor litellm).
- `CostAccumulator.Record`: when the computed cost is `0` AND the call had
  non-zero tokens AND the model is unpriced, returns a one-time (per model,
  per process) user-visible warning string. Priced models and zero-token calls
  return `""`.
- Fetch is best-effort: network errors, non-200, or parse errors fall through to
  the next resolution step. Never blocks/errors a session.

## Constraints
- Must NOT treat the supplemental/embedded map as the primary source — the
  litellm table (cache/network/embedded snapshot) is primary; supplemental is a
  gap-filler for IDs litellm lacks.
- Must NOT block, slow, or error a session on pricing fetch failure. All fetch/
  parse/cache errors are non-fatal.
- Must NOT log/warn per-call: unknown-model, alias, and unpriced-$0 warnings fire
  at most once per model per process (`sync.Once` / `sync.Map`).
- Must NOT add a third-party HTTP/JSON dependency — stdlib `net/http` +
  `encoding/json` only.
- Must NOT change the additive cost formula or `TokenUsage` field semantics.
- Must NOT remove models still needed for historical lookups (kept the Claude
  3/3.5 dated IDs in `supplementalPricing` since litellm omits them).
- All cost tests MUST pass offline (embedded snapshot); live fetch is exercised
  only against `httptest.Server`.

## Interfaces
```go
// internal/runtime/cost/pricing.go
const litellmURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
const litellmURLEnv = "LITELLM_MODEL_COST_MAP_URL"
const pricingCacheTTL = 24 * time.Hour
const pricingFetchTimeout = 3 * time.Second

//go:embed model_prices.json
var embeddedPrices []byte

type litellmEntry struct {
    InputCostPerToken           float64 `json:"input_cost_per_token"`
    OutputCostPerToken          float64 `json:"output_cost_per_token"`
    CacheCreationInputTokenCost float64 `json:"cache_creation_input_token_cost"`
    CacheReadInputTokenCost     float64 `json:"cache_read_input_token_cost"`
}

func resolvePricing(model string) (Pricing, bool) // sync.Once-guarded load
func fetchLiteLLM(ctx context.Context, client *http.Client, url string) (map[string]Pricing, error)
func parseLiteLLM(data []byte) (map[string]Pricing, error) // skips sample_spec + no-token-cost entries
func pricingCachePath() (string, error)                    // ~/.forge/model_prices.json

// internal/runtime/cost/cost.go (changed)
func LookupPricing(model string) (Pricing, bool) // litellm table then supplemental
func Calculate(model string, usage types.TokenUsage) float64 // signature unchanged

// cmd/forge/cost.go (changed)
func (c *CostAccumulator) Record(ev types.OutboundEvent, t *cost.Tracker, sessionID string) (warning string)
// returns a one-time user-visible warning when cost==0 on non-zero tokens for an unpriced model
```

## Edge Cases
- Network down / litellm 404: fetch fails → stale cache if present, else
  embedded snapshot. sonnet/haiku/opus still priced offline. No session error.
- Malformed cache JSON: parse error treated as cache-miss; proceed to
  network/embedded. File overwritten only on a successful fetch, never deleted.
- litellm entry with null/absent cache cost fields (e.g. OpenAI): cache costs
  decode to 0; input/output still priced.
- Entry lacking any token cost (embedding/audio-only records): skipped by
  `parseLiteLLM`; `sample_spec` key skipped too.
- Model present in NO source (typo/brand-new unlisted): `Calculate` returns 0.0,
  logs once; `Record` emits a one-time user-visible $0-with-tokens warning.
- litellm-missing-but-needed dated models (Claude 3.5 sonnet/haiku 2024 IDs):
  covered by `supplementalPricing` so historical cost rows stay priced.
- Concurrent first `Calculate`/`resolvePricing` across goroutines: table loads
  exactly once (`sync.Once`); no duplicate fetches/races.
- Read-only `~/.forge`: cache write fails silently; in-memory table
  (network/embedded) still used for the process lifetime.
- haiku alias target absent from worker AND litellm (the original
  `claude-haiku-4-20250506` bug): fixed to a real priced ID; CI guard
  (`TestModelAliasesArePriced`, `TestAllAliasTargetsPriced`) prevents recurrence.

## Alternatives
- Hardcode a `claude-opus-4-8`/missing-model entry only (issue's original
  suggestion): fast but recurs on every model release. Rejected per maintainer
  guidance requiring dynamic, cached pricing. The embedded snapshot + supplemental
  map are retained ONLY as offline/gap fallbacks.
- Bundle/vendor the litellm JSON at build time without runtime refresh: goes
  stale at release boundaries. Rejected; embed + best-effort network refresh +
  local cache keeps prices current without redeploys while staying offline-safe.
- `forge stats --recompute` historical backfill (from the original draft):
  dropped from scope for #278 — the live fix prevents new $0 rows; backfilling
  old rows can be a follow-up if desired.
- `claude-opus-4-6 → claude-opus-4-8` default bump (from the original draft):
  dropped — opus-4-6 is present and priced in litellm; not part of the #278
  alias-desync bug.
