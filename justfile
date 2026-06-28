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

# Build everything
build-all: build

# Install unified forge binary to GOBIN (defaults to ~/go/bin)
install:
  go install ./cmd/forge

# ── Dev ──────────────────────────────────────────────────────

# Run interactive CLI (unified binary)
dev: build
  ./forge

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
  rm -f forge
