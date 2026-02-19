# Product Requirements Document: Multi-Harness Agent Compatibility

Version: 2.0.0
Date: 2026-02-18
Author: PRD-engineer Agent
Status: Draft
Location: /home/tanwa/relay-mesh/PRD.md

---

## Executive Summary

relay-mesh is a Go-based MCP server that enables anonymous agent-to-agent messaging over NATS. Today it works exclusively with OpenCode through a tightly coupled plugin system, push delivery API, and session resolver. This PRD defines the work required to make relay-mesh compatible with all major AI coding agent harnesses -- starting with Claude Code (priority 1) and OpenAI Codex CLI (priority 2) -- without breaking the existing OpenCode integration.

The core insight is that MCP itself is the universal integration surface: every harness already speaks MCP over stdio or HTTP. The differences lie in three supplemental capabilities: (1) how a harness provides session identity for binding, (2) how protocol context is injected and maintained, and (3) how incoming messages are delivered to an idle agent. This PRD defines a harness adapter architecture that encapsulates these per-harness differences behind a clean interface, keeping the broker and MCP tool surface unchanged.

The expected outcome is that two agents running in different harnesses (e.g., one in Claude Code, one in Codex CLI) can register on the same relay-mesh broker, discover each other, and exchange messages transparently.

---

## Problem Statement

### User Problems

1. **Single-harness lock-in.** relay-mesh only works with OpenCode. Developers using Claude Code, Codex CLI, Cursor, or other tools cannot participate in agent meshes without switching to OpenCode.
2. **No cross-harness communication.** Even if an MCP connection is established from another harness, there is no mechanism for session binding, protocol context injection, or push delivery outside OpenCode.
3. **Manual ceremony per harness.** Each new harness integration requires the user to manually configure MCP connections, understand session binding, and remember protocol rules. There is no `relay-mesh install-<harness>` equivalent for anything other than OpenCode.
4. **Push delivery is OpenCode-only.** The `opencodepush` package (`internal/opencodepush/pusher.go:16-133`) is hardcoded to the OpenCode HTTP API (`POST /session/{id}/prompt_async`). No other harness benefits from proactive message delivery.

### Business Impact

1. **Adoption ceiling.** relay-mesh cannot grow beyond the OpenCode user base, which is a small fraction of the AI coding agent ecosystem.
2. **Cross-tool collaboration blocked.** The primary value proposition of agent-to-agent messaging -- heterogeneous teams of agents from different tools working together -- is unreachable.
3. **Competitive gap.** Claude Code Agent Teams (cs50victor/claude-code-teams-mcp) already provides Claude Code agent coordination. Without relay-mesh compatibility, Claude Code users have no reason to use relay-mesh.

---

## Codebase Investigation Results

### Architecture Findings

- **Pattern**: Single-binary Go server with CLI subcommand dispatch (`cmd/server/main.go:35-66`)
- **Entry Points**: `runServer()` (line 68), `meshUp()` (line 114), `meshDown()` (line 130), `installOpenCodePlugin()` (line 387)
- **Core Modules**:
  - `internal/broker/broker.go` (760 lines) -- agent registry, NATS pub/sub, fuzzy discovery
  - `internal/opencodepush/pusher.go` (133 lines) -- HTTP push to OpenCode sessions
  - `internal/opencodepush/session_resolver.go` (112 lines) -- auto-bind via OpenCode session API
  - `.opencode/plugins/relay-mesh-auto-bind.js` (108 lines) -- OpenCode plugin for hook-based session injection

### Existing Related Features

- **OpenCode Plugin** (`.opencode/plugins/relay-mesh-auto-bind.js:1-108`): Demonstrates the full adapter pattern -- session binding injection (`tool.execute.before`, line 63), protocol context injection (`tool.execute.after`, line 80), compaction survival (`experimental.session.compacting`, line 94), and post-compaction reinject (`event`, line 101).
- **Session Binding Priority** (`cmd/server/main.go:573-590`): Already supports multiple binding sources: explicit `session_id` parameter, HTTP headers (`X-Opencode-Session-Id` etc.), and auto-resolve from OpenCode session API. This pattern extends naturally to per-harness detection.
- **CLI Install Command** (`cmd/server/main.go:387-455`): `install-opencode-plugin` reads/writes OpenCode config JSON and inserts plugin path. This is the template for `install-claude-code` and `install-codex`.
- **Orchestration Commands** (`cmd/server/main.go:114-141`): `mesh-up`/`mesh-down` manage NATS + OpenCode + relay HTTP. New harness adapters need equivalent orchestration.

### Technical Landscape

- **Tech Stack**: Go 1.25, NATS 2.11 (JetStream), mcp-go v0.40.0
- **Data Layer**: In-memory agent registry (`broker.agents map[string]*agentState`), NATS JetStream stream `RELAY_MESSAGES` for durable history (7-day retention)
- **API Structure**: MCP tools over stdio or Streamable HTTP. 10 tools registered in `buildMCPServer()` (`cmd/server/main.go:457-553`)
- **Testing Infrastructure**: Embedded NATS server per test (`broker_test.go:23-50`), httptest for push tests (`pusher_test.go:14-86`), `waitForQueuedMessages` polling helper (`broker_test.go:68-88`)

### Discovered Constraints

