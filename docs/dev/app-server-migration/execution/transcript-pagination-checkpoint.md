# Transcript Pagination Checkpoint

Status: active handoff checkpoint

Last updated: 2026-04-05

## Scope Of This Checkpoint

This is the follow-up checkpoint after `execution/transcript-pagination-handoff.md`.

The main goal of this slice was to keep pushing the transcript-performance work without stepping into the dirtier in-flight CLI areas blindly.

## Landed In This Slice

Committed in:

- `679d515` `fix: cache dormant transcript reads`
- follow-up runtime-client cache slice pending commit in current worktree

### 1. Dormant transcript read cache in `server/sessionview`

New file:

- `server/sessionview/dormant_cache.go`

What it does:

- caches dormant transcript summary state per `session_dir + session_id + last_sequence`
- stores:
  - committed transcript entry count
  - last committed assistant final answer
  - ongoing tail window snapshot
  - dormant active-run view when the latest durable run is still running
- uses a bounded LRU-style cap (`16` entries) so the cache cannot grow without bound

Current usage:

- dormant `GetSessionMainView(...)` now uses the cache instead of rescanning transcript state on every call
- dormant `GetSessionTranscriptPage(...)` now uses the cache for:
  - default `ongoing_tail`
  - bounded pages fully covered by the cached tail window
- arbitrary older dormant pages still fall back to a full streaming scan

### 2. `session.Open(...)` bootstrap no longer allocates full `[]Event` just to derive freshness/sequence

Files:

- `server/session/event_log.go`
- `server/session/conversation_freshness.go`
- `server/session/store.go`

What changed:

- `bootstrapEventLogStateLocked()` now uses `walkEventsFile(...)` to derive:
  - `last_sequence`
  - `conversationFreshness`
  - trailing EOF repair requirement
- this avoids eager `[]Event` allocation on open/bootstrap for the common reopen path

## New Tests Added

- `server/sessionview/dormant_cache_test.go`
  - `TestDormantTranscriptCacheReusesEntryForUnchangedRevision`
  - `TestDormantTranscriptCacheInvalidatesOnRevisionAdvance`
  - `TestDormantTranscriptCacheEvictsLeastRecentlyUsedEntry`
  - `TestServiceUsesDormantCacheForMainViewAndTailCoveredPages`

## Verification Run

Passed during this slice:

- `./scripts/test.sh ./server/sessionview`
- `./scripts/test.sh ./server/session ./server/sessionview`
- `./scripts/build.sh --output ./bin/builder`

## What This Improves

- repeated dormant session main-view reads are now effectively O(1) after the first build for a given revision
- repeated dormant tail / near-tail transcript reads are now effectively O(1) after the first build for a given revision
- session open/bootstrap no longer pays a full `[]Event` allocation just to reconcile freshness and last sequence

## What Is Still Open

### 1. Dormant arbitrary older pages still rescan the full event log

The new cache only accelerates:

- main-view summary reads
- `ongoing_tail`
- bounded page requests fully covered by the cached tail window

If the user scrolls far enough back in a dormant session, the service still walks the whole event log for that page.

### 2. True frontend detail-mode pagination/cache completion is still the next major slice

The frontend already has partial implementation in:

- `cli/app/ui_transcript_pager.go`
- `cli/app/ui_transcript_mode.go`
- `cli/app/ui_runtime_adapter.go`
- related tests in `cli/app/ui_mode_flow_test.go` and `cli/app/ui_runtime_adapter_test.go`

But this area is dirty/in-flight and should not be touched casually.

Concrete observations already verified locally:

- `uiDetailTranscriptWindow` already exists with:
  - bounded local cache (`uiDetailTranscriptMaxEntries = 1000`)
  - merge/replace logic
  - tail sync logic
  - `pageBefore()` / `pageAfter()` helpers
- `maybeRequestDetailTranscriptPage()` already issues edge-triggered page loads in detail mode
- deferred detail warmup already exists via `detailTranscriptLoadMsg`
- there are already tests covering:
  - deferred initial detail load
  - duplicate detail refresh suppression
  - detail-mode no-op for native history rebuild
  - session-change reset
  - live-tail/detail-window interaction

That means the next CLI cut should extend the current pager model, not introduce a second pagination architecture.

Safest likely files for the next CLI-side cut:

- `cli/app/ui_transcript_pager.go`
- `cli/app/ui_transcript_mode.go`
- narrow matching tests in:
  - `cli/app/ui_mode_flow_test.go`
  - `cli/app/ui_runtime_adapter_test.go`

Most collision-prone files to avoid broad edits in unless necessary:

- `cli/app/ui.go`
- `cli/app/ui_runtime_adapter.go`
- any unrelated dirty `cli/app/*server*` files

## Frontend Audit Result

The read-only audit completed and confirmed that detail-mode pagination is not missing wholesale; it is partially implemented and should be extended rather than replaced.

### Already implemented

- `cli/app/ui_transcript_pager.go`
  - bounded detail window (`page size 250`, `max cached entries 1000`, edge-triggered paging helpers)
  - `replace`, `merge`, `syncTail`, `pageBefore`, `pageAfter`
- `cli/app/ui_transcript_mode.go`
  - lazy detail entry
  - deferred bounded transcript load
  - edge-triggered page requests in detail mode
- `cli/app/ui_runtime_adapter.go`
  - keeps ongoing tail state and detail-window state separate
  - live projected transcript entries append into both
  - detail applies skip native-history rebuilds
- tests already cover:
  - lazy detail entry / deferred load / edge paging
  - duplicate detail refresh suppression
  - session-change reset
  - retry recovery across both modes

### Still missing for a true bounded detail cache

- frontend still keeps one contiguous merged detail window, not a real multi-page cache
- `sessionRuntimeClient` still caches only one last transcript page globally, not keyed by request/window
- request dedupe is not wired even though `lastRequest` / `pageRequestEqual` already exist
- no explicit viewport-anchor compensation was verified when `trimAround(...)` drops older loaded entries
- pager invariants are tested mostly indirectly, not with direct unit coverage for trim/merge/request generation

### Safest next CLI-side cut

Start with request dedupe / request-keyed transcript caching in the cleaner files:

- `cli/app/ui_runtime_client.go`
- `cli/app/ui_runtime_client_test.go`
- `cli/app/ui_transcript_mode.go`
- `cli/app/ui_runtime_sync.go`
- `cli/app/ui_mode_flow_test.go`

Avoid broad edits first in the more collision-prone in-flight files:

- `cli/app/ui_transcript_pager.go`
- `cli/app/ui_runtime_adapter.go`
- `cli/app/ui_runtime_adapter_test.go`

### 3. Runtime-client request-keyed caching follow-up

Landed in the current worktree during this checkpoint session:

- `cli/app/ui_runtime_client.go`
- `cli/app/ui_runtime_client_test.go`

What it adds:

- request-keyed transcript-page cache on top of the existing default-tail cache
- exact-request reuse for `LoadTranscriptPage(req)`
- preserved authoritative behavior for `RefreshTranscript()`
- no broad edits to the dirtier pager/adapter files

Validation already run:

- `./scripts/test.sh ./cli/app -run 'TestRuntimeClientLoadTranscriptPageDefaultsToOngoingTail|TestRuntimeClientLoadTranscriptPageReusesFreshCachedPageForSameRequest|TestRuntimeClientLoadTranscriptPageCachesByRequestKey|TestRuntimeClientRefreshTranscriptBypassesFreshCachedPage'`
- `./scripts/test.sh ./cli/app -run 'TestCtrlTDeferredDetailLoadUsesBoundedTranscriptPageRequest|TestDetailEdgePagingWaitsForFirstNavigationToResolveMetrics|TestCtrlTDeferredDetailLoadSkipsDuplicateDetailRebuildEndToEnd'`
- `./scripts/build.sh --output ./bin/builder`

## Recommended Next Step

Do this next:

1. implement request dedupe / request-keyed caching for transcript page loads in `sessionRuntimeClient`
2. thread that through the existing deferred detail-load / edge-paging flow
3. add focused tests in `ui_runtime_client_test.go` and `ui_mode_flow_test.go`
4. only then consider deeper pager-window ownership changes in the dirtier files

## Important Caution

The main workspace still contains many unrelated dirty `cli/app` files from parallel work. Do not assume this checkpoint owns those edits.

## Extra Verification Run

In addition to package-local tests, this slice also passed:

- `./scripts/test.sh ./server/core ./server/registry`

That covers the loopback/core path that constructs `sessionview.NewService(...)` with `registry.NewPersistenceSessionResolver(...)`.
