# Bumshi — developer task runner.
# Run `make help` for the list of targets.

SERVER_DIR := server
BINARY     := bumshid
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X github.com/bumshi/bumshi/server/internal/version.Version=$(VERSION) \
	-X github.com/bumshi/bumshi/server/internal/version.Commit=$(COMMIT) \
	-X github.com/bumshi/bumshi/server/internal/version.BuildDate=$(BUILD_DATE)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the server binary into ./bin
	cd $(SERVER_DIR) && go build -trimpath -ldflags "$(LDFLAGS)" -o ../bin/$(BINARY) ./cmd/bumshid

.PHONY: run
run: ## Run the server in development mode
	cd $(SERVER_DIR) && BUMSHI_ENV=development BUMSHI_ACCESS_LOG=true go run ./cmd/bumshid

.PHONY: test
test: ## Run tests with the race detector and coverage
	cd $(SERVER_DIR) && go test -race -covermode=atomic -coverprofile=coverage.out ./...

.PHONY: vet
vet: ## Run go vet
	cd $(SERVER_DIR) && go vet ./...

.PHONY: fmt
fmt: ## Format the code
	cd $(SERVER_DIR) && gofmt -s -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file is not gofmt-clean
	@out=$$(cd $(SERVER_DIR) && gofmt -s -l .); \
	if [ -n "$$out" ]; then echo "not formatted:"; echo "$$out"; exit 1; fi

.PHONY: lint
lint: ## Run golangci-lint (must be installed)
	cd $(SERVER_DIR) && golangci-lint run

.PHONY: tidy
tidy: ## Tidy the module graph
	cd $(SERVER_DIR) && go mod tidy

.PHONY: check
check: fmt-check vet test ## Run all fast checks

.PHONY: docker
docker: ## Build the server Docker image
	docker build -f $(SERVER_DIR)/Dockerfile -t bumshi/$(BINARY):$(VERSION) \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		$(SERVER_DIR)

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin dist $(SERVER_DIR)/coverage.out
