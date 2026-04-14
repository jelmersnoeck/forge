---
id: rename-server-to-gateway
status: implemented
---
# Rename `forge server` subcommand to `forge gateway`

## Description
Rename the `forge server` subcommand to `forge gateway` to avoid confusion with
the agent's own background HTTP server. The gateway is a session-management
proxy — it spawns agents, not serves them. The `--server` CLI flag becomes
`--gateway`.

## Context
- `cmd/forge/main.go` — subcommand dispatch + help text
- `cmd/forge/gateway.go` — `runGateway()` function (renamed from server.go), log messages, daemon output
- `cmd/forge/cli.go` — `--gateway` flag (was `--server`), error messages, gatewayURL variables, hints
- `justfile` — `dev-gateway`, `dev-gateway-daemon`, `stop-gateway`, `tail-gateway` recipes
- `AGENTS.md` — documentation references
- `README.md` — usage docs
- `.forge/specs/forge-architecture.md` — architecture spec
- `internal/server/gateway/gateway.go` — package doc comment

## Behavior
- `forge gateway` replaces `forge server` as the subcommand
- `forge gateway -daemon` replaces `forge server -daemon`
- `--gateway` CLI flag replaces `--server` flag
- Log messages say "forge gateway" not "forge server"
- Daemon output says "forge gateway started in background"
- justfile recipes: `dev-gateway`, `dev-gateway-daemon`, `stop-gateway`, `tail-gateway`
- Help text updated throughout
- All docs (AGENTS.md, README.md, specs) updated

## Constraints
- Do not rename the `internal/server/` package tree — "server" is accurate for the HTTP server code
- Do not rename `GATEWAY_PORT`/`GATEWAY_HOST` env vars — they already say gateway
- File renamed from `cmd/forge/server.go` → `cmd/forge/gateway.go`
- Backward compat preserved: `forge server` and `--server` still work with deprecation notices

## Interfaces
No type/API changes. Pure rename of user-facing strings and flags.

## Edge Cases
- User runs `forge server` — should still work, prints deprecation notice pointing to `forge gateway`
- User uses `--server URL` — should still work, prints deprecation notice pointing to `--gateway`
- Scripts using `just dev-server` — old recipes removed, new names used
