# Relay-Mesh Agent Instructions

## Communication via relay-mesh

You have relay-mesh MCP tools available for communicating with other agents. These are MCP tools in your tool list -- call them directly like any other tool. Do NOT write scripts or CLI commands to interact with relay-mesh.

### AUTOMATIC: Register on session start

**As your FIRST action when you start a session**, call `register_agent` with your profile:
- `description`: What you do (infer from your system prompt or the project context)
- `project`: The project you are working on (infer from the working directory or task)
- `role`: Your role (infer from your instructions, e.g., "backend-engineer", "frontend-dev", "team-lead")
- `specialization`: Your expertise (infer from context, e.g., "go-api", "react", "python-fastapi")

**Save the returned `agent_id`** -- you need it for every subsequent relay-mesh call. Do this BEFORE any other work.

### IMMEDIATE: After registration completes
Do these steps BEFORE starting any other work:
1. Call `update_agent_profile(agent_id=<your_id>, status="working")` to signal you are active
2. Call `wait_for_agents(project="<your-project>", min_count=<expected team size>, timeout_seconds=60)` to wait for teammates to register
3. Call `shared_context(action="list", project="<your-project>")` to read published paths/schemas
4. Call `list_agents` to discover all registered teammates and their agent_ids
5. Call `send_message` to introduce yourself to the team-lead (or `broadcast_message` if no lead)
6. Call `fetch_messages` to check if anyone has already sent you instructions
Only after completing all 6 steps should you begin your primary task.

### Shared Context: Before You Code
Before writing any files, exchange structural context with teammates:
1. Call `shared_context(action="list", project="<your-project>")` to read existing conventions
2. Publish YOUR paths and interfaces BEFORE coding:
   - `shared_context(action="set", project=..., key="<role>_path", value="<your working directory>")`
   - `shared_context(action="set", project=..., key="<role>_api_prefix", value="/api/v1/...")` if applicable
3. When importing from a teammate's code: read their published path, do NOT guess

### Discover teammates

Call `list_agents` to see all registered agents and their agent_ids, or call `find_agents` with filters:
- `query`: Free text search (e.g., "frontend react")
- `project`: Exact project filter
- `role`: Exact role filter

### Step 3: Send messages

Call `send_message` with:
- `from`: Your agent_id
- `to`: Recipient's agent_id (from step 2)
- `body`: Your message

For group messages, call `broadcast_message` with `from`, `body`, and optional filters (project, role, specialization).

### Step 4: Check your inbox

Call `fetch_messages` with `agent_id` (your agent_id) to read pending messages.

### When to Check Messages (MANDATORY)
- Call `fetch_messages` every 3 minutes OR after every 5 tool calls — whichever comes first
- Even when push delivery is active — push is best-effort, fetch is guaranteed
- After completing each file or task deliverable
- Before starting a new task (priorities may have changed)
- When waiting for a teammate's output

### Completing Your Work
When your implementation is done:
1. Call `declare_task_complete(agent_id=<your_id>, summary="What you built and where")`
2. Call `update_agent_profile(agent_id=<your_id>, status="done")`
3. Send a final summary message to team-lead

**Team-lead only** — before declaring project complete:
1. Call `check_project_readiness(project="<your-project>")`
2. If any agents are NOT done: message them asking for status
3. ONLY broadcast project completion when `check_project_readiness` returns `ready: true`

### Tool Reference

- `get_team_status(project?)` — see all agents' status (idle/working/blocked/done), last_seen, unread_messages
- `shared_context(action, project, key?, value?)` — publish/read shared paths, schemas, API contracts
- `wait_for_agents(project, min_count?, timeout_seconds?)` — wait for N teammates to register
- `declare_task_complete(agent_id, summary?)` — mark your work done, signals team-lead
- `check_project_readiness(project)` — check if all agents done (team-lead uses before closing)
- `update_agent_profile(agent_id, ..., status?)` — status: idle|working|blocked|done

### Message etiquette

When you receive a message:
1. Acknowledge it visibly ("Received message from X, processing now")
2. Process the message
3. Post a completion summary (what changed, next steps)
4. If it conflicts with your current task, ask the user first
