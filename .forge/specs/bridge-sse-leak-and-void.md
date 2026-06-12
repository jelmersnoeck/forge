# Discord bridge: fix SSE connection leak and "thread → session" void

**Status:** implemented on `troy/discord-bridge`, appended to PR #183.

**Filed:** 2026-06-12 (incident response).
**Implementer:** Abed (direct, bypassing Forge — Forge itself was broken; see "Why this didn't go through Forge" below).

## Context

On 2026-06-11 Jelmer had to do emergency surgery on the mac mini — 17,600+
TCP connections were piled up and the host couldn't start things back up.
He killed pelton-feeds, squamish-events, keycard-scout,
hello-anthropic-agent, and openclaw-searxng to reclaim ports. Post-mortem
grep pointed at `forge-discord-bridge` as the primary culprit.

Then, on 2026-06-12, while trying to fix the leak via Forge, we discovered
a second bug: every `@Troy` mention in this very thread went into the void
for ~9 hours. The bridge had silently lost the thread→session mapping
after the spec phase completed.

Two distinct bugs, both rooted in the SSE relay layer.

## Bugs

### Bug 1: SSE connection leak (the 17k TCP conns)

**Root cause A — fresh `http.Client` per subscription.** In
`internal/forge/client.go`, `SubscribeEvents` constructed a new
`&http.Client{}` on every call. Each new client got its own
`http.Transport` (default), which created its own connection pool. On
every reconnect we leaked the previous transport's idle connections —
the runtime had no way to reach them for cleanup since the client
reference was gone the moment the function returned.

**Root cause B — flat-1s reconnect with no cap, no dead-thread reaping.**
In `internal/bridge/bridge.go`, `handleSSEDisconnect` reconnected after a
hardcoded `time.Second` — no exponential backoff, no maximum attempts,
no surrender. If the gateway was unhealthy or the thread was stale, the
bridge spun reconnects every second indefinitely. Each one leaked
another fresh `http.Transport` worth of sockets per Bug A. N stale
threads × hours = thousands of half-open sockets.

**Root cause C — subscribe-failure path was silent.** When
`SubscribeEvents` itself returned an error (gateway 5xx), the relay
goroutine logged and returned — the relay slot was leaked (never marked
dead, never retried), AND any in-flight TCP setup was abandoned. Bug A
and Bug C compounded: every failed subscribe leaked another transport.

### Bug 2: "done" event treated as session terminator → the void

In `Bridge.OnForgeEvent`, the bridge handled `evt.Type == "done"` by
calling `b.sessions.Delete(threadID)` + `cancelRelay(threadID)`. But the
Forge runtime loop emits `done` after **every assistant turn** that
finishes with no tool_use — i.e., the agent stops calling tools and is
awaiting the next user message. The SSE stream stays open. The session
stays alive. The session is reusable for follow-up messages.

So after the spec phase finished on 2026-06-11 at 23:19, the bridge
silently dropped the thread→session mapping for thread
`1514677875402346680`. Every subsequent `@Troy` mention hit
`b.sessions.GetByThread(threadID) == ""` and `onMessageCreate` returned
nil. The messages were silently void.

`bridge_active_sessions 0` from `/metrics` was the only externally
visible symptom — nobody alerts on that.

## Fixes

### `internal/forge/client.go`

- Add a long-lived `sseClient *http.Client` field on `HTTPClient`, with
  a tuned `http.Transport` (`MaxIdleConns=16`,
  `MaxIdleConnsPerHost=4`, `IdleConnTimeout=90s`). No request timeout
  (SSE is long-lived; ctx cancellation handles teardown).
- `SubscribeEvents` reuses `c.sseClient` instead of constructing
  `&http.Client{}`. This is the actual leak fix — one transport, one
  pool, reused forever.

### `internal/bridge/bridge.go`

- Add `reconnectAttempts map[string]int` and `reconnectLastErr
  map[string]string` to `Bridge`. Mutex-protected via the existing
  `b.mu`.
- `OnForgeEvent` now clears both maps on every successful event
  delivery (relay is healthy → reset the counter).
- `handleSSEDisconnect(ctx, thread, session, cause error)`:
  - If `sessions.GetByThread(thread) == ""`, the relay is intentionally
    dead (🛑 / archive); clear tracking and return.
  - Increment attempt counter.
  - If `attempt > sseMaxReconnectAttempts (=10)`: log loud, post a
    warning message to the thread, `cancelRelay`. **Do NOT** delete the
    session mapping — the session may still be revivable via direct
    gateway API, and a future bridge restart picks it up from the
    pinned `forge-meta`. We just stop spinning sockets.
  - Otherwise sleep for `sseBackoffFor(attempt)` (1s → 2s → 4s → 8s →
    16s → 30s cap) and call `startSSERelay`.
