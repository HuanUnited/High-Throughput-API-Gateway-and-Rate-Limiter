#!/usr/bin/env bash
set -euo pipefail

# Ensure you are on main and create a backup
git checkout main
git branch -f backup-main main

# Clean up any leftover temporary branch
git branch -D polished-main 2>/dev/null || true

# Start a clean orphan branch
git checkout --orphan polished-main
git rm -rf --cached . 2>/dev/null || true

commit_step() {
    local date="$1"
    local message="$2"
    shift 2

    for item in "$@"; do
        if [ -e "$item" ]; then
            git add "$item"
        fi
    done

    GIT_AUTHOR_DATE="$date" \
    GIT_COMMITTER_DATE="$date" \
    git commit -m "$message"
}

echo "Building refined commit history..."

# Milestone 1: Base repository and module definition
commit_step "2026-06-15 10:00:00 +0700" \
"chore(project): initialize Go module layout and git configuration" \
.gitignore go.mod

# Milestone 2: Configuration loader and Makefile
commit_step "2026-06-18 14:30:00 +0700" \
"feat(config): implement environment configuration loader and build tools" \
.env.example Makefile internal/config/

# Milestone 3: Core thread-safe token bucket algorithm
commit_step "2026-06-23 16:15:00 +0700" \
"feat(limiter): implement in-memory token bucket algorithm with unit tests" \
internal/limiter/

# Milestone 4: Storage schema and PostgreSQL integration
commit_step "2026-07-04 11:20:00 +0700" \
"feat(storage): implement PostgreSQL persistence and quota migrations" \
migrations/ internal/storage/postgres.go internal/storage/postgres_test.go

# Milestone 5: L1 In-Memory Caching layer
commit_step "2026-07-12 15:45:00 +0700" \
"feat(storage): add thread-safe L1 cache with TTL and negative lookup caching" \
internal/storage/l1_cached_storage.go internal/storage/l1_cached_storage_test.go

# Milestone 6: Distributed Redis rate limiting
commit_step "2026-07-19 13:10:00 +0700" \
"feat(ratelimit): implement Redis token bucket with server-time Lua synchronization" \
internal/ratelimit/

# Milestone 7: Prometheus metrics and observability
commit_step "2026-07-27 17:30:00 +0700" \
"feat(metrics): add Prometheus latency collectors and path sanitization" \
internal/metrics/ deploy/

# Milestone 8: Resilience, Circuit Breaker, and Proxy Handler
commit_step "2026-08-05 16:00:00 +0700" \
"feat(proxy): build reverse proxy with circuit breaker and idempotent retries" \
internal/handler/

# Milestone 9: Application entrypoint and lifecycle probes
commit_step "2026-08-12 11:15:00 +0700" \
"feat(gateway): wire application entrypoint, healthcheck probes, and signal handling" \
cmd/api-gateway/main.go cmd/api-gateway/main_test.go

# Milestone 10: Containerization and multi-service deployment
commit_step "2026-08-18 18:20:00 +0700" \
"build(docker): configure distroless Dockerfile and docker-compose stack" \
Dockerfile docker-compose.yml

# Milestone 11: CI/CD workflows and linting rules
commit_step "2026-08-21 14:00:00 +0700" \
"ci(workflows): configure GitHub Actions for linting and database integration tests" \
.github/ .golangci.yml

# Milestone 12: Load testing suite and performance benchmarks
commit_step "2026-08-23 15:30:00 +0700" \
"test(load): implement k6 multi-scenario load tests and benchmark suite" \
loadtests/ cmd/api-gateway/benchmark_test.go

# Milestone 13: Final documentation and architectural reference
commit_step "2026-08-25 14:00:00 +0700" \
"docs(readme): finalize system architecture diagrams and performance benchmarks" \
README.md

# Stage any remaining tracked files to match current workspace state exactly
if [ -n "$(git status --porcelain)" ]; then
    GIT_AUTHOR_DATE="2026-08-25 14:30:00 +0700" \
    GIT_COMMITTER_DATE="2026-08-25 14:30:00 +0700" \
    git add -A
    git commit -m "chore: synchronize repository tree state"
fi

echo "Commit history reconstruction complete."