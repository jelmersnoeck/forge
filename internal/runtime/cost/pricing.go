package cost

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// litellmURL is the upstream pricing source. Overridable via the
// LITELLM_MODEL_COST_MAP_URL env var (mirroring litellm's own convention) so
// tests can point at a local httptest.Server.
const litellmURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// litellmURLEnv is the env var that overrides litellmURL.
const litellmURLEnv = "LITELLM_MODEL_COST_MAP_URL"

// pricingCacheTTL bounds how long a fresh local cache is trusted before refetch.
const pricingCacheTTL = 24 * time.Hour

// pricingFetchTimeout caps the best-effort network fetch. Fetch failure must
// never block or error a session — it falls through to cache then embed.
const pricingFetchTimeout = 3 * time.Second

// tokensPerMillion converts litellm per-token USD costs to forge's per-1M-token
// Pricing struct (multiply each litellm value by 1e6).
const tokensPerMillion = 1_000_000.0

// embeddedPrices is a snapshot of the litellm table, the offline source of
// truth. Cost calculation works with zero network and in tests/air-gapped runs.
//
//go:embed model_prices.json
var embeddedPrices []byte

// litellmEntry mirrors the per-token USD fields of a litellm model record.
// Costs are per single token, NOT per million. Cache fields may be null/absent
// (e.g. OpenAI entries) and decode to 0.
type litellmEntry struct {
	InputCostPerToken           float64 `json:"input_cost_per_token"`
	OutputCostPerToken          float64 `json:"output_cost_per_token"`
	CacheCreationInputTokenCost float64 `json:"cache_creation_input_token_cost"`
	CacheReadInputTokenCost     float64 `json:"cache_read_input_token_cost"`
}

// pricingOnce guards a single load/refresh of the in-memory table per process.
var pricingOnce sync.Once

// resolvedPrices is the in-memory pricing table, loaded once via pricingOnce.
var resolvedPrices map[string]Pricing

// resolvePricing returns pricing for model and whether it was found, applying
// the cache → network → stale-cache → embedded resolution order on first call.
// The in-memory table is loaded exactly once per process via sync.Once.
func resolvePricing(model string) (Pricing, bool) {
	pricingOnce.Do(loadPricing)
	p, ok := resolvedPrices[model]
	return p, ok
}

// loadPricing populates resolvedPrices using, in priority order:
//  1. Fresh local cache (~/.forge/model_prices.json, < TTL).
//  2. Network fetch from litellm → on success, write cache, use it.
//  3. Stale local cache (any age) if fetch failed.
//  4. Embedded snapshot (offline last resort, always succeeds).
//
// Every step is best-effort; failures fall through silently. The embedded
// snapshot guarantees a non-nil table.
func loadPricing() {
	cachePath, cacheErr := pricingCachePath()

	// 1. Fresh cache.
	if cacheErr == nil {
		if table, ok := loadFreshCache(cachePath); ok {
			resolvedPrices = table
			return
		}
	}

	// 2. Network fetch.
	ctx, cancel := context.WithTimeout(context.Background(), pricingFetchTimeout)
	defer cancel()
	if table, err := fetchLiteLLM(ctx, http.DefaultClient, pricingURL()); err == nil && len(table) > 0 {
		if cacheErr == nil {
			writeCache(cachePath, table)
		}
		resolvedPrices = table
		return
	}

	// 3. Stale cache (any age).
	if cacheErr == nil {
		if table, ok := loadCache(cachePath); ok {
			resolvedPrices = table
			return
		}
	}

	// 4. Embedded snapshot.
	if table, err := parseLiteLLM(embeddedPrices); err == nil {
		resolvedPrices = table
		return
	}

	// Should never happen: embedded snapshot is valid at build time.
	resolvedPrices = map[string]Pricing{}
}

// pricingURL returns the litellm source URL, honoring the env override.
func pricingURL() string {
	if u := os.Getenv(litellmURLEnv); u != "" {
		return u
	}
	return litellmURL
}

// pricingCachePath returns ~/.forge/model_prices.json.
func pricingCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, ".forge", "model_prices.json"), nil
}

// loadFreshCache loads the cache only if it exists and is younger than the TTL.
func loadFreshCache(path string) (map[string]Pricing, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if time.Since(info.ModTime()) >= pricingCacheTTL {
		return nil, false
	}
	return loadCache(path)
}

// loadCache reads and parses the cache file at any age. A read or parse error
// is treated as a cache miss (returns false), never a crash.
func loadCache(path string) (map[string]Pricing, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	table, err := parseLiteLLM(data)
	if err != nil || len(table) == 0 {
		return nil, false
	}
	return table, true
}

// writeCache persists the raw litellm table to the cache path. The on-disk
// format mirrors litellm (per-token costs) so a reload re-normalizes
// identically. Write failure (e.g. read-only ~/.forge) is silently ignored.
func writeCache(path string, table map[string]Pricing) {
	raw := make(map[string]litellmEntry, len(table))
	for model, p := range table {
		raw[model] = litellmEntry{
			InputCostPerToken:           p.Input / tokensPerMillion,
			OutputCostPerToken:          p.Output / tokensPerMillion,
			CacheCreationInputTokenCost: p.CacheWrite / tokensPerMillion,
			CacheReadInputTokenCost:     p.CacheRead / tokensPerMillion,
		}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// fetchLiteLLM fetches and parses the upstream table into per-1M-token Pricing.
// The client and url are injectable for tests.
func fetchLiteLLM(ctx context.Context, client *http.Client, url string) (map[string]Pricing, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch pricing: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch pricing: status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return parseLiteLLM(data)
}

// parseLiteLLM decodes the litellm JSON into a per-1M-token Pricing table.
// It skips the non-model "sample_spec" key and any entry lacking an
// input_cost_per_token. Null/absent cache fields decode to 0.
func parseLiteLLM(data []byte) (map[string]Pricing, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse pricing: %w", err)
	}

	table := make(map[string]Pricing, len(raw))
	for model, msg := range raw {
		if model == "sample_spec" {
			continue
		}
		var e litellmEntry
		if err := json.Unmarshal(msg, &e); err != nil {
			// Skip malformed entries rather than failing the whole table.
			continue
		}
		if e.InputCostPerToken == 0 && e.OutputCostPerToken == 0 {
			// No usable token pricing (e.g. embedding/audio-only records).
			continue
		}
		table[model] = Pricing{
			Input:      e.InputCostPerToken * tokensPerMillion,
			Output:     e.OutputCostPerToken * tokensPerMillion,
			CacheWrite: e.CacheCreationInputTokenCost * tokensPerMillion,
			CacheRead:  e.CacheReadInputTokenCost * tokensPerMillion,
		}
	}
	return table, nil
}
