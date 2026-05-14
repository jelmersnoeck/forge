---
id: web-ui
status: draft
---
# Forge Web UI — live + historical session dashboard

## Description
A browser-based dashboard that shows every Forge session — active *and*
closed — and lets the operator drill into what's happening (or what
happened) inside each one. Read-only for v1: live tail of events, full
historical replay, cost + token rollups, status filters. No
send-message-from-the-web yet; that's a later spec.

The UI ships as a static SPA served by the existing Forge gateway binary
on a configurable path (default `/ui`), backed by a small set of new HTTP
endpoints. No new daemon, no JS framework cult — a single static bundle
served by the gateway, talking JSON + SSE to the gateway it lives in.

It runs in the same gateway container the rest of Forge already runs in,
so the build rule is satisfied (no host process, no separate container
unless someone wants to deploy it that way).

## Context
Today the gateway exposes:
- `POST /sessions` → create
- `GET /sessions/{id}` → single-session metadata
- `POST /sessions/{id}/messages` / `…/review` / `…/interrupt` → mutate
- `GET /sessions/{id}/events` → live SSE stream

Today the gateway does NOT expose:
- Any list endpoint (`GET /sessions` does not exist).
- Any historical replay (the bus only knows live SSE; closed sessions are
  only on disk in `sessions/<id>.jsonl`).
- Any aggregate cost view tied to the gateway (`forge stats` reads
  `~/.forge/costs.db` from a CLI; the gateway never touches it).

Sources of truth on disk:
- `internal/server/bus/bus.go` — in-memory `SessionMeta` map (active only).
- `internal/runtime/session/store.go` — `<baseDir>/<sessionId>.jsonl`,
  append-only history of `types.SessionMessage`.
- `internal/runtime/cost/tracker.go` — SQLite at `~/.forge/costs.db`,
  rows keyed by `session_id`. Already indexes by session id.

The UI is the consumer; the gateway grows the endpoints it needs.

## Behavior

### New gateway HTTP endpoints (additive)

