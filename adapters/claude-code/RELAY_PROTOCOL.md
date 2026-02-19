# Relay-Mesh Protocol Context

You are connected to relay-mesh for agent-to-agent messaging. All tools below are MCP tools in your tool list -- call them directly.

## AUTOMATIC: Register on session start
**As your FIRST action**, call `register_agent` with description, project, role, specialization (infer from your context). Save the returned `agent_id`. Do this BEFORE any other work.

## IMMEDIATE: After registration completes

Do these steps BEFORE starting any other work:
1. Call `list_agents` to discover all registered teammates and their agent_ids
2. If a team lead exists, call `send_message` to introduce yourself (e.g., "I'm [name], ready to work on [area]"). Otherwise call `broadcast_message` to announce your presence to all teammates
3. Call `fetch_messages` to check if anyone has already sent you work or instructions

Only after completing all 3 steps should you begin your primary task.

## Workflow (after registration)
1. **Discover**: Call `list_agents` or `find_agents` (supports fuzzy search) to find teammates.
2. **Message**: Call `send_message` (from=your_agent_id, to=recipient_agent_id, body=message).
3. **Check Inbox**: Call `fetch_messages` (agent_id=your_agent_id) after each task, before starting new work, or when waiting.
4. **Broadcast**: Call `broadcast_message` (from, body, optional: project/role/query filters).
5. **Update Profile**: Call `update_agent_profile` when your info changes.

## When to Check Messages
- After completing each task or deliverable
- Before starting a new task
- When waiting for a teammate
- NOT in a tight loop -- once every few minutes

## Message Etiquette
1. When you receive a message, acknowledge it visibly before acting
2. After processing, post a completion summary (what changed, next steps)
3. Never process relay messages silently
4. If a message conflicts with your current task, ask the user first
