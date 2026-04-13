---
id: claude-cli-provider
status: implemented
---
# Claude CLI as LLM provider via `claude -p`

## Description
Add a new LLM provider that wraps the `claude` CLI (Claude Code) as a backend.
Instead of calling the Anthropic API directly, Forge spawns `claude -p` with
`--output-format stream-json` and streams its NDJSON output as `ChatDelta` events.
This lets users leverage their Claude.ai subscription without an API key. The
Claude CLI process acts as the full agentic engine (including its own tool
execution), so Forge's conversation loop treats it as a text-only provider that
never returns `tool_use` stop reasons.

## Context
- `internal/runtime/provider/claude_cli.go` — new provider implementation
- `internal/runtime/provider/claude_cli_test.go` — comprehensive tests (34 cases)
- `internal/runtime/provider/` — existing providers (anthropic.go, openai.go)
- `internal/types/types.go` — `LLMProvider` interface, `ChatDelta`, `ChatRequest`
- `internal/agent/worker.go` — provider selection logic (`selectProvider()` function),
  model aliasing for CLI, Claude CLI added to review orchestrator's provider pool

## Behavior
- New file `internal/runtime/provider/claude_cli.go` implements `LLMProvider`.
- Constructor: `NewClaudeCLI()` — no API key needed; requires `claude` on PATH.
- On each `Chat()` call, spawns: `claude -p --output-format stream-json
  --include-partial-messages --dangerously-skip-permissions --model <model>
  --system-prompt <system> --resume <sessionID> "<prompt>"`.
  - First call in a session omits `--resume` and captures the `session_id` from
    the first streamed message for subsequent calls.
  - Subsequent calls within the same Forge session reuse the Claude CLI session
    via `--resume <claudeSessionID>`.
- The provider parses NDJSON lines from stdout and emits `ChatDelta` events:
  - `stream_event` with `content_block_delta` / `text_delta` → `ChatDelta{Type: "text_delta"}`
  - `assistant` messages (complete turn) → emit any remaining text, then `ChatDelta{Type: "message_stop", StopReason: "end_turn"}`
  - `result` message → final done signal
  - Tool use events from the CLI are **not** surfaced as `tool_use_start/delta/end`
    to Forge's loop. They are internal to the Claude CLI. Text output from tool
    results is streamed as text deltas so the user sees progress.
- Usage: token counts are extracted from `assistant` and `result` messages when
  available (the CLI includes `usage` objects in assistant messages).
- Errors: if the CLI exits non-zero or stderr contains errors, emit
  `ChatDelta{Type: "error"}`.
- Worker selects this provider via `selectProvider()` function: when
  `ANTHROPIC_API_KEY` is unset but `claude` is found on PATH. No settings-based
  `provider: "claude-cli"` configuration was added (auto-detection only).
- The `--model` flag passes through the model from `ChatRequest.Model` (Claude
  CLI accepts aliases like `sonnet`, `opus`, `haiku`).
- Process lifecycle: each `Chat()` invocation is a single `claude -p` execution.
  The `--resume` flag handles session continuity. On context cancellation, the
  process group is killed (using `Setpgid` + group kill per existing learning).

## Constraints
- Do not modify the `LLMProvider` interface — the Claude CLI provider must
  conform to the existing `Chat(ctx, ChatRequest) (<-chan ChatDelta, error)`
  contract.
- Do not attempt to pass Forge's custom tool schemas to the CLI — the CLI only
  supports its own built-in tools.
- The Claude CLI session ID is **not** the same as Forge's session ID. Track the
  mapping internally in the provider struct.
- Never buffer the entire CLI response before emitting — stream continuously.
- Do not import the Anthropic SDK in the Claude CLI provider.
- Tests must not require `claude` on PATH — use a mock script or test the parser
  in isolation.
- Process cleanup must kill the entire process group, not just the direct child.

## Interfaces

```go
// ClaudeCLIProvider implements types.LLMProvider by wrapping the Claude CLI.
type ClaudeCLIProvider struct {
    claudeSessionID string     // tracks the CLI session across calls
    mu              sync.Mutex // protects claudeSessionID
}

func NewClaudeCLI() *ClaudeCLIProvider

// Chat spawns `claude -p` and streams NDJSON output as ChatDelta events.
func (p *ClaudeCLIProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error)
```

NDJSON line types from Claude CLI `--output-format stream-json`:
```json
// System init
{"type":"system","session_id":"abc-123","message":"..."}

// Streaming partial (with --include-partial-messages)
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}}

// Complete assistant turn
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"..."}],"usage":{...}},"session_id":"abc-123"}

// Tool use (internal to CLI — not surfaced to Forge loop)
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01...","name":"Read","input":{...}}]}}

// Final result
{"type":"result","session_id":"abc-123","is_error":false,"duration_ms":1234,"cost_usd":0.003}
```

## Edge Cases
- `claude` not on PATH → return clear error from first `Chat()` call (not constructor).
- CLI exits mid-stream (crash, OOM) → emit error delta, close channel.
- Context cancelled while CLI running → kill process group via `cmd.Cancel`,
  `cmd.WaitDelay` (5s) prevents pipe deadlock from orphan children.
- Very long system prompts → passed via `--system-prompt` arg directly (exec.Command
  bypasses shell, so no escaping issues; OS ARG_MAX ~256KB on macOS).
- Claude CLI rate-limited (user subscription limits) → surface the error from
  stderr/stdout.
- Multiple concurrent `Chat()` calls on same provider instance → mutex protects
  session ID, but only one CLI process runs at a time (sequential by nature of
  the conversation loop).
- First message in session has no `--resume` → capture session_id from response.
- `--resume` with invalid/expired session → CLI may error; fall back to new session.
- Model aliasing: when using Claude CLI, the worker allows any model string from
  settings (not just `claude-*` prefixed), since the CLI accepts aliases.
