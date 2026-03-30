# Application Server Migration Requirements

Status: draft requirements

Last updated: 2026-03-27

## Purpose

`builder` currently combines agent runtime, persistence, tool execution, and terminal presentation inside one CLI application. This migration splits those concerns so `builder` becomes an application server and frontends attach to it through a stable protocol.

The first frontend is CLI. Future frontends may include desktop, web, and remote clients.

This document defines product requirements and scope for that migration. It does not define package-level implementation sequencing or exact wire payload schemas.

## Product Outcome

The resulting system should let a single `builder` server process:

- run and persist agents,
- manage multiple sessions across multiple projects,
- expose typed read models for frontend hydration,
- stream authoritative real-time activity to attached frontends,
- accept control requests from multiple attached frontends,
- continue operating even when frontends disconnect.

The resulting frontends should:

- render their own UX independently,
- attach and detach without owning runtime state,
- consume a shared protocol instead of runtime internals,
- remain replaceable so a new frontend can be added without reworking server-side business logic.

## Goals

- Fully decouple presentation from agent execution and runtime state.
- Preserve all existing product functionality while allowing UX and protocol redesign where the new architecture benefits from it.
- Support both local embedded use and standalone server use.
- Support multiple simultaneous frontends attached to the same running server.
- Make typed queries and live activity streams part of the standard contract.
- Keep the server authoritative for state, execution, and policy.
- Keep the system single-user in v1 without baking in assumptions that would block future remote auth or multi-user growth.

## Non-Goals

- Exact CLI interaction or presentation parity with today's monolith.
- Multi-user tenancy in the first server architecture.
- Multi-server coordination, replication, or high availability.
- Client-local execution of agent tools.
- Rewriting storage formats or adopting event-sourcing as part of this migration.
- Prematurely defining broad filesystem or inspection APIs before there is a concrete frontend need.

## Architectural Invariants

- Frontend packages must not import server-owned runtime, persistence, tool, process, or provider-auth packages directly.
- All mutating protocol requests must carry a client-generated `client_request_id` and be idempotent within an explicit server-defined scope.
- `project_id`, `session_id`, `run_id`, `process_id`, `approval_id`, and `ask_id` are opaque server-assigned IDs. Filesystem paths are never protocol identity.
- v1 supports at most one active primary run per session.
- Typed queries and hydration views are the source of truth for initial render and reconnect.
- Event replay and catch-up are best-effort within explicit retention windows, not a guaranteed universal contract.
- The protocol must distinguish durable state changes from high-rate live feeds.
- Existing persisted sessions must remain loadable, either directly or through lazy server migration/adoption.
- The server binds locally by default. Remote listeners require explicit opt-in.
- The persistence layout remains a server implementation detail, not part of the frontend contract.

## Locked Product Decisions

The following are already locked for this feature and should be treated as requirements rather than open design topics:

- Primary protocol: JSON-RPC 2.0 over WebSocket.
- Method taxonomy: resource-oriented namespaces such as `project.*`, `session.*`, `run.*`, `process.*`, `approval.*`, `ask.*`, `subscription.*`, and `system.*`.
- Read/query style: dedicated typed methods per resource/view rather than a generic query endpoint.
- Versioning model: a single protocol version for the whole frontend/server contract, complemented by explicit capability flags in handshake.
- Supporting HTTP surface: minimal endpoints only, for concerns like health, auth, and bootstrap.
- Bootstrap/ops surface: a minimal dedicated health/readiness endpoint outside the JSON-RPC/WebSocket contract.
- Server process model: one server process, potentially long-lived, hosting multiple projects and multiple concurrent sessions.
- Packaging target: one codebase that can run embedded or standalone.
- CLI default behavior: attach to an already-running compatible server if available, otherwise offer local server startup.
- Ownership boundary: the server owns runtime, persistence, tools, provider credentials, background processes, and policy enforcement.
- Presentation boundary: frontends own all UX and rendering.
- Control model: multiple frontends may control one session; the server serializes mutating commands per session.
- Reconnect model: frontends should normally refetch fresh state or typed hydration views; stream catch-up is optional or best-effort rather than the standard reconnect path.
- Trust model: local/single-user in v1, but future remote authn/authz must remain architecturally possible.
- Frontend submissions: structured request objects from day one.
- Submission shape: a generic user-intent/request envelope rather than a separate RPC shape per submission style.
- Version negotiation: incompatible protocol versions fail explicitly rather than silently downgrading.

