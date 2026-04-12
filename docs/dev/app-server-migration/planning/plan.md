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

This phase establishes the new durable model, the SQLite metadata store, and the app-global daemon topology that later phases depend on.

Deliverables:

- hybrid persistence spec and source-of-truth split locked
- SQLite selected as authoritative store for structured metadata/resources
- SQL-first storage tooling direction locked (`sqlc` + explicit SQL migrations)
- server identity and direct attach topology no longer imply one workspace/project scope
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

Status:

- Phase 4A-4C storage/model work is largely landed in the current branch.
- The remaining unfinished Phase 4 work is topology/attach cutover: the daemon is still workspace-scoped in handshake identity and attach/startup resolution paths, and the old discovery-file direction has now been rejected.
- That remaining slice is now tracked explicitly as Phase 4D and should execute before Phase 6.

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

## Phase 4D: App-Global Direct Attach And Multi-Project Daemon Cutover

Goal:

Finish the topology cutover that Phase 4 always intended: client and daemon connect over the explicitly configured server address first, then project/workspace context is resolved over server-owned queries and attachments.

This phase does not redesign persistence again. It closes the remaining workspace-scoped daemon assumptions above the already-landed metadata model.

Detailed implementation planning for this slice lives in `phase-4d-plan.md`.

Deliverables:

- persisted daemon-discovery artifacts are removed from the target architecture; client attach uses configured `server_host` + `server_port` directly
- `protocol.ServerIdentity` stops implying one hosted `project_id` / `workspace_root`; it describes the server process and capabilities only
- server core composition stops binding itself to a single workspace/project during startup
- one daemon can host multiple registered projects and accept `project.attach` for any hosted project
- CLI attach-or-start dials the configured daemon address first, then resolves cwd/project/workspace context over server-owned path-resolution and registration queries
- unknown-cwd startup and registration flow work against the remote/loopback server boundary rather than local workspace-bound heuristics or persisted discovery files
- serve/transport/startup tests prove one configured daemon can be used from multiple workspace roots under one persistence root
- topology cutover is hard: no migration script or bridge mode for the old workspace-scoped discovery-file model

Primary risks:

- leaving hidden workspace-scoped assumptions in startup, serve UX, or transport tests
- accidentally introducing a second project/workspace source of truth outside the metadata authority
- mixing topology cutover with unrelated transcript or UI work and making verification noisy

Rollback point:

- keep the already-landed Phase 4A-4C metadata model intact while replacing daemon attach/startup paths incrementally

Storage migration scope ends at Phase 4C; topology/direct-attach cutover completes in Phase 4D.

The remaining phases are post-storage hardening and proof work.

## Phase 5: Multi-Client Correctness And Reconnect Hardening

Goal:

With the storage migration complete, harden transcript correctness, reconnect behavior, and then prove the app behaves correctly with multiple attached clients in realtime.

This phase is intentionally split. The first subphase locks the frontend/server transcript architecture and failure semantics so ongoing mode cannot keep regressing under reconnect or stream gaps. The second subphase proves that architecture under true multi-client realtime operation.

Status: both Phase 5A and Phase 5B exit gates are satisfied in the current branch. Phase 4D remains the next migration step before Phase 6 implementation.

## Phase 5A: Transcript Authority, Recovery, And Failure Semantics

Status: exit gate satisfied in the current branch.

Goal:

Lock the committed-transcript architecture and failure semantics so ongoing mode, detail mode, and reconnect paths share one transcript truth model.

Deliverables:

