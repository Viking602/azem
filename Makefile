BINARY ?= azem
VERSION ?= dev
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X 'main.version=$(VERSION)' -X 'main.gitCommit=$(GIT_COMMIT)' -X 'main.buildTime=$(BUILD_TIME)'

.PHONY: build test

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/azem

test:
	go test ./...
