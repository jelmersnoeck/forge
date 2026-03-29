# forge — async coding agent

# Go 1.26.1 toolchain auto-download needs the sum database enabled
export GOSUMDB := "sum.golang.org"

# List available recipes
default:
  @just --list

# ── Build ────────────────────────────────────────────────────

# Build server binary
build-server:
  go build -o forge-server ./cmd/server

# Build CLI binary
build-cli:
  go build -o forge-cli ./cmd/cli

# Build agent binary
build-agent:
  go build -o forge-agent ./cmd/agent

# Build everything
build: build-server build-cli build-agent

# ── Dev ──────────────────────────────────────────────────────

# Build agent + server and run server
dev-server: build-agent build-server
  ./forge-server

# Build and run server in daemon mode
dev-server-daemon: build-agent build-server
  ./forge-server -daemon

# Stop daemon server
stop-server:
  @if [ -f /tmp/forge/sessions/forge.pid ]; then \
    kill $(cat /tmp/forge/sessions/forge.pid) && echo "Server stopped"; \
  else \
    echo "No PID file found at /tmp/forge/sessions/forge.pid"; \
  fi

# Tail server logs (daemon mode)
tail-server:
  tail -f /tmp/forge/sessions/forge.log

# Build and run CLI
dev-cli: build-cli
  ./forge-cli

# ── Test ──────────────────────────────────────────────────────

# Run all tests
test:
  go test ./...

# Run tests with verbose output
test-v:
  go test -v ./...

# ── Lint ──────────────────────────────────────────────────────

# Run go vet
vet:
  go vet ./...

# ── Docker ─────────────────────────────────────────────────

# Build and start via docker compose (reads .env)
up:
  docker compose up --build -d

# Stop compose services
down:
  docker compose down

# Tail server logs
logs:
  docker compose logs -f server

# ── Cleanup ──────────────────────────────────────────────────

# Remove build artifacts
clean:
  rm -f forge-server forge-cli forge-agent