- **Constitution Article II** (`CONSTITUTION.md:19-22`): Go is the required implementation language. Harness-side hook scripts (bash, JS) are outside the Go core but must be generated/managed by Go CLI commands.
- **Constitution Article I** (`CONSTITUTION.md:12-15`): POC scope. No auth, no persistence, no governance. Multi-harness support stays within this boundary.
- **Single `main.go`** (`cmd/server/main.go`, 853 lines): All MCP handlers, CLI dispatch, and orchestration in one file. Adding 2-3 more install commands will push this past maintainability limits. Consider extracting CLI commands into `cmd/server/cli_*.go` files.
- **OpenCode coupling** (`internal/opencodepush/`): Package name, types, and logic are OpenCode-specific. The push and session resolver interfaces need abstraction for multi-harness support.

---

## Proposed Solution

### Overview

Introduce a three-layer adapter architecture that keeps the existing broker and MCP tool surface untouched while adding per-harness integration logic:

```
+--------------------------------------------------------------+
|                    Harness Adapters Layer                     |
|  +----------+ +------------+ +----------+ +----------------+ |
|  | OpenCode | | Claude     | | Codex    | | Generic/       | |
|  | (exists) | | Code       | | CLI      | | MCP-only       | |
|  +----+-----+ +-----+------+ +----+-----+ +-------+--------+ |
|       |             |             |               |           |
+-------+-------------+-------------+---------------+-----------+
|              relay-mesh MCP Server (unchanged)                |
|  register_agent | send_message | fetch_messages               |
|  find_agents | broadcast | bind_session                       |
+---------------------------------------------------------------+
|              NATS JetStream Transport (unchanged)             |
|  relay.agent.<id> subjects | RELAY_MESSAGES stream            |
+---------------------------------------------------------------+
```

Each adapter is responsible for:
1. **Session binding**: How to detect and inject the harness session ID
2. **Protocol context**: How to inject and maintain the communication protocol in the agent's context
3. **Push delivery**: How to notify an idle agent of incoming messages
4. **Installation**: CLI command to configure the harness for relay-mesh

### User Stories

1. As a Claude Code user, I want to run `relay-mesh install-claude-code` and have my Claude Code hooks configured so that my agent automatically registers with session binding and receives messages.
2. As a Codex CLI user, I want to run `relay-mesh install-codex` and have my Codex skill and MCP config set up so that my agent can participate in the mesh.
3. As a developer running agents across multiple harnesses, I want agents from Claude Code and OpenCode to discover each other through `find_agents` and exchange messages transparently.
4. As a relay-mesh operator, I want `relay-mesh mesh-up` to detect which harnesses are available and start the appropriate services without manual configuration.
5. As a Claude Code user, I want to be notified via desktop notification when a message arrives for my agent, even if my session is idle.

### Success Criteria

- [ ] Claude Code agent can register, send, and receive messages via relay-mesh MCP tools
- [ ] Claude Code hooks auto-inject session_id on register_agent
- [ ] Claude Code Stop hook polls for pending messages and resumes session if messages waiting
- [ ] Codex CLI agent can register, send, and receive messages via relay-mesh MCP tools
- [ ] Codex CLI auto-detects CODEX_THREAD_ID for session binding
- [ ] Two agents in different harnesses (e.g., Claude Code + OpenCode) can exchange messages
- [ ] `relay-mesh install-claude-code` configures .claude/hooks/ and .mcp.json correctly
- [ ] `relay-mesh install-codex` configures config.toml MCP entry and creates skill files
- [ ] Push delivery abstraction supports OpenCode (prompt_async), Claude Code (Stop hook), and Codex (polling)
- [ ] All existing tests pass without modification
- [ ] New adapter packages have test coverage for core paths

---

## Technical Specification

### Implementation Approach

Following existing patterns discovered in the codebase investigation.

#### Consistency with Existing Code

- **Follow pattern found in**: `cmd/server/main.go:387-455` (`installOpenCodePlugin` -- read config, inject entry, write back)
- **Reuse component from**: `internal/opencodepush/pusher.go:16-36` (Pusher struct with enabled/disabled pattern)
- **Extend existing module**: `cmd/server/main.go:35-66` (CLI subcommand dispatch)

### Architecture Changes

```
internal/
  broker/                    (unchanged)
    broker.go
    broker_test.go
  push/                      (NEW -- replaces opencodepush)
    push.go                  - Adapter interface + registry
    opencode.go              - OpenCode adapter (extracted from opencodepush/)
    opencode_test.go
    claudecode.go            - Claude Code adapter (Stop hook polling, notify-send)
    claudecode_test.go
    codex.go                 - Codex CLI adapter (polling fallback)
    codex_test.go
    generic.go               - Generic MCP-only adapter (fetch_messages polling)
  opencodepush/              (deprecated, forwarding to push/opencode.go)
    pusher.go                - Thin wrapper for backward compat during transition
    session_resolver.go      - Moved to push/opencode.go

cmd/server/
  main.go                    - Add install-claude-code, install-codex subcommands
                             - Replace opencodepush with push adapter dispatch

adapters/                    (NEW -- harness-side configuration files)
  claude-code/
    hooks/
      preToolUse.sh          - Injects session_id into register_agent
      postToolUse.sh         - Injects protocol context after registration
      stop.sh                - Polls fetch_messages, exit 2 if messages waiting
      notification.sh        - Desktop notification on message delivery
    mcp.json.tmpl            - Template for .mcp.json generation
    RELAY_PROTOCOL.md        - Protocol context for Claude Code (injected into hooks)
  codex/
    SKILL.md                 - Codex skill definition
    openai.yaml              - Codex skill metadata
    AGENTS.md.snippet        - Protocol instructions for AGENTS.md
    config.toml.tmpl         - Template for MCP config entry
```

### New Components

