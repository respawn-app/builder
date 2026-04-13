# App Server Migration Plan

This is the current implementation-planning baseline derived from the migration requirements and external architecture review.

The plan is intentionally incremental. It keeps the product working throughout and avoids wrapping the current monolith in transport before the real boundaries exist.

## Still Open

The migration is not fully complete yet. The highest-signal unresolved gaps across Phases 1-6A are:

- Phase 2: full project / ask / approval resource surfaces, complete event-hub stream model, and broader idempotent mutation coverage
- Phase 4: unknown-cwd startup registration flow, create-project / attach-workspace flow, and explicit workspace rebind UX after relocation
- Phase 5A: transcript commit-notification / revision-advance contract and revision-aware incremental transcript fetch strategy
- Phase 5B: transcript paging/compression strategy for large-session rehydrate
- Phase 6B: root-cause elimination for the remaining transcript divergence bugs

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

## Phase 1: Create A Transport-Neutral Server API In Process

Goal:

Create the real application-service boundary before any network transport work starts.

Requirements:

- frontend code must reach server-owned behavior only through transport-neutral client interfaces
- boundary contracts must not leak runtime-native server types into CLI/TUI packages
- loopback/in-process wiring must exercise the same boundary shape future remote clients will use

Deliverables:

- [x] transport-neutral service layer for project, session, run, process, approval, and ask operations, with the first mandatory extracted slice being session launch/run for `builder run`
- [x] loopback or in-process client adapter that talks through that service layer
- [x] CLI switched onto the client-style boundary instead of direct runtime access, starting with `cli/builder/main.go:runSubcommand`
- [x] boundary enforcement preventing TUI or CLI packages from importing server internals directly

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
   Bubble Tea onboarding/auth screens, terminal bells, and direct process-control affordances remain frontend-owned UX adapters by design; the next concrete cut line is Phase 2 resource identity, hydration views, and stream semantics.

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

Requirements:

- resource identity must be explicit for the server-owned entities the frontend and transport operate on
- hydration reads must be typed, bounded, and separate from transient live streams
- live event delivery must have explicit gap semantics instead of silent loss
- durable transcript state and live/progressive state must remain separate consistency domains

Deliverables:

- [x] session, run, and process resource identities are established on the shared client/server boundary
- [x] one-active-primary-run invariant enforcement exists in the server-owned runtime model
- [x] typed hydration views exist for startup and session main view
- [x] the first event-hub slice exists with explicit session-activity gap semantics
- [x] process output buffering and retention policy exist for the current local-runtime surface
- [ ] project registry and project-facing typed reads are complete across startup, picker, and session flows
- [ ] ask resource identities and read surfaces are complete and transport-neutral
- [ ] approval resource identities and read surfaces are complete and transport-neutral
- [ ] event hub stream classes and retention semantics are complete, including process-output streaming on the real protocol boundary
- [ ] `client_request_id` idempotency support is consistent across the mutating resource surface rather than targeted endpoints only

Progress:

- The Phase 2 foundation slice is now complete, but broader Phase 2 remains open. The live runtime now exposes explicit active-run identity/status, `shared/clientui.RuntimeMainView` now bundles active-session hydration, `server/runtimeview` now owns the runtime-to-main-view projection plus the first server-owned active-session read service, the CLI consumes that bundled view through the client boundary, focused coverage now proves run-state payload semantics for completed and interrupted runs, the session log now persists minimal durable run lifecycle history for later `run.get`-style reads, `shared/serverapi` plus `server/sessionview` now provide the first transport-neutral `session.getMainView` / `run.get`-style application read service, dormant session reads now resolve through read-only `server/session.Snapshot` loading rather than mutating reopen paths, `server/registry` now owns reusable runtime and persistence resolution rather than keeping those registries trapped in `server/embedded`, live background processes now carry explicit owning session/run/step identity at creation time, and `shared/serverapi` plus `server/processview` now provide the first transport-neutral process read and control service subset that the CLI uses for `/ps` hydration plus kill/inline actions instead of projecting manager snapshots and control entirely inside `cli/app`. `process.kill` now carries `client_request_id` as a mutating contract; `process.inlineOutput` is on-boundary but remains read-like rather than mutating. `shared/serverapi` plus `server/sessionactivity` now also provide the first live session-activity subscription seam, backed by server-owned runtime registries and explicit lag failure rather than silent event loss across both interactive and headless active runtimes. Focused integration coverage now proves two shared clients can hydrate the same active session and observe the same runtime-originated update through the embedded server boundary. `shared/serverapi.SessionTranscriptPageRequest/Response`, `session.getTranscriptPage`, and `shared/clientui.TranscriptPage` are now also landed as the first dedicated committed-transcript read surface, with revision metadata sourced from persisted session sequence and used by the CLI as the authoritative transcript repair path instead of `session.getMainView`. Process ownership/read state remains live-only and in-memory rather than restart-durable.

