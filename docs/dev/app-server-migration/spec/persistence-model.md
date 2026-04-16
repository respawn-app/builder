# App Server Migration: Persistence Model

Status: locked design baseline for the storage migration track

This document defines the post-migration storage model for Builder.

It intentionally separates:

- structured server-owned metadata and resources,
- large append-only session artifacts,
- one-time migration mechanics from the legacy workspace-container layout.

## Storage Direction

Builder moves to a hybrid persistence model.

Authoritative storage is split by data shape:

- SQLite is authoritative for structured metadata and server-owned resources.
- Files remain authoritative for large append-only session artifacts.

This is a deliberate design choice.

The migration does not move the session transcript log into a relational model right now.

## Source Of Truth Split

### SQLite Is Authoritative For

- projects
- workspaces
- worktrees
- session metadata
- session execution target
- run metadata
- ask/approval metadata
- runtime leases
- request-id deduplication state
- future task/board/agent/schedule metadata

### Files Remain Authoritative For

- `events.jsonl`
- `steps.log`
- future large attachments, artifacts, exports, or raw process logs

### Explicit Non-Goal

- `session.json` does not survive the migration as an authoritative file.
- After successful migration, session metadata authority lives in SQLite.

## Durable Domain Model

The durable top-level model is:

- `project`
- `workspace`
- `worktree`
- `session`

`workspace` is the neutral durable execution-root concept.

It is intentionally not named `repo`.

If git metadata exists, Builder may expose it and derive worktree records from it, but the durable model must not be coupled to git terminology.

## Filesystem Layout

Post-migration persistence root shape:

```text
<persistence-root>/
  auth.json
  db/
    main.sqlite3
    main.sqlite3-wal
    main.sqlite3-shm
  projects/
    <project-id>/
      sessions/
        <session-id>/
          events.jsonl
          steps.log
          artifacts/
  migration-backups/
    pre-project-v1-<timestamp>/
      ... legacy tree backup ...
  migrations/
    project-v1/
      manifest.json
      state.json
      staging/
        <timestamp>/
          main.sqlite3
          ... staging metadata ...
```

Notes:

- `projects/.../sessions/...` contains only file-backed artifacts after migration.
- SQLite owns the catalog and resource relationships.
- Builder should not introduce separate persisted filesystem index files once SQLite owns metadata authority; build any additional lookup caches in memory at runtime instead.
- `migration-backups/` persists the old tree after successful migration until the user chooses to delete it.

## SQLite Runtime Mode

The metadata database should live in a single server-owned SQLite file.

Direction:

- one primary metadata database file under `db/`
- WAL mode enabled for normal runtime operation
- SQL migrations are explicit, ordered files
- schema access is generated from hand-written SQL via `sqlc`

## SQLite Shape Direction

The schema should stay intentionally narrow.

Do not create giant 50-column tables to mirror the full transcript/runtime payload model.

Use relational columns for stable/queryable keys, and JSON columns for unstable nested metadata where needed.

### Core Tables

Expected first-wave tables:

- `projects`
- `workspaces`
- `worktrees`
- `sessions`
- `runs`
- `asks`
- `approvals`
- `runtime_leases`
- `client_request_dedup`

Explicit non-requirement for the migration:

- live process APIs and process control are part of the frontend/server split
- durable process-history/resource persistence is not required for that split
- if Builder later wants historical process records after process exit or server restart, that should be treated as a separate feature rather than as migration-critical metadata authority

### JSON Column Guidance

Prefer JSON columns for metadata that is:

- nested,
- unstable,
- rarely filtered on,
- currently coupled to existing session metadata shape.

Likely JSON-column candidates:

- locked session contract snapshot
- continuation metadata
- usage-state snapshot
- optional workspace/git metadata snapshot

## Session Metadata Model

SQLite becomes authoritative for session metadata after migration.

Minimum durable session metadata includes:

- `session_id`
- `project_id`
- session name
- parent session id
- input draft
- current execution target
- created/updated timestamps
- in-flight flags
- agents-injected flag
- locked contract snapshot
- continuation metadata
- usage snapshot as needed

The current execution target shape is:

- `workspace_id`
- optional `worktree_id`
- `cwd_relpath`

