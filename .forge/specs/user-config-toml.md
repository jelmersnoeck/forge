---
id: user-config-toml
status: implemented
---
# Persistent user config with TOML and `forge config` commands

## Description
TOML-based user configuration at `~/.forge/config.toml` for persistent settings:
default LLM provider, default model, and commit/PR attribution. `forge config`
subcommands read and write values from the CLI. The config feeds provider and
model selection so users don't need env vars or project-level JSON.

This iteration adds a persistent **default model** (`model.default`). Previously
`/model <name>` in the TUI only set a session-scoped override (lives in the
worker's `modelOverride`, lost on exit). Users now have three ways to set a
global default that survives restarts: `forge config set model.default <name>`,
`/model <name> --global` in the TUI, and a bare `/model` that opens an
interactive selector (arrow keys + enter) which sets the chosen model globally.

## Context
- `~/.forge/config.toml` — user-level config file (TOML)
- `internal/config/config.go` — JSON config loader (project-level ForgeConfig, unchanged)
- `internal/config/user_config.go` — TOML user config loader/writer; add `model.default`
  to `UserConfig`, `validKeys`, `SetValue`/`GetValue`/`listValuesAt`, `validateValue`
- `cmd/forge/main.go` — `config` subcommand routing (unchanged)
- `cmd/forge/config.go` — `forge config` command implementation (unchanged; driven by validKeys)
- `internal/agent/worker.go`:
  - `selectProvider()` reads user config for provider preference (already implemented)
  - `resolveModel()` (worker.go ~1096) — add user-config `model.default` to the priority chain
  - `defaultModel` constants (worker.go:255, :809) — replace hardcoded `claude-opus-4-6`
    fallback path with config-aware resolution. The sub-agent runner (worker.go ~808)
    now routes through the new `resolveDefaultModel` helper.
- `cmd/forge/cli.go`:
  - `/model` TUI handler (~585) — bare `/model` opens an interactive selector
    overlay; `--global` flag persists on typed sets
  - `applyModelSwitch`/`buildModelChoices`/`renderModelSelector` — selector
    overlay helpers; `model` struct gains selector state fields; `tea.KeyMsg`
    handler intercepts navigation keys when the overlay is active
  - `isModelCommand`/`parseModelArg` (~1927) — extend to surface `--global`
  - imports `internal/config` for `SetValue`

## Behavior
- `~/.forge/config.toml` stores user-level preferences in TOML format.
- Supported keys (existing):
  - `provider.default` → `[provider] default` — "anthropic" | "claude-cli" | "openai"
  - `commit.attribution.coAuthor`, `commit.attribution.enabled`, `commit.attribution.generatedBy`
  - `pr.attribution.enabled`
- New key:
  - `model.default` → `[model] default` — a model ID or alias (e.g. "opus",
    "sonnet", "claude-sonnet-4-20250514"). Stored verbatim; resolved at agent
    startup the same way the `--model` flag is.
- `forge config set <key> <value>` writes to `~/.forge/config.toml`.
- `forge config get <key>` reads and prints the value.
- `forge config list` prints all current config values (env overrides flagged).
- `forge config set` with no args prints usage help.
- `forge config set model.default opus` → subsequent `forge` sessions (with no
  `--model` flag) start on opus.

### Model selection priority
At agent startup, `resolveModel()` resolves the effective model with this
priority (highest first):
1. Session override — `--model` flag at launch, or `/model <name>` set mid-session
   (lives in worker `modelOverride`).
2. Project `settings.json` `model` (only when it's a real `claude-` ID, per existing logic).
3. `~/.forge/config.toml` `model.default` (NEW).
4. Hardcoded fallback `claude-opus-4-6`.

For the Claude CLI provider, aliases pass through unchanged (existing
`ResolveModelAlias` behavior); for the Anthropic provider, aliases expand via
`modelAliases`.

### `/model` TUI command
- `/model` (interactive mode) — opens an **interactive model selector overlay**
  (UPDATED; previously printed the current model). It fetches the model list
  (showing a "loading models..." spinner), then renders a navigable list:
  - `↑`/`↓` move the highlight, `enter` selects, `esc` cancels.
  - Selecting an entry sets the model **globally** — equivalent to
    `/model <name> --global` (session override + persists `model.default`).
  - While the overlay is open, other keystrokes are swallowed (no text input).
  - In gateway (non-interactive) mode, `/model` keeps the old behavior: it just
    prints the current session model (no selector, no switching).
- `/model list` — lists available models as text (unchanged).
- `/model <name>` — sets session-only override (unchanged; lost on exit).
- `/model <name> --global` (NEW) — sets the session override AND persists
  `model.default = <name>` to `~/.forge/config.toml`. Confirmation line notes
  it was saved globally.
- `--global` with no model name (`/model --global`) → error: a model name is required.
- Model switching (with or without `--global`) is only supported in interactive
  mode; gateway mode rejects with the existing message.

- Unknown provider names produce a clear error at agent startup (unchanged).
- The existing JSON project config (`config.json`) and settings files are unaffected.

## Constraints
- Uses `github.com/BurntSushi/toml` v1.6.0 — de-facto Go TOML library, zero transitive deps.
- TOML file is human-readable and hand-editable.
- Existing env-var-based provider selection preserved as highest-priority override (FORGE_PROVIDER).
- Project-level settings.json files untouched.
- `selectProvider()` priority: FORGE_PROVIDER env var > config.toml > ANTHROPIC_API_KEY env > claude CLI on PATH > fallback.
- `model.default` is stored verbatim (no validation against a model allowlist) —
  models change frequently and the Anthropic API is the source of truth. An
  invalid model surfaces as an API error at call time, not at `config set` time.
- `/model <name>` without `--global` must NOT write to config — it stays session-scoped.
- `--global` must be parsed positionally-agnostically: `/model opus --global` and
  `/model --global opus` both work.
- Persisting via `--global` must not block the TUI on a slow disk for noticeably
  long; failures to write are surfaced as an error line, not swallowed silently.

## Interfaces

```go
// internal/config/user_config.go

// UserConfig represents ~/.forge/config.toml
type UserConfig struct {
    Provider ProviderConfig   `toml:"provider"`
    Model    ModelConfig      `toml:"model"`   // NEW
    Commit   CommitUserConfig `toml:"commit"`
    PR       PRUserConfig     `toml:"pr"`
}

type ProviderConfig struct {
    Default string `toml:"default"` // "anthropic", "claude-cli", "openai"
}

// ModelConfig holds the default model preference. NEW.
type ModelConfig struct {
    Default string `toml:"default"` // model ID or alias, e.g. "opus" / "claude-sonnet-4-20250514"
}

// validKeys gains: "model.default" → "default LLM model (id or alias, e.g. opus, sonnet)"
// SetValue/GetValue/listValuesAt/validateValue gain a "model.default" case.
// validateValue for model.default: accept any non-empty string (no allowlist).

func LoadUserConfig() (UserConfig, error)
func SaveUserConfig(cfg UserConfig) error
func SetValue(key, value string) error
func GetValue(key string) (string, error)
func ListValues() (map[string]string, error)
```

```go
// internal/agent/worker.go

// resolveModel gains a user-config tier between settings and the hardcoded
// default. Signature unchanged.
// override > settings (claude- prefix) > userConfig.Model.Default > defaultModel.
func (w *Worker) resolveModel(settingsModel string, isClaudeCLI bool, defaultModel string) string

// resolveDefaultModel is a new package-level helper extracted to share the
// settings > userConfig.Model.Default > default chain (no session override).
// Used by resolveModel and by the sub-agent runner (makeAgentRunner), so
// sub-agents with no explicit Model now respect model.default too.
func resolveDefaultModel(settingsModel string, isClaudeCLI bool, defaultModel string) string
```

```go
// cmd/forge/cli.go

// parseModelArg returns the model name with any "--global" token stripped.
// parseModelGlobal reports whether "--global" was present.
//   "/model opus --global" → ("opus", true)
//   "/model opus"          → ("opus", false)
//   "/model --global"      → ("", true)   // caller errors: name required
func parseModelArg(text string) string
func parseModelGlobal(text string) bool

// applyModelSwitch performs a session model switch and, when global is true,
// persists model.default. Failed global writes are surfaced but the in-session
// switch still proceeds. Gateway mode rejects. Shared by the typed /model
// command and the selector overlay.
func (m model) applyModelSwitch(arg string, global bool) (model, tea.Cmd)

// modelChoice is one selectable entry in the /model selector overlay.
type modelChoice struct{ id, label string }

// buildModelChoices flattens provider model lists into ordered selectable
// entries (Claude CLI aliases first, then each provider's concrete IDs;
// errored providers skipped).
func buildModelChoices(providers []types.ProviderModels) []modelChoice

// renderModelSelector renders the overlay (loading spinner, list, cursor).
func (m model) renderModelSelector() string

// model gains selector state: modelSelectorActive, modelSelectorLoading,
// modelSelectorItems []modelChoice, modelSelectorCursor. The tea.KeyMsg
// handler intercepts up/down/enter/esc while modelSelectorActive is true.
```

```
# CLI commands
forge config get <key>          # print value
forge config set <key> <value>  # set value (model.default, provider.default, ...)
forge config list               # print all config

# TUI
/model                  # interactive selector (enter = set globally), gateway: prints current model
/model list             # list available models (text)
/model <name>           # session-only switch
/model <name> --global  # session switch + persist as model.default
```

## Edge Cases
- `~/.forge/` directory doesn't exist → create it on first `config set` / `--global`.
- `~/.forge/config.toml` doesn't exist → `config get` returns empty, `config list` shows defaults;
  `resolveModel` falls through to hardcoded `claude-opus-4-6`.
- Invalid TOML in config file → return parse error, don't silently ignore.
- Unknown config key in `config set`/`config get` → reject with list of valid keys.
- `model.default` set to a nonexistent model (e.g. "claude-opus-4-8") → accepted at
  `config set` time (no allowlist); fails at first API call with the provider's error,
  surfaced via the existing `error` event path. This is the exact bug from the
  prior investigation: the switch succeeds locally but the API rejects the model.
- `/model --global` with no model name → error line "a model name is required", no write.
- `/model <name> --global` in gateway (non-interactive) mode → rejected with the
  existing "model switching is not supported in gateway mode" message; nothing persisted.
- `--global` write fails (read-only home, disk full) → surface the error as an output
  line; the session override is still applied (don't lose the in-session switch).
- `FORGE_PROVIDER` env var set → overrides config file. Printed by `config list` as `(env override)`.
- Provider value not one of known providers → error at set time with valid options.
- Concurrent `/model --global` from a fast-typing user → SetValue does read-modify-write
  of the whole file; last write wins. Acceptable for a single-user local config.
- `/model` selector opened, then `esc` → overlay closes, "Model selection cancelled",
  no switch, no write.
- `/model` selector with an empty/loading list → `enter` is a no-op until the list
  arrives; navigation keys are clamped to list bounds.
- `/model` selector enter on a highlighted entry → sets that model globally
  (persists `model.default`) via the shared `applyModelSwitch(id, true)` path.
- `/model` in gateway (non-interactive) mode → no selector; prints the current
  session model only (selector is interactive-mode only).
