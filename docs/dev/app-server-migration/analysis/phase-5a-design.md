# Phase 5A Design: Transcript Authority, Recovery, And Failure Semantics

Status: historical Phase 5A design baseline; parts of the recovery model are superseded by Phase 6 transcript-semantics decisions

Last updated: 2026-04-12

## Goal

Define the exact frontend/server contract that makes ongoing mode, detail mode, and reconnect behavior transcript-correct before broader multi-client proof work begins.

This document is intentionally narrow. It does not redesign the whole protocol. It locks the state model, recovery rules, and rendered acceptance expectations for Phase 5A.

Superseded points after product review:

- compaction is not a same-session transcript rewrite for frontend sync purposes
- rollback/fork is navigation or attachment to a different session target, not same-session mutation
- external continuity loss remains a valid authoritative rehydrate case; same-session logical divergence is Phase 6 root-cause bug work, not an acceptable redraw path

## Core Rules

- Committed transcript truth comes only from transcript hydration reads.
- Ongoing normal-buffer scrollback is committed-only and append-only after startup.
- Live session activity is transient UX input only; it is never transcript authority.
- A transport gap, subscription loss, or transcript-affecting RPC failure invalidates transient live state immediately.
- Recovery uses committed hydration plus resubscribe, never replay reconstruction from live events.
- If the client cannot prove a contiguous committed suffix for recovery, production behavior clears and rebuilds the affected session view from a fresh committed hydrate. Global debug mode (`debug = true` or `BUILDER_DEBUG=1`) may hard-fail instead.

## Client State Model

Per attached session, the frontend owns two independent state buckets:

1. `committed transcript cache`
   - source: `session.getTranscriptPage`
   - authority for detail mode
   - authority for ongoing committed scrollback
   - keyed by `session_id` plus `transcript_revision`

2. `live transient projection`
   - source: live subscriptions
   - assistant deltas
   - reasoning deltas
   - busy/running state
   - transient tool-progress hints
   - never used as committed transcript truth

The session view itself moves through these states:

- `detached`
  - no session attachment
  - no live subscription

- `attached_unhydrated`
  - session attached, but committed transcript not yet hydrated
  - no transcript rendering allowed beyond existing already-emitted committed scrollback from this process lifetime

- `hydrating`
  - transcript pages are being fetched authoritatively
  - transient live projection may exist, but it is not allowed to mutate committed scrollback until hydrate confirms contiguous committed state

- `live_consistent`
  - committed cache is current for the latest known committed revision
  - live subscription may enrich the viewport with transient activity

- `live_invalidated`
  - stream gap, reconnect, or transcript-affecting RPC failure occurred
  - transient live projection is discarded immediately
  - ongoing/detail transcript correctness must come only from the last committed cache plus recovery flow

- `rehydrating_after_gap`
  - client is fetching committed transcript pages and preparing the missing suffix or, for explicit external continuity loss only, a full ongoing-buffer re-issue
  - no transcript-affecting provisional state may be emitted into committed scrollback

- `continuity_recovery_required`
  - suffix continuity could not be proven
  - production behavior: invalidate transient live state and perform a fresh committed hydrate; same-session divergence is a bug, while external continuity loss may additionally re-issue the ongoing buffer
  - debug behavior: fail loudly when global debug mode is enabled

Allowed transitions:

- `detached -> attached_unhydrated`
- `attached_unhydrated -> hydrating`
- `hydrating -> live_consistent`
- `live_consistent -> live_invalidated`
- `live_invalidated -> rehydrating_after_gap`
- `rehydrating_after_gap -> live_consistent`
- `rehydrating_after_gap -> continuity_recovery_required`
- `continuity_recovery_required -> hydrating`

Invalid transitions should be treated as implementation bugs in debug mode.

## Transcript Revision Contract

Phase 5A should lock the transcript contract around a single monotonic committed revision.

Required properties:

- `transcript_revision` is monotonic per session.
- any committed transcript mutation that changes what detail mode or ongoing committed scrollback should render must advance `transcript_revision`.
- transient live activity does not advance `transcript_revision` by itself.
- transcript hydration responses must carry the revision they describe.
- the client tracks:
  - `last_hydrated_revision`
  - `last_emitted_committed_revision` for ongoing scrollback

Preferred recovery shape:

