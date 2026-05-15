---
id: web-ui-api
status: draft
---
# Gateway API endpoints for web dashboard

## Description
Add read-only API endpoints to the existing gateway so a web dashboard (or curl)
can list sessions, replay closed session history, and query aggregate cost data.
This is the data layer for the web UI (see `web-ui` spec), but is independently
useful and ships as PR 1 of a two-PR rollout.

Related specs: `forge-architecture` (gateway), `session-naming` (session names
in metadata), `automated-review` (review events in history).

## Context
Files and systems that change:

- `internal/server/gateway/gateway.go` — new route registrations + handler funcs
- `internal/server/bus/bus.go` — new `ListSessions()` function to iterate the in-memory map
- `internal/runtime/session/store.go` — new `List()` to enumerate `*.jsonl` files in sessionsDir
- `internal/runtime/cost/tracker.go` — new `GetSessionCost(sessionID)` query; existing `GetSessionBreakdown` and `GetDailySummaries` are reused
- `internal/server/gateway/auth.go` — new file; bearer-token middleware
- `internal/server/gateway/gateway_test.go` — new file; handler-level tests
- `cmd/forge/gateway.go` — pass `sessionsDir` and optional `costsDB` path into gateway.Config; read `FORGE_GATEWAY_TOKEN`

Gateway config grows two fields:
- `SessionsDir string` (already present in scope via `cfg.SessionsDir` used by handlers)
- `CostDBPath string` (new; path to `~/.forge/costs.db`)
- `Token string` (new; optional shared secret)

## Behavior

### Authentication
- If `FORGE_GATEWAY_TOKEN` is set, every request must include `Authorization: Bearer <token>`. Requests without a valid token receive `401 Unauthorized` with body `{"error":"unauthorized"}`.
- If `FORGE_GATEWAY_TOKEN` is empty or unset, all requests pass through (open access).
- The auth check is a middleware applied to all `/api/` routes.

### `GET /api/sessions`
- Returns a JSON array of session summary objects, combining:
  1. Active sessions from `bus.ListSessions()` (in-memory).
  2. Closed sessions discovered by scanning `*.jsonl` files in `sessionsDir` that are NOT in the active set.
- Each object contains: `sessionId`, `name` (from `metadata.name` if present, else `sessionId`), `status` (`"active"` or `"closed"`), `createdAt`, `lastActiveAt`, `cwd`.
- For closed sessions without bus metadata, `createdAt` and `lastActiveAt` are derived from the first and last JSONL line timestamps.
- Sorted by `lastActiveAt` descending (most recent first).
- Supports `?status=active` or `?status=closed` query param to filter.
- Supports `?limit=N&offset=M` for pagination (defaults: limit=50, offset=0).

### `GET /api/sessions/{sessionId}/history`
- Returns the full JSONL session replayed as a JSON array of `SessionMessage` objects.
- Loads from `sessionsDir/<sessionId>.jsonl` via `session.Store.Load()`.
- Returns `404` if no JSONL file exists and session is not in the bus.
- Returns `200` with an empty array if session exists in bus but has no history file yet.

### `GET /api/sessions/{sessionId}/costs`
- Returns cost breakdown for a single session: total cost, call count, token breakdown, first/last call timestamps.
- Queries `cost_records` table filtered by `session_id`.
- Returns `200` with zeroed fields if no cost records exist for the session.

### `GET /api/costs/summary`
- Returns aggregate cost data for a time range.
- Query params: `?start=YYYY-MM-DD&end=YYYY-MM-DD` (defaults to current calendar month).
- Response: `{ "totalCost", "sessionCount", "callCount", "inputTokens", "outputTokens", "cacheCreationTokens", "cacheReadTokens", "daily": [...] }`.
- Reuses `tracker.GetDailySummaries()`.

### Existing endpoints unchanged
- `POST /sessions`, `GET /sessions/{sessionId}`, `POST /sessions/{sessionId}/messages`, `GET /sessions/{sessionId}/events`, `POST /sessions/{sessionId}/review`, `POST /sessions/{sessionId}/interrupt` remain at their current paths, unaffected.
- New read-only endpoints are namespaced under `/api/` to separate dashboard-facing routes from agent-facing routes and to allow independent auth scoping.

