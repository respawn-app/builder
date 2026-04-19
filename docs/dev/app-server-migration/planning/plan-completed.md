# Completed App Server Migration Work

This file archives phases that are complete so `plan.md` can stay focused on open work.

## Completed Phases

### Hard-Cut Rollback: Remove SQLite-Backed Request Dedup Persistence

Completed.

Outcome:

- SQLite-backed persisted dedup was hard-cut back out of production code and storage contracts
- `client_request_id` remains on API surfaces where still intended, but no shipped behavior depends on SQLite replay authority
- rollback-related tests and docs were reconciled to the non-persisted duplicate-protection model
- the one-off local operator follow-through for Nek was executed during the rollback slice

### Phase 0: Freeze Behavior And Define Proof Obligations

Completed.

Outcome:

- behavior-preservation baseline defined
- persistence and workflow audit completed
- proof obligations and characterization groundwork established

### Phase 1: Create A Transport-Neutral Server API In Process

Completed.

Outcome:

- frontend/server boundary established behind transport-neutral client APIs
- loopback/in-process client path landed
- CLI/TUI no longer depend on direct runtime ownership as the main architectural path

### Phase 3: Add Real Transport And Local Daemon Mode

Completed.

Outcome:

- JSON-RPC-over-WebSocket gateway landed
- handshake and attach lifecycle landed
- `builder serve` landed
- CLI attach-or-start over real transport landed

### Phase 4: Storage Model Lock And Metadata Foundation

Completed.

Outcome:

- hybrid persistence model locked
- SQLite metadata authority landed
- one-time session metadata cutover landed
- runtime lease and execution-target cutover landed
- app-global direct attach topology landed
- explicit workspace binding UX and headless recovery commands landed

Subphases completed:

- Phase 4A: SQLite metadata introduction
- Phase 4B: staged storage migration and session metadata cutover
- Phase 4C: execution target and lease cutover
- Phase 4D: app-global direct attach and multi-project daemon cutover
- Phase 4E: workspace binding UX and headless recovery

### Phase 5A: Transcript Authority, Recovery, And Failure Semantics

Completed.

Outcome:

- committed transcript authority model landed
- ongoing/detail/reconnect flows now hydrate from the same committed transcript source
- explicit commit notifications and revision-aware tail hydration landed
- transport failures and stream gaps now recover through authoritative transcript state

### Phase 5B: Realtime Multi-Client Proof

Completed.

Outcome:

- multi-client realtime tests landed
- reconnect, ask, approval, and lifecycle race coverage landed
- remote and loopback active-session paths now share the same correctness model

### Phase 6A: Align Transcript Semantics With Product Contract

Completed.

Outcome:

- compaction modeled as same-session committed progression
- rollback/fork modeled as navigation to a different session target
- TUI ongoing-buffer reissue restricted to external continuity-loss cases only

### Phase 6B: Transcript Divergence Hardening

Completed.

Outcome:

- ordinary successful turns no longer hydrate unless continuity was actually lost
- committed transcript truth is now derived from authoritative transcript pages plus explicitly committed runtime events
- current TUI ongoing/transcript stability regressions were hardened at the runtime, hydration, and rendered-surface layers rather than being left as compensating UI behavior
- the live reproduction matrix for ordinary turns, tool rows, committed user rows, compaction notices, commentary streaming, and assistant-final cleanup is green

Detailed landed slices:

- runtime event intake no longer coalesces `assistant_delta` / `reasoning_delta` before UI application
- runtime-committed assistant/tool/reviewer rows with explicit transcript coordinates now apply as committed transcript state immediately without waiting for hydrate
- committed tool-start events now replace matching transient tool rows instead of being treated as already-applied; rendered ongoing/committed surfaces keep the resolved tool block visible without hydrate once the committed result arrives
- ordinary committed user flushes are now explicitly asserted on the committed ongoing surface without hydrate while later commentary/tool events continue streaming on top
- deferred committed user flushes are now explicitly proved on the rendered ongoing surface: they stay hidden while the assistant is still live, then merge into the committed transcript without hydrate once the assistant final catches up
- the committed-path regression matrix now has targeted coverage for queued user flush, tool start, resolved tool path/finalize, assistant final answer, and reviewer terminal message on the projected-runtime surfaces
- the `conversation_updated` hydration contract is now explicit in tests: plain updates never hydrate, matching committed updates skip hydrate, and only real committed continuity loss takes the committed-conversation sync path
- bootstrap authoritative refresh is now race-covered: a stale bootstrap page cannot overwrite a later committed runtime event that arrived while the refresh was in flight
- concurrent-client interleaving is now race-covered: a hydrating client and a live client converge to the same committed snapshot without duplicate rows when the same committed advancement reaches them through refresh vs live-event paths
- ordinary authoritative hydrates no longer clear/replay the TUI normal-buffer scrollback; only Category C continuity recovery paths are still allowed to rebuild committed ongoing history
- compaction completed/failed no longer synthesize transcript rows before persistence; the persisted `local_entry_added` event is the sole transcript source for compaction notices/errors
- bare committed `conversation_updated` suppression is now replacement-safe: it only collapses into a following rich event when both represent the same committed tail count
- ongoing error refresh no longer relies on broad plain `conversation_updated`; the dedicated `ongoing_error_updated` event now drives authoritative set/clear refresh
- regression coverage exists for the committed-coordinate runtime row path and the compaction single-authority path
- rendered committed-ongoing regression coverage exists for the queued-follow-up visibility/order lifecycle before the assistant-final / queued-user slice

