# Transcript Pagination Slice

## Problem

The current app-server migration still treats transcript history as fully materialized state in multiple hot paths:

- runtime restore reads all of `events.jsonl` into memory before replaying
- transcript hydration defaults to full committed transcript reads
- ongoing-mode native replay can re-emit the entire committed transcript into scrollback
- detail mode still assumes a fully resident transcript slice in the frontend

This is not viable for long-lived sessions with many compactions or millions of transcript entries.

## Goals For This Slice

1. Stop hot-path full transcript hydration for ongoing mode.
2. Stop runtime restore from reading the full event log into memory in one shot.
3. Introduce protocol support for both offset-based and page-based transcript reads.
4. Preserve correctness of the current CLI while setting up the next slice for true detail-mode pagination/cache.

## Scope Landed In This Slice

- `session.getTranscriptPage` request now supports:
  - offset/limit
  - page/page_size
  - `ongoing_tail` window hint
- the CLI runtime transcript sync path now defaults to `ongoing_tail`
- the server computes the ongoing transcript window as:
  - `max(last 500 committed entries, entries since last compaction)`
- runtime restore now walks `events.jsonl` incrementally instead of calling `ReadEvents()`

## Explicitly Deferred To The Next Slice

- dormant transcript paging without any full replay fallback
- frontend detail-mode two-way async page loading with bounded cache purging
- protocol search/jump affordances on top of transcript pagination
- replacing remaining snapshot-style read helpers built on `Store.ReadEvents()`

## Design Notes

- `ongoing_tail` is intentionally a server-owned window policy. The frontend should not need to reconstruct compaction boundaries itself.
- page-based pagination is exposed now so future frontends do not need another protocol change to adopt numbered paging.
- runtime restore is allowed to scan the whole event log, but it must do so incrementally and retain only runtime state, not `[]Event`.
- detail-mode pagination/cache remains a separate slice because it changes frontend state ownership and scroll behavior substantially.
