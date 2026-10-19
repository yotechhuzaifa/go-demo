# Makefile for common development tasks.
#
# Run `make` or `make help` to see the available targets. Targets use standard
# Go tooling plus Docker for the image target, so there is no hidden build
# system to learn.

APP_NAME := taskflow
CMD_PATH := ./cmd/api
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
ENV_FILE ?= .env
DOCKER_IMAGE ?= taskflow:$(VERSION)
SWAG_VERSION := v1.16.6

# .DEFAULT_GOAL makes plain `make` behave like `make help`.
.DEFAULT_GOAL := help
.PHONY: help run build test test-race cover fmt vet check tidy clean smoke docs docs-clean docker-build

help: ## Show this help text.
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-24s %s\n", $$1, $$2}'

run: ## Run the API locally, loading .env when present (auto-migrates and seeds).
	@set -a; \
	if [ -f "$(ENV_FILE)" ]; then . "$(ENV_FILE)"; fi; \
	set +a; \
	go run $(CMD_PATH)

demo-jsonplaceholder: ## Call JSONPlaceholder (GET, POST, PUT, DELETE).
	go run ./cmd/jsonplaceholder

build: ## Compile a stripped binary into bin/.
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(BIN_DIR)/$(APP_NAME) $(CMD_PATH)

test: ## Run all tests.
	go test ./...

test-race: ## Run tests with the race detector.
	go test -race ./...

cover: ## Run tests and print coverage per function.
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

fmt: ## Format every Go file.
	gofmt -w .

vet: ## Run Go's built-in static analysis.
	go vet ./...

check: fmt vet test ## Format, vet, and test (use before committing).

tidy: ## Keep go.mod/go.sum consistent with imports.
	go mod tidy

smoke: ## Exercise the running API with curl (server must already be running).
	./scripts/smoke.sh

docs: ## Generate Swagger docs from handler annotations into docs/swagger/.
	GOTOOLCHAIN=local go run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION) init \
		-g cmd/api/main.go \
		-o docs/swagger \
		--parseDependency \
		--parseInternal

docs-clean: ## Remove generated Swagger files (the placeholder is recreated by hand).
	rm -f docs/swagger/docs.go docs/swagger/swagger.json docs/swagger/swagger.yaml

docker-build: ## Build the container image.
	docker build --build-arg VERSION=$(VERSION) -t $(DOCKER_IMAGE) .

clean: ## Remove build and coverage artifacts.
	rm -rf $(BIN_DIR) coverage.out coverage.html

encrypt-env:
	gpg -c --cipher-algo AES256 .env
	
decrypt-env:
	gpg -o .env -d .env.gpg