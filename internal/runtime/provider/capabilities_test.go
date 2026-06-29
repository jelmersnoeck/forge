package provider

import (
	"context"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestProviderDefaultModel(t *testing.T) {
	tests := map[string]struct {
		provider types.LLMProvider
		want     string
	}{
		"anthropic":  {provider: NewAnthropic("greendale"), want: "claude-opus-4-6"},
		"openai":     {provider: NewOpenAI("troy-barnes"), want: "gpt-4.1"},
		"claude-cli": {provider: NewClaudeCLI(), want: ""},
		"no-caps":    {provider: noCapProvider{}, want: ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, DefaultModel(tc.provider))
		})
	}
}

func TestProviderLightweightModels(t *testing.T) {
	tests := map[string]struct {
		provider types.LLMProvider
		want     []string
	}{
		"anthropic":  {provider: NewAnthropic("k"), want: []string{"claude-haiku-4-5", "claude-haiku-4-5-20251001"}},
		"openai":     {provider: NewOpenAI("k"), want: []string{"gpt-4.1-mini"}},
		"claude-cli": {provider: NewClaudeCLI(), want: []string{""}},
		"no-caps":    {provider: noCapProvider{}, want: []string{""}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, LightweightModels(tc.provider))
		})
	}
}

// noCapProvider implements LLMProvider but neither optional capability
// interface — exercises the helper fallbacks.
type noCapProvider struct{}

func (noCapProvider) Chat(_ context.Context, _ types.ChatRequest) (<-chan types.ChatDelta, error) {
	return nil, nil
}
