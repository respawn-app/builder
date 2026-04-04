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
   Progress: `server/runprompt` now owns the headless `builder run` launch path, `server/bootstrap` now owns the lower-level embedded bootstrap helpers for config/container/auth/runtime-support state, `server/embedded` now owns the first explicit in-process app-server composition root used by `cli/app`, `server/authflow` now owns auth readiness polling and env-backed auth-store policy, `server/launch` now owns bootstrap continuation resolution plus session open/create/hydration, `server/lifecycle` now owns interactive lifecycle mutations, `server/sessioncontrol` now owns interactive session selection and transition orchestration, `server/startup` now owns embedded startup request assembly and auth-ready reentry, `server/onboarding` now owns onboarding policy, `server/runtimewire` now owns runtime preparation/tool wiring shared by both interactive and headless flows, `server/runtimeview` plus `shared/clientui` now provide the first client-facing UI projection seam for runtime events and chat snapshots, the embedded frontend/server seam is now a frontend-shaped facade rather than a bag of privileged server handles, the TUI now depends on a shared client-facing interactive runtime contract instead of a concrete `*runtime.Engine`, representative and characterization UI suites now instantiate the projected/shared constructor directly via shared test helpers, and the old engine-shaped `NewUIModel(...)` compatibility wrapper has been deleted.
2. Introduce the first client-facing use cases around the current headless path: `ResolveLaunchContext`, `OpenOrCreateSession`, `SubmitUserMessage`, `GetSessionSnapshot`, `SubscribeSessionEvents`, and `InterruptRun`.
3. Remap `cli/builder/main.go:runSubcommand` and `cli/app/run_prompt.go` onto the loopback client boundary without exposing `runtime.Engine`, `session.Store`, `runtime.Event`, or `runtime.ChatSnapshot`.
4. Only after the headless seam is real, widen the boundary to interactive session/open flows and richer read models.
   Progress: `shared/clientui.RuntimeStatus` now exists as the first bundled interactive read model, and the TUI status/back/freshness/status-collector paths now consume that snapshot instead of fanning out across loopback getter methods. `shared/clientui.ProcessClient` plus `BackgroundProcess` now also cover process-list hydration, status-line process counts, and process log-path reads without treating `shelltool.Snapshot` as the UI read model. `shared/clientui.RuntimeSessionView` now bundles session metadata, conversation freshness, and transcript hydration for runtime conversation re-sync. The remaining interactive runtime control paths in `cli/app` now also route through model-level helpers over `shared/clientui.RuntimeClient` instead of scattering direct loopback runtime calls across command/submission/queue controller files, and runtime-event state transitions now reduce through `shared/clientui.ReduceRuntimeEvent(...)` rather than frontend-local Bubble Tea orchestration.
5. Keep `cli/app/ui_runtime_adapter.go`, `cli/app/ui_status*.go`, `cli/app/ui_processes.go`, `cli/app/auth_gate.go`, and onboarding/import UX as deferred knots until client DTOs and hydration views exist.
   Status: Phase 1 exit gate is now satisfied. Bubble Tea onboarding/auth screens, terminal bells, and direct process-control affordances remain frontend-owned UX adapters by design; the next concrete cut line is Phase 2 resource identity, hydration views, and stream semantics.

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

Progress:

