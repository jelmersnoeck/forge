package provider

import "github.com/jelmersnoeck/forge/internal/types"

// DefaultModel returns the provider's preferred default model, or "" when the
// provider doesn't implement types.ModelDefaulter (letting the provider's own
// API resolve a default). Never leaks a claude- model for a non-Anthropic
// provider since each provider supplies its own value.
func DefaultModel(p types.LLMProvider) string {
	if d, ok := p.(types.ModelDefaulter); ok {
		return d.DefaultModel()
	}
	return ""
}

// LightweightModels returns the provider's ordered cheap-model list for
// auxiliary calls. Falls back to a single empty entry ([""]) when the provider
// doesn't implement types.LightweightModeler or returns an empty list — callers
// then send an empty model string and the provider uses its own default.
func LightweightModels(p types.LLMProvider) []string {
	if lm, ok := p.(types.LightweightModeler); ok {
		if models := lm.LightweightModels(); len(models) > 0 {
			return models
		}
	}
	return []string{""}
}
