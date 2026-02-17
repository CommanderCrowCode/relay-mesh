# Relay Mesh Constitution

Version: 1.0.0  
Effective: 2026-02-17  
Authority: Project Maintainer

## Preamble

Relay Mesh is a minimal POC proving MCP-mediated anonymous agent messaging over NATS. This constitution governs implementation standards and change boundaries so the project remains small, reliable, and useful as a Civitas stepping stone.

## Article I: Scope and Boundaries

1. The project remains a focused POC: MCP server + NATS messaging + anonymous agents.
2. No hidden expansion into identity, governance, or persistence without explicit scope change.
3. Agents interact through MCP tools only.

## Article II: Architectural Law

4. Go is the implementation language for core runtime code.
5. NATS is the required transport backbone.
6. Broker state is in-memory by design for this phase.
7. MCP stdio server in `cmd/server` is the external interface.

## Article III: Engineering Standards

8. Public behavior must be documented in `README.md`.
9. Error paths must return explicit errors; avoid panics in normal control flow.
10. Inputs are validated at tool and broker boundaries.
11. Code must stay simple and readable over abstract/generalized.

## Article IV: Testing and Quality Gates

12. `go build ./...` must pass before changes are considered complete.
13. `go test ./...` must pass before changes are considered complete.
14. Broker behavior changes require tests that cover the modified path.
15. Any discovered race or nondeterminism in tests must be fixed, not ignored.

## Article V: Operational Expectations

16. Local developer setup must work with `docker-compose.yml` and documented commands.
17. Startup and shutdown paths must be clean.
18. Breakages in documented run flow are release-blocking issues.

## Article VI: Evolution Rules

19. Any feature that introduces identity/auth must be gated behind a new PRD revision.
20. Any feature that introduces persistence must define migration and recovery behavior first.
21. Any change that violates this constitution requires an explicit amendment in this file.

