# =============================================================================
# High-Throughput API Gateway Makefile
# =============================================================================

# Shell configuration
SHELL := /bin/bash
.SHELLFLAGS := -ec

# Project variables
PROJECT_NAME := api-gateway
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
GO_VERSION := 1.22
BINARY_NAME := gateway

# Go build variables
BUILD_DIR := build
DIST_DIR := dist
COVERAGE_DIR := coverage

# Docker variables
DOCKER_COMPOSE := docker-compose.yml
DOCKER_COMPOSE_PROD := docker-compose.prod.yml

# Redis variables
REDIS_CONTAINER := redis-gateway

# Load test variables
K6_SCRIPT := loadtests/k6_script.js
K6_RESULTS_DIR := loadtests/results

# Colors for output
GREEN := \033[0;32m
RED := \033[0;31m
YELLOW := \033[1;33m
BLUE := \033[0;34m
NC := \033[0m # No Color

# =============================================================================
# Default target
# =============================================================================
.PHONY: help
help: ## Show this help message
	@echo "$(BLUE)Available targets:$(NC)"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  $(GREEN)%-20s$(NC) %s\n", $$1, $$2}'

# =============================================================================
# Build targets
# =============================================================================
.PHONY: build
build: ## Build the application binary
	@echo "$(BLUE)Building $(PROJECT_NAME)...$(NC)"
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/api-gateway
	@echo "$(GREEN)✓ Build complete: $(BUILD_DIR)/$(BINARY_NAME)$(NC)"

.PHONY: build-local
build-local: ## Build for local development
	@echo "$(BLUE)Building for local development...$(NC)"
	@mkdir -p $(BUILD_DIR)
	@go build -ldflags="-X main.version=$(VERSION)" -o $(BUILD_DIR)/$(BINARY_NAME)-local ./cmd/api-gateway
	@echo "$(GREEN)✓ Local build complete$(NC)"

.PHONY: build-race
build-race: ## Build with race detector enabled
	@echo "$(BLUE)Building with race detector...$(NC)"
	@mkdir -p $(BUILD_DIR)
	@go build -race -ldflags="-X main.version=$(VERSION)" -o $(BUILD_DIR)/$(BINARY_NAME)-race ./cmd/api-gateway
	@echo "$(GREEN)✓ Race detector build complete$(NC)"

.PHONY: build-all
build-all: clean build build-race ## Build all variants
	@echo "$(GREEN)✓ All builds complete$(NC)"

# =============================================================================
# Test targets
# =============================================================================
.PHONY: test
test: ## Run all tests
	@echo "$(BLUE)Running tests...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	@go test -v -race -count=1 ./... 2>&1 | tee $(COVERAGE_DIR)/test-output.txt
	@echo "$(GREEN)✓ Tests complete$(NC)"

.PHONY: test-short
test-short: ## Run short tests (skip integration)
	@echo "$(BLUE)Running short tests...$(NC)"
	@go test -short -count=1 ./...
	@echo "$(GREEN)✓ Short tests complete$(NC)"

.PHONY: test-coverage
test-coverage: ## Run tests and generate coverage report
	@echo "$(BLUE)Running tests with coverage...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	@go test -race -coverprofile=$(COVERAGE_DIR)/coverage.out -covermode=atomic ./...
	@go tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html
	@go tool cover -func=$(COVERAGE_DIR)/coverage.out | grep total
	@echo "$(GREEN)✓ Coverage report generated: $(COVERAGE_DIR)/coverage.html$(NC)"

.PHONY: test-race
test-race: ## Run tests with race detector
	@echo "$(BLUE)Running tests with race detector...$(NC)"
	@go test -race -count=1 ./...
	@echo "$(GREEN)✓ Race tests complete$(NC)"

.PHONY: test-integration
test-integration: ## Run integration tests (requires Docker)
	@echo "$(BLUE)Running integration tests...$(NC)"
	@./scripts/run-integration-tests.sh
	@echo "$(GREEN)✓ Integration tests complete$(NC)"

# =============================================================================
# Benchmark targets
# =============================================================================
.PHONY: benchmark
benchmark: ## Run all benchmarks
	@echo "$(BLUE)Running benchmarks...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	@go test -bench=. -benchmem -benchtime=5s -run=^$ ./cmd/api-gateway/ 2>&1 | tee $(COVERAGE_DIR)/benchmark-results.txt
	@echo "$(GREEN)✓ Benchmarks complete: $(COVERAGE_DIR)/benchmark-results.txt$(NC)"

.PHONY: benchmark-p50
benchmark-p50: ## Run latency percentile benchmarks
	@echo "$(BLUE)Running latency percentile benchmarks...$(NC)"
	@go test -bench=LatencyPercentiles -benchmem -benchtime=10s -run=^$ ./cmd/api-gateway/
	@echo "$(GREEN)✓ Latency benchmarks complete$(NC)"

.PHONY: benchmark-throughput
benchmark-throughput: ## Run throughput benchmarks
	@echo "$(BLUE)Running throughput benchmarks...$(NC)"
	@go test -bench=Throughput -benchmem -benchtime=10s -run=^$ ./cmd/api-gateway/
	@echo "$(GREEN)✓ Throughput benchmarks complete$(NC)"

.PHONY: benchmark-comparison
benchmark-comparison: ## Run benchmark with different configurations
	@echo "$(BLUE)Running comparison benchmarks...$(NC)"
	@go test -bench=Table -benchmem -benchtime=5s -run=^$ ./cmd/api-gateway/
	@echo "$(GREEN)✓ Comparison benchmarks complete$(NC)"

.PHONY: bench
bench: benchmark ## Alias for benchmark

# =============================================================================
# Load testing targets (K6)
# =============================================================================
.PHONY: loadtest
loadtest: ## Run K6 load tests
	@echo "$(BLUE)Running K6 load tests...$(NC)"
	@mkdir -p $(K6_RESULTS_DIR)
	@k6 run --summary-export=$(K6_RESULTS_DIR)/summary.json $(K6_SCRIPT)
	@echo "$(GREEN)✓ Load tests complete: $(K6_RESULTS_DIR)/summary.json$(NC)"

.PHONY: loadtest-full
loadtest-full: ## Run full K6 load test suite
	@echo "$(BLUE)Running full K6 load test suite...$(NC)"
	@mkdir -p $(K6_RESULTS_DIR)
	@k6 run --out json=$(K6_RESULTS_DIR)/full-results.json \
		--summary-export=$(K6_RESULTS_DIR)/full-summary.json \
		--vus=10000 \
		--duration=5m \
		$(K6_SCRIPT)
	@echo "$(GREEN)✓ Full load tests complete$(NC)"

.PHONY: loadtest-spike
loadtest-spike: ## Run spike load test
	@echo "$(BLUE)Running spike load test...$(NC)"
	@mkdir -p $(K6_RESULTS_DIR)
	@k6 run -e DURATION=1m -e VUS=20000 \
		--summary-export=$(K6_RESULTS_DIR)/spike-summary.json \
		$(K6_SCRIPT)
	@echo "$(GREEN)✓ Spike load tests complete$(NC)"

.PHONY: loadtest-memory
loadtest-memory: ## Run memory benchmark test
	@echo "$(BLUE)Running memory benchmark test...$(NC)"
	@mkdir -p $(K6_RESULTS_DIR)
	@k6 run -e API_BASE_URL=http://localhost:8080 -e VUS=5000 \
		--summary-export=$(K6_RESULTS_DIR)/memory-summary.json \
		$(K6_SCRIPT)
	@echo "$(GREEN)✓ Memory load tests complete$(NC)"

# =============================================================================
# Docker targets
# =============================================================================
.PHONY: docker-build
docker-build: ## Build Docker images
	@echo "$(BLUE)Building Docker images...$(NC)"
	@docker compose -f $(DOCKER_COMPOSE) build
	@echo "$(GREEN)✓ Docker build complete$(NC)"

.PHONY: docker-up
docker-up: ## Start all services
	@echo "$(BLUE)Starting services...$(NC)"
	@docker compose -f $(DOCKER_COMPOSE) up -d
	@echo "$(GREEN)✓ Services started$(NC)"

.PHONY: docker-down
docker-down: ## Stop all services
	@echo "$(BLUE)Stopping services...$(NC)"
	@docker compose -f $(DOCKER_COMPOSE) down
	@echo "$(GREEN)✓ Services stopped$(NC)"

.PHONY: docker-restart
docker-restart: docker-down docker-up ## Restart all services

.PHONY: docker-logs
docker-logs: ## View service logs
	@docker compose -f $(DOCKER_COMPOSE) logs -f

.PHONY: docker-redis
docker-redis: ## Start Redis container
	@echo "$(BLUE)Starting Redis container...$(NC)"
	@docker run -d --name $(REDIS_CONTAINER) \
		-p 6379:6379 \
		-e REDIS_PASSWORD=redispass \
		-v redis_data:/data \
		redis:7.2-alpine \
		redis-server --requirepass redispass --appendonly yes
	@echo "$(GREEN)✓ Redis started: $(REDIS_CONTAINER)$(NC)"

# =============================================================================
# Quality targets
# =============================================================================
.PHONY: lint
lint: ## Run linters
	@echo "$(BLUE)Running linters...$(NC)"
	@go vet ./...
	@echo "$(GREEN)✓ Lint complete$(NC)"

.PHONY: fmt
fmt: ## Format code
	@echo "$(BLUE)Formatting code...$(NC)"
	@gofmt -w .
	@echo "$(GREEN)✓ Formatting complete$(NC)"

.PHONY: check
check: fmt lint test ## Run all quality checks

# =============================================================================
# Cleaning targets
# =============================================================================
.PHONY: clean
clean: ## Clean build artifacts
	@echo "$(BLUE)Cleaning...$(NC)"
	@rm -rf $(BUILD_DIR) $(COVERAGE_DIR) $(K6_RESULTS_DIR)
	@go clean -cache -testcache
	@echo "$(GREEN)✓ Clean complete$(NC)"

.PHONY: clean-all
clean-all: clean ## Clean everything including Docker volumes
	@echo "$(BLUE)Cleaning everything...$(NC)"
	@docker compose -f $(DOCKER_COMPOSE) down -v 2>/dev/null || true
	@echo "$(GREEN)✓ Full clean complete$(NC)"

# =============================================================================
# Utility targets
# =============================================================================
.PHONY: run
run: ## Run the application locally
	@echo "$(BLUE)Running application...$(NC)"
	@go run ./cmd/api-gateway

.PHONY: run-memory
run-memory: ## Run with in-memory rate limiter
	@echo "$(BLUE)Running with in-memory rate limiter...$(NC)"
	@RATE_LIMIT_BACKEND=memory go run ./cmd/api-gateway

.PHONY: run-redis
run-redis: ## Run with Redis rate limiter
	@echo "$(BLUE)Running with Redis rate limiter...$(NC)"
	@RATE_LIMIT_BACKEND=redis \
	REDIS_HOST=localhost \
	REDIS_PORT=6379 \
	REDIS_PASSWORD=redispass \
	go run ./cmd/api-gateway

.PHONY: version
version: ## Show version information
	@echo "$(BLUE)Project:$(NC) $(PROJECT_NAME)"
	@echo "$(BLUE)Version:$(NC) $(VERSION)"
	@echo "$(BLUE)Go:$(NC) $(shell go version)"

.PHONY: deps
deps: ## Install dependencies
	@echo "$(BLUE)Installing dependencies...$(NC)"
	@go mod download
	@go mod tidy
	@echo "$(GREEN)✓ Dependencies installed$(NC)"

.PHONY: tools
tools: ## Install development tools
	@echo "$(BLUE)Installing development tools...$(NC)"
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@go install github.com/rakyll/hey@latest
	@echo "$(GREEN)✓ Tools installed$(NC)"

# =============================================================================
# Development workflow targets
# =============================================================================
.PHONY: dev
dev: ## Start development environment
	@echo "$(BLUE)Starting development environment...$(NC)"
	@docker compose up -d redis postgres
	@RATE_LIMIT_BACKEND=redis \
	REDIS_HOST=localhost \
	REDIS_PORT=6379 \
	REDIS_PASSWORD=redispass \
	go run ./cmd/api-gateway

.PHONY: dev-clean
dev-clean: clean-all ## Clean development environment
	@echo "$(GREEN)✓ Development environment cleaned$(NC)"

# =============================================================================
# CI/CD targets
# =============================================================================
.PHONY: ci
ci: fmt lint test-coverage build ## Run CI checks

.PHONY: ci-benchmark
ci-benchmark: benchmark loadtest ## Run CI benchmarks

# =============================================================================
# Database targets
# =============================================================================
.PHONY: db-migrate
db-migrate: ## Run database migrations
	@echo "$(BLUE)Running database migrations...$(NC)"
	@go run ./cmd/migrate
	@echo "$(GREEN)✓ Migrations complete$(NC)"

.PHONY: db-reset
db-reset: ## Reset database
	@echo "$(BLUE)Resetting database...$(NC)"
	@docker compose exec postgres psql -U postgres -d gateway -c "DROP TABLE IF EXISTS clients CASCADE;"
	@docker compose exec postgres psql -U postgres -d gateway -c "CREATE TABLE clients (api_key TEXT PRIMARY KEY, rate_limit INTEGER NOT NULL, created_at TIMESTAMP DEFAULT NOW(), updated_at TIMESTAMP DEFAULT NOW());"
	@echo "$(GREEN)✓ Database reset complete$(NC)"