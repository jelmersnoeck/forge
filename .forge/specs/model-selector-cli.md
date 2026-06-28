---
id: model-selector-cli
status: implemented
---
# CLI model selection, in-session switching, and model listing

## Description

Model selection for forge: `--model` flag, `/model` slash command for mid-session
switching, and `/model list` to show available models per provider. The model
name is displayed in the status bar. Phases 1 and 2 (flag + switching) are
implemented. Phase 3 adds model listing that queries available providers at
runtime and displays available models grouped by provider.

## Context

- `cmd/forge/cli.go:580-609` — `/model` command dispatch (switch on `parseModelArg`)
- `cmd/forge/cli.go:1929-1987` — `renderModelList`, `modelAliasesForDisplay`, `shortModelName`
- `cmd/forge/cli.go:2005-2055` — `sendSetModel`, `fetchModelList`
- `cmd/forge/cli_test.go` — tests for `parseModelArg`, `renderModelList`
- `cmd/forge/commands.go` — slash command registry (updated description)
- `internal/agent/server.go:42-44` — `POST /model`, `GET /models` routes
- `internal/agent/server.go:162-173` — `handleListModels` handler
- `internal/agent/worker.go:50-55` — `providers` field on Worker
- `internal/agent/worker.go:68-81` — `SetModel`, `ModelOverride`
- `internal/agent/worker.go:88-128` — `ListModels` method
- `internal/agent/worker.go:870-886` — `collectProviders` function
- `internal/agent/worker.go:1009-1043` — `modelAliases`, `ResolveModelAlias`, `resolveModel`
- `internal/runtime/provider/anthropic.go:148-166` — `AnthropicProvider.ListModels`
- `internal/runtime/provider/openai.go:270-340` — `OpenAIProvider.ListModels`, `isChatModel`
- `internal/types/types.go:116-137` — `LLMProvider`, `ModelLister`, `ModelEntry`, `ProviderModels`
- Anthropic SDK `model.go` — `client.Models.ListAutoPaging()` returns `ModelInfo` with `ID`, `DisplayName`
- OpenAI API `GET /v1/models` — returns `{data: [{id, ...}]}`

## Behavior

### Phase 1-2 (implemented)

1. `forge --model <name>` starts session with that model.
2. Short aliases (`opus`, `sonnet`, `haiku`) resolve to full IDs.
3. `/model <name>` switches model mid-session.
4. `/model` with no argument shows current model.
5. Status bar shows short model name.

### Phase 3: `/model list`

6. `/model list` queries each available provider for its model catalog and
   displays them grouped by provider in the output area.
7. Provider availability is determined the same way the agent does it:
   - **Anthropic**: `ANTHROPIC_API_KEY` is set
   - **OpenAI**: `OPENAI_API_KEY` is set
   - **Claude CLI**: `claude` binary is on PATH
8. For providers with API-based listing:
   - **Anthropic**: `GET /v1/models` via the SDK's `client.Models.List()`.
     Display `DisplayName` and `ID` for each model.
   - **OpenAI**: `GET /v1/models` via raw `net/http`. Display model `id`.
9. For **Claude CLI**: list the hardcoded aliases (`opus`, `sonnet`, `haiku`)
   since the CLI has no list endpoint. Note "(aliases — CLI resolves these)".
10. The listing is done agent-side via a new `GET /models` endpoint. The CLI
    sends `GET <gateway>/models` and renders the response.
11. Output format in the TUI (dimStyle, grouped by provider header):

    ```
    Available models:

    Anthropic
      claude-opus-4-6            Claude Opus 4
      claude-sonnet-4-20250514   Claude Sonnet 4
      claude-haiku-4-20250506    Claude Haiku 4
      ...

    OpenAI
      gpt-4.1
      gpt-4.1-mini
      ...

    Claude CLI (aliases)
      opus    → claude-opus-4-6
      sonnet  → claude-sonnet-4-20250514
      haiku   → claude-haiku-4-20250506
    ```

12. If no providers are available, display:
    "No providers configured. Set ANTHROPIC_API_KEY, OPENAI_API_KEY, or install claude CLI."
13. The model list is fetched fresh each time `/model list` is invoked (no caching).
14. The `/model list` subcommand is handled CLI-side: `parseModelArg` returns
    `"list"`, which triggers the list flow instead of a model switch.

## Constraints

- Must not break gateway mode. The `GET /models` endpoint is on the agent;
  the CLI hits whatever `m.gateway` URL is configured.
- Must not change model resolution when `--model` is absent.
- Must not add external dependencies (Anthropic SDK already in go.mod; OpenAI
  uses raw `net/http`).
- API calls to list models must have a reasonable timeout (5s). On timeout or
  error, display the error for that provider and continue with others.
- OpenAI returns hundreds of models; filter to only show models whose `id`
  starts with `gpt-` or `o1-` or `o3-` or `o4-` (chat-capable models).
- Must not block the TUI — the list fetch runs as a `tea.Cmd`.

## Interfaces

```go
// internal/types/types.go — new types for model listing
type ModelEntry struct {
    ID          string `json:"id"`
    DisplayName string `json:"displayName,omitempty"`
}

type ProviderModels struct {
    Provider string       `json:"provider"`
    Models   []ModelEntry `json:"models"`
    Error    string       `json:"error,omitempty"` // non-fatal: timeout, auth failure
}

// LLMProvider — new optional interface for model listing
type ModelLister interface {
    ListModels(ctx context.Context) ([]ModelEntry, error)
}

// internal/agent/server.go — new endpoint
mux.HandleFunc("GET /models", handleListModels(worker))

func handleListModels(worker *Worker) http.HandlerFunc
// Returns JSON: []ProviderModels

// internal/agent/worker.go — worker method + provider collection
func (w *Worker) ListModels(ctx context.Context) []ProviderModels
func collectProviders() map[string]types.LLMProvider  // Anthropic, OpenAI, Claude CLI

// internal/runtime/provider/anthropic.go
func (p *AnthropicProvider) ListModels(ctx context.Context) ([]types.ModelEntry, error)
// Uses client.Models.ListAutoPaging for auto-paginated iteration

// internal/runtime/provider/openai.go
func (p *OpenAIProvider) ListModels(ctx context.Context) ([]types.ModelEntry, error)
// GET /v1/models, filtered by isChatModel (gpt-, o1-, o3-, o4- prefixes)

// cmd/forge/cli.go — new types and functions
type modelsListMsg []types.ProviderModels

func (m model) fetchModelList() tea.Cmd
func renderModelList(providers []types.ProviderModels) []string
func modelAliasesForDisplay() [][2]string  // ordered alias→full-ID pairs
```

## Edge Cases

1. **Empty `--model` flag**: Treated as unset, falls back to settings-based
   resolution.

2. **Unknown alias**: Passed through as-is; provider returns API error.

3. **`/model` while agent is working**: Model change takes effect next turn.

4. **Gateway mode `/model list`**: Works — `GET /models` goes through the
   same gateway URL. If gateway doesn't proxy, returns connection error
   which is displayed.

5. **Anthropic API key invalid**: `ListModels` returns auth error string in
   `ProviderModels.Error`; other providers still listed.

6. **OpenAI returns hundreds of models**: Filtered to `gpt-*`, `o1-*`, `o3-*`,
   `o4-*` prefixes to keep output manageable.

7. **Network timeout**: 5s context deadline per provider. On timeout, that
   provider's entry shows `Error: "request timed out"`, others still appear.

8. **No providers configured**: Message tells user which env vars to set.

9. **Claude CLI aliases displayed**: Shown with `→` to indicate they're
   aliases, not direct model IDs.