- normal case:
  - client observes a revision advance or suspects one after reconnect/gap
  - client requests transcript pages after `last_emitted_committed_revision`
  - if the server can prove the returned data is the contiguous committed suffix, the client appends only that suffix to ongoing scrollback

- fallback case:
  - if suffix continuity cannot be proven, the client must not guess
  - production behavior clears and rebuilds the affected session view from fresh committed hydration
  - debug mode may hard-fail instead under global debug mode

The server does not need stream-history replay for this contract.

## Ongoing Mode Rules

- Startup may replay committed transcript history into normal-buffer scrollback once.
- After startup, ongoing normal-buffer history is append-only.
- Assistant deltas, reasoning deltas, and tool-progress hints may appear only in the transient live viewport.
- Committed tool-call/tool-result/final-answer transcript entries are appended only once they are present in committed transcript hydration.
- Recovery never clears and replays old committed normal-buffer history.
- Recovery may append a missing committed suffix, or clear and rebuild the affected session view if suffix continuity cannot be proven.

## Detail Mode Rules

- Detail mode reads only from the committed transcript cache.
- Detail mode does not need live replay for correctness.
- Reconnect and stream-gap behavior use the same committed hydrate contract as ongoing mode.

## Failure Semantics

Transcript-affecting failures include:

- stream gap / slow-subscriber drop
- reconnect after connection loss
- transcript-page RPC failure during repair
- mutation/read adapter failure that makes transcript correctness uncertain

Required response:

- invalidate transient live projection immediately
- stop trusting live session activity for transcript-visible state
- schedule authoritative hydrate-plus-resubscribe recovery
- do not degrade to empty/idle success state
- do not keep rendering stale live transcript state as if it were correct

## Observability

Every transcript-visible transition should be attributable to one of these causes:

- `transcript_revision_advanced`
- `transcript_hydrate_started`
- `transcript_hydrate_succeeded`
- `transcript_hydrate_failed`
- `session_activity_gap`
- `live_projection_invalidated`
- `committed_suffix_appended`
- `continuity_replay_required`
- `continuity_replay_started`
- `continuity_replay_succeeded`

These should be sufficient to explain why a line appeared, why a line did not appear, and why recovery chose suffix append versus explicit continuity-loss replay.

## Acceptance Matrix

Phase 5A should not be considered complete without rendered-path proof for at least these scenarios:

- user message appears immediately in ongoing mode while remaining committed-only in normal-buffer scrollback
- assistant delta appears only in the transient live region and does not enter normal-buffer scrollback until commit
- tool call, tool result, and final answer remain visible in the transient ongoing live region until explicit hydrate authority catches up, without loss or stale overwrite
- `conversation_updated` entries can advance committed transcript state directly without waiting for a separate hydrate round-trip
- stream gap invalidates live transient state immediately
- reconnect repairs the missing committed transcript suffix without re-emitting old scrollback
- non-contiguous suffix fallback clears and rebuilds the affected session view in production mode
- global debug mode (`debug = true` or `BUILDER_DEBUG=1`) hard-fails on non-contiguous suffix recovery
- transcript-affecting adapter/RPC failures are surfaced and do not degrade into fake empty/idle success state
- loopback and remote paths obey the same committed-suffix repair rules
- detail mode and ongoing mode converge on the same committed transcript revision after recovery

## Acceptance Mapping To Concrete Test Surfaces

This section maps each Phase 5A acceptance item to the concrete test file/case that should prove it. If no current proof exists, the gap is explicit and should be filled before the acceptance item is considered complete.

### 1. Ongoing normal-buffer scrollback appends only committed transcript entries

Existing proof surface:

- `cli/app/ui_native_history_test.go`
  - `TestNativePendingToolCallStaysLiveUntilResultThenAppendsFinalBlock`
  - `TestNativePendingToolPreviewUsesBubbleTeaDotSpinnerWithoutCommittingScrollback`
  - `TestNativeParallelToolCompletionWaitsForStablePrefixBeforeAppend`

Gap to add:

- `cli/app/ui_native_scrollback_integration_test.go`
  - add a rendered assistant-delta case proving transient assistant output never enters committed normal-buffer scrollback before commit

### 2. Stream gap invalidates transient live state immediately

Existing proof surface:

- `cli/app/stream_resubscribe_test.go`
  - `TestStartSessionActivityEventsResubscribesAfterStreamGap`
