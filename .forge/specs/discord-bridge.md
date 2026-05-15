---
id: discord-bridge
status: active
---
# Discord ↔ Forge bridge (Troy persona)

## Description
A standalone, containerized service that connects a Discord server to a Forge
gateway. Each Discord forum thread in a configured channel becomes one Forge
session; each Forge SSE event becomes a Discord message (or reaction); each
human reply in the thread becomes a Forge `POST /messages` call. The bot is
Troy — Forge wearing a persona — so the channel reads like a normal coding
conversation with a colleague.

The bridge does not run *inside* Forge. It is a separate process that talks
to Forge over its existing HTTP gateway API. This keeps Forge unaware of
Discord and keeps the bridge replaceable (Slack, Mattermost, etc. later).
It satisfies the build rule: agents run in containers (Docker).

The bridge lives in this repo under `cmd/forge-discord-bridge/` so it can
share types (`internal/types`) with the gateway without copy-paste. It
builds and releases as a separate binary / container image.

> **Design decision: no persistent store.**
>
> The bridge has no database. The two sources of truth are Discord (threads,
> messages, reactions) and the Forge gateway (sessions, events). Introducing
> a third store (SQLite, Redis, etc.) would create a reconciliation problem:
> any crash or incomplete write leaves the database disagreeing with the
> sources it mirrors. Instead, the bridge derives all state from the existing
> sources on startup and keeps only transient bookkeeping in memory. The
> trade-offs (a few duplicate messages on hard crash, lost pending posts on
> SIGKILL) are acceptable for a chat integration and documented in the State
> section below.

## Context
- **Forge gateway HTTP API** (already exists):
  - `POST /sessions` → `{sessionId, metadata}` to create a session
  - `POST /sessions/{id}/messages` → send a user message into a session
  - `POST /sessions/{id}/interrupt` → stop the agent mid-loop
  - `GET /sessions/{id}/events` → SSE stream of `OutboundEvent`s (type, content, toolName, usage, model)
- **OutboundEvent.Type** vocabulary (subset that matters for the bridge):
  `text`, `tool_use`, `done`, `error`, `interrupted`, `thinking`, `compact`,
  `retry`, `usage`, `intent_classified`, `ideation_start`, `clarification_question`,
  `planning_start`, `staleness_warning`, `phase_error`, `pr_url`, `pr_monitor`,
  `task_status`.
- **Discord forum channel model**: a parent channel with threads. The user
  starts a new thread = new task. Bot posts in the thread.
- **OpenClaw bot account `pelton`** is already in the guild "Study Room F"
  (id `1491267748632985700`); it's the same agent identity as me. The bridge
  will use a SEPARATE bot identity (`troy`) so the personas don't collide.
- **Build rule** (MEMORY.md): every agent I build runs in Docker.

## Behavior

### The bot identity
- Bridge runs under a dedicated Discord bot application named **Troy** (or
  `forge-bot` for non-personalized deploys).
- Default avatar/banner ships in the repo so a fresh deploy looks right.
- Status: `Watching: <N> sessions` (live count of active Forge sessions),
  updated on session start/done.
- Username/avatar overridable via env so self-hosted instances can re-brand
  (`BRIDGE_BOT_NAME`, `BRIDGE_BOT_AVATAR_URL`).

### Channel topology
- Bridge is configured for **one or more "Forge channels"** per guild:
  - `forge-channel:1504550234661978343` (e.g. `#forge` in our server)
- The Forge channel SHOULD be a **forum channel**. If it's a regular text
  channel, the bridge still works but creates threads off the trigger message
  instead.
- Inside the configured channel, **every new thread = a new Forge session**.
- Outside the configured channel, the bot ignores everything except direct
  pings (which get a helpful "use #forge to start a task" response).

### Starting a task
1. Human creates a Discord thread in `#forge` with a title + body
   (forum starter message). Title becomes the working name; body is the
   initial prompt.
2. Bridge detects new thread via Discord gateway event (`THREAD_CREATE`).
3. Bridge calls Forge `POST /sessions` with:
   ```json
   {
     "cwd": "<configured repo path for this Discord channel>",
     "metadata": {
       "source": "discord",
       "discord.guildId": "...",
       "discord.channelId": "...",
       "discord.threadId": "...",
       "discord.starterMessageId": "...",
       "discord.userId": "...",
       "discord.username": "...",
       "session.initiator.name": "<resolved git name>",
       "session.initiator.email": "<resolved git email>"
     }
   }
   ```
