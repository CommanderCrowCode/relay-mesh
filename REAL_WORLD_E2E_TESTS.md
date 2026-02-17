# Real-World E2E Test Scenarios (OpenCode + relay-mesh)

This document defines realistic, high-signal E2E scenarios to validate `relay-mesh` beyond toy messaging.

Scope:
- Multi-agent, multi-project collaboration
- Relevance routing (`find_agents`, `broadcast_message`)
- Direct messaging (`send_message`)
- Delivery verification (`fetch_messages`, `fetch_message_history`)
- Session push behavior (OpenCode-bound sessions)
- Profile evolution (`update_agent_profile`)

## 1. Test Environment

Run once:

```bash
cd /Users/tanwa/relay-mesh
make install
relay-mesh mesh-up
opencode mcp list
```

Expected:
- `relay-mesh` shows `connected`
- OpenCode server on `http://127.0.0.1:4097`
- MCP endpoint on `http://127.0.0.1:8080/mcp`

Clean slate before each scenario:

```bash
relay-mesh down
relay-mesh up
```

## 2. Common Agent Metadata Contract

Every test agent should register with:
- `description`
- `project`
- `role`
- `specialization`
- optional: `name`, `github`, `branch`

Recommended naming convention:
- `name`: `<project>-<role>-<index>` (example: `billing-backend-1`)

## 3. Evidence Collection

For each scenario, capture:
- OpenCode tool-call outputs (JSON)
- `opencode export <session_id>` for receiver sessions
- `~/.relay-mesh/relay-http.log` tail

Minimum pass condition:
- Sender sees successful tool response
- Receiver confirms message either via push in transcript or `fetch_messages`
- `fetch_message_history` contains the message ID(s)

## 4. Scenario Set (12)

## Scenario 1: Cross-Project Feature Handoff

Goal:
- Backend agent in `payments` asks frontend agent in `checkout` to consume new API.

Setup:
1. Register `payments-backend-1` (`project=payments`, `role=backend`, `specialization=go-api`).
2. Register `checkout-frontend-1` (`project=checkout`, `role=frontend`, `specialization=react`).

Steps:
1. Sender calls `find_agents` with `query=checkout frontend react`.
2. Sender calls `send_message` with API contract details.
3. Receiver calls `fetch_messages`.
4. Receiver calls `update_agent_profile` to add `branch=feat/new-api`.

Expected:
- Correct target found.
- Message delivered and fetchable.
- Updated profile visible in `list_agents`.

## Scenario 2: Incident Swarm (Production Outage)

Goal:
- On-call SRE broadcasts severity-1 incident to all relevant infra agents.

Setup:
1. Register SRE sender (`project=platform`, `role=sre`, `specialization=incident-response`).
2. Register 3 receivers: DB, API, observability specialists.

Steps:
1. Call `broadcast_message` with `project=platform`, `body=SEV1 details`.
2. Each receiver runs `fetch_messages max=1`.
3. Verify durable storage with `fetch_message_history`.

Expected:
- Broadcast returns one envelope per eligible receiver.
- No unrelated project agents receive it.
- History shows all messages.

## Scenario 3: Security CVE Campaign Across Repos

Goal:
- Security lead coordinates patch across `gateway`, `auth`, `billing`.

Setup:
1. Register one security lead.
2. Register 3 service maintainers with distinct `project`.

Steps:
1. Lead uses `find_agents query=maintainer`.
2. Lead sends per-project `send_message` including patch deadline.
3. Maintainers update profile `branch` as patch branch.
4. Lead queries `find_agents query=cve-2026` (in descriptions).

Expected:
- Directed fan-out works across projects.
- Profile updates reflect progress context.

## Scenario 4: Data Migration With Rollback Coordination

Goal:
- Migration coordinator aligns DB, backend, and QA agents in one rollout window.

Setup:
1. Register `migration-coordinator`.
2. Register DB, backend, QA agents under same `project`.

Steps:
1. Coordinator broadcasts pre-check list with `role` filter (first backend+db).
2. Coordinator sends direct rollback plan to QA.
3. QA validates receipt via `fetch_messages`.
4. Coordinator confirms all messages in `fetch_message_history`.

Expected:
- Role-filtered broadcast reaches only intended groups.
- Direct rollback note delivered to QA only.

## Scenario 5: Release Train (Multi-Team Go/No-Go)

Goal:
- Release manager gathers go/no-go from 5 functional roles.

Setup:
1. Register release manager.
2. Register agents for backend, frontend, QA, SRE, support.

Steps:
1. Manager uses `broadcast_message` with `project=<release project>`.
2. Each role sends `send_message` reply to manager.
3. Manager runs `fetch_messages max=10`.

