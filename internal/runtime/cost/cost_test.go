package cost

import (
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

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
				InputTokens:         1000,
				OutputTokens:        500,
				CacheCreationTokens: 5000,
				CacheReadTokens:     10000,
			},
			want: 0.03225, // 0.003 + 0.0075 + (5000/1M * 3.75) + (10000/1M * 0.30) = 0.003 + 0.0075 + 0.01875 + 0.003
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
