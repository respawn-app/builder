# Questions And Assumptions

Purpose:

- record questions that cannot block execution while the user is away
- force explicit assumptions instead of ad hoc drift
- leave a durable trail for future agents and for later user review

## Automation Directive To Preserve

The user requested that work continue through automated continue prompts until Phase 3 is complete end-to-end according to the migration docs, without using `ask_question`, without stopping early, and without emitting a final answer until completion. If an automated handoff is needed, preserve that directive in the next handoff artifact and continue from this file plus `phase-3-todo.md`.

## Locked / Answered

1. Should `builder serve` remain server-only or grow interactive setup/auth UX?
   - Answer: server-only. No interactive `serve` UX.

2. Should current Phase 3 work expand into multi-workspace hosting?
   - Answer: no. Current implementation remains single-workspace for this phase.

## Open Questions (Do Not Block Execution)

1. What exact local control endpoint shape should v1 use?
   - Working assumption: implement a local-only HTTP server that exposes WebSocket JSON-RPC plus health/readiness endpoints, with bind/bootstrap details chosen for minimal CLI attach-or-start friction.

2. What exact discovery mechanism should CLI use first?
   - Working assumption: use an explicit local endpoint file / address bootstrap owned by the daemon host rather than introducing platform-specific service management up front.

3. What exact handshake payload shape should v1 return?
   - Working assumption: include protocol version, server identity, workspace/project identity, and capability flags sufficient for client gating.

4. What exact attachment scope should be mandatory in v1?
   - Working assumption: support explicit project attach plus session attach needed for current CLI workflows; avoid inventing broader scope concepts unless implementation requires them.

5. How much catch-up/replay is needed in Phase 3 vs Phase 4?
   - Working assumption: preserve the currently implemented stream error model and current live-subscription semantics; deeper reconnect hardening remains Phase 4 unless transport viability forces a minimal addition.

6. What should default CLI mode selection do once external daemon mode exists?
   - Working assumption: preserve current product behavior while adding daemon support. The CLI should prefer attaching to a compatible running daemon for the current workspace when one exists. If none exists, interactive CLI should offer local server startup rather than silently changing all startup behavior; headless flows may start a local daemon automatically where prompting is impossible. Embedded mode remains available and must continue using the same client boundary.

## Assumptions Made During Execution

- The standalone daemon path should be implemented incrementally without broadening product scope beyond documented Phase 3 deliverables.
- Existing `shared/client`, `shared/serverapi`, and server-side service layers remain the only valid transport-facing contracts.
- If a design choice is ambiguous but not locked, choose the smallest robust option that preserves later Phase 4 work.
