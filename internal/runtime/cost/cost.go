// Package cost calculates LLM API costs based on token usage.
package cost

import (
	"fmt"

	"github.com/jelmersnoeck/forge/internal/types"
)

// Pricing holds per-token costs in USD per million tokens.
type Pricing struct {
	Input      float64 // cost per 1M input tokens
	Output     float64 // cost per 1M output tokens
	CacheWrite float64 // cost per 1M cache write tokens
	CacheRead  float64 // cost per 1M cache read tokens
}

// modelPricing maps model names to their pricing.
// Prices as of April 2026 from Anthropic's pricing page.
var modelPricing = map[string]Pricing{
	// Claude Sonnet 4.5 (2025-09-29)
	"claude-sonnet-4-5-20250929": {
		Input:      3.00,
		Output:     15.00,
		CacheWrite: 3.75,
		CacheRead:  0.30,
	},
	// Claude 3.5 Sonnet (2024-10-22)
	"claude-3-5-sonnet-20241022": {
		Input:      3.00,
		Output:     15.00,
		CacheWrite: 3.75,
		CacheRead:  0.30,
	},
	// Claude 3.5 Sonnet (2024-06-20)
	"claude-3-5-sonnet-20240620": {
		Input:      3.00,
		Output:     15.00,
		CacheWrite: 3.75,
		CacheRead:  0.30,
	},
	// Claude 3 Opus
	"claude-3-opus-20240229": {
		Input:      15.00,
		Output:     75.00,
		CacheWrite: 18.75,
		CacheRead:  1.50,
	},
	// Claude 3 Sonnet
	"claude-3-sonnet-20240229": {
		Input:      3.00,
		Output:     15.00,
		CacheWrite: 3.75,
		CacheRead:  0.30,
	},
	// Claude 3 Haiku
	"claude-3-haiku-20240307": {
		Input:      0.25,
		Output:     1.25,
		CacheWrite: 0.30,
		CacheRead:  0.03,
	},
	// Claude 3.5 Haiku
	"claude-3-5-haiku-20241022": {
		Input:      1.00,
		Output:     5.00,
		CacheWrite: 1.25,
		CacheRead:  0.10,
	},
}

// Calculate computes the cost in USD for the given token usage and model.
// Returns 0.0 if model is unknown.
func Calculate(model string, usage types.TokenUsage) float64 {
	pricing, ok := modelPricing[model]
	if !ok {
		return 0.0
	}

	// Convert tokens to millions and multiply by per-million-token costs
	inputCost := float64(usage.InputTokens) / 1_000_000.0 * pricing.Input
	outputCost := float64(usage.OutputTokens) / 1_000_000.0 * pricing.Output
	cacheWriteCost := float64(usage.CacheCreationTokens) / 1_000_000.0 * pricing.CacheWrite
	cacheReadCost := float64(usage.CacheReadTokens) / 1_000_000.0 * pricing.CacheRead

	return inputCost + outputCost + cacheWriteCost + cacheReadCost
}

// FormatCost formats a cost in USD with appropriate precision.
// Returns "$X.XX" for costs >= $0.01, "< $0.01" for smaller amounts, "$0.00" for zero.
func FormatCost(cost float64) string {
	switch {
	case cost == 0.0:
		return "$0.00"
	case cost < 0.01:
		return "< $0.01"
	default:
		return fmt.Sprintf("$%.2f", cost)
	}
}
