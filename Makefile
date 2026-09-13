# Atlas — developer entry points. `nix develop` provides the Go toolchain.

GO ?= go

.PHONY: build test vet fmt check run once nix

build: ## Build ./atlas
	CGO_ENABLED=0 $(GO) build -o atlas .

test: ## Run the unit tests
	CGO_ENABLED=0 $(GO) test ./...

vet: ## Static analysis
	CGO_ENABLED=0 $(GO) vet ./...

fmt: ## Format the tree (and report strays)
	$(GO) fmt ./...
	gofmt -l .

check: fmt vet test ## Full pre-commit gate

run: build ## Run interactively
	./atlas

once: build ## Render one frame without a TTY
	./atlas --once --width 128 --height 40

nix: ## Build through the flake (runs the test suite)
	nix build .#default
