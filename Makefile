# ============================================================
# Rate Limiter Gateway - Development Makefile
# ============================================================

SHELL := /bin/bash
GO ?= go
BINARY_NAME := api-gateway
CMD_DIR := ./cmd/api-gateway

# Version injection (if using git tags)
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
LDFLAGS := -X main.version=$(VERSION)

# Color output helpers
YELLOW := \033[1;33m
GREEN := \033[1;32m
RED := \033[1;31m
RESET := \033[0m

.PHONY: all run test lint lint-fix build clean help

## Default: show help
all: help

## Run the application with hot-reload (requires air installed)
run: ## Run the API gateway
	@echo "$(YELLOW)Starting API Gateway...$(RESET)"
	@go run $(CMD_DIR)/main.go

## Run tests
test: ## Run all tests with race detector
	@echo "$(YELLOW)Running tests...$(RESET)"
	@$(GO) test -race -count=1 -v ./...
	@echo "$(GREEN)All tests passed ✓$(RESET)"

## Run lint checks
lint: ## Run linters
	@echo "$(YELLOW)Running linter...$(RESET)"
	@test -x $$(which golangci-lint) || (echo "$(RED)golangci-lint not found. Install: curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin$(RESET)"; exit 1)
	@golangci-lint run ./...
	@echo "$(GREEN)Lint passed ✓$(RESET)"

## Run lint with auto-fix
lint-fix: ## Auto-fix lint issues
	@echo "$(YELLOW)Auto-fixing lint issues...$(RESET)"
	@golangci-lint run --fix ./...

## Build the binary
build: ## Build the API gateway binary
	@echo "$(YELLOW)Building $(BINARY_NAME)...$(RESET)"
	@CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY_NAME) $(CMD_DIR)/main.go
	@echo "$(GREEN)Build complete: bin/$(BINARY_NAME)$(RESET)"

## Clean build artifacts
clean: ## Remove build artifacts
	@echo "$(YELLOW)Cleaning...$(RESET)"
	@rm -rf bin/
	@rm -f coverage.txt coverage.html
	@echo "$(GREEN)Cleaned ✓$(RESET)"

## Check for security vulnerabilities
sec: ## Run security scan (govulncheck)
	@echo "$(YELLOW)Running security scan...$(RESET)"
	@test -x $$(which govulncheck) || (echo "$(RED)govulncheck not found. Install: go install golang.org/x/vuln/cmd/govulncheck@latest$(RESET)"; exit 1)
	@govulncheck ./...
	@echo "$(GREEN)Security scan complete ✓$(RESET)"

## Format code
fmt: ## Format Go source files
	@gofmt -s -w .

## Install dev dependencies
deps: ## Install development dependencies
	@echo "$(YELLOW)Installing development dependencies...$(RESET)"
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@go mod tidy
	@echo "$(GREEN)Dependencies installed ✓$(RESET)"

## Show help
help: ## Display available commands
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "$(GREEN)%-15s$(RESET) %s\n", $$1, $$2}'