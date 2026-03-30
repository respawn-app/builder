# App Server Migration Plan

Status: draft phased plan

This is the current implementation-planning baseline derived from the migration requirements and external architecture review.

The plan is intentionally incremental. It keeps the product working throughout and avoids wrapping the current monolith in transport before the real boundaries exist.

## Phase 0: Freeze Behavior And Define Proof Obligations

Deliverables:

- complete command and workflow compatibility inventory
- characterization coverage for behavior-heavy areas
- persisted-session storage audit
- finalized session and run invariants
- black-box acceptance harness design

Required outputs:

- `../spec/behavior-preservation.md` complete and cross-checked against the codebase
- `phase-0-checkpoint.md` complete and used as the execution checklist
- `phase-0-workstreams.md` complete enough to drive bounded parallel work
- `boundary-map.md` complete enough to define the first extraction seam
- `../analysis/persistence-audit.md` complete enough to define the old-data adoption baseline
- session fixture corpus for old data adoption
- explicit list of busy-safe versus busy-blocked operations

Primary risks:

- skipping this phase guarantees accidental regressions while the migration still claims preserved functionality

Parallelizable work:

- command inventory verification
- storage audit
- acceptance-harness design

## Phase 1: Create A Transport-Neutral Server API In Process

Goal:

Create the real application-service boundary before any network transport work starts.

Deliverables:

- transport-neutral service layer for project, session, run, process, approval, and ask operations
- loopback or in-process client adapter that talks through that service layer
- CLI switched onto the client-style boundary instead of direct runtime access
- boundary enforcement preventing TUI or CLI packages from importing server internals directly

Expected cut lines from the current repo:

- server-only:
  - `internal/runtime`
  - `internal/session`
  - `internal/tools`
  - `internal/llm`
  - `internal/auth`
- frontend-only:
  - `internal/tui`
  - CLI shell and command translation pieces
- likely split:
  - `internal/app` -> server composition and CLI composition
- new shared boundary packages:
  - `internal/protocol`
  - `internal/client`
  - `internal/serverapi` or equivalent service-layer package

Intermediate state:

- same binary
- no network transport yet
- CLI still fully functional, but now through the boundary that future frontends will use

Primary risks:

- creating a god-service instead of a cohesive application-service layer
- leaving hidden import leaks that re-couple the CLI to runtime internals

Rollback point:

- thin adapters may temporarily route selected flows through old internals while extraction completes, but the intended direction remains boundary-first

## Phase 2: Stabilize Resource Model, Hydration Views, And Event Hub

Goal:

Introduce the minimum server-owned resource model and read models needed for real multi-client behavior.

Deliverables:

- project registry
- session, run, process, approval, and ask resource identities
- one-active-primary-run invariant enforcement
- idempotency support through `client_request_id`
- typed hydration views for startup and session main view
- event hub with explicit stream classes and gap semantics
- process output buffering and retention policy

Intermediate state:

- still one binary
- CLI still runs through the in-process client boundary
- tests can attach a second synthetic client to the same session

Primary risks:

- overfitting hydration views to today’s TUI rendering
- turning protocol events into accidental storage truth
- failing to distinguish durable transcript state from live output

Parallelizable work after the boundary exists:

- process resource surface
- approval and ask surface
- CLI command remapping onto the new client interface

## Phase 3: Add Real Transport And Local Daemon Mode

Goal:

Expose the already-existing server boundary over the real protocol.

Deliverables:

- JSON-RPC-over-WebSocket gateway
- handshake, capability exchange, and attachment lifecycle
- health and readiness endpoint
- local discovery on well-known control endpoint or socket
- `builder serve`
- CLI attach-or-start logic against the real transport

Critical rule:

- embedded mode must still use the same client boundary, not direct object access

Intermediate state:

- CLI can run against embedded local server or external daemon
- core workflows remain preserved

Primary risks:

- OS-specific discovery and lifecycle edge cases
- hidden assumptions that only worked in loopback mode

Rollback point:

- keep the in-process transport adapter for tests and fallback while the network transport hardens

## Phase 4: Multi-Client Correctness And Reconnect Hardening

Goal:

Make the boundary reliable under the conditions that actually matter for future non-CLI clients.

Deliverables:

- two real clients attached to one session in tests
- deterministic approval and ask race handling
- reconnect with hydration-first recovery
- best-effort catch-up with explicit gap handling
- process-control race coverage
- slow-client handling and bounded buffering

Primary risks:

- duplicate submissions after reconnect or retry
- slow-client memory growth
- ambiguous queue semantics during active runs

Rollback point:

- if stream catch-up proves too fragile, force full rehydrate on reconnect and keep replay narrow and best-effort

## Phase 5: Data Adoption, Standalone Polish, And Boundary Proof

Goal:

Finish the migration by proving the CLI is now just one frontend.

Deliverables:

- lazy adoption or migration of existing session data
- project missing and inaccessible states handled cleanly
- server identity surfaced in the frontend
- at least one minimal non-CLI reference or test client
- CI boundary enforcement active
- acceptance suite able to run against external-daemon mode only

Primary risks:

- old-session compatibility bugs
- path canonicalization bugs
- stale project registry behavior

Rollback point:

- prefer reversible metadata additions and lazy adoption rather than destructive one-shot migration

## Sequential Versus Parallel Work

Must remain sequential at the high level:

1. behavior freeze and proof obligation
2. transport-neutral service boundary
3. resource and hydration model
4. real transport
5. multi-client hardening and data adoption

Reason:

Starting with transport first almost guarantees wrapping the monolith instead of actually decomposing it.

Can be parallelized once phase 1 exists:

- process resource API
- approval and ask resource API
- command remapping
- auth bootstrap UX
- local discovery UX

## Proof Gates Per Phase

- Phase 0 exit gate: compatibility inventory and characterization plan are accepted.
- Phase 1 exit gate: CLI no longer depends on privileged runtime access for migrated flows.
- Phase 2 exit gate: second client can hydrate and observe one session in tests.
- Phase 3 exit gate: CLI works against embedded and external server through the same client boundary.
- Phase 4 exit gate: reconnect, approval races, and slow-subscriber failure modes are covered.
- Phase 5 exit gate: existing data adoption works and a non-CLI client can complete the baseline workflow set.
