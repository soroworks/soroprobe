BINARY      := soroprobe
PKG         := github.com/soroworks/soroprobe
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
LDFLAGS     := -X main.version=$(VERSION)
GO          ?= go

# The Stellar SDK requires Go 1.25. GOTOOLCHAIN=auto lets an older local `go`
# fetch the right toolchain automatically rather than failing to build.
export GOTOOLCHAIN ?= auto

.PHONY: all
all: tidy fmt vet test build

.PHONY: build
build: ## Build the soroprobe binary into ./bin
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/soroprobe

.PHONY: install
install: ## Install soroprobe into GOBIN
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/soroprobe

.PHONY: test
test: ## Run the test suite (never touches the network)
	$(GO) test ./...

.PHONY: test-race
test-race: ## Run the test suite with the race detector
	$(GO) test -race ./...

.PHONY: cover
cover: ## Run tests and open a coverage report
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

.PHONY: fmt
fmt: ## Format all Go source
	$(GO) fmt ./...

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: tidy
tidy: ## Tidy go.mod and go.sum
	$(GO) mod tidy

.PHONY: fixtures
fixtures: ## Re-record test fixtures from the live testnet
	$(GO) run ./internal/stellar/stellartest/record

.PHONY: run
run: ## Run the HTTP API locally
	$(GO) run ./cmd/soroprobe serve

.PHONY: docker
docker: ## Build the Docker image
	docker build -t $(BINARY):$(VERSION) -t $(BINARY):latest .

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin coverage.out

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
