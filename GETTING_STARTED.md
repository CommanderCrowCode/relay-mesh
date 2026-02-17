# Getting Started: OpenCode Shared Mesh + Push Delivery

This guide is the reliable OpenCode-only setup:
1. One shared `relay-mesh` MCP server on HTTP (single mesh for all OpenCode sessions).
2. One OpenCode headless server for session API.
3. Built-in relay push from `send_message` into target OpenCode session via `prompt_async`.

References:
1. OpenCode MCP docs: https://opencode.ai/docs/mcp-servers/
2. OpenCode config schema (`mcp.local` / `mcp.remote`): https://opencode.ai/config.json
3. OpenCode Server API (`/session`, `/session/{id}/prompt_async`): https://opencode.ai/docs/server-api/
4. OpenCode plugin hooks (`tool.execute.before`): https://opencode.ai/docs/plugins/hooks

## 1. Start Core Services (Automatic)

Install once:

```bash
cd /Users/tanwa/relay-mesh
make install
```

Then one command starts missing services and reuses already-running ones:

```bash
relay-mesh mesh-up
```

What it does:
1. Ensures NATS is up.
2. Checks if OpenCode server API is already running at `http://127.0.0.1:4097`; starts it only if missing.
3. Checks if relay MCP HTTP server is already running at `http://127.0.0.1:8080/mcp`; starts it only if missing.

## 2. Configure OpenCode MCP as Remote (Shared)

Create or update `/Users/tanwa/.config/opencode/opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "relay-mesh": {
      "type": "remote",
      "url": "http://127.0.0.1:8080/mcp",
      "enabled": true,
      "timeout": 15000
    }
  }
}
```

Verify:

```bash
opencode mcp list
```

You should see `relay-mesh` connected.

## 2.5 Enable Auto Session Binding Plugin (No Manual Session IDs)

This is done automatically by `make install` (idempotent).

If you need to set it manually, add the plugin path to your OpenCode config (`/Users/tanwa/.config/opencode/opencode.json`):

```json
{
  "$schema": "https://opencode.ai/config.json",
  "plugin": [
    "/Users/tanwa/relay-mesh/.opencode/plugins/relay-mesh-auto-bind.js"
  ],
  "mcp": {
    "relay-mesh": {
      "type": "remote",
      "url": "http://127.0.0.1:8080/mcp",
      "enabled": true,
      "timeout": 15000
    }
  }
}
```

Restart OpenCode after adding plugin config.

## 3. Register OpenCode Agents

In each OpenCode session, call:

1. `register_agent` with required metadata:
   - `description`
   - `project`
   - `role`
   - `specialization`
   - optional `name`, `github`, `branch`
2. Save returned `agent_id`.
3. Response should include `session_id` automatically via plugin hook.

If some metadata is unknown right now, use `"unknown"` and refine later with `update_agent_profile`.

Since all sessions now use the same shared MCP server, `list_agents` should show all registered agents.

## 4. Bind Agent to OpenCode Session (Fallback)

Only required if plugin is not enabled or session auto-injection fails.

In that case:
1. Get the OpenCode session ID:

```bash
curl -sS http://127.0.0.1:4097/session | jq
```

2. Call MCP tool:
   - `bind_session` with `{ "agent_id": "ag-...", "session_id": "..." }`
3. Optionally verify:
   - `get_session_binding` with `{ "agent_id": "ag-..." }`

This binding is kept in relay server memory and used for push delivery.

Tip: you can also pass session id directly in registration:
`register_agent { "name":"alpha","description":"...","project":"...","role":"...","specialization":"...","session_id":"..." }`

Example registration:
`register_agent { "name":"alpha","description":"backend runtime owner","project":"civitas","role":"backend engineer","specialization":"go+nats","github":"CommanderCrowCode","branch":"feat/mesh" }`

Example profile update later:
`update_agent_profile { "agent_id":"ag-...","description":"now also handles discovery","branch":"main","specialization":"distributed-systems" }`

## 5. End-to-End Test

From session `alpha`:
1. `send_message` to `beta` with `body: "hello from alpha"`

Relevance routing checks:
1. `find_agents` with `{ "query":"backend", "project":"civitas" }`
2. `broadcast_message` with `{ "from":"ag-alpha","body":"daily sync","project":"civitas","role":"backend engineer" }`

Expected:
1. `beta` session receives a new injected message automatically.
2. `fetch_messages` for `beta` still returns queued message as fallback.

## 6. Shutdown

```bash
relay-mesh mesh-down
```
