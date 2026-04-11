# App Server Migration Plan

This is the current implementation-planning baseline derived from the migration requirements and external architecture review.

The plan is intentionally incremental. It keeps the product working throughout and avoids wrapping the current monolith in transport before the real boundaries exist.

## Phase 0: Freeze Behavior And Define Proof Obligations - DONE

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

## Phase 1: Create A Transport-Neutral Server API In Process - DONE

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

## Phase 2: Stabilize Resource Model, Hydration Views, And Event Hub - DONE

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

## Phase 3: Add Real Transport And Local Daemon Mode - DONE PARTIAL

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

Phase 3 temporarily scoped servers to workspaces.

## Phase 4: Storage Model Lock And Metadata Foundation

Goal:

Lock the hybrid persistence architecture and introduce the new metadata authority without mixing it with reconnect hardening or broad multi-client proof work.

This phase establishes the new durable model, the SQLite metadata store, and the server-global discovery identity that later phases depend on.

Deliverables:

- hybrid persistence spec and source-of-truth split locked
- SQLite selected as authoritative store for structured metadata/resources
- SQL-first storage tooling direction locked (`sqlc` + explicit SQL migrations)
- server identity/discovery no longer implies one workspace/project scope
- top-level durable model finalized as `project > workspace > worktree`
- workspace-first CLI startup and registration flow locked as the initial UX
- session execution target model finalized as `(workspace_id, worktree_id?, cwd_relpath)`
- explicit runtime lease model remains in scope, but implementation may follow the metadata cutover work

Primary risks:

- designing the new storage split too vaguely and re-opening it during implementation
- keeping `session.json` alive as a shadow authority
- dragging transcript-file redesign into the metadata migration

Rollback point:

- storage design artifacts can still be revised before live migration code lands

## Phase 4A: SQLite Metadata Introduction

Goal:

Introduce the SQLite metadata plane and workspace/project catalog without migrating old data yet.

Deliverables:

- app-global server identity and discovery
- SQLite schema for projects, workspaces, worktrees, sessions, runs, processes, asks/approvals, leases, and request deduplication
- storage layer based on explicit SQL plus `sqlc`
- session metadata authority moved behind the new storage layer even if legacy reads still exist during this subphase
- workspace/path-resolution queries and registration mutations
- CLI startup flow for unknown cwd specified against the new query/mutation surfaces

Primary risks:

- leaking old workspace-container assumptions into the new schema
- over-modeling unstable nested metadata into wide tables instead of JSON columns

Rollback point:

- keep SQLite-backed metadata behind the storage boundary until migration/cutover is ready

## Phase 4B: Staged Storage Migration And Session Metadata Cutover

Goal:

Perform the one-time storage migration from legacy workspace-container sessions into the hybrid model and remove `session.json` authority.

Deliverables:

- blocking startup migrator
- staged metadata build and validation before cutover
- final cutover into the new project/session artifact layout
- timestamped backup of the old tree after success
- `session.json` removed from migrated session directories
- lazy interactive session creation preserved under the new metadata authority
- workspace relocation surfaced as explicit rebind UX only

Primary risks:

- migration failure after partial cutover
- losing legacy session metadata that currently lives only in `session.json`
- startup regressions caused by lazy-session semantics changing accidentally

Rollback point:

- keep the timestamped backup tree and require successful verification before normal startup resumes

## Phase 4C: Execution Target And Lease Cutover

Goal:

Finish the new execution-target and runtime-lease model on top of the migrated storage foundation.

Deliverables:

- session execution target stored as shared server-owned metadata
- session hydration/status surfaces expose workspace/worktree context from SQLite metadata
- runtime activation/release redesigned around explicit `lease_id`
- duplicate-safe activate/release semantics
- reconnect reacquires a fresh lease after hydrate/attach
- process/runtime/session mutations route through the new metadata authority cleanly
- runtime-durable blank sessions remain launch-invisible until they gain user-meaningful state, so Phase 4C does not regress session pickers/startup resume by exposing freshly prepared empty sessions

Primary risks:

- runtime lease leaks
- hidden frontend-local workspace assumptions in runtime/tool preparation
- treating durable session-row existence as equivalent to user-visible resumable session state

Rollback point:

- keep transcript correctness recoverable through hydrate-plus-resubscribe even if execution-target UX is still incomplete