#### 1. Push Adapter Interface (`internal/push/push.go`)

- Location: `/home/tanwa/relay-mesh/internal/push/push.go`
- Pattern: Following existing Pusher pattern in `internal/opencodepush/pusher.go:16-36`
- Purpose: Define a common interface for per-harness push delivery

```go
package push

import "github.com/tanwa/relay-mesh/internal/broker"

// Adapter delivers messages to a harness-specific agent session.
type Adapter interface {
    // Name returns the adapter identifier (e.g., "opencode", "claude-code", "codex").
    Name() string
    // Enabled reports whether this adapter is active.
    Enabled() bool
    // Push delivers a message to the agent's bound session.
    Push(sessionID, targetAgentID string, msg broker.Message) error
}

// Registry holds active push adapters keyed by harness name.
type Registry struct {
    adapters map[string]Adapter
}

func NewRegistry() *Registry { ... }
func (r *Registry) Register(a Adapter) { ... }
func (r *Registry) Push(harness, sessionID, agentID string, msg broker.Message) error { ... }
func (r *Registry) PushAll(sessionID, agentID string, msg broker.Message) error { ... }
```

- Dependencies: `internal/broker` (Message type only)

#### 2. OpenCode Adapter (`internal/push/opencode.go`)

- Location: `/home/tanwa/relay-mesh/internal/push/opencode.go`
- Pattern: Direct extraction from `internal/opencodepush/pusher.go:16-133` and `session_resolver.go:1-112`
- Purpose: Existing OpenCode push + session resolution, conforming to Adapter interface

#### 3. Claude Code Adapter (`internal/push/claudecode.go`)

- Location: `/home/tanwa/relay-mesh/internal/push/claudecode.go`
- Pattern: Following Pusher enabled/disabled pattern from `internal/opencodepush/pusher.go:23-36`
- Purpose: Claude Code message delivery via desktop notification (notify-send/osascript) and pending message state file for Stop hook consumption

```go
type ClaudeCodeAdapter struct {
    stateDir string  // ~/.relay-mesh/claude-code/
    enabled  bool
}

// Push writes pending message to state file and sends desktop notification.
// The Stop hook script reads this state file and returns exit 2 if messages pending.
func (a *ClaudeCodeAdapter) Push(sessionID, agentID string, msg broker.Message) error { ... }
```

- Dependencies: OS notification command, filesystem for state

#### 4. Codex CLI Adapter (`internal/push/codex.go`)

- Location: `/home/tanwa/relay-mesh/internal/push/codex.go`
- Pattern: Following Pusher enabled/disabled pattern
- Purpose: Codex has no inbound hooks or push API. This adapter writes pending message state for external polling and optionally sends desktop notification.

#### 5. Claude Code Hook Scripts (`adapters/claude-code/hooks/`)

Claude Code hooks receive JSON on stdin and can modify behavior. These are bash scripts generated by `install-claude-code`.

**preToolUse.sh** -- Session binding injection:
```bash
#!/usr/bin/env bash
# Claude Code PreToolUse hook for relay-mesh
# Reads hook JSON from stdin, injects session_id into register_agent calls
INPUT="$(cat)"
TOOL_NAME="$(echo "$INPUT" | jq -r '.tool_name // ""')"
if [[ "$TOOL_NAME" != *"register_agent"* ]]; then
  exit 0  # Pass through, no modification
fi
SESSION_ID="$(echo "$INPUT" | jq -r '.session_id // ""')"
if [[ -z "$SESSION_ID" ]]; then
  exit 0
fi
# Inject session_id into tool input if not already present
EXISTING="$(echo "$INPUT" | jq -r '.tool_input.session_id // ""')"
if [[ -z "$EXISTING" ]]; then
  echo "$INPUT" | jq --arg sid "$SESSION_ID" '.tool_input.session_id = $sid'
else
  exit 0  # Already has session_id
fi
```

**stop.sh** -- Pending message check (pseudo-push):
```bash
#!/usr/bin/env bash
# Claude Code Stop hook for relay-mesh
# Checks for pending messages and returns exit 2 to continue session
STATE_FILE="$HOME/.relay-mesh/claude-code/pending-messages.json"
if [[ -f "$STATE_FILE" ]] && [[ -s "$STATE_FILE" ]]; then
  MESSAGES="$(cat "$STATE_FILE")"
  # Clear the file after reading
  > "$STATE_FILE"
  echo "You have pending relay-mesh messages. Please call fetch_messages to retrieve them."
  exit 2  # Exit code 2 tells Claude Code to continue instead of stopping
fi
exit 0
```

**postToolUse.sh** -- Protocol context injection:
```bash
#!/usr/bin/env bash
# Claude Code PostToolUse hook for relay-mesh
# Injects protocol context after successful register_agent
INPUT="$(cat)"
TOOL_NAME="$(echo "$INPUT" | jq -r '.tool_name // ""')"
if [[ "$TOOL_NAME" != *"register_agent"* ]]; then
  exit 0
fi
# Check if registration succeeded (output contains agent_id)
TOOL_OUTPUT="$(echo "$INPUT" | jq -r '.tool_output // ""')"
if echo "$TOOL_OUTPUT" | jq -e '.agent_id' >/dev/null 2>&1; then
  cat "$HOME/.relay-mesh/claude-code/RELAY_PROTOCOL.md"
fi
exit 0
```

#### 6. Install Commands

