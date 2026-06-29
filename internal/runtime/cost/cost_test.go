package cost

import (
	"os"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

// TestMain forces cost tests offline so the litellm resolver is deterministic:
// an unreachable URL and an empty HOME make resolvePricing fall back to the
// embedded snapshot (+ supplemental map) with no network dependency.
func TestMain(m *testing.M) {
	_ = os.Setenv("HOME", os.TempDir()+"/forge-cost-test-home")
	_ = os.Setenv(litellmURLEnv, "http://127.0.0.1:1/offline")
	os.Exit(m.Run())
}

func TestIsAliasModel(t *testing.T) {
	tests := map[string]struct {
		model string
		want  bool
	}{
		"dated haiku 4.5": {
			model: "claude-haiku-4-5-20251001",
			want:  false,
		},
		"alias haiku 4.5": {
			model: "claude-haiku-4-5",
			want:  true,
		},
		"dated sonnet": {
			model: "claude-3-5-sonnet-20241022",
			want:  false,
		},
		"dated opus": {
			model: "claude-3-opus-20240229",
			want:  false,
		},
		"alias opus 4-6 (no date suffix)": {
			model: "claude-opus-4-6",
			want:  true,
		},
		"short model name": {
			model: "haiku",
			want:  true,
		},
		"exactly 8 chars (date suffix length)": {
			model: "12345678",
			want:  true,
		},
		"exactly 9 chars no dash separator": {
			model: "x12345678",
			want:  true,
		},
		"minimum dated model (prefix-YYYYMMDD)": {
			model: "x-12345678",
			want:  false,
		},
		"empty string": {
			model: "",
			want:  true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, isAliasModel(tc.model))
		})
	}
}

func TestCalculate(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		model string
		usage types.TokenUsage
		want  float64
	}{
		"sonnet 3.5 basic usage": {
			model: "claude-3-5-sonnet-20241022",
			usage: types.TokenUsage{
				InputTokens:  1000,
				OutputTokens: 500,
			},
			want: 0.0105, // (1000/1M * 3.00) + (500/1M * 15.00) = 0.003 + 0.0075
		},
		"sonnet 3.5 with cache": {
			model: "claude-3-5-sonnet-20241022",
			usage: types.TokenUsage{
				InputTokens:         1000, // Non-cached input
				OutputTokens:        500,
				CacheCreationTokens: 5000,  // Separate from InputTokens
				CacheReadTokens:     10000, // Separate from InputTokens
			},
			// Cost: (1000/1M * $3) + (500/1M * $15) + (5000/1M * $3.75) + (10000/1M * $0.30)
			//     = $0.003 + $0.0075 + $0.01875 + $0.003
			//     = $0.03225
			want: 0.03225,
		},
		"opus high cost": {
			model: "claude-3-opus-20240229",
			usage: types.TokenUsage{
				InputTokens:  1000,
				OutputTokens: 1000,
			},
			want: 0.090, // (1000/1M * 15.00) + (1000/1M * 75.00)
		},
		"haiku low cost": {
			model: "claude-3-haiku-20240307",
			usage: types.TokenUsage{
				InputTokens:  1000,
				OutputTokens: 1000,
			},
			want: 0.00150, // (1000/1M * 0.25) + (1000/1M * 1.25)
		},
		"haiku 4.5 alias": {
			model: "claude-haiku-4-5",
			usage: types.TokenUsage{
				InputTokens:  1000,
				OutputTokens: 1000,
			},
			want: 0.006, // (1000/1M * 1.00) + (1000/1M * 5.00)
		},
		"haiku 4.5 dated": {
			model: "claude-haiku-4-5-20251001",
			usage: types.TokenUsage{
				InputTokens:  1000,
				OutputTokens: 1000,
			},
			want: 0.006, // (1000/1M * 1.00) + (1000/1M * 5.00)
		},
		"retired haiku 3.5 still priced for historical lookups": {
			model: "claude-3-5-haiku-20241022",
			usage: types.TokenUsage{
				InputTokens:  1000,
				OutputTokens: 1000,
			},
			want: 0.006, // (1000/1M * 1.00) + (1000/1M * 5.00)
		},
		"unknown model": {
			model: "claude-unknown-model",
			usage: types.TokenUsage{
				InputTokens:  1000,
				OutputTokens: 1000,
			},
			want: 0.0,
		},
		"zero tokens": {
			model: "claude-3-5-sonnet-20241022",
			usage: types.TokenUsage{},
			want:  0.0,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := Calculate(tc.model, tc.usage)
			r.InDelta(tc.want, got, 0.0001, "cost mismatch")
		})
	}
}

