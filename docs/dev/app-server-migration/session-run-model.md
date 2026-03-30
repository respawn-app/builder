# App Server Migration: Session And Run Model

Status: planning baseline

This document defines the minimum resource and execution model that must be treated as settled before implementation starts.

Without this model, queue semantics, reconnect behavior, approval races, interrupt behavior, and process ownership will drift.

## Core Resources

## Project

A `project` is a durable server-local registration that permanently maps 1:1 to exactly one repository, one canonical workspace root, and one durable project or session container.

Minimum fields:

- `project_id`
- repository identity
- canonical root path
- display name
- availability state
- repository metadata when available
- session summary metadata

Protocol identity is always `project_id`, not a filesystem path.

## Session

A `session` is the durable conversational and work container.

Minimum fields:

- `session_id`
- `project_id`
- durable transcript state
- durable session metadata and config
- lineage metadata
- zero or more historical runs

The session is the durable object a user resumes later.

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
- Idempotency is keyed by `client_request_id` within explicit server-defined scope.
- A duplicated mutating request must not create duplicated prompt submission, approval outcome, or process-control effect.

The protocol must be explicit about whether a rejected operation failed because the session was busy, because a resource was already resolved, or because the caller sent a duplicate request.

## Transcript And Live Output

- Durable transcript state and partial live output are distinct.
- Partial assistant output may be streamed live before the durable transcript record is finalized.
- Reconnect must recover durable transcript state from typed reads, not by assuming the live stream is fully replayable.
- If a frontend misses part of a live stream, the protocol must signal a gap explicitly.

## Attachment Model

- Attachment does not imply snapshot delivery.
- A frontend attaches to a project or session context, then explicitly hydrates via typed reads, then subscribes to the streams it needs.
- Frontends may attach to the same session concurrently.

## Note On Busy-State Interaction

Session-wide live settings still obey busy-state compatibility rules.

That means the migration keeps the distinction between commands that may update live session state during an active run and commands that remain blocked while busy.