**`install-claude-code`** (`cmd/server/main.go`):
- Creates `<project>/.mcp.json` with relay-mesh MCP server entry (stdio or HTTP)
- Creates `.claude/hooks/` directory with preToolUse.sh, postToolUse.sh, stop.sh
- Creates `~/.relay-mesh/claude-code/RELAY_PROTOCOL.md` with protocol context
- Creates `.claude/settings.json` hook entries if not present

**`install-codex`** (`cmd/server/main.go`):
- Creates `~/.codex/config.toml` MCP server entry (or updates existing)
- Creates Codex skill files (SKILL.md + openai.yaml) in `~/.codex/skills/relay-mesh/`
- Creates AGENTS.md snippet for protocol instructions

### Modified Components

#### 1. CLI Dispatch (`cmd/server/main.go:35-66`)
- **Current Purpose**: Routes subcommands to serve, version, install-opencode-plugin, mesh-up, mesh-down
- **Required Changes**: Add `install-claude-code`, `install-codex`, and optional `detect-harness` subcommands
- **Impact Analysis**: Pure addition to switch statement. No breaking changes.

#### 2. Push Delivery in Send Handler (`cmd/server/main.go:646-669`)
- **Current Purpose**: After `b.Send()`, pushes to OpenCode if session bound
- **Required Changes**: Replace `pusher.Push()` with `pushRegistry.PushAll()` or harness-specific dispatch based on binding metadata
- **Impact Analysis**: The broker needs to store which harness type a session binding belongs to, or the push registry tries all enabled adapters.

#### 3. Session Binding Metadata (`internal/broker/broker.go:47-53`)
- **Current Purpose**: `agentState` stores `SessionID` as a plain string
- **Required Changes**: Add `Harness string` field to `agentState` to record which harness bound the session. This allows the push registry to dispatch to the correct adapter.
- **Impact Analysis**: Internal struct change. No external API change since `bind_session` already accepts arbitrary session IDs.

#### 4. Register Handler Session Detection (`cmd/server/main.go:556-594`)
- **Current Purpose**: Detects session ID from input, headers, or OpenCode session resolver
- **Required Changes**: Add harness auto-detection. Check environment variables:
  - `CLAUDE_CODE` or presence of `.claude/` directory for Claude Code
  - `CODEX_THREAD_ID` env var for Codex CLI
  - Falls back to OpenCode resolver if available
- **Impact Analysis**: Additional detection logic before the existing OpenCode fallback. No breaking changes.

### Data Models

```go
// In internal/broker/broker.go -- extend agentState
type agentState struct {
    ID        string
    Profile   AgentProfile
    Subject   string
    SessionID string
    Harness   string    // NEW: "opencode", "claude-code", "codex", "generic"
    Queue     []Message
}
```

No new database schemas (in-memory only per Constitution).

### API Specifications

#### Existing MCP Tools (no changes to external contract)

The `bind_session` tool gains an optional `harness` parameter:

```yaml
# Extension to existing bind_session tool
Tool: bind_session
Input:
  - agent_id: string, required, "Agent id to bind"
  - session_id: string, optional, "Session id (auto-detected if omitted)"
  - harness: string, optional, "Harness type: opencode|claude-code|codex|generic (auto-detected if omitted)"
Response:
  - 200: { "agent_id": "...", "session_id": "...", "harness": "..." }
```

#### New CLI Commands

```
relay-mesh install-claude-code [--project-dir=<path>] [--transport=stdio|http] [--http-url=<url>]
relay-mesh install-codex [--global] [--transport=stdio|http] [--http-url=<url>]
relay-mesh detect-harness
```

---

## Implementation Plan and Progress Tracking

**This section is a living document. Update task status as work progresses.**

Status key: pending | in-progress | complete | blocked

### Phase 1: Claude Code Adapter (Priority 1, Week 1-2)

- [ ] pending -- Task 1.1: Create `internal/push/push.go` with Adapter interface and Registry
  - Status: Not started
  - Assigned to: TBD
  - Notes: Follow pattern from `internal/opencodepush/pusher.go:16-36`
  - Reference: `internal/opencodepush/pusher.go:38-40` (Enabled pattern)

- [ ] pending -- Task 1.2: Extract OpenCode adapter to `internal/push/opencode.go`
  - Status: Not started
  - Assigned to: TBD
  - Notes: Move all logic from `internal/opencodepush/pusher.go` and `session_resolver.go`. Keep `internal/opencodepush/` as deprecated forwarding wrapper for one release cycle.
  - Reference: `internal/opencodepush/pusher.go:1-133`, `session_resolver.go:1-112`

- [ ] pending -- Task 1.3: Implement `internal/push/claudecode.go`
  - Status: Not started
  - Assigned to: TBD
  - Notes: Desktop notification via `notify-send` (Linux) or `osascript` (macOS). State file at `~/.relay-mesh/claude-code/pending-messages.json` for Stop hook consumption.
  - Reference: `internal/opencodepush/pusher.go:42-90` (Push method pattern)

- [ ] pending -- Task 1.4: Add `Harness` field to `agentState` in broker
  - Status: Not started
  - Assigned to: TBD
  - Notes: Internal field only. Auto-detected on bind or passed explicitly. Update `BindSession` signature.
  - Reference: `internal/broker/broker.go:47-53`

- [ ] pending -- Task 1.5: Wire push.Registry into `cmd/server/main.go`
  - Status: Not started
  - Assigned to: TBD
  - Notes: Replace direct `pusher.Push()` calls (lines 659-664, 742-747) with registry dispatch. Detect harness from binding metadata.
  - Reference: `cmd/server/main.go:646-669` (sendHandler), `cmd/server/main.go:715-752` (broadcastHandler)

