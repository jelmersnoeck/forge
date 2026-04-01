# Forge Test Suite

Comprehensive test coverage for the Forge async coding agent.

## Test Structure

```
test/
├── integration/      # Integration tests (require built binaries)
│   ├── agent_test.go     # Agent HTTP server tests
│   └── gateway_test.go   # Gateway + backend tests
└── e2e/             # End-to-end tests (full workflows)
    └── cli_test.go       # CLI, server, session persistence tests
```

Unit tests are co-located with source code in `internal/` packages.

## Test Categories

### 1. Unit Tests
**Location:** `internal/**/*_test.go`  
**Tags:** None  
**Run:** `just test-unit` or `make test-unit`

Tests individual components in isolation:
- Tools (Read, Write, Edit, Bash, Grep, Glob, WebSearch, Reflect)
- Runtime components (context loader, prompt assembly, cost tracking, etc.)
- Server components (bus, backend interfaces)
- TUI model state transitions

**Requirements:**
- None (pure Go tests)
- Tools like `Grep` require `ripgrep` on PATH

### 2. Integration Tests
**Location:** `test/integration/`  
**Tags:** `integration`  
**Run:** `just test-integration` or `make test-integration`

Tests interactions between components:
- **Agent HTTP Server:** Tests agent's `/health`, `/messages`, `/events`, `/interrupt` endpoints with a mock LLM provider
- **Gateway Server:** Tests session management, message forwarding, SSE streaming
- **Backend (Tmux):** Tests tmux session management and git worktree isolation

**Requirements:**
- Built `forge` binary (automatically built by test commands)
- `tmux` on PATH (for backend tests)
- `git` on PATH (for worktree tests)

### 3. E2E Tests
**Location:** `test/e2e/`  
**Tags:** `e2e`  
**Run:** `just test-e2e` or `make test-e2e`

Tests complete user workflows:
- **CLI Direct Mode:** CLI spawns agent subprocess
- **CLI Server Mode:** CLI connects to gateway server
- **Session Persistence:** Sessions saved as JSONL and resumable
- **Stats Command:** Cost analytics work correctly
- **Agent Health Check:** Agent subprocess reports healthy

**Requirements:**
- Built `forge` binary
- `tmux` on PATH
- `curl` on PATH (for API calls)
- Dummy `ANTHROPIC_API_KEY` (tests don't call real API)

## Running Tests

### Quick Commands

```bash
# All tests (unit + integration + e2e)
just test-all
make test-all

# Individual suites
just test-unit          # Fast, no dependencies
just test-integration   # Requires tmux, git
just test-e2e           # Requires built binary, tmux

# With coverage
just test-coverage
make test-coverage
```

### CI/CD

GitHub Actions workflow (`.github/workflows/test.yml`) runs:
1. **Unit tests** with race detector
2. **Integration tests** on Ubuntu (ripgrep, tmux installed)
3. **E2E tests** with dummy API key
4. **Linting** via golangci-lint
5. **Auto-merge** for Dependabot PRs if all checks pass

### Local Development

```bash
# Run specific test
go test -v ./internal/tools -run TestBashTool

# Run integration tests only
go test -v -tags=integration ./test/integration/...

# Run with race detector
go test -race ./...

# Run with timeout (prevent hangs)
go test -timeout 5m ./...

# Debug hanging test
go test -v -timeout 30s ./internal/tools -run TestBashToolInteractiveCommands
```

## Test Patterns

### Table-Driven Tests
All tests use table-driven style with `map[string]struct`:

```go
tests := map[string]struct {
    input    string
    wantErr  bool
}{
    "valid input": {
        input:   "test",
        wantErr: false,
    },
    "invalid input": {
        input:   "",
        wantErr: true,
    },
}

for name, tc := range tests {
    t.Run(name, func(t *testing.T) {
        r := require.New(t)
        // test logic using tc
    })
}
```

### Test Data
Use Community TV show references:
- **Users:** Troy Barnes, Abed Nadir, Jeff Winger, Britta Perry
- **Locations:** Greendale Community College
- **IDs:** `greendale-101`, `study-group-7`, `paintball-tournament`

### Assertions
Use `testify/require` with short variable name:

```go
r := require.New(t)
r.NoError(err)
r.Equal(expected, actual)
```

### Cleanup
- Use `t.TempDir()` for file operations (auto-cleanup)
- Use `defer cleanup()` for processes, servers
- Tests should never leave artifacts

## Known Issues

### Timeouts
Some tests may timeout in CI if resources are constrained. Default timeout is 5m for unit tests, 10m for integration/e2e.

### Interactive Commands
The `bash_test.go` previously had a hanging test due to testing `vim` in a pipe. Fixed by using `echo | grep` instead.

### CI Environment
- **Anthropic API:** Tests use dummy key `sk-ant-test-dummy-key-for-ci`
- **Tmux:** Tests create ephemeral sessions with unique names to avoid conflicts
- **Git:** Worktree tests create temporary repos in `t.TempDir()`

## Adding New Tests

### Unit Test
1. Create `*_test.go` next to source file
2. Use table-driven pattern
3. Test error cases and edge cases
4. Use `t.TempDir()` for filesystem tests

### Integration Test
1. Add to `test/integration/`
2. Add `//go:build integration` tag
3. Check for required tools (tmux, git) and skip if missing
4. Build binaries if needed
5. Clean up resources in defers

### E2E Test
1. Add to `test/e2e/`
2. Add `//go:build e2e` tag
3. Build `forge` binary using `buildForgeBinary(t)` helper
4. Use subprocess execution with timeouts
5. Verify output/behavior end-to-end

## Coverage Goals

- **Unit tests:** 80%+ coverage for critical paths
- **Integration tests:** Cover all HTTP endpoints and backend operations
- **E2E tests:** Cover all user-facing workflows

Run `just test-coverage` to generate HTML coverage report.
