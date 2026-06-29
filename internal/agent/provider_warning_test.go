package agent

import (
	"testing"

	"github.com/jelmersnoeck/forge/internal/credentials"
	"github.com/stretchr/testify/require"
)

// resetCredentials installs the default env-backed credential resolver so each
// test sees only the env vars it sets.
func resetCredentials(t *testing.T) {
	t.Helper()
	credentials.SetDefault(credentials.Resolve(nil))
}

func TestResolveProviderName(t *testing.T) {
	tests := map[string]struct {
		forgeProvider string
		anthropicKey  string
		openaiKey     string
		want          string
	}{
		"explicit openai via env": {
			forgeProvider: "openai", want: "openai",
		},
		"explicit anthropic via env": {
			forgeProvider: "anthropic", want: "anthropic",
		},
		"unknown env name falls back to anthropic": {
			forgeProvider: "opena1", want: "anthropic",
		},
		"auto-detect anthropic from key": {
			anthropicKey: "greendale-key", want: "anthropic",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			t.Setenv("HOME", t.TempDir()) // isolate from real user config
			t.Setenv("FORGE_PROVIDER", tc.forgeProvider)
			t.Setenv("ANTHROPIC_API_KEY", tc.anthropicKey)
			t.Setenv("OPENAI_API_KEY", tc.openaiKey)
			resetCredentials(t)

			r.Equal(tc.want, resolveProviderName())
		})
	}
}

func TestStartupKeyWarning(t *testing.T) {
	tests := map[string]struct {
		forgeProvider string
		anthropicKey  string
		openaiKey     string
		wantContains  string // "" = expect no warning
	}{
		"openai provider missing key warns OPENAI_API_KEY": {
			forgeProvider: "openai", wantContains: "OPENAI_API_KEY",
		},
		"openai provider with key no warning": {
			forgeProvider: "openai", openaiKey: "troy-barnes", wantContains: "",
		},
		"anthropic provider missing key warns ANTHROPIC_API_KEY": {
			forgeProvider: "anthropic", wantContains: "ANTHROPIC_API_KEY",
		},
		"anthropic provider with key no warning": {
			forgeProvider: "anthropic", anthropicKey: "abed-key", wantContains: "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			t.Setenv("HOME", t.TempDir())
			t.Setenv("FORGE_PROVIDER", tc.forgeProvider)
			t.Setenv("ANTHROPIC_API_KEY", tc.anthropicKey)
			t.Setenv("OPENAI_API_KEY", tc.openaiKey)
			resetCredentials(t)

			got := StartupKeyWarning()
			switch tc.wantContains {
			case "":
				r.Empty(got)
			default:
				r.Contains(got, tc.wantContains)
			}
		})
	}
}
