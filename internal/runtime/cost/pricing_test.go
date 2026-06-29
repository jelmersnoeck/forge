package cost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// resetPricingForTest clears the once-loaded singleton so a test can force a
// fresh resolution path. Test-only.
func resetPricingForTest() {
	pricingOnce = sync.Once{}
	resolvedPrices = nil
}

const sampleLiteLLM = `{
  "sample_spec": {"input_cost_per_token": 9.99, "litellm_provider": "x"},
  "claude-sonnet-4-20250514": {
    "input_cost_per_token": 3e-06,
    "output_cost_per_token": 1.5e-05,
    "cache_creation_input_token_cost": 3.75e-06,
    "cache_read_input_token_cost": 3e-07,
    "litellm_provider": "anthropic"
  },
  "gpt-4.1": {
    "input_cost_per_token": 2e-06,
    "output_cost_per_token": 8e-06,
    "cache_creation_input_token_cost": null,
    "cache_read_input_token_cost": 5e-07,
    "litellm_provider": "openai"
  },
  "embedding-only": {"litellm_provider": "openai", "mode": "embedding"}
}`

func TestParseLiteLLM(t *testing.T) {
	r := require.New(t)

	table, err := parseLiteLLM([]byte(sampleLiteLLM))
	r.NoError(err)

	// sample_spec is skipped.
	_, ok := table["sample_spec"]
	r.False(ok, "sample_spec must be skipped")

	// embedding-only has no token cost -> skipped.
	_, ok = table["embedding-only"]
	r.False(ok, "entry without token cost must be skipped")

	// Per-token -> per-1M conversion.
	son := table["claude-sonnet-4-20250514"]
	r.InDelta(3.00, son.Input, 1e-9)
	r.InDelta(15.00, son.Output, 1e-9)
	r.InDelta(3.75, son.CacheWrite, 1e-9)
	r.InDelta(0.30, son.CacheRead, 1e-9)

	// null cache-write coerces to 0.
	gpt := table["gpt-4.1"]
	r.InDelta(2.00, gpt.Input, 1e-9)
	r.InDelta(0.0, gpt.CacheWrite, 1e-9)
	r.InDelta(0.50, gpt.CacheRead, 1e-9)
}

func TestParseLiteLLMMalformed(t *testing.T) {
	r := require.New(t)
	_, err := parseLiteLLM([]byte("not json"))
	r.Error(err)
}

func TestEmbeddedSnapshotPricesAliasTargets(t *testing.T) {
	r := require.New(t)

	table, err := parseLiteLLM(embeddedPrices)
	r.NoError(err)

	// The sonnet alias target must be priced by the embedded snapshot — the
	// exact bug in issue #278 (sonnet resolved to $0).
	son, ok := table["claude-sonnet-4-20250514"]
	r.True(ok, "sonnet alias target missing from embedded snapshot")
	r.Greater(son.Input, 0.0)
	r.Greater(son.Output, 0.0)
}

func TestFetchLiteLLM(t *testing.T) {
	r := require.New(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sampleLiteLLM))
	}))
	defer srv.Close()

	table, err := fetchLiteLLM(context.Background(), srv.Client(), srv.URL)
	r.NoError(err)
	r.Contains(table, "claude-sonnet-4-20250514")
	r.InDelta(3.00, table["claude-sonnet-4-20250514"].Input, 1e-9)
}

func TestFetchLiteLLMNon200(t *testing.T) {
	r := require.New(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := fetchLiteLLM(context.Background(), srv.Client(), srv.URL)
	r.Error(err)
}

func TestLoadPricingFreshCache(t *testing.T) {
	r := require.New(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	// Write a fresh cache the loader should trust ahead of network/embedded.
	forgeDir := filepath.Join(home, ".forge")
	r.NoError(os.MkdirAll(forgeDir, 0o755))
	cache := `{"troy-barnes-model": {"input_cost_per_token": 1e-05, "output_cost_per_token": 2e-05}}`
	r.NoError(os.WriteFile(filepath.Join(forgeDir, "model_prices.json"), []byte(cache), 0o644))

	resetPricingForTest()
	t.Cleanup(resetPricingForTest)

	p, ok := resolvePricing("troy-barnes-model")
	r.True(ok, "fresh cache entry must win")
	r.InDelta(10.00, p.Input, 1e-9)
	r.InDelta(20.00, p.Output, 1e-9)
}

func TestLoadPricingNetworkThenEmbedded(t *testing.T) {
	r := require.New(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	// No cache present. Point the URL at a live server returning a custom model.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"greendale-model": {"input_cost_per_token": 4e-06, "output_cost_per_token": 8e-06}}`))
	}))
	defer srv.Close()
	t.Setenv(litellmURLEnv, srv.URL)

	resetPricingForTest()
	t.Cleanup(resetPricingForTest)

	p, ok := resolvePricing("greendale-model")
	r.True(ok, "network entry must be used when no fresh cache")
	r.InDelta(4.00, p.Input, 1e-9)

	// Network result is cached to disk for next process.
	_, err := os.Stat(filepath.Join(home, ".forge", "model_prices.json"))
	r.NoError(err, "successful fetch must write the cache")
}

func TestLoadPricingFetchFailFallsToEmbedded(t *testing.T) {
	r := require.New(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	// Unreachable URL forces fetch failure -> embedded fallback.
	t.Setenv(litellmURLEnv, "http://127.0.0.1:1/nope")

	resetPricingForTest()
	t.Cleanup(resetPricingForTest)

	// Embedded snapshot prices the sonnet alias target.
	p, ok := resolvePricing("claude-sonnet-4-20250514")
	r.True(ok, "embedded fallback must price sonnet when offline")
	r.Greater(p.Input, 0.0)
}

func TestLoadFreshCacheTTL(t *testing.T) {
	r := require.New(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "model_prices.json")
	r.NoError(os.WriteFile(path, []byte(sampleLiteLLM), 0o644))

	// Backdate mtime beyond the TTL.
	old := time.Now().Add(-2 * pricingCacheTTL)
	r.NoError(os.Chtimes(path, old, old))

	_, ok := loadFreshCache(path)
	r.False(ok, "stale cache must not be treated as fresh")

	// But loadCache (any age) still reads it.
	_, ok = loadCache(path)
	r.True(ok, "stale cache must still be loadable at any age")
}
