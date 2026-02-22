# Forge

Forge is developer tooling for governed AI development. A Go CLI/server that wraps any coding agent with principles, review, real-environment testing, and project-level orchestration.

## Architecture

```
Frontends (CLI, Server, Webhooks)
        ↓
   Forge Engine (core orchestration)
        ↓
 ┌──────┼──────┬──────┬────────┐
Agent  Tracker Review  Env  Planner
 i/f    i/f   Engine   i/f
```

Everything is an interface. Agents, trackers, environments are all pluggable backends.

### Core Interfaces
- **Agent** (`internal/agent/`): LLM execution. Backends: `claude-code` (shells out to `claude`), `opencode`, `http` (generic)
- **Tracker** (`internal/tracker/`): Issue/PR management. Backends: `github`, `jira`, `linear`, `file`
- **Environment** (`internal/env/`): Deploy targets. Backends: `docker-compose`, `k3d`, `vcluster`
- **Engine** (`internal/engine/`): Orchestrates the governed build loop. Owns agents, trackers, principles, envs

### The Governed Build Loop
```
Issue → PLAN → [Human Approval] → CODE → REVIEW → (Critical? → loop back | Clean → PR)
```
Max 3 iterations. Review is the product — separate agent sessions enforce human-defined principles.

### Key Design Decisions
- **Go**: Single binary, easy cross-compilation, good concurrency, no runtime deps
- **Shell out to agents**: Agent CLIs handle auth, model selection, tools. Interface boundary is stdin/stdout
- **Tracker as interface**: Every org uses different tools. Abstraction is thin
- **Principles as structured YAML**: Not prose — machine-evaluable with IDs, categories, severity, rationale

## Project Structure

```
forge/
├── cmd/forge/           # CLI entrypoint (cobra)
│   ├── main.go
│   ├── build.go         # forge build
│   ├── review.go        # forge review
│   ├── plan.go          # forge plan
│   ├── serve.go         # forge serve
│   └── workstream.go    # forge build --workstream
├── internal/
│   ├── engine/          # Core Build/Review/Plan orchestration
│   ├── agent/           # Agent interface + backends
│   ├── tracker/         # Tracker interface + backends
│   ├── principles/      # PrincipleStore, schema, prompt assembly
│   ├── review/          # Review orchestration, findings, prompts
│   ├── env/             # Environment interface + backends
│   └── server/          # HTTP server, API, webhooks, jobs, SSE
├── pkg/config/          # .forge/config.yaml parsing
└── prompts/             # Skill prompts (plan.md, code.md, review-*.md)
```

## Development Conventions

### Go
- Go 1.22+ with standard project layout
- Use `internal/` for non-exported packages, `pkg/` only for config
- Interfaces in their own files (e.g., `agent.go` for interface, `claudecode.go` for implementation)
- Error handling: wrap with `fmt.Errorf("operation: %w", err)`, never swallow errors
- Context propagation: always pass `context.Context` as first parameter
- Concurrency: use `sync.WaitGroup` for parallel reviewers, respect `ctx` cancellation
- Testing: table-driven tests, mock interfaces, use `testify` only if needed
- Structured logging: use `slog` (stdlib)

### CLI
- Framework: cobra
- Config: viper for `.forge/config.yaml`
- Output: structured terminal output, `--format json` for machine-readable

### Naming
- Branch pattern: `forge/{{.Tracker}}-{{.IssueID}}`
- Workstream branch: `forge/ws-{{.WorkstreamID}}`
- Principle IDs: `{category}-{number}` (e.g., `sec-001`, `arch-003`)

### Issue References (URI scheme)
```
github://org/repo#123   # full
gh:org/repo#123         # short
#123                    # default (from config)
jira://PROJECT-456      # Jira
linear://TEAM-789       # Linear
./specs/feature.md      # local file
```

## Phases (see GitHub issues)

| Phase | Focus | Issues |
|-------|-------|--------|
| 1 | Orchestrator + Single Skill Loop | #1-#16, #36, #45, #48, #50 |
| 2 | Reviewers, Principles, CI | #17-#21, #46, #49 |
| 3 | Server + Webhooks | #22-#28 |
| 4 | Planner + Workstreams | #29-#35, #47, #51 |
| 5 | Kubernetes Isolation | #37-#41 |
| 6 | Production Hardening | #42-#43 |
| 7 | Custom Agents | #44 |

## Key Files

- `forge-architecture-v0.md`: Full architecture doc with interface definitions and code examples
- `forge-thesis.docx`: Thesis document — why governance is the gap, validation plan
- `forge-product-vision-v3.docx`: Product vision — agent team model, trust hierarchy, timeline

## Principles

Forge's own principles live in `.forge/principles/` (once #45 is done). The principle framework is thesis-critical — if principles don't improve agent output, the governance thesis fails.

## Dogfooding

Issues tagged `dogfood` should be built/validated using Forge itself once minimally viable:
- #45: Write Forge's own principle sets
- #46: Use `forge review` on Forge PRs
- #47: Use `forge build` to implement Forge features

## Testing Strategy

- Unit tests: table-driven, mock interfaces
- Integration tests: mock agent binaries (shell scripts that return known output)
- E2E tests: full governed loop against a test repo
- CI: `go test ./...` on every PR, `forge review` once available (#46)

## Build & Run

```bash
make build    # builds forge binary
make test     # runs all tests
make lint     # runs golangci-lint
forge init    # scaffold .forge/ directory
forge build --issue gh:org/repo#123    # governed build loop
forge review --diff HEAD~1..HEAD       # standalone review
forge plan --issue gh:org/repo#123     # generate plan only
forge serve --port 8080                # start server
```