- [ ] pending -- Task 1.6: Create Claude Code hook scripts in `adapters/claude-code/hooks/`
  - Status: Not started
  - Assigned to: TBD
  - Notes: preToolUse.sh (session injection), postToolUse.sh (protocol context), stop.sh (pending message poll), notification.sh (notify-send wrapper). All scripts read JSON from stdin per Claude Code hook protocol.
  - Reference: `.opencode/plugins/relay-mesh-auto-bind.js:63-78` (equivalent OpenCode logic)

- [ ] pending -- Task 1.7: Create RELAY_PROTOCOL.md for Claude Code context injection
  - Status: Not started
  - Assigned to: TBD
  - Notes: Equivalent to `PROTOCOL_CONTEXT` constant in `.opencode/plugins/relay-mesh-auto-bind.js:3-21`. Markdown format for file-based injection.
  - Reference: `COMMUNICATION_PROTOCOL.md:1-54`

- [ ] pending -- Task 1.8: Implement `install-claude-code` CLI command
  - Status: Not started
  - Assigned to: TBD
  - Notes: Follow `installOpenCodePlugin` pattern. Generate `.mcp.json`, `.claude/hooks/*`, `~/.relay-mesh/claude-code/RELAY_PROTOCOL.md`. Handle existing config gracefully (idempotent).
  - Reference: `cmd/server/main.go:387-455` (installOpenCodePlugin)

- [ ] pending -- Task 1.9: Add harness auto-detection to register handler
  - Status: Not started
  - Assigned to: TBD
  - Notes: Check for `CLAUDE_CODE` env var, `session_id` format patterns, or explicit `harness` parameter. Insert before OpenCode resolver fallback.
  - Reference: `cmd/server/main.go:556-594` (registerHandler)

- [ ] pending -- Task 1.10: Write tests for Claude Code adapter
  - Status: Not started
  - Assigned to: TBD
  - Notes: Test push (state file write + notification command invocation), test hook scripts (mock stdin JSON), test install command (temp dir config generation).
  - Reference: `internal/opencodepush/pusher_test.go:1-86` (test patterns), `internal/broker/broker_test.go:23-50` (embedded NATS pattern)

- [ ] pending -- Task 1.11: E2E validation -- Claude Code + OpenCode cross-harness messaging
  - Status: Not started
  - Assigned to: TBD
  - Notes: Register agent in Claude Code via hooks, register agent in OpenCode via plugin, exchange messages bidirectionally.
  - Reference: `REAL_WORLD_E2E_TESTS.md:1-306` (scenario patterns)

### Phase 2: Codex CLI Adapter (Priority 2, Week 2-3)

- [ ] pending -- Task 2.1: Implement `internal/push/codex.go`
  - Status: Not started
  - Assigned to: TBD
  - Notes: Codex CLI has NO inbound hooks (rejected as NOT_PLANNED). Adapter writes state file for polling. Desktop notification via `notify-send`/`osascript`.
  - Reference: `internal/push/claudecode.go` (similar state-file pattern)

- [ ] pending -- Task 2.2: Implement `CODEX_THREAD_ID` env var detection for auto-bind
  - Status: Not started
  - Assigned to: TBD
  - Notes: Codex CLI exposes `CODEX_THREAD_ID` to child processes (including MCP server subprocess in stdio mode). Detect in register handler.
  - Reference: `cmd/server/main.go:556-594` (registerHandler session detection)

- [ ] pending -- Task 2.3: Create Codex skill files
  - Status: Not started
  - Assigned to: TBD
  - Notes: SKILL.md defines relay-mesh protocol instructions. openai.yaml defines skill metadata. These go in `adapters/codex/` as templates and `~/.codex/skills/relay-mesh/` on install.

- [ ] pending -- Task 2.4: Create AGENTS.md protocol snippet
  - Status: Not started
  - Assigned to: TBD
  - Notes: Codex reads AGENTS.md for protocol context. Provide a snippet that users append to their project AGENTS.md.

- [ ] pending -- Task 2.5: Implement `install-codex` CLI command
  - Status: Not started
  - Assigned to: TBD
  - Notes: Updates `~/.codex/config.toml` with MCP server entry. Copies skill files. Idempotent.
  - Reference: `cmd/server/main.go:387-455` (installOpenCodePlugin pattern)

- [ ] pending -- Task 2.6: Write tests for Codex adapter
  - Status: Not started
  - Assigned to: TBD
  - Notes: Test CODEX_THREAD_ID detection, config.toml generation, skill file creation.

- [ ] pending -- Task 2.7: E2E validation -- Codex + OpenCode cross-harness messaging
  - Status: Not started
  - Assigned to: TBD
  - Notes: Register agent in Codex via skill, register agent in OpenCode, exchange messages.

### Phase 3: Push Layer Generalization (Week 3-4)

- [ ] pending -- Task 3.1: Refactor `cmd/server/main.go` to use push.Registry exclusively
  - Status: Not started
  - Assigned to: TBD
  - Notes: Remove all direct `opencodepush.Pusher` and `opencodepush.SessionResolver` references from main.go. All push goes through registry.
  - Reference: `cmd/server/main.go:77-86` (pusher/resolver creation)

- [ ] pending -- Task 3.2: Remove deprecated `internal/opencodepush/` package
  - Status: Not started
  - Assigned to: TBD
  - Notes: After phase 1 and 2 complete, remove the forwarding wrapper. All logic lives in `internal/push/opencode.go`.

