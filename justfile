# forge — async coding agent

# List available recipes
default:
  @just --list

# ── Build ────────────────────────────────────────────────────

# Build all packages (types → tools → runtime → server → cli)
build: build-types build-tools build-runtime build-server build-cli

# Build shared types
build-types:
  npm run build -w @forge/types

# Build tools
build-tools: build-types
  npm run build -w @forge/tools

# Build runtime
build-runtime: build-types build-tools
  npm run build -w @forge/runtime

# Build server
build-server: build-types build-tools build-runtime
  npm run build -w @forge/server

# Build CLI
build-cli: build-types
  npm run build -w @forge/cli

# ── Dev ──────────────────────────────────────────────────────

# Start server in dev mode (builds types, tools, runtime first, watches for changes)
dev-server: build-types build-tools build-runtime
  npm run dev -w @forge/server

# Start CLI in dev mode (builds types first)
dev-cli: build-types
  npm run dev -w @forge/cli

# ── Typecheck ────────────────────────────────────────────────

# Typecheck all packages
check:
  npx tsc --build

# ── Cleanup ──────────────────────────────────────────────────

# Remove all build artifacts
clean:
  rm -rf packages/*/dist packages/*/*.tsbuildinfo
