.PHONY: all build test test-race vet lint clean ci help

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
	@echo "  clean      - Remove compiled binary and test coverage reports"
	@echo "  ci         - Run full CI checks (vet, lint, build, test-race)"

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

clean:
	rm -rf $(BIN_DIR) $(COVERAGE_OUT) $(COVERAGE_HTML)

ci: vet lint build test-race
