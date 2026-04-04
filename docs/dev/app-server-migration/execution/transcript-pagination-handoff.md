# Transcript Pagination Handoff

## Current Slice

This is a post-Phase-3 stabilization/refactor slice targeting large-session transcript performance and memory pressure.

The user requirement for this slice is:

- stop full-transcript hydration/re-emission in ongoing mode
- stop full-file `events.jsonl` reads in hot paths where feasible
- add protocol support for both offset-based and page-based transcript reads
- prepare the boundary for real detail-mode pagination and bounded caches

## Landed In This Slice

### 1. Runtime transcript read contract widened

- `shared/clientui/types.go`
  - added `TranscriptWindow`
  - `TranscriptPageRequest` now supports `offset/limit`, `page/page_size`, and `window`
- `shared/serverapi/session_view.go`
  - `SessionTranscriptPageRequest` now carries `page`, `page_size`, and `window`
- `shared/clientui/runtime.go`
  - `RuntimeClient` now has `LoadTranscriptPage(req)`
- forwarded through:
  - `cli/app/ui_runtime_client.go`
  - `server/primaryrun/runtime_client.go`
  - related test fakes/stubs

### 2. CLI default transcript hydration is now bounded

- `cli/app/ui_runtime_client.go`
  - `RefreshTranscript()` now requests `window=ongoing_tail` by default
  - this is the key change that stops the current active session from hydrating the full transcript during the normal ongoing-mode sync path

### 3. Server-owned ongoing tail policy added

- `server/runtime/chat_store.go`
  - added `ongoingTailSnapshot(maxEntries)`
  - tail window is computed as `max(last 500 committed entries, entries since last compaction checkpoint)`
- `server/runtime/engine_state.go`
  - added `Engine.OngoingTailTranscriptWindow(maxEntries)`
- `server/runtimeview/transcript.go`
  - `TranscriptPageFromRuntime(...)` now honors `window=ongoing_tail`
  - request normalization now supports page-number pagination via `page/page_size`

### 4. Runtime restore no longer reads the entire event log into `[]Event`

- `server/session/event_log.go`
  - added streaming walkers: `walkEventsFile(...)`, `walkEventsFromReader(...)`
- `server/session/store.go`
  - added `(*Store).WalkEvents(...)`
- `server/runtime/message_lifecycle.go`
  - `RestoreMessages()` now uses `WalkEvents(...)` instead of `ReadEvents()`

This does not solve all large-session memory use, but it removes one concrete full-file eager load from runtime startup/restore.

## Verification Already Run

- `go test ./server/runtimeview ./server/runtime ./server/sessionview ./cli/app ./server/primaryrun ./shared/clientui -count=1`
- `./scripts/test.sh ./server/runtimeview ./server/runtime ./server/sessionview ./cli/app ./server/primaryrun ./shared/clientui`
- `bash ./scripts/build.sh --output ./bin/builder`

All passed at the end of this slice.

## Key Files Touched

- `shared/clientui/types.go`
- `shared/clientui/runtime.go`
- `shared/serverapi/session_view.go`
- `cli/app/ui_runtime_client.go`
- `cli/app/ui_runtime_client_test.go`
- `server/primaryrun/runtime_client.go`
- `server/session/event_log.go`
- `server/session/store.go`
- `server/runtime/message_lifecycle.go`
- `server/runtime/chat_store.go`
- `server/runtime/engine_state.go`
- `server/runtimeview/transcript.go`
- `server/runtimeview/projection_test.go`
- `docs/dev/app-server-migration/analysis/transcript-pagination-slice.md`

## Important Deferred Work

These are still outstanding and are the correct next steps for the next agent.

### A. Main-view transcript payload is still too heavy

- `server/runtimeview/projection.go` still builds full `ChatSnapshot` for `SessionViewFromRuntime(...)`
- `server/sessionview/service.go` dormant `GetSessionMainView(...)` still replays the full dormant session

The CLI mostly uses transcript hydration now, so this is less urgent than before, but it is still a real remaining cost.

### B. Dormant transcript page still slices after full replay

- `server/sessionview/service.go`
  - dormant branch in `GetSessionTranscriptPage(...)` still uses `replayDormantSession(...)`
  - this still clones/replays the full session to answer transcript reads

This is the next real backend cut if the goal is to stop dormant/offline full transcript loads.

### C. Detail-mode frontend pagination is not implemented yet

- the CLI still expects a fully resident transcript slice in `m.transcriptEntries`
- there is no two-way async detail-page cache with purge limits yet

The new contract is ready for this work, but the actual UI cache/scroll integration remains undone.

## Recommended Next Step

Do this next, in order:

1. Replace dormant `GetSessionTranscriptPage(...)` replay with a file-backed bounded transcript scanner/pager.
2. Switch the CLI detail mode to explicit page loading over `LoadTranscriptPage(req)` with a bounded cache.
3. Only after that, simplify `GetSessionMainView(...)` so it no longer hydrates transcript content at all.
