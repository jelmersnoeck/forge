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

# Run all tests (unit + integration + e2e)
test:
  go test -race -timeout 5m ./...

# Run tests with verbose output
test-v:
  go test -v -race -timeout 5m ./...

# Run only unit tests (exclude integration and e2e)
test-unit:
  go test -race -timeout 5m $(go list ./... | grep -v '/test/')

# Run integration tests
test-integration: build
  go test -v -race -timeout 10m -tags=integration ./test/integration/...

# Run E2E tests
test-e2e: build
  go test -v -race -timeout 10m -tags=e2e ./test/e2e/...

# Run all test suites (unit + integration + e2e)
test-all: test-unit test-integration test-e2e

# Run tests with coverage
test-coverage:
  go test -race -coverprofile=coverage.out -covermode=atomic ./...
  go tool cover -html=coverage.out -o coverage.html
  @echo "Coverage report: coverage.html"

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
