# Phase 2 Deferred / Optional Work

This document collects Phase 2 ideas that are intentionally **not** required to ship the current TUI against one device-global server.

If an item here becomes necessary for a real desktop/web frontend later, it can be promoted back into `plan.md` with concrete scope and acceptance criteria.

## Deferred Principles

- Do not build future-facing resource families until a real frontend forces the need.
- Do not introduce second systems for transcript-adjacent state when the current TUI already works transcript-first.
- Do not optimize for speculative multi-client collaboration while same-session single-controller semantics are still explicitly temporary.

## Deferred Resource Families

These are plausible later server resources, but they are not needed to ship the current TUI:

- first-class `ask.*` resources such as `ask.get` / `ask.listPendingBySession`
- first-class `approval.*` resources such as `approval.get` / `approval.listPendingBySession`
- prompt-activity stream families dedicated to live ask/approval delivery
- richer project/workspace routes aimed at GUI-only inventory or side-panel UX
- optional convenience routes that duplicate information already available through transcript or existing TUI-critical reads

Current decision:

- asks and approvals remain transcript-driven for the current TUI
- transcript remains the source of narrative history and pending-action context for those flows

## Deferred Multi-Client Semantics

Not required now:

- allowing multiple clients to control or mutate the same session concurrently
- designing deterministic collaborative editing or concurrent prompt-submission semantics
- making ask/approval races first-class before a real non-TUI frontend needs them

Current temporary policy:

- multiple clients may attach/read the same session
- only one client may control/mutate a session at a time

## Deferred Stream Taxonomy

Not required now unless the current TUI proves it needs them materially:

- prompt activity stream families separate from transcript/session activity
- generalized event hub taxonomy for future GUI clients
- durable process-output retention/replay semantics beyond what the current TUI materially exercises
- optional stream families intended only for side panels, badges, or richer GUI affordances

Current shipping bias:

- keep only TUI-critical live surfaces in the active plan
- defer new stream families until a concrete frontend needs them

## Deferred Future-Proofing Work

Still valuable later, but not a blocker for current shipment:

- first-class resource storage for asks/approvals in SQLite
- richer resource indexes for GUI hydration and reconnect
- broader protocol route expansion beyond what the TUI uses today
- multi-client per-session collaboration semantics
- transport-level optimizations aimed at GUI-only interaction patterns

## Promotion Rule

Move an item from this file back into `plan.md` only when at least one of these becomes true:

- the current TUI cannot ship reliably without it
- a real new frontend cannot be built sanely without it
- stability/performance of the current server materially depends on it