4. Bridge pins a metadata message to the thread containing a fenced block:
   ````
   ```forge-meta
   session: <forge-session-id>
   ```
   ````
   This is the sole record of the thread↔session mapping (see State).
5. Bridge POSTs the starter-message body to `/sessions/{id}/messages`.
6. Bridge opens an SSE connection to `/sessions/{id}/events` and starts
   translating events back to thread messages.
7. Bridge reacts 🤖 to the starter message to confirm the handoff.

### Continuing a task
- Every subsequent message in the thread (from any non-bot user) is
  forwarded as a Forge user message via `POST /messages`.
- If the agent is currently mid-loop, the bridge does NOT auto-interrupt —
  it lets Forge's existing queueing/interrupt logic handle it. The human
  can react ⏸ to a recent bot message to send an explicit `/interrupt`.
- If the human edits a prior message, the bridge does NOT replay it. Edits
  on already-sent messages are out of scope for v1.

### Translating Forge events to Discord
- **`text`** — buffered and posted as a single Discord message when the
  agent yields. Two flush conditions:
  1. `done` event arrives → flush whatever's buffered.
  2. Buffer ≥ 1800 chars (Discord limit 2000; leave headroom) → flush in
     chunks, split on sentence boundaries when possible.
- **`tool_use`** — collapsed into a Discord embed with the tool name,
  truncated args (200 chars), and a "running…" indicator. When the tool
  result arrives (the next non-tool event), the embed is *edited* to
  "✓ done" or "✗ failed". The embed never grows — long output stays in
  Forge's logs and is linkable from the embed.
- **`thinking`** — surfaced as a 💭 reaction on the most recent bot
  message, removed when the next `text` event arrives. Optional, gated by
  `BRIDGE_SHOW_THINKING` env (default off — too noisy).
- **`intent_classified`** / **`planning_start`** / **`ideation_start`** —
  collapsed into a single "phase header" message at most once per phase
  transition (e.g. `🧭 Intent: feature → planning`). Suppress per-phase
  noise; the user wants progress, not a play-by-play.
- **`clarification_question`** — posted as a normal message with the
  question; the bot WAITS for a reply (no auto-progress) since the next
  human message becomes the answer.
- **`staleness_warning`** — surfaced as a ⚠️ reaction, no message.
- **`phase_error`** / **`error`** — posted as a message prefixed `❌` with
  the error content. Pinned to the thread.
- **`interrupted`** — posted as `⏸ Interrupted by user.`
- **`retry`** — silent. Logged in the bridge but not surfaced.
- **`compact`** — silent.
- **`usage`** — accumulated; surfaced once at the end of the run in the
  `done` summary (see below). Never per-call.
- **`pr_url`** — posted as a celebratory message: `🚀 PR ready: <url>`,
  pinned to the thread. The starter message gets a 🚀 reaction.
- **`pr_monitor`** — silent. CI status changes can be reflected later.
- **`done`** — posts a final summary embed: total cost, tokens (in/out),
  models used, wall time, PR url if any. Flushes any remaining text buffer.
  Removes any 💭 reactions.

### Bot reactions humans can use
The bridge polls/listens for reactions on its own messages:
- ⏸ on any bot message → `POST /interrupt` for that session.
- 🔁 on a bot message → re-send the prior human message (rare; recovery
  path for transient API errors).
- 🛑 on the *starter* message → end session, archive thread.
- Anything else → ignored, no error.

### Lifecycle
- **Session ends** when:
  - Forge emits `done` with `pr_url` set → bridge marks session "complete",
    keeps thread open for follow-up but stops the SSE relay.
  - Thread is archived in Discord → bridge calls `/interrupt` then drops
    the mapping.
  - User reacts 🛑 to starter → bridge calls `/interrupt`, archives the
    thread, drops the mapping.
  - Bridge crash/restart → see State / Startup reconciliation below.
- **Idle eviction**: a thread with no activity for 7 days and no active
  Forge session has its mapping pruned. The thread stays in Discord;
  re-posting in it does NOT auto-revive (user must create a new thread).

### State

The bridge is stateless on disk. All bookkeeping lives in memory and is
rebuilt from Discord + Forge on startup.

