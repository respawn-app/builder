# Transcript Overlap Audit

Status: active stabilization work

Last updated: 2026-04-05

## Purpose

This document records the current audit of overlapping transcript-visible mutation paths.

Its job is narrower than the overall stabilization plan: identify whether there are still known local frontend overlap paths where hydration or recovery can clobber newer live state.

## Audited Overlap Classes

### 1. Equal-revision or older transcript hydrate replacing newer live tail state

Status: covered

Proof:

- `cli/app/ui_runtime_adapter_test.go`
  - `TestApplyRuntimeTranscriptPageRejectsEqualRevisionTailReplacementAfterLiveAppend`
  - `TestApplyRuntimeTranscriptPageRejectsOlderRevisionTailReplacement`
  - `TestApplyRuntimeTranscriptPageRejectsEqualRevisionShiftedTailReplacement`

Meaning:

- stale or same-revision tail hydrates do not replace newer appended committed transcript state
- shifted same-revision pages are rejected rather than treated as authoritative tail replacement

### 2. Hydrate clearing visible live assistant output

Status: covered

Proof:

- `cli/app/ui_runtime_adapter_test.go`
  - `TestApplyRuntimeTranscriptPageRejectsEqualRevisionTailReplacementThatClearsLiveOngoing`
  - `TestApplyRuntimeTranscriptPageAcceptsNewerRevisionTailReplacementThatClearsLiveOngoing`

Meaning:

- same-revision hydrate is not allowed to clear visible ongoing assistant output
- newer authoritative hydrate may clear it

### 3. Hydrate clearing visible live reasoning output

Status: covered

Proof:

- `cli/app/ui_runtime_adapter_test.go`
  - `TestApplyRuntimeTranscriptPageRejectsEqualRevisionReasoningClear`
  - `TestApplyRuntimeTranscriptPageAcceptsNewerRevisionReasoningClear`

Meaning:

- same-or-older hydrate does not clear live reasoning
- newer authoritative hydrate may clear it

### 4. Equal-revision hydrate carrying runtime-only authoritative tail changes

Status: covered

Proof:

- `cli/app/ui_runtime_adapter_test.go`
  - `TestApplyRuntimeTranscriptPageAcceptsEqualRevisionTailReplacementWhenRuntimeOnlyEntryArrives`
  - `TestApplyRuntimeTranscriptPageAcceptsEqualRevisionTailReplacementWhenOngoingErrorChanged`
  - `TestApplyRuntimeTranscriptPageAcceptsEqualRevisionTailReplacementWhenOngoingErrorCleared`

Meaning:

- the equal-revision guard is not absolute
- equal-revision hydrate is still accepted when it carries authoritative tail/error surface changes that do not conflict with newer visible live state

### 5. User-flush-triggered hydrate racing with later live assistant activity

Status: covered

Proof:

- `cli/app/ui_runtime_adapter_test.go`
  - `TestProjectedUserMessageFlushedDoesNotClobberLaterAssistantDelta`
- `cli/app/ui_native_scrollback_integration_test.go`
  - `TestNativeProgramUserFlushDoesNotTriggerTranscriptSyncThatDropsCommentary`

Meaning:

- user flush no longer schedules the transcript hydrate that previously raced with live assistant commentary

### 6. Stream-gap recovery rehydrate across multiple streams

Status: covered

Proof:

- `cli/app/stream_resubscribe_test.go`
  - `TestStartSessionActivityEventsResubscribeStaysIsolatedAcrossStreams`
- `cli/app/ui_transcript_sync_recovery_test.go`
  - `TestSessionActivityGapRecoveryEventuallyHydratesCommittedTranscriptInBothModes`

Meaning:

- recovery-triggered hydrate remains scoped to the affected activity stream
- after a gap, authoritative transcript recovery converges ongoing and detail views

## Remaining Local Overlap Caveat

No remaining local loopback/frontend overwrite race is currently proven in automated tests.

The main remaining transcript-equivalence caveat is not a local overwrite bug. It is the deferred remote session-activity parity gap:

- remote session activity preserves live assistant progress via `assistant_delta`
- it does not yet carry the persisted assistant commentary transcript entry for assistant/tool-call turns on the raw stream

That gap is tracked as an intentional defer in the stabilization plan.

## Conclusion

For the migrated frontend path, the currently known local overlap classes are either:

- guarded by revision-aware replacement rules
- covered by targeted automated tests
- or explicitly deferred as remote raw-stream parity rather than local overwrite correctness