- Remaining broader Phase 2 work from the migration requirements is still open and is tracked explicitly in the unchecked deliverables above.

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

Requirements:

- the real transport must expose the same server boundary used in loopback/embedded mode
- embedded mode must not regain privileged object access as a shortcut
- transport identity and attach lifecycle must be explicit and testable
- server startup/attach behavior must be deterministic and not rely on hidden local-only heuristics

Deliverables:

- [x] JSON-RPC-over-WebSocket gateway
- [x] handshake, capability exchange, and attachment lifecycle
- [x] health and readiness endpoint
- [x] `builder serve`
- [x] CLI attach-or-start logic against the real transport
- [x] ~local discovery on a well-known control endpoint or socket~
  Superseded by the locked direct-configured attach topology; do not implement this old deliverable.

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

Requirements:

- structured metadata must have one authoritative durable store
- session transcript/log bulk files must remain file-backed in this phase
- project/workspace/worktree identity and execution-target semantics must be explicit and durable
- workspace resolution is exact-match on canonical workspace roots; nested subdirectories remain unknown until explicitly attached
- startup/registration flows must operate through the server-owned metadata authority rather than hidden local shortcuts
- unknown-cwd interactive startup must branch into an explicit binding flow with create-project and attach-to-existing-project choices
- headless startup in an unregistered workspace must fail fast and point users/agents at explicit recovery commands rather than inventing hidden project/workspace state

Deliverables:

- [x] hybrid persistence spec and source-of-truth split are locked
- [x] SQLite is selected as the authoritative store for structured metadata/resources
- [x] SQL-first storage tooling direction is locked (`sqlc` + explicit SQL migrations)
- [x] server identity and direct attach topology no longer imply one workspace/project scope
- [x] top-level durable model is finalized as `project > workspace > worktree`
- [x] session execution target model is finalized as `(workspace_id, worktree_id?, cwd_relpath)`
- [x] explicit runtime lease model is implemented on the metadata-backed runtime path
- [ ] workspace-first CLI startup and registration flow is implemented end-to-end, including unknown-cwd project picker / create-project / attach-workspace flows
- [ ] headless unknown-workspace failure path is implemented with short recovery guidance using `builder project [path]`, `builder attach [path]`, and `builder attach --project <project-id> [path]`

Primary risks:

- designing the new storage split too vaguely and re-opening it during implementation
- keeping `session.json` alive as a shadow authority
- dragging transcript-file redesign into the metadata migration

Rollback point:

- storage design artifacts can still be revised before live migration code lands

Status:

- Phase 4A-4C storage/model work is largely landed in the current branch.
- Phase 4D topology/direct-attach work is largely landed.
- The remaining Phase 4 gap is the real unknown-cwd startup and registration flow over the server boundary; until that lands, Phase 4 is not actually complete.

## Phase 4A: SQLite Metadata Introduction

Goal:

Introduce the SQLite metadata plane and workspace/project catalog without migrating old data yet.

Requirements:

- metadata schema must represent the locked durable model without preserving legacy workspace-container assumptions as authority
- storage access must be SQL-first and type-generated rather than reflection/ORM driven
- registration/path-resolution capabilities must be available behind the metadata boundary before cutover

Deliverables:

- [x] app-global server identity foundation exists
- [x] SQLite schema exists for projects, workspaces, worktrees, sessions, runs, processes, asks/approvals, leases, and request deduplication
- [x] storage layer is based on explicit SQL plus `sqlc`
- [x] session metadata authority moved behind the new storage layer
- [x] workspace registration mutation exists in the metadata authority
- [ ] workspace/path-resolution queries are exposed end-to-end through the startup boundary
- [ ] CLI startup flow for unknown cwd is implemented against those query/mutation surfaces

Primary risks:

- leaking old workspace-container assumptions into the new schema
- over-modeling unstable nested metadata into wide tables instead of JSON columns

Rollback point:

- keep SQLite-backed metadata behind the storage boundary until migration/cutover is ready

## Phase 4B: Staged Storage Migration And Session Metadata Cutover

Goal:

Perform the one-time storage migration from legacy workspace-container sessions into the hybrid model and remove `session.json` authority.

Requirements:

- migration must be blocking, verified, and non-silent
- migrated sessions must no longer depend on `session.json` as authority
- post-migration session creation and startup semantics must preserve the intended lazy visibility behavior
- relocation must remain explicit user action rather than automatic rebinding

Deliverables:

- [x] blocking startup migrator
- [x] staged metadata build and validation before cutover
- [x] final cutover into the new project/session artifact layout
- [x] timestamped backup of the old tree after success
- [x] `session.json` removed from migrated session directories
- [x] lazy interactive session creation preserved under the new metadata authority
- [ ] workspace relocation is surfaced as explicit rebind UX only

Primary risks:

- migration failure after partial cutover
- losing legacy session metadata that currently lives only in `session.json`
- startup regressions caused by lazy-session semantics changing accidentally

Rollback point:

- keep the timestamped backup tree and require successful verification before normal startup resumes

## Phase 4C: Execution Target And Lease Cutover

Goal:

Finish the new execution-target and runtime-lease model on top of the migrated storage foundation.

Requirements:

- session execution target must be stored as server-owned metadata rather than frontend-local state
- runtime activation/release must be explicit, duplicate-safe, and reconnect-safe
- durable blank sessions must not leak into launch/session-picking UX before they become user-meaningful

Deliverables:

- [x] session execution target stored as shared server-owned metadata
- [x] session hydration/status surfaces expose workspace/worktree context from SQLite metadata
- [x] runtime activation/release redesigned around explicit `lease_id`
- [x] duplicate-safe activate/release semantics
- [x] reconnect reacquires a fresh lease after hydrate/attach
- [x] process/runtime/session mutations route through the new metadata authority cleanly
- [x] runtime-durable blank sessions remain launch-invisible until they gain user-meaningful state

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

Requirements:

- the daemon must be app-global per persistence root rather than workspace-scoped
- attach must use configured host/port directly with no discovery artifact, no fallback port, and no silent rebinding
- project/workspace context resolution must happen over server-owned queries and attachments after transport attach
- unknown-cwd startup must enter explicit registration/project-selection flow rather than crashing or auto-registering
- headless unknown-cwd startup must fail fast with explicit operator/agent recovery guidance instead of auto-registering hidden project/workspace state

Deliverables:

- [x] persisted daemon-discovery artifacts are removed from the target architecture; client attach uses configured `server_host` + `server_port` directly
- [x] `protocol.ServerIdentity` stops implying one hosted `project_id` / `workspace_root`; it describes the server process and capabilities only
- [x] server core composition stops binding itself to a single workspace/project during startup
- [x] one daemon can host multiple registered projects and accept `project.attach` for any hosted project
- [x] CLI attach-or-start dials the configured daemon address first for already-registered workspaces
- [x] serve/transport/startup tests prove one configured daemon can be used from multiple workspace roots under one persistence root
- [x] topology cutover is hard: no migration script or bridge mode for the old workspace-scoped discovery-file model
- [ ] cwd/project/workspace resolution over server-owned path-resolution and registration queries is complete for unknown workspaces, not only already-registered ones
- [ ] unknown-cwd startup and registration flow works end-to-end over the remote/loopback boundary without `project id is required` crash paths
- [ ] headless unregistered-workspace failures mention the explicit recovery path via `builder project [path]`, `builder attach [path]`, and `builder attach --project <project-id> [path]`

### Phase 4D.a: Workspace Binding UX And Headless Recovery

Goal:

Finish the missing user/agent-facing binding behavior on top of the already-landed app-global daemon and metadata model.

Requirements:

- interactive unknown-cwd startup must enter a post-auth binding flow rather than erroring
- the binding flow must offer create-new-project first, then a clearly separated attach-to-existing-project section
- existing-project rows must surface a meaningful preview derived from the project's main workspace path
- headless unknown-cwd flows must fail fast with explicit, short self-recovery instructions
- workspace binding inspection and mutation needed for agent recovery must be available as explicit CLI commands rather than implicit background behavior

Deliverables:

