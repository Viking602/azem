BINARY ?= azem
VERSION ?= dev
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X 'main.version=$(VERSION)' -X 'main.gitCommit=$(GIT_COMMIT)' -X 'main.buildTime=$(BUILD_TIME)'

.PHONY: build azem-eval azem-eval-linux daemon gpui gui gui-windows frontend test test-gpui test-gui sqlc architecture-check contracts contracts-check

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/azem

azem-eval:
	go build -ldflags "$(LDFLAGS)" -o dist/eval/azem-eval ./cmd/azem-eval

azem-eval-linux: azem-eval
	mkdir -p dist/eval
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/eval/azem-eval-linux-amd64 ./cmd/azem-eval
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/eval/azem-eval-linux-arm64 ./cmd/azem-eval

daemon:
	mkdir -p dist/bin
	GOWORK=off go build -ldflags "$(LDFLAGS)" -o dist/bin/azem-daemon ./cmd/azem-daemon

gpui: daemon
	cd gpui && cargo build --locked --release -p azem-gpui
ifeq ($(shell uname -s),Darwin)
	mkdir -p dist/Azem-GPUI.app/Contents/MacOS dist/Azem-GPUI.app/Contents/Resources
	cp gpui/macos/Info.plist dist/Azem-GPUI.app/Contents/Info.plist
	cp cmd/azem-gui/AppIcon.icns dist/Azem-GPUI.app/Contents/Resources/AppIcon.icns
	cp gpui/target/release/azem-gpui dist/Azem-GPUI.app/Contents/MacOS/Azem
	cp dist/bin/azem-daemon dist/Azem-GPUI.app/Contents/MacOS/azem-daemon
	codesign --force --sign - --timestamp=none dist/Azem-GPUI.app/Contents/MacOS/azem-daemon
	codesign --force --sign - --timestamp=none dist/Azem-GPUI.app/Contents/MacOS/Azem
	codesign --force --sign - --timestamp=none dist/Azem-GPUI.app
	codesign --verify --deep --strict --verbose=2 dist/Azem-GPUI.app
else
	mkdir -p dist/gpui
	cp gpui/target/release/azem-gpui dist/gpui/azem-gpui
	cp dist/bin/azem-daemon dist/gpui/azem-daemon
endif

frontend:
	cd frontend && bun install --frozen-lockfile && bun run build

# Match LSMinimumSystemVersion in Info.plist; keeps CGO objects and the Go
# linker on the same deployment target (avoids "built for newer macOS than linked").
MACOSX_DEPLOYMENT_TARGET ?= 12.0
DARWIN_CGO_ENV := MACOSX_DEPLOYMENT_TARGET=$(MACOSX_DEPLOYMENT_TARGET) \
	CGO_CFLAGS="-mmacosx-version-min=$(MACOSX_DEPLOYMENT_TARGET)" \
	CGO_LDFLAGS="-mmacosx-version-min=$(MACOSX_DEPLOYMENT_TARGET)"

gui: frontend
ifeq ($(shell uname -s),Darwin)
	mkdir -p dist/Azem.app/Contents/MacOS dist/Azem.app/Contents/Resources
	cp cmd/azem-gui/Info.plist dist/Azem.app/Contents/Info.plist
	cp cmd/azem-gui/AppIcon.icns dist/Azem.app/Contents/Resources/AppIcon.icns
	$(DARWIN_CGO_ENV) go build -ldflags "$(LDFLAGS)" -o azem-gui ./cmd/azem-gui
	cp azem-gui dist/Azem.app/Contents/MacOS/Azem
	codesign --force --sign - --timestamp=none dist/Azem.app/Contents/MacOS/Azem
	codesign --force --sign - --timestamp=none dist/Azem.app
	codesign --verify --deep --strict --verbose=2 dist/Azem.app
else ifeq ($(shell go env GOOS),windows)
	go build -ldflags "-H windowsgui $(LDFLAGS)" -o Azem.exe ./cmd/azem-gui
else
	go build -ldflags "$(LDFLAGS)" -o azem-gui ./cmd/azem-gui
endif

WINDOWS_ARCH ?= amd64

gui-windows: frontend
	mkdir -p dist/windows-$(WINDOWS_ARCH)
	GOOS=windows GOARCH=$(WINDOWS_ARCH) CGO_ENABLED=0 go build -ldflags "-H windowsgui $(LDFLAGS)" -o dist/windows-$(WINDOWS_ARCH)/Azem.exe ./cmd/azem-gui

test: contracts-check
ifeq ($(shell uname -s),Darwin)
	$(DARWIN_CGO_ENV) go test ./...
else
	go test ./...
endif

test-gpui: contracts-check
	GOWORK=off go test ./internal/desktopipc ./internal/daemon ./internal/desktop ./cmd/azem-daemon
	cd gpui && cargo fmt --all --check
	cd gpui && cargo clippy --workspace --all-targets -- -D warnings
	cd gpui && cargo test --workspace --all-targets

test-gui:
	cd frontend && bun run typecheck && bun run test && bun run build

ifeq ($(shell uname -s),Darwin)
	$(DARWIN_CGO_ENV) go test ./internal/desktop ./internal/desktop/termhost ./cmd/azem-gui
else
	go test ./internal/desktop ./internal/desktop/termhost ./cmd/azem-gui
endif

sqlc:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate

contracts:
	go run ./cmd/gen-contracts

contracts-check:
	go run ./cmd/gen-contracts -check

architecture-check:
	sentrux check .
