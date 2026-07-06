# Iceberg Sentry — release-quality build entry point.

BINARY      := iceberg-sentry
CMD         := ./cmd/iceberg-sentry
VERSION     ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w \
               -X main.version=$(VERSION) \
               -X main.commit=$(COMMIT) \
               -X main.buildDate=$(BUILD_DATE)

.PHONY: help build test race lint fmt vet cover bench install clean docker docker-dev release-snapshot fixtures site-serve

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "\033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the binary into ./iceberg-sentry
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o $(BINARY) $(CMD)

test: ## Run the test suite
	go test ./...

race: ## Run the test suite with -race
	go test -race ./...

lint: fmt vet ## Format check + vet + golangci-lint (if installed)
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=5m; \
	else \
		echo "golangci-lint not installed; skipping"; \
	fi

fmt: ## Verify gofmt (fails if diff)
	@out=$$(gofmt -l -d .); if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

vet: ## Run go vet
	go vet ./...

cover: ## Test with coverage
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

bench: ## Run Go benchmarks
	go test -run=^$$ -bench=. -benchmem -count=3 ./internal/scan ./internal/iceberg ./internal/bloom

install: build ## Install into $HOME/.local/bin
	install -d $(HOME)/.local/bin
	install -m 0755 $(BINARY) $(HOME)/.local/bin/$(BINARY)

clean: ## Remove build artefacts
	rm -f $(BINARY) coverage.out
	rm -rf dist/

docker: ## Build the release-style container (uses pre-built binary)
	$(MAKE) build
	docker build -t $(BINARY):$(VERSION) .

docker-dev: ## Build container from source with buildx
	docker build -f Dockerfile.dev \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(BINARY):$(VERSION) .

release-snapshot: ## Local release-style build via goreleaser (no publish)
	goreleaser release --snapshot --clean

fixtures: ## Generate pyiceberg test fixtures under ./warehouse
	pip install -q "pyiceberg[pyarrow,sql-sqlite]>=0.7.0" pyarrow
	python scripts/gen_fixtures.py --root ./warehouse --clean

site-serve: ## Serve the marketing + docs site locally on :8080
	python3 -m http.server -d site 8080