- unified committed-transcript sync path so live session activity can gap without leaving detail/ongoing transcript state stale; baseline repair now uses `session.getTranscriptPage`, and the remaining work is revision-aware paging plus incremental fetch strategy; see `../analysis/transcript-sync-reliability.md`
- finalize the authoritative server-owned state model by proving committed transcript state, ephemeral live state, and projection-only render state stay explicitly separated across frontend and server boundaries
- one frontend committed-transcript cache per attached session, with ongoing mode, detail mode, and native ongoing scrollback reduced to derived projection state rather than parallel authorities
- one frontend live-transient state path for assistant deltas, reasoning deltas, transient busy state, and similar progressive UX concerns, kept separate from committed transcript hydration
- ongoing-mode normal-buffer scrollback committed-only by contract: replay committed history at startup, append only new committed transcript suffixes afterward, and never emit provisional live activity into immutable scrollback
- transcript-affecting live activity evolved toward commit notifications or equivalent revision-advance signals so clients do not depend on raw live-event replay for transcript correctness
- dedicated transcript hydration remains distinct from metadata/status hydration so `session.getMainView` does not regress into a second transcript transport
- explicit stream-drop handling that invalidates live transient state immediately and forces rehydrate plus resubscribe before live UX resumes
- transport-crossing runtime reads and mutations stop swallowing failures or degrading into fake empty/idle state; transcript-affecting failures must stop the affected view and recover from committed transcript once connectivity returns
- external-continuity-loss recovery locked: if transport continuity is externally broken (disconnect, stream gap, client/server restart, subscription invalidation), recovery happens from fresh committed hydrate; for this category only, TUI ongoing may re-issue its scrollback surface from authoritative state. Same-session logical divergence remains a product bug to eliminate, not an acceptable redraw path
- process-control race coverage
- slow-client handling and bounded buffering

Acceptance checklist before implementation of later 5A slices continues:

- rendered proof that ongoing normal-buffer scrollback appends only committed transcript entries and never emits provisional live activity into immutable scrollback
- rendered proof that a stream gap or subscription loss invalidates transient ongoing live state immediately instead of leaving stale transcript-visible UI behind
- rendered proof that external continuity-loss recovery rehydrates from authoritative committed state and that TUI may re-issue the ongoing buffer for that recovery class only
- rendered proof that same-session logical divergence does not get normalized into silent redraw behavior; debug mode still hard-fails invariant violations during development
- rendered proof that transcript-affecting transport failures are surfaced, stop the affected live transcript view, and recover only after successful committed hydrate plus resubscribe
- loopback and remote paths both obey the same committed-suffix repair semantics for transcript-critical flows

Primary risks:

- ongoing-mode scrollback still depending on live replay for transcript truth
- stale transcript or live-state divergence after the metadata cutover
- reconnect paths preserving a visually plausible but incorrect transcript tail

Rollback point:

- keep transcript correctness recoverable through committed hydration plus resubscribe even if progressive live UX remains temporarily degraded

## Phase 5B: Realtime Multi-Client Proof

Status: exit gate satisfied in the current branch.

Goal:

Prove the Phase 5A transcript/reconnect architecture under real multi-client attachment and concurrent operator behavior.

Deliverables:

- two real clients from different workspaces attached to one server in tests
- two real clients attached to the same session on the same server
- deterministic approval and ask race handling
- reconnect with hydration-first recovery
- transcript paging/compression strategy for large-session rehydrate
- loopback and remote active-session paths proven to obey the same transcript commit, hydrate, and freshness semantics rather than merely converging eventually by different rules
- idempotent session lifecycle transitions end-to-end. Scope: add `client_request_id` to `shared/serverapi.SessionResolveTransitionRequest`, thread it through `cli/app/session_lifecycle.go`, and implement duplicate suppression in the server lifecycle path so retry-safe `fork_rollback`, `logout`, and future transition actions cannot be applied twice after disconnect/retry.

Current proof surface:

- `cli/app/session_server_target_test.go`
  - `TestRemoteInteractiveRuntimeTwoClientsConvergeOnSameSessionAcrossWorkspaces`
  - `TestRemoteInteractiveRuntimeReconnectHydratesCommittedTranscriptAcrossWorkspaces`
  - `TestRemoteInteractiveRuntimeAskRaceFirstWinsAcrossWorkspaces`
  - `TestRemoteInteractiveRuntimeApprovalRaceFirstWinsAcrossWorkspaces`
  - `TestRemoteInteractiveRuntimeResolveTransitionForkRollbackDeduplicatesAcrossWorkspaces`
  - `TestRemoteSessionActivitySlowSubscriberGapHydratesAndResubscribesAcrossWorkspaces`

Supporting persistence regression coverage:

- `server/session/fileless_metadata_test.go`
  - `TestForkAtUserMessagePreservesPersistenceOptions`

Primary risks:

- runtime lease leaks or premature runtime release during disconnect/reconnect churn
- cross-client concurrency surfacing transcript, approval, or lifecycle races that single-client reconnect tests miss

Rollback point:

- Phase 5A remains a valid landing zone even if the full multi-client proof matrix needs more time

## Phase 6: Transcript Semantics Alignment And Root-Cause Elimination