Expected:
- Manager receives exactly one response per participating role.
- Timestamps indicate near-real-time coordination.

## Scenario 6: Customer Escalation Bridge (Support -> Engineering)

Goal:
- Support triage agent escalates a high-value customer issue to correct specialist.

Setup:
1. Register support agent with customer context.
2. Register backend and data agents with distinct `specialization`.

Steps:
1. Support uses `find_agents query=postgres timeout`.
2. Sends directed `send_message` with repro steps.
3. Specialist acknowledges with return `send_message`.
4. Support verifies history for full thread.

Expected:
- Correct specialist selected via relevance query.
- Two-way message thread persisted in history.

## Scenario 7: Dependency Upgrade Program

Goal:
- Platform agent coordinates org-wide upgrade (e.g., `nats.go` major bump).

Setup:
1. Register platform coordinator.
2. Register maintainers across 4 projects.

Steps:
1. Coordinator `broadcast_message query=go nats`.
2. Maintainers update profiles with upgrade branches.
3. Coordinator runs `find_agents specialization=go` and audits `branch`.

Expected:
- Relevance-based broadcast reaches maintainers likely affected.
- Profile state becomes a lightweight upgrade tracker.

## Scenario 8: Research-to-Implementation Handoff

Goal:
- ML research agent hands model constraints to inference and infra agents.

Setup:
1. Register research, inference, infra agents in `ai-platform`.

Steps:
1. Research broadcasts summary to `project=ai-platform`.
2. Sends private detail to infra only.
3. Inference agent fetches queue and updates description with decision.

Expected:
- Mixed public/private coordination works.
- Decision trace appears in profile updates and history.

## Scenario 9: Compliance Audit Preparation

Goal:
- Compliance lead requests evidence packets from app teams.

Setup:
1. Register compliance lead.
2. Register 3 app team agents.

Steps:
1. Lead sends standardized checklist via broadcast.
2. Teams reply direct with artifact pointers.
3. Lead fetches all responses and verifies count.

Expected:
- Structured request fan-out and response fan-in complete.
- No dropped messages in history.

## Scenario 10: New Agent Onboarding With Unknowns

Goal:
- New agent joins with incomplete context, then enriches metadata later.

Setup:
1. Register new agent with `github=unknown`, `branch=unknown`.

Steps:
1. Existing lead sends onboarding message.
2. New agent fetches and responds.
3. New agent updates profile fields once known.

Expected:
- System handles partial metadata at registration.
- Metadata refinement works without re-registering.

## Scenario 11: Push Delivery Visibility Check (OpenCode Session Injection)

Goal:
- Verify receiver gets OpenCode push when bound to session.

Setup:
1. Ensure `register_agent` result includes `session_id` or use `bind_session`.

Steps:
1. Sender calls `send_message`.
2. Receiver session exports transcript: `opencode export <session_id>`.
3. Search for `New relay-mesh message for <agent_id>`.

Expected:
- Push text appears in receiver transcript.
- Same message exists in `fetch_message_history`.

## Scenario 12: Restart/Recovery Semantics (Known POC Limits)

Goal:
- Validate behavior after `relay-mesh` restart.

Setup:
1. Register 2 agents and exchange messages.

Steps:
1. Run `relay-mesh down && relay-mesh up`.
2. Call `list_agents`.
3. Re-register agents and repeat one message exchange.

Expected:
- Pre-restart in-memory agent registry is cleared.
- New registrations function immediately after restart.
- History behavior follows NATS/JetStream lifecycle on your local container state.

## 5. Regression Checklist (Run After Any Change)

1. `go test ./...` passes.
2. `register_agent` auto-binds session in OpenCode.
3. `list_agents` returns all currently registered agents.
4. `send_message` + `fetch_messages` works.
5. `broadcast_message` respects filters.
6. `find_agents` returns relevant profiles.
7. `update_agent_profile` persists updates.
8. `fetch_message_history` returns durable records.
9. Push appears in receiver transcript (`opencode export` proof).
10. `relay-mesh down/up` recovery behavior matches documented expectations.

## 6. Failure Triage Guide

If message not visible:
1. Check binding: `get_session_binding`.
2. Check queue fallback: `fetch_messages`.
3. Check history: `fetch_message_history`.
4. Check OpenCode server health: `curl -sS http://127.0.0.1:4097/session`.
5. Check relay log: `tail -n 200 ~/.relay-mesh/relay-http.log`.

If agents appear stale:
1. Restart stack: `relay-mesh down && relay-mesh up`.
2. Re-register agents for a true clean run.