- [ ] post-auth `binding` flow exists for interactive unknown-cwd startup
- [ ] create-project path pre-fills the editable project name from the cwd directory name and continues into a new session after binding
- [ ] existing-project picker rows use project preview paths derived from the main/earliest workspace root
- [ ] existing-project selection can explicitly bind the current workspace to that project and continue startup
- [ ] `builder project [path]` resolves the project bound to a path, defaulting to `cwd`
- [ ] `builder attach [path]` binds a workspace to the project already bound to `cwd`, while `builder attach --project <project-id> [path]` provides an explicit project-id override
- [ ] headless unregistered-workspace errors mention the recovery commands in a short guide

Acceptance proof:

- [ ] interactive startup tests cover the chosen existing-project attach branch for unknown cwd (`immediate attach+continue` or `confirm then attach+continue`)
- [ ] headless unregistered-workspace tests assert the fail-fast error text includes the recovery commands and does not auto-create bindings
- [ ] CLI command tests cover `builder project [path]`, path-first `builder attach [path]`, and explicit `builder attach --project <project-id> [path]` flows, including default-`cwd` behavior

Primary risks:

- leaving hidden workspace-scoped assumptions in startup, serve UX, or transport tests
- accidentally introducing a second project/workspace source of truth outside the metadata authority
- mixing topology cutover with unrelated transcript or UI work and making verification noisy

Rollback point:

- keep the already-landed Phase 4A-4C metadata model intact while replacing daemon attach/startup paths incrementally

Storage migration scope ends at Phase 4C; topology/direct-attach cutover completes in Phase 4D.

Status: direct attach and multi-project hosting are landed, but the unknown-cwd registration/startup deliverables above keep Phase 4D open.

The remaining phases are post-storage hardening and proof work.

## Phase 5: Multi-Client Correctness And Reconnect Hardening

Goal:

With the storage migration complete, harden transcript correctness, reconnect behavior, and then prove the app behaves correctly with multiple attached clients in realtime.

This phase is intentionally split. The first subphase locks the frontend/server transcript architecture and failure semantics so ongoing mode cannot keep regressing under reconnect or stream gaps. The second subphase proves that architecture under true multi-client realtime operation.

Requirements:

- committed transcript correctness must not depend on lossless live stream delivery
- reconnect and stream-gap recovery must be authoritative rather than heuristic
- multi-client proof must validate that different clients observe the same committed truth under churn and races

Status: the transcript architecture and proof slices are largely landed, but the unchecked deliverables below keep Phase 5 open.

## Phase 5A: Transcript Authority, Recovery, And Failure Semantics

Status: most of the subphase is landed, but the unchecked deliverables below keep Phase 5A open.

Goal:

Lock the committed-transcript architecture and failure semantics so ongoing mode, detail mode, and reconnect paths share one transcript truth model.

Requirements:

- committed transcript state must have one authoritative hydrate-and-project path in the frontend
- live streams may accelerate UX but must never be required for committed transcript correctness
- external continuity loss must rehydrate from authority; same-session divergence must remain a bug to eliminate
- transcript-affecting failures must be surfaced and must not degrade into fake empty/idle state

Deliverables:

- [x] unified committed-transcript sync path exists so live session activity gaps no longer leave detail/ongoing transcript state permanently stale; baseline repair now uses `session.getTranscriptPage`
- [x] committed transcript state, ephemeral live state, and projection-only render state are explicitly separated across frontend and server boundaries
- [x] one frontend committed-transcript cache per attached session exists, with ongoing mode, detail mode, and native ongoing scrollback reduced to derived projection state rather than parallel authorities
- [x] one frontend live-transient state path exists for assistant deltas, reasoning deltas, transient busy state, and similar progressive UX concerns, kept separate from committed transcript hydration
- [x] ongoing-mode normal-buffer scrollback is committed-only by contract
- [x] dedicated transcript hydration remains distinct from metadata/status hydration so `session.getMainView` does not regress into a second transcript transport
- [x] explicit stream-drop handling invalidates live transient state immediately and forces rehydrate plus resubscribe before live UX resumes
- [x] transport-crossing runtime reads and mutations stop swallowing failures or degrading into fake empty/idle state; transcript-affecting failures stop the affected view and recover from committed transcript once connectivity returns
- [x] external-continuity-loss recovery is locked: same-session logical divergence is not normalized into redraw behavior
- [x] process-control race coverage exists
- [x] slow-client handling and bounded buffering exist
- [ ] transcript-affecting live activity has evolved all the way to explicit commit notifications or equivalent revision-advance signals so clients do not depend on generic live-event inference for transcript correctness
- [ ] revision-aware paging plus incremental fetch strategy beyond the current baseline tail hydration is implemented

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