#### Thread ↔ session mapping (pinned metadata message)
Each Forge-managed thread contains a pinned message with a fenced metadata
block:
````
```forge-meta
session: <forge-session-id>
```
````
On startup the bridge scans every active (non-archived) thread in each
configured channel, reads pinned messages, extracts `session:` values,
and rebuilds the in-memory `map[threadID]sessionID`. This is the only
mapping; there is no external database.

*Why pinned messages?* They are visible to humans debugging the bridge,
discoverable via the Discord API in a single call per thread
(`GET /channels/{id}/pins`), and survive bot restarts. Thread-name
suffixes were considered but are ugly in the UI and limited to ~100 chars.
Thread metadata fields are not flexible enough and lack human visibility.

#### Event-ID deduplication (in-memory ring buffer)
Each active session keeps a ring buffer of the last 256 Forge event IDs
(`eventID string`). Before posting a Discord message for an event, the
bridge checks the buffer. If present, the event is skipped.

On restart, buffers are empty. The bridge replays SSE from the start
(Forge does not support `Last-Event-ID` resume yet) and re-checks
Discord: events whose content already appears as a recent bot message in
the thread are skipped heuristically (match on first 100 chars of
content). A handful of duplicates may slip through on hard crash; this
is acceptable and self-evident ("you see a message twice after the bot
restarts; we know").

#### Outbound retry queue (in-memory FIFO)
Failed Discord posts (rate-limited, transient 5xx) are appended to an
in-memory FIFO with exponential backoff (1s → 4s → 16s → 60s → 5m →
30m → dead-letter log at 6h). A single goroutine drains the queue.

On graceful shutdown (`SIGTERM`): the drain goroutine gets a context
cancellation + 5s grace period to flush remaining items. On hard crash
(`SIGKILL`): pending posts are lost. This is acceptable for chat
messages — the Forge session still has the full event history; the
Discord thread is missing at most a few trailing messages.

#### Startup reconciliation
1. Scan configured channels → list active threads → read pinned messages
   → rebuild `threadID→sessionID` map.
2. For each mapped session, re-open the SSE subscription to
   `/sessions/{id}/events` (full replay; dedupe via ring buffer +
   heuristic).
3. Start the outbound queue drain goroutine (empty on fresh start).
4. Set `/readyz` to 200 once reconciliation completes.

#### Discord rate limit handling
Respect `X-RateLimit-Remaining` / `Retry-After`; serialize bursts per
channel; coalesce multiple text events into one message rather than
spraying.

### Configuration
Env (all required unless noted):
- `DISCORD_BOT_TOKEN`
- `DISCORD_GUILD_ID` (single guild for v1; multi-guild later)
- `FORGE_GATEWAY_URL` — e.g. `http://forge-gateway:3000`
- `BRIDGE_LISTEN_ADDR` (default `:8080`, for healthchecks + admin)
- `BRIDGE_BOT_NAME` (optional override)
- `BRIDGE_BOT_AVATAR_URL` (optional)
- `BRIDGE_SHOW_THINKING` (default `false`)
- `BRIDGE_LOG_LEVEL` (default `info`)

Channel config (JSON file mounted at `/config/channels.json`, hot-reloaded
on SIGHUP):
```json
{
  "channels": [
    {
      "channelId": "1504550234661978343",
      "repoPath": "/code/forge",
      "defaultBaseBranch": "main",
      "allowedUserIds": null
    }
  ]
}
```
- `allowedUserIds: null` (or missing) → anyone in the channel can start a task.
- `allowedUserIds: [...]` → only listed Discord user ids can start a task.
  Others get a polite refusal and a 🚫 reaction.

### Admin HTTP API
On `BRIDGE_LISTEN_ADDR`:
- `GET /healthz` — returns 200 if Discord WS is connected and Forge
  gateway is reachable.
- `GET /readyz` — 200 once initial reconciliation is complete.
- `GET /sessions` — list active mappings (auth: `Authorization: Bearer
  <BRIDGE_ADMIN_TOKEN>` env, optional; if unset, the route is disabled).
- `POST /sessions/{threadId}/interrupt` — manual interrupt (same auth).
- `GET /metrics` — Prometheus format: active session count, events/sec by
  type, Discord rate-limit retries, Forge API errors.

### Containerization
- Single Dockerfile (Go binary, distroless final stage).
- `docker-compose.yml` example in `deploy/` showing bridge + forge-gateway.
  No named volumes needed — the bridge has no on-disk state.
- Health check uses `/healthz`.
- Non-root user. No bind mounts of host paths beyond `/config`. Forge
  worktrees are NOT mounted into the bridge container — the bridge only
  talks HTTP to Forge.

## Constraints
- The bridge speaks the public Forge HTTP API only. It MAY import
  `internal/types` for the `OutboundEvent` contract (and similar small
  shared structs) since the binaries ship from the same repo, but it MUST
  NOT import handlers, the bus, the session store, or any other gateway
  internals. The wire is the public API; sharing struct definitions is a
  build-time convenience, not a coupling.
- The bridge MUST handle SSE reconnection. If Forge gateway restarts mid-
  session, the bridge reconnects with backoff and reports the gap as a
  ⚠️ reaction on the most recent message.
- The bridge MUST be idempotent on inbound Discord events (Discord may
  deliver `THREAD_CREATE` multiple times during gateway resumes).
- The bridge MUST NOT post the API key / model name / session id in
  channel content. Session id goes in the `done` summary embed *only* if
  `BRIDGE_REVEAL_SESSION_ID=true`.
- The bridge MUST NOT respond to its own messages (loop prevention).
- Discord thread names: **ASCII only** — see MEMORY.md re: `Invalid Form
  Body` on non-ASCII. Sanitize on rename if we ever auto-rename threads.
- Containers only. No `go run` on the host as a deploy path.

## Interfaces

```go
// cmd/forge-discord-bridge/main.go — single binary entrypoint.

// internal/discord/ — discordgo wrapper, gateway events, reactions, posting.
type Client interface {
    PostMessage(ctx context.Context, threadID, content string, opts ...PostOption) (messageID string, err error)
    EditMessage(ctx context.Context, threadID, messageID, content string) error
    AddReaction(ctx context.Context, threadID, messageID, emoji string) error
    RemoveReaction(ctx context.Context, threadID, messageID, emoji string) error
    ArchiveThread(ctx context.Context, threadID string) error
    SubscribeEvents(ctx context.Context) (<-chan Event, error)
}

// internal/forge/ — Forge HTTP gateway client.
type Forge interface {
    CreateSession(ctx context.Context, cwd string, metadata map[string]any) (sessionID string, err error)
    SendMessage(ctx context.Context, sessionID, text string) error
    Interrupt(ctx context.Context, sessionID string) error
    SubscribeEvents(ctx context.Context, sessionID, sinceEventID string) (<-chan types.OutboundEvent, error)
}

// internal/bridge/ — translation layer.
type Bridge struct {
    forge    Forge
    discord  Client
    sessions *SessionMap   // in-memory threadID→sessionID, rebuilt on startup
    dedup    *EventDedup   // per-session ring buffers (256 entries each)
    outbox   *RetryQueue   // in-memory FIFO with exponential backoff
    cfg      Config
}

func (b *Bridge) OnDiscordEvent(ctx context.Context, evt discord.Event) error
func (b *Bridge) OnForgeEvent(ctx context.Context, threadID string, evt types.OutboundEvent) error

// SessionMap rebuilds from Discord pinned messages on startup.
type SessionMap struct {
    mu       sync.RWMutex
    byThread map[string]string // threadID → sessionID
    bySession map[string]string // sessionID → threadID (reverse index)
}

func (m *SessionMap) Rebuild(ctx context.Context, discord Client, channelIDs []string) error

// EventDedup holds a ring buffer of recently-seen event IDs per session.
type EventDedup struct {
    mu      sync.Mutex
    buffers map[string]*ring.Buffer[string] // sessionID → ring buffer (cap 256)
}

func (d *EventDedup) Seen(sessionID, eventID string) bool
func (d *EventDedup) Record(sessionID, eventID string)

// RetryQueue is an in-memory FIFO for failed outbound Discord posts.
type RetryQueue struct {
    items []RetryItem
    mu    sync.Mutex
}

func (q *RetryQueue) Enqueue(item RetryItem)
func (q *RetryQueue) Drain(ctx context.Context) // blocks until ctx cancelled; 5s grace
```

Since the bridge lives in the same repo, it imports `OutboundEvent` and
sibling structs directly from `internal/types`. If we later split the
bridge into its own repo, the move is mechanical: copy the contract struct
over and pin the gateway version it targets.

## Edge Cases
- **Thread created by a bot** (e.g. another integration): ignore, no
  session created.
- **Forge gateway unreachable on thread create**: post `⏳ Forge is
  unreachable. Will retry…` to the thread, enqueue the create, retry with
  backoff. Surface ❌ after 5 minutes of failure.
- **Discord token revoked mid-run**: bridge restarts, fails `/healthz`,
  orchestrator (compose / k8s) restarts it. Sessions rebuild from pinned
  messages; SSE replays from start with ring-buffer dedup.
- **Two humans in the same thread**: both can send messages; both go
  through. Forge sees a flat user message stream. No author attribution
  for now (Forge doesn't model per-message authors). Out of scope: tag
  the human in `commit.coAuthor`.
- **Very long agent output**: chunked into multiple messages on sentence
  boundaries. Code blocks split at fence boundaries to avoid mid-block
  splits.
- **Forge emits a `text` event mid-tool**: buffer it. Don't post until
  the tool resolves (it'd look like the bot is talking over itself).
- **PR creation fails**: surface the `phase_error` and DO NOT silently
  retry. The user can react 🔁 to retry.
- **Discord WebSocket disconnect**: discordgo handles reconnect. While
  disconnected, outbound posts queue in the in-memory retry queue. Inbound
  events during the gap are recovered via Discord's resume token (discordgo
  built-in) or missed entirely if resume fails — log a warning, post a ⚠️
  to the starter message of any active thread on reconnect.
- **Same human types two messages in quick succession**: forwarded
  in-order. No coalescing on the bridge side — let Forge's queueing
  decide.
- **Forge session id collision** (UUID birthday-paradox bullshit): not a
  real concern at our scale; treat as impossible. If a UUID collision
  surfaces, log error and refuse the create.

## Tests
- **Unit tests** for the event translator: golden inputs (`OutboundEvent`
  streams) → expected Discord call sequences. Cover all event types
  listed above.
- **Unit tests** for chunking: long text, long code blocks, mixed
  fences, weird whitespace.
- **Integration test** with a fake Forge gateway (in-process HTTP server
  + scripted SSE stream) and a Discord stub. Drives: thread create →
  session create → message → events → done → summary.
- **Restart resilience test**: kill the bridge mid-stream, restart, assert
  session map rebuilds from pinned messages, SSE reconnects, and at most a
  few duplicate messages appear (no data loss beyond pending outbox).
- **Idempotency test**: replay the same `THREAD_CREATE` twice; assert one
  session, one mapping.
- **Container test**: `docker build` succeeds; the resulting image runs
  `/healthz` green against a stub.

## Rollout
1. Build the bridge in this repo under `cmd/forge-discord-bridge/`.
   New binary, new Dockerfile, new compose entry. Existing `forge` /
   `forge gateway` / `forge agent` binaries untouched.
2. Create a new Discord application "Troy" (separate from `pelton`).
   Required bot scopes: `bot`, `applications.commands`. Bot permissions:
   `View Channels`, `Send Messages`, `Send Messages in Threads`, `Create
   Public Threads`, `Manage Threads` (archive), `Add Reactions`, `Read
   Message History`, `Embed Links`, `Attach Files`.
3. Stage: deploy on the Mac mini via docker-compose alongside a local
   Forge gateway. Test against a private channel in Study Room F.
4. Iterate on event-translation noise level until threads feel like
   conversation, not telemetry.
5. Cut a `v0.1.0` tag once a Troy PR is opened, reviewed, and merged
   *via the bridge end-to-end*.

## Out of Scope
- **No persistent store.** The bridge has no database. Trade-offs (duplicate
  messages on hard crash, lost pending posts on SIGKILL) are documented in
  the State section.
- **Resumable SSE in Forge gateway.** Bridge will replay-from-zero +
  dedupe via in-memory ring buffer until Forge gateway adds a resume header.
  Separate spec: extend `GET /events` to honor `Last-Event-ID`.
- **Multi-guild support.** v1 is single-guild. The config shape is
  guild-agnostic so we can add later.
- **Slack / Mattermost / Matrix bridges.** Same translator core, but
  delivery layer differs. Not now.
- **Keycard-vended credentials for the bridge.** v1 reads
  `DISCORD_BOT_TOKEN` from env. Phase 2 fetches it from Keycard at boot.
- **Per-message human attribution in commits.** Out of this spec; tracked
  separately under the autonomous-attribution follow-ups (gateway-injected
  `GIT_AUTHOR_*`).
- **Inline action buttons** (Discord components v2). v1 uses emoji
  reactions only — broad client support, no migration friction. Buttons
  later.
- **Voice/stage channels.** lol no.