## Constraints
- All new endpoints are read-only (GET). No writes to bus, session files, or cost DB.
- Must not import `internal/agent` or `internal/runtime/loop` — the gateway is a separate layer.
- Must not break existing non-`/api/` routes (the CLI and agent talk to `/sessions/*` directly).
- `cost.Tracker` must be opened in read-only mode (`?mode=ro` SQLite URI) when used by the gateway, to avoid write contention with the agent process that owns the DB.
- Auth middleware must NOT apply to existing `/sessions/*` routes (agents don't send bearer tokens).
- `bus.ListSessions()` must be concurrency-safe (take read lock on `sessions.m`).
- Session list scan of JSONL files must be bounded: read only first and last line of each file for timestamps, not the full file. Cap at 1000 files scanned.
- No new Go dependencies. Standard library `net/http`, `encoding/json`, `database/sql`.

## Interfaces

```go
// bus.go — new export
func ListSessions() []*types.SessionMeta

// session/store.go — new export
type SessionSummary struct {
    SessionID    string `json:"sessionId"`
    FirstTS      int64  `json:"firstTimestamp"`
    LastTS       int64  `json:"lastTimestamp"`
}
func (s *Store) List() ([]SessionSummary, error)

// cost/tracker.go — new export
func NewReadOnlyTracker(dbPath string) (*Tracker, error)
func (t *Tracker) GetSessionCost(sessionID string) (*SessionBreakdown, error)

// gateway/auth.go — new file
func TokenAuthMiddleware(token string) func(http.Handler) http.Handler

// gateway/gateway.go — Config additions
type Config struct {
    Port         int
    Host         string
    WorkspaceDir string
    SessionsDir  string
    Backend      backend.Backend
    CostDBPath   string // path to costs.db (optional; costs endpoints 404 if empty)
    Token        string // optional bearer token
}
```

### Response shapes

```jsonc
// GET /api/sessions
{
  "sessions": [
    {
      "sessionId": "abc-123",
      "name": "troy-barnes-session",
      "status": "active",
      "createdAt": 1715000000000,
      "lastActiveAt": 1715003600000,
      "cwd": "/tmp/forge/workspace"
    }
  ],
  "total": 42,
  "limit": 50,
  "offset": 0
}

// GET /api/sessions/{id}/history
{
  "messages": [ /* array of SessionMessage */ ]
}

// GET /api/sessions/{id}/costs
{
  "sessionId": "abc-123",
  "totalCost": 0.42,
  "callCount": 17,
  "inputTokens": 50000,
  "outputTokens": 12000,
  "cacheCreationTokens": 3000,
  "cacheReadTokens": 8000,
  "firstCall": "2026-05-14T10:00:00Z",
  "lastCall": "2026-05-14T10:30:00Z"
}

// GET /api/costs/summary
{
  "totalCost": 12.34,
  "sessionCount": 8,
  "callCount": 200,
  "inputTokens": 500000,
  "outputTokens": 120000,
  "cacheCreationTokens": 30000,
  "cacheReadTokens": 80000,
  "daily": [
    {
      "date": "2026-05-14",
      "totalCost": 3.21,
      "sessionCount": 2,
      "callCount": 50,
      "inputTokens": 100000,
      "outputTokens": 30000
    }
  ]
}
```

## Edge Cases

1. **No costs.db file** — `CostDBPath` is empty or file doesn't exist. Cost endpoints return `503 Service Unavailable` with `{"error":"cost tracking not available"}`. Session list and history still work.
2. **Concurrent JSONL write during read** — `session.Store.Load()` already holds a read lock. A partially-written last line is silently skipped (JSON parse fails, line is dropped).
3. **Thousands of JSONL files** — `List()` caps at 1000 most-recently-modified files. Older sessions are not surfaced. Response includes `"truncated": true` when cap is hit.
4. **Session exists in bus but no JSONL yet** — `GET /api/sessions/{id}/history` returns `{"messages":[]}`. `GET /api/sessions` shows it as active with bus metadata.
5. **Empty `Authorization` header when token is configured** — returns `401`, not `400`.
6. **`costs.db` locked by agent** — read-only open (`?mode=ro`) avoids `SQLITE_BUSY`. If open still fails, cost endpoints return `503`.
7. **JSONL file with zero lines** — `List()` returns the file with `firstTimestamp: 0, lastTimestamp: 0`. Session list marks `createdAt: 0` which the UI can render as "unknown".

## Tests
- Handler tests using `httptest.NewServer` for each endpoint.
- Token auth: request without header → 401, wrong token → 401, correct token → 200, no token configured → 200.
- Session list: seed bus + create JSONL files → verify merged list, status field, sort order.
- History: write JSONL → load via endpoint → verify message count and structure.
- Session cost: seed cost DB → query → verify totals.
- Cost summary: seed multi-day data → verify daily array and aggregate.
- Pagination: create 60 sessions → default limit returns 50, offset=50 returns 10.

## Rollout
- PR 1 of 2. Ships before the `web-ui` spec (UI bundle).
- Feature-flagged by presence of `FORGE_GATEWAY_TOKEN` (auth) and `costs.db` (cost endpoints).
- No breaking changes to existing gateway routes.

## Out of Scope
- WebSocket or SSE streaming of live events (handled by existing `/sessions/{id}/events`).
- Write operations (send message, interrupt, review trigger) — the UI calls existing routes directly for those in v2.
- Per-user identity, OIDC, RBAC.
- Rate limiting.
