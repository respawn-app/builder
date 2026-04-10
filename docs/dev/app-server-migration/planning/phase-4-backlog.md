# Phase 4 Storage Backlog

Status: planning baseline

Purpose:

- turn the Phase 4 architecture into an implementation-ready backlog
- define the first concrete API and data-model surfaces
- make adoption work explicit so coding does not re-open product scope

This file is intentionally narrower than `plan.md`.

`plan.md` explains the phase. This file names the concrete first slices to build.

## First Implementation Checklist

This is the minimum sequence the next implementation turn should be able to start from directly.

1. Define the first SQLite schema and migration set for:
   - `projects`
   - `workspaces`
   - `worktrees`
   - `sessions`
   - `runtime_leases`
2. Add the storage boundary and generated typed DB access (`sqlc`) without changing transcript files yet.
3. Add the startup migration gate that detects legacy layout and blocks normal startup.
4. Implement staged metadata build in a migration staging area.
5. Implement cutover verification rules before normal startup resumes.
6. Implement final cutover:
   - install staged SQLite metadata DB
   - move session artifact directories into `projects/<project-id>/sessions/<session-id>/`
   - remove `session.json` from migrated sessions
   - preserve timestamped backup of the old tree
7. Verify migrated sessions can hydrate, resume, and preserve lazy interactive-session semantics.

## Phase 4 Scope Lock

- The server becomes app-global rather than workspace-scoped.
- The durable domain model is `project > workspace > worktree`.
- Persistence is hybrid: SQLite for structured metadata/resources, files for large session artifacts.
- Sessions belong to projects and carry mutable execution targets `(workspace_id, worktree_id?, cwd_relpath)`.
- CLI remains workspace-first outside startup and registration flows.
- Runtime leases are explicit server-side identities; reconnect rehydrates, reattaches, and acquires a fresh lease.
- `session.json` is removed after successful migration; SQLite becomes authoritative for session metadata.

## Workstream 1: Global Server Identity And Discovery

Goal:

Replace workspace-scoped discovery/identity with app-global server identity.

First concrete surfaces:

- `shared/protocol.ServerIdentity`
  - stop implying one `project_id` / `workspace_root` per server
  - expose server-process identity and capabilities only
- discovery record shape
  - move from workspace-owned discovery assumptions to app-global discovery
- CLI attach-or-start resolution
  - discover one compatible local server process first
  - only after attach should cwd/path resolution decide project or workspace context

Acceptance slice:

- one server can be discovered from two different workspace roots
- handshake no longer claims one workspace/project scope

## Workstream 2: Project / Workspace / Worktree Registry

Goal:

Introduce the durable server-owned resource graph without forcing immediate broad project UX.

First concrete surfaces:

- new server-owned registry/service package for:
  - project registration
  - workspace registration inside a project
  - workspace/worktree lookup by path
  - availability refresh
- typed ids:
  - `project_id`
  - `workspace_id`
  - `worktree_id`
- typed read models in `shared/clientui` / `shared/serverapi` for at least:
  - project summaries
  - workspace summaries
  - worktree summaries
  - cwd resolution result

Minimum query surface:

- `project.list`
- `project.getOverview`
- `project.resolvePath` or equivalent cwd/path-resolution query
- workspace/worktree metadata included where startup and status need it

Important guardrail:

- do not bake the current workspace-first CLI simplification into ids, persistence ownership, or protocol shape

## Workstream 3: Session Execution Target

Goal:

Move session execution context from implicit workspace/config state to explicit shared session state.

First concrete surfaces:

- session metadata additions for current `workspace_id`, optional `worktree_id`, and `cwd_relpath`
- session hydration reads include current execution target metadata
- status/read models expose current workspace/worktree to the CLI
- runtime preparation resolves cwd/config/tool wiring from the session execution target, not from frontend-local workspace assumptions

Mutation surface:

