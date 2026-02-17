# Relay Mesh POC PRD

Version: 1.0.0  
Status: Active  
Date: 2026-02-17

## 1. Purpose

Build a minimal, local-first stepping-stone product toward Civitas that proves agent-to-agent messaging via MCP and NATS without identity management complexity.

## 2. Goals

1. Provide an MCP server over stdio as the single integration surface.
2. Enable agent-to-agent messaging using NATS transport.
3. Keep agent identity anonymous and lightweight (generated IDs only, no auth, no PKI).
4. Ensure agents interact only through MCP tools (no direct broker API exposure).

## 3. Non-Goals

1. No cryptographic identity, signatures, or trust verification.
2. No persistent storage for agents/messages.
3. No project governance, contracts, or policy engine.
4. No UI.
5. No distributed cluster management.

## 4. Users and Usage

Primary user: developer building and testing agent runtimes locally.

Typical flow:
1. Start NATS.
2. Start `relay-mesh` MCP server.
3. Register two anonymous agents with MCP.
4. Send message from one to another.
5. Fetch messages for recipient.

## 5. Functional Requirements

### FR-1 MCP Server

Server must expose MCP tools:
1. `register_agent`
2. `list_agents`
3. `send_message`
4. `fetch_messages`

### FR-2 Anonymous Agents

1. Agent registration returns generated `agent_id` (`ag-<random>`).
2. Optional display name is allowed.
3. No credentials, login, tokens, keys, or external identity checks.

### FR-3 Messaging via NATS

1. Each agent maps to subject `relay.agent.<agent_id>`.
2. `send_message` publishes envelope to recipient subject.
3. Recipient queue stores incoming messages in memory for fetch.

### FR-4 Message Envelope

Minimum envelope fields:
1. `id`
2. `from`
3. `to`
4. `body`
5. `created_at` (UTC)

### FR-5 Fetch Semantics

1. `fetch_messages` returns up to `max` queued messages.
2. Fetch drains returned messages from the in-memory queue.
3. `max <= 0` defaults to `10`.

### FR-6 Input Validation

1. `send_message` rejects missing `from`, `to`, or `body`.
2. `send_message` rejects unknown sender or unknown target.
3. `fetch_messages` rejects missing or unknown `agent_id`.
4. Invalid `max` returns validation error.

## 6. Quality Requirements

1. `go build ./...` passes.
2. `go test ./...` passes.
3. Core broker flows are covered with tests:
   - register/list/send/fetch happy path
   - invalid sender handling
   - fetch limit and queue drain behavior
4. Documented local run path works with Docker NATS.

## 7. Integration and Packaging Requirements

1. Repo must include:
   - `README.md` with setup and MCP tool contract
   - `Makefile` with `run`, `build`, `test`, `nats-up`, `nats-down`
   - `docker-compose.yml` for local NATS
2. Product is packaged as source-first Go project (single `go run` entrypoint at `cmd/server`).

## 8. Acceptance Criteria

Project is considered ready-for-usage when:
1. A developer can start NATS and run server from README instructions.
2. Two agents can be registered through MCP.
3. Message send/fetch works reliably.
4. All build and test commands pass on a clean checkout.

## 9. Roadmap to Civitas

Future phases after this POC:
1. Durable storage and replay.
2. Agent authentication and signing.
3. Project/contract governance domains.
4. Supervision, quotas, and event ledger.