- The Phase 2 foundation slice is now complete, but broader Phase 2 remains open. The live runtime now exposes explicit active-run identity/status, `shared/clientui.RuntimeMainView` now bundles active-session hydration, `server/runtimeview` now owns the runtime-to-main-view projection plus the first server-owned active-session read service, the CLI consumes that bundled view through the client boundary, focused coverage now proves run-state payload semantics for completed and interrupted runs, the session log now persists minimal durable run lifecycle history for later `run.get`-style reads, `shared/serverapi` plus `server/sessionview` now provide the first transport-neutral `session.getMainView` / `run.get`-style application read service, dormant session reads now resolve through read-only `server/session.Snapshot` loading rather than mutating reopen paths, `server/registry` now owns reusable runtime and persistence resolution rather than keeping those registries trapped in `server/embedded`, live background processes now carry explicit owning session/run/step identity at creation time, and `shared/serverapi` plus `server/processview` now provide the first transport-neutral process read and control service subset that the CLI uses for `/ps` hydration plus kill/inline actions instead of projecting manager snapshots and control entirely inside `cli/app`. `process.kill` now carries `client_request_id` as a mutating contract; `process.inlineOutput` is on-boundary but remains read-like rather than mutating. `shared/serverapi` plus `server/sessionactivity` now also provide the first live session-activity subscription seam, backed by server-owned runtime registries and explicit lag failure rather than silent event loss across both interactive and headless active runtimes. Focused integration coverage now proves two shared clients can hydrate the same active session and observe the same runtime-originated update through the embedded server boundary. `shared/serverapi.SessionTranscriptPageRequest/Response`, `session.getTranscriptPage`, and `shared/clientui.TranscriptPage` are now also landed as the first dedicated committed-transcript read surface, with revision metadata sourced from persisted session sequence and used by the CLI as the authoritative transcript repair path instead of `session.getMainView`. Process ownership/read state remains live-only and in-memory rather than restart-durable.

- Remaining broader Phase 2 work from the migration requirements is still open: project registry and project-facing typed reads, ask and approval resource/read surfaces, fuller event/live-feed split including process-output streams, and the stricter event-hub/retention model required by the protocol spec.

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
- transcript paging/compression strategy for large-session rehydrate
- unified committed-transcript sync path so live session activity can gap without leaving detail/ongoing transcript state stale; baseline repair now uses `session.getTranscriptPage`, and the remaining work is revision-aware paging plus incremental fetch strategy; see `../analysis/transcript-sync-reliability.md`
- explicit stream-drop handling that forces rehydrate plus resubscribe
- process-control race coverage
- slow-client handling and bounded buffering
- transport-safe `shared/clientui.RuntimeClient` error semantics so frontend reads can distinguish RPC failure from real empty/idle state. Scope: replace the current zero-value fallback behavior in `cli/app/ui_runtime_client.go` with a shared contract change in `shared/clientui.RuntimeClient`, either by returning explicit read errors from runtime-view methods or by adding a last-known-good cache shape that carries freshness plus transport-failure metadata.
- transport-crossing runtime mutations must stop silently swallowing failures. Scope: remove the current fire-and-forget behavior in `cli/app/ui_runtime_client.go` and related mutation adapters; the Phase 4 design must either propagate mutation errors through the frontend boundary or provide an explicit shared error-reporting/retry channel that preserves user input and operator visibility.
- idempotent session lifecycle transitions end-to-end. Scope: add `client_request_id` to `shared/serverapi.SessionResolveTransitionRequest`, thread it through `cli/app/session_lifecycle.go`, and implement duplicate suppression in the server lifecycle path so retry-safe `fork_rollback`, `logout`, and future transition actions cannot be applied twice after disconnect/retry.

Primary risks:

- duplicate submissions after reconnect or retry
- slow-client memory growth
- ambiguous queue semantics during active runs

Rollback point:

- if any live stream proves too fragile under load, drop the stream and keep reconnect strictly snapshot/page based until a stronger read model exists

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
- protocol/documentation cleanup for deferred transport-surface follow-ups that were intentionally held out of Phase 3 review fixes, including any remaining JSON-RPC envelope tightening that v1 frontends actually need rather than speculative spec-width expansion

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
- Phase 2 foundation exit gate: second client can hydrate and observe one session in tests. Status: satisfied.
- Phase 3 exit gate: CLI works against embedded and external server through the same client boundary.
- Phase 4 exit gate: reconnect, approval races, and slow-subscriber failure modes are covered.
- Phase 5 exit gate: existing data adoption works and a non-CLI client can complete the baseline workflow set.
