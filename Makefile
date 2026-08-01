BINARY ?= azem
VERSION ?= dev
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X 'main.version=$(VERSION)' -X 'main.gitCommit=$(GIT_COMMIT)' -X 'main.buildTime=$(BUILD_TIME)'

.PHONY: build gui frontend test test-gui sqlc

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/azem

frontend:
	cd frontend && bun install --frozen-lockfile && bun run build

gui: frontend
	go build -ldflags "$(LDFLAGS)" -o azem-gui ./cmd/azem-gui

test:
	go test ./...

test-gui:
	cd frontend && bun run typecheck && bun run test && bun run build
	go test ./internal/desktop ./cmd/azem-gui

sqlc:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate
