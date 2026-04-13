# App Server Migration: Locked Decisions

Locked from product work on 2026-03-27 and updated after external architecture review.

## Topology

- `builder` is moving toward a single-process application server with attachable frontends.
- The first frontend is CLI.
- The long-term packaging target is one codebase that can run both as an embedded local server and as a standalone long-lived server.
- The CLI should detect a running compatible server and attach to it; otherwise it should offer UX to run a local server.
- One server process is expected. Replication and multi-server coordination are out of scope unless projects are isolated.

## Protocol

- The primary frontend/server contract is JSON-RPC 2.0 over WebSocket.
- Normal application methods must not use the reserved `rpc.*` namespace.
- Method taxonomy should be resource-oriented, e.g. `project.*`, `session.*`, `run.*`, `process.*`, `approval.*`, `ask.*`, `prompt.*`, `subscription.*`, `system.*`.
- Query and read APIs should use dedicated typed methods per resource or view rather than a generic query endpoint.
- Versioning uses a single protocol version for the whole frontend/server contract.
- Handshake must happen before normal operations and must return protocol version plus capability flags.
- Minimal HTTP endpoints are allowed for health, auth, and bootstrap concerns.
- A dedicated health/readiness endpoint should exist outside the JSON-RPC/WebSocket surface.
- Frontend submissions should use structured request objects from day one rather than plain-text-only request shapes.
- Structured frontend submissions should use a generic user-intent envelope rather than separate RPC shapes for every submission style.
- Incompatible protocol versions should fail explicitly with a compatibility error; there is no silent downgrade or best-effort fallback.
- Every mutating request includes a client-generated `client_request_id` and must be idempotent within an explicit scope.

## Ownership And Boundaries

- The server owns project registration, session persistence, run state, agent execution, tool execution, background processes, provider credentials, and approval policy enforcement.
- Frontends are pure clients. They do not execute agent tools locally.
- Frontends own presentation, rendering, navigation, and slash-command catalogs.
- Frontends may query server state on demand in addition to subscribing to live activity.
- The server must never interpret raw slash-command syntax.
- Frontend packages must not import server-owned runtime, persistence, tool, process, or auth internals directly.

## Project, Session, And Run Model

- `project` is the primary top-level server resource.
- A project is a durable server-owned work container and may span multiple workspaces; it is not defined by one path.
- `workspace` is a child resource of `project`.
- Each workspace is a durable server-local registration that maps 1:1 to exactly one canonical execution root.
- Workspace resolution is exact-match only on canonical workspace roots. Nested paths remain unregistered unless the user explicitly attaches them as their own workspaces.
- `worktree` is optional child metadata of a git-backed workspace, not of project directly.
- Worktrees must not become the thing that defines project identity.
- Protocol identity remains opaque server ids such as `project_id`, `workspace_id`, and `worktree_id`; filesystem paths and git metadata are never protocol identity.
- Equivalent paths or symlink/path-spelling variants should canonicalize and deduplicate to the same workspace/worktree record rather than creating duplicates.
- Canonical project metadata includes at least: stable `project_id`, display name, availability state, workspace summary metadata, and session summary metadata.
- Canonical workspace metadata includes at least: stable `workspace_id`, canonical root path, availability state, optional git metadata when available, and worktree summary metadata.
- Projects have server-stored display names decoupled from filesystem folder names.
- A single server process may host multiple projects.
- Project discovery and registration are first-class server capabilities.
- Registering a new project is an explicit step; opening or attaching to an unseen path must not implicitly create a project.
- Registering the first workspace in a project requires the root path to exist and be accessible at registration time.
- Workspace root is immutable after registration unless the user explicitly rebinds the workspace after relocation.
- Git remains the source of truth for existing worktrees. Builder may store additive metadata and links, but it does not own a mirrored authoritative worktree registry in v1.
- Sessions are partitioned by project.
- A session belongs to a project and carries a current execution target like `(workspace_id, worktree_id?, cwd_relpath)` as shared server-owned state.
- Session lists and pickers are project-wide; current workspace/worktree context is surfaced as session metadata rather than treated as session identity.
- The initial user-facing CLI UX may stay workspace-first even though the server model is project-aware.
- A session is the durable conversational and work container.
- A session may contain multiple runs over time.
- A run is a single execution attempt or span within a session.
- v1 supports at most one active primary run per session.
- Internal delegated workers should not automatically become child sessions; user-visible branch and review workflows may create child sessions, while internal delegated work should stay under session or run-scoped runtime structures.
- Multiple frontends may control the same session concurrently.
- The server serializes mutating commands through authoritative per-session ordering.
- The server persists durable parent/child session lineage links and related metadata.
- Session discovery and listing are first-class server query surfaces with enough metadata for startup and picker UIs.

## Scope And Compatibility

- Preserving existing product functionality is mandatory.
- Exact CLI interaction parity is not mandatory; UX flows, protocol shape, and presentation may change where the new architecture benefits from it.
- Existing persisted sessions must remain loadable through the one-time staged migration.
- v1 remains single-user, but the protocol and storage boundaries must not block future authn/authz or multi-user expansion.
- Project removal and archive feature work are out of scope for this migration.

## Persistence

- Builder adopts hybrid persistence rather than remaining filesystem-only.
- SQLite is authoritative for structured metadata and server-owned resources.
- Large append-only session artifacts such as `events.jsonl` and `steps.log` remain file-backed for now.
- `session.json` is removed after successful migration; session metadata authority moves to SQLite.
- Interactive session creation remains lazily durable.
- Session durability and launch visibility are distinct thresholds. A freshly prepared blank interactive session may be persisted early for runtime leases and metadata-backed execution-target resolution while still remaining hidden from session pickers, project session counts, and startup resume flows until it gains user-meaningful identity such as a name, draft, lineage, or first prompt/history.
- The one-time storage migration is blocking at startup, stages metadata before cutover, and preserves the old tree as a timestamped backup after success.
- Workspace relocation is not auto-rebound; rebinding is explicit user action.
- SQL is hand-written and explicit; typed DB access should be generated via `sqlc`, and SQL schema migration execution should run through Goose rather than an ORM-owned migrator.

## Auth And Execution

- Upstream LLM and provider credentials are server-owned.
- Frontends should authenticate to the builder server rather than directly to providers.
- The server is the sole policy enforcer for guarded actions and blocks on approval requests until a frontend answers.
- Any attached frontend with access to the session may answer asks or approvals; the server applies the first committed authoritative response.
- Pending ask and approval delivery is a first-class server-driven prompt activity stream; attach and reconnect still use explicit pending-resource reads for hydration.
- Restart recovery should preserve the current transcript-driven behavior: interrupted tool-call attempts remain durable in conversation state, reopen appends the interruption marker, and the next model turn re-evaluates what to do. This is distinct from persisting broker queue state as a first-class durable object.
- The server binds locally by default. Remote listeners require explicit opt-in.
- Server listen configuration is explicit via separate host and port settings. The daemon binds exactly the configured address, uses a fixed built-in default port in the private/dynamic range, and fails startup if that port is occupied; it must not silently rebind, fall back, or auto-pick an ephemeral port.
- The frontend must be able to show which server or execution host it is attached to.

## Workflow Ownership

- Durable or stateful workflows should be server-owned where they materially affect system state.
- Forking is server-owned.
- Compaction is primarily server-owned.
- For frontend transcript-sync semantics, compaction is same-session committed transcript progression, not a same-session transcript rewrite that justifies non-append recovery behavior.
- Process control, including background shell and subprocess execution, is purely server-owned.
- Asks and approvals should be first-class API resources or method families rather than only event shapes.
- Approvals are split: the server blocks and enforces policy; the frontend exposes controls and sends responses.
- Back-navigation or UI teleportation remains frontend-owned.
- `review` is a frontend-owned built-in workflow composed from generic server capabilities rather than a dedicated server review feature.
- Child-session parent linkage should be set atomically at session creation time.
- Low-level composable operations must exist. Convenience atomic workflows are allowed where they improve correctness, idempotency, and UX.
- Rollback or fork flows navigate or attach to a different session target; they are not same-session transcript mutation.

## Command Ownership

- `/status` is a frontend view composed from typed server data. It should not become its own top-level resource family.
- `/ps` is backed by first-class server process resources with list, inspect, control, and output access.
- `/new` and `/resume` are frontend affordances over first-class server session operations.
- `/compact` is a frontend alias over a first-class server compaction capability.
- `/name` is server-stored session metadata edited and reflected by the frontend.
- `/back` remains frontend-local navigation over server-owned parent and child relationships.
- `/supervisor` and `/autocompaction` are frontend aliases over server-native per-session runtime policy or configuration toggles.
- `/thinking` and `/fast` are frontend aliases over server-native session-wide live configuration toggles.
- `/init` is a frontend-owned built-in command, not a server-provisioned capability; the frontend may translate it into a structured submission.
- `/review` is a frontend-owned built-in workflow composed from generic server capabilities rather than a dedicated server review feature.
- `/logout` has mixed ownership depending on attachment and auth mode.
- `/exit` remains frontend-local.
- The server should not carry or provision built-in slash commands; frontends own slash-command catalogs and may map entries to server capabilities, local UI actions, or structured submissions.
- File-backed or custom prompt commands remain frontend-local; frontends own parsing and may evolve richer command metadata in the future.

## Event And Hydration Model

- Frontends should depend on normalized domain state and event models, not raw provider or runtime payloads.
- Typed queries and hydration views are the source of truth for initial render and reconnect.
- Reconnect is snapshot/page based. A stream-history or cursor recovery contract is not part of the required architecture.
- Large-session reconnect should prefer transcript pagination and, where useful, compression over any stream-history reconstruction design.
- If a live stream drops or a subscriber falls behind, the recovery path is rehydrate plus resubscribe.
- Any transport failure, stream gap, or subscription loss that risks transcript correctness must invalidate the affected live projection immediately; the client must recover from committed hydration after connectivity returns rather than continuing with stale or guessed transcript state.
- Transport-crossing read or mutation failures must not be swallowed, downgraded to empty/idle success states, or masked by stale frontend transcript data.
- The protocol must distinguish durable lower-volume state transitions from high-rate live feeds.
- Transcript truth comes from typed hydration reads such as `session.getMainView` and transcript-page reads.
- Ongoing-mode normal-buffer scrollback is committed-transcript only. The frontend may replay committed history at startup and append only new committed transcript suffixes afterward; provisional live activity must never become immutable scrollback authority.
- Live assistant deltas, reasoning deltas, busy state, tool-progress hints, and similar progressive UX concerns are transient projection state only. They may improve live UX, but they are disposable and must not be treated as transcript truth.
- During continuous attachment, ongoing normal-buffer history is append-only. Same-session logical divergence must not be normalized by clear-and-replay or full-buffer re-emission.
- If external continuity is broken (for example disconnect, stream gap, client restart, daemon restart, or subscription invalidation), recovery is authoritative rehydrate plus resubscribe rather than stale/live replay.
- For that external continuity-loss recovery class only, TUI ongoing may re-issue its ongoing buffer from authoritative committed state. This is acceptable recovery behavior for a new recovered surface instance.
- Client-side transcript divergence caused by reconciliation, deduplication, ordering, overlap, or pagination bugs is not an acceptable redraw case; those bugs must be fixed at the root cause. Global debug mode (`debug = true` or `BUILDER_DEBUG=1`) may hard-fail invariant violations during development.
- Prompt activity is a distinct live stream class, separate from both session activity and list-style pending prompt reads.
- Process output is a separate stream class from process state.
- Subscriptions should target explicit resource-scoped streams rather than a single multiplexed server-wide feed.
- Subscriptions are live-only; initial state should come from separate explicit queries or hydration views rather than being bundled into subscribe.
- The server must expose typed hydration views sufficient for startup, session main view, process inspection, and pending asks or approvals.
- Broad filesystem or runtime inspection APIs are not mandated up front; that surface should expand only when implementation proves a real need.
- Runtime leases are explicit server-side identities, but reconnect does not reclaim an old lease id. Clients rehydrate, reattach, and acquire a fresh lease; active runs remain server-owned and continue independently.
- Session browsing and launch surfaces must use server-owned launch-visibility rules rather than raw session-row existence. Runtime-only durable sessions must not leak into pickers or project/session listings.

## Startup And Composition

- CLI local server attach should use the explicitly configured `server_host` and `server_port` with compatibility handshake; persisted discovery artifacts are not part of the target architecture.
- Compatibility should be established through a dedicated initial handshake method before attach or query calls.
- Session attachment and event subscription should be separate explicit protocol steps.
- `attach` should acknowledge plus return minimal attached-resource metadata such as ids and kinds, but not snapshots.
- Project attachment establishes context only; project and session index snapshots remain explicit queries.
- Server handshake identity should describe the server process and capabilities, not imply that the server is scoped to one project or workspace.
- The topology cutover away from workspace-scoped daemon discovery is hard. No migration script, bridge mode, or long-lived compatibility layer is maintained for old discovery artifacts.
- When CLI startup cwd does not resolve to any registered project/workspace/worktree, the user should see a project picker with explicit registration choices. Creating a new project may attach the current workspace as the first workspace and remember the current root as the main worktree when appropriate.
- Selecting an existing project from that flow should ask whether to attach the current workspace to that project or exit; registration remains explicit.
- The unknown-cwd interactive binding picker should show one explicit create-new-project action first, then a visually separated existing-project section. That existing-project section should reuse session-picker row structure, but each project preview line is the project's main workspace path rendered relative to the user's home directory when possible and otherwise absolute.
- Headless startup in an unregistered workspace must fail fast rather than auto-creating hidden project/workspace state.
- To unblock agent workflows in that fail-fast model, Builder should provide explicit CLI helpers for workspace binding inspection/mutation: `builder project [path]` resolves the bound project for a path (default `cwd`), and `builder attach [path]` explicitly binds a workspace to the project already bound to `cwd`. Raw project ids remain an escape hatch via `builder attach --project <project-id> [path]`. Both commands default `path` to `cwd`; the path-first form fails if the current workspace is not already bound to a project.
- CLI UX may remain workspace-first outside that startup or registration flow. Projects need not become a broad first-class navigation surface in the TUI yet.
- If the CLI started an embedded local server, CLI exit should prompt every time for the intended server lifecycle rather than assuming a shutdown policy.
- That exit prompt should present neutral choices without a recommended default.
