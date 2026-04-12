# Phase 4D Plan: App-Global Direct Attach And Multi-Project Daemon Cutover

Status: planned next slice

## Purpose

Finish the daemon-topology cutover that Phase 4 always intended.

Phase 4A-4C already landed the metadata authority, session layout cutover, execution-target metadata, and explicit runtime leases. What remains is the server topology above that storage model: handshake identity, startup resolution, and project attachment are still effectively workspace-scoped in key codepaths, and the old discovery-file direction must be removed.

This document narrows that remaining work into an implementation-ready slice.

## Current Mismatch

The metadata layer is project-aware and app-global enough to support multiple projects, but several runtime/startup surfaces still act like "one daemon per workspace":

- `shared/protocol.ServerIdentity` still carries `project_id` and `workspace_root`
- current startup/serve paths still rely on persisted discovery artifacts and workspace-bound matching heuristics
- `server/core.New(...)` binds startup to one workspace root and one `project_id`
- `server/transport/gateway.go` only accepts `project.attach` for one pre-bound project
- CLI remote attach/startup logic still discovers a daemon by current workspace, then rejects remotes whose `project_id` does not match the local binding

Those assumptions must be removed to call Phase 4 complete.

## Goal

Client and daemon connect through the explicitly configured `server_host` + `server_port` first. Only after attach does the client resolve cwd/project/workspace context through server-owned queries and mutations.

## Non-Goals

- no new transcript or reconnect architecture work
- no broad project-navigation UI in TUI
- no new persistence redesign
- no attempt to make one daemon span multiple persistence roots

## Locked Direction

### 1. Direct attach scope

Attach/bootstrap uses the configured daemon address directly.

Locked direction:

- client and daemon converge on the same configured `server_host` + `server_port`
- client dials that exact address directly
- handshake verifies compatibility only
- no persisted discovery artifact
- no port scanning
- no fallback ports

Rationale:

- one daemon per local Builder data root is still the intended topology
- config already gives both sides a shared bootstrap address
- it avoids persisted discovery drift and stale-record cleanup logic entirely

### 1a. Listen address configuration

Daemon listen configuration is explicit and fully user-configurable.

Locked direction:

- separate `server_host` and `server_port` config settings
- fixed built-in default port in the private/dynamic range
- bind exactly the configured address
- if the configured port is occupied, startup fails explicitly
- no silent rebinding, no fallback port, no `:0` ephemeral bind

Implication for implementation:

- the current `net.Listen("tcp", "127.0.0.1:0")` behavior in `server/serve/serve.go` is incompatible with 4D and must be removed during this slice
- the existing persisted-discovery path is also incompatible with 4D and must be removed rather than migrated

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
2. Dial the configured daemon address via `server_host` + `server_port`.
3. If none exists, start one.
4. Call `project.resolvePath` against the daemon.
5. If the path is known, continue normal workspace-first startup.
6. If the path is unregistered, show project picker / registration choices.
7. Perform explicit registration/attach mutation over RPC.
8. Continue into session planning/attach.

The important inversion is: daemon attach happens before project/workspace identity resolution.

## Suggested Implementation Slices

### Slice 1: Direct Attach + Handshake

- remove persisted discovery from bootstrap/attach
- introduce explicit listen-address config (`server_host` / `server_port`) and fail-fast occupied-port behavior
- remove `project_id` / `workspace_root` from `ServerIdentity`
- update remote dial / handshake tests

### Slice 2: App-global core + gateway

- refactor `server/core` so it is not constructed around one bound project
- make `project.list` / `project.getOverview` truly app-global
- allow `project.attach` for any hosted project

### Slice 3: Startup resolution + registration

- add or finish `project.resolvePath`
- add server-owned registration/attach mutations
- move CLI startup from workspace-matched discovery heuristics to daemon-first direct dial

### Slice 4: Proof

- one daemon reachable from two different workspace roots under the same persistence root
- same daemon lists/attaches multiple projects
- unknown cwd registration flow works over remote boundary
- attach/start/startup failure behavior stays explicit when the configured port is occupied or unavailable

## First Implementation Checklist

Start the coding pass from these proof surfaces:

1. `shared/config`
   - add explicit `server_host` / `server_port` config surface in `shared/config/config_registry.go`, `shared/config/config_defaults.go`, and `shared/config/config_test.go`
   - add validation/precedence coverage for fixed-port startup and occupied-port behavior assumptions
2. `server/serve`
   - rewrite startup/listen tests around direct configured-address bind
   - add fixed-port / occupied-port failure coverage; no `:0` fallback allowed
3. `server/transport/gateway`
   - rewrite handshake / `project.attach` tests so one server no longer advertises one bound `project_id` and can attach multiple hosted projects
4. `cli/app/run_prompt_target`
   - rewrite attach/start tests so daemon-first direct dial no longer filters by local workspace-bound `project_id` matching or persisted discovery state
5. `cli/app/session_server_target`
   - rewrite interactive startup/attach tests so one configured daemon can be used from multiple workspace roots and unknown-cwd registration flows over the server boundary

These suites should go red first and then become the proof surface for the 4D cutover.

## Key Files Expected To Change

- `shared/protocol/protocol.go`
- `shared/protocol/handshake.go`
- `server/serve/serve.go`
- `server/core/core.go`
- `server/transport/gateway.go`
- `cli/app/run_prompt_target.go`
- `cli/app/session_server_target.go`
- related serve/startup/session-target/remote tests

## Acceptance Criteria

- one app-global daemon can be directly attached from multiple workspace roots under the same persistence root
- handshake identity no longer implies one hosted project or workspace
- `project.attach` is valid for any hosted project, not just a pre-bound one
- CLI startup dials the configured daemon first and resolves cwd/project/workspace context over RPC afterward
- unknown cwd registration flow no longer depends on persisted discovery files or local project-id matching heuristics
- topology cutover is hard: no discovery-file migration script, bridge mode, or dual-path compatibility layer is maintained

## Rollout Notes

- no long-lived dual-topology support is required
- no migration script is maintained for the old discovery-file topology; the codebase cuts over directly to configured-address attach
- this is acceptable because Builder is single-user local software and daemon bootstrap topology is not user data