- [ ] pending -- Task 3.3: Implement generic MCP-only adapter (`internal/push/generic.go`)
  - Status: Not started
  - Assigned to: TBD
  - Notes: For harnesses with no hooks or push API (Windsurf, Zed, JetBrains). Desktop notification only. Agent must manually call `fetch_messages`.

- [ ] pending -- Task 3.4: Update `mesh-up` to detect available harnesses
  - Status: Not started
  - Assigned to: TBD
  - Notes: `mesh-up` currently starts NATS + OpenCode server + relay HTTP (`cmd/server/main.go:114-128`). Add harness detection: if OpenCode is available, start OpenCode server; if not, skip. Always start NATS + relay HTTP.
  - Reference: `cmd/server/main.go:114-128` (meshUp), `cmd/server/main.go:172-188` (ensureOpenCode)

- [ ] pending -- Task 3.5: Extract CLI commands into separate files
  - Status: Not started
  - Assigned to: TBD
  - Notes: `cmd/server/main.go` is 853 lines and growing. Extract install commands, mesh orchestration, and helpers into `cmd/server/cli_install.go`, `cmd/server/cli_mesh.go`, `cmd/server/helpers.go`.

- [ ] pending -- Task 3.6: Documentation -- update README.md, GETTING_STARTED.md, CLAUDE.md
  - Status: Not started
  - Assigned to: TBD
  - Notes: Add Claude Code and Codex sections. Update architecture diagram. Add cross-harness E2E scenario.

### Phase 4: Broader Harness Compatibility (Future, Week 5+)

- [ ] pending -- Task 4.1: Cursor adapter (.cursor/mcp.json + hooks.json)
  - Status: Not started
  - Notes: Cursor has `beforeMCPExecution` hook analogous to Claude Code PreToolUse. Priority per `HARNESS_COMPATIBILITY.md:71-83`.

- [ ] pending -- Task 4.2: GitHub Copilot adapter (.vscode/mcp.json)
  - Status: Not started
  - Notes: Copilot supports hooks + skills + subagents.

- [ ] pending -- Task 4.3: Amazon Q adapter (~/.aws/amazonq/mcp.json)
  - Status: Not started
  - Notes: Agent hooks (`agentSpawn`, `userPromptSubmit`) map directly to relay-mesh patterns.

- [ ] pending -- Task 4.4: MCP server-initiated notifications (long-term universal push)
  - Status: Not started
  - Notes: MCP spec includes server-to-client notifications via Streamable HTTP. Once harnesses implement this, relay-mesh can push natively to any connected client.

### Overall Progress

- **Completed Tasks**: 0/22 (0%)
- **In Progress**: 0
- **Blocked**: 0
- **Last Updated**: 2026-02-18 by PRD-engineer Agent

---

## Testing Strategy

### Test Patterns to Follow

Based on investigation of existing tests:

- **Unit Test Pattern**: Found in `internal/broker/broker_test.go:90-159` -- embedded NATS, register/send/fetch, assertion on message fields
- **Integration Pattern**: Found in `internal/opencodepush/pusher_test.go:14-86` -- httptest server, capture request paths/bodies, verify HTTP contract
- **Mocking Strategy**: No mocking framework used. Tests use real embedded NATS and httptest. Continue this pattern for adapter tests.
- **Helper Pattern**: `newTestBroker(t)` at `broker_test.go:52-66`, `waitForQueuedMessages(t, ...)` at `broker_test.go:68-88`

### Coverage Requirements

- Current coverage: Broker core + OpenCode push delivery covered
- Target coverage: All new adapter Push methods, all install commands, all hook scripts
- Critical paths requiring tests:
  1. Push adapter interface dispatch (registry selects correct adapter)
  2. Claude Code state file write/read cycle
  3. Codex CODEX_THREAD_ID env detection
  4. Install commands (config file generation in temp dirs)
  5. Hook scripts (bash script testing via subprocess with mock stdin)

### New Test Files

| File | Tests |
|------|-------|
| `internal/push/push_test.go` | Registry dispatch, multi-adapter registration |
| `internal/push/opencode_test.go` | Extracted from `opencodepush/pusher_test.go` |
| `internal/push/claudecode_test.go` | State file write, notification command mock |
| `internal/push/codex_test.go` | CODEX_THREAD_ID detection, config generation |
| `cmd/server/install_test.go` | Install commands with temp dirs |

### QA Testing Requirements

The qa-tester agent must verify:
- All success criteria listed above
- All user stories have working end-to-end paths
- Cross-harness messaging works (agent A in harness X, agent B in harness Y)
- Install commands are idempotent (running twice does not corrupt config)
- Existing OpenCode integration is not regressed
- All MCP tools work identically regardless of connecting harness
- Desktop notifications fire on supported platforms

---

## Integration Points

### Existing Systems to Interface With

1. **NATS JetStream**
   - Current Integration: `internal/broker/broker.go:64-84` (connection), `broker.go:484-504` (stream setup)
   - Required Changes: None. Broker layer is harness-agnostic.

2. **OpenCode Server API**
   - Current Integration: `internal/opencodepush/pusher.go:42-90` (prompt_async), `session_resolver.go:50-101` (session list)
   - Required Changes: Move to `internal/push/opencode.go`. No API contract change.

3. **Claude Code Hooks System**
   - New Integration: Bash scripts in `.claude/hooks/` directory
   - Hook JSON stdin protocol: `{ "tool_name": "...", "tool_input": {...}, "session_id": "..." }`
   - Exit codes: 0 = pass, 1 = block, 2 = continue (for Stop hook)

