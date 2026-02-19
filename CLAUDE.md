# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Relay-mesh is a minimal POC for anonymous agent-to-agent messaging over NATS, mediated through an MCP server. It's a stepping stone toward a larger system called "Civitas." Agents (e.g., OpenCode sessions) register with the broker, discover each other, and exchange messages — all through MCP tools, never directly via the broker API.

## Build & Development Commands

```bash
make build              # Compile (go build with version ldflags)
make test               # Run all tests (go test ./...)
make run                # Start MCP server (stdio transport)
make run-http           # Start MCP server (HTTP at 127.0.0.1:8080/mcp)
make install            # Build binary → ~/.local/bin/relay-mesh + install OpenCode plugin
make nats-up            # Start NATS via docker compose
make nats-down          # Stop NATS
```

After `make install`, the CLI provides orchestration commands:
```bash
relay-mesh up           # Start full stack (NATS + OpenCode server + relay HTTP)
relay-mesh down         # Stop managed processes
relay-mesh version      # Print version info
```

Run a single test by name:
```bash
go test ./internal/broker/ -run TestSendAndFetch
```

## Architecture

```
OpenCode Sessions
       │
       │  MCP (stdio or HTTP :8080/mcp)
       ▼
cmd/server/main.go            ← All MCP tool handlers + CLI subcommands (~850 lines)
       │
       │  broker.Broker
       ▼
internal/broker/broker.go     ← In-memory agent registry + NATS pub/sub + fuzzy search
       │
       │  NATS subjects: relay.agent.<agent_id>
       │  JetStream stream: RELAY_MESSAGES (7-day retention)
       ▼
   NATS server (:4222, via Docker)

On message delivery (if OPENCODE_URL set):
       │
       ▼
internal/opencodepush/        ← HTTP push to OpenCode API (prompt injection + toast)
  pusher.go                      POST /session/<id>/prompt_async
  session_resolver.go            GET /session (auto-bind on register)
```

**Key design constraints** (from CONSTITUTION.md):
- Broker state is intentionally in-memory (no persistence layer)
- Agents interact through MCP tools only — never directly via broker
- No identity/auth in this POC phase
- Go is the only runtime language; NATS is the required transport

## Code Layout

- `cmd/server/main.go` — Single-file entrypoint containing all MCP tool registrations, handlers, CLI subcommand dispatch (`serve`, `up`, `down`, `version`, `install-opencode-plugin`), and process orchestration
- `internal/broker/` — Core domain: agent registry, message routing via NATS JetStream, fuzzy agent discovery with relevance scoring
- `internal/opencodepush/` — Optional HTTP push delivery to OpenCode sessions (prompt injection + TUI toasts)
- `.opencode/plugins/relay-mesh-auto-bind.js` — OpenCode plugin that auto-injects `session_id` into `register_agent` calls and preserves protocol context through compaction

## ID Conventions

- Agent IDs: `ag-<16 hex chars>` (e.g., `ag-a1b2c3d4e5f6a7b8`)
- Message IDs: `msg-<16 hex chars>`
- NATS subjects: `relay.agent.<agent_id>`

## Transport Modes

- **stdio** (default): One MCP server per OpenCode session, used for local dev
- **http** (`MCP_TRANSPORT=http`): Single shared server for multi-agent mesh at `MCP_HTTP_ADDR/MCP_HTTP_PATH`

## Environment Variables

| Variable | Default | Purpose |
|---|---|---|
| `NATS_URL` | `nats://127.0.0.1:4222` | NATS connection |
| `MCP_TRANSPORT` | `stdio` | `stdio` or `http` |
| `MCP_HTTP_ADDR` | `127.0.0.1:8080` | HTTP listen address |
| `OPENCODE_URL` | *(disabled)* | OpenCode server URL for push delivery |
| `OPENCODE_AUTO_BIND_WINDOW` | `15m` | Max age for auto-bind session resolution |

## Testing Patterns

- Tests use an **embedded NATS server** (`nats-server/v2/server`) — no Docker required for tests
- `newTestBroker(t)` spins up a real NATS+JetStream instance per test with automatic cleanup
- `waitForQueuedMessages(t, b, agentID, minCount)` polls with a 3-second deadline for async NATS delivery
- `pusher_test.go` uses `net/http/httptest` for the OpenCode HTTP surface
- Broker behavior changes require tests covering the modified path (Constitution Article IV)

## Session Binding Priority

When `register_agent` is called, the session is bound in this order:
1. Explicit `session_id` in tool input
2. HTTP request headers (`X-Opencode-Session-Id`, `X-Session-Id`)
3. Auto-resolved from latest active unbound OpenCode session (via `SessionResolver`)

## State Directory

`~/.relay-mesh/` stores PID files and logs for managed processes (`relay-http.pid`, `opencode-serve.pid`, etc.).
