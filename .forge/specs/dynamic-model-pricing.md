---
id: dynamic-model-pricing
status: draft
---
# Fetch and cache model pricing dynamically; stop silent $0 cost

## Description
`forge stats` reports $0.00 despite heavy usage because the cost pricing table
hardcodes only `claude-opus-4-6` while the user runs `claude-opus-4-8`. On a
pricing-map miss, `cost.Calculate` silently returns `0.0`, so every call records
a $0 row. Per maintainer guidance (issue #263 comment), pricing must NOT be
hardcoded as the source of truth: forge fetches current pricing from the LiteLLM
public price table, caches it locally at `~/.forge/pricing.json`, and falls back
to a small embedded table only when fetch+cache are both unavailable. Unknown
models become loud (log-once) instead of silently free. Stale `claude-opus-4-6`
defaults/aliases are bumped to `claude-opus-4-8`. A `forge stats --recompute`
backfill repairs the 1965 historical $0 rows.

## Context
- `internal/runtime/cost/cost.go` — `modelPricing` map, `Pricing` type,
  `Calculate`, `logAliasOnce`/`aliasLogOnce`, `isAliasModel`. Becomes the
  embedded fallback + resolver entry point.
- `internal/runtime/cost/pricing.go` (NEW) — pricing resolver: fetch LiteLLM
  JSON, parse, cache to `~/.forge/pricing.json`, load cache, fall back to
  embedded map. Holds the resolved table behind a once-loaded singleton.
- `internal/runtime/cost/pricing_test.go` (NEW) — tests for fetch/parse/cache/
  fallback using `httptest.Server` and `t.TempDir()` (no network).
- `internal/runtime/cost/cost_test.go` — add opus-4-8 + unknown-model coverage.
- `internal/runtime/cost/tracker.go` — add `Recompute` method for the backfill;
  reads each row, recomputes `cost` via `Calculate`, UPDATEs in place.
- `cmd/forge/stats.go` — wire `--recompute` flag.
- `cmd/forge/cost.go` — `CostAccumulator.Record` calls `cost.Calculate` (no
  change needed; resolver is internal to the cost package).
- `internal/agent/worker.go` — `defaultModel = "claude-opus-4-6"` (~365, ~1047)
  and `opus` alias (~1314) → `claude-opus-4-8`.
- `cmd/forge/cli.go` — `opus` alias `{"opus", "claude-opus-4-6"}` (~1206) →
  `claude-opus-4-8`; comment at ~1155.

## Behavior
- LiteLLM source URL:
  `https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json`.
  Entry shape: per-token USD floats `input_cost_per_token`,
  `output_cost_per_token`, `cache_creation_input_token_cost`,
  `cache_read_input_token_cost`. Convert to per-MTok by ×1_000_000.
  (Confirmed live 2026-06-29: `claude-opus-4-8` = Input 5.00 / Output 25.00 /
  CacheWrite 6.25 / CacheRead 0.50.)
- Pricing resolution order for a model, first hit wins:
  1. In-memory resolved table (loaded once per process).
  2. Local cache `~/.forge/pricing.json` if present and < 24h old.
  3. Network fetch from LiteLLM → on success, write cache, use it.
  4. Stale local cache (any age) if fetch failed.
  5. Embedded fallback `modelPricing` map (offline last resort).
- `Calculate(model, usage)`:
  - Resolves pricing via the order above. On a hit, computes additive cost
    (input + output + cacheWrite + cacheRead per current formula) and returns it.
  - On a complete miss (no pricing in any source), logs ONCE per model via the
    existing `sync.Map`+`sync.Once` pattern (mirroring `logAliasOnce`):
    `[cost] WARNING: no pricing found for model %q — recording $0.00; run 'forge stats --recompute' after pricing is available`.
    Returns `0.0` so tracking never blocks.
- Fetch is best-effort and MUST NOT block or fail a session: network errors,
  non-200, or parse errors fall through to the next resolution step silently
  (except the final log-once miss). Fetch timeout ≤ 5s.
- `claude-opus-4-8` resolves to a non-zero cost out of the box (via LiteLLM or,
  if offline, the embedded fallback which now includes opus-4-8).
- Stale defaults bumped: `claude-opus-4-6` → `claude-opus-4-8` in
  `internal/agent/worker.go` (defaultModel ×2, opus alias) and `cmd/forge/cli.go`
  (opus alias + comment).
- `forge stats --recompute`: iterates all `cost_records`, recomputes `cost` per
  row using current pricing, UPDATEs rows where the value changed, prints a
  summary (`recomputed N rows, M changed`). Idempotent. Off by default; never
  runs implicitly on `forge stats` open.

## Constraints
- Must NOT treat the embedded `modelPricing` map as the source of truth — it is
  the offline fallback only. The resolver prefers cache/network.
- Must NOT block, slow, or error a session on pricing fetch failure. All fetch/
  parse/cache errors are non-fatal and silent except the final unknown-model
  log-once.
- Must NOT log per-call: unknown-model and alias warnings fire at most once per
  model per process (`sync.Map` of `*sync.Once`).
- Must NOT add a third-party HTTP/JSON dependency — use `net/http` +
  `encoding/json` from stdlib.
- Must NOT run the recompute backfill implicitly; only via explicit
  `forge stats --recompute`.
- Must NOT remove existing models from the embedded fallback map.
- Must NOT change the additive cost formula or `TokenUsage` field semantics.

## Interfaces
```go
// internal/runtime/cost/pricing.go

// litellmURL is the upstream pricing source.
const litellmURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// pricingCacheTTL bounds how long a local cache is used before refetch.
const pricingCacheTTL = 24 * time.Hour

// resolvePricing returns pricing for model and whether it was found, applying
// the in-memory → cache → network → stale-cache → embedded resolution order.
// Loads/refreshes the in-memory table on first call (sync.Once).
func resolvePricing(model string) (Pricing, bool)

// litellmEntry mirrors the per-token fields of a LiteLLM model record.
type litellmEntry struct {
    InputCostPerToken            float64 `json:"input_cost_per_token"`
    OutputCostPerToken           float64 `json:"output_cost_per_token"`
    CacheCreationInputTokenCost  float64 `json:"cache_creation_input_token_cost"`
    CacheReadInputTokenCost      float64 `json:"cache_read_input_token_cost"`
}

// fetchLiteLLM fetches and parses the upstream table into per-MTok Pricing.
// Injectable URL + client for tests.
func fetchLiteLLM(ctx context.Context, client *http.Client, url string) (map[string]Pricing, error)

// pricingCachePath returns ~/.forge/pricing.json.
func pricingCachePath() (string, error)

// internal/runtime/cost/cost.go (changed)
func Calculate(model string, usage types.TokenUsage) float64

// internal/runtime/cost/tracker.go (new method)
// Recompute recalculates cost for every row using current pricing, updating
// rows whose cost changed. Returns (rowsScanned, rowsChanged, error).
func (t *Tracker) Recompute() (scanned int, changed int, err error)
```

## Edge Cases
- Network down / LiteLLM 404: fetch fails → use stale local cache if present,
  else embedded fallback. No session error; opus-4-8 still priced via embedded
  fallback.
- Malformed cache JSON at `~/.forge/pricing.json`: parse error treated as
  cache-miss; proceed to network/embedded. Do not crash; do not delete the file
  unless re-fetch succeeds (overwrite on success).
- Model present in LiteLLM with zero/absent cache cost fields: missing JSON keys
  decode to 0.0 → cache costs contribute $0, input/output still priced.
  Acceptable (matches upstream).
- Unknown model in NO source (typo, brand-new unlisted model): `Calculate`
  returns 0.0 and logs the miss exactly once per model. Tracking proceeds.
- `forge stats --recompute` on a DB containing models still unpriced: those rows
  recompute to 0 (unchanged), counted in scanned but not changed; no error.
- Concurrent first `Calculate` calls across goroutines: resolver load guarded by
  `sync.Once` so the table loads exactly once; no duplicate fetches/races.
- Cache file in a read-only `~/.forge`: cache write fails silently; in-memory
  table from network/embedded still used for the process lifetime.

## Alternatives
- Hardcode a `claude-opus-4-8` entry only (issue's original suggestion #1): fast
  but recurs on every new model release. Rejected per maintainer comment
  requiring dynamic, cached pricing. The embedded map is retained ONLY as the
  offline fallback.
- Bundle/vendor the LiteLLM JSON at build time: avoids runtime fetch but goes
  stale at release boundaries and bloats the binary. Rejected; live fetch +
  local cache keeps prices current without redeploys.
