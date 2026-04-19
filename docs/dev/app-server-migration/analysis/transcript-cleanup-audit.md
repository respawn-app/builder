# Transcript Cleanup Audit

Status: reconciled stabilization checkpoint

Last updated: 2026-04-05

## Purpose

This document closes the remaining cleanup/process item from `execution/transcript-stabilization-plan.md`:

- `Close quick symptom patches that do not fit the stabilized model`

Its job is narrow: confirm whether the current transcript stabilization work still relies on ad hoc symptom patches instead of the documented ownership/freshness model.

## Audit Result

No known transcript-specific symptom patch remains open on the migrated frontend path.

The current implementation uses explicit model-aligned mechanisms instead:

- committed transcript recovery goes through `session.getTranscriptPage`
- live activity remains on session-activity events
- transcript page replacement is guarded by explicit revision/freshness rules
- stream-gap recovery triggers hydrate rather than attempting replay reconstruction
- diagnostics are debug-only and instance-scoped rather than package-global

## What Was Audited

### 1. User-flush handling

Result: aligned

- `EventUserMessageFlushed` no longer triggers transcript sync as a side effect
- the event only appends the committed user entry immediately and preserves later live commentary

This is now part of the ownership model, not a quick patch.

### 2. Hydrate overwrite guards

Result: aligned

- same/older revision transcript pages are rejected when they would wipe newer visible live state
- equal-revision pages are still accepted for authoritative runtime-only tail/error changes

These guards are the explicit freshness contract documented in `analysis/transcript-freshness-rules.md`.

### 3. Stream-gap recovery

Result: aligned

- gap recovery remains a repair trigger only
- it does not attempt to reconstruct transcript correctness from replayed deltas

This matches the documented ownership split between live activity and committed transcript hydration.

### 4. Diagnostics

Result: aligned

- transcript diagnostics are gated behind `BUILDER_TRANSCRIPT_DIAGNOSTICS=1`
- logger wiring is instance-scoped instead of package-global

This is observability infrastructure, not a user-visible stabilization hack.

## Explicit Non-Issues

These are intentionally not classified as cleanup debt for this slice.

### Remote raw commentary-entry parity

This defer note is stale.

The runtime now emits a committed assistant commentary event for tool-using turns, projection forwards it onto session activity, and gateway coverage proves the remote ordering `user -> assistant_progress -> commentary -> tool_call -> tool_result -> final`.

This is no longer cleanup debt and should not be tracked as an explicit defer.

### Release confidence and branch usability

These are product/release decisions, not stabilization implementation debt.

They should stay visible in the release-gate section rather than being treated as missing low-level cleanup.

## Conclusion

The cleanup/process item can be marked complete.

Remaining open work is no longer "remove quick patches." It is:

- an intentional remote raw-stream parity defer
- branch/release confidence decisions outside this implementation slice