4. **Codex CLI Config**
   - New Integration: `~/.codex/config.toml` for MCP server entry
   - TOML format: `[mcp_servers.relay-mesh]` section with `command` or `url` key
   - Skill directory: `~/.codex/skills/relay-mesh/`

5. **Desktop Notifications**
   - New Integration: `notify-send` (Linux), `osascript -e 'display notification'` (macOS)
   - Used by Claude Code and Codex adapters for incoming message alerts

---

## Risk Assessment

### Technical Risks

| Risk | Evidence | Probability | Impact | Mitigation |
|------|----------|-------------|--------|------------|
| Claude Code hook protocol changes | Claude Code is actively developed; hook stdin format not yet stable | Medium | High | Version-check hook scripts; pin to known Claude Code version in docs |
| Codex CLI lacks CODEX_THREAD_ID in all modes | Feature confirmed for CLI mode but untested in all configurations | Medium | Medium | Fall back to manual bind_session; document known working configurations |
| main.go grows beyond maintainability | Already 853 lines, adding 2 install commands + detection | High | Medium | Phase 3 Task 3.5 extracts into multiple files |
| Hook script portability (bash) | macOS vs Linux differences in jq, notify-send | Medium | Medium | Use POSIX-compatible bash; test on both platforms |
| OpenCode regression during push refactor | Moving opencodepush to push/ changes import paths | Low | High | Keep deprecated forwarding wrapper during transition; run full test suite |

### Identified Technical Debt

- **Single main.go**: `cmd/server/main.go` at 853 lines handles CLI dispatch, MCP tool registration, orchestration, and helpers. Needs split before adding more commands.
- **Hardcoded OpenCode references**: "OpenCode" appears in tool descriptions (`bind_session` description says "OpenCode session_id"), variable names, and comments throughout. Should be generalized to "harness session" in user-facing strings.
- **No harness field in binding**: `agentState.SessionID` is a plain string with no indication of which harness it belongs to. Push dispatch currently assumes OpenCode.

---

## Dependencies and Blockers

### Code Dependencies

- **Internal**: `internal/broker` (Message type, Broker API) -- unchanged
- **External**: `github.com/mark3labs/mcp-go v0.40.0` -- unchanged
- **External**: `github.com/nats-io/nats.go v1.48.0` -- unchanged
- **New External**: None required. Hook scripts use bash + jq (system tools). No new Go dependencies.

### Infrastructure Dependencies

- **NATS Server**: Docker container via `docker-compose.yml:1-7` -- unchanged
- **Claude Code**: Must be installed on developer machine. Hooks require Claude Code v1.0+ with hook support.
- **Codex CLI**: Must be installed on developer machine. Requires MCP support in config.toml.
- **jq**: Required by hook scripts for JSON parsing. Widely available system tool.

---

## Recommendations

### Refactoring Opportunities

1. **Split main.go**: Extract CLI install commands, mesh orchestration, and helpers into separate files within `cmd/server/`. This should happen in Phase 1 before adding new commands.
2. **Generalize OpenCode-specific language**: Tool descriptions, variable names, and comments reference "OpenCode" where "harness" or "agent session" would be more accurate. Update in Phase 3.
3. **Consider `.claude/commands/` for relay-mesh**: Claude Code supports slash commands via `.claude/commands/` directory. A `/relay-status` command could call `list_agents` + `fetch_messages` in one step.

### Best Practices to Adopt

1. **Idempotent install commands**: `installOpenCodePlugin` at `cmd/server/main.go:387-455` checks for existing entries before writing. All new install commands must follow this pattern.
2. **Enabled/disabled pattern**: `opencodepush.Pusher` at `pusher.go:23-36` returns a disabled instance when the base URL is empty. All adapters should follow this zero-configuration-is-off pattern.
3. **Embedded test infrastructure**: `broker_test.go:23-66` uses real embedded NATS. New adapter tests should use real filesystem operations in temp dirs rather than mocks.

---

## Open Questions

1. **Claude Code hook stdin schema**: The exact JSON schema for Claude Code hook stdin varies by hook type (PreToolUse vs PostToolUse vs Stop). Need to verify against current Claude Code version before implementing hook scripts.
   - Options: (a) Test against live Claude Code, (b) Use documented schema from Claude Code docs, (c) Build flexible parsing that handles schema variations.

2. **Codex CLI MCP server lifecycle**: When Codex CLI spawns an MCP server via stdio, does it set `CODEX_THREAD_ID` in the subprocess environment? Confirmed in Codex docs but needs runtime verification.
   - Options: (a) Test with live Codex CLI, (b) Add fallback to manual `bind_session`.

3. **Push adapter selection for shared HTTP mode**: When multiple harnesses connect to the same HTTP relay-mesh server, how does the server know which adapter to use for push delivery? The `Harness` field on `agentState` solves this, but it requires explicit or auto-detected harness identification at bind time.
   - Options: (a) Require explicit `harness` parameter in `bind_session`, (b) Auto-detect from session ID format or environment.

4. **Claude Code Agent Teams interop**: Claude Code already has a teams/mailbox protocol (`~/.claude/teams/`). Should relay-mesh messages also appear in the teams mailbox for agents that use both systems?
   - Options: (a) Ignore -- separate systems, (b) Bridge adapter that writes to both relay-mesh NATS and teams mailbox, (c) Document as complementary systems.

5. **Hook script error handling**: If a Claude Code hook script fails (e.g., jq not installed), should it silently pass through (exit 0) or block the tool call (exit 1)?
   - Recommendation: Silent pass-through (exit 0) with stderr logging. Blocking a tool call due to relay-mesh hook failure would be a poor UX.

---

