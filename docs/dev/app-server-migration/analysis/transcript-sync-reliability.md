# Transcript Sync Reliability

Status: Phase 5 implementation checkpoint; committed-transcript recovery architecture and multi-client proof are landed

Last updated: 2026-04-12

## Regression

Primary debug session: `d117d033-1ea4-486b-ab58-aca49c607f06`

Observed symptoms on the migration branch:

- ongoing mode occasionally misses committed transcript updates
- detail mode can also miss committed transcript updates until restart or full reload
- missed items include supervisor suggestions, compaction notices, tool-call blocks, and sometimes the final assistant reply itself
- the problem appears more often when the terminal UI is backgrounded or otherwise not consuming updates promptly

This is not just a paint bug. It is a transcript-consistency bug at the frontend/server boundary.

## Current Architecture

There are two different data paths for the same session:

1. Live session-activity stream
   - `server/registry/runtime_registry.go`
   - `cli/app/session_activity_channel.go`
   - `cli/app/ui_runtime_adapter.go`
   - carries ephemeral runtime events such as assistant deltas, reasoning deltas, run-state changes, and `conversation_updated`

2. Authoritative read model
   - `server/sessionview/service.go`
   - `cli/app/ui_runtime_client.go`
   - `cli/app/ui_runtime_adapter.go`
   - now split into:
     - `session.getMainView` / `RuntimeMainView` for lightweight session metadata + run state
     - `session.getTranscriptPage` / `TranscriptPage` for committed transcript hydration

The ongoing UI then adds a third layer on top:

3. Native ongoing scrollback projection
   - `cli/app/ui_native_history.go`
   - projects committed transcript entries into append-only terminal scrollback

That means correctness currently depends on three components staying in sync:

- stream delivery
- authoritative read refresh
- native scrollback projection

## Why Messages Are Being Missed

## 1. The live session stream is intentionally lossy

`server/registry/runtime_registry.go` uses bounded subscriber channels. If a client falls behind, the subscription is closed with `ErrStreamGap`.

That part is acceptable only if recovery is always authoritative.

## 2. Gap recovery is not actually authoritative today

`cli/app/session_activity_channel.go` responds to a stream gap by resubscribing and injecting a synthetic `conversation_updated` event.

That synthetic event then calls `syncConversationFromEngine()` through `cli/app/ui_runtime_adapter.go`.

Before this checkpoint, that path depended on `RefreshMainView()` implemented in `cli/app/ui_runtime_client.go`, which had two reliability problems:

- explicit transcript refresh used a very short read timeout
- read failure could fall back to stale cached state and treat that stale state as the current view

That means a gap could happen, the client could attempt recovery, the recovery read could fail or time out, and the UI would keep rendering stale transcript state with no authoritative correction.

The current implementation no longer uses `RefreshMainView()` as the transcript repair path. `conversation_updated` and stream-gap recovery now repair through `RefreshTranscript()` over `session.getTranscriptPage`, while `session.getMainView` remains a metadata/read-model surface.

## 3. The UI mixes ephemeral activity with committed transcript state

Assistant deltas and reasoning deltas are correctly ephemeral. Committed transcript entries are not.

Today the frontend receives ephemeral activity and separately rehydrates committed transcript state, but those paths are not modeled as separate consistency domains. A missed live event can therefore delay or suppress a committed transcript refresh in ways that are hard to reason about.

## 4. Ongoing scrollback depends on already-correct committed projection

`cli/app/ui_native_history.go` assumes the committed transcript projection it receives is already authoritative. If the transcript model is stale, ongoing scrollback stays stale too.

This is why the issue is visible in both ongoing and detail modes: ongoing is not the source of the bug, it only exposes it more clearly.

## Required Architecture

We need one correctness rule:

`Committed transcript state must have exactly one authoritative frontend path: hydrate from a server read model, then project locally.`

Corollaries:

- live streams are never the source of truth for committed transcript content
- live streams may accelerate UX, but they cannot be required for correctness
- any live-stream gap must mark committed transcript state dirty
- dirty committed transcript state must be repaired by authoritative rehydrate, not by replaying deltas
- local terminal scrollback must only consume the repaired committed projection

## Product Semantics Locked After Review

Two earlier assumptions were incorrect and are now superseded by product decisions:

- compaction is not a same-session transcript rewrite for frontend recovery purposes; it is ordinary same-session committed transcript progression surfaced through committed entries
- rollback/fork is navigation or attachment to a different session target, not same-session transcript mutation

That leaves two relevant recovery classes:

- Category A: client-side logical divergence bugs such as bad deduplication, ordering, overlap, pagination, or live-vs-hydrate reconciliation
- Category C: external continuity loss such as disconnect, stream gap, client restart, daemon restart, or subscription invalidation

Category A must be fixed at root cause. Category C recovers through authoritative rehydrate; in TUI ongoing mode, re-issuing the ongoing buffer is acceptable for that recovery class.

## Recovery Class Mapping

