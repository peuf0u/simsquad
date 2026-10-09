.PHONY: build test lint fmt tidy install clean help

BINARY := simsquad
PKG    := github.com/peuf0u/simsquad
GOBIN  ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

# Version metadata — overridden by goreleaser at release time.
VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X 'main.version=$(VERSION)' \
	-X 'main.commit=$(COMMIT)' \
	-X 'main.date=$(DATE)'

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the simsquad binary into ./bin/
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/simsquad

install: ## Install simsquad into $$GOBIN
	go install -ldflags "$(LDFLAGS)" ./cmd/simsquad

test: ## Run unit tests
	go test ./...

lint: ## Run golangci-lint (install via: brew install golangci-lint)
	golangci-lint run ./...

fmt: ## Format Go sources
	gofmt -w .
	goimports -w -local $(PKG) .

tidy: ## Tidy go.mod
	go mod tidy

clean: ## Remove build artifacts
	rm -rf bin dist