- `cli/app/ui_projected_runtime_test.go`
  - `TestProjectRuntimeEventChannelPublishesSyntheticConversationUpdateAfterBridgeGap`
  - `TestBridgeGapHydratesTranscriptStateInProjectedUI`

Gap to add:

- `cli/app/ui_native_scrollback_integration_test.go`
  - add a rendered case proving visible transient ongoing content is dropped immediately on gap rather than lingering until hydrate finishes

### 3. Reconnect/recovery hydrates only the missing committed suffix without replaying old scrollback

Existing proof surface:

- `cli/app/ui_native_scrollback_integration_test.go`
  - `TestNativeProgramConversationRefreshHydratesCommittedTranscriptWithoutReplayDuplication`
- `cli/app/ui_transcript_sync_recovery_test.go`
  - `TestSessionActivityGapRecoveryEventuallyHydratesCommittedTranscriptInBothModes`

Gap to add:

- `cli/app/ui_native_scrollback_integration_test.go`
  - add a rendered suffix-specific case proving recovery after reconnect appends only entries after `last_emitted_committed_revision`

### 4. Same-session divergence does not replay scrollback; continuity-loss recovery may re-issue the ongoing buffer

Current proof surface:

- `cli/app/ui_native_history_test.go`
  - `TestNativeScrollbackDoesNotReplaySameSessionNonAppendMutation`
  - `TestNativeScrollbackRebasesWhenNoSharedPrefixExists`
  - `TestNativeHistorySnapshotDoesNotReplaySameSessionRewriteInOngoingMode`
  - `TestNativeHistorySnapshotReplaysDuringContinuityRecovery`
  - `TestNativeScrollbackResumesAssistantFlushesAfterFullRebuild`
  - `TestNativeDetailExitRebuildsWholeTranscriptWhenCommittedRootDiverged`

### 5. Global debug mode hard-fails on non-contiguous recovery

Current proof surface:

- `cli/app/ui_native_history_test.go`
  - `TestNativeScrollbackPanicsInDebugModeOnNonContiguousRecovery`

### 6. Transcript-affecting transport failures are surfaced and do not degrade into fake empty/idle success state

Existing proof surface:

- `cli/app/ui_transcript_sync_recovery_test.go`
  - `TestSessionActivityGapRecoveryEventuallyHydratesCommittedTranscriptInBothModes`
- `cli/app/ui_projected_runtime_test.go`
  - `TestHydrationRetryErrorReleasesRuntimeEventFenceWhileRetryIsScheduled`

Contract gap to close:

- `cli/app/ui_runtime_client_test.go`
  - existing tests such as `TestRuntimeClientMainViewFailsFastWhenReadStalls` and `TestRuntimeClientMainViewCachesFallbackAfterReadError` codify fallback-to-stale behavior for non-transcript reads; transcript-affecting paths must not inherit that contract implicitly

Gap to add:

- `cli/app/ui_runtime_client_test.go`
  - add transcript-specific failure tests proving transcript repair paths return/propagate failure instead of degrading to fake empty success

### 7. Loopback and remote paths obey the same committed-suffix repair rules

Existing proof surface:

- `server/transport/gateway_test.go`
  - `TestGatewayRemoteSessionActivityPreservesActiveSubmitOrderingUsingAssistantDeltaProgress`
- `shared/client/remote_test.go`
  - `TestRemoteSessionActivitySubscriptionPreservesTranscriptCriticalOrderingWithAssistantDeltaProgress`

Gap to add:

- remote rendered-path proof is still missing for suffix repair and rebuild semantics
- target files:
  - `cli/app/remote_server_test.go` for end-to-end remote attachment behavior
  - `cli/app/ui_native_scrollback_integration_test.go` or a new remote-rendered integration suite for remote ongoing/detail recovery parity

### 8. Detail mode and ongoing mode converge on the same committed revision after recovery

Existing proof surface:

- `cli/app/ui_transcript_sync_recovery_test.go`
  - `TestSessionActivityGapRecoveryEventuallyHydratesCommittedTranscriptInBothModes`

Gap to add:

- once revision-aware suffix paging lands, add an explicit revision assertion rather than only checking text convergence

## Implementation Notes

- `session.getMainView` must remain metadata/status hydration, not transcript transport.
- transcript-correctness recovery should prefer revision-aware suffix paging once available; full fresh hydrate remains the safe fallback.
- any existing frontend path that can append transcript-visible content directly from live runtime events should be treated as suspect until proven transient-only.
