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

# Install unified forge binary to GOBIN (defaults to ~/go/bin)
install:
  go install ./cmd/forge

# ── Dev ──────────────────────────────────────────────────────

# Run interactive CLI
dev: build
  ./forge

# Build and run server
dev-server: build
  ./forge server

# Build and run server in daemon mode
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

# ── MCP Server ──────────────────────────────────────────────

# Build MCP server
build-mcp:
  cd mcp-server && npm install && npm run build

# Run MCP server (STDIO)
mcp: build-mcp
  cd mcp-server && npm start

# Run MCP server (HTTP)
mcp-http: build-mcp
  cd mcp-server && npm run start:http

# ── Cleanup ──────────────────────────────────────────────────

# Remove build artifacts
clean:
  rm -f forge
  rm -rf mcp-server/dist mcp-server/node_modules
