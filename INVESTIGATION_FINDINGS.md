# Relay-Mesh Investigation Findings

Date: 2026-02-19
Sessions analyzed: 2 (morning 11:07, evening 17:11)
Log source: `~/.relay-mesh/relay-http.log`
OpenCode log: `~/.local/share/opencode/log/2026-02-19T101025.log`

## Summary

5 agents registered with relay-mesh but communication never flows. Agents register (often multiple times) then go silent. Zero meaningful message exchange in the 17:11 session.

---

## Finding 1: Duplicate Registrations

**What:** 17 registrations for 5 agents. Each agent registers 2-4 times, creating duplicate entries in the broker registry.

**Evidence:**
```
17:11:02-05  5x backend-engineer
17:11:07-09  4x devops-engineer
17:11:09     1x Agent4-Frontend
17:11:17     1x backend-engineer (again)
17:11:46-00  4x team-lead
17:12:48     1x Agent4-Frontend (again)
```

**Root cause:** No deduplication in the broker. Each `register_agent` call creates a new `agent_id`. When an agent re-registers (e.g., after context compaction or retry), it gets a new ID and the old one becomes orphan. Messages sent to the old ID are lost.

**Where to fix:**
- `cmd/server/main.go` — the `register_agent` MCP tool handler
- Look for `case "register_agent"` in the tool dispatch
- The broker's `Register()` method in the handler always creates a new agent

**Fix approach:**
- Deduplicate by `session_id`: if the same `session_id` registers again, update the existing agent profile and return the same `agent_id` instead of creating a new one
- The `session_id` is already auto-injected by the OpenCode plugin's `tool.execute.before` hook
- Add a `sessionToAgent map[string]string` lookup in the broker
- On register: if `session_id` exists in map, call `update_agent_profile` internally and return existing `agent_id`

---

## Finding 2: Communication Dies After Registration

**What:** After the registration burst (17:11:02-17:12:48), there are zero `send_message` calls, zero `fetch_messages` calls, and only 1 `broadcast_message` in the entire session. Agents register and then never communicate.

**Evidence:**
```
relay-http.log tool call counts (17:11 session):
  register_agent:     17
  broadcast_message:   1 (from the earlier 11:35 session only)
  send_message:        0
  fetch_messages:      0
  list_agents:         0 (post-registration)
```

**Root cause:** The auto-register instruction says "register as your FIRST action" but doesn't strongly enough drive the next step. Agents complete registration and consider that task done, then move on to their primary work (coding) without establishing communication channels.

**Where to investigate:**
- `adapters/RELAY_AGENT_INSTRUCTIONS.md` — the instructions file loaded via `opencode.json` `instructions` array
- `.opencode/plugins/relay-mesh-auto-bind.js` — the `PROTOCOL_CONTEXT` constant injected via `experimental.chat.system.transform`
- `COMMUNICATION_PROTOCOL.md` — reference protocol doc
- `adapters/claude-code/RELAY_PROTOCOL.md` — Claude Code variant

**Fix approach:**
- After the auto-register section, add an explicit "IMMEDIATE NEXT STEP" block:
  ```
  After registering:
  1. Call list_agents to discover all teammates
  2. Call send_message to introduce yourself to the team lead (or broadcast if no lead)
  3. Call fetch_messages to check if anyone has already sent you work
  ```
- Make this a numbered checklist, not prose — agents follow checklists better
- Consider: should the plugin auto-inject a follow-up prompt after registration that says "Now discover teammates and check messages"?

---

## Finding 3: Inconsistent Project Names

**What:** Agents register with different project names for the same project, making `find_agents(project=...)` useless.

**Evidence:**
```
"Small Business Inventory Management System"   (7 registrations)
"inventory-management"                          (5 registrations)
"SmallBusinessInventory"                        (2 registrations)
```

**Root cause:** The instructions say "infer from your context" for the project field. Each agent infers differently from the same project description. There's no canonical project name enforced.

**Where to investigate:**
- Agent prompt files at `~/playground/emergent_benchmark/opencode/agent*_prompt.md` — check if they specify a project name
- `~/playground/emergent_benchmark/opencode/INSTRUCTIONS.md` — the shared project spec

**Fix approaches (pick one or combine):**
1. **Normalize in broker:** When registering, normalize the project name (lowercase, strip spaces, truncate). This is a server-side fix in `cmd/server/main.go` register handler.
2. **Fuzzy match on discovery:** `find_agents` already uses Levenshtein distance for `query` — extend fuzzy matching to the `project` filter too. Currently `project` is an exact match.
3. **Require project name in agent prompts:** Update the benchmark agent prompts to include `project: "inventory-management"` explicitly. This is a user-side fix.
4. **Plugin-level normalization:** The `tool.execute.before` hook could normalize the project field before it hits the server.