## Functional Requirements

## Server Responsibilities

The server must be the sole authority for:

- project registration, discovery, and availability state,
- session creation, loading, persistence, and lifecycle,
- run creation, run state, interruptibility, and outcomes,
- agent execution and runtime orchestration,
- tool execution including `shell`, `patch`, `view_image`, and future server-side tools,
- background process ownership and control,
- provider authentication and credentials,
- approval gating and guarded-action policy enforcement,
- hydration views, stream retention, and authoritative event ordering.

Server-side state must survive frontend disconnects. Frontend presence must not be required for a session or process to continue existing unless the user explicitly requests termination.

## Frontend Responsibilities

Frontends must be responsible for:

- rendering transcript and status UX,
- navigation, overlays, keyboard shortcuts, and presentation-specific flows,
- translating user intent into protocol requests,
- maintaining local UI projection state and caches only,
- answering asks and approvals surfaced by the server,
- owning built-in and file-backed slash-command catalogs.

Frontends must not depend on privileged in-process access that future frontends cannot rely on.

The server must never interpret raw slash-command syntax. The frontend must translate slash commands into frontend-local actions, one or more server requests, or a structured submission envelope.

## Protocol Requirements

The protocol must:

- use JSON-RPC 2.0 over WebSocket as the primary bidirectional contract,
- avoid using the reserved `rpc.*` namespace for normal application methods,
- use a dedicated handshake before normal operations,
- return both protocol version and capability flags during handshake,
- organize methods around resource-oriented namespaces,
- support request-response operations for commands and queries,
- expose reads through dedicated typed methods per resource/view rather than a generic query endpoint,
- use structured request payloads for frontend submissions from day one so the contract is not limited to plain-text prompt sends,
- make mutating requests idempotent through `client_request_id`,
- keep session attachment and event subscription as separate explicit protocol steps,
- let `attach` return acknowledgment plus minimal attached-resource metadata rather than full snapshots,
- keep project attachment lightweight so project/session index state is fetched through explicit queries rather than implicitly returned on attach,
- support explicit snapshot and hydration-view requests in addition to streaming events,
- support server-initiated asks and approval requests with client responses,
- carry only normalized domain events and live-feed payloads that frontends can depend on safely,
- avoid exposing frontend-specific rendering assumptions as protocol requirements,
- be versionable so future frontends can negotiate compatibility.

The protocol should expose only the query surface that real frontends need. It should not commit to a broad remote filesystem or introspection API before concrete implementation demands justify it.

## Hydration And Query Requirements

The server must expose typed read models sufficient for at least these day-one frontend needs:

- project and session picker flows,
- the active session main view,
- transcript pagination,
- process list and process inspect flows,
- pending ask and approval views,
- active run inspection,
- session and project overview surfaces.

At minimum, the design must admit typed views equivalent to:

- `project.list`,
- `project.getOverview`,
- `session.listByProject`,
- `session.getOverview`,
- `session.getTranscriptPage`,
- `session.getMainView`,
- `run.get`,
- `process.list`,
- `process.get`,
- `approval.listPendingBySession`,
- `ask.listPendingBySession`.

`session.getMainView` must remain presentation-neutral. It is a typed hydration bundle, not terminal layout data.

## Event And Live Activity Requirements

The event contract must not collapse all activity into one undifferentiated stream.

The server must define at least these classes of stream:

- durable lower-volume state transitions,
- live session activity for partial assistant output and progress,
- process output streams for stdout and stderr.

Requirements:

- streams are ordered within their own stream scope,
- replay or catch-up is best-effort within explicit retention windows,
- clients can detect a gap or expired cursor explicitly,
- slow subscribers receive an explicit gap or backpressure failure rather than silent truncation,
- durable transcript state remains distinct from partial live output,
- process output retention is defined independently from process state retention.

The protocol must make it obvious which feeds are durable, which are ephemeral, and how clients recover after falling behind.

## Project Model

The server must support multiple projects within one running process.

