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

3. What exact local control endpoint shape should v1 use?
   - Answer: local-only HTTP with WebSocket JSON-RPC plus `/healthz` and `/readyz`, published through a workspace discovery record.

4. What exact discovery mechanism should CLI use first?
   - Answer: a workspace-container discovery record owned by the daemon host.

5. What exact handshake payload shape should v1 return?
   - Answer: protocol version, server identity, workspace/project identity, and capability flags.

6. What exact attachment scope should be mandatory in v1?
   - Answer: explicit project attach and session attach.

7. What should default CLI mode selection do once external daemon mode exists?
   - Answer: prefer attaching to a compatible running daemon for the workspace; if none exists, attempt local daemon startup; embedded mode remains the fallback path using the same shared boundary.

## Open Questions (Do Not Block Execution)

1. What is the reconnect model for large sessions?
   - Answer: reconnect is snapshot/page based. Clients rehydrate from authoritative reads such as `session.getMainView` and future transcript-page reads, then resubscribe. Future work should prefer transcript pagination and compression over any stream-history recovery contract.

## Assumptions Made During Execution

- The standalone daemon path should be implemented incrementally without broadening product scope beyond documented Phase 3 deliverables.
- Existing `shared/client`, `shared/serverapi`, and server-side service layers remain the only valid transport-facing contracts.
- If a design choice is ambiguous but not locked, choose the smallest robust option that preserves later Phase 4 work.