- `startSSERelay`: preserve existing translator state across reconnect
  (used to overwrite the `Translator` on every reconnect, losing
  in-flight batching state). Subscribe failure now routes through
  `handleSSEDisconnect` so subscribe errors get backoff + cap, not
  silent leak.
- **REMOVE the `evt.Type == "done"` deletion branch in `OnForgeEvent`.**
  Sessions are only torn down via 🛑 reaction (`onReactionAdd`) or
  thread archival (`onThreadUpdate`). The `done` event remains useful
  for translator side effects (flushing batched text into a Discord
  post) but no longer touches session lifecycle.
- `cancelRelay` also clears the reconnect tracking maps.

### `internal/bridge/admin.go`

- `/metrics` now exposes:
  - `bridge_sse_reconnecting_threads` (gauge: count of threads in
    backoff)
  - `bridge_sse_reconnect_attempts{thread="..."}` (gauge: current
    attempt count per thread)
- These exist so the next time something goes sideways we can see it
  from outside without code archaeology. Bridge log noise alone
  produced ~0 signal during this incident.

## Tests added

- `TestBridge_OnForgeEvent_DoneDoesNotDeleteSession` — regression test
  for Bug 2. Asserts the thread→session mapping survives a `done`
  event. Comment explicitly cites the 2026-06-12 incident date so
  future-me knows why this assertion exists.
- `TestSseBackoffFor_Schedule` — pins the exponential backoff schedule
  (1s, 2s, 4s, 8s, 16s, 30s cap) so we don't regress to flat-1s.
  Includes an overflow-safety case (`attempt=100` → cap).
- `TestBridge_OnForgeEvent_ResetsReconnectAttempts` — confirms healthy
  traffic resets the counter, so a long session can survive many
  individual transient disconnects without exhausting the budget.

## Verification (post-deploy)

```bash
# Bridge fd count should be flat under gateway flapping
lsof -p $(docker inspect -f '{{.State.Pid}}' forge-discord-bridge) -iTCP | wc -l

# Active sessions should be > 0 once a thread is bound
curl -s http://localhost:8087/metrics | grep bridge_active

# Reconnect counter should be visible per-thread when SSE flaps
curl -s http://localhost:8087/metrics | grep bridge_sse_reconnect

# Smoke test for the void fix: send multiple turns in one thread, confirm
# all messages route to forge (check forge-gateway logs).
```

The host-level mitigation Jelmer applied yesterday (killing 5 services
and reclaiming ports) addressed the symptom. This patch addresses the
cause. With both, the bridge should no longer be able to leak the host
into TCP exhaustion even under sustained gateway instability.

## Out of scope

- Gateway-side SSE relay leak (the `startRelay` goroutine in
  `internal/server/gateway/gateway.go` with `relays map[string]struct{}`
  and no cancellation). That was the subject of yesterday's stalled
  spec at `.forge/specs/sse-relay-leak.md` in the orphaned worktree.
  Should be its own follow-up spec once Forge is unblocked.
- The bridge `Rebuild()` is startup-only. If the bridge restarts with
  active sessions, it relies on Discord pinned `forge-meta` messages to
  reconstruct the map. That works for our case but is brittle (Discord
  pin limits, archive races). A persistent local store would be the
  right move long-term. Not urgent now that Bug 2 is fixed — Bug 2 was
  the real silent-loss mechanism.

## Why this didn't go through Forge

Per AGENTS.md, all code work routes through Forge. We tried — twice.
First attempt: forge-gateway container had no tmux + `FORGE_BIN`
mis-defaulted to `forge` → `/forge`. Patched both, re-spawned. Second
attempt: Forge wrote the spec for the gateway-side leak (different bug
from this one), then the phase pipeline stalled spec→coder.

Then we discovered the void: even if Forge had finished, the bridge
couldn't have routed the next message to it because session
`be3afbcd...` had been deleted from the in-memory map after the spec
phase emitted `done`. Three stacked failure modes in Forge itself
(tmux, FORGE_BIN, void), all in the very code path Forge needed to fix
itself. Chicken/egg.

Jelmer gave the explicit "just do it this once" override at 2026-06-12
~09:01 PT. This document and the commit message preserve that exception
for the audit trail. Memory entry filed in `MEMORY.md`.

Follow-up Forge specs to file once Forge is unblocked:

1. Gateway-side SSE relay leak + stale-address bug
   (`.forge/specs/sse-relay-leak.md` content from orphaned worktree —
   recover and refile).
2. Forge phase pipeline does not advance spec → coder reliably.
3. forge-gateway `FORGE_BIN` resolution: `filepath.Abs(Base(...))`
   ordering bug + missing startup check that the binary exists.
4. Bridge session-map persistence (local store), so a bridge restart
   never loses active sessions even if Discord pin scan misses one.
