# App Server Migration: Session And Run Model

Status: planning baseline

This document defines the minimum resource and execution model that must be treated as settled before implementation starts.

Without this model, queue semantics, reconnect behavior, approval races, interrupt behavior, and process ownership will drift.

## Core Resources

## Project

A `project` is a durable server-owned work container.

It is the top-level grouping boundary for sessions, workspaces, and future workspace-management features.

It may span one or more workspaces and is not defined by one path or one worktree path.

Minimum fields:

- `project_id`
- display name
- availability state
- workspace summary metadata
- session summary metadata

Protocol identity is always `project_id`, not a filesystem path.

## Workspace

A `workspace` is a durable child resource of `project` that maps 1:1 to exactly one canonical execution root.

Minimum fields:

- `workspace_id`
- `project_id`
- canonical workspace root path
- availability state
- optional git metadata when available
- worktree summary metadata

Protocol identity is `workspace_id`, not path spelling or git metadata.

## Worktree

A `worktree` is optional child metadata of a git-backed workspace and a candidate session execution target.

Worktrees belong to workspaces, not projects directly.

Git remains the source of truth for existing worktrees. Builder may store additive metadata and links, but it does not replace git as the authoritative worktree registry in v1.

Minimum fields:

- `worktree_id`
- `workspace_id`
- canonical worktree path
- availability state
- branch metadata when available
- whether it is the primary workspace root worktree

Protocol identity is `worktree_id`, not a path.

## Session

A `session` is the durable conversational and work container.

Minimum fields:

- `session_id`
- `project_id`
- current execution target `(workspace_id, worktree_id?, cwd_relpath)`
- durable transcript state
- durable session metadata and config
- lineage metadata
- zero or more historical runs

The session is the durable object a user resumes later.

Changing workspace or worktree does not create a new session by itself; it updates shared session state.

## Run

A `run` is a single execution attempt or span inside a session.

Minimum fields:

- `run_id`
- `session_id`
- status
- timestamps
- interrupt or completion outcome
- references to active process, approval, ask, or delegated task state when relevant

Runs are execution spans, not replacement sessions.

## Process

A `process` is a first-class runtime resource distinct from its output stream.

Minimum fields:

- `process_id`
- owning session or run
- command metadata
- cwd or project association
- status
- timestamps
- exit result when completed
- output retention metadata

`/ps` and related UI should be driven by process resources, not inferred from ad hoc shell logs.

## Ask And Approval

`ask` and `approval` are first-class resources with explicit lifecycle state.

Minimum lifecycle states:

- `pending`
- `answered`
- `denied`
- `expired`
- `cancelled`

The first committed authoritative answer wins. Later responders receive a deterministic already-resolved result.

## Internal Delegated Work

Internal delegated workers or subagents are not child sessions by default.

Rule for v1:

- user-visible branch, fork, or review workflow -> child session
- internal delegated worker -> child run or run-scoped task

This keeps session lineage meaningful and prevents the session inventory from being polluted by runtime internals.

## v1 Execution Invariants

- A session may accumulate multiple runs over time.
- v1 allows at most one active primary run per session.
- Starting a new primary run while one is already active returns an explicit busy error.
- Frontends may keep local queued-input or draft UX, but the server does not provide a cross-client queued primary-run facility in v1.
- Session execution target is shared across attached frontends and must not be modeled as a client-local override.
- Reads and hydration queries remain available regardless of active-run state.
- Approval responses, ask responses, process control, and run interrupt remain available while a run is active.
- Runtime tuning settings such as `/thinking` and `/fast` are session-scoped live settings. When a busy-state rule permits the update during an active run, the active run observes the new value and future runs inherit it until changed again.

## Busy-State Mutation Baseline

The migration must preserve the observable distinction between commands that are allowed while busy and commands that are not.

Current compatibility baseline from the CLI command registry:

- Allowed while busy:
  - status reads and overlays
  - process inspection and control
  - session rename
  - thinking-level changes or reads
  - supervisor policy changes or reads
  - auto-compaction policy changes or reads
- Not allowed while busy in the current CLI baseline:
  - starting another primary run
  - compaction request
  - fast-mode toggle or readback

If the migration changes any of these semantics, the change must be deliberate and called out in the compatibility proof.

## Ordering And Serialization

- Mutating operations serialize per session.
- Ordering is authoritative on the server, not inferred by clients.
- Idempotency is keyed by `client_request_id` within explicit server-defined scope and retention window.
- Runtime lease acquisition and release use a separate explicit lease identity rather than overloading request-id idempotency keys.
- Reconnect does not resume or reclaim a previous lease id; the client rehydrates, reattaches, and acquires a fresh lease if it still needs runtime residency.
- A duplicated mutating request must not create duplicated prompt submission, approval outcome, or process-control effect.

The protocol must be explicit about whether a rejected operation failed because the session was busy, because a resource was already resolved, or because the caller sent a duplicate request.

## Transcript And Live Output

- Durable transcript state and ephemeral live activity are distinct.
- Transcript hydration and reconnect use typed reads and transcript pages.
- Live session activity is not the authoritative transcript transport; it is optional progressive state for an already-attached frontend.
- If a frontend misses part of a live stream, recovery is rehydrate plus resubscribe.
- Each frontend should maintain one committed-transcript authority per session; detail windows, ongoing-mode buffers, and native transcript projections are derived render state only.
- `session.getMainView` and similar status hydrators are not a substitute transcript transport; committed transcript content belongs to dedicated transcript reads.

## Attachment Model

- Attachment does not imply snapshot delivery.
- A frontend attaches to a project or session context, then explicitly hydrates via typed reads, then subscribes to the streams it needs.
- Frontends may attach to the same session concurrently.
- Runtime residency or ownership must not rely on attach request ids; if the server keeps session runtime leases, those leases need explicit identity and disconnect cleanup semantics.

## Phase 4 Scope Guardrail

The storage migration should land the full `project > workspace > worktree` server model.

However, the initial CLI UX may remain workspace-first as long as the server-side model, ids, and query surfaces do not hard-code that simplification.

## Note On Busy-State Interaction

Session-wide live settings still obey busy-state compatibility rules.

That means the migration keeps the distinction between commands that may update live session state during an active run and commands that remain blocked while busy.
