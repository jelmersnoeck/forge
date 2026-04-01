# Forge Makefile
# Provides make-based build and test commands as an alternative to justfile

.PHONY: help
help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ── Build ──

.PHONY: build
build: ## Build unified forge binary
	go build -o forge ./cmd/forge

.PHONY: build-all
build-all: build ## Build all binaries (unified + legacy)
	go build -o forge-agent ./cmd/agent
	go build -o forge-server ./cmd/server
	go build -o forge-cli ./cmd/cli

.PHONY: install
install: ## Install forge binary to GOBIN
	go install ./cmd/forge

.PHONY: clean
clean: ## Remove build artifacts
	rm -f forge forge-agent forge-server forge-cli coverage.out coverage.html

# ── Test ──

.PHONY: test
test: ## Run all tests
	go test -race -timeout 5m ./...

.PHONY: test-v
test-v: ## Run tests with verbose output
	go test -v -race -timeout 5m ./...

.PHONY: test-unit
test-unit: ## Run only unit tests (exclude integration and e2e)
	go test -race -timeout 5m $$(go list ./... | grep -v '/test/')

.PHONY: test-integration
test-integration: build ## Run integration tests
	go test -v -race -timeout 10m -tags=integration ./test/integration/...

.PHONY: test-e2e
test-e2e: build ## Run E2E tests
	go test -v -race -timeout 10m -tags=e2e ./test/e2e/...

.PHONY: test-all
test-all: test-unit test-integration test-e2e ## Run all test suites

.PHONY: test-coverage
test-coverage: ## Run tests with coverage
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# ── Lint ──

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format code with goimports
	goimports -w .

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run ./...

.PHONY: lint-fix
lint-fix: ## Run golangci-lint with --fix
	golangci-lint run --fix ./...

# ── Dev ──

.PHONY: dev
dev: build ## Run interactive CLI
	./forge

.PHONY: dev-server
dev-server: build ## Run server (foreground)
	./forge server

.PHONY: dev-server-daemon
dev-server-daemon: build ## Run server daemon
	./forge server -daemon

.PHONY: stop-server
stop-server: ## Stop daemon server
	@if [ -f /tmp/forge/sessions/forge.pid ]; then \
		kill $$(cat /tmp/forge/sessions/forge.pid) && echo "Server stopped"; \
	else \
		echo "No PID file found"; \
	fi

# ── CI ──

.PHONY: ci
ci: vet test-unit lint ## Run CI checks (vet + unit tests + lint)

.PHONY: ci-full
ci-full: vet test-all lint ## Run full CI checks (vet + all tests + lint)
