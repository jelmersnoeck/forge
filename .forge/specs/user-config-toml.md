---
id: user-config-toml
status: implemented
---
# Persistent user config with TOML and `forge config` commands

## Description
Add a TOML-based user configuration file at `~/.forge/config.toml` for persistent
settings like the default LLM provider. Introduce `forge config` subcommands to
read and write configuration values from the CLI. The config feeds into provider
selection so users don't need env vars or project-level JSON to pick their default.

## Context
- `~/.forge/config.toml` — new user-level config file (TOML)
- `internal/config/config.go` — existing JSON config loader (project-level ForgeConfig)
- `internal/config/user_config.go` — new: TOML user config loader + writer
- `cmd/forge/main.go` — add `config` subcommand routing
- `cmd/forge/config.go` — new: `forge config` command implementation
- `internal/agent/worker.go` — `selectProvider()` reads user config for provider preference
- `internal/types/types.go` — `MergedSettings` may gain a `Provider` field

## Behavior
- `~/.forge/config.toml` stores user-level preferences in TOML format.
- Initial supported keys:
  - `[provider]` section with `default = "anthropic"` (or `"claude-cli"`, `"openai"`)
- `forge config set provider.default anthropic` writes to `~/.forge/config.toml`.
- `forge config get provider.default` reads and prints the value.
- `forge config list` prints all current config values.
- `forge config set` with no args prints usage help.
- `selectProvider()` in worker.go checks user config before falling back to env detection.
  Priority: env var override (`FORGE_PROVIDER`) > user config > auto-detect (current behavior).
- Unknown provider names produce a clear error at agent startup.
- The existing JSON project config (`config.json`) and settings files are unaffected.

## Constraints
- Uses `github.com/BurntSushi/toml` v1.6.0 — de-facto Go TOML library, zero transitive deps.
- TOML file is human-readable and hand-editable.
- Existing env-var-based provider selection preserved as highest-priority override (FORGE_PROVIDER).
- Project-level settings.json files untouched.
- `selectProvider()` priority: FORGE_PROVIDER env var > config.toml > ANTHROPIC_API_KEY env > claude CLI on PATH > fallback.

## Interfaces

```go
// internal/config/user_config.go

// UserConfig represents ~/.forge/config.toml
type UserConfig struct {
    Provider ProviderConfig `toml:"provider"`
}

type ProviderConfig struct {
    Default string `toml:"default"` // "anthropic", "claude-cli", "openai"
}

// LoadUserConfig loads ~/.forge/config.toml. Returns zero value if missing.
func LoadUserConfig() (UserConfig, error)

// SaveUserConfig writes the config to ~/.forge/config.toml.
func SaveUserConfig(cfg UserConfig) error

// SetValue sets a dotted key (e.g. "provider.default") to a value.
func SetValue(key, value string) error

// GetValue returns the value for a dotted key, or "" if unset.
func GetValue(key string) (string, error)
```

```
# CLI commands
forge config get <key>        # print value
forge config set <key> <value> # set value
forge config list              # print all config
```

## Edge Cases
- `~/.forge/` directory doesn't exist → create it on first `config set`.
- `~/.forge/config.toml` doesn't exist → `config get` returns empty, `config list` shows defaults.
- Invalid TOML in config file → return parse error, don't silently ignore.
- Unknown config key in `config set` → reject with list of valid keys.
- Unknown config key in `config get` → reject with list of valid keys.
- `FORGE_PROVIDER` env var set → overrides config file. Printed by `config list` as `(env override)`.
- Provider value not one of known providers → error at set time with valid options.
