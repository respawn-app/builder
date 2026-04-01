# Phase 3 Handoff Ledger

Use this file during future agent handoffs. It exists to prevent drift.

## Mandatory Directive

Continue Phase 3 execution until the full phase is complete according to `../planning/plan.md` and the migration specs. Do not stop early. Do not use `ask_question` while the user is away. Record questions in `questions.md` and proceed with the best justified assumption. Do not emit a final answer until Phase 3 is complete end-to-end. If the phase is already complete when an automated continue arrives, use `NO_OP`.

## Current Starting Point

- Branch: `app-server-phase-3`
- Baseline checkpoint commit before this branch: `7cd6397` (`refactor: add standalone server host startup path`)
- `builder serve` exists as a standalone headless host shell.
- Server host startup no longer depends on `cli/app` startup handlers.
- Local-only HTTP daemon bootstrap now exists in `server/serve`.
- Discovery record publication/removal now exists in `server/discovery` + `shared/protocol`.
- Health/readiness endpoints now exist on the daemon host.
- Phase 3 transport, handshake, subscriptions over transport, and external attach are not implemented yet.

## Current Next Step

- Build the JSON-RPC-over-WebSocket gateway on top of the new `server/serve` host.
- Start with handshake/capabilities plus transport adapters for already-existing shared service surfaces (`project`, `session view`, `process view/control`, `run prompt`, `session activity`, `process output`, `ask view`, `approval view`).
- After that, add the external client implementation and only then widen the remaining interactive control/session-plan seams.

## Resume Rules

1. Read `phase-3-todo.md` first.
2. Read `questions.md` second.
3. Check current git status.
4. Continue the highest-priority unchecked Phase 3 item.
5. Update these execution docs when assumptions or completion gates change.
