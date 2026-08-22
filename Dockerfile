###############################################################################
# STAGE 1: Build stage
###############################################################################
FROM golang:1.25-alpine AS builder

# Install build dependencies and CA certificates for Alpine
RUN apk add --no-cache git ca-certificates tzdata && \
    adduser -D -u 10001 appuser

WORKDIR /app

# Copy go.mod and go.sum first for better layer caching
COPY go.mod go.sum* ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build a statically linked binary with stripped symbols
# CGO_ENABLED=0 ensures static linking for distroless compatibility
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -a -installsuffix cgo \
    -ldflags="-w -s" \
    -o /app/gateway ./cmd/api-gateway

###############################################################################
# STAGE 2: Final stage (distroless - production)
###############################################################################
FROM gcr.io/distroless/static-debian12:nonroot

# Copy the compiled binary from builder stage
COPY --from=builder --chown=nonroot:nonroot /app/gateway /app/gateway

# Copy CA certificates for HTTPS calls
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
ENV TZ=UTC

# Declare the port the service listens on
EXPOSE 8080

# Set non-root user for security (already nonroot in distroless)
USER nonroot:nonroot

# Health check to verify service is responding
# Uses a tiny wget to hit the healthz endpoint (no shell needed in distroless)
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD ["wget", "-qO-", "http://127.0.0.1:8080/healthz"]

# Run the gateway service
ENTRYPOINT ["/app/gateway"]

