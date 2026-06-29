---
id: credential-providers
status: implemented
---
# Pluggable credential providers for LLM API key resolution

## Description
Introduce an `internal/credentials` package that resolves named LLM credentials
from a pluggable source. Ship an `env` provider (logical-key → env-var mapping)
and a `Chain` resolver. Replace direct `os.Getenv("ANTHROPIC_API_KEY"/"OPENAI_API_KEY")`
reads at all LLM-key call sites with the resolver. Source order is configurable
via `~/.forge/config.toml [credentials] sources`, defaulting to `["env"]` so
existing setups are unaffected. LLM keys only; MCP auth is out of scope.
Implements GitHub issue #238.

## Context
New package:
- `internal/credentials/credentials.go` — `Provider` interface, `EnvProvider`,
  `Chain`, logical-key constants, logical→env mapping, package-level default
  resolver (`Default()`, `SetDefault()`), and `Resolve(sources []string)`.
- `internal/credentials/credentials_test.go` — tests.

Config:
- `internal/config/user_config.go` — add `CredentialsConfig` with
  `Sources []string` under `[credentials]`; wire into `UserConfig`. (No new
  `forge config set` key — list-valued, out of scope for this issue.)

LLM-key read sites to migrate (replace the two LLM env reads only):
- `internal/agent/worker.go` — `selectProvider`, `providerFromName`,
  `collectProviders`, `runReview` (lines ~943-947), and the two reads inside
  `providerFromName`/`selectProvider`.
- `internal/agent/phase/orchestrator.go` — `runReviewerWithDiff` (~714-718).
- `cmd/forge/session_name.go` — `newLightweightProvider` (~25-31).
- `cmd/forge/cli.go` — `reviewProviderSummary` (~1364-1368); the auto-detect
  in the env-detection helper (~1364) — only the two LLM keys.
- `cmd/forge/agent.go` — `runAgent` startup check (~30); set the default
  resolver here from user config before any provider construction.
- `internal/tools/websearch.go` — `webSearchHandler` (~67) uses
  `credentials.Default().Get(...)`.

Leave untouched: `os.Getenv` for `HOME`, `PATH`, `FORGE_PROVIDER`,
`SESSIONS_DIR`, `WORKSPACE_DIR`, `FORGE_BIN`, and all `internal/mcp/*`.

## Behavior
- `credentials.AnthropicAPIKey` and `credentials.OpenAIAPIKey` are exported
  logical-key constants with values `"anthropic.api_key"` and `"openai.api_key"`.
- `EnvProvider.Get("anthropic.api_key")` returns `(os.Getenv("ANTHROPIC_API_KEY"), true)`
  when the env var is set to a non-empty value, else `("", false)`.
- `EnvProvider.Get` on an unknown logical key returns `("", false)`.
- `EnvProvider.Name()` returns `"env"`.
- `Chain.Get` tries providers in order and returns the first `(value, true)`;
  returns `("", false)` if none match. `Chain.Name()` returns `"chain(env)"`
  (comma-joined member names).
- `Resolve(nil)` and `Resolve([]string{})` return a chain equivalent to
  `["env"]`. `Resolve([]string{"env"})` returns an env-only chain.
- An unknown source name in the list is skipped with a logged warning; if the
  resulting chain is empty it falls back to env-only.
- `Default()` returns a process-wide resolver, lazily initialized to env-only.
  `SetDefault(p Provider)` replaces it. `cmd/forge/agent.go` calls `SetDefault`
  early in `runAgent` with `Resolve(userCfg.Credentials.Sources)`.
- All migrated call sites read keys through the resolver
  (`credentials.Default().Get(credentials.AnthropicAPIKey)`), preserving their
  existing "non-empty → use provider" semantics: a `false`/empty result means
  the provider is treated as unavailable exactly as before.
- `~/.forge/config.toml` with no `[credentials]` table → sources default to
  `["env"]`; zero behavior change vs. current code.
- `[credentials]\nsources = ["env"]` → identical to default.

## Constraints
- Must not change behavior when `[credentials]` is unset: resolution is
  env-only and byte-for-byte equivalent to the current `os.Getenv` reads.
- Must not read or resolve env-var spellings outside the `EnvProvider`; other
  providers and call sites use logical keys only.
- Must not migrate non-credential `os.Getenv` calls (`HOME`, `PATH`,
  `FORGE_PROVIDER`, `SESSIONS_DIR`, `WORKSPACE_DIR`, `FORGE_BIN`).
- Must not touch `internal/mcp/*` (MCP auth out of scope).
- Must not implement `FileProvider`, `KeychainProvider`, or any
  `forge credentials set` command this round.
- `internal/credentials` must not import `internal/config` (avoid cycle); the
  config→resolver wiring lives in `cmd/forge` / `internal/agent`, which pass a
  `[]string` of source names into `credentials.Resolve`.
- An empty string from an env var counts as absent (`ok=false`), matching the
  current `key != ""` guards.

## Interfaces
```go
package credentials

// Logical credential keys (source-agnostic). Not raw env-var names.
const (
    AnthropicAPIKey = "anthropic.api_key"
    OpenAIAPIKey    = "openai.api_key"
)

// Provider resolves a logical credential key from some source.
type Provider interface {
    // Get returns the value for a logical key; ok=false if absent or empty.
    Get(key string) (value string, ok bool)
    // Name identifies the provider for logging/config.
    Name() string
}

// EnvProvider resolves logical keys via a logical→env-var mapping over os.LookupEnv.
type EnvProvider struct{}

func NewEnvProvider() EnvProvider
func (EnvProvider) Get(key string) (string, bool)
func (EnvProvider) Name() string // "env"

// Chain tries providers in order; first non-empty hit wins.
type Chain struct{ providers []Provider }

func NewChain(providers ...Provider) Chain
func (c Chain) Get(key string) (string, bool)
func (c Chain) Name() string // "chain(env)"

// Resolve builds a Chain from source names. nil/empty → ["env"].
// Unknown names are skipped (logged); empty result falls back to env-only.
func Resolve(sources []string) Provider

// Default returns the process-wide resolver (lazy env-only).
func Default() Provider
// SetDefault overrides the process-wide resolver.
func SetDefault(p Provider)
```

```go
// internal/config/user_config.go
type CredentialsConfig struct {
    Sources []string `toml:"sources"` // tried in order; nil/empty → ["env"]
}
type UserConfig struct {
    // ...existing fields...
    Credentials CredentialsConfig `toml:"credentials"`
}
```

## Edge Cases
- `[credentials] sources = []` (explicit empty) → treated as unset → env-only.
- `sources = ["file"]` (unknown this round) → "file" skipped with warning,
  chain empty → falls back to env-only so the agent still works.
- `sources = ["env", "env"]` → duplicate env providers; harmless, first hit wins.
- Env var set to empty string (`ANTHROPIC_API_KEY=`) → `Get` returns
  `("", false)`; call site treats provider as unavailable (matches current).
- `Default()` called before `SetDefault` (e.g. in a unit test or the
  websearch tool when no agent wired it) → returns a lazily-initialized
  env-only resolver; never nil.
- Concurrent `Default()`/`SetDefault` from multiple goroutines → guarded by a
  mutex/`sync.Once`; no data race.
- `LoadUserConfig` error in `runAgent` → log warning, fall back to env-only
  resolver; do not abort startup.
- `SetDefault(nil)` → resets the process-wide resolver to env-only (never nil),
  matching `Default()`'s lazy-init invariant.
