---
id: web-ui
status: draft
---
# Browser dashboard for Forge session monitoring

## Description
A read-only browser dashboard served by the existing gateway binary. Shows all
Forge sessions (active and closed), lets the operator drill into a single session
to see events, tool calls, output, and costs. No Node runtime — static assets
are embedded via `embed.FS` and served at `/_ui/`.

Depends on `web-ui-api` spec for all data endpoints. This spec covers only the
frontend bundle, its embed/serve plumbing, and the build pipeline.

Related specs: `web-ui-api` (data layer), `forge-architecture` (gateway),
`session-naming` (session display names), `inline-task-progress` (event types
for task status).

## Context
Files and systems that change:

- `web/` — new top-level directory for the frontend SPA source
  - `web/package.json` — frontend deps and build script
  - `web/src/` — TypeScript + React source
  - `web/dist/` — build output (gitignored, embedded at build time)
- `web/embed.go` — `//go:embed dist/*` directive, exports `embed.FS`
- `internal/server/gateway/ui.go` — new file; mounts the embedded FS at `/_ui/`
- `internal/server/gateway/gateway.go` — import `web` package, register UI routes
- `cmd/forge/gateway.go` — no changes (config flows through gateway.Config)
- `Dockerfile` — multi-stage: Node build → Go build → scratch/distroless runtime
- `justfile` — new `build-web` and `dev-web` targets

## Behavior

### Serving
- Static assets served at `/_ui/*` by the gateway process.
- `/_ui/` serves `index.html`. All sub-paths under `/_ui/` that don't match a static file also serve `index.html` (SPA fallback for client-side routing).
- `Cache-Control: public, max-age=31536000, immutable` on hashed assets (`*.js`, `*.css` with content hash in filename). `Cache-Control: no-cache` on `index.html`.
- No auth on `/_ui/*` static files themselves. API calls from the SPA include the bearer token (stored in `localStorage` after a one-time prompt if `401` is received).

### Session List View (`/_ui/`)
- Default landing page shows a table of all sessions.
- Columns: Name, Status (active/closed badge), Created, Last Active, Cost, CWD.
- Active sessions show a green dot; closed sessions are greyed.
- Auto-refreshes every 10 seconds via polling `GET /api/sessions`.
- Click a row to navigate to session detail.
- Filter bar: status toggle (all / active / closed), text search on name/ID.
- Sorted by last active descending by default. Column headers are clickable for sort.
- Pagination controls when total > 50.

### Session Detail View (`/_ui/sessions/{id}`)
- Header: session name, status badge, created/last-active timestamps, total cost.
- Main area: scrollable event log reconstructed from `/api/sessions/{id}/history`.
  - User messages: left-aligned, distinct background.
  - Assistant text: rendered as markdown (code blocks, inline code, lists).
  - Tool calls: collapsible card showing tool name, input summary, output (truncated to 500 chars, expandable).
  - Thinking blocks: collapsed by default, expandable.
  - Error events: red highlight.
  - Usage events: small inline badge showing token count.
- Cost sidebar (or footer on mobile): per-session cost breakdown from `/api/sessions/{id}/costs` — total cost, input/output/cache tokens.
- For active sessions: auto-poll history every 5 seconds and append new messages. Show "Live" indicator.
- For closed sessions: single fetch, no polling.

### Token Prompt
- On first `401` response from any `/api/` call, show a modal asking the user to enter the gateway token.
- Token is stored in `localStorage` under key `forge_gateway_token`.
- A "Sign out" button in the header clears the token and reloads.

### Responsive / Theme
- Dark theme by default. No light mode toggle in v1.
- Responsive: works on 1440px desktop and 375px mobile. Table collapses to card layout below 768px.

## Constraints
- No Node.js runtime in the final container image. The web build is a CI/build-time step only.
- Frontend bundle must be < 500 KB gzipped (framework + all deps).
- Use React 19 + Vite for the build toolchain. TypeScript strict mode.
- No component library (no MUI, no Chakra). Use Tailwind CSS 4 for styling.
- No state management library. React built-in state + context is sufficient for read-only views.
- No server-side rendering. Pure client-side SPA.
- `web/dist/` is gitignored. CI builds it. Local dev uses `just build-web` or Vite dev server with proxy.
- `embed.go` must use `//go:embed` with build tag `!dev` so that during development the gateway can proxy to Vite's dev server instead.
- Must not add Go dependencies. `embed`, `net/http`, `io/fs` from stdlib.
- Docker build must not require host-installed Node. Use `node:22-alpine` build stage.
- Gateway must start and serve existing API routes even if the embedded UI is empty (zero-byte `dist/`). The UI is optional chrome.

