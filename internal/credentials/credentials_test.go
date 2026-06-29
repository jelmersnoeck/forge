package credentials

import (
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvProvider(t *testing.T) {
	tests := map[string]struct {
		key      string
		envName  string
		envValue string
		setEnv   bool
		wantVal  string
		wantOK   bool
	}{
		"anthropic set": {
			key: AnthropicAPIKey, envName: "ANTHROPIC_API_KEY",
			envValue: "troy-barnes-key", setEnv: true,
			wantVal: "troy-barnes-key", wantOK: true,
		},
		"openai set": {
			key: OpenAIAPIKey, envName: "OPENAI_API_KEY",
			envValue: "greendale-key", setEnv: true,
			wantVal: "greendale-key", wantOK: true,
		},
		"anthropic empty counts as absent": {
			key: AnthropicAPIKey, envName: "ANTHROPIC_API_KEY",
			envValue: "", setEnv: true,
			wantVal: "", wantOK: false,
		},
		"anthropic unset": {
			key: AnthropicAPIKey, envName: "ANTHROPIC_API_KEY",
			setEnv:  false,
			wantVal: "", wantOK: false,
		},
		"unknown logical key": {
			key: "abed.api_key", setEnv: false,
			wantVal: "", wantOK: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			if tc.envName != "" {
				if tc.setEnv {
					t.Setenv(tc.envName, tc.envValue)
				} else {
					t.Setenv(tc.envName, "")
					_ = os.Unsetenv(tc.envName)
				}
			}

			got, ok := NewEnvProvider().Get(tc.key)
			r.Equal(tc.wantOK, ok)
			r.Equal(tc.wantVal, got)
		})
	}
}

func TestEnvProviderName(t *testing.T) {
	require.Equal(t, "env", NewEnvProvider().Name())
}

// staticProvider is a test-only provider returning a fixed value.
type staticProvider struct {
	name  string
	value string
	ok    bool
}

func (s staticProvider) Get(string) (string, bool) { return s.value, s.ok }
func (s staticProvider) Name() string              { return s.name }

func TestChainFirstHitWins(t *testing.T) {
	r := require.New(t)

	chain := NewChain(
		staticProvider{name: "file", value: "señor-chang", ok: true},
		staticProvider{name: "env", value: "should-not-win", ok: true},
	)
	got, ok := chain.Get(AnthropicAPIKey)
	r.True(ok)
	r.Equal("señor-chang", got)
	r.Equal("chain(file,env)", chain.Name())
}

func TestChainSkipsMisses(t *testing.T) {
	r := require.New(t)

	chain := NewChain(
		staticProvider{name: "file", ok: false},
		staticProvider{name: "env", value: "human-being", ok: true},
	)
	got, ok := chain.Get(AnthropicAPIKey)
	r.True(ok)
	r.Equal("human-being", got)
}

func TestChainNoHit(t *testing.T) {
	r := require.New(t)

	chain := NewChain(staticProvider{name: "file", ok: false})
	got, ok := chain.Get(AnthropicAPIKey)
	r.False(ok)
	r.Empty(got)
}

func TestResolve(t *testing.T) {
	tests := map[string]struct {
		sources  []string
		wantName string
	}{
		"nil defaults to env":        {sources: nil, wantName: "chain(env)"},
		"empty defaults to env":      {sources: []string{}, wantName: "chain(env)"},
		"explicit env":               {sources: []string{"env"}, wantName: "chain(env)"},
		"duplicate env":              {sources: []string{"env", "env"}, wantName: "chain(env,env)"},
		"unknown falls back to env":  {sources: []string{"file"}, wantName: "chain(env)"},
		"unknown then env keeps env": {sources: []string{"file", "env"}, wantName: "chain(env)"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.wantName, Resolve(tc.sources).Name())
		})
	}
}

func TestDefaultLazyInit(t *testing.T) {
	r := require.New(t)

	// Reset to ensure lazy path.
	defaultMu.Lock()
	defaultResolver = nil
	defaultMu.Unlock()

	d := Default()
	r.NotNil(d)
	r.Equal("chain(env)", d.Name())
}

func TestSetDefault(t *testing.T) {
	r := require.New(t)
	t.Cleanup(func() { SetDefault(nil) })

	SetDefault(staticProvider{name: "keychain", value: "k", ok: true})
	got, ok := Default().Get(AnthropicAPIKey)
	r.True(ok)
	r.Equal("k", got)

	// nil resets to env-only.
	SetDefault(nil)
	r.Equal("chain(env)", Default().Name())
}

func TestDefaultConcurrent(t *testing.T) {
	defaultMu.Lock()
	defaultResolver = nil
	defaultMu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = Default().Name()
			SetDefault(NewChain(NewEnvProvider()))
		}()
	}
	wg.Wait()
}
