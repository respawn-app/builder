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

- transport-neutral service layer for project, session, run, process, approval, and ask operations, with the first mandatory extracted slice being session launch/run for `builder run`
- loopback or in-process client adapter that talks through that service layer
- CLI switched onto the client-style boundary instead of direct runtime access, starting with `cli/builder/main.go:runSubcommand`
- boundary enforcement preventing TUI or CLI packages from importing server internals directly

Expected cut lines from the current repo:

- server-only:
  - `server/runtime`
  - `server/session`
  - `server/tools`
  - `server/llm`
  - `server/auth`
- server-composition extraction targets:
  - `cli/app/bootstrap.go`
  - `cli/app/launch_planner.go`
  - `cli/app/runtime_factory.go`
- frontend-only:
  - `cli/builder/main.go`
  - `cli/tui`
  - `cli/app/ui*.go`
  - session/auth pickers and onboarding UX
- likely split:
  - `cli/app/app.go`
  - `cli/app/run_prompt.go`
  - `cli/app/session_lifecycle.go`
  - `cli/app/auth_gate.go`
- new shared boundary packages:
  - `shared/client`
  - `shared/serverapi`
  - future `shared/protocol` transport-envelope types only if the RPC layer needs a separate home

Repo-grounded implementation order:

1. Extract the server-owned launch/runtime composition now trapped in `bootstrap.go`, `launch_planner.go`, and `runtime_factory.go`.
   Progress: `server/runprompt` now owns the headless `builder run` launch path, `server/bootstrap` now owns the lower-level embedded bootstrap helpers for config/container/auth/runtime-support state, `server/embedded` now owns the first explicit in-process app-server composition root used by `cli/app`, `server/authflow` now owns auth readiness polling and env-backed auth-store policy, `server/launch` now owns bootstrap continuation resolution plus session open/create/hydration, `server/lifecycle` now owns interactive lifecycle mutations, `server/sessioncontrol` now owns interactive session selection and transition orchestration, `server/startup` now owns embedded startup request assembly and auth-ready reentry, `server/onboarding` now owns onboarding policy, `server/runtimewire` now owns runtime preparation/tool wiring shared by both interactive and headless flows, `server/runtimeview` plus `shared/clientui` now provide the first client-facing UI projection seam for runtime events and chat snapshots, the TUI now depends on a shared client-facing interactive runtime contract instead of a concrete `*runtime.Engine`, representative and characterization UI suites now instantiate the projected/shared constructor directly via shared test helpers, and the old engine-shaped `NewUIModel(...)` compatibility wrapper has been deleted.
2. Introduce the first client-facing use cases around the current headless path: `ResolveLaunchContext`, `OpenOrCreateSession`, `SubmitUserMessage`, `GetSessionSnapshot`, `SubscribeSessionEvents`, and `InterruptRun`.
3. Remap `cli/builder/main.go:runSubcommand` and `cli/app/run_prompt.go` onto the loopback client boundary without exposing `runtime.Engine`, `session.Store`, `runtime.Event`, or `runtime.ChatSnapshot`.
4. Only after the headless seam is real, widen the boundary to interactive session/open flows and richer read models.
   Progress: `shared/clientui.RuntimeStatus` now exists as the first bundled interactive read model, and the TUI status/back/freshness/status-collector paths now consume that snapshot instead of fanning out across loopback getter methods. `shared/clientui.ProcessClient` plus `BackgroundProcess` now also cover process-list hydration, status-line process counts, and process log-path reads without treating `shelltool.Snapshot` as the UI read model. `shared/clientui.RuntimeSessionView` now bundles session metadata, conversation freshness, and transcript hydration for runtime conversation re-sync. The remaining interactive runtime control paths in `cli/app` now also route through model-level helpers over `shared/clientui.RuntimeClient` instead of scattering direct loopback runtime calls across command/submission/queue controller files.
5. Keep `cli/app/ui_runtime_adapter.go`, `cli/app/ui_status*.go`, `cli/app/ui_processes.go`, `cli/app/auth_gate.go`, and onboarding/import UX as deferred knots until client DTOs and hydration views exist.
   Next concrete cut line: replace the remaining onboarding/auth UX orchestration and the event-oriented interactive runtime adapter surfaces in `cli/app` now that startup/session/onboarding policy already routes through `server/startup`, `server/sessioncontrol`, and `server/onboarding`, and command/submission control paths already route through model-level runtime helpers.

Intermediate state:

- same binary
- no network transport yet
- CLI still fully functional, but the headless path is already using the same boundary future frontends will use

Primary risks:

- creating a god-service instead of a cohesive application-service layer
- leaving hidden import leaks that re-couple the CLI to runtime internals
- preserving frontend access to runtime-native types such as `runtime.Event` and `runtime.ChatSnapshot` under a thin wrapper

Rollback point:

- thin adapters may temporarily route selected flows through old internals while extraction completes, but only if the client-facing requests/responses/events remain transport-neutral and do not leak server-native types

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
- Phase 1 exit gate: at minimum the `builder run` flow and migrated launch/run flows no longer depend on privileged runtime access, and the frontend reaches them only through the client boundary.
- Phase 2 exit gate: second client can hydrate and observe one session in tests.
- Phase 3 exit gate: CLI works against embedded and external server through the same client boundary.
- Phase 4 exit gate: reconnect, approval races, and slow-subscriber failure modes are covered.
- Phase 5 exit gate: existing data adoption works and a non-CLI client can complete the baseline workflow set.
