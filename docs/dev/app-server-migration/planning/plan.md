# App Server Migration Plan

This file tracks only work that is still ahead.

Completed phases were moved to `docs/dev/app-server-migration/planning/plan-completed.md` so this file stays usable during implementation.

Phase numbers are historical labels. They are kept for continuity, not because work must execute strictly in numeric order.

## Current Focus

Current shipping path:

1. Phase 6B: finish transcript hardening so current app-server build is shippable
2. Phase 7: finish standalone polish and boundary proof needed for release confidence

Not on the shipping critical path:

- Phase 2 residual resource-surface work
- Phase 8 shared frontend transcript architecture refactor

## Open Work

### Phase 6B: Transcript Divergence Hardening

Goal: remove the currently reproducing transcript failures without blocking shipment on a larger frontend transcript architecture rewrite.

Requirements for this phase:

- [x] committed transcript truth is derived only from authoritative transcript pages plus explicitly committed runtime events
- [ ] ordinary successful turns do not call `requestRuntimeTranscriptSync()` unless continuity was actually lost
- [x] same-session transcript divergence remains a bug; only Category C continuity-loss paths may reissue committed ongoing scrollback
- [x] live commentary / streaming UI state is not allowed to mutate committed transcript state implicitly
- [x] compaction remains ordinary same-session committed progression, not transcript replacement

Implementation workstreams:

Dependency note:

- land `6B.2` and `6B.3` before `6B.1` and `6B.4`; otherwise frontend cleanup will keep reintroducing compensating logic around a still-ambiguous runtime commit contract and hydrate policy

#### 6B.1 Frontend committed-transcript application cleanup

Scope: `cli/app/ui_runtime_adapter.go`, `cli/app/ui_runtime_sync.go`, `cli/app/ui.go`

- [x] first add failing regression cases in `ui_runtime_adapter_test.go` / `ui_native_scrollback_integration_test.go` for each branch being changed, then change code, then mark the corresponding live-matrix item
- [x] committed events whose hidden prefix starts before the loaded tail window now trim that off-screen overlap and reconcile only the visible overlap/suffix instead of hydrating immediately
- [x] committed events skipped because they are fully off-screen still advance local transcript revision / committed-count metadata so later fence events do not look like unexplained continuity loss
- [x] refactor `shouldRecoverCommittedTranscriptFromConversationUpdate` so it does not infer transcript truth from event kind alone and only escalates on explicit committed continuity loss
- [x] refactor `deferProjectedCommittedTail` and `mergeDeferredCommittedTailIntoEvent` so deferred rows are created only for known queued-user committed flushes and can merge only once into the next contiguous committed event
- [x] make `deferredCommittedTail` clearing explicit on hydrate, disconnect, session switch, authoritative invalidation, and committed continuity loss
- [x] make same-revision authoritative page handling explicit in `applyRuntimeTranscriptPageWithRecovery`: pure append, authoritative empty-ongoing clear, exact committed-tail replacement after continuity loss

#### 6B.2 Runtime committed-event contract audit

Scope: `server/runtime/*.go`, `server/runtimeview/projection.go`, `shared/clientui/runtime_events.go`

- [x] direct user flush, queued user flush, persisted local entry, and cache warning paths now emit only their rich committed event and no extra committed `conversation_updated` afterward
- [x] compaction completed/failed no longer synthesize transcript rows before persistence; the persisted `local_entry_added` event is the only compaction transcript source across runtime, projection, and client layers
- [x] ongoing error refresh no longer relies on broad plain `conversation_updated`; a dedicated `ongoing_error_updated` event now drives authoritative ongoing-error set/clear refresh
- [x] generic committed `conversation_updated` emission is now explicitly narrowed: tool-result mirror `llm.RoleTool` messages do not emit it, and the remaining generic committed-advance path is reserved for persisted message rows that do not already have a richer runtime event

- [x] lower-layer regression coverage now exists in `server/runtime/*test.go` and `shared/clientui/runtime_events_test.go` for committed `conversation_updated` narrowing and `ongoing_error_updated` refresh semantics
- [x] audit `emitConversationUpdated` and `emitCommittedTranscriptAdvanced` callsites and list which ones are allowed to carry `CommittedTranscriptChanged` (plain transient updates are now restricted to `AppendLocalEntry` and `clearStreamingAssistantState`; committed generic advancement is restricted to `appendMessage` visible rows without a richer runtime event plus explicit history-replacement paths)
- [x] lock `step_executor.go` assistant-final ordering to: persist final assistant row, then emit the rich committed `assistant_message` event with the correct committed range, with no fallback committed hydrate path needed for ordinary success
- [x] lock `tool_executor.go` tool-start ordering to: persist tool call row, then emit `tool_call_started` with the correct committed range and no same-turn bare committed `conversation_updated`
- [x] lock `tool_executor.go` tool-complete ordering to: persist tool result row, then emit `tool_call_completed` with the correct committed range and no same-turn bare committed `conversation_updated`
- [x] lock queued-user flush ordering so `user_message_flushed` remains the only committed advancement signal for that committed row and does not require pre-append hydrate
- [x] lock reviewer terminal/local-entry paths so persisted `reviewer_status` / related terminal local entries remain the only transcript source and do not force pre-append hydrate through extra committed advancement
- [x] finish the remaining plain non-committed `conversation_updated` callsite audit by enumerating each surviving emitter (`AppendLocalEntry` and `clearStreamingAssistantState`) and documenting why they stay

