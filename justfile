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

# Build agent binary (legacy - deprecated, use 'forge agent' instead)
build-agent:
  go build -o forge-agent ./cmd/agent

# Build everything (new + legacy)
build-all: build build-agent build-server build-cli

# Install unified forge binary to GOBIN (defaults to ~/go/bin)
install:
  go install ./cmd/forge

# ── Dev ──────────────────────────────────────────────────────

# Run interactive CLI (unified binary)
dev: build
  ./forge

# Build and run server (unified binary)
dev-server: build
  ./forge server

# Build and run server in daemon mode (unified binary)
dev-server-daemon: build
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

# ── Cleanup ──────────────────────────────────────────────────

# Remove build artifacts
clean:
  rm -f forge forge-server forge-cli forge-agent