## Interfaces

### Go embed plumbing

```go
// web/embed.go
package web

import "embed"

//go:embed dist/*
var Assets embed.FS
```

```go
// internal/server/gateway/ui.go
package gateway

import (
    "io/fs"
    "net/http"
)

// RegisterUI mounts the embedded SPA at /_ui/.
// If assets is nil or empty, this is a no-op (gateway works without UI).
func RegisterUI(mux *http.ServeMux, assets fs.FS)
```

### Frontend route structure

| Route | View | Data source |
|---|---|---|
| `/_ui/` | Session list | `GET /api/sessions` |
| `/_ui/sessions/:id` | Session detail | `GET /api/sessions/:id/history` + `GET /api/sessions/:id/costs` |

### Key TypeScript types

```typescript
interface Session {
  sessionId: string;
  name: string;
  status: "active" | "closed";
  createdAt: number;    // epoch ms
  lastActiveAt: number; // epoch ms
  cwd: string;
}

interface SessionMessage {
  uuid: string;
  parentUuid?: string;
  sessionId: string;
  type: "user" | "assistant" | "system" | "reflection";
  message: any;     // ChatMessage shape
  timestamp: number; // epoch ms
}

interface SessionCost {
  sessionId: string;
  totalCost: number;
  callCount: number;
  inputTokens: number;
  outputTokens: number;
  cacheCreationTokens: number;
  cacheReadTokens: number;
  firstCall: string; // ISO 8601
  lastCall: string;
}
```

### Dockerfile skeleton

```dockerfile
# Stage 1: Build frontend
FROM node:22-alpine AS web-build
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.24-alpine AS go-build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /app/web/dist web/dist
RUN CGO_ENABLED=1 go build -o /forge ./cmd/forge

# Stage 3: Runtime
FROM alpine:3.21
RUN apk add --no-cache tmux git
COPY --from=go-build /forge /usr/local/bin/forge
ENTRYPOINT ["forge", "gateway"]
```

## Edge Cases

1. **No `web/dist/` directory at compile time** — `embed.FS` is empty. `RegisterUI` detects this and skips route registration. Gateway starts normally without UI.
2. **SPA route refresh** — User refreshes `/_ui/sessions/abc-123`. The fallback handler serves `index.html`, React router picks up the path. Non-`/_ui/` paths are unaffected.
3. **Token in localStorage but gateway token changed** — API returns `401`. UI clears stored token and shows the token prompt modal again.
4. **Large session history (>10k messages)** — History endpoint returns all messages. Frontend uses virtualized scrolling (react-window or similar, only if bundle stays <500KB) or caps display at 2000 messages with a "load more" button.
5. **Gateway restarts while UI is open** — Polling fails. UI shows a "Connection lost — retrying" banner. Resumes automatically when gateway comes back.
6. **Concurrent Vite dev server + gateway** — Dev mode: gateway proxies `/_ui/*` to `http://localhost:5173` instead of serving embed.FS. Controlled by `FORGE_UI_DEV_PROXY` env var.
7. **Session with no name** — Display the first 8 chars of session ID as fallback name.
8. **Mobile viewport** — Table columns collapse. CWD and cost columns hidden below 768px. Session list becomes a card stack.

## Tests
- Go: `RegisterUI` with nil FS is a no-op (no panic, no routes registered).
- Go: `RegisterUI` with populated FS serves `index.html` at `/_ui/`, serves `/_ui/assets/foo.js` with immutable cache header, returns `index.html` for `/_ui/sessions/xyz` (SPA fallback).
- Frontend: unit tests for API client (mock fetch), session list filtering, token storage logic.
- Integration: `docker build` succeeds, container starts, `curl /_ui/` returns HTML.

## Rollout
- PR 2 of 2. Ships after `web-ui-api` is merged.
- Initial deployment: Docker image on Jelmer's Mac mini, exposed via Tailscale.
- Gated: UI routes only register if `web/dist/` is non-empty at build time.
- `just build-web` added for local dev. CI runs it before `go build`.

## Out of Scope
- Send-message or interrupt buttons (read-only v1).
- Live SSE streaming in the UI (polling is sufficient for v1; existing `/sessions/{id}/events` SSE is available for v2).
- Light mode / theme toggle.
- Per-user auth, OIDC, multi-tenant.
- Cost charts / graphs (table view only in v1).
- Mobile-native app.
