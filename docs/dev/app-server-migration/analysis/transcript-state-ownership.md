# Transcript State Ownership

Status: implemented stabilization contract

Last updated: 2026-04-05

## Purpose

This document identifies every major frontend path that can mutate transcript-visible state today and classifies what it is allowed to own during stabilization.

This is not a low-level design. It is a state-ownership map for the stabilization work in `execution/transcript-stabilization-plan.md`.

## State Categories

For stabilization purposes, transcript-visible state is split into three categories.

### 1. Committed transcript state

Durable session history that should survive reload/reconnect.

Examples:

- user messages once flushed/committed
- assistant commentary/final messages once committed
- tool-call rows once committed to transcript
- tool results once committed to transcript
- compaction notices
- reviewer/supervisor notices that are represented as transcript entries

### 2. Live transient state

Visible activity that improves active-session UX but is not itself the durable transcript source of truth.

Examples:

- assistant streaming delta text
- reasoning streaming delta text
- pending tool-call preview before completion
- transient running/idle state
- ephemeral live-region rendering in ongoing mode

### 3. Projection/render state

Frontend-only derived state that exists to render ongoing/detail surfaces efficiently.

Examples:

- `uiModel.transcriptEntries`
- detail transcript window cache
- native ongoing scrollback projection
- current view/model transcript snapshot inside Bubble Tea

## Current Mutation Paths

## A. Live session-activity stream

Primary files:

- `cli/app/session_activity_channel.go`
- `cli/app/ui_runtime_adapter.go`
- `shared/clientui/runtime_events.go`

Current responsibility:

- apply live projected runtime events
- append transcript entries carried directly on events
- update live transient state such as assistant/reasoning deltas
- update some input/control state

Allowed ownership during stabilization:

- live transient state
- direct append of transcript entries explicitly present on events

Not allowed during stabilization:

- blind replacement of committed transcript state from cached/hydrated reads
- implicit overwrite of newer visible state through synthetic recovery events

Current overlap risk:

- some events still trigger transcript sync indirectly
- gap recovery currently re-enters transcript hydration through synthetic `conversation_updated`

## B. Authoritative transcript hydration

Primary files:

- `cli/app/ui_runtime_sync.go`
- `cli/app/ui_runtime_adapter.go`
- `cli/app/ui_runtime_client.go`
- `server/sessionview/service.go`

Current responsibility:

- fetch transcript pages from the server read model
- replace or merge the current transcript projection
- refresh transcript state after recovery/gap/hydration requests

Allowed ownership during stabilization:

- committed transcript state
- detail/ongoing committed transcript caches

Not allowed during stabilization:

- clearing newer live transient state unless the refresh is known to be authoritative for that state
- acting as the primary path for active-session progressive UX

Current overlap risk:

- `applyRuntimeTranscriptPage(...)` can replace view-facing transcript state wholesale
- refresh timing can race with later live events

## C. Detail transcript paging/window

Primary files:

- `cli/app/ui_transcript_pager.go`
- `cli/app/ui_runtime_adapter.go`

Current responsibility:

- retain a bounded detail-mode window
- merge or replace paged transcript ranges
- carry transcript metadata such as offsets, totals, and ongoing text

Allowed ownership during stabilization:

- detail-mode committed transcript window only

Not allowed during stabilization:

- redefining authoritative committed transcript state independently of transcript hydration

Current overlap risk:

- tail sync and page merge are distinct code paths
- session switches and tail refreshes can still reset detail state broadly

## D. Native ongoing scrollback projection

Primary files:

- `cli/app/ui_native_history.go`
- `cli/app/ui_runtime_adapter.go`

Current responsibility:

- append-only terminal projection for ongoing mode
- flush committed transcript-visible entries into normal-buffer history

Allowed ownership during stabilization:

- projection/render state only

Not allowed during stabilization:

- acting as a source of truth for transcript correctness
- inventing committed transcript content independently of transcript state

Current overlap risk:

- depends entirely on upstream transcript projection being correct
- can appear broken even when the real source-of-truth bug is earlier in the pipeline

## E. Resubscribe/gap recovery

Primary files:

- `cli/app/session_activity_channel.go`
- `cli/app/ui_runtime_sync.go`

Current responsibility:

- resubscribe after `ErrStreamGap`
- force the frontend back through transcript repair

Allowed ownership during stabilization:

- triggering recovery only

Not allowed during stabilization:

- pretending to reconstruct transcript correctness from replayed live events

Current overlap risk:

- synthetic `conversation_updated` is a coarse repair trigger
- recovery path does not yet distinguish stale hydration from newer local live state

## F. Runtime-client cached reads

Primary files:

- `cli/app/ui_runtime_client.go`

Current responsibility:

- cache main-view and transcript reads
- serve quick follow-up reads without re-fetching every time

Allowed ownership during stabilization:

- read caching only

Not allowed during stabilization:

- becoming a hidden competing source of transcript truth

Current overlap risk:

- cached reads can hide whether the UI is rendering fresh or stale transcript state

## Ownership Rules For Stabilization

These are the working rules for the stabilization phase.

1. Live session-activity is the only active-session path allowed to update live transient state.
2. Transcript hydration is the only path allowed to rehydrate committed transcript state.
3. Projection/render layers must not invent or reinterpret committed transcript content.
4. Recovery paths may mark transcript state dirty and trigger hydrate, but they must not silently overwrite newer visible state without a freshness check.
5. Ongoing mode and detail mode must derive committed transcript from the same committed transcript model.

## Known High-Risk Overlaps

- `EventConversationUpdated` still routes active sessions back through transcript sync.
- transcript refresh and live event application can interleave without an explicit freshness contract.
- resubscribe recovery uses a synthetic event rather than a transcript-specific recovery signal.
- live tool-call visibility mixes committed transcript entries with live-region behavior.
- detail tail sync and ongoing committed state still meet inside `applyRuntimeTranscriptPage(...)`.

## Immediate Stabilization Priorities

- [x] shrink the number of places that can replace `uiModel.transcriptEntries`
- [x] stop recovery-triggered transcript reads from wiping newer visible live state
- [x] document when `conversation_updated` should trigger hydrate and when it should not
- [x] make loopback and remote transcript-critical flows obey the same ownership rules
- [x] add rendered-path tests around each remaining local high-risk overlap

The remaining remote raw-stream commentary-entry gap is an explicit runtime-event contract defer, not an ownership-rule violation.

This document does not claim event-for-event raw stream parity between loopback and remote paths. It only claims that both paths now obey the same frontend ownership rules for committed transcript hydrate vs live transient state.

## Exit Condition For This Workstream

This workstream is complete when there is one clear answer for each of the following:

- what owns committed transcript state?
- what owns live transient state?
- what is allowed to replace committed transcript state?
- what is never allowed to clear newer visible live state?
