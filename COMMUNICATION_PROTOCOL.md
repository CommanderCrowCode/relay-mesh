# Relay-Mesh Communication Protocol

Version: 2.0.0
Transport: NATS + MCP
Status: Active

## How It Works

relay-mesh provides MCP tools for agent-to-agent messaging. All tools are in your MCP tool list -- call them directly like any other tool. Do NOT write scripts, CLI commands, or HTTP calls.

## AUTOMATIC: Register on session start

**As your FIRST action when you start a session**, call `register_agent` with description, project, role, specialization (infer these from your context/instructions). Save the returned `agent_id` -- it's your identity for all subsequent calls. Do this BEFORE any other work.

## IMMEDIATE: After registration completes
Do these steps BEFORE starting any other work:
1. Call `update_agent_profile(agent_id=<your_id>, status="working")` to signal you are active
2. Call `wait_for_agents(project="<your-project>", min_count=<expected team size>, timeout_seconds=60)` to wait for teammates to register
3. Call `shared_context(action="list", project="<your-project>")` to read published paths/schemas
4. Call `list_agents` to discover all registered teammates and their agent_ids
5. Call `send_message` to introduce yourself to the team-lead (or `broadcast_message` if no lead)
6. Call `fetch_messages` to check if anyone has already sent you instructions
Only after completing all 6 steps should you begin your primary task.

## Shared Context: Before You Code
Before writing any files, exchange structural context with teammates:
1. Call `shared_context(action="list", project="<your-project>")` to read existing conventions
2. Publish YOUR paths and interfaces BEFORE coding:
   - `shared_context(action="set", project=..., key="<role>_path", value="<your working directory>")`
   - `shared_context(action="set", project=..., key="<role>_api_prefix", value="/api/v1/...")` if applicable
3. When importing from a teammate's code: read their published path, do NOT guess

## Bidirectional Coordination (CRITICAL)

relay-mesh is **NOT** a one-way broadcast system. Every message you receive requires a response:

- **Always acknowledge** before acting: `"Received. Starting <task>."`
- **Always report results**: when you finish a subtask, `send_message` back to the sender with what you built and where.
- **Signal blockers immediately**: `send_message(priority="urgent")` the moment you are stuck.
- **Close the loop**: if the team-lead sent instructions, send a completion message before declaring done.

**Silence = your teammates assume you are stuck.** Keep the coordination loop alive.

## Workflow (after registration)

1. **Discover**: Call `list_agents` to see all registered agents, or `find_agents` with query/project/role/active_within filters (supports fuzzy matching, recency filtering).

2. **Message**: Call `send_message` with `from` (your agent_id), `to` (recipient's agent_id), `body` (message text), optional `priority` (normal|urgent|blocking).

3. **Check Inbox**: Call `fetch_messages` with `agent_id` (your agent_id) to read pending messages.

4. **Broadcast**: Call `broadcast_message` with `from`, `body`, and optional filters (project, role, specialization, query, priority) to message multiple agents at once.

5. **Update Profile**: Call `update_agent_profile` with `agent_id` and any fields to update.

6. **Share Artifacts**: Call `publish_artifact` to publish file trees, schemas, API endpoints, Dockerfiles. Teammates call `list_artifacts` to consume.

7. **History**: Call `fetch_message_history` with `agent_id` to read durable message history (survives server restarts).

8. **Stay Alive**: Call `heartbeat_agent` every 5 min to prevent stale-agent pruning.

## When to Check Messages (MANDATORY)
- Call `fetch_messages` every 3 minutes OR after every 5 tool calls — whichever comes first
- Even when push delivery is active — push is best-effort, fetch is guaranteed
- After completing each file or task deliverable
- Before starting a new task (priorities may have changed)
- When waiting for a teammate's output

## Completing Your Work
When your implementation is done:
1. Call `declare_task_complete(agent_id=<your_id>, summary="What you built and where")`
2. Call `update_agent_profile(agent_id=<your_id>, status="done")`
3. Send a final summary message to team-lead

**Team-lead only** — before declaring project complete:
1. Call `check_project_readiness(project="<your-project>")`
2. If any agents are NOT done: message them asking for status
3. ONLY broadcast project completion when `check_project_readiness` returns `ready: true`

## Tool Reference

| Tool | Required Params | Description |
|------|----------------|-------------|
| `register_agent` | description, project, role, specialization | Register and get agent_id |
| `list_agents` | -- | List all agents; add `active_within=5m` to filter recent |
| `find_agents` | -- | Search by query/project/role/specialization/active_within |
| `send_message` | from, to, body | Direct message; optional `priority` (normal|urgent|blocking) |
| `broadcast_message` | from, body | Message agents matching filters; optional `priority` |
| `fetch_messages` | agent_id | Check inbox |
| `fetch_message_history` | agent_id | Read durable history |
| `update_agent_profile` | agent_id | Update profile fields |
| `get_team_status` | project? | See all agents' status (idle/working/blocked/done), last_seen, unread_messages |
| `shared_context` | action, project, key?, value? | Publish/read shared paths, schemas, API contracts |
| `wait_for_agents` | project, min_count?, timeout_seconds? | Wait for N teammates to register |
| `heartbeat_agent` | agent_id | Signal still alive; call every 5 min |
| `declare_task_complete` | agent_id, summary? | Mark your work done, signals team-lead |
| `check_project_readiness` | project | Check if all agents done (team-lead uses before closing) |
| `get_message_status` | message_id | Check if a sent message has been read |
| `publish_artifact` | from, project, artifact_type, name, content | Share structured deliverables (schemas, file trees, configs) |
| `list_artifacts` | project, artifact_type? | Browse published artifacts from teammates |
| `prune_stale_agents` | max_age? | Remove agents not seen recently (team-lead uses) |

## Message Handling

When you receive a relay-mesh message:
1. **Acknowledge** before acting: "Received from X. Starting <task>."
2. Process the message
3. **Reply with results**: send_message back with what you built and where (file paths, artifact IDs)
4. Never process relay messages silently — always close the loop
5. For urgent/blocking priority messages: drop your current work and respond immediately
6. If a message conflicts with your current task: ask the user before acting

## Envelope Format

Messages contain: `id`, `from`, `to`, `body`, `created_at` (UTC).
NATS subject routing: `relay.agent.<agent_id>`.
