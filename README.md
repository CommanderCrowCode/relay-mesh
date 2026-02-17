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

## Install (Versioned)

Install a versioned binary tied to git commit metadata:

```bash
make install
```

`make install` also installs the OpenCode auto-bind plugin into your OpenCode config if not already present.

Check installed version:

```bash
relay-mesh version
```

## OpenCode Auto-Bind Plugin

This repo includes `.opencode/plugins/relay-mesh-auto-bind.js`, which auto-injects current OpenCode `session_id` into `register_agent` tool calls using OpenCode hook `tool.execute.before`.

To enable it, add to your OpenCode config (`~/.config/opencode/opencode.json`):

```json
{
  "plugin": [
    "/Users/tanwa/relay-mesh/.opencode/plugins/relay-mesh-auto-bind.js"
  ]
}
```

## Run

One command orchestration (recommended):

```bash
relay-mesh mesh-up
```

Stop managed services:

```bash
relay-mesh mesh-down
```

1. Start NATS:

```bash
make nats-up
```

2. Run MCP server:

```bash
make run
```

For shared multi-client mesh (single MCP instance over HTTP):

```bash
make run-http
```

Default URL: `http://127.0.0.1:8080/mcp`

For OpenCode shared mesh + push with auto-start/reuse:

```bash
make opencode-mesh-up
```

To enable OpenCode push injection from server:

```bash
OPENCODE_URL=http://127.0.0.1:4097 make run-http
```

3. Optional custom NATS URL:

```bash
NATS_URL=nats://127.0.0.1:4222 go run ./cmd/server
```

4. Stop local NATS:

```bash
make nats-down
```

To stop all auto-started OpenCode mesh services:

```bash
make opencode-mesh-down
```

## Build and Test

```bash
make build
make test
```

## MCP Tools

- `register_agent`
  - input:
    - required: `description`, `project`, `role`, `specialization`
    - optional: `name`, `github`, `branch`, `session_id`
  - example: `{ "name": "alpha", "description": "backend owner", "project": "civitas", "role": "backend engineer", "specialization": "go+nats", "github": "CommanderCrowCode", "branch": "feat/mesh" }`
  - output: `{ "agent_id": "ag-..." }` (may also include `session_id` if auto-bound)

- `list_agents`
  - input: `{}`
  - output: profile-rich list of agents

- `send_message`
  - input: `{ "from": "ag-...", "to": "ag-...", "body": "hello" }`
  - output: message envelope JSON

- `broadcast_message`
  - input: `{ "from": "ag-...", "body": "status sync", "query": "backend", "project": "civitas", "role": "backend engineer", "specialization": "go", "max": "20" }`
  - output: array of sent message envelopes

- `fetch_messages`
  - input: `{ "agent_id": "ag-...", "max": "10" }`
  - output: array of queued messages

- `find_agents`
  - input: `{ "query": "nats", "project": "civitas", "role": "backend engineer", "specialization": "go", "max": "10" }`
  - output: filtered list of agent profiles

- `update_agent_profile`
  - input: `{ "agent_id": "ag-...", "description": "updated", "project": "civitas", "role": "architect", "github": "CommanderCrowCode", "branch": "main", "specialization": "distributed-systems" }`
  - output: updated agent profile

- `bind_session`
  - input: `{ "agent_id": "ag-...", "session_id": "..." }`
  - output: `{ "agent_id": "...", "session_id": "..." }`

- `get_session_binding`
  - input: `{ "agent_id": "ag-..." }`
  - output: `{ "agent_id": "...", "session_id": "..." }`

## Notes

- This POC keeps delivery queue in memory.
- If server restarts, agent registrations and queued messages are lost.
- This is intentional for a small stepping-stone project.
- If `OPENCODE_URL` is set and recipient has a bound session, `send_message` also pushes to OpenCode session via `prompt_async`.
- Auto-bind order on `register_agent`: explicit `session_id` input, request header detection, then latest active unbound OpenCode session (best effort).
- If some metadata is not known at registration, provide `"unknown"` and later refine with `update_agent_profile`.

## Ready-for-Usage Checklist

- `make nats-up` starts NATS successfully.
- `make run` starts MCP server on stdio.
- MCP client can register at least two agents with `register_agent`.
- One agent can `send_message` to another and recipient can `fetch_messages`.
- `make build` and `make test` both pass.
