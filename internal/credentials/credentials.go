// Package credentials resolves named LLM credentials from a pluggable source.
//
// Call sites read logical keys (e.g. "anthropic.api_key"), not raw env-var
// names. The env provider maps logical keys to env vars; future providers
// (file, keychain) resolve the same logical keys from other sources without
// any call site change.
//
//	logical key            env provider          call site
//	"anthropic.api_key" ──► ANTHROPIC_API_KEY ──► Default().Get(AnthropicAPIKey)
package credentials

import (
	"log"
	"os"
	"strings"
	"sync"
)

// Logical credential keys. Source-agnostic — NOT raw env-var names.
const (
	AnthropicAPIKey = "anthropic.api_key"
	OpenAIAPIKey    = "openai.api_key"
)

// logicalToEnv maps logical credential keys to their env-var spelling.
// Only the env provider knows about env-var names.
var logicalToEnv = map[string]string{
	AnthropicAPIKey: "ANTHROPIC_API_KEY",
	OpenAIAPIKey:    "OPENAI_API_KEY",
}

// EnvVarName returns the environment-variable spelling for a logical credential
// key, or "" if the key is unknown. Used for provider-aware user messages.
func EnvVarName(logicalKey string) string {
	return logicalToEnv[logicalKey]
}

// Provider resolves a logical credential key from some source.
type Provider interface {
	// Get returns the value for a logical key. ok is false if the key is
	// unknown, absent, or resolves to an empty string.
	Get(key string) (value string, ok bool)
	// Name identifies the provider for logging and config.
	Name() string
}

// EnvProvider resolves logical keys via the logical→env mapping over the
// process environment. An empty env value counts as absent.
type EnvProvider struct{}

// NewEnvProvider returns the default environment-backed provider.
func NewEnvProvider() EnvProvider { return EnvProvider{} }

// Get resolves a logical key from the environment.
func (EnvProvider) Get(key string) (string, bool) {
	envName, known := logicalToEnv[key]
	if !known {
		return "", false
	}
	v, ok := os.LookupEnv(envName)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// Name returns "env".
func (EnvProvider) Name() string { return "env" }

// Chain tries its providers in order; the first non-empty hit wins.
type Chain struct {
	providers []Provider
}

// NewChain builds a Chain from the given providers in priority order.
func NewChain(providers ...Provider) Chain {
	return Chain{providers: providers}
}

// Get returns the first provider's value for key, or ("", false) if none match.
func (c Chain) Get(key string) (string, bool) {
	for _, p := range c.providers {
		if v, ok := p.Get(key); ok {
			return v, true
		}
	}
	return "", false
}

// Name returns "chain(<member names>)".
func (c Chain) Name() string {
	names := make([]string, 0, len(c.providers))
	for _, p := range c.providers {
		names = append(names, p.Name())
	}
	return "chain(" + strings.Join(names, ",") + ")"
}

// Resolve builds a Provider from an ordered list of source names. A nil or
// empty list defaults to env-only. Unknown source names are skipped with a
// warning; if nothing resolves, it falls back to env-only so resolution always
// works.
func Resolve(sources []string) Provider {
	if len(sources) == 0 {
		return NewChain(NewEnvProvider())
	}

	var providers []Provider
	for _, src := range sources {
		switch src {
		case "env":
			providers = append(providers, NewEnvProvider())
		default:
			log.Printf("[credentials] unknown source %q — skipping", src)
		}
	}

	if len(providers) == 0 {
		log.Printf("[credentials] no usable sources in %v — falling back to env", sources)
		return NewChain(NewEnvProvider())
	}
	return NewChain(providers...)
}

var (
	defaultMu       sync.RWMutex
	defaultResolver Provider
)

// Default returns the process-wide resolver, lazily initialized to env-only.
// Never returns nil.
func Default() Provider {
	defaultMu.RLock()
	r := defaultResolver
	defaultMu.RUnlock()
	if r != nil {
		return r
	}

	defaultMu.Lock()
	defer defaultMu.Unlock()
	if defaultResolver == nil {
		defaultResolver = NewChain(NewEnvProvider())
	}
	return defaultResolver
}

// SetDefault replaces the process-wide resolver. A nil argument resets it to
// env-only.
func SetDefault(p Provider) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if p == nil {
		p = NewChain(NewEnvProvider())
	}
	defaultResolver = p
}
