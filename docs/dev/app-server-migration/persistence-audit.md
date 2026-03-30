# App Server Migration: Persistence Audit

Status: initial Phase 0 audit

This document records the current persistence shape that the server migration must adopt rather than accidentally break.

It is intentionally conservative. The goal is to identify durable truth, current layout assumptions, and minimum metadata pressure before any storage redesign is attempted.

## Current On-Disk Shape

Primary implementation:

- `internal/session/store.go`
- `internal/session/types.go`
- `internal/session/event_log.go`
- `internal/config/config_workspace_index.go`

Current layout is organized under the configured persistence root:

- persistence root
  - `sessions/`
    - workspace container
      - session id
        - `session.json`
        - `events.jsonl`
        - `steps.log`

Adjacent/root-level persistence files that matter during migration:

- `workspaces.json`
  - legacy workspace-container mapping still adopted on lookup
- `auth.json`
  - auth state adjacent to sessions, but not part of session restore

Important current assumptions:

- workspace roots resolve to a stable workspace container via `config.ResolveWorkspaceContainer`
- sessions are currently partitioned by that workspace container
- `session.OpenByID` searches under `persistenceRoot/sessions/...`
- legacy workspace-container mapping is still supported through the workspace index logic in config

## Durable Files

## `session.json`

Current metadata shape comes from `internal/session/types.go`.

Notable fields include:

- `session_id`
- `name`
- `first_prompt_preview`
- `input_draft`
- `parent_session_id`
- `workspace_root`
- `workspace_container`
- continuation context
- timestamps
- `last_sequence`
- `model_request_count`
- `in_flight_step`
- `agents_injected`
- locked model/provider contract

This is clearly durable metadata, not just cache.

## `events.jsonl`

Current event shape:

- monotonically increasing `seq`
- timestamp
- kind
- optional `step_id`
- opaque JSON payload

The event log is append-oriented and periodically compacted in place.

Important behavioral details from `internal/session/event_log.go`:

- trailing partial lines may be repaired or dropped on read
- the file is treated as authoritative enough to rebuild sequence state and conversation freshness
- compaction rewrites the JSONL file, but does not appear to transform logical event meaning

Current durable event kinds that matter to restore/adoption include at least:

- `message`
- `tool_completed`
- `local_entry`
- `history_replaced`
- `prompt_history`

## `steps.log`

`steps.log` exists as runtime diagnostics and observability, but it does not appear to be restore-critical state.

That matters because the migration should not confuse operational logs with durable session truth.

## Durable Versus Derived

Likely durable source of truth today:

- session identity and metadata in `session.json`
- ordered session event history in `events.jsonl`
- parent/child lineage through `parent_session_id`
- workspace root/container association in session metadata
- prompt history persisted through explicit `prompt_history` events

Likely derived or reconstructable:

- picker summaries returned by `ListSessions`
- conversation freshness state derived from event history
- some current status/read-model surfaces in `internal/app`
- transcript/chat snapshots and ongoing assistant deltas synthesized in runtime/UI layers

## Migration Pressure Points

The new server architecture needs additional durable concepts that do not obviously exist as first-class storage objects yet:

- project registry metadata
- run identity and run outcome metadata
- approval and ask state records
- process metadata and retention metadata
- server-level attachment/runtime visibility that is not purely UI-local

## Recommended Minimum Metadata Additions

To avoid destructive rewrite, the conservative direction is:

- keep current session directory and event log shape readable
- add the smallest possible metadata needed for server-owned resources
- avoid replacing `events.jsonl` with a new storage engine during the migration itself

Minimum additions likely needed:

- stable `project_id` mapping for each current workspace container or canonical root
- explicit run metadata associated with a session
- approval/ask durable records or clearly reconstructable event representations
- process records sufficient for process list/inspect semantics independent of shell log text
- project availability metadata or registry state outside individual sessions

## Adoption Risks

## 1. Workspace Container Is Already A Strong Storage Assumption

Risk:

- current storage and lookup logic strongly assumes workspace-container partitioning
- the new project model must adopt that reality without letting storage layout leak into protocol identity

## 2. Session Lookup Is Persistence-Root Driven

Risk:

- `session.OpenByID` currently searches under the legacy/current persistence-root structure
- any server migration that changes lookup rules must either preserve this path or introduce lazy adoption logic

Related constraint:

- legacy flat persistence layouts are already ignored by `OpenByID`, so adoption logic must be explicit rather than assumed

## 3. Event Log Meaning Is Only Partially Formalized

Risk:

- `events.jsonl` is durable, but the migration docs intentionally do not want to make protocol events equal storage events
- a server read-model layer will need to consume existing session events without turning the protocol into accidental event-sourcing

## 4. Existing Metadata Is Session-Centric, Not Project/Run-Centric

Risk:

- new project and run resources may be under-modeled if Phase 1 assumes they already exist on disk in a clean form

## 5. No Explicit Schema Version Markers Exist Yet

Risk:

- current `session.json` and event envelopes do not carry a schema/version field
- migration branching would otherwise have to depend on heuristic interpretation of payload shape

## 6. Prompt-History Adoption Already Has Mixed Legacy/New Semantics

Risk:

- prompt history currently bridges old and new behavior in a way that can surprise migration work
- before first explicit `prompt_history`, legacy user `message` events may still backfill prompt history
- after explicit `prompt_history` starts, later user `message` events are not adopted the same way

This is a real sharp edge for partially upgraded data and should be preserved or normalized deliberately.

## 7. Process And Approval Durability Are Under-Modeled Today

Risk:

- background/process state is mostly ephemeral; persistence captures final injected notices rather than a durable process resource model
- approval state does not yet exist as a durable first-class persistence concept

## Recommended Phase 0 Decision Direction

- prefer direct readability or lazy adoption of existing session directories
- keep old `session.json` / `events.jsonl` resumable
- add server-owned metadata incrementally rather than mass-migrating storage up front
- treat project registry state as additive to the existing workspace-container/session structure

## Recommended Minimum Additive Metadata

If the migration adds persistence fields, the conservative starting point is:

- `session.Meta.schema_version`
- `session.Event.event_version`
- `session.Meta.project_id`
- `session.Event.event_id` for idempotent import/sync
- `session.Event.run_id` in addition to current `step_id`
- `session.Meta.origin` such as `interactive`, `headless`, `server`, or `subagent`

For new capabilities, prefer additive event kinds or records over rewriting current message/tool semantics:

- approval lifecycle events keyed by `approval_id`
- process lifecycle/output metadata keyed by `process_id`

## Inspection Targets For Follow-Up

- `internal/session/fork.go`
- `internal/session/prompt_history.go`
- `internal/session/conversation_freshness.go`
- `internal/app/launch_planner.go`
- `internal/config/config_workspace_index.go`

This is the minimum audit needed before Phase 1 extraction begins.