Requirements:

- `project` is the primary top-level server resource,
- each project is a durable server-local registration that permanently maps 1:1 to exactly one repository, one canonical workspace root, and one durable project or session container,
- repository identity is expected and part of the canonical project record, but protocol identity remains the opaque `project_id` rather than repo metadata,
- project persistence remains partitioned per project through that single durable project container, even though clients must not treat storage layout as protocol identity,
- reopening the same canonical root resolves to the same project rather than creating a duplicate,
- equivalent paths or symlink/path-spelling variants must canonicalize and deduplicate to the same project registration,
- canonical project metadata includes at least stable `project_id`, display name, canonical root path, availability state, repository metadata when available, and session summary metadata,
- project availability states must cover at least `available`, `missing`, and `inaccessible`,
- projects have server-stored display names decoupled from filesystem folder names,
- the server can discover or register projects at runtime through first-class capabilities,
- project registration requires the root path to exist and be accessible at registration time,
- registering a new project is an explicit step; opening or attaching to an unseen path must not implicitly create a project,
- project root is immutable after registration,
- sessions are associated with a project.

## Session, Run, And Process Model

The minimum session and run model is specified in `session-run-model.md` and is part of the planning baseline.

Requirements:

- a session is the durable conversational and work container,
- a session may accumulate multiple runs over time,
- a run is a single execution attempt or span inside a session,
- v1 permits at most one active primary run per session,
- runtime tuning operations such as `/thinking` and `/fast` are session-scoped live settings rather than per-run-only settings,
- internal delegated work must not explode session lineage by default,
- process resources are first-class and distinct from process output streams,
- queue and busy-state semantics must be explicit and testable.

## Concurrency, Idempotency, And Ordering

The server must allow multiple frontends to attach to and control the same session.

Requirements:

- mutating operations are serialized through authoritative per-session ordering,
- every mutating request is idempotent through `client_request_id`,
- duplicate retries must not create duplicate prompt submissions, duplicate approvals, or duplicate process-control actions,
- the server must define which operations are rejected while a primary run is already active,
- reads remain available regardless of active-run state,
- approval and ask responses must be deterministic under races,
- the server is responsible for defining final ordering and emitting resulting authoritative state transitions.

## Tool Execution Model

All agent tools execute on the server machine against the server's project state.

Requirements:

- frontends are pure clients and never become implicit execution targets,
- tool results, approvals, and process control are authoritative on the server,
- remote attachment must not change the execution target,
- any future execution-target abstraction would require a deliberate product decision and must not be assumed by this migration.

## Approval And Ask Flows

The server must own guarded-action enforcement.

Requirements:

- asks and approvals are first-class protocol resources,
- when an approval is required, the server pauses the guarded action and emits an approval request,
- any attached frontend with access to the session can answer with approve or deny,
- the first committed authoritative response wins,
- later responders receive a deterministic already-resolved result,
- the server applies the answer and continues or rejects the guarded action,
- approval semantics are consistent regardless of which frontend answers.

Current clarification for planning:

- The current monolith now has Phase 0 proof for both live-process queued ask or approval behavior and restart recovery of interrupted tool-call attempts through persisted conversation state.
- Current restart behavior is transcript-driven: the interrupted tool-call attempt remains durable, reopen appends the interruption marker, and the next model turn re-evaluates what to do.
- Planning and implementation should preserve that restart behavior rather than assuming a durable broker-queue object or inventing a different default contract silently.

## Workflow Ownership Requirements

The server should own workflows that create durable state or affect system behavior in a frontend-independent way.

This includes at least:

- project registration,
- session lifecycle,
- run lifecycle,
- forking,
- compaction,
- process control,
- ask lifecycle,
- approval lifecycle,
- agent execution lifecycle.

Frontend-owned workflows may remain frontend-specific where the behavior is primarily navigational or presentational.

Examples:

- back-navigation or UI teleportation can stay frontend-owned,
- `review` should be implementable as a frontend-owned built-in workflow composed from generic server capabilities rather than requiring a dedicated server-native state machine,
- frontend-owned prompt commands such as `/init` or file-backed prompt commands remain frontend-side command-catalog concerns.

