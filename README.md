# Forge

Governed AI development tooling. A Go CLI and server that wraps any coding agent with principles, automated review, real-environment testing, and project-level orchestration.

## Why Forge

AI coding agents produce code fast, but without governance that speed creates risk. Forge adds structure:

- **Principles** — machine-evaluable rules (security, architecture, simplicity) that agents must follow, defined as structured YAML
- **Automated review** — a separate agent session evaluates code against your principles, producing actionable findings with severity levels
- **The governed build loop** — Plan → Code → Review → iterate (max 3 rounds), then PR. Critical findings block merging
- **Pluggable everything** — agents, issue trackers, and deploy environments are all interfaces with swappable backends

## The Governed Build Loop

```
Issue → PLAN → [Human Approval] → CODE → REVIEW → Clean? → PR
                                            ↑         │
                                            └─── No ───┘  (max 3 iterations)
```

Forge takes an issue, generates a plan for human approval, runs a coding agent, then runs a *separate* review agent that evaluates the output against your principles. Critical findings loop back for fixes. Clean reviews produce a PR.

## Quick Start

```bash
# Build
make build

# Initialize a project
forge init

# Run a governed build against an issue
forge build --issue gh:org/repo#123

# Review a diff against principles
forge review --diff HEAD~1..HEAD

# Generate a plan without coding
forge plan --issue gh:org/repo#123

# Start the HTTP server
forge serve --port 8080
```

## Installation

```bash
# From source
git clone https://github.com/jelmersnoeck/forge.git
cd forge
make install
```

Requires Go 1.22+.

## Configuration

Run `forge init` to scaffold a `.forge/` directory in your project:

```
.forge/
├── config.yaml         # Agent, tracker, environment settings
└── principles/         # Your principle sets (YAML)
    ├── security.yaml
    ├── architecture.yaml
    └── simplicity.yaml
```

### Principles

Principles are structured YAML — not prose. Each principle has an ID, category, severity, description, check criteria, and examples:

```yaml
- id: sec-001
  category: security
  severity: critical
  title: No hardcoded secrets or credentials
  check: >
    Look for string literals that appear to be API keys,
    passwords, tokens, or connection strings...
```

Severity levels: `critical` (blocks merge), `warning` (requires attention), `info` (advisory).

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

### Backends

| Interface | Backends | Description |
|-----------|----------|-------------|
| **Agent** | `claude-code`, `opencode`, `http` | LLM execution — shells out to agent CLIs |
| **Tracker** | `github`, `jira`, `linear`, `file` | Issue and PR management |
| **Environment** | `docker-compose`, `k3d`, `vcluster` | Deploy targets for testing |

### Key Design Decisions

- **Go** — single binary, no runtime dependencies, good concurrency
- **Shell out to agents** — agent CLIs handle auth, model selection, and tools; Forge's interface boundary is stdin/stdout
- **Principles as structured YAML** — machine-evaluable with IDs, categories, severity, and rationale; not unstructured prose

## CI Integration

Forge ships a GitHub Action for principle-based review on every PR:

```yaml
# .github/workflows/forge-review.yml
- name: Run Forge Review
  run: |
    ./forge review \
      --diff HEAD~1..HEAD \
      --format sarif \
      --severity-threshold warning
```

Results are uploaded as SARIF to GitHub Code Scanning. Critical findings fail the check.

## Issue References

Forge uses a URI scheme for cross-tracker issue references:

```
gh:org/repo#123         # GitHub (short)
github://org/repo#123   # GitHub (full)
#123                    # Default tracker from config
jira://PROJECT-456      # Jira
linear://TEAM-789       # Linear
./specs/feature.md      # Local file spec
```

## Development

```bash
make build    # Build binary to bin/forge
make test     # Run all tests
make lint     # Run golangci-lint
make fmt      # Format code
make vet      # Run go vet
make check    # fmt + vet + lint + test
make clean    # Remove build artifacts
```

See [CLAUDE.md](CLAUDE.md) for detailed development conventions.

## License

See [LICENSE](LICENSE) for details.