Storage migration scope ends at Phase 4C.

The remaining phases are post-storage hardening and proof work.

## Phase 5: Multi-Client Correctness And Reconnect Hardening

Goal:

With the storage migration complete, harden the actual multi-client and reconnect behavior.

Deliverables:

- two real clients from different workspaces attached to one server in tests
- two real clients attached to the same session on the same server
- deterministic approval and ask race handling
- reconnect with hydration-first recovery
- transcript paging/compression strategy for large-session rehydrate
- unified committed-transcript sync path so live session activity can gap without leaving detail/ongoing transcript state stale; baseline repair now uses `session.getTranscriptPage`, and the remaining work is revision-aware paging plus incremental fetch strategy; see `../analysis/transcript-sync-reliability.md`
- finalize the authoritative server-owned state model by proving committed transcript state, ephemeral live state, and projection-only render state stay explicitly separated across frontend and server boundaries
- one frontend committed-transcript cache per attached session, with ongoing mode, detail mode, and native ongoing scrollback reduced to derived projection state rather than parallel authorities
- one frontend live-transient state path for assistant deltas, reasoning deltas, transient busy state, and similar progressive UX concerns, kept separate from committed transcript hydration
- transcript-affecting live activity evolved toward commit notifications or equivalent revision-advance signals so clients do not depend on raw live-event replay for transcript correctness
- dedicated transcript hydration remains distinct from metadata/status hydration so `session.getMainView` does not regress into a second transcript transport
- loopback and remote active-session paths proven to obey the same transcript commit, hydrate, and freshness semantics rather than merely converging eventually by different rules
- explicit stream-drop handling that forces rehydrate plus resubscribe
- process-control race coverage
- slow-client handling and bounded buffering
- transport-safe `shared/clientui.RuntimeClient` error semantics so frontend reads can distinguish RPC failure from real empty/idle state. Scope: replace the current zero-value fallback behavior in `cli/app/ui_runtime_client.go` with a shared contract change in `shared/clientui.RuntimeClient`, either by returning explicit read errors from runtime-view methods or by adding a last-known-good cache shape that carries freshness plus transport-failure metadata.
- transport-crossing runtime mutations must stop silently swallowing failures. Scope: remove the current fire-and-forget behavior in `cli/app/ui_runtime_client.go` and related mutation adapters; the Phase 4 design must either propagate mutation errors through the frontend boundary or provide an explicit shared error-reporting/retry channel that preserves user input and operator visibility.
- idempotent session lifecycle transitions end-to-end. Scope: add `client_request_id` to `shared/serverapi.SessionResolveTransitionRequest`, thread it through `cli/app/session_lifecycle.go`, and implement duplicate suppression in the server lifecycle path so retry-safe `fork_rollback`, `logout`, and future transition actions cannot be applied twice after disconnect/retry.

Primary risks:

- runtime lease leaks or premature runtime release during disconnect/reconnect churn
- stale transcript or live-state divergence after the metadata cutover

Rollback point:

- keep live-stream correctness recoverable through hydrate-plus-resubscribe even if progressive stream behavior remains incomplete

## Phase 6: Standalone Polish And Boundary Proof

Goal:

Finish the migration by proving the CLI is now just one frontend.

Deliverables:

- project missing and inaccessible states handled cleanly
- CI boundary enforcement active
- acceptance suite able to run against external-daemon mode only
- final state-management proof that no frontend projection, pagination window, native transcript flush queue, or transport cache is treated as durable transcript truth
- non-CLI boundary proof against the migrated storage model
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
5. storage design and metadata foundation
6. staged migration and execution-target cutover
7. multi-client hardening
8. standalone proof

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
- Phase 4 exit gate: hybrid persistence, durable model, and startup/registration/storage-tooling direction are fully locked.
- Phase 4A exit gate: SQLite metadata plane exists behind the storage boundary and the server-global workspace/project model is queryable.
- Phase 4B exit gate: one-time staged migration succeeds and `session.json` no longer exists in migrated sessions.
- Phase 4C exit gate: session execution targets and runtime leases use the new metadata authority correctly.
- Phase 5 exit gate: reconnect, approval races, slow-subscriber failure modes, and the single-authority committed-transcript model are covered.
- Phase 6 exit gate: the migrated storage model is proven under external-daemon mode and a non-CLI client can complete the baseline workflow set.