| Case | Class | Required behavior |
| --- | --- | --- |
| missed user message commit during active attachment | Category A | fix root cause; do not normalize with redraw |
| missed tool start/output/finalize during active attachment | Category A | fix root cause; do not normalize with redraw |
| missed final assistant answer during active attachment | Category A | fix root cause; do not normalize with redraw |
| stream gap / slow subscriber | Category C | authoritative rehydrate plus resubscribe; TUI ongoing may re-issue buffer |
| client restart | Category C | authoritative rehydrate on attach; TUI ongoing may re-issue buffer |
| daemon restart | Category C | authoritative rehydrate on reattach; TUI ongoing may re-issue buffer |
| transport disconnect | Category C | authoritative rehydrate on recovery; TUI ongoing may re-issue buffer |

The purpose of this split is to keep recovery semantics honest: external continuity loss is recoverable by redraw from authority, while same-session logical divergence is a correctness bug in our reconciliation logic.

## Proposed Model

Split session communication into two explicit layers.

## A. Committed transcript synchronization

Server responsibility:

- expose committed transcript reads as typed hydration data
- page large transcripts explicitly
- attach a monotonic committed-transcript revision to hydration responses and commit notifications

Client responsibility:

- keep one committed transcript cache keyed by session + revision
- treat that cache as the only source for committed transcript rendering in detail and ongoing modes
- rehydrate by page, not by replay

Recommended shape:

- `session.getMainView` for lightweight session metadata + current run state + current transcript head metadata
- `session.getTranscriptPage` for committed transcript pages
- future transcript responses carry `transcript_revision`

Implementation status:

- landed:
  - dedicated `session.getTranscriptPage`
  - `RuntimeClient.Transcript()` / `RefreshTranscript()`
  - transcript revision sourced from persisted session `last_sequence`
  - CLI transcript convergence through transcript reads rather than `MainView`
- not landed yet:
  - detail-mode pagination UX
  - revision-aware incremental fetch instead of full-page hydrate in the CLI
  - transcript-specific commit notifications on the live event stream

## B. Ephemeral live activity

Server responsibility:

- stream only ephemeral activity here: assistant delta, reasoning delta, run-state changes, prompt activity, process output, transient notices
- make stream gap explicit

Client responsibility:

- use these streams only for progressive UX
- if a gap occurs, clear or invalidate ephemeral state as needed
- immediately trigger authoritative committed-state rehydrate when the gap could affect transcript-visible state

## Event Contract Direction

The session activity stream should eventually stop pretending to be transcript transport.

For transcript correctness, the stream should evolve toward notifications like:

- `transcript_committed { session_id, transcript_revision }`
- `session_state_changed { ... }`
- `run_state_changed { ... }`
- `assistant_delta { ... }`
- `reasoning_delta { ... }`

That lets the frontend do one thing reliably:

- if `transcript_revision` advanced, fetch authoritative committed transcript state

instead of trying to infer committed transcript correctness from a pile of live runtime events.

## Immediate Tactical Fixes

These are worth doing before the full protocol redesign because they reduce current regressions without changing product direction.

1. Make transcript rehydrate asynchronous.
   - Do not block the Bubble Tea update loop on transcript reads.

2. Make explicit transcript refresh authoritative.
   - Failed explicit refresh must not poison the cached main view.

3. Retry authoritative refresh after transient failure.
   - A dropped stream followed by one failed read must not leave the transcript stale forever.

4. Keep render-path reads cheap and best-effort.
   - Fast render/status reads can stay cached and bounded.
   - Correctness paths must use the authoritative refresh path.

The current checkpoint implements items 1-4 on top of `session.getTranscriptPage` in the CLI client.

## Planned Follow-up Work

- Phase 6A: remove remaining code/docs/tests that still describe compaction or rollback as same-session transcript rewrite behavior
- Phase 6B: eliminate Category A root-cause bugs in transcript reconciliation instead of normalizing them with redraw/rebuild behavior
- keep targeted logging for `session_activity` gap, hydrate failure, hydrate retry, successful transcript repair, and committed revision advance
- keep treating authoritative transcript hydration as the repair primitive for Category C continuity loss

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

## Non-Goals

- no partial replay or cursor recovery for committed transcript correctness
- no dependence on live-stream history to reconstruct transcript state
- no second transcript source of truth in the frontend
- no treating compaction or rollback/fork as acceptable same-session non-append mutation cases for transcript recovery
- no normalizing Category A client-side divergence bugs into acceptable redraw behavior

## Acceptance Criteria For This Area

- backgrounding or slowing the UI may delay live updates, but committed transcript state is eventually repaired automatically
- a missed stream event cannot permanently hide a committed assistant message, tool result, supervisor note, or compaction note
- ongoing mode and detail mode derive committed transcript from the same frontend cache
- reconnect correctness depends on typed hydration reads and transcript paging, not on replaying old live events
- TUI may re-issue the ongoing buffer only for external continuity-loss recovery, not for same-session logical divergence bugs