- add a server-owned session operation to change execution target
- keep it serialized with other session mutations
- reject or defer target changes while an incompatible active run state exists

Acceptance slice:

- two attached clients see the same execution target
- changing execution target does not change session identity

## Workstream 4: Startup Path Resolution And Registration Flow

Goal:

Define the workspace-first CLI startup flow over the new project-aware server model.

First concrete surfaces:

- cwd-resolution query returning one of:
  - known project/workspace/worktree match
  - known project but unknown worktree
  - unregistered workspace/path
  - unsupported path
- project registration mutation
- attach-current-workspace-to-existing-project mutation

CLI Phase 4 behavior:

- if cwd resolves cleanly, continue workspace-first startup
- if cwd is unregistered, show project picker
- picker options:
  - create new project and attach current workspace as first workspace/main worktree when appropriate
  - choose existing project, then ask whether to attach current workspace to it or exit
  - exit/cancel

Out of scope for Phase 4 CLI:

- broad project navigation mode
- full multi-workspace management UI

## Workstream 5: SQLite Metadata And Session Cutover

Goal:

Move structured metadata authority into SQLite without moving large transcript artifacts into the database.

First concrete surfaces:

- explicit SQLite schema and migrations
- `sqlc`-generated typed access layer
- session metadata rows authoritative for:
  - identity
  - lineage
  - execution target
  - draft/name/timestamps
  - locked/continuation/usage JSON snapshots
- `session.json` removed from post-migration session directories

Important guardrail:

- `events.jsonl` remains the authority for committed transcript payloads during this phase

## Workstream 6: Runtime Lease Redesign

Goal:

Close the current Phase 3 hole where runtime activation/release semantics are coupled to request ids.

First concrete surfaces:

- `session.runtime.activate`
  - returns a server-issued `lease_id`
- `session.runtime.release`
  - releases by `lease_id`, not by activate request id
- explicit lease lifecycle state in the runtime service

Required semantics:

- `client_request_id` remains request idempotency only
- activate duplicate retry returns the same successful outcome for the same request id scope without minting duplicate leases
- release duplicate retry is safe
- disconnect cleanup is explicit and testable
- reconnect does not reclaim old lease ids; client rehydrates, reattaches, and acquires a fresh lease if needed
- active runs continue independently of frontend lease churn

Acceptance slice:

- duplicate activate/release coverage
- disconnect before release coverage
- fresh reconnect lease after disconnect coverage

## Workstream 7: Staged One-Time Migration

Goal:

Perform the blocking startup migration from the legacy workspace-container layout into the hybrid model.

Required steps:

- scan and validate the legacy tree before cutover
- build the target SQLite database in a staging area
- map each legacy workspace root/container to one migrated project and one primary workspace
- move session artifact directories into the new canonical `projects/<project-id>/sessions/<session-id>/` layout during cutover
- preserve session ids
- preserve the old tree as a timestamped backup after success

Adoption guardrails:

- normal startup is blocked until migration succeeds
- if staging fails, the old live tree remains untouched
- workspace relocation is handled later via explicit rebind, not inferred auto-migration

## Suggested Build Order

1. Server identity and app-global discovery
2. Registry/data model plus cwd-resolution query
3. SQLite metadata schema and typed access layer
4. Startup registration flow over the new query/mutation surface
5. Staged one-time migration and session metadata cutover
6. Session execution-target metadata and hydration adoption
7. Runtime lease redesign
8. Multi-client race/reconnect/backpressure hardening over the new model

## Exit Criteria

- one local server process can host sessions from multiple workspaces/projects
- CLI startup remains workspace-first but uses explicit project registration when cwd is unknown
- session status and hydration expose current workspace/worktree context
- SQLite is authoritative for structured metadata
- migrated sessions no longer contain `session.json`
- runtime lease semantics no longer overload request ids
- reconnect works via hydrate/attach/fresh-lease acquisition
- existing persisted sessions migrate successfully at startup and remain usable afterward
