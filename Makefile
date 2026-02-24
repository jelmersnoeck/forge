.PHONY: build test lint clean fmt vet check install

BINARY    := forge
BUILD_DIR := bin
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS   := -ldflags "-X main.version=$(VERSION)"

build:
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/forge/

install:
	go install $(LDFLAGS) ./cmd/forge/

test:
	go test ./...

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint:
	golangci-lint run ./...

fmt:
	gofmt -l -w .

vet:
	go vet ./...

check: fmt vet lint test

clean:
	rm -rf $(BUILD_DIR) coverage.out

.DEFAULT_GOAL := build
