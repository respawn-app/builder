# App Server Migration Plan

This file tracks only work that is still ahead.

Completed phases were moved to `docs/dev/app-server-migration/planning/plan-completed.md` so this file stays usable during implementation.

Phase numbers are historical labels. They are kept for continuity, not because work must execute strictly in numeric order.

## Current Focus

Current shipping path:

1. Phase 2 residual: finish the current-TUI server boundary and guardrails

Not on the shipping critical path:

- Phase 8 shared frontend transcript architecture refactor

## Open Work

### Phase 2 Residual: Resource Surfaces And Event Hub

Goal: finish only the server-owned reads, guardrails, and transport semantics required for the current TUI to run correctly against one device-global server.

Already landed before this residual phase:

- active-session hydration via `session.getMainView` / `run.get`-style reads
- active run identity and durable run lifecycle metadata
- transport-neutral process list/get plus `kill` / `inline-output` control
- active-session live activity seam with explicit lag failure semantics
- initial `client_request_id` duplicate suppression on the headless prompt-submit path

This section is intentionally only the residual work after `planning/phase-2-checkpoint.md`.
It must not re-list the already-landed run/process/main-view foundation slice as open implementation work.

Everything not required to ship the current TUI on one device-global server is deferred to `docs/dev/app-server-migration/planning/phase-2-deferred.md`.

Requirements for this phase:

- [ ] current TUI startup, project picker, and session picker can hydrate from typed project/session reads without CLI-local metadata stitching
- [ ] exactly one app-server process may operate on a persistence root at a time; a second server process fails explicitly instead of racing on the same database or artifact tree
- [ ] same-session multi-client is temporarily restricted: multiple clients may attach and read, but exactly one client may control or mutate a session at a time
- [ ] loopback and real transport preserve the same TUI-critical DTOs, run control semantics, and streaming behavior
- [ ] database and migration boundaries remain clean enough to extend later for GUI frontends without introducing speculative resource systems now

Dependency note:

- land `2R.1` before `2R.2` so startup/session hydration uses typed reads before we tighten server/control ownership rules
- land `2R.2` before `2R.3` so single-server and single-controller guardrails are explicit before we call the TUI path shippable
- land `2R.4` alongside each remaining TUI-relevant mutating action instead of as a cleanup pass afterward

#### 2R.1 Minimal project/session reads for current TUI startup

Scope: `server/projectview/*`, `server/sessionview/*`, `shared/serverapi/*`, `shared/client/*`, startup/picker consumers in `cli/app/*`

Entry criteria for implementation:

- the server exposes enough typed reads for startup to answer, without CLI-local metadata stitching: `project.list`, `project.getOverview`, `session.listByProject`, and the current binding or registration state already needed by the existing startup flow
- those reads include the minimum fields the current TUI actually renders: project id, display name, root path, availability, workspace summary data, session counts, latest activity, and session summary rows

- [ ] define the minimal project/session hydration surface needed by the current startup and picker UX: `project.list`, `project.getOverview`, `session.listByProject`, workspace summaries, availability, session counts, latest activity, and binding/registration state where already required by the TUI flow
- [ ] remove remaining startup or picker decisions that still depend on CLI-owned metadata stitching when the same result should come from typed server reads
- [ ] make project/session picker flows consume the same transport-neutral DTOs in loopback and real-server mode
- [ ] add regression coverage proving dormant project/session picker hydration does not mutate persistence state and does not require active runtime presence

Deliverable for `2R.1`:

- the current TUI can render startup project selection, workspace binding state, and per-project session inventory from typed reads only

#### 2R.2 Single-server and single-controller guardrails

Scope: app-server bootstrap, persistence-root locking, session control or runtime lease ownership, TUI session attach/control paths

- [ ] enforce one app-server process per persistence root with an explicit failure path when another server already owns that root
- [ ] enforce temporary same-session control exclusivity: many clients may attach or read, but only one client may submit/control/mutate a session at a time
- [ ] mark that same-session control restriction explicitly as temporary in code comments where the contract is enforced
- [ ] add coverage proving a second client can still attach or read while a controlling client exists, but receives a deterministic rejection for mutating actions

