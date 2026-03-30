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
- Method taxonomy should be resource-oriented, e.g. `project.*`, `session.*`, `run.*`, `process.*`, `approval.*`, `ask.*`, `subscription.*`, `system.*`.
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
- Each project is a durable server-local registration that permanently maps 1:1 to exactly one repository, one canonical workspace root, and one durable project or session container.
- Repository identity is part of the canonical project record, but protocol identity remains the opaque `project_id` rather than repo metadata.
- Project persistence remains partitioned per project through that single durable project container, even though clients must not treat storage layout as protocol identity.
- Reopening the same canonical root attaches to the same canonical project rather than creating a duplicate.
- Equivalent paths or symlink/path-spelling variants should canonicalize and deduplicate to the same project.
- Canonical project metadata includes at least: stable `project_id`, display name, canonical root path, availability state, repository metadata when available, and session summary metadata.
- Projects have server-stored display names decoupled from filesystem folder names.
- A single server process may host multiple projects.
- Project discovery and registration are first-class server capabilities.
- Project registration requires the root path to exist and be accessible at registration time.
- Registering a new project is an explicit step; opening or attaching to an unseen path must not implicitly create a project.
- Project root is immutable after registration.
- Sessions are partitioned by project.
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
- Existing persisted sessions must remain loadable, either directly or through lazy migration or adoption.
- v1 remains single-user, but the protocol and storage boundaries must not block future authn/authz or multi-user expansion.
- Project removal and archive feature work are out of scope for this migration.

## Auth And Execution

- Upstream LLM and provider credentials are server-owned.
- Frontends should authenticate to the builder server rather than directly to providers.
- The server is the sole policy enforcer for guarded actions and blocks on approval requests until a frontend answers.
- Any attached frontend with access to the session may answer asks or approvals; the server applies the first committed authoritative response.
- The server binds locally by default. Remote listeners require explicit opt-in.
- The frontend must be able to show which server or execution host it is attached to.

## Workflow Ownership

- Durable or stateful workflows should be server-owned where they materially affect system state.
- Forking is server-owned.
- Compaction is primarily server-owned.
- Process control, including background shell and subprocess execution, is purely server-owned.
- Asks and approvals should be first-class API resources or method families rather than only event shapes.
- Approvals are split: the server blocks and enforces policy; the frontend exposes controls and sends responses.
- Back-navigation or UI teleportation remains frontend-owned.
- `review` is a frontend-owned built-in workflow composed from generic server capabilities rather than a dedicated server review feature.
- Child-session parent linkage should be set atomically at session creation time.
- Low-level composable operations must exist. Convenience atomic workflows are allowed where they improve correctness, idempotency, and UX.

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
- Ordered stream catch-up is best-effort within retention windows; it is not a universal replay contract.
- If a cursor is no longer replayable, the server should return an explicit cursor-expired or gap error and require rehydration plus resubscription.
- The protocol must distinguish durable lower-volume state transitions from high-rate live feeds.
- Process output is a separate stream class from process state.
- Subscriptions should target explicit resource-scoped streams rather than a single multiplexed server-wide feed.
- Subscriptions are live-only; initial state should come from separate explicit queries or hydration views rather than being bundled into subscribe.
- The server must expose typed hydration views sufficient for startup, session main view, process inspection, and pending asks or approvals.
- Broad filesystem or runtime inspection APIs are not mandated up front; that surface should expand only when implementation proves a real need.

## Startup And Composition

- CLI local server discovery should use a well-known local control endpoint or socket with compatibility handshake.
- Compatibility should be established through a dedicated initial handshake method before attach or query calls.
- Session attachment and event subscription should be separate explicit protocol steps.
- `attach` should acknowledge plus return minimal attached-resource metadata such as ids and kinds, but not snapshots.
- Project attachment establishes context only; project and session index snapshots remain explicit queries.
- If the CLI started an embedded local server, CLI exit should prompt every time for the intended server lifecycle rather than assuming a shutdown policy.
- That exit prompt should present neutral choices without a recommended default.
