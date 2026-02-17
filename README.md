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
- NATS server

## Run

1. Start NATS:

```bash
docker run --rm -p 4222:4222 nats:2
```

2. Build and run MCP server:

```bash
cd ~/relay-mesh
go run ./cmd/server
```

3. Optional custom NATS URL:

```bash
NATS_URL=nats://127.0.0.1:4222 go run ./cmd/server
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
