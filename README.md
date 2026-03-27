# forge

Async coding agent. Runs Claude Code headlessly, receives work via HTTP, and
streams results back over SSE. Adapters (Slack, Linear, GitHub, Datadog, ...)
sit on top and translate platform events to the generic API.

## Architecture

```
  Adapters (Slack, Linear, CLI, ...)
       │
       ▼
  ┌──────────┐    pushMessage    ┌──────────┐
  │ Gateway  │ ───────────────▶  │ Worker   │ ── query() ──▶ Claude Agent SDK
  │ (HTTP)   │ ◀─────────────── │ (per     │
  └──────────┘  subscribeEvents  │  thread) │
       │                         └──────────┘
       ▼
  SSE stream
```

Each conversation gets its own worker. Workers drive the Agent SDK's `query()`
loop and resume sessions across messages using the SDK's built-in session
persistence.

## Packages

| Package | Description |
|---------|-------------|
| `@forge/types` | Shared API types — `InboundMessage`, `OutboundEvent`, `ThreadMeta` |
| `@forge/server` | Gateway (Fastify HTTP + SSE) and worker (Agent SDK loop) |
| `@forge/cli` | Interactive REPL that talks to the server |

## Quick start

```bash
npm install
just dev-server              # terminal 1
just dev-cli                 # terminal 2
```

The CLI creates a thread, subscribes to events, and lets you send messages
interactively. The server needs `ANTHROPIC_API_KEY` set in `.env` or the
environment.

## API

### `POST /threads`

Create a new conversation thread.

```json
{ "metadata": { "channel": "C123" } }
```

Returns `{ "threadId": "...", "metadata": { ... } }`.

### `GET /threads/:threadId`

Retrieve thread metadata.

### `POST /threads/:threadId/messages`

Send a message into a thread. Queued and processed asynchronously.

```json
{ "text": "list files in src/", "source": "slack", "user": "troy.barnes" }
```

### `GET /threads/:threadId/events`

SSE stream of `OutboundEvent` objects. Each event has a unique `id` for
deduplication and SSE `Last-Event-ID` resumption.

Event types: `text`, `tool_use`, `done`, `error`.

## Just recipes

```
just build        # types → server → cli
just dev-server   # start server with watch mode
just dev-cli      # start interactive CLI
just check        # typecheck all packages
just clean        # remove build artifacts
```

## Writing an adapter

An adapter is any process that speaks HTTP to the server. The pattern:

1. Receive a platform event (Slack message, Linear comment, etc.)
2. Map it to a thread — `POST /threads` on first contact, store `threadId`
3. Forward the message — `POST /threads/:threadId/messages`
4. Subscribe to output — `GET /threads/:threadId/events` (SSE)
5. Translate `OutboundEvent` back to the platform's format

The server doesn't know or care who's calling it. `metadata` on threads and
messages is an opaque bag for adapter-specific correlation data.
