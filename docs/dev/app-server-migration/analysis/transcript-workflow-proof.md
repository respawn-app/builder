# Transcript Workflow Proof

Status: reconciled stabilization checkpoint

Last updated: 2026-04-05

## Purpose

This document maps the transcript-stabilization workflow checklist to concrete automated tests.

It exists so the stabilization plan can be kept honest: checklist items should only be marked complete when there is a corresponding automated proof.

## Workflow Checklist To Test Mapping

### 1. Ordinary user submit shows in ongoing mode immediately

Primary proof:

- `cli/app/ui_test.go`
  - `TestRuntimeClientSubmitShowsUserMessageInTranscriptWhenFlushedEventArrives`

Rendered/native reinforcement:

- `cli/app/ui_native_scrollback_integration_test.go`
  - `TestNativeProgramUserFlushDoesNotTriggerTranscriptSyncThatDropsCommentary`

### 2. Assistant commentary stays visible after user flush instead of being dropped by hydrate

Primary proof:

- `cli/app/ui_native_scrollback_integration_test.go`
  - `TestNativeProgramUserFlushDoesNotTriggerTranscriptSyncThatDropsCommentary`

Supporting frontend-apply proof:

- `cli/app/ui_runtime_adapter_test.go`
  - `TestProjectedUserMessageFlushedDoesNotClobberLaterAssistantDelta`

### 3. Loopback ongoing-mode mixed event flow renders in order

Rendered/native proof:

- `cli/app/ui_native_scrollback_integration_test.go`
  - `TestNativeProgramRendersMixedRuntimeEventsFromChannelInRealtime`
  - `TestNativeProgramUserFlushDoesNotTriggerTranscriptSyncThatDropsCommentary`

### 4. Transcript hydrate after `conversation_updated` restores committed transcript without replay duplication

Primary proof:

- `cli/app/ui_native_scrollback_integration_test.go`
  - `TestNativeProgramConversationRefreshHydratesCommittedTranscriptWithoutReplayDuplication`

### 5. Stale or same-revision hydrate cannot wipe newer live assistant output

Primary proofs:

- `cli/app/ui_runtime_adapter_test.go`
  - `TestApplyRuntimeTranscriptPageRejectsEqualRevisionTailReplacementAfterLiveAppend`
  - `TestApplyRuntimeTranscriptPageRejectsOlderRevisionTailReplacement`
  - `TestApplyRuntimeTranscriptPageRejectsEqualRevisionTailReplacementThatClearsLiveOngoing`
  - `TestApplyRuntimeTranscriptPageAcceptsNewerRevisionTailReplacementThatClearsLiveOngoing`

### 6. Stale or same-revision hydrate cannot wipe newer live reasoning output

Primary proofs:

- `cli/app/ui_runtime_adapter_test.go`
  - `TestApplyRuntimeTranscriptPageRejectsEqualRevisionReasoningClear`
  - `TestApplyRuntimeTranscriptPageAcceptsNewerRevisionReasoningClear`

### 7. Dormant-session reopen preserves committed transcript

Primary proof:

- `cli/app/ui_mode_flow_test.go`
  - `TestScenarioHarnessRestartAndSessionResumeKeepsTranscriptVisible`

Supporting startup visibility proof:

- `cli/app/ui_runtime_adapter_test.go`
  - `TestStartupSeedsCachedTranscriptBeforeBoundedSync`

### 8. Remote session-activity preserves transcript-critical ordering

Current remote proof:

- `server/transport/gateway_test.go`
  - `TestGatewayRemoteSessionActivityPreservesActiveSubmitOrderingUsingAssistantDeltaProgress`
- `shared/client/remote_test.go`
  - `TestRemoteSessionActivitySubscriptionPreservesTranscriptCriticalOrderingWithAssistantDeltaProgress`

Important limitation:

- `server/transport/gateway_test.go` now proves raw session-activity parity for the persisted assistant commentary transcript entry on assistant/tool-call turns.
- `shared/client/remote_test.go` remains narrower and only proves ordering with live assistant progress via `assistant_delta`.

## Minimum Required Scenarios

Proven now:

- user message appears immediately in ongoing mode
- assistant commentary appears live
- tool call appears live in order
- tool result appears in order
- final answer appears in order
- reconnect rehydrates committed transcript without blanking the session
- stale transcript reads cannot wipe newer live state

Still intentionally open:

- remote rendered-path proof for suffix repair and rebuild parity with loopback

## Phase 5A Pre-Implementation Gaps

The stabilization slice proved the older hydrate-vs-live overlap rules, but Phase 5A adds stricter requirements that still need concrete proof work.

Explicit remaining gaps before Phase 5A can be called complete:

- rendered proof that assistant deltas never enter committed ongoing scrollback before commit
- rendered proof that stream-gap recovery invalidates visible transient ongoing state immediately
- rendered proof of suffix-only recovery after `last_emitted_committed_revision`
- rendered proof of production rebuild fallback when suffix continuity cannot be proven
- rendered proof of global debug-mode hard-fail behavior
- remote rendered-path proof for suffix repair and rebuild parity with loopback

## How To Use This Document

- When a stabilization checklist item is checked, point to at least one test here.
- If a test changes or is removed, update this document in the same patch.
- If a new workflow is added to the plan, add its automated proof here before marking it complete.
