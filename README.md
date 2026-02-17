# relay-mesh

Minimal POC for standalone agent messaging foundations:

- MCP server (stdio)
- Agent-to-agent messaging over NATS
- No identity management (anonymous agent IDs)
- Agents interact only through MCP tools

## Architecture

- `cmd/server`: MCP server entrypoint
- `internal/broker`: in-memory agent registry + NATS-backed delivery
- NATS subjects: `relay.agent.<agent_id>`

## Prerequisites

- Go 1.25+
- Docker (for local NATS via `docker compose`)

## Run

1. Start NATS:

```bash
make nats-up
```

2. Run MCP server:

```bash
make run
```

3. Optional custom NATS URL:

```bash
NATS_URL=nats://127.0.0.1:4222 go run ./cmd/server
```

4. Stop local NATS:

```bash
make nats-down
```

## Build and Test

```bash
make build
make test
```

## MCP Tools

- `register_agent`
  - input: `{ "name": "optional" }`
  - output: `{ "agent_id": "ag-..." }`

- `list_agents`
  - input: `{}`
  - output: `[ { "id": "ag-...", "name": "..." } ]`

- `send_message`
  - input: `{ "from": "ag-...", "to": "ag-...", "body": "hello" }`
  - output: message envelope JSON

- `fetch_messages`
  - input: `{ "agent_id": "ag-...", "max": "10" }`
  - output: array of queued messages

## Notes

- This POC keeps delivery queue in memory.
- If server restarts, agent registrations and queued messages are lost.
- This is intentional for a small stepping-stone project.

## Ready-for-Usage Checklist

- `make nats-up` starts NATS successfully.
- `make run` starts MCP server on stdio.
- MCP client can register at least two agents with `register_agent`.
- One agent can `send_message` to another and recipient can `fetch_messages`.
- `make build` and `make test` both pass.