`cwd_relpath` exists so Builder preserves the exact working directory inside a workspace/worktree without storing session-specific absolute cwd paths as durable identity.

## Workspace And Worktree Model

### Workspace

A `workspace` maps 1:1 to one canonical execution root path.

It may be git-backed or not.

Minimum durable metadata:

- `workspace_id`
- `project_id`
- canonical absolute path
- availability state
- optional git metadata snapshot
- display metadata
- primary-workspace marker when needed

### Worktree

A `worktree` is optional workspace child metadata when the workspace is git-backed and worktree management matters.

Git remains the source of truth for actual worktree topology.

Builder stores only the additive metadata it needs for product behavior.

## Write Model And Drift Rules

The hybrid store cannot provide one physical atomic commit across SQLite and artifact files.

The design should therefore be explicit about authority and repair.

### Transcript Write Rule

- `events.jsonl` remains the authority for committed transcript content.
- Session transcript summary metadata in SQLite may be repaired from the file when needed.

Recommended write order for transcript-affecting operations:

1. append and flush transcript payload to `events.jsonl`
2. update SQLite metadata in one transaction
3. on startup/open, repair SQLite summary drift from the transcript file if the prior process crashed between those steps

This preserves the existing practical bias that transcript durability matters more than summary counters.

### Metadata-Only Write Rule

If an operation touches only structured metadata and not transcript artifacts, SQLite alone is authoritative.

## Lazy Session Creation

Interactive session creation remains lazily durable.

That means:

- creating a new interactive session does not immediately require a SQLite row or artifact directory,
- the session becomes durable only on the first real durable write,
- headless/subagent flows may still become durable quickly because they usually write immediately.

This preserves the current observable behavior where abandoned brand-new interactive sessions can disappear without leaving durable clutter.

## One-Time Migration Design

Migration is a blocking startup flow.

Normal startup does not proceed until migration either succeeds or reports a blocking error.

### Goals

- no long-lived dual-read legacy codepath for normal runtime
- no `session.json` after successful migration
- staged migration work before cutover
- old tree preserved as a timestamped backup after success

### Migration Input

Legacy input is the current workspace-container layout under:

```text
<persistence-root>/sessions/<workspace-container>/<session-id>/
```

plus any read-only legacy workspace index inputs already supported today.

### Migration Output

- new SQLite metadata store
- new project/workspace/worktree/session resource graph
- session artifact directories under `projects/<project-id>/sessions/<session-id>/`
- no `session.json` in migrated session directories

### Migration Mapping Rules

- each distinct legacy workspace root/container becomes exactly one migrated `project` and one primary `workspace`
- the migration must not guess or auto-merge multiple legacy roots into one multi-workspace project
- if a legacy workspace is git-backed and a worktree distinction is meaningful, create a `worktree` record
- otherwise the workspace alone is the execution target

### Staging And Cutover

The migration should complete in two conceptual stages:

1. staging
   - scan and validate the legacy tree
   - compute the full migration manifest
   - build the target SQLite database in a staging area
   - validate path canonicalization and target layout ahead of cutover
2. cutover
   - take an exclusive migration lock
   - move the legacy tree to a timestamped backup location
   - install the staged SQLite database
   - move session artifact directories into the new canonical layout using same-filesystem renames where possible
   - verify the new layout
   - mark migration complete

The cutover step is intentionally short and runs only after the staged metadata build has succeeded.

### Failure Behavior

- if staging fails, Builder leaves the old live tree untouched
- if cutover fails after exclusive lock is taken, Builder stays in migration-required mode and surfaces explicit recovery state instead of booting normally on a half-cutover tree
- successful migration keeps the old tree under `migration-backups/` for manual deletion later

## Workspace Relocation After Migration

If a workspace path later disappears or moves:

- the workspace becomes `missing`
- Builder must not auto-rebind to an inferred replacement path
- rebinding is always an explicit user action
- session execution targets continue to point to stable ids plus `cwd_relpath`, so rebind does not require session identity changes

## Tooling Direction

For the SQLite metadata store:

- use hand-written SQL as the source of truth
- generate typed Go access code with `sqlc`
- keep migrations explicit SQL files rather than framework-owned runtime schema mutation

This keeps the metadata schema explicit while avoiding ORM-owned persistence behavior.
