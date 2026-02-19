# Relay-Mesh Harness Compatibility Matrix

Version: 1.0.0
Date: 2026-02-18
Status: Research Complete

## Context

Relay-mesh exposes an MCP server (stdio or HTTP at `127.0.0.1:8080/mcp`) backed by NATS for agent-to-agent messaging. This document evaluates how each major AI coding harness can integrate with relay-mesh so that agents from different tools communicate transparently.

Relay-mesh integration requires three things from a harness:
1. **MCP client support** -- the harness can connect to relay-mesh as an MCP server
2. **Hook/plugin surface** -- the harness can auto-inject context (session binding, protocol rules) into tool calls
3. **Push capability** -- relay-mesh can deliver inbound messages into a running agent session without polling

---

## Compatibility Matrix

| Harness | MCP Support | Transports | Hooks / Plugins | Multi-Agent | Push Capable | Config Location |
|---|---|---|---|---|---|---|
| **Cursor** | Yes (native) | stdio, SSE, Streamable HTTP | Yes -- `hooks.json` with lifecycle events (`beforeMCPExecution`, `afterFileEdit`, `stop`) | Yes -- subagents, background agents, parallel workers | Partial -- background agents are remote; no local prompt injection API | `.cursor/mcp.json` (project) or global via Settings UI |
| **Windsurf** | Yes (native, Cascade) | stdio, SSE, Streamable HTTP | No dedicated hook system; MCP tool filtering only | Yes -- parallel Cascade sessions with Git worktrees (Wave 13) | No -- no external prompt injection API | `~/.codeium/windsurf/mcp_config.json` |
| **Aider** | Not yet native (PR #3672 pending) | N/A (community MCP wrappers use stdio) | No hooks system; Python scripting API (unofficial) | No native multi-agent; community MCP bridges exist | No -- CLI-driven, no daemon/push API | N/A (no native MCP config; community tools use Claude Desktop format) |
| **Continue.dev** | Yes (native, agent mode only) | stdio, SSE, Streamable HTTP | No lifecycle hooks; context providers and rules in config.yaml | Limited -- multiple CLI processes; no in-process orchestration | Partial -- CLI headless mode (`-p` flag) accepts piped input | `~/.continue/config.yaml` or `.continue/mcpServers/*.yaml` (workspace) |
| **Amazon Q Developer** | Yes (CLI + IDE) | stdio, SSE, Streamable HTTP | Yes -- agent hooks (`agentSpawn`, `userPromptSubmit`) in custom agent JSON | Yes -- CLI Agent Orchestrator (CAO) with tmux-isolated workers | No -- no external prompt injection; hooks fire on user prompt only | `~/.aws/amazonq/mcp.json` (global) or `.amazonq/mcp.json` (workspace) |
| **GitHub Copilot** | Yes (VS Code agent mode) | stdio, SSE (legacy), Streamable HTTP | Yes -- hooks at lifecycle events; custom agents with skills and subagents | Yes -- subagents via `#tool:runSubagent`, coding agent (async on GitHub Actions) | No -- no external prompt injection API into running chat session | `.vscode/mcp.json` (project) or VS Code settings |
| **Zed** | Yes (native) | stdio only (SSE/HTTP not yet supported) | No hooks; ACP delegates tool permissions to editor | Yes -- ACP supports multiple external agents (Claude, Gemini CLI, Codex) in parallel | No -- ACP sessions are editor-initiated only | `settings.json` under `context_servers` key |
| **JetBrains (Junie)** | Yes (native) | stdio only (HTTP planned) | No hooks; ACP support (beta, 2025.3+) delegates to IDE permissions | Limited -- single Junie agent per project; ACP allows external agents | No -- no external prompt injection API | `.junie/mcp/mcp.json` (project) or `~/.junie/mcp/mcp.json` (global) |

---

## Integration Strategies Per Harness

### 1. Cursor

Cursor is the strongest integration target after Claude Code itself. It supports all three MCP transports, so relay-mesh can be added as either a stdio process per workspace or a shared Streamable HTTP server at `127.0.0.1:8080/mcp`. The `hooks.json` system with `beforeMCPExecution` events allows building a plugin analogous to relay-mesh's existing OpenCode auto-bind plugin -- intercepting `register_agent` calls to inject session metadata and protocol context. The main gap is push delivery: Cursor has no public API to inject prompts into a running agent session. The workaround is polling-based (`fetch_messages` on a timer or triggered by a hook) or leveraging background agents which run remotely. For local use, a `beforeMCPExecution` hook could prepend a "check relay messages" step before each tool call.

### 2. Windsurf (Codeium)

Windsurf Cascade supports all three transports, making MCP connectivity straightforward -- add relay-mesh as a Streamable HTTP server in `mcp_config.json`. Wave 13's parallel Cascade sessions with Git worktrees are a natural fit for multi-agent relay-mesh workflows (one Cascade session per agent). The significant gap is the lack of hooks or a plugin system: there is no way to automatically inject session IDs or protocol context into `register_agent` calls. Registration would need to be manual or rely on relay-mesh's HTTP header-based session detection (`X-Session-Id`). Push delivery is also unsupported -- agents must poll with `fetch_messages`. Despite these gaps, the multi-pane Cascade dashboard is compelling for monitoring a relay-mesh swarm visually.

### 3. Aider

Aider currently has no native MCP support (issue #3314, PR #3672 pending). Integration today requires wrapping relay-mesh as a tool via Aider's unofficial Python scripting API or using community bridges like AiderDesk's MCP server. The scripting API (`Coder.create()` + `coder.run()`) enables programmatic message injection, which could serve as a push mechanism -- an external process calls `coder.run("fetch and process relay messages")`. However, this API is explicitly marked as unsupported and subject to breaking changes. The best near-term strategy is to wait for native MCP support to land, then configure relay-mesh as a stdio server. Until then, Aider integration is feasible but fragile.

### 4. Continue.dev

Continue supports MCP natively in agent mode with all three transports. Configuration is clean -- add relay-mesh as a stdio or streamable-http entry in `config.yaml` or a workspace YAML file. The CLI's headless mode (`-p` flag) enables scripted invocation, which could serve as a push entry point for relay-mesh message delivery. However, Continue lacks lifecycle hooks, so there is no way to auto-inject session binding or protocol context into tool calls. Agents would need explicit instructions in their `rules` configuration to register with relay-mesh on startup and poll for messages. The context provider system could potentially expose relay-mesh messages as `@relay` context, but this requires a custom provider implementation.

### 5. Amazon Q Developer

Amazon Q has strong MCP support across CLI and IDE with all three transports. The custom agent system with hooks (`agentSpawn` for one-time init, `userPromptSubmit` for per-prompt context) is a direct analog to relay-mesh's OpenCode plugin. An `agentSpawn` hook could call `register_agent` and inject protocol context at session start, while `userPromptSubmit` could inject latest relay messages as context before each interaction. The CLI Agent Orchestrator (CAO) with tmux-isolated workers maps naturally to relay-mesh multi-agent topologies. The gap is push delivery -- hooks only fire on user-initiated prompts, not on external events. Polling via `userPromptSubmit` hooks is the practical workaround. Configuration at `~/.aws/amazonq/mcp.json` supports global relay-mesh availability across all projects.

### 6. GitHub Copilot (VS Code Agent Mode)

Copilot's agent mode supports MCP with stdio and Streamable HTTP transports, configured in `.vscode/mcp.json`. The hooks system and custom agents with skills provide a rich extensibility surface -- a relay-mesh skill could handle registration, message polling, and protocol enforcement. Subagent support (`#tool:runSubagent` with git worktree isolation) enables multi-agent relay-mesh workflows where each subagent registers independently. The coding agent (async, GitHub Actions-based) could also use relay-mesh MCP servers for cross-agent coordination in CI. The main limitation is no push delivery into a running VS Code chat session. The mitigation is the same as Cursor: hook-triggered polling or a custom skill that checks relay messages at strategic points.

### 7. Zed

Zed supports MCP via stdio only -- no SSE or Streamable HTTP yet. This means relay-mesh must run as a local stdio process per Zed workspace, not as a shared HTTP server. The Agent Client Protocol (ACP) is Zed's primary extensibility mechanism, enabling external agents (Claude, Gemini CLI, Codex) to run inside the editor with MCP tool access. ACP passes MCP server endpoints to agents at session start, so relay-mesh tools would automatically be available to any ACP agent. The lack of hooks means no auto-injection of session binding. The interesting angle is that ACP agents already support MCP natively, so relay-mesh integration comes "for free" if the agent itself (e.g., Claude Code) already supports relay-mesh. Push is not supported -- ACP sessions are editor-initiated. Adding Streamable HTTP support to Zed would significantly improve the integration story.

### 8. JetBrains (Junie)

Junie supports MCP via stdio only, configured in `.junie/mcp/mcp.json`. The integration is straightforward -- add relay-mesh as a stdio server. JetBrains has adopted ACP (beta in 2025.3+), which enables external AI agents to operate within the IDE and access MCP tools. Like Zed, this means relay-mesh tools become available to any ACP-compatible agent running inside IntelliJ, PyCharm, etc. Junie itself is a single-agent system with no subagent or parallel execution model, so multi-agent relay-mesh workflows would require multiple IDE windows or ACP agents. No hooks or push delivery exist. The best strategy is stdio MCP for Junie direct use, and ACP for bringing relay-mesh to third-party agents inside JetBrains IDEs. HTTP transport support is planned but not yet available.

---

## Integration Priority Ranking

Based on MCP maturity, hook support, multi-agent capability, and ecosystem size:

| Priority | Harness | Rationale |
|---|---|---|
| 1 | **Cursor** | Full transport support, hooks for auto-bind, subagents, massive user base |
| 2 | **GitHub Copilot** | Full transport support, hooks + skills + subagents, largest ecosystem |
| 3 | **Amazon Q Developer** | Full transports, agent hooks map directly to relay-mesh patterns, CLI orchestrator |
| 4 | **Windsurf** | Full transports, parallel sessions, but no hooks for auto-bind |
| 5 | **Continue.dev** | Full transports, CLI headless mode, open-source (community plugins possible) |
| 6 | **Zed** | stdio only but ACP gives free MCP passthrough to external agents |
| 7 | **JetBrains (Junie)** | stdio only, ACP beta, but covers IntelliJ/PyCharm ecosystem |
| 8 | **Aider** | No native MCP yet; wait for PR #3672 to land |

---

## Transport Strategy

Relay-mesh currently supports two transport modes:

- **stdio**: One MCP server per session (current default)
- **HTTP**: Shared server at `127.0.0.1:8080/mcp` via Streamable HTTP

For maximum harness compatibility:

- **Cursor, Windsurf, Continue, Amazon Q, Copilot**: Use Streamable HTTP for shared mesh or stdio for per-session isolation
- **Zed, JetBrains**: Must use stdio until these editors add HTTP transport support
- **Aider**: Will use stdio once native MCP lands

The HTTP transport is preferable for multi-harness meshes because it allows agents from different tools to share a single relay-mesh broker without running separate server processes.

---

## Push Delivery Gap Analysis

Relay-mesh currently supports push delivery to OpenCode sessions via `POST /session/<id>/prompt_async`. This is the gold standard: when Agent A sends a message to Agent B, relay-mesh proactively injects the message into Agent B's session.

No other harness currently exposes an equivalent prompt injection API. The workarounds are:

| Strategy | Harnesses | Mechanism |
|---|---|---|
| **Hook-triggered polling** | Cursor, Copilot, Amazon Q | `beforeMCPExecution` / `userPromptSubmit` hook checks for new messages before each tool call |
| **Periodic fetch in rules** | Windsurf, Continue, Zed, JetBrains | Agent instructions include "poll `fetch_messages` every N interactions" |
| **External script push** | Aider, Continue CLI | Python API / headless CLI receives piped instructions from relay-mesh watcher |
| **MCP sampling/notifications** | All (future) | MCP spec includes server-initiated notifications; once harnesses implement this, relay-mesh can push natively |

The MCP specification's server-to-client notification mechanism (part of the Streamable HTTP transport) is the long-term solution. As harnesses adopt this, relay-mesh can push messages to any connected client without harness-specific workarounds.

---

## Next Steps

1. Build Cursor `hooks.json` auto-bind plugin (analogous to OpenCode plugin)
2. Create `.vscode/mcp.json` template for GitHub Copilot integration
3. Create `~/.aws/amazonq/mcp.json` template for Amazon Q integration
4. Test Streamable HTTP transport with Windsurf and Continue.dev
5. Monitor Aider PR #3672 for native MCP support
6. Test ACP passthrough with Zed and JetBrains once stdio is configured
7. Investigate MCP server-initiated notifications for universal push delivery
