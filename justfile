# forge — async coding agent

# Go 1.26.1 toolchain auto-download needs the sum database enabled
export GOSUMDB := "sum.golang.org"

# List available recipes
default:
  @just --list

# ── Build ────────────────────────────────────────────────────

# Build unified forge binary
build:
  go build -o forge ./cmd/forge

# Build server binary (legacy)
build-server:
  go build -o forge-server ./cmd/server

# Build CLI binary (legacy)
build-cli:
  go build -o forge-cli ./cmd/cli

# Build agent binary (legacy - still needed by server backend)
build-agent:
  go build -o forge-agent ./cmd/agent

# Build everything (new + legacy)
build-all: build build-agent build-server build-cli

# ── Dev ──────────────────────────────────────────────────────

# Run interactive CLI (unified binary)
dev: build
  ./forge

# Build and run server (unified binary)
dev-server: build build-agent
  ./forge server

# Build and run server in daemon mode (unified binary)
dev-server-daemon: build build-agent
  ./forge server -daemon

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
dev-cli: build
  ./forge

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
  rm -f forge forge-server forge-cli forge-agent
