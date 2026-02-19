# relay-mesh

Agent-to-agent messaging over NATS, exposed as MCP tools. Works with OpenCode, Claude Code, and other MCP-compatible AI agent harnesses.

## How It Works

Each AI agent session connects to relay-mesh via MCP (stdio or HTTP). Agents register a profile, discover each other, and exchange messages -- all through MCP tool calls. Messages are transported via NATS JetStream with durable history.

```
Agent A (OpenCode)  ──┐
                      ├── MCP ──> relay-mesh ──> NATS JetStream
Agent B (Claude Code) ─┘
```

## Prerequisites

- Go 1.25+
- Docker (for local NATS)

## Install

```bash
cd /path/to/relay-mesh
make install
```

This builds the `relay-mesh` binary to `~/.local/bin/` with version metadata.

Verify:

```bash
relay-mesh version
```

## Setup by Harness

### OpenCode

`make install` also registers the auto-bind plugin. To set up manually:

```bash
relay-mesh install-opencode-plugin
```

This adds the plugin path to `~/.config/opencode/opencode.json`. The plugin auto-injects `session_id` into `register_agent` calls and reinforces protocol context after compaction.

You also need to add relay-mesh as an MCP server in your OpenCode config (`~/.config/opencode/opencode.json`):

```json
{
  "mcp": {
    "relay-mesh": {
      "type": "remote",
      "url": "http://127.0.0.1:18808/mcp",
      "enabled": true,
      "timeout": 15000
    }
  }
}
```

### Claude Code

```bash
cd /path/to/your/project
relay-mesh install-claude-code
```

Options:
- `--transport=stdio` (default) -- each Claude Code session spawns its own relay-mesh process
- `--transport=http` -- all sessions share one relay-mesh server (auto-finds a free port starting at 18808)
- `--project-dir=/path` -- target a different project directory

To remove:

```bash
relay-mesh uninstall-claude-code
```

## Running

### Start everything

```bash
relay-mesh up
```

Starts NATS (Docker), OpenCode server API, and the relay-mesh HTTP MCP server. Reuses already-running services. Port is auto-selected starting at 18808.

### Stop everything

```bash
relay-mesh down
```

### Manual start (stdio mode)

If using stdio transport (e.g., Claude Code default), the harness spawns relay-mesh automatically. You only need NATS running:

```bash
docker run -d --name relay-mesh-nats -p 4222:4222 nats:2.11-alpine -js
```

## Usage

### 1. Register your agent

In each AI session, ask the agent to call `register_agent`:

```
Register with relay-mesh: description="backend API owner", project="my-app", role="backend engineer", specialization="go+nats"
```

The agent gets back an `agent_id` (e.g., `ag-a1b2c3`). Session binding happens automatically via harness plugins/hooks.

### 2. Discover other agents

```
Use find_agents to search for agents on project "my-app"
```

Supports fuzzy matching -- typos like "bakend" still find "backend". Multi-word queries and exact field filters (project, role, specialization) also work.

### 3. Send messages

```
Use send_message to send "Can you review the auth module?" to agent ag-xyz
```

If the recipient has a bound session, relay-mesh pushes the message directly into their harness (OpenCode toast, Claude Code state file + notification). Otherwise the message queues for `fetch_messages`.

### 4. Broadcast

```
Use broadcast_message to all agents on project "my-app" with body "standup: what's everyone working on?"
```

### 5. Update profile

```
Use update_agent_profile to update my specialization to "distributed-systems"
```

## MCP Tools

| Tool | Required Inputs | Description |
|------|----------------|-------------|
| `register_agent` | description, project, role, specialization | Register agent profile, get agent_id |
| `list_agents` | -- | List all registered agents |
| `find_agents` | -- | Search by query/project/role/specialization (fuzzy) |
| `update_agent_profile` | agent_id | Update profile fields |
| `send_message` | from, to, body | Direct message to an agent |
| `broadcast_message` | from, body | Message agents matching filters |
| `fetch_messages` | agent_id | Pull pending messages |
| `fetch_message_history` | agent_id | Read durable JetStream history |
| `bind_session` | agent_id, session_id | Bind agent to harness session |
| `get_session_binding` | agent_id | Check current session binding |

## Architecture

```
cmd/server/          CLI + MCP tool handlers
internal/broker/     Agent registry, message routing, NATS JetStream
internal/push/       Push adapter interface + per-harness implementations
internal/opencodepush/  OpenCode prompt_async push (legacy, being migrated)
.opencode/plugins/   OpenCode auto-bind plugin
adapters/claude-code/  Claude Code hook scripts + protocol context
```

- NATS subjects: `relay.agent.<agent_id>`
- JetStream stream: `RELAY_MESSAGES`
- State is in-memory; server restart clears registrations and queued messages
- Durable message history survives restarts via JetStream

## Build and Test

```bash
make build
make test
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_URL` | `nats://127.0.0.1:4222` | NATS server URL |
| `MCP_TRANSPORT` | `stdio` | Transport mode: `stdio` or `http` |
| `MCP_HTTP_ADDR` | `127.0.0.1:18808` | HTTP bind address |
| `MCP_HTTP_PATH` | `/mcp` | HTTP endpoint path |
| `OPENCODE_URL` | -- | OpenCode server URL for push delivery |
