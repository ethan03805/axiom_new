VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -ldflags "\
	-X github.com/openaxiom/axiom/internal/version.Version=$(VERSION) \
	-X github.com/openaxiom/axiom/internal/version.GitCommit=$(GIT_COMMIT) \
	-X github.com/openaxiom/axiom/internal/version.BuildDate=$(BUILD_DATE)"

.PHONY: build test lint clean install

build:
	go build $(LDFLAGS) -o bin/axiom ./cmd/axiom

install:
	go install $(LDFLAGS) ./cmd/axiom

test:
	go test ./... -v -count=1

test-short:
	go test ./... -short -count=1

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, running go vet"; \
		go vet ./...; \
	fi

clean:
	rm -rf bin/
	go clean -testcache

tidy:
	go mod tidy

# Quick check: build + vet + test
check: tidy lint test