func TestFormatCost(t *testing.T) {
	tests := map[string]struct {
		cost float64
		want string
	}{
		"zero": {
			cost: 0.0,
			want: "$0.00",
		},
		"less than cent": {
			cost: 0.005,
			want: "< $0.01",
		},
		"exactly one cent": {
			cost: 0.01,
			want: "$0.01",
		},
		"normal cost": {
			cost: 1.23,
			want: "$1.23",
		},
		"large cost": {
			cost: 123.456,
			want: "$123.46",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := FormatCost(tc.cost)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestFormatNumber(t *testing.T) {
	tests := map[string]struct {
		num  int
		want string
	}{
		"zero": {
			num:  0,
			want: "0",
		},
		"single digit": {
			num:  5,
			want: "5",
		},
		"two digits": {
			num:  42,
			want: "42",
		},
		"three digits": {
			num:  999,
			want: "999",
		},
		"thousand": {
			num:  1000,
			want: "1,000",
		},
		"four digits": {
			num:  1234,
			want: "1,234",
		},
		"five digits": {
			num:  12345,
			want: "12,345",
		},
		"six digits": {
			num:  123456,
			want: "123,456",
		},
		"million": {
			num:  1234567,
			want: "1,234,567",
		},
		"ten million": {
			num:  12345678,
			want: "12,345,678",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := FormatNumber(tc.num)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestFormatNumberWithPercent(t *testing.T) {
	tests := map[string]struct {
		num   int
		total int
		want  string
	}{
		"zero total": {
			num:   100,
			total: 0,
			want:  "100",
		},
		"zero value": {
			num:   0,
			total: 1000,
			want:  "0 (0%)",
		},
		"15 percent": {
			num:   1500,
			total: 10000,
			want:  "1,500 (15%)",
		},
		"50 percent": {
			num:   50,
			total: 100,
			want:  "50 (50%)",
		},
		"100 percent": {
			num:   1000,
			total: 1000,
			want:  "1,000 (100%)",
		},
		"small percent rounds to zero": {
			num:   1,
			total: 1000,
			want:  "1 (0%)",
		},
		"large numbers": {
			num:   250000,
			total: 1000000,
			want:  "250,000 (25%)",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := FormatNumberWithPercent(tc.num, tc.total)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestCalculateOpenAI(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		model string
		usage types.TokenUsage
		want  float64
	}{
		"gpt-4.1 input+output": {
			model: "gpt-4.1",
			usage: types.TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000},
			want:  10.0, // 2.00 + 8.00
		},
		"gpt-4.1-mini": {
			model: "gpt-4.1-mini",
			usage: types.TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000},
			want:  2.0, // 0.40 + 1.60
		},
		"o3": {
			model: "o3",
			usage: types.TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000},
			want:  10.0, // 2.00 + 8.00
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r.InDelta(tc.want, Calculate(tc.model, tc.usage), 1e-9)
		})
	}
}

func TestCalculateUnknownModelReturnsZero(t *testing.T) {
	r := require.New(t)
	// An unpriced model (e.g. brand-new gpt-5) returns 0.0 — the warning is
	// additive and must not change the return value.
	got := Calculate("gpt-5-greendale", types.TokenUsage{InputTokens: 1000, OutputTokens: 1000})
	r.Equal(0.0, got)
	// Calling again must not panic (sync.Once dedup); still 0.0.
	r.Equal(0.0, Calculate("gpt-5-greendale", types.TokenUsage{InputTokens: 5}))
}

// TestSonnetAliasTargetPriced is the direct regression for issue #278: the
// sonnet alias target used to be absent from the pricing map, so a session that
// burned 100M tokens reported $0.00. It must now resolve to a non-zero cost via
// the litellm-sourced table (embedded snapshot when offline).
func TestSonnetAliasTargetPriced(t *testing.T) {
	r := require.New(t)
	got := Calculate("claude-sonnet-4-20250514", types.TokenUsage{InputTokens: 100_000_000})
	r.Greater(got, 0.0, "sonnet alias target must not report $0 on real usage")
}

// aliasTargets enumerates every model ID an alias (or review default) can
// resolve to. Kept in sync with internal/agent/worker.go modelAliases,
// cmd/forge/cli.go modelAliasesForDisplay, and the review orchestrator default.
// Duplicated here (not imported) to avoid an import cycle; the CI assertion
// below catches alias/pricing desync regardless.
var aliasTargets = []string{
	"claude-opus-4-6",           // opus alias
	"claude-sonnet-4-20250514",  // sonnet alias + review default
	"claude-haiku-4-5-20251001", // haiku alias (fixed from bogus 20250506)
}

// TestAllAliasTargetsPriced guards against future alias/pricing desync — the
// exact class of bug in issue #278. Every alias target must resolve to a
// non-zero price in the loaded table.
func TestAllAliasTargetsPriced(t *testing.T) {
	r := require.New(t)
	for _, model := range aliasTargets {
		p, ok := LookupPricing(model)
		r.Truef(ok, "alias target %q has no pricing — fix the alias or add the model", model)
		r.Greaterf(p.Input, 0.0, "alias target %q has zero input price", model)
		r.Greaterf(p.Output, 0.0, "alias target %q has zero output price", model)
	}
}