Goal:

Remove the remaining incorrect transcript assumptions before final standalone proof work. This phase explicitly separates product-semantic alignment from bug elimination so we stop papering over logic mistakes with recovery behavior.

This phase is intentionally split. Phase 6A aligns code/docs with the product contract for compaction, rollback, and external continuity loss. Phase 6B removes the actual client-side divergence bugs that are currently causing missed user messages, tool lifecycle entries, and other committed transcript holes.

## Phase 6A: Align Transcript Semantics With Product Contract

Goal:

Fix any remaining code, tests, and documentation that still model compaction or rollback/fork as same-session transcript mutation.

Deliverables:

- compaction explicitly modeled as ordinary same-session committed transcript progression for frontend sync purposes, not as a same-session transcript rewrite requiring non-append recovery
- rollback/fork explicitly modeled as navigation or attachment to a different session target, not as same-session transcript mutation
- ongoing recovery semantics narrowed so only external continuity-loss causes permit authoritative re-issue of the TUI ongoing buffer
- `cli/app/ui_native_history.go` recovery path (`emitNonContiguousNativeProjectionRecovery` / `emitForcedNativeProjectionReplay`) redesigned or removed so same-session logical divergence no longer maps to redraw/replay semantics
- diagnostics, tests, and design docs updated to stop describing compaction/rollback as same-session non-append transcript rewrites
- gap/reconnect recovery documentation updated to distinguish external continuity loss from client-side logical divergence

Primary risks:

- preserving old wording/tests that keep pushing implementation toward the wrong sync model
- leaving navigation paths or runtime events that still smuggle rollback in as same-session rewrite semantics

Rollback point:

- keep the code on the current transport/persistence architecture while the semantic cleanup lands incrementally

## Phase 6B: Eliminate Transcript Divergence Root Causes

Goal:

Remove the actual client-side transcript correctness bugs instead of compensating for them with redraw/recovery logic.

Deliverables:

- eliminate heuristic or render-shape-based transcript deduplication on the frontend
- make committed transcript reconciliation identity/order/revision based across live apply and hydrate apply paths
- remove known user-message, tool lifecycle, and committed-tail race conditions between live events and authoritative hydrate
- tighten pagination, overlap, and committed-suffix accounting so committed transcript windows cannot silently drop or suppress rows
- add focused regression coverage for the currently reproducing failures: user-message commit, tool start/output/finalize, assistant final answer, compaction notice visibility, and concurrent-client interleaving
- keep external continuity-loss recovery as the fallback for Category C only, while Category A logic bugs fail loudly in debug and are measured explicitly in diagnostics

Primary risks:

- treating fallback recovery as success and never actually shrinking divergence frequency
- overfitting the fix to TUI symptoms instead of shared frontend transcript state machinery needed by desktop and web

Rollback point:

- retain the current authoritative hydrate path and diagnostics while replacing individual buggy reconciliation rules incrementally

## Phase 7: Standalone Polish And Boundary Proof

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
7. app-global direct attach and multi-project daemon cutover
8. transcript semantics alignment
9. transcript root-cause elimination
10. standalone proof

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
- Phase 4D exit gate: direct attach uses configured `server_host` + `server_port`; handshake identity is process-scoped; one daemon can host multiple projects; CLI startup dials the configured daemon first and resolves cwd/project context over server-owned queries instead of workspace-scoped discovery heuristics or persisted discovery files.
- Phase 5A exit gate: ongoing/detail/reconnect share one committed-transcript authority model; ongoing scrollback is committed-only; stream drops invalidate transient live state and recover via committed hydration plus resubscribe; transcript-affecting failures are not swallowed. Status: satisfied.
- Phase 5B exit gate: reconnect, approval races, slow-subscriber failure modes, and the single-authority committed-transcript model are covered under realtime multi-client attachment. Status: satisfied.
- Phase 6A exit gate: code/docs/tests no longer describe compaction as same-session transcript rewrite or rollback/fork as same-session mutation; external continuity-loss recovery is the only accepted TUI re-issue path.
- Phase 6B exit gate: the known transcript divergence bug class is root-caused rather than normalized by redraw, and the focused reproduction matrix for user/tool/assistant committed visibility is green.
- Phase 7 exit gate: the migrated storage model is proven under external-daemon mode and a non-CLI client can complete the baseline workflow set.
