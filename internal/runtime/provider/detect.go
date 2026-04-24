package provider

import (
	"errors"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/jelmersnoeck/forge/internal/config"
	"github.com/jelmersnoeck/forge/internal/types"
)

// validateAPIKey trims whitespace and returns the cleaned key.
// Returns empty string if the key is blank or whitespace-only after trimming.
func validateAPIKey(key string) string {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" && key != "" {
		log.Printf("[provider] API key is whitespace-only — treating as unset")
	}
	return trimmed
}

// claudeCLIBinary is the executable name for the Claude CLI, used in PATH lookups.
const claudeCLIBinary = "claude"

// DetectResult holds the outcome of provider auto-detection.
// Provider is a ready-to-use instance (nil when Found is false).
// Name is the canonical provider name for callers that need deferred
// instantiation via FromName/FromNameOrFallback.
type DetectResult struct {
	Provider types.LLMProvider
	Name     string
	Found    bool
}

// ResolveResult holds the outcome of the full provider resolution chain.
type ResolveResult struct {
	// Name is the canonical provider name (e.g. "anthropic", "openai", "claude-cli").
	// Empty when no provider was found.
	Name string

	// Source describes where the name came from: "env", "config", "auto-detect", or "".
	Source string

	// Found is true when a provider was resolved from any source.
	Found bool

	// ConfigErr is non-nil when the user config file exists but failed to load.
	// Callers should surface this — a corrupted config silently falling through
	// to auto-detect is surprising.
	ConfigErr error
}

// ResolveProvider determines which provider to use, without instantiating it.
//
// Priority:
//  1. FORGE_PROVIDER env var (explicit override)
//  2. ~/.forge/config.toml [provider].default
//  3. Auto-detect: ANTHROPIC_API_KEY → OPENAI_API_KEY → `claude` on PATH
//
// Callers use the returned Name with FromName (nullable) or FromNameOrFallback
// (non-nil) depending on whether they need a guaranteed provider.
func ResolveProvider() ResolveResult {
	// Priority 1: explicit env override
	if envProv := os.Getenv("FORGE_PROVIDER"); envProv != "" {
		return ResolveResult{Name: envProv, Source: "env", Found: true}
	}

	// Priority 2: user config
	var configErr error
	userCfg, err := config.LoadUserConfig()
	switch {
	case err != nil:
		configErr = err
		// Distinguish expected "file not found" from real config problems.
		if !isNotExist(err) {
			log.Printf("[provider] WARNING: user config is corrupted or unreadable: %v — falling through to auto-detect", err)
		}
	case userCfg.Provider.Default != "":
		return ResolveResult{Name: userCfg.Provider.Default, Source: "config", Found: true}
	}

	// Priority 3: auto-detect from environment
	result := AutoDetect()
	if result.Found {
		if configErr != nil && !isNotExist(configErr) {
			log.Printf("[provider] WARNING: resolved via auto-detect but config had errors: %v", configErr)
		}
		return ResolveResult{Name: result.Name, Source: "auto-detect", Found: true, ConfigErr: configErr}
	}

	return ResolveResult{ConfigErr: configErr}
}

// AutoDetect probes the environment for available LLM providers.
// Priority: ANTHROPIC_API_KEY → OPENAI_API_KEY → `claude` CLI on PATH.
//
// Returns Found=false when nothing is available.
func AutoDetect() DetectResult {
	if key := validateAPIKey(os.Getenv("ANTHROPIC_API_KEY")); key != "" {
		return DetectResult{Provider: NewAnthropic(key), Name: "anthropic", Found: true}
	}
	if key := validateAPIKey(os.Getenv("OPENAI_API_KEY")); key != "" {
		return DetectResult{Provider: NewOpenAI(key), Name: "openai", Found: true}
	}
	if resolvedPath, err := exec.LookPath(claudeCLIBinary); err == nil {
		// LookPath checks executability, but verify the resolved path to catch
		// broken symlinks.
		if _, statErr := os.Stat(resolvedPath); statErr == nil {
			return DetectResult{Provider: NewClaudeCLI(), Name: "claude-cli", Found: true}
		} else {
			log.Printf("[provider] %s found at %s but not accessible: %v", claudeCLIBinary, resolvedPath, statErr)
		}
	}
	return DetectResult{}
}

// FromName instantiates a provider by its canonical name. Returns nil when
// the name is recognised but the required credentials are missing (for
// lightweight callers that treat nil as "no provider available").
func FromName(name string) types.LLMProvider {
	switch name {
	case "anthropic":
		if key := validateAPIKey(os.Getenv("ANTHROPIC_API_KEY")); key != "" {
			return NewAnthropic(key)
		}
		log.Printf("[provider] provider=anthropic but ANTHROPIC_API_KEY not set")
		return nil
	case "openai":
		if key := validateAPIKey(os.Getenv("OPENAI_API_KEY")); key != "" {
			return NewOpenAI(key)
		}
		log.Printf("[provider] provider=openai but OPENAI_API_KEY not set")
		return nil
	case "claude-cli":
		if _, err := exec.LookPath(claudeCLIBinary); err == nil {
			return NewClaudeCLI()
		}
		log.Printf("[provider] provider=claude-cli but `%s` not found on PATH", claudeCLIBinary)
		return nil
	default:
		log.Printf("[provider] unknown provider %q", name)
		return nil
	}
}

// FromNameOrFallback instantiates a provider by name, always returning a
// non-nil provider. Falls back to Anthropic with a warning if credentials
// or the name are invalid — the provider will fail on first API call with a
// clear error instead of returning nil.
func FromNameOrFallback(name string) types.LLMProvider {
	switch name {
	case "anthropic":
		key := validateAPIKey(os.Getenv("ANTHROPIC_API_KEY"))
		if key == "" {
			log.Println("[provider] WARNING: provider=anthropic but ANTHROPIC_API_KEY not set — API calls will fail")
		}
		return NewAnthropic(key)
	case "claude-cli":
		if _, err := exec.LookPath(claudeCLIBinary); err != nil {
			log.Printf("[provider] WARNING: provider=claude-cli but `%s` not found on PATH", claudeCLIBinary)
		}
		return NewClaudeCLI()
	case "openai":
		key := validateAPIKey(os.Getenv("OPENAI_API_KEY"))
		if key == "" {
			log.Println("[provider] WARNING: provider=openai but OPENAI_API_KEY not set — API calls will fail")
		}
		return NewOpenAI(key)
	default:
		log.Printf("[provider] WARNING: unknown provider %q — falling back to anthropic", name)
		return NewAnthropic(validateAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
	}
}

// isNotExist reports whether err (or any wrapped error) is a "file not found" error.
func isNotExist(err error) bool {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return os.IsNotExist(pathErr)
	}
	return os.IsNotExist(err)
}