#### 6B.3 Hydration gate tightening

Scope: `cli/app/ui_runtime_adapter.go`, `cli/app/ui_runtime_sync.go`, `shared/clientui/runtime_events.go`

- [x] ordinary runtime-backed submit completion no longer requests hydrate unless queued-drain flow explicitly needs authoritative transcript state first
- [x] deferred committed user-flush tails are now included in the client-side committed baseline when later committed `conversation_updated` events are evaluated for continuity loss
- [x] all remaining ongoing-tail hydrate requests now use explicit sync causes (`bootstrap`, `queued_drain`, `committed_conversation_updated`, `committed_gap`, `dirty_follow_up`, `continuity_recovery`) instead of the old generic no-arg transcript sync path
- [x] non-contiguous committed events now record the exact divergence reason in transcript diagnostics before transitioning into authoritative recovery

- [ ] first add failing regression cases in `ui_runtime_adapter_test.go` for each hydrate trigger being removed or retained, then change code, then mark the corresponding live-matrix item
- [x] enumerate all ongoing-tail hydrate callsites and reduce them to explicit causes only: disconnect/reconnect, `requires_hydration`, startup/session-target bootstrap, or committed continuity loss
- [x] make contiguous committed events apply locally without hydrate even when a plain `conversation_updated` is nearby in the same turn
- [x] make non-contiguous committed events log the exact divergence through diagnostics/debug and transition into authoritative recovery instead of silently accepting stale state
- [x] remove ordinary successful-turn hydrate fallbacks that are currently masking same-session logic bugs

#### 6B.4 Live overlay and ongoing-surface hardening

Scope: `cli/app/ui_runtime_adapter.go`, `cli/app/ui_native_history.go`, `cli/app/ui_runtime_connection.go`

- [x] first add failing regression cases in `ui_native_scrollback_integration_test.go` / `ui_projected_runtime_test.go`, then change code, then mark the corresponding live-matrix item
- [x] added a rendered committed-ongoing regression in `ui_native_scrollback_integration_test.go` for the queued-follow-up visibility/order lifecycle before continuing the assistant-final / queued-user slice
- [x] fix the assistant-final / deferred-user boundary proved by `TestQueuedFollowUpRemainsHiddenUntilFinalCatchUpThenAppendsOnceInRenderedOngoing`: tighten `mergeDeferredCommittedTailIntoEvent` plus `shouldClearAssistantStreamForCommittedAssistantEvent` so the final assistant commit clears the matching live overlay exactly once, merges the deferred queued-user tail exactly once, and never leaves stale or duplicated final text behind
- [x] fix tool lifecycle row visibility on the rendered ongoing surface so committed tool start/result rows remain visible without hydrate during same-turn assistant replies
- [x] fix committed queued-user visibility on the rendered ongoing surface so committed user rows remain visible without hydrate after deferred flush merge and after ordinary queued-user commit
- [x] keep TUI ongoing-buffer rebuilds restricted to Category C recovery paths only in `ui_native_history.go`; same-session bugs must not clear/replay scrollback

#### 6B.5 Regression and proof coverage

Scope: `cli/app/ui_runtime_adapter_test.go`, `cli/app/ui_native_scrollback_integration_test.go`, `cli/app/ui_projected_runtime_test.go`, `server/runtime/*test.go`, `shared/clientui/*test.go`

- [x] complete the committed-path regression matrix with one targeted test each for: queued user flush, tool start, tool result, tool finalize, assistant final answer, reviewer terminal message
- [x] add lower-layer regression coverage that compaction status events do not project transcript rows before persistence and that persisted `local_entry_added` remains the only committed compaction source
- [x] add runtime-loop regression coverage for `ongoing_error_updated` set/clear lifecycle through authoritative refresh
- [x] add tests for startup authoritative refresh racing with local committed events and verify no duplicate or stale committed rows remain
- [x] add tests for concurrent-client interleaving where one client hydrates while another client is receiving live committed events
- [x] add tests that plain `conversation_updated` never requests hydrate and committed `conversation_updated` requests hydrate only on actual continuity loss

