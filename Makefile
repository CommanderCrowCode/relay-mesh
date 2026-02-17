.PHONY: nats-up nats-down run run-http opencode-mesh-up opencode-mesh-down build test install package

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildDate=$(BUILD_DATE)
BIN_DIR ?= $(HOME)/.local/bin
DIST_DIR ?= dist

nats-up:
	docker compose up -d nats

nats-down:
	docker compose down

run:
	go run ./cmd/server

run-http:
	MCP_TRANSPORT=http MCP_HTTP_ADDR=127.0.0.1:8080 MCP_HTTP_PATH=/mcp go run ./cmd/server

opencode-mesh-up:
	./scripts/opencode-mesh-up.sh

opencode-mesh-down:
	./scripts/opencode-mesh-down.sh

build:
	go build -ldflags "$(LDFLAGS)" ./...

test:
	go test ./...

install:
	mkdir -p "$(BIN_DIR)"
	go build -ldflags "$(LDFLAGS)" -o "$(BIN_DIR)/relay-mesh" ./cmd/server
	RELAY_MESH_PLUGIN_PATH="$(CURDIR)/.opencode/plugins/relay-mesh-auto-bind.js" "$(BIN_DIR)/relay-mesh" install-opencode-plugin
	@echo "Installed $(BIN_DIR)/relay-mesh"
	@echo "Run: relay-mesh version"
	@echo "Run: relay-mesh up"

package:
	mkdir -p "$(DIST_DIR)"
	go build -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/relay-mesh" ./cmd/server
	cd "$(DIST_DIR)" && tar -czf relay-mesh-$(VERSION)-$(COMMIT).tar.gz relay-mesh
	@echo "Packaged $(DIST_DIR)/relay-mesh-$(VERSION)-$(COMMIT).tar.gz"