When a frontend creates a child session for a workflow like `review`, parent linkage should be set atomically at session creation time.

The protocol should not assume that frontend-owned command expansions are plain text forever. It should leave room for future structured `client_meta` inside a submission envelope without requiring server-side command provisioning in v1.

Low-level composable operations must exist. Convenience atomic workflows are allowed and recommended where they improve correctness, idempotency, and UX.

## CLI Frontend Requirements

The CLI frontend must remain a first-class frontend, not a privileged special case.

Requirements:

- it discovers local servers through a well-known local control endpoint or socket plus compatibility handshake,
- it can attach to an existing compatible server,
- it can start or embed a local server when needed,
- if it started an embedded local server, exit flow prompts for the intended server lifecycle instead of assuming shutdown behavior,
- that exit flow presents neutral choices without a recommended default,
- it uses the same client boundary that future frontends will use,
- it preserves all existing product functionality at the product level,
- it is allowed to redesign UX where appropriate for the new architecture.

The CLI should remain able to cover the existing product surface, including session selection, session resume, prompts, asks, approvals, process visibility and control, and current core workflows.

## Auth And Trust Boundaries

The first server architecture may stay local-trust and single-user, but it must not entangle frontend and provider auth in a way that blocks remote-safe evolution.

Requirements:

- provider credentials are server-owned,
- frontends authenticate to the builder server rather than directly to upstream providers,
- the default listener is local-only and non-routable,
- remote bind or remote-safe auth is explicit and off by default,
- the handshake must expose enough server identity and capability information for the frontend to show which execution host it is attached to,
- the protocol boundary and storage model must admit future frontend authn/authz without breaking the architecture.

Remote-safe authn/authz is not required to be implemented in this migration.

## Capability Preservation

The migration must preserve all existing product functionality, even if exact UX changes.

`behavior-preservation.md` is part of the requirements baseline, not a nice-to-have appendix.

At minimum, the resulting system must still support:

- creating, resuming, and persisting sessions,
- running agents with the existing tool model,
- background processes and process inspection or control,
- asks and approvals,
- compaction,
- forking and child-session flows,
- headless execution where no frontend is currently attached,
- status and runtime inspection surfaces needed by the frontend,
- existing persisted session data adoption.

Compatibility may be delivered through new protocol operations, compatibility adapters, or frontend remapping, but not through renewed coupling between UI and runtime internals.

## Migration Constraints

- The migration must move toward a hard frontend/server boundary rather than a cosmetic extraction.
- Shared code may exist, but frontends must not reach into server-owned mutable runtime state directly.
- The server contract must be sufficient for at least one future non-CLI frontend to be feasible without architectural rework.
- The resulting architecture must not assume that the frontend and server run on the same machine.
- The resulting architecture must still support the embedded same-machine case cleanly.
- Existing persisted sessions must remain resumable without destructive one-shot migration.
- Project removal or archive semantics are explicitly out of scope for this migration.

## Acceptance Criteria

The migration is only acceptable when all of the following are true:

- A single running server can host multiple sessions across multiple projects.
- A CLI frontend can attach to that server and perform the current product workflows end to end.
- A second frontend can attach to the same session, receive authoritative state and live activity, and issue control requests.
- Reconnect works through authoritative hydration views and, where available, best-effort catch-up without silent state loss.
- A CLI crash or disconnect does not stop an active run unless explicitly requested.
- Duplicate retries do not create duplicate submissions, approvals, or process actions.
- A slow subscriber receives an explicit gap or backpressure failure.
- Two clients can race an approval or ask response and get deterministic outcomes.
- Tool execution remains server-side regardless of which frontend is attached.
- Approval and ask flows are server-authoritative and frontend-agnostic.
- The CLI can run against either an embedded local server or an already-running server.
- Embedded mode and external-daemon mode both use the same client boundary.
- Existing persisted sessions remain loadable or are lazily migrated by the server.
- The system exposes a frontend-safe protocol boundary rather than relying on CLI-only runtime access.

## Explicit Deferrals

The following are deferred beyond this requirements document:

- exact JSON payload shapes,
- exact JSON-RPC method names,
- package-by-package implementation sequencing,
- exact local discovery mechanism details,
- future multi-user design,
- final remote authn/authz design.
