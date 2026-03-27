# forge

Async coding agent — headless Claude Code behind a platform-agnostic HTTP API.

## Repository layout

```
packages/
  types/     shared contracts (API, tools, context, sessions)
  tools/     tool registry + in-process implementations (Read, Write, Edit, Bash, Glob, Grep)
  runtime/   conversation loop, context loader, LLM provider, session persistence
  server/    Fastify gateway + per-thread worker
  cli/       interactive REPL client (reference adapter)
```

npm workspaces monorepo. Build order: types -> tools -> runtime -> server -> cli.

## Build & run

```bash
just build        # build all (types -> tools -> runtime -> server -> cli)
just dev-server   # dev server with file watching
just dev-cli      # interactive CLI
just check        # typecheck everything
just clean        # nuke dist/ dirs
```

## How it works

1. Gateway receives HTTP requests and enqueues messages via an in-memory bus
2. Each thread gets its own worker (async task in the same process)
3. Workers run a ConversationLoop that drives the Anthropic Messages API directly
4. ContextLoader discovers CLAUDE.md, skills, agents, and rules from the filesystem
5. Tools execute in-process (file ops, bash, ripgrep)
6. Output streams back via EventEmitter -> SSE to HTTP clients
7. Sessions persist as JSONL for resume

The bus (`bus.ts`) uses Maps + promise-based waiters for the queue and
EventEmitter for pub/sub. Swap to Redis when workers move to separate
processes (k8s Agent Sandbox phase).

## Key files

- `packages/runtime/src/loop.ts` — agentic conversation loop (replaces Agent SDK)
- `packages/runtime/src/context.ts` — CLAUDE.md, skills, agents, rules discovery
- `packages/runtime/src/prompt.ts` — system prompt assembly
- `packages/runtime/src/provider/anthropic.ts` — Anthropic Messages API streaming
- `packages/tools/src/registry.ts` — tool registry + createDefaultRegistry()
- `packages/tools/src/tools/` — individual tool implementations
- `packages/server/src/bus.ts` — in-memory message queue + pub/sub + thread store
- `packages/server/src/worker.ts` — worker loop using ConversationLoop
- `packages/server/src/gateway.ts` — HTTP routes, SSE streaming, worker lifecycle
- `packages/types/src/index.ts` — shared contracts (all packages import from here)

## Conventions

- TypeScript, ESM (`"type": "module"`)
- Strict mode, Node16 module resolution
- Platform-agnostic API: `source` is a free-form string, `metadata` is opaque
- Adapters are external HTTP clients — the server has no platform-specific code

## API endpoints

```
POST   /threads                    create thread (accepts metadata)
GET    /threads/:threadId          get thread info
POST   /threads/:threadId/messages send message (text, source, user, metadata)
GET    /threads/:threadId/events   SSE stream of OutboundEvents
```

## Adding a package

1. Create `packages/<name>/` with `package.json`, `tsconfig.json`, `src/`
2. Add to root `package.json` workspaces array
3. Import shared types from `@forge/types`
4. Add build/dev recipes to justfile

## Test data

Use TV show Community references for fake data (Troy Barnes, Greendale
Community College, etc.).