**Recommended:** Option 2 (fuzzy project matching in `find_agents`) + Option 3 (explicit project name in prompts). Server-side normalization (Option 1) is too opinionated.

---

## Finding 4: All Agents Register as Same Role

**What:** Most agents register as `backend-engineer` regardless of their actual role in the agent prompts.

**Evidence:**
```
backend-engineer:   7 registrations
devops-engineer:    4 registrations
team-lead:          4 registrations
frontend-engineer:  2 registrations
```

The 5 agent prompts define: team-lead, backend, frontend, devops, and QA/testing. But the registrations skew heavily to backend-engineer.

**Root cause:** Same as Finding 3 — "infer from context" is too vague. The agent reads its system prompt, sees the project is about building a backend system, and registers as backend-engineer even if its prompt says "you are the frontend developer."

**Where to investigate:**
- Agent prompts at `~/playground/emergent_benchmark/opencode/agent*_prompt.md`
- Check if prompts explicitly state: "Your relay-mesh role is X"

**Fix approach:**
- In agent prompts, add explicit relay-mesh registration fields:
  ```
  When registering with relay-mesh, use these exact values:
  - project: "inventory-management"
  - role: "frontend-engineer"
  - name: "Agent4-Frontend"
  ```
- Alternatively, update `RELAY_AGENT_INSTRUCTIONS.md` to say "Use the role described in your system prompt, NOT the project domain"

---

## Finding 5: Old Binary Running

**What:** The relay-mesh server was not restarted after rebuilding. Log shows `body_len=190` format instead of full `body` content, confirming the old binary.

**Evidence:**
```
11:34:31 INFO message sent ... body_len=190    ← old format
```
The new binary logs `body="actual message content"`.

**Where to fix:**
- Kill and restart the relay-mesh process: `pkill relay-mesh && relay-mesh &`
- Or if running in a terminal, Ctrl+C and restart

---

## Finding 6: Doom Loop Triggers (58 total)

**What:** OpenCode's doom loop detection triggered 58 times during the session, across many tool types (not just relay-mesh).

**Evidence:**
```
doom_loop pattern counts:
  * (generic):                11
  uv:                          5
  relay-mesh_register_agent:   2
  (various others):           40
```

**Root cause:** The `permission: "allow"` config auto-allows doom loop checks rather than blocking them, but the sheer volume (58) suggests agents are repeatedly calling the same tools. The 2 relay-mesh doom loops are from the re-registration issue (Finding 1).

**Where to investigate:**
- OpenCode session log: `~/.local/share/opencode/log/2026-02-19T101025.log`
- Search for `doom_loop` with `action=ask` (ones that actually blocked) vs `action=allow` (ones that passed through)

---

## Priority Order for Fixes

1. **Dedup registrations by session_id** (Finding 1) — prevents orphan agents and duplicate entries. This is a server-side fix in `cmd/server/main.go`.

2. **Strengthen post-registration workflow** (Finding 2) — add explicit "after registering, do X Y Z" checklist in protocol context and instructions file. Update `RELAY_AGENT_INSTRUCTIONS.md` and `PROTOCOL_CONTEXT` in plugin.

3. **Fuzzy project matching in find_agents** (Finding 3) — extend the existing Levenshtein fuzzy matching from `query` to `project` filter in `find_agents` handler.

4. **Explicit registration values in agent prompts** (Finding 3 + 4) — update benchmark agent prompts with exact project/role/name values.

5. **Restart server with new binary** (Finding 5) — immediate action, no code change needed.

---

## Key Files Reference

| File | Purpose |
|------|---------|
| `cmd/server/main.go` | MCP tool handlers, broker, register/send/fetch logic |
| `.opencode/plugins/relay-mesh-auto-bind.js` | OpenCode plugin: session binding, protocol context injection |
| `adapters/RELAY_AGENT_INSTRUCTIONS.md` | Instructions loaded into OpenCode sessions via `instructions` config |
| `COMMUNICATION_PROTOCOL.md` | Protocol reference document |
| `adapters/claude-code/RELAY_PROTOCOL.md` | Claude Code protocol variant |
| `~/.config/opencode/opencode.json` | OpenCode global config (plugin, instructions, mcp) |
| `~/.relay-mesh/relay-http.log` | Relay-mesh server activity log |
| `~/.local/share/opencode/log/` | OpenCode session logs (sorted by date) |
| `~/playground/emergent_benchmark/opencode/agent*_prompt.md` | Agent prompt files for the benchmark |
| `~/playground/emergent_benchmark/opencode/INSTRUCTIONS.md` | Shared project specification |
