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

# Build discord bridge binary
build-bridge:
  go build -o forge-discord-bridge ./cmd/forge-discord-bridge

# Build everything
build-all: build build-bridge

# Install unified forge binary to GOBIN (defaults to ~/go/bin)
install:
  go install ./cmd/forge

# ── Dev ──────────────────────────────────────────────────────

# Run interactive CLI (unified binary)
dev: build
  ./forge

# Build and run gateway (unified binary)
dev-gateway: build
  ./forge gateway

# PID/log file resolution: FORGE_RUN_DIR > SESSIONS_DIR > /tmp/forge/sessions
_pid_file := env("FORGE_RUN_DIR", env("SESSIONS_DIR", "/tmp/forge/sessions")) / "forge.pid"
_log_file := env("FORGE_RUN_DIR", env("SESSIONS_DIR", "/tmp/forge/sessions")) / "forge.log"

# Build and run gateway in daemon mode (unified binary)
dev-gateway-daemon: build
  ./forge gateway -daemon

# Stop daemon gateway
stop-gateway:
  @if [ -f "{{ _pid_file }}" ]; then \
    kill $(cat "{{ _pid_file }}") && echo "Gateway stopped"; \
  else \
    echo "No PID file found at {{ _pid_file }}"; \
  fi

# Tail gateway logs (daemon mode)
tail-gateway:
  tail -f "{{ _log_file }}"

# Show gateway daemon status
gateway-status:
  @echo "PID file: {{ _pid_file }}"
  @if [ -f "{{ _pid_file }}" ]; then \
    PID=$(cat "{{ _pid_file }}"); \
    if kill -0 "$PID" 2>/dev/null; then \
      echo "Status:   running (pid $PID)"; \
    else \
      echo "Status:   dead (stale pid $PID)"; \
    fi; \
  else \
    echo "Status:   not running"; \
  fi
  @echo "Log file: {{ _log_file }}"

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
  rm -f forge forge-discord-bridge
