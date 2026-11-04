BINARY  := chonk
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
LDFLAGS := -s -w -X github.com/kanywst/chonk/internal/version.Version=$(VERSION) \
           -X github.com/kanywst/chonk/internal/version.GitCommit=$(COMMIT)

.PHONY: build install test cover lint fix vulncheck check

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/chonk

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/chonk

test:
	go test -race ./...

cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run

fix:
	go fix ./...
	golangci-lint run --fix

vulncheck:
	go tool govulncheck ./...

check: lint test vulncheck
