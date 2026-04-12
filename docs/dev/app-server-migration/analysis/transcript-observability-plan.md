# Transcript Observability Plan

Status: implemented stabilization slice

Last updated: 2026-04-05

## Purpose

This document scopes the minimum useful instrumentation needed to localize transcript regressions without guesswork.

The goal is not general logging. The goal is to answer one question quickly:

"Did the visible transcript change because of server emission, transport, hydration, or frontend apply logic?"

## Approach

Use one debug-only diagnostic log family and one shared fingerprint helper so the same visible transcript mutation can be correlated across layers without dumping full transcript text.

Suggested log family:

- `transcript.diag.*`

These diagnostics should be gated behind global debug mode or a dedicated transcript-diagnostics toggle so normal UX is unaffected.

Current enablement:

- `BUILDER_DEBUG=1` or `debug = true` for strict developer-oriented behavior
- `BUILDER_TRANSCRIPT_DIAGNOSTICS=1`
- UI tests may also enable it with `WithUITranscriptDiagnostics(true)`

## Common Structured Fields

Use the same core fields at every hook point.

- `session_id`
- `mode`
- `path`
- `kind`
- `step_id`
- `revision`
- `freshness`
- `entries_count`
- `entries_digest`
- `ongoing_chars`
- `assistant_delta_chars`
- `reasoning_key`
- `reasoning_chars`

## Fingerprint Rules

Use stable digests instead of raw transcript dumps.

- `entries_digest`: stable hash of ordered transcript entry tuples
- `event_digest`: stable hash of the live event payload relevant to transcript visibility
- `page_digest`: stable hash of transcript-page identity plus visible transcript content

The exact hashing function is less important than stability across the server/client boundary.

## Hook Points

### 1. Server emission and projection

Primary hooks:

- `server/sessionruntime/service.go`
  - runtime `OnEvent` path before publish
- `server/runprompt/headless.go`
  - headless runtime `OnEvent` path before publish
- `server/runprompt/logger.go`
  - shared formatting helpers for projection/publish diagnostics

Emit:

- `transcript.diag.server.project_event`
- `transcript.diag.server.publish_activity`

Purpose:

- prove whether transcript-visible entries existed before transport
- catch projection loss before the event leaves the server boundary

### 2. Transport and recovery

Primary hooks:

- `cli/app/session_activity_channel.go`
  - live event receive path
  - stream-gap / resubscribe path
  - synthetic recovery-event emit path

Emit:

- `transcript.diag.client.recv_activity`
- `transcript.diag.client.activity_gap`
- `transcript.diag.client.synthetic_conversation_updated`

Purpose:

- separate transport loss from later recovery-triggered hydration

### 3. Hydration request and response

Primary hooks:

- `cli/app/ui_runtime_sync.go`
  - transcript refresh request path
  - transcript refresh response/apply handoff
- `cli/app/ui_runtime_client.go`
  - transcript fetch path

Emit:

- `transcript.diag.client.hydrate_start`
- `transcript.diag.client.hydrate_fetch`
- `transcript.diag.client.hydrate_response`
- `transcript.diag.client.rebuild_required`
- `transcript.diag.client.rebuild_start`
- `transcript.diag.client.rebuild_succeeded`

Purpose:

- distinguish bad read-model data from bad frontend-apply decisions
- distinguish committed suffix-append recovery from full committed rebuild recovery

### 4. Frontend apply logic

Primary hooks:

- `cli/app/ui_transcript_diag.go`
  - shared frontend diagnostic formatting helpers
- `cli/app/ui_runtime_adapter.go`
  - projected event apply path
  - transcript-entry append path
  - transcript-page apply path

Emit:

- `transcript.diag.client.apply_event`
- `transcript.diag.client.append_entries`
- `transcript.diag.client.apply_page_start`
- `transcript.diag.client.apply_page_reject`
- `transcript.diag.client.apply_page_commit`
- `transcript.diag.client.append_committed_suffix`
- `transcript.diag.client.commit_rebuild`

Purpose:

- explain whether visible transcript state changed because of live append, hydrate commit, or hydrate rejection
- make full rebuild decisions obvious in logs instead of looking like ordinary suffix repair

## Required Reject Reasons

When transcript-page replacement is rejected, log an explicit structured reason rather than inferring it from context.

Initial reason set:

- `stale_revision`
- `live_dirty_same_or_older_revision`
- `same_revision_would_clear_ongoing`

Extend this only when a new reject case is actually introduced.

## Expected Debugging Outcomes

With this instrumentation in place:

- a server emission/projection bug is visible before transport
- a transport bug is visible as server publish without matching client receive
- a hydration/read bug is visible as wrong page content before frontend apply
- a frontend apply bug is visible as an unexpected append, reject, or commit decision

## Delivery Guidance

This is intentionally the smallest useful slice.

Implement it as:

1. one shared digest helper
2. one gated diagnostic logger family
3. the hook points above
4. explicit reject/apply reasons for transcript-page decisions

Implemented files:

- `shared/transcriptdiag/diag.go`
- `cli/app/ui.go`
- `cli/app/ui_transcript_diag.go`
- `cli/app/ui_runtime_adapter.go`
- `cli/app/ui_runtime_sync.go`
- `cli/app/ui_runtime_client.go`
- `cli/app/session_activity_channel.go`
- `server/runprompt/logger.go`
- `server/runprompt/headless.go`
- `server/sessionruntime/service.go`
- `cli/app/ui_transcript_diag_test.go`

Do not broaden this into generic TUI logging.
