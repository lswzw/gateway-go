.PHONY: all build test test-race vet lint guard clean ci help

BIN_DIR := bin
BINARY := $(BIN_DIR)/gateway
COVERAGE_OUT := coverage.out
COVERAGE_HTML := coverage.html

all: build

help:
	@echo "Available make targets:"
	@echo "  build      - Compile the gateway binary"
	@echo "  test       - Run all unit tests"
	@echo "  test-race  - Run all tests with race detector and coverage profile"
	@echo "  vet        - Run go vet static analysis"
	@echo "  lint       - Run linter (golangci-lint or fallback to go vet)"
	@echo "  guard      - Check design gates (no external shared-storage clients)"
	@echo "  clean      - Remove compiled binary and test coverage reports"
	@echo "  ci         - Run full CI checks (vet, lint, guard, build, test-race)"

build:
	@mkdir -p $(BIN_DIR)
	go build -v -o $(BINARY) ./cmd/gateway

test:
	go test -v ./...

test-race:
	go test -v -race -coverprofile=$(COVERAGE_OUT) -covermode=atomic ./...

vet:
	go vet ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, falling back to go vet..."; \
		go vet ./...; \
	fi

# guard 检查设计门禁：禁止引入外部共享存储客户端（Redis/redigo/rueidis 等）。
# 详见 internal/cache 包注释与 docs/performance-optimization.md。
guard:
	@if grep -Eq 'github\.com/(redis|go-redis|gomodule/redigo)' go.mod; then \
		echo "✗ 依赖门禁未通过：禁止引入外部共享存储客户端（设计结论：缓存只用本地内存）"; \
		exit 1; \
	fi
	@echo "✓ 依赖门禁通过：无外部共享存储客户端"

clean:
	rm -rf $(BIN_DIR) $(COVERAGE_OUT) $(COVERAGE_HTML)

ci: vet lint guard build test-race