Status: most of the proof matrix is landed, but the unchecked deliverables below keep Phase 5B open.

Goal:

Prove the Phase 5A transcript/reconnect architecture under real multi-client attachment and concurrent operator behavior.

Requirements:

- proof must cover true remote multi-client attachment, not only loopback equivalence
- concurrent ask/approval/lifecycle operations must be deterministic and retry-safe
- large-session recovery must remain bounded and practical rather than relying on replaying entire live histories

Deliverables:

- [x] two real clients from different workspaces attached to one server in tests
- [x] two real clients attached to the same session on the same server
- [x] deterministic approval and ask race handling
- [x] reconnect with hydration-first recovery
- [x] loopback and remote active-session paths are proven to obey the same transcript commit, hydrate, and freshness semantics rather than merely converging eventually by different rules
- [x] idempotent session lifecycle transitions exist end-to-end via `client_request_id`
- [ ] transcript paging/compression strategy for large-session rehydrate is implemented and proven

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

Requirements:

- product semantics must be reflected directly in code/docs/tests rather than inferred from old implementation behavior
- same-session correctness bugs must be fixed at root cause rather than normalized via redraw/recovery behavior
- any continuity-loss redraw allowance must stay narrowly scoped to the approved external-failure class

## Phase 6A: Align Transcript Semantics With Product Contract

Status: semantics alignment is landed.

Goal:

Fix any remaining code, tests, and documentation that still model compaction or rollback/fork as same-session transcript mutation.

Requirements:

- compaction must be treated as ordinary same-session committed progression for frontend sync semantics
- rollback/fork must be treated as navigation to a different session target, not same-session mutation
- only external continuity loss may authorize ongoing-buffer re-issue in TUI

Deliverables:

- [x] compaction is explicitly modeled as ordinary same-session committed transcript progression for frontend sync purposes, not as a same-session transcript rewrite requiring non-append recovery
- [x] rollback/fork is explicitly modeled as navigation or attachment to a different session target, not as same-session transcript mutation
- [x] ongoing recovery semantics are narrowed so only external continuity-loss causes permit authoritative re-issue of the TUI ongoing buffer
- [x] `cli/app/ui_native_history.go` recovery path no longer maps same-session logical divergence to redraw/replay semantics
- [x] diagnostics, tests, and design docs have been updated to stop describing compaction/rollback as same-session non-append transcript rewrites
- [x] gap/reconnect recovery documentation distinguishes external continuity loss from client-side logical divergence

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
- Phase 2 foundation checkpoint: second client can hydrate and observe one session in tests.
- Phase 3 exit gate: CLI works against embedded and external server through the same client boundary.
- Phase 4 exit gate: hybrid persistence, durable model, and startup/registration/storage-tooling direction are fully locked.
- Phase 4A exit gate: SQLite metadata plane exists behind the storage boundary and the server-global workspace/project model is queryable.
- Phase 4B exit gate: one-time staged migration succeeds and `session.json` no longer exists in migrated sessions.
- Phase 4C exit gate: session execution targets and runtime leases use the new metadata authority correctly.
- Phase 4D exit gate: direct attach uses configured `server_host` + `server_port`; handshake identity is process-scoped; one daemon can host multiple projects; CLI startup dials the configured daemon first and resolves cwd/project context over server-owned queries instead of workspace-scoped discovery heuristics or persisted discovery files.
- Phase 5A exit gate: ongoing/detail/reconnect share one committed-transcript authority model; ongoing scrollback is committed-only; stream drops invalidate transient live state and recover via committed hydration plus resubscribe; transcript-affecting failures are not swallowed.
- Phase 5B exit gate: reconnect, approval races, slow-subscriber failure modes, and the single-authority committed-transcript model are covered under realtime multi-client attachment.
- Phase 6A exit gate: code/docs/tests no longer describe compaction as same-session transcript rewrite or rollback/fork as same-session mutation; external continuity-loss recovery is the only accepted TUI re-issue path.
- Phase 6B exit gate: the known transcript divergence bug class is root-caused rather than normalized by redraw, and the focused reproduction matrix for user/tool/assistant committed visibility is green.
- Phase 7 exit gate: the migrated storage model is proven under external-daemon mode and a non-CLI client can complete the baseline workflow set.
