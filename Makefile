SHELL := /bin/bash
.DEFAULT_GOAL := help

# ---------------------------------------------------------------------------
# Project metadata
# ---------------------------------------------------------------------------
BINARY_NAME    := haproxy-otel-spoe
MODULE         := github.com/fgouteroux/haproxy-otel-spoe
IMAGE_REPO     := ghcr.io/fgouteroux/haproxy-otel-spoe

# ---------------------------------------------------------------------------
# Version stamping — injected at link time
# ---------------------------------------------------------------------------
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(MODULE)/internal.Version=$(VERSION) \
	-X $(MODULE)/internal.Commit=$(COMMIT) \
	-X $(MODULE)/internal.BuildTime=$(BUILD_TIME)

# ---------------------------------------------------------------------------
# Build settings
# ---------------------------------------------------------------------------
GO          := go
GOFLAGS     := CGO_ENABLED=0
BUILD_FLAGS := -trimpath -ldflags "$(LDFLAGS)"
OUT_DIR     := dist

.PHONY: all
all: tidy fmt vet lint build ## Run tidy, fmt, vet, lint, then build

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------
.PHONY: build
build: ## Build the binary for the host OS/arch
	$(GOFLAGS) $(GO) build $(BUILD_FLAGS) -o $(BINARY_NAME) .

.PHONY: build-linux-amd64
build-linux-amd64: ## Cross-compile for linux/amd64
	mkdir -p $(OUT_DIR)
	GOOS=linux GOARCH=amd64 $(GOFLAGS) $(GO) build $(BUILD_FLAGS) \
		-o $(OUT_DIR)/$(BINARY_NAME)-linux-amd64 .

.PHONY: build-linux-arm64
build-linux-arm64: ## Cross-compile for linux/arm64
	mkdir -p $(OUT_DIR)
	GOOS=linux GOARCH=arm64 $(GOFLAGS) $(GO) build $(BUILD_FLAGS) \
		-o $(OUT_DIR)/$(BINARY_NAME)-linux-arm64 .

.PHONY: build-all
build-all: build-linux-amd64 build-linux-arm64 ## Build for all supported platforms

# ---------------------------------------------------------------------------
# Test
# ---------------------------------------------------------------------------
.PHONY: test
test: ## Run unit tests
	$(GO) test ./...

.PHONY: test-race
test-race: ## Run unit tests with race detector
	$(GO) test -race ./...

.PHONY: test-coverage
test-coverage: ## Run tests and produce coverage report
	$(GO) test -race -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# ---------------------------------------------------------------------------
# Code quality
# ---------------------------------------------------------------------------
.PHONY: fmt
fmt: ## Run gofmt and goimports across the codebase
	$(GO) fmt ./...

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: lint
lint: ## Run golangci-lint (must be installed)
	golangci-lint run ./...

.PHONY: lint-fix
lint-fix: ## Run golangci-lint with auto-fix
	golangci-lint run --fix ./...

# ---------------------------------------------------------------------------
# Security
# ---------------------------------------------------------------------------
.PHONY: sec
sec: gosec govulncheck ## Run all security scanners

.PHONY: gosec
gosec: ## Run gosec static analysis
	@command -v gosec >/dev/null 2>&1 || { echo "gosec not installed: go install github.com/securecodewarrior/gosec/v2/cmd/gosec@latest"; exit 1; }
	gosec -fmt=sarif -out=gosec.sarif ./... || gosec ./...

.PHONY: govulncheck
govulncheck: ## Run govulncheck for known CVEs
	@command -v govulncheck >/dev/null 2>&1 || { echo "govulncheck not installed: go install golang.org/x/vuln/cmd/govulncheck@latest"; exit 1; }
	govulncheck ./...

# ---------------------------------------------------------------------------
# Module hygiene
# ---------------------------------------------------------------------------
.PHONY: tidy
tidy: ## Run go mod tidy
	$(GO) mod tidy

.PHONY: verify
verify: ## Verify module checksums
	$(GO) mod verify

# ---------------------------------------------------------------------------
# Docker / Container
# ---------------------------------------------------------------------------
.PHONY: docker-build
docker-build: ## Build container image using Containerfile
	docker build -f Containerfile -t $(IMAGE_REPO):$(VERSION) .

.PHONY: docker-push
docker-push: ## Push container image to GHCR (requires docker login to ghcr.io)
	docker push $(IMAGE_REPO):$(VERSION)

# ---------------------------------------------------------------------------
# Release
# ---------------------------------------------------------------------------
.PHONY: release-snapshot
release-snapshot: ## Build a local snapshot release via GoReleaser (no publish)
	goreleaser release --snapshot --clean

.PHONY: release-dry-run
release-dry-run: ## Validate GoReleaser config without publishing
	goreleaser check

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------
.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BINARY_NAME) $(OUT_DIR) coverage.out coverage.html gosec.sarif

# ---------------------------------------------------------------------------
# Install
# ---------------------------------------------------------------------------
.PHONY: install
install: build ## Install binary to $GOPATH/bin
	$(GO) install .

# ---------------------------------------------------------------------------
# Help
# ---------------------------------------------------------------------------
.PHONY: help
help: ## Display this help message
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} \
		/^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
