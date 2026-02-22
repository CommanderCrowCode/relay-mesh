# Relay-Mesh Protocol Context

You are connected to relay-mesh for agent-to-agent messaging. All tools below are MCP tools in your tool list -- call them directly.

## AUTOMATIC: Register on session start
**As your FIRST action**, call `register_agent` with description, project, role, specialization (infer from your context). Save the returned `agent_id`. Do this BEFORE any other work.

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
relay-mesh is **NOT** a one-way broadcast system. Every message requires a response:
- **Acknowledge before acting**: "Received. Starting <task>."
- **Reply with results**: send_message back when you finish — include file paths, artifact IDs.
- **Signal blockers**: `send_message(priority="urgent")` the moment you are stuck.
- **Silence = you look stuck.** Keep the loop alive.

## Workflow (after registration)
1. **Discover**: Call `list_agents(active_within="5m")` or `find_agents` (fuzzy search, recency filter) to find teammates.
2. **Message**: Call `send_message` (from, to, body, optional priority: normal|urgent|blocking).
3. **Check Inbox**: Call `fetch_messages` after each task, before starting new work, or when waiting.
4. **Broadcast**: Call `broadcast_message` (from, body, optional: project/role/query/priority filters).
5. **Share Artifacts**: Call `publish_artifact` to share schemas, file trees, Dockerfiles. Teammates call `list_artifacts`.
6. **Heartbeat**: Call `heartbeat_agent(agent_id)` every 5 min during long tasks to stay visible.

## When to Check Messages (MANDATORY)
- Call `fetch_messages` every 3 minutes OR after every 5 tool calls — whichever comes first
- Even when push delivery is active — push is best-effort, fetch is guaranteed
- After completing each file or task deliverable
- Before starting a new task (priorities may have changed)
- Immediately when you become unblocked

## Completing Your Work
When your implementation is done:
1. Call `declare_task_complete(agent_id=<your_id>, summary="What you built and where")`
2. Call `update_agent_profile(agent_id=<your_id>, status="done")`
3. Send a final summary message to team-lead (include artifact IDs, file paths)

**Team-lead only** — before declaring project complete:
1. Call `check_project_readiness(project="<your-project>")`
2. If any agents are NOT done: message them asking for status
3. ONLY broadcast project completion when `check_project_readiness` returns `ready: true`

## Tool Reference

- `get_team_status(project?)` — all agents' status (idle/working/blocked/done), last_seen, unread_messages
- `shared_context(action, project, key?, value?)` — publish/read shared paths, schemas, API contracts
- `wait_for_agents(project, min_count?, timeout_seconds?)` — wait for N teammates to register
- `heartbeat_agent(agent_id)` — signal still alive; call every 5 min to avoid pruning
- `declare_task_complete(agent_id, summary?)` — mark your work done, signals team-lead
- `check_project_readiness(project)` — check if all agents done (team-lead uses before closing)
- `update_agent_profile(agent_id, ..., status?)` — status: idle|working|blocked|done
- `get_message_status(message_id)` — check if a sent message has been read
- `publish_artifact(from, project, artifact_type, name, content)` — share schemas, file trees, configs
- `list_artifacts(project, artifact_type?)` — browse teammates' published artifacts
- `prune_stale_agents(max_age?)` — remove agents not seen recently (team-lead only)

## Message Etiquette
1. **Acknowledge** received messages before acting — "Received from X. Starting <task>."
2. **Reply with results** after processing — include file paths and artifact IDs
3. For urgent/blocking priority: respond immediately, drop current work if needed
4. Never process relay messages silently — always close the loop
5. If a message conflicts with your current task, ask the user first