Deliverable for `2R.2`:

- the current TUI has clear single-server and single-controller semantics instead of relying on accidental process or client exclusivity

#### 2R.3 TUI-critical live surfaces only

Scope: `server/sessionactivity/*`, process read/control or output paths that the current TUI actually exercises, loopback/transport parity

- [ ] keep only the live surfaces the current TUI materially needs on the app-server boundary: session activity, process inspection/control, and any process-output access already required by `/ps`
- [ ] keep asks and approvals transcript-driven for now; do not introduce first-class ask/approval resources or prompt-activity streams in this phase
- [ ] define gap/backpressure semantics for the TUI-critical live streams we keep, with explicit rehydrate/resubscribe behavior
- [ ] add black-box loopback plus real-transport tests proving the current TUI-critical live feeds behave the same across transports

Deliverable for `2R.3`:

- the current TUI uses only the minimal live server surfaces it actually needs, with no extra future-facing stream families added yet

#### 2R.4 `client_request_id` idempotency expansion for TUI-critical mutations

Scope: mutating APIs the current TUI can retry across loopback/transport boundaries: prompt submission, session lifecycle, process control, project/workspace mutation, and any session-control mutations retained after `2R.2`

Named contract to preserve while implementing:

- dedup scope is `(method, resource identity, client_request_id)` rather than process-global request ids
- dedup retention is persisted and time-bounded; the initial residual-phase target is a documented default window shared across mutating APIs, not ad hoc per-handler memory caches
- exact same payload replays the original outcome, payload mismatch rejects deterministically, and cancellation or timeout does not become a cached success result

- [ ] introduce one persisted dedup store or table with explicit scope, payload fingerprinting, retention window, and mismatch rejection semantics
- [ ] apply that dedup contract to the remaining TUI-relevant mutable operations not already covered, including process mutations and project/session lifecycle mutations
- [ ] make duplicate-retry outcomes deterministic: same payload replays the original outcome, mismatched payload rejects cleanly, cancellations are not cached as permanent success
- [ ] add retry/race coverage for the retained TUI-critical mutable operations under the temporary single-controller model

Deliverable for `2R.4`:

- transport retries are safe across the current TUI write surface, not only for prompt submission

#### 2R.5 Phase proof and rollout

Scope: focused acceptance coverage plus docs and contract cleanup

- [ ] add acceptance coverage proving the current TUI can run end-to-end against one device-global server using only the retained project/session/process/live surfaces
- [ ] document the final resource or stream taxonomy and retention semantics in the migration spec once code is landed
- [ ] audit the active plan and move completed residual Phase 2 slices into `plan-completed.md` so the remaining backlog stays readable

Non-goals for this phase:

- do not add broad remote filesystem inspection APIs
- do not move transcript storage into the database
- do not add first-class ask/approval resource storage in this phase; asks and approvals remain transcript-driven for the current TUI
- do not add prompt-activity stream families or other optional routes needed only for future GUI frontends
- do not start desktop or web-specific rendering work here; this phase is server contract work

### Phase 8: Shared Frontend Transcript Architecture

Goal: improve transcript reliability systemically after shipment by moving transcript semantics into shared frontend logic instead of frontend-specific codepaths.

Concrete tasks:

- [ ] consolidate committed-tail reconciliation so `eventTranscriptEntriesReconcileWithCommittedTail`-equivalent logic reasons in one place over session id, revision, committed count, committed start, and contiguous overlap
- [ ] introduce one shared frontend transcript reducer/op model in shared code
- [ ] migrate TUI transcript state transitions onto that shared reducer as the first consumer
- [ ] replace event-kind-driven transcript handling with explicit transcript ops
- [ ] formalize one committed transcript model plus one live overlay model for frontend consumers
- [ ] add deterministic transcript trace replay coverage so field failures can be reproduced against the shared reducer