### Phase 7: Standalone Polish And Boundary Proof

Completed.

Outcome:

- rollback selection targeting/highlighting is fixed and covered
- release-blocking startup and attach failure states are covered by tests
- external-daemon acceptance proof is green
- frontend caches/projection state are explicitly treated as non-durable transcript views
- direct-address attach and fail-fast workspace binding docs are reconciled

### Phase 2 Residual: Implemented Slices

Completed implementation slices from the broader Phase 2 residual are archived here so `plan.md` can stay focused on the remaining proof/cleanup work.

#### 2R.1 Required project/session reads for current TUI startup

Outcome:

- current TUI startup, project picker, and session picker hydrate from typed project/session reads without CLI-local metadata stitching
- project/session picker flows consume the same transport-neutral DTOs in loopback and real-server mode
- dormant project/session picker hydration is regression-covered as side-effect-free
- obsolete CLI-local persistence bootstrap and lifecycle helper paths on the startup path were removed in favor of server-owned reads

#### 2R.2 Single-server and single-controller guardrails

Outcome:

- one app-server process per persistence root is enforced explicitly
- same-session mutation/control is temporarily restricted to one controlling client while preserving attach/read access for others
- the temporary restriction is documented as a scoped shipping simplification, not the target multi-client contract

#### 2R.3 TUI-critical live surface contracts

Outcome:

- retained TUI-facing live surfaces are locked for this phase: session activity, prompt activity, ask/approval routes, process inspection/control, and required process output
- loopback and real transport preserve the same live-surface semantics for the current TUI
- gap/backpressure behavior is defined around rehydrate/resubscribe rather than silent loss

#### 2R.4 `client_request_id` idempotency expansion for TUI-critical mutations

Outcome:

- persisted deduplication now covers session launch, headless run prompt, prompt answers, process kill, session lifecycle draft/transition mutations, and the current runtime-control write surface
- duplicate retries replay deterministically, mismatched payloads reject cleanly, and cancellations are not cached as success
- persisted idempotency for `sessionruntime.activate` / `sessionruntime.release` is explicitly deferred to a later dedicated session-control slice

#### 2R.5 Phase proof and rollout

Outcome:

- remaining intentional `cli/* -> builder/server/*` imports were audited and documented as a temporary shared-runtime adapter set rather than persistence-boundary leaks; persistence-specific frontend bypasses and dead local-only persistence helpers were removed
- current-TUI device-global-server acceptance proof is covered by `server/serve/serve_test.go` (`TestStartBuildsStandaloneServerFromCoreStartup`, `TestServeExposesConfiguredHealthEndpoints`) and `cli/app/session_server_target_test.go` (`TestStartSessionServerUsesConfiguredDaemonForInteractiveFlow`, `TestRemoteInteractiveRuntimeTwoClientsConvergeOnSameSessionAcrossWorkspaces`, `TestRemoteReadOnlyClientHydratesCommittedTranscriptAcrossWorkspaces`)
- the active plan was reduced to residual open work, with completed Phase 2 implementation slices archived here

### Remote-Server Blockers: Server-Owned Auth Bootstrap And Path-Independent Attach

Completed.

Outcome:

- standalone/remote `builder serve` now boots far enough to expose transport before auth is configured
- auth bootstrap is server-owned via explicit pre-auth RPC (`auth.getBootstrapStatus`, `auth.completeBootstrap`)
- remote attach no longer depends on host-local project/workspace binding metadata
- remote attach is resilient to host/server path mismatch via explicit server-owned `workspace_id` selection
- interactive startup now splits local-path mode vs server-browsing mode based on server-side path availability
- first-setup remote/server-browsing admin commands landed: `builder project list`, `builder project create --path <server-path> --name <project-name>`, and `builder attach --project <project-id> <server-path>`
- transport/integration proof landed for the shipped remote attach/auth behavior, including multi-workspace explicit selection coverage

## Still Partially Open Elsewhere

These historical phases still have residual open work tracked in `plan.md`:

- Phase 2: resource surfaces and event hub
- Phase 8: shared frontend transcript architecture