## Non-Goals (Explicit Exclusions)

1. **Replacing Claude Code Agent Teams.** relay-mesh complements agent teams by providing cross-harness messaging. We do not replicate the teams mailbox protocol.
2. **Building a UI.** No dashboard, web interface, or TUI beyond existing CLI commands.
3. **Adding authentication.** Remains a POC with anonymous agents per Constitution Article I.
4. **Distributed NATS clustering.** Single-node NATS via Docker for local development only.
5. **MCP protocol extensions.** We use standard MCP tools. No custom protocol modifications.

---

## Appendices

### A. Harness Integration Capability Matrix

| Capability | OpenCode | Claude Code | Codex CLI | Cursor | Copilot |
|-----------|----------|-------------|-----------|--------|---------|
| MCP stdio | Yes | Yes | Yes | Yes | Yes |
| MCP HTTP | Yes | Yes | Yes | Yes | Yes |
| Hook: pre-tool | Plugin JS | PreToolUse bash | None | hooks.json | Hooks |
| Hook: post-tool | Plugin JS | PostToolUse bash | None | hooks.json | Hooks |
| Hook: stop/idle | Plugin event | Stop bash | None | None | None |
| Hook: compaction | Plugin JS | SessionStart bash | N/A | N/A | N/A |
| Push delivery | prompt_async API | Stop hook poll | Polling only | Polling only | Polling only |
| Session ID source | Plugin + API | Hook stdin JSON | CODEX_THREAD_ID env | TBD | TBD |
| Auto-bind | Session resolver | PreToolUse hook | Env var detection | TBD | TBD |
| Protocol context | Plugin inject | PostToolUse + file | SKILL.md + AGENTS.md | TBD | TBD |
| Desktop notify | Toast API | notify-send | notify-send | TBD | TBD |
| Install command | install-opencode-plugin | install-claude-code | install-codex | TBD | TBD |

### B. Code References

- Main server entry: `/home/tanwa/relay-mesh/cmd/server/main.go:35-66`
- MCP tool registration: `/home/tanwa/relay-mesh/cmd/server/main.go:457-553`
- Register handler (session binding): `/home/tanwa/relay-mesh/cmd/server/main.go:556-594`
- Send handler (push delivery): `/home/tanwa/relay-mesh/cmd/server/main.go:646-669`
- Broadcast handler (push delivery): `/home/tanwa/relay-mesh/cmd/server/main.go:715-752`
- Broker agent state: `/home/tanwa/relay-mesh/internal/broker/broker.go:47-53`
- Broker RegisterAgent: `/home/tanwa/relay-mesh/internal/broker/broker.go:100-145`
- Broker BindSession: `/home/tanwa/relay-mesh/internal/broker/broker.go:268-284`
- OpenCode pusher: `/home/tanwa/relay-mesh/internal/opencodepush/pusher.go:16-133`
- OpenCode session resolver: `/home/tanwa/relay-mesh/internal/opencodepush/session_resolver.go:13-112`
- OpenCode plugin (auto-bind): `/home/tanwa/relay-mesh/.opencode/plugins/relay-mesh-auto-bind.js:1-108`
- Install OpenCode plugin: `/home/tanwa/relay-mesh/cmd/server/main.go:387-455`
- Mesh orchestration: `/home/tanwa/relay-mesh/cmd/server/main.go:114-141`
- Test helpers: `/home/tanwa/relay-mesh/internal/broker/broker_test.go:23-88`
- Push test pattern: `/home/tanwa/relay-mesh/internal/opencodepush/pusher_test.go:14-86`

### C. Alternative Approaches Considered

**Option A: Harness-agnostic polling-only approach**
- Remove all push delivery. Every harness polls with `fetch_messages`.
- Pro: Simplest implementation. Zero per-harness code.
- Con: Degrades OpenCode experience (loses push). Polling latency is poor UX.
- Verdict: Rejected. Push is the key differentiator.

**Option B: MCP sampling for universal push**
- Use MCP specification's `sampling/createMessage` for server-initiated messages.
- Pro: Standard MCP mechanism. Would work across all harnesses.
- Con: Not implemented by any harness today. mcp-go may not support it yet.
- Verdict: Track for Phase 4 (Task 4.4) as long-term solution. Not viable for immediate implementation.

**Option C: Filesystem-based message queue**
- Write incoming messages to files in a well-known directory. Harness hooks/skills read files.
- Pro: Zero network dependencies for push. Works offline.
- Con: Duplicates NATS functionality. File locking complexity. Cleanup burden.
- Verdict: Partially adopted -- Claude Code Stop hook uses a state file as a lightweight signal, but the primary message store remains NATS.

---

## Roadmap Summary

```
Week 1-2: Phase 1 -- Claude Code Adapter
  - Push adapter interface + registry
  - Claude Code hooks (preToolUse, postToolUse, stop)
  - install-claude-code CLI command
  - Cross-harness E2E: Claude Code <-> OpenCode

Week 2-3: Phase 2 -- Codex CLI Adapter
  - CODEX_THREAD_ID auto-bind
  - Codex skill + AGENTS.md snippet
  - install-codex CLI command
  - Cross-harness E2E: Codex <-> OpenCode

Week 3-4: Phase 3 -- Push Layer Generalization
  - Full push registry migration
  - Remove deprecated opencodepush
  - Generic MCP-only adapter
  - main.go file split
  - Documentation updates

Week 5+: Phase 4 -- Broader Compatibility
  - Cursor, Copilot, Amazon Q adapters
  - MCP server-initiated notifications (long-term)
```