Live reproduction matrix:

- [x] per-turn `Transcript sync bug` banner no longer appears during ordinary successful turns
- [x] tool lifecycle rows no longer disappear from ongoing transcript during normal runs
- [x] committed user messages no longer disappear from ongoing transcript
- [x] compaction notices remain visible in the committed transcript tail
- [x] commentary/live area no longer flickers during updates
- [x] commentary streams incrementally where expected instead of repainting as one full message
- [x] assistant final commit no longer leaves duplicated or stale live text in the ongoing area

Manual/live validation note:

- [x] live reproduction matrix ownership is user-driven validation against rebuilt binaries; engineering work here is to keep tests/diagnostics aligned with each reproduced failure class and update this matrix honestly as fixes land

Non-goals:

- do not expand this phase into a shared frontend transcript architecture rewrite
- do not block shipment on desktop/web-oriented transcript reducer work

### Phase 7: Standalone Polish And Boundary Proof

Goal: finish the release-facing proof that Builder is operating correctly as an app-global server with the CLI/TUI as just one frontend.

Concrete tasks:

- [x] fix rollback selection targeting so `Esc Esc` / fork rollback uses the actually selected user message instead of jumping to an earlier unrelated message
- [x] fix rollback selection viewport anchoring/highlighting so the selected rollback candidate stays visibly highlighted on screen in the detail overlay / native flow
- [x] handle missing project, inaccessible project, and invalid attach target states cleanly across startup and attach flows
- [x] add or enable CI boundary enforcement for client/server architectural cut lines
- [x] make the acceptance suite runnable against external-daemon mode, covering the release-critical remote scenarios exercised in `cli/app/session_server_target_test.go`
- [x] add tests that assert frontend projection state, transcript paging windows, native transcript flush queue, and transport caches are not treated as durable transcript truth
- [x] reconcile deferred transport/protocol docs so the public/config docs and migration spec all describe direct-address attach (`server_host` + `server_port`) and fail-fast workspace binding consistently

Deferred outside this slice:

- real non-CLI client proof remains deferred to desktop/web client development instead of being faked inside Phase 7

### Phase 2 Residual: Resource Surfaces And Event Hub

Goal: finish the transport-neutral resource surfaces that were intentionally deferred while shipping the app-server migration.

Concrete tasks:

- [ ] add complete project read surfaces needed by startup, picker, and session flows
- [ ] add ask resource identities plus transport-neutral read APIs
- [ ] add approval resource identities plus transport-neutral read APIs
- [ ] finish event-hub stream classes and retention semantics, including process-output streaming on the real protocol boundary
- [ ] extend `client_request_id` idempotency coverage across the remaining mutating APIs

### Phase 8: Shared Frontend Transcript Architecture

Goal: improve transcript reliability systemically after shipment by moving transcript semantics into shared frontend logic instead of frontend-specific codepaths.

Concrete tasks:

- [ ] consolidate committed-tail reconciliation so `eventTranscriptEntriesReconcileWithCommittedTail`-equivalent logic reasons in one place over session id, revision, committed count, committed start, and contiguous overlap
- [ ] introduce one shared frontend transcript reducer/op model in shared code
- [ ] migrate TUI transcript state transitions onto that shared reducer as the first consumer
- [ ] replace event-kind-driven transcript handling with explicit transcript ops
- [ ] formalize one committed transcript model plus one live overlay model for frontend consumers
- [ ] add deterministic transcript trace replay coverage so field failures can be reproduced against the shared reducer

## Execution Order

Release-critical order:

1. Phase 6B
2. Phase 7

Then:

3. Phase 2 residual
4. Phase 8

## Exit Criteria

### Phase 6B exit

- [ ] live reproduction matrix is green on current builds
- [ ] focused regression tests cover every committed-path bug class fixed in this phase
- [ ] ordinary successful turns do not hydrate unless continuity was actually lost
- [ ] every remaining hydrate callsite is explained by one of the explicit causes listed in `6B.3`

### Phase 7 exit

- [x] external-daemon acceptance suite is green (`cli/app/session_server_target_test.go`)
- [ ] release-blocking startup/attach failure states are covered by tests

### Phase 2 residual exit

- [ ] project, ask, and approval read surfaces exist on the transport-neutral boundary
- [ ] process-output streaming semantics are defined and tested on the real protocol boundary
- [ ] remaining mutating APIs have consistent `client_request_id` idempotency behavior

### Phase 8 exit

- [ ] transcript semantics are shared through one reducer/op model
- [ ] real transcript failures can be replayed deterministically against that reducer
