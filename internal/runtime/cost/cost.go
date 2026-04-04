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
	// Claude Opus 4 (estimated from actual usage data)
	"claude-opus-4-6": {
		Input:      5.00,
		Output:     25.00,
		CacheWrite: 6.25,
		CacheRead:  0.50,
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
	// Claude 3.5 Haiku (2024-10-22)
	"claude-3-5-haiku-20241022": {
		Input:      1.00,
		Output:     5.00,
		CacheWrite: 1.25,
		CacheRead:  0.10,
	},
	// Claude Haiku 4.5 (2025-10-01)
	"claude-haiku-4-5-20251001": {
		Input:      1.00,
		Output:     5.00,
		CacheWrite: 1.25,
		CacheRead:  0.10,
	},
}

// Calculate computes the cost in USD for the given token usage and model.
// Returns 0.0 if model is unknown.
//
// Note: Based on ccusage (the standard Claude usage tracking tool), all four token
// types (input, output, cache_creation, cache_read) are treated as ADDITIVE, meaning:
// - input_tokens = non-cached input tokens
// - cache_creation_input_tokens = tokens written to cache (charged separately)
// - cache_read_input_tokens = tokens read from cache (charged separately)
// - Total billable input = input_tokens + cache_creation_input_tokens + cache_read_input_tokens
//
// This interpretation matches how ccusage calculates costs and avoids double-counting.
func Calculate(model string, usage types.TokenUsage) float64 {
	pricing, ok := modelPricing[model]
	if !ok {
		return 0.0
	}

	// All token types are charged separately (additive model)
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

// FormatNumber formats an integer with thousands separators.
// Example: 1234567 -> "1,234,567"
func FormatNumber(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}

	// Convert to string and add commas
	str := fmt.Sprintf("%d", n)
	var result []byte
	for i, digit := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(digit))
	}
	return string(result)
}

// FormatNumberWithPercent formats an integer with thousands separators and percentage.
// Example: FormatNumberWithPercent(1500, 10000) -> "1,500 (15%)"
func FormatNumberWithPercent(n, total int) string {
	formatted := FormatNumber(n)
	if total == 0 {
		return formatted
	}
	percent := float64(n) / float64(total) * 100.0
	return fmt.Sprintf("%s (%.0f%%)", formatted, percent)
}
