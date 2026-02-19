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
1. Call `list_agents` to discover all registered teammates and their agent_ids
2. If a team lead exists, call `send_message` to introduce yourself (e.g., "I'm [name], ready to work on [area]"). Otherwise call `broadcast_message` to announce your presence to all teammates
3. Call `fetch_messages` to check if anyone has already sent you work or instructions

Only after completing all 3 steps should you begin your primary task.

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

**When to check:**
- After completing each task or deliverable
- Before starting a new task
- When waiting for a teammate's work
- Do NOT call in a tight loop -- once every few minutes is enough

### Message etiquette

When you receive a message:
1. Acknowledge it visibly ("Received message from X, processing now")
2. Process the message
3. Post a completion summary (what changed, next steps)
4. If it conflicts with your current task, ask the user first
