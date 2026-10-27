# CLAUDE.md

This document provides developer guidelines, architecture context, and command references for the `gateway-go` project.

---

## 1. Project Overview & Architecture

`gateway-go` is a high-performance, production-ready API Gateway built with Go and Gin. It provides dynamic routing, pluggable middleware, client IP security, traffic control (rate limiting, circuit breaking), and observability.

### Core Architecture Components

```
Client HTTP Request
       │
       ▼
[Gin HTTP Server] (Business Port, default 8080)
       │
       ├─► Global Middleware (Access Log with body sampling, Request ID, Client IP)
       │
       ├─► Router Manager (internal/router)
       │     └─ Trie Router + LRU Route Cache (Exact, Prefix, Regex, Wildcard, Priority, A/B Testing)
       │
       ├─► Plugin Chain (internal/plugin/chain)
       │     └─ Ordered execution: ratelimit -> circuitbreaker -> cors -> ipwhitelist -> interface_auth
       │     └─ Short-circuits on error or written response; caches execution results when enabled
       │
       └─► Upstream Reverse Proxy (cmd/gateway)
             └─ Streaming body forwarding (no full-body buffering; optional SHA256 sampling)
```

### Management vs. Business Plane Separation

- **Business Port (`server.port`, default `8080`)**:
  - Exclusively handles business API proxy traffic.
  - **Notice**: Internal management endpoints (e.g. `/gatewaygo/health`) are strictly prohibited on the business port to prevent internal probe and metrics leakage.
- **Admin Management Port (`admin.listen`, default `127.0.0.1:9090`)**:
  - Internal operational endpoints:
    - `GET /healthz` - Liveness probe
    - `GET /readyz` - Readiness probe
    - `GET /metrics` - Prometheus metrics

---

## 2. Directory Layout

- `cmd/gateway/`: Gateway main entrypoint, HTTP server lifecycle (graceful shutdown, signal handling, reverse proxy).
- `config/`: Default configuration files (`config.yaml`).
- `internal/config/`: Configuration loader, validator, and hot reload manager (`ConfigManager`).
- `internal/netutil/`: Client IP extraction with trusted proxies / CIDR support (`netutil.ClientIP`).
- `internal/plugin/`: Pluggable middleware architecture, lifecycle, and plugin registry.
  - `chain/`: Plugin execution chain with sorting, caching, and short-circuit error handling.
  - `plugins/ratelimit/`: Token bucket rate limiting (per-IP or per-path).
  - `plugins/circuitbreaker/`: Sliding window circuit breaker with 3-state transitions (Closed, Open, HalfOpen).
  - `plugins/ipwhitelist/`: IP access control.
  - `plugins/cors/`: Cross-origin resource sharing.
  - `plugins/interface_auth/`: API authentication.
- `internal/response/`: Unified error response formatter (`response.RespondError`) ensuring idempotency and consistent JSON errors.
- `internal/router/`: Trie router, route caching, rule matching, and A/B test routing.
- `internal/logger/`: Zap-based structured logging with access logging.
- `internal/pool/`: High-performance `sync.Pool` object pools (`BufferPool`, `RequestPool`).
- `internal/testutil/`: Reusable test helpers (`NewMockUpstream`, `AssertJSON`, `AssertJSONExact`, `AssertStatus`, `NewTestContext`, `PerformRequest`, `CreateTempConfigFile`).

---

## 3. Developer Commands

### Build & Run

```bash
# Build binary to bin/gateway
make build
# or: go build -v -o bin/gateway ./cmd/gateway

# Run gateway with config
go run ./cmd/gateway -c config/config.yaml

# Test configuration syntax without starting the server (similar to nginx -t)
go run ./cmd/gateway -c config/config.yaml -t

# Send signal to running gateway (reload | stop | quit)
go run ./cmd/gateway -s reload
```

### Testing & Code Quality

```bash
# Run all unit tests
make test
# or: go test -v ./...

# Run all tests with race detector and coverage output (Mandatory before committing)
make test-race
# or: go test -v -race -coverprofile=coverage.out -covermode=atomic ./...

# Static analysis
make vet
# or: go vet ./...

# Linting
make lint

# Run full CI suite locally (vet, lint, build, test-race)
make ci

# Clean build artifacts and coverage files
make clean
```

---

## 4. Coding & Design Conventions

1. **Thread Safety & Race Free**:
   - All code must pass `go test -race ./...` with zero race warnings.
   - Synchronize shared state using mutexes or atomic operations. Avoid lock inversions (e.g. holding read lock while acquiring write lock).
2. **Streaming Reverse Proxy**:
   - Never use `io.ReadAll` to buffer full request or response bodies.
   - Use `io.TeeReader` or optional sampling (`log.body_limit`) to avoid 4KB truncation or unbounded memory allocation.
3. **Unified Error Contract**:
   - Do not write raw error responses directly; use `response.RespondError(c, status, message, requestID)`.
   - The standalone `error` plugin is deprecated; error handling is handled uniformly by the gateway core.
4. **Client IP Security**:
   - Never trust `X-Forwarded-For` or `X-Real-IP` headers blindly.
   - Always resolve client IPs via `netutil.ClientIP(c, trustedProxies)`.
5. **Testing Baseline**:
   - Maintain unit and integration tests across packages using `internal/testutil`.
   - Core packages must maintain test statement coverage ≥ 60%.
