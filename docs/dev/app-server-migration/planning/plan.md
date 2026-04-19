# App Server Migration Plan

This file tracks only work that is still ahead.

Completed phases were moved to `docs/dev/app-server-migration/planning/plan-completed.md` so this file stays usable during implementation.

Phase numbers are historical labels. They are kept for continuity, not because work must execute strictly in numeric order.

## Current Focus

Current shipping path:

1. Phase 8 shared frontend transcript architecture refactor
2. Phase 9 multi-client session control follow-up

Not on the shipping critical path:

- Optional future frontend/API expansion beyond the current TUI shipping boundary

## Open Work

### Phase 2 Residual: Resource Surfaces And Event Hub

Completed. Phase 2 residual implementation, proof, and boundary-audit closeout are archived in `docs/dev/app-server-migration/planning/plan-completed.md`.

Phase 2 follow-up that still remains open is tracked separately in `docs/dev/app-server-migration/planning/phase-9-multi-client-session-control.md`.

### Hard-Cut Rollback: Remove SQLite-Backed Request Dedup Persistence

Completed. Archived in `docs/dev/app-server-migration/planning/plan-completed.md`.

### Remote-Server Blockers: Server-Owned Auth Bootstrap And Path-Independent Attach

Completed. Archived in `docs/dev/app-server-migration/planning/plan-completed.md`.

### Phase 8: Frontend-Agnostic Transcript Semantics

Goal: improve transcript reliability systemically after shipment by defining one frontend-agnostic transcript semantics model, one reconciliation contract, and reproducible trace coverage that future Kotlin/Desktop/Web frontends can implement correctly.

Scope reduction:

- This phase is not about building Go-only shared frontend infrastructure.
- The valuable output is the semantics, invariants, trace fixtures, and reference behavior, not reusable Go packages for future frontends.
- The current Go TUI is only the first reference consumer/proving ground for those semantics.

Concrete tasks:

- [ ] consolidate committed-tail reconciliation so `eventTranscriptEntriesReconcileWithCommittedTail`-equivalent logic reasons in one place over session id, revision, committed count, committed start, and contiguous overlap
- [ ] replace event-kind-driven transcript handling with explicit transcript ops/invariants that can be implemented outside Go
- [ ] formalize one committed transcript model plus one live overlay model for frontend consumers regardless of implementation language
- [ ] define trace fixtures / replay cases that any frontend implementation can run against the transcript semantics
- [ ] use the current Go TUI as the first reference implementation/proof target for those semantics where helpful
- [ ] add deterministic transcript trace replay coverage so field failures can be reproduced against the semantics model rather than only against one frontend implementation
