# Forge Quick Reference

## Commands

```bash
# Start server
just dev-server

# Start CLI
just dev-cli

# Build all
just build

# Run tests
just test

# Resume session
forge-cli --resume <session-id>
```

## CLI Interface

```
┌─────────────────────────────────────┐
│ Event Stream (auto-scrolls)         │
│   [Write] file.go                   │
│   [Bash] go test                    │
├─────────────────────────────────────┤
│ Queued messages:                    │
│ → Processing                        │
│   Waiting                           │
├─────────────────────────────────────┤
│ ╭─────────────────────────────────╮ │
│ │ > Type here...                  │ │
│ ╰─────────────────────────────────╯ │
└─────────────────────────────────────┘
```

## Keyboard

- **Enter** → Queue/send message
- **Backspace** → Delete character  
- **Ctrl+C** → Exit
- **Type** → Characters appear

## Queue Patterns

### Test After Changes
```
> Create feature, test after each file
```
Agent queues: `go test` (immediate)

### Commit When Done
```
> Refactor code, commit at end
```
Agent queues: `git commit` (completion)

### Both Queues
```
> Add feature, test each file, commit when done
```
Agent queues:
- `go test` (immediate)
- `git commit` (completion)

## Event Types

| Event | Display | Color |
|-------|---------|-------|
| text | Normal text | White |
| tool_use | [ToolName] detail | Yellow |
| error | error: message | Red |
| done | (blank line) | - |
| queue_immediate | ⏱ Queued immediate | Magenta |
| queue_on_complete | ⏱ Queued on complete | Magenta |
| queued_task_result | [queued] output | Magenta |
| queued_task_error | [queued error] msg | Red |

## Tools Available

| Tool | Purpose |
|------|---------|
| Read | Read files |
| Write | Create/overwrite files |
| Edit | String replacement |
| Bash | Execute commands |
| Glob | File pattern matching |
| Grep | Search with ripgrep |
| WebSearch | Search the web |
| QueueImmediate | Queue after each tool |
| QueueOnComplete | Queue on completion |

## Environment Variables

```bash
# Server
GATEWAY_PORT=3000
WORKSPACE_DIR=/tmp/forge/workspace
SESSIONS_DIR=/tmp/forge/sessions
AGENT_BIN=forge-agent

# Agent
ANTHROPIC_API_KEY=sk-...

# Web Search (optional)
SEARCH_PROVIDER=duckduckgo  # or "brave"
BRAVE_API_KEY=your_key

# CLI
FORGE_URL=http://localhost:3000
```

## Common Workflows

### Rapid Development
```bash
> Create module A
> Create module B
> Create module C
> Test all modules
> Commit changes

# All queued, processed sequentially
```

### Test-Driven
```bash
> Implement calculator with tests after each file

Agent:
  ⏱ Queued: go test
  [Write] calc.go
  [queued] go test → PASS
  [Write] calc_test.go
  [queued] go test → PASS
```

### Auto-Commit
```bash
> Refactor database code, commit when done

Agent:
  ⏱ Queued on complete: git commit
  [Edit] db.go
  [Edit] db_test.go
  [done]
  [queued] git commit → Committed
```

## API Endpoints

### Server
```
POST /sessions
GET  /sessions/{id}
POST /sessions/{id}/messages
GET  /sessions/{id}/events (SSE)
```

### Agent
```
GET  /health
POST /messages
GET  /events (SSE)
```

## Directory Structure

```
cmd/
  server/    → Server binary
  agent/     → Agent binary
  cli/       → CLI binary
internal/
  agent/     → Agent implementation
  runtime/   → Loop, provider, context
  server/    → Gateway, bus, backend
  tools/     → Tool implementations
  types/     → Shared contracts
docs/        → Documentation
```

## Quick Tips

✅ **Queue messages** - Don't wait, keep typing
✅ **Use queues** - "test after each file"
✅ **Resume sessions** - Save the session ID
✅ **Commit auto** - "commit when done"
✅ **Batch work** - Queue 5+ tasks at once

❌ **Don't wait** - Input always available
❌ **Don't interrupt** - Let agent finish
❌ **Don't lose session** - Save ID on exit

## Documentation

- `CLAUDE.md` - Architecture
- `README.md` - Quick start
- `docs/CLI_TUI.md` - CLI guide
- `docs/QUEUE_SYSTEM.md` - Queue details

## Support

```bash
# Check server status
curl http://localhost:3000/health

# View logs
just logs

# Rebuild
just clean && just build
```

## Example Session

```bash
$ just dev-server
[server] listening on :3000

$ just dev-cli
forge cli — session abc-123

> Create a todo app with add and list
  commands. Test after each file.
  Commit when done.

  ⏱  Queued immediate: go test ./...
  ⏱  Queued on complete: git commit -am 'Add todo app'
  
  [Write] todo.go
  [queued] go test ./...
  PASS
  
  [Write] todo_test.go
  [queued] go test ./...
  PASS
  
  [done]
  [queued] git commit -am 'Add todo app'
  [main 7f8d9a] Add todo app

> Add a delete command

  [Edit] todo.go
  [queued] go test ./...
  PASS
  
  [done]

^C
To resume: forge-cli --resume abc-123
```

---

**Remember:** The input is always available - keep typing! 🚀
