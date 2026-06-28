---
id: model-selector-cli
status: implemented
---
# CLI model selection and in-session switching

## Description

Add a `--model` flag to `forge` and a `/model` slash command for mid-session
switching. The model name is displayed in the status bar. This covers Phases 1
and 2 of the issue; Phase 3 (picker popup) is out of scope but the slash
command is the natural extension point.

## Context

- `cmd/forge/cli.go:120-130` — CLI flag parsing in `runCLI()`
- `cmd/forge/cli.go:246` — `spawnLocalAgent()` call, passes mode/spec args
- `cmd/forge/cli.go:1649-1660` — agent subprocess arg construction
- `cmd/forge/cli.go:785-815` — status bar rendering (`viewStatusBar()`)
- `cmd/forge/cli.go:62-109` — TUI `model` struct (has `modelName` field)
- `cmd/forge/cli.go:882-885` — handles `"model"` SSE event
- `cmd/forge/cli.go:555-593` — slash command dispatch (currently only `/review`)
- `cmd/forge/commands.go` — slash command registry
- `cmd/forge/agent.go:12-57` — `runAgent()` flag parsing, `agent.Config`
- `internal/agent/server.go:17-24` — `Config` struct (no Model field yet)
- `internal/agent/server.go:31-61` — `Start()`, HTTP mux setup
- `internal/agent/worker.go:29-50` — `Worker` struct, `NewWorker()`
- `internal/agent/worker.go:94-104` — model resolution logic
- `internal/agent/hub.go` — Hub message queue + pub/sub
- `internal/runtime/loop/loop.go:61` — `Options.Model` consumed by conversation loop

## Behavior

### `--model` flag

1. `forge --model claude-sonnet-4-20250514` starts a session using that model.
2. `forge --model sonnet` works — short aliases are passed through. The agent's
   existing model resolution logic (worker.go:96-104) already handles Claude CLI
   aliases; for the Anthropic provider, aliases are resolved to full model IDs
   using a known alias map.
3. The `--model` flag value is passed to the agent subprocess as `--model <value>`.
4. In the agent, the explicit `--model` value takes highest priority over
   settings-based resolution (settings.json `model` field).
5. If `--model` is not set, behavior is unchanged (settings → default).

### Alias map

The following short aliases resolve to full Anthropic model IDs:

| Alias     | Model ID                        |
|-----------|---------------------------------|
| `opus`    | `claude-opus-4-6`              |
| `sonnet`  | `claude-sonnet-4-20250514`     |
| `haiku`   | `claude-haiku-4-20250506`      |

When the provider is Claude CLI, aliases are passed through unresolved (the CLI
handles its own alias resolution).

### In-session model switching

6. `/model <name>` switches the model for all subsequent turns.
7. The CLI sends `POST /model` to the agent with `{"model": "<name>"}`.
8. The agent updates its mutable model field (mutex-protected) and responds
   with `200 OK` and `{"model": "<resolved>"}`.
9. The agent emits a `"model"` SSE event with the new model name so the CLI
   updates `modelName` and the status bar.
10. `/model` with no argument displays the current model in the output area.
11. The `/model` command appears in slash-command autocomplete.

### Status bar

12. The status bar displays the short model name alongside cost info.
    Format: `model-short | in: N | out: N | $X.XX` on the right side.
    The short name is derived by stripping the `claude-` prefix and any date
    suffix (e.g. `claude-sonnet-4-20250514` → `sonnet-4`).

## Constraints

- Must not break gateway mode. The `/model` command sends HTTP to whatever
  `m.gateway` URL is configured (direct agent or gateway proxy).
- Must not change model resolution when `--model` is absent — existing
  settings.json and default behavior unchanged.
- Must not add external dependencies.
- The alias map is hardcoded; must not query external APIs.
- The mutable model field in Worker must be accessed under a mutex — the
  HTTP handler and the worker loop run on different goroutines.

## Interfaces

```go
// cmd/forge/agent.go — flag and config addition
// Added to runAgent() flag parsing:
model := fs.String("model", "", "model to use (overrides settings)")

// internal/agent/server.go — Config struct
type Config struct {
    Port        int
    CWD         string
    SessionID   string
    SessionsDir string
    Mode        string
    SpecPath    string
    Model       string // explicit model override from --model flag
}

// internal/agent/server.go — new handler
mux.HandleFunc("POST /model", handleSetModel(worker))

func handleSetModel(w *Worker) http.HandlerFunc

// internal/agent/worker.go — mutable model with accessor
func (w *Worker) SetModel(model string)
func (w *Worker) Model() string

// cmd/forge/cli.go — new slash command helper
func isModelCommand(input string) bool
func parseModelArg(input string) string

// cmd/forge/cli.go — HTTP call
func (m model) sendSetModel(modelName string) tea.Cmd
```

## Edge Cases

1. **Empty `--model` flag**: `forge --model ""` — treated as unset, falls back
   to settings-based resolution. The flag's zero value is empty string.

2. **Unknown alias**: `forge --model gpt-4` — passed through as-is to the
   Anthropic provider, which will return an API error. No CLI-side validation
   beyond the alias map.

3. **`/model` while agent is working**: The HTTP endpoint is always available
   (runs on the HTTP server goroutine). Model change takes effect on the next
   turn, not mid-turn. The current turn continues with the old model.

4. **Gateway mode `/model`**: The gateway does not currently proxy arbitrary
   endpoints. For Phase 2, `/model` only works in interactive (direct agent)
   mode. In gateway mode, `/model` returns an error message to the user:
   "model switching is not supported in gateway mode".

5. **`/model` with alias vs full name**: Both work. `/model sonnet` resolves
   via the alias map; `/model claude-sonnet-4-20250514` passes through directly.

6. **Status bar with no model yet**: Before the first `"model"` SSE event,
   `modelName` is empty. The status bar omits the model display until set.