All return JSON unless noted. All gated by the same admin token check
(optional `Authorization: Bearer <FORGE_GATEWAY_TOKEN>` env; if unset,
the routes are open — matches today's posture).

- `GET /sessions`
  Query: `?status=active|closed|all` (default `all`),
  `?limit=N` (default 50, max 500), `?cursor=<opaque>` for pagination,
  `?q=<substr>` (matches session id and metadata values).
  Returns:
  ```json
  {
    "sessions": [
      {
        "sessionId": "...",
        "status": "active|closed",
        "cwd": "...",
        "title": "<derived: first 80 chars of first user message, or sessionId>",
        "createdAt": 0,
        "lastActiveAt": 0,
        "messageCount": 12,
        "model": "claude-...",       // last-used
        "costUSD": 0.42,
        "tokens": {"in": 0, "out": 0, "cacheCreation": 0, "cacheRead": 0},
        "phase": "coder|reviewer|finalize|...",  // last seen
        "metadata": { ... }
      }
    ],
    "nextCursor": "..." | null
  }
  ```
  Implementation: union of (a) in-memory `bus.SessionMeta` and (b) JSONL
  files in the session base dir. JSONL discovery: scan directory, parse
  filename, stat for mtime. Costs joined via SQLite query
  `SELECT session_id, SUM(cost), SUM(input_tokens), ... GROUP BY session_id`.
  Cache the cost rollup for 5s to avoid hammering SQLite on a busy
  dashboard.

- `GET /sessions/{id}/history`
  Query: `?from=<seq>&limit=N` (default 500).
  Returns the full or paged JSONL contents as a normalized event array:
  ```json
  {
    "sessionId": "...",
    "messages": [
      {
        "seq": 0,
        "role": "user|assistant|tool|system",
        "type": "text|tool_use|tool_result|usage|phase|...",
        "content": "...",
        "toolName": "...",
        "timestamp": 0,
        "metadata": { ... }
      }
    ],
    "hasMore": false
  }
  ```
  Source: `session.Store.Load(id)`. The `types.SessionMessage` shape is
  flattened into this normalized event row so the UI has one rendering
  path for live + historical.

- `GET /sessions/{id}/stats`
  Returns:
  ```json
  {
    "costUSD": 0.42,
    "tokens": {"in": 0, "out": 0, "cacheCreation": 0, "cacheRead": 0},
    "perModel": [
      {"model": "claude-opus-...", "callCount": 4, "costUSD": 0.31, "tokens": {...}}
    ],
    "durationMs": 123456,
    "phaseTimings": {"intent": 1200, "spec": 3400, "coder": 80000, "review": 12000, "finalize": 4000}
  }
  ```
  Source: SQLite + scan of the JSONL for phase boundaries.

- `GET /events` (gateway-wide multiplex)
  SSE stream of session-lifecycle events:
  `session_created`, `session_closed`, `session_updated` (status/phase
  change), plus a lightweight `event_summary` `{sessionId, type, ts}`
  for top-level "what's hot" rendering.
  Existing `GET /sessions/{id}/events` keeps streaming full per-session
  events for the detail view.

### The UI itself

Stack:
- **Vanilla TypeScript + Vite + Preact**. No Next, no Tailwind religion.
  Output is a static bundle (`web/dist/`) embedded into the Go binary
  via `embed.FS`. One binary, no separate server, no node-runtime in
  the container.
- **Styling**: small CSS file, dark-by-default with a `prefers-color-scheme`
  light fallback. No CSS framework. Inline SVG icons.
- **State**: a single `Store` (signals or Zustand-lite — TBD in implementation).
  SSE feeds the store; the UI re-renders off the store.

Routes (client-side, hash router so it works behind any path prefix):
- `#/` — sessions list.
- `#/sessions/:id` — session detail.
- `#/stats` — aggregate cost + rate (later; v1 stub).

#### Sessions list (`#/`)
- Header: search box, status filter pills (`Active`, `Closed`, `All`),
  count of currently-streaming sessions, total $ today.
- Body: table-ish layout (not a `<table>` — flex rows for responsive).
  Columns: status dot (🟢 active / ⚪ closed / 🔴 errored),
  title (derived), repo (basename of `cwd`),
  phase (badge), age, model, cost, last activity.
- Active rows pulse a subtle dot when a new event arrives (visual
  liveness indicator).
- Click row → detail view.
- Refresh: list is hydrated from `GET /sessions`; live updates from the
  `/events` SSE feed (`session_created`, `session_closed`, `session_updated`).
  No polling.

#### Session detail (`#/sessions/:id`)
- Top: title, sessionId (copy button), cwd, metadata (source=discord,
  thread id, initiator, etc. — rendered as key/value pills).
- Sidebar (right or collapsible bottom on mobile): stats panel — cost,
  tokens by type, models, phase timings, an "open in terminal" hint
  (`forge --gateway ... --resume <id>`).
- Main pane: event timeline.
  - For ACTIVE sessions: load history via `GET /history`, then attach
    SSE `GET /events` to tail. Merge by timestamp; dedupe by event id.
  - For CLOSED sessions: history only, no SSE attachment.
  - Each event rendered by type:
    - `text` → markdown rendered (sanitized, no HTML), monospaced
      code blocks with syntax highlighting via `highlight.js` (lazy-
      loaded chunk).
    - `tool_use` → collapsible card with tool name + args (`Bash`,
      `Edit`, etc.). Collapsed by default; click to expand args.
      Result attached when `tool_result` arrives.
    - `thinking` → italic muted block, collapsed unless
      "show thinking" toggle is on.
    - phase events → horizontal divider with the phase name as a chip.
    - `usage` → small inline cost tag at the end of each turn.
    - `pr_url` → pinned at top of timeline + the URL highlighted.
    - errors → red callout with copy button on the message.
  - Auto-scroll to bottom when new events arrive AND the user is
    already pinned to bottom. Pause auto-scroll if the user scrolls up.
- A small status strip across the top: connection state (`SSE: live` /
  `SSE: reconnecting in 2s` / `closed`), latency to gateway,
  current model.
- No write actions in v1. Interrupt / send-message are future work.

### Auth
- Same model as the rest of the gateway today: optional `Authorization:
  Bearer <FORGE_GATEWAY_TOKEN>` env. If set, the UI presents a one-time
  paste-token modal on first load, stores it in `localStorage`, and
  sends it on every request (including the SSE EventSource via a
  query-param fallback since EventSource can't set headers — see
  Constraints).
- If unset, UI is open. Same security posture as the API today; flagged
  loudly in the deployment docs.

### Mounting / config
- Default mount path: `/ui` (so the gateway can serve other routes too).
  Override via `FORGE_UI_PATH=/somewhere/else` or
  `FORGE_UI_DISABLE=1`.
- The static bundle is embedded; no on-disk dependency. `just build`
  produces a single binary.
- Dev mode: `just dev-ui` runs Vite on `:5173` with proxy rules to the
  gateway on `:3000`. Production builds embed `web/dist/`.

## Constraints
- The UI MUST work read-only with today's gateway plus the additive
  endpoints. No breaking changes to existing routes.
- SSE auth via query-param (`?token=...`) is acceptable since EventSource
  cannot set headers. Document this explicitly; redact tokens from
  gateway logs (already done in the bus log code — verify).
- The embedded bundle MUST NOT increase the gateway binary by more than
  ~3 MB compressed. Lean assets; lazy-load `highlight.js`.
- No third-party telemetry, no fonts from CDNs. Self-contained.
- The UI MUST handle session-id collisions across cwd cleanly: same id,
  different cwd → list as two rows visually if it ever happens (it
  shouldn't, but defensive). Today UUIDs prevent this.
- Cost rollup MUST handle the case where the SQLite DB is missing or
  empty (fresh install). Return zeros; do not 500.
- History reads MUST stream large JSONL files without slurping —
  `session.Store` is sync today; the new handler reads in chunks,
  emits NDJSON when client requests `Accept: application/x-ndjson` for
  very long histories. Default JSON response for small ones.

## Interfaces

```go
// internal/server/gateway/sessions_list.go
func handleListSessions(cfg Config) http.HandlerFunc

// internal/server/gateway/sessions_history.go
func handleSessionHistory(cfg Config) http.HandlerFunc

// internal/server/gateway/sessions_stats.go
func handleSessionStats(cfg Config) http.HandlerFunc

// internal/server/gateway/events.go (new)
// Multiplexed lifecycle SSE feed across all sessions.
func handleGatewayEvents(w http.ResponseWriter, r *http.Request)

// internal/server/bus/bus.go (extend)
// PublishLifecycle emits a session_created/closed/updated event on
// the gateway-wide channel.
func PublishLifecycle(kind string, sessionID string, payload map[string]any)
func SubscribeLifecycle() (<-chan LifecycleEvent, func())
```

```go
// internal/runtime/cost/tracker.go (extend)
// SessionTotals returns a map of sessionID → totals for the given ids.
// Empty input → all sessions. Bounded by the SQLite index on session_id.
func (t *Tracker) SessionTotals(ctx context.Context, ids []string) (map[string]SessionTotals, error)

type SessionTotals struct {
    CostUSD       float64
    InputTokens   int64
    OutputTokens  int64
    CacheCreation int64
    CacheRead     int64
    LastModel     string
}
```

```go
// internal/runtime/session/store.go (extend)
// Stream emits messages incrementally; the handler can flush per chunk.
func (s *Store) Stream(sessionID string, from int) (<-chan types.SessionMessage, func(), error)

// Discover scans the base dir for session ids + mtimes (closed sessions).
func (s *Store) Discover() ([]DiscoveredSession, error)
```

```ts
// web/src/api.ts
export interface SessionRow {
  sessionId: string;
  status: "active" | "closed" | "errored";
  cwd: string;
  title: string;
  createdAt: number;
  lastActiveAt: number;
  messageCount: number;
  model: string;
  costUSD: number;
  tokens: TokenBreakdown;
  phase?: string;
  metadata: Record<string, unknown>;
}

export const api = {
  listSessions(params: ListParams): Promise<SessionList>,
  getHistory(id: string, from?: number, limit?: number): Promise<History>,
  getStats(id: string): Promise<Stats>,
  subscribeSession(id: string, sinceEventId?: string): EventSource,
  subscribeGateway(): EventSource,
};
```

## Edge Cases
- **Session in memory but no JSONL yet** (newly created, no messages):
  list it with `messageCount: 0`, `lastActiveAt = createdAt`.
- **JSONL on disk but bus forgot it** (gateway restart): treat as
  `status: "closed"`. Add a "Reactivate" affordance later, NOT in v1.
- **Corrupt JSONL line**: skip with a warning event in the response
  (`{warnings: ["line 42: invalid json"]}`); do not 500.
- **Very long session histories** (10k+ events): client requests in
  chunks via `?from=&limit=`, renders only visible chunk + windowed
  list. Don't try to render 10k DOM nodes.
- **Search across large history** (later): out of scope; v1 search is
  metadata-only on the list view.
- **Cost DB locked** (writer holds exclusive lock): the read endpoint
  uses `PRAGMA query_only` + short timeout; on timeout return cached
  rollup if recent, else zeros + a header `X-Forge-Costs: stale`.
- **SSE behind reverse proxy** with buffering: emit `:keepalive` comment
  every 15s and recommend `proxy_buffering off;` in docs.
- **Browser sleeps / network drop**: EventSource auto-reconnects; the
  UI dedupes by event id and refetches `/history?from=lastSeq` on
  resume.
- **Multiple tabs open**: each tab maintains its own EventSource. The
  gateway bus already supports fan-out subscribers per session.
- **Closed-then-resumed session** (CLI `--resume`): the bus re-attaches
  on next gateway boot? Today's behavior is sessions are recreated by
  the CLI calling `POST /sessions` again. For now: a resumed session
  gets a new id; the old one stays "closed" in the UI. Future:
  surface a `resumedFrom` metadata link.

## Tests
- **Endpoint tests** (`internal/server/gateway/*_test.go`): table-driven
  for list/history/stats with mixed in-memory + on-disk fixtures.
  Cover status filter, pagination cursors, empty-cost-db case,
  corrupt-jsonl case.
- **Cost rollup tests** (`internal/runtime/cost/tracker_test.go`):
  fixture DB with 3 sessions × 3 models; assert SessionTotals.
- **Lifecycle event tests**: assert `PublishLifecycle` reaches gateway
  SSE subscribers within 100ms.
- **UI**: component tests for the event renderers (tool_use, text,
  thinking, error) with golden JSON inputs. Use Playwright for a smoke
  e2e: list → click row → see live events → close session → see
  status flip.
- **Bundle size guard**: CI step asserts `web/dist/` gzipped < 1 MB
  and embedded binary delta < 3 MB.

## Rollout
1. Land the gateway endpoints first; verify with curl. They're useful
   even without a UI.
2. Land the UI in a follow-up PR — keeps the diff reviewable.
3. Document the optional `FORGE_GATEWAY_TOKEN` in README; recommend
   setting it before exposing the gateway over Tailscale or to a
   network you don't fully trust.

## Out of Scope
- **Write actions from the UI** (send message, interrupt, retry).
  Read-only is intentional for v1 to keep the security surface small
  and to ship faster. Separate spec when we add it.
- **Diff / file-explorer view of the worktree.** Useful, big, separate.
- **Multi-gateway federation** (one UI viewing several gateways at
  once). v1 is one-gateway-one-UI.
- **Authentication beyond a shared bearer token.** No OIDC, no per-user
  identity. Bridge to Keycard later — separate spec.
- **WebSocket-based bidirectional control.** Stick to SSE for v1.
- **Stats page** beyond a stub. Detailed cost analytics already live in
  `forge stats`; the UI surfaces totals + per-session, not historical
  charts. Real `#/stats` is a separate spec.
- **Mobile-first.** Responsive enough to read on a phone, but the
  primary surface is desktop.
- **Realtime collaboration cursors / who's-watching presence.** lol no.
