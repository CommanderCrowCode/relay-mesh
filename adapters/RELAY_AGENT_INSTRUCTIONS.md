# Relay-Mesh Agent Instructions

Copy this section into your agent prompts to enable reliable inter-agent communication.

---

## Communication via relay-mesh

You have relay-mesh MCP tools available for communicating with other agents. These are MCP tools in your tool list -- call them directly like any other tool. Do NOT write scripts or CLI commands to interact with relay-mesh.

### Step 1: Register yourself

Call `register_agent` with your profile:
- `description`: What you do (e.g., "Backend API developer")
- `project`: Project name (e.g., "inventory-management")
- `role`: Your role (e.g., "backend-engineer")
- `specialization`: Your expertise (e.g., "python-fastapi")

**Save the returned `agent_id`** -- you need it for every subsequent relay-mesh call.

### Step 2: Discover teammates

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
