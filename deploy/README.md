# Forge gateway + Discord bridge deploy

Compose stack: `forge-gateway` (the agent runtime) and `discord-bridge`
(Discord ↔ Forge glue). Designed to run on a single host (e.g. a Mac mini)
behind Tailscale.

## Prerequisites

1. **Anthropic API key** present in an OpenClaw agent profile at
   `~/.openclaw/agents/main/agent/auth-profiles.json` (profile type
   `api_key`). The build script extracts it automatically.
2. **Discord bot application** — create via
   <https://discord.com/developers/applications>, copy the bot token.
3. **Secrets file** at `~/.openclaw/workspace/secrets/discord-bridge.env`:
   ```
   DISCORD_BOT_TOKEN=...
   DISCORD_GUILD_ID=1491267748632985700
   # optional:
   # BRIDGE_BOT_NAME=troy
   # BRIDGE_LOG_LEVEL=info
   ```
   `chmod 600` it.

## Run

From the repo root:

```bash
deploy/scripts/build-env.sh     # generates deploy/.env (0600) from secrets
cd deploy
docker compose --env-file .env up -d --build
docker compose logs -f
```

Stop with `docker compose --env-file .env down`. Add `-v` to nuke session
state.

## Ports

| Service          | Host  | Container | Notes                                |
|------------------|-------|-----------|--------------------------------------|
| forge-gateway    | 3000  | 3000      | Forge HTTP API.                      |
| discord-bridge   | 8087  | 8080      | Admin API (`/healthz`, `/readyz`).   |

Host port 8080 is taken by `spray-wall`; bridge admin sits on **8087**.

## Filesystem coupling

- The **bridge** never sees host paths. The bridge image is portable.
- The **gateway** mounts `${FORGE_WORKSPACE_DIR}` at `/workspace` and runs
  with `WORKSPACE_DIR=/workspace`. The gateway is what knows where code
  lives. `FORGE_WORKSPACE_DIR` defaults to `$HOME/code/forge`.

### Single-repo limitation (v1)

The gateway operates inside one repo per instance. If you want multiple
Discord channels mapping to multiple repos, run multiple gateway instances
(different `GATEWAY_PORT` and `FORGE_WORKSPACE_DIR`) and point separate
bridges at them. A multi-repo gateway is tracked as a follow-up spec.

## Dashboard

Both services declare `dashboard.enable: "true"` labels, so they show up
on `http://jelmers-mac-mini` under the **Forge** category.

## Channel mapping

`deploy/channels.json` lists the Discord channels the bridge will react to.
Schema:

```json
{
  "channels": [
    {
      "channelId": "1504550234661978343",
      "defaultBaseBranch": "main",
      "allowedUserIds": null
    }
  ]
}
```

- `defaultBaseBranch` — Forge cuts task branches off this.
- `allowedUserIds: null` — anyone can trigger Forge from the channel.
- `allowedUserIds: [..]` — only those Discord user ids can trigger work.

Reload after edits without restarting:

```bash
docker kill -s HUP forge-discord-bridge
```

## Smoke test

After `docker compose up -d`:

```bash
curl -fsS http://localhost:3000/health        # gateway up
curl -fsS http://localhost:8087/healthz       # bridge up + Discord WS
curl -fsS http://localhost:8087/readyz        # bridge fully reconciled
```

Then in Discord: open a thread off any message in a configured channel and
type a directive. The bridge replies in-thread with a pinned `forge-meta`
fenced block containing the session id; Forge streams output as the run
progresses.
