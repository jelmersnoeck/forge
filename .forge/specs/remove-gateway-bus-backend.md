---
id: remove-gateway-bus-backend
status: implemented
---
# Remove the gateway server, bus, and tmux backend packages

## Description
Delete the in-memory gateway/bus/backend infrastructure and the `forge gateway`
subcommand (Option A from issue #191). This is ~2,100 lines (gateway + tests +
bus + backend) of low-value code: session state is in-memory only, the bus
duplicates the agent Hub waiter pattern, daemon mode is broken (documented in
AGENTS.md gotchas), and the relay has reconnection logic for a stale-address bug
it can't fix. The CLI spawns local agents directly without any of it. Supersedes
the `gateway` spec. Future multi-session work should start from real requirements
(persistent storage, auth, rate limiting). The client-side `--gateway` flag is
out of scope and stays.

## Context
Delete entirely:
- `internal/server/gateway/` (`gateway.go`, `gateway_test.go`)
- `internal/server/bus/` (whole package)
- `internal/server/backend/` (`backend.go`, `tmux.go`, `worktree.go`, `worktree_test.go`)
- `cmd/forge/gateway.go`
- `cmd/forge/gateway_test.go`

Edit:
- `cmd/forge/main.go` — remove `gateway` + deprecated `server` switch cases,
  `runGateway` references, header/help text mentioning gateway
- `justfile` — remove `dev-gateway`, `dev-gateway-daemon`, `stop-gateway`,
  `tail-gateway`, `gateway-status`, and the `_pid_file`/`_log_file` vars if only
  used by those targets
- `AGENTS.md` — remove `forge gateway` Architecture bullet, Gateway Mode sections,
  bus/backend repo-layout entries, gateway gotchas, gateway env-var notes,
  gateway API-endpoints section
- `README.md` — remove Gateway Mode sections/diagrams
- `.forge/specs/gateway.md` — set status to `superseded`

Keep untouched (NOT in scope — client side, no import of removed packages):
- `cmd/forge/cli.go` `--gateway` flag, `createSession`, `listenEvents`,
  `gatewayURL` plumbing (talks to a remote gateway over plain HTTP)
- agent Hub waiter-list pattern in `internal/agent/`

## Behavior
- `go build ./...` and `go vet ./...` succeed with the packages and subcommand gone.
- `forge gateway` prints "Unknown command: gateway" and help, exits 1.
- `forge server` (deprecated alias) likewise unknown, exits 1.
- `forge help` no longer lists `gateway`.
- `forge` (interactive, local agent) and `forge agent` are unaffected.
- `go test ./...` passes; no dangling references to removed packages.
- `internal/server/` directory removed (or empty → removed).

## Constraints
- Do NOT remove the client-side `--gateway` flag or its HTTP helpers in cli.go.
- Do NOT touch the agent Hub waiter-list pattern — it is the canonical copy.
- Do NOT introduce build tags or stub-registration files (no Option B abstraction).
- Do NOT leave breadcrumb comments ("// moved", "// removed gateway here").
- Do NOT add a slim-down replacement (no Option C).
- Do NOT delete `.forge/specs/gateway.md`; mark it superseded.

## Interfaces
No new interfaces. Net deletion. Resulting `main.go` switch:
```go
switch cmd {
case "agent":
    os.Exit(runAgent(os.Args[1:]))
case "stats":
    os.Exit(runStats(os.Args[1:]))
case "mcp":
    os.Exit(runMCP(os.Args[1:]))
case "config":
    os.Exit(runConfig(os.Args[1:]))
case "help", "-h", "--help":
    printHelp()
    os.Exit(0)
default:
    // flag → interactive; else unknown
}
```

## Edge Cases
- Shared helper imported by non-gateway code: grep showed none outside
  `internal/server/`; if the compiler surfaces one, relocate it to its consumer
  rather than keeping a dead package.
- `internal/server/` becomes empty after deletion → remove the now-empty dir.
- justfile `_pid_file`/`_log_file` vars: remove only if no remaining target uses
  them; otherwise leave. (Implemented: both were used exclusively by gateway
  targets, so they were removed.)
- External scripts/CI invoking `forge gateway` will break — none found in
  `.github/`; documented as a deliberate breaking change in the issue.
- `forge server` deprecated alias is removed alongside `gateway` (it forwarded to
  the same removed code).

## Alternatives

### shelve-behind-build-tag
Gate every gateway/bus/backend file behind `//go:build gateway` and split the
subcommand registration into tagged/stub files so the default build excludes it,
adding a CI lane that builds with `-tags gateway` to prevent bitrot.

**Not selected because:** It contradicts the maintainer's stated Option A
recommendation, requires introducing a stub-registration abstraction the plain
`switch`-based dispatcher in `main.go` lacks (violates the "idiomatic, simple"
guidance in AGENTS.md), and adds a permanent CI lane to fight silent rot —
ongoing maintenance that defeats the issue's "reduce cognitive load" goal. The
in-memory hollowness persists, so reviving it still needs a rewrite; git history
preserves the reference implementation just as well.

### slim-down (Option C, issue only — no candidate)
Inline bus storage into the gateway, drop daemon mode and the relay reconnection
loop to reach ~300 lines.

**Not selected because:** No refined candidate proposed it, and it keeps the core
defect — in-memory-only session state — making the "session management" value
proposition still hollow. Maintaining 300 lines of an architecturally-doomed
design costs more than rebuilding from real persistence requirements later.
