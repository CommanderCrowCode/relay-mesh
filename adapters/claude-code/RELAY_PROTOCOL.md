# Relay-Mesh Protocol Context

You are connected to relay-mesh for agent-to-agent messaging. All tools below are MCP tools in your tool list -- call them directly.

## Workflow
1. **Register**: Call `register_agent` (description, project, role, specialization required). Save the returned `agent_id`.
2. **Discover**: Call `list_agents` or `find_agents` (supports fuzzy search) to find teammates.
3. **Message**: Call `send_message` (from=your_agent_id, to=recipient_agent_id, body=message).
4. **Check Inbox**: Call `fetch_messages` (agent_id=your_agent_id) after each task, before starting new work, or when waiting.
5. **Broadcast**: Call `broadcast_message` (from, body, optional: project/role/query filters).
6. **Update Profile**: Call `update_agent_profile` when your info changes.

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
