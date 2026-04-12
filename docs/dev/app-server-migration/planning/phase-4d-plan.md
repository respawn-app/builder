# Phase 4D Plan: App-Global Discovery And Multi-Project Daemon Cutover

Status: planned next slice

## Purpose

Finish the daemon-topology cutover that Phase 4 always intended.

Phase 4A-4C already landed the metadata authority, session layout cutover, execution-target metadata, and explicit runtime leases. What remains is the server topology above that storage model: discovery, handshake identity, startup resolution, and project attachment are still workspace-scoped in key codepaths.

This document narrows that remaining work into an implementation-ready slice.

## Current Mismatch

The metadata layer is project-aware and app-global enough to support multiple projects, but several runtime/startup surfaces still act like "one daemon per workspace":

- `shared/protocol.ServerIdentity` still carries `project_id` and `workspace_root`
- `shared/discovery` still stores discovery records per workspace container
- `server/core.New(...)` binds startup to one workspace root and one `project_id`
- `server/transport/gateway.go` only accepts `project.attach` for one pre-bound project
- CLI remote attach/startup logic still discovers a daemon by current workspace, then rejects remotes whose `project_id` does not match the local binding

Those assumptions must be removed to call Phase 4 complete.

## Goal

One compatible local daemon is discovered app-globally first. Only after attach does the client resolve cwd/project/workspace context through server-owned queries and mutations.

## Non-Goals

- no new transcript or reconnect architecture work
- no broad project-navigation UI in TUI
- no new persistence redesign
- no attempt to make one daemon span multiple persistence roots

## Locked Direction

### 1. Discovery scope

Discovery becomes one record per persistence root, not one record per workspace container.

Recommended path:

- `filepath.Join(persistenceRoot, protocol.DiscoveryFilename)`

Rationale:

- one daemon per local Builder data root is the intended topology
- it matches the already-app-global metadata DB under the same root
- it avoids cwd/workspace heuristics before daemon attach

### 2. Handshake identity

`protocol.ServerIdentity` should describe only the server process and capabilities.

Keep:

- `protocol_version`
- `server_id`
- `pid`
- `capabilities`

Remove from identity:

- `project_id`
- `workspace_root`

Project/workspace context is not server identity. It is queryable state.

### 3. Core composition

`server/core` becomes app-global over the metadata authority.

That means:

- startup should no longer call `EnsureWorkspaceBinding(...)` just to construct the server
- server-owned services should be able to list and attach any hosted project from metadata
- per-project planning/session directories are resolved from metadata/project context at operation time, not baked into core identity

### 4. Startup resolution surface

The daemon needs a typed cwd-resolution query for startup.

Recommended minimal query:

- `project.resolvePath`

Recommended result states:

- `known_workspace`
- `known_project_unknown_worktree`
- `unregistered_workspace`
- `unsupported_path`

Minimum response payload should include enough data for workspace-first startup UX:

- canonical cwd/workspace root
- matched `project_id` / `workspace_id` / `worktree_id` when known
- project summary metadata when relevant

### 5. Registration mutations

Minimal server-owned mutations needed for Phase 4D startup flow:

- create project with current workspace as first workspace
- attach current workspace to existing project

The CLI remains workspace-first, but the registration decision must cross the server boundary instead of mutating local metadata before daemon attach.

## Client Startup Flow After 4D

1. Resolve local `workspaceRoot` / cwd only enough to load config and persistence root.
2. Discover one compatible daemon via the app-global discovery record.
3. If none exists, start one.
4. Call `project.resolvePath` against the daemon.
5. If the path is known, continue normal workspace-first startup.
6. If the path is unregistered, show project picker / registration choices.
7. Perform explicit registration/attach mutation over RPC.
8. Continue into session planning/attach.

The important inversion is: daemon discovery happens before project/workspace identity resolution.

## Suggested Implementation Slices

### Slice 1: Discovery + handshake

- add app-global discovery-path helper based on persistence root
- switch `serve` to write the app-global record
- remove `project_id` / `workspace_root` from `ServerIdentity`
- update remote dial / handshake tests

### Slice 2: App-global core + gateway

- refactor `server/core` so it is not constructed around one bound project
- make `project.list` / `project.getOverview` truly app-global
- allow `project.attach` for any hosted project

### Slice 3: Startup resolution + registration

- add or finish `project.resolvePath`
- add server-owned registration/attach mutations
- move CLI startup from workspace-matched discovery heuristics to daemon-first resolution

### Slice 4: Proof

- one daemon discovered from two different workspace roots
- same daemon lists/attaches multiple projects
- unknown cwd registration flow works over remote boundary
- serve/discovery shutdown cleanup still works correctly

## First Implementation Checklist

Start the coding pass from these proof surfaces:

1. `server/serve`
   - rewrite discovery-path and discovery-record tests for app-global discovery under one persistence root
2. `server/transport/gateway`
   - rewrite handshake / `project.attach` tests so one server no longer advertises one bound `project_id` and can attach multiple hosted projects
3. `cli/app/run_prompt_target`
   - rewrite remote discovery tests so daemon-first discovery no longer filters by local workspace-bound `project_id` matching
4. `cli/app/session_server_target`
   - rewrite interactive startup/attach tests so one discovered daemon can be used from multiple workspace roots and unknown-cwd registration flows over the server boundary

These suites should go red first and then become the proof surface for the 4D cutover.

## Key Files Expected To Change

- `shared/protocol/protocol.go`
- `shared/protocol/handshake.go`
- `shared/discovery/discovery.go`
- `server/serve/serve.go`
- `server/core/core.go`
- `server/transport/gateway.go`
- `cli/app/run_prompt_target.go`
- `cli/app/session_server_target.go`
- related serve/startup/session-target/remote tests

## Acceptance Criteria

- one app-global daemon can be discovered and attached from multiple workspace roots under the same persistence root
- handshake identity no longer implies one hosted project or workspace
- `project.attach` is valid for any hosted project, not just a pre-bound one
- CLI startup discovers the daemon first and resolves cwd/project/workspace context over RPC afterward
- unknown cwd registration flow no longer depends on workspace-scoped discovery files or local project-id matching heuristics

## Rollout Notes

- no long-lived dual-topology support is required
- an old workspace-scoped daemon simply becomes undiscoverable after the cutover; the CLI may start a new app-global daemon instead
- this is acceptable because Builder is single-user local software and the discovery topology is not user data
