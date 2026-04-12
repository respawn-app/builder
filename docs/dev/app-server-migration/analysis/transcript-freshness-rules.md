# Transcript Freshness Rules

Status: implemented stabilization contract

Last updated: 2026-04-05

## Purpose

These are the working overwrite/freshness rules for transcript stabilization on `app-server-integration`.

They define when the frontend is allowed to replace transcript-visible state and when it must preserve already-visible live state instead.

This is a stabilization contract, not a wire-level protocol design.

## Rule 1: Live activity and transcript hydration have different jobs

Live session activity exists to make active sessions feel immediate.

Transcript hydration exists to recover or rehydrate committed session history.

The frontend must not treat those jobs as interchangeable.

## Rule 2: A hydration read is not permission to clear newer visible live state

If the UI has already shown newer live activity, a transcript read must not blindly replace that visible state unless the frontend can prove the read is at least as fresh as what is currently visible.

Corollary:

- a recovery-triggered transcript read is not automatically authoritative for transient live state

## Rule 3: Explicit transcript entries carried on live events may append committed state immediately

If a live event carries transcript entries directly, the frontend may append those entries into committed transcript projection immediately.

This is allowed because the server already normalized those entries for transcript visibility.

Corollary:

- appending explicitly carried transcript entries is different from replacing the entire transcript window from a read

## Rule 4: `conversation_updated` means "committed transcript may have changed"

During stabilization, `conversation_updated` must be treated as a coarse repair signal only.

It means the frontend may need an authoritative transcript hydrate.

It does not mean the frontend may clear or replace newer visible live activity first.

If a `conversation_updated` event already carries transcript entries, those entries are part of the committed transcript delta and may advance committed frontend state directly.

Corollary:

- ordinary runtime transcript-bearing events stay transient until a later `conversation_updated` or hydrate establishes committed authority

## Rule 5: Recovery must invalidate transient live state deliberately, not accidentally

If reconnect or stream-gap recovery requires transient live state to be discarded, that must happen as an explicit recovery decision.

It must not happen as a side effect of applying a transcript page.

## Rule 6: Ongoing and detail committed transcript must converge from the same committed model

Ongoing mode and detail mode may use different render projections, but they must converge from the same committed transcript state.

Corollary:

- paging/window logic is allowed to change what detail mode has loaded
- paging/window logic is not allowed to become a separate source of transcript truth

## Rule 7: Recovery paths may mark transcript state dirty, but they do not own merge semantics

Resubscribe/gap handling is allowed to request transcript repair.

It is not allowed to decide on its own how transcript hydration should overwrite currently visible state.

## Rule 8: Cached reads are optimization, not authority

Cached transcript or main-view reads must never be treated as a competing authority against newer visible state.

If cached data is used, it must still obey the same overwrite rules as a fresh hydrate.

## Immediate Consequences For Execution

- [x] stop treating every transcript read as a safe full replacement
- [x] separate "dirty, needs hydrate" from "safe to overwrite current visible state"
- [x] make `conversation_updated` a hydrate trigger, not a blanket permission to reset the view
- [x] ensure recovery tests cover stale-read vs newer-live-state cases explicitly
- [x] ensure remote and loopback paths obey the same overwrite rules

The remaining remote raw-stream commentary-entry gap does not change these overwrite rules. It is a separate event-shape defer.

This document does not claim full raw event parity between loopback and remote paths. It only defines the overwrite/freshness contract that both paths must obey once transcript-visible state reaches the frontend.

## Exit Condition

These rules are implemented well enough when all of the following are true:

- [x] a stale transcript read cannot erase newer live-visible activity
- [x] recovery/hydration restores committed transcript without blanking active sessions
- [x] loopback and remote paths obey the same replacement rules
- [x] rendered ongoing-mode tests prove the main overwrite races are closed
