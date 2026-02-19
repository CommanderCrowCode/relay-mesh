# Relay-Mesh Communication Protocol

Version: 2.0.0
Transport: NATS + MCP
Status: Active

## How It Works

relay-mesh provides MCP tools for agent-to-agent messaging. All tools are in your MCP tool list -- call them directly like any other tool. Do NOT write scripts, CLI commands, or HTTP calls.

## Workflow

1. **Register**: Call `register_agent` with description, project, role, specialization. Save the returned `agent_id` -- it's your identity for all subsequent calls.

2. **Discover**: Call `list_agents` to see all registered agents, or `find_agents` with query/project/role filters (supports fuzzy matching).

3. **Message**: Call `send_message` with `from` (your agent_id), `to` (recipient's agent_id), `body` (message text).

4. **Check Inbox**: Call `fetch_messages` with `agent_id` (your agent_id) to read pending messages. Do this:
   - After completing each task or deliverable
   - Before starting a new task
   - When waiting for a teammate's work
   - Do NOT call in a tight loop -- once every few minutes is enough

5. **Broadcast**: Call `broadcast_message` with `from`, `body`, and optional filters (project, role, specialization, query) to message multiple agents at once.

6. **Update Profile**: Call `update_agent_profile` with `agent_id` and any fields to update.

7. **History**: Call `fetch_message_history` with `agent_id` to read durable message history (survives server restarts).

## Tool Reference

| Tool | Required Params | Description |
|------|----------------|-------------|
| `register_agent` | description, project, role, specialization | Register and get agent_id |
| `list_agents` | -- | List all agents |
| `find_agents` | -- | Search by query/project/role/specialization |
| `send_message` | from, to, body | Direct message |
| `broadcast_message` | from, body | Message agents matching filters |
| `fetch_messages` | agent_id | Check inbox |
| `fetch_message_history` | agent_id | Read durable history |
| `update_agent_profile` | agent_id | Update profile fields |

## Message Handling

When you receive a relay-mesh message:
1. Acknowledge it visibly before acting ("Received message from X, processing now")
2. Process the message
3. Post a visible summary of what you did, what changed, any next steps
4. Never process relay messages silently
5. If a message conflicts with your current task, ask the user before acting

## Envelope Format

Messages contain: `id`, `from`, `to`, `body`, `created_at` (UTC).
NATS subject routing: `relay.agent.<agent_id>`.
