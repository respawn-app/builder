# App Server Migration Plan

This file tracks only work that is still ahead.

Completed phases were moved to `docs/dev/app-server-migration/planning/plan-completed.md` so this file stays usable during implementation.

Phase numbers are historical labels. They are kept for continuity, not because work must execute strictly in numeric order.

## Current Focus

Current shipping path:

1. Hard-cut rollback: remove SQLite-backed request dedup persistence before it ships further
2. Remote-server blockers: server-owned auth bootstrap and path-independent remote attach
3. Phase 8 shared frontend transcript architecture refactor

Not on the shipping critical path:

- Phase 9 multi-client session control follow-up in `planning/phase-9-multi-client-session-control.md`

## Open Work

### Hard-Cut Rollback: Remove SQLite-Backed Request Dedup Persistence

Goal: remove the partially-landed persisted request deduplication model cleanly before it becomes part of the shipped storage contract.

This is a rollback slice, not a forward expansion of the migration.

Locked direction for this rollback:

- SQLite-backed request deduplication must be removed from code and from the active migration story for now.
- This is a hard cutover, not a compatibility bridge.
- We do not keep half-enabled persistence code, dormant tables, or long-lived fallback branches around this feature.
- `client_request_id` request fields may stay on the API surface where they are still part of the intended contract, but SQLite persistence must stop being the authority for deduplication.
- Local cleanup is acceptable because only Nek's machine is affected right now.

Concrete tasks:

- [ ] remove the persisted dedup implementation from runtime codepaths and service wiring
  Required code scope:
  - remove `server/idempotency/*` coordinator usage from the active write surface
  - remove metadata-store methods, SQLC queries, and service plumbing that exist only for persisted dedup state
  - remove dependency injection in `server/core/*` that constructs or passes the persisted dedup coordinator
  - eliminate any now-dead error mapping, response replay helpers, or persistence observers introduced only for SQLite-backed dedup
  First code entrypoints to unwind:
  - `server/idempotency/*`
  - `server/core/core.go`
  - `server/sessionlaunch/*`
  - `server/runprompt/*`
  - `server/promptcontrol/*`
  - `server/processview/*`
  - `server/sessionlifecycle/*`
  - `server/runtimecontrol/*`
- [ ] hard-cut the storage layer back out
  Required storage scope:
  - remove the `mutation_dedupe` table from the active schema/migration story
  - remove the corresponding Goose migration files from the repo rather than carrying a dead migration forward
  - remove SQLC models/query outputs that existed only for this table
  - update docs/specs/decisions in the same slice so persisted dedup is explicitly removed/deferred from the current contract before or alongside the code change
- [ ] define the post-rollback runtime behavior explicitly
  Required behavior scope:
  - restore or retain only the non-persisted duplicate-suppression behavior we intentionally want right now
  - make the remaining duplicate-protection scope explicit in code and docs so it does not pretend to survive process restart
  - ensure `sessionruntime.activate` / `release` remain on their existing lease-specific semantics and are not pulled into this rollback in a confusing way
- [ ] rewrite the tests around the rollback instead of preserving persisted-dedup expectations
  Required test scope:
  - delete or rewrite the persisted-dedup expectation tests first, before changing implementation, so the rollback runs in explicit red/green order
  - first test buckets to rewrite:
    - `server/sessionlaunch/service_test.go`
    - `server/runprompt/headless_test.go`
    - `server/promptcontrol/service_test.go`
    - `server/processview/service_test.go`
    - `server/sessionlifecycle/service_test.go`
    - `server/transport/gateway_test.go`
  - keep or add tests that prove the intended current non-persisted behavior still works where we rely on it
  - rerun the touched server/client/transport packages plus canonical build after the rollback
- [ ] add the required operator follow-through steps after code lands
  Required rollout scope:
  - explicitly ask Nek to restart the running `builder` CLI / embedded app-server after moving to the rollbacked executable so no old in-memory dedup state lingers
  - after that restart, ask Nek to clean up the local metadata DB manually at `<persistence-root>/db/main.sqlite3` on his machine by removing the abandoned `mutation_dedupe` table/state
  - document that this manual DB cleanup is a one-off local step for Nek, not a productized migration flow

Exit criteria:

- no production code depends on SQLite-backed request deduplication
- no active migration/spec/decision doc claims persisted dedup is current behavior
- tests no longer prove persisted replay behavior as part of the current shipping contract
- Nek is prompted to restart after upgrading to the new executable
- Nek is given the one-off manual local DB cleanup step after restart

### Phase 2 Residual: Resource Surfaces And Event Hub

Completed. Phase 2 residual implementation, proof, and boundary-audit closeout are archived in `docs/dev/app-server-migration/planning/plan-completed.md`.

Phase 2 follow-up that still remains open is tracked separately in `docs/dev/app-server-migration/planning/phase-9-multi-client-session-control.md`.

### Remote-Server Blockers: Server-Owned Auth Bootstrap And Path-Independent Attach

Goal: make a Dockerized or otherwise remote `builder serve` instance usable without shared host persistence, while keeping auth state server-owned.

This slice resolves the blockers documented in `docs/dev/app-server-migration/blockers.md`.

Locked requirements for this slice:

- server keeps auth; client does not become a hidden auth owner just because it is the machine with the browser or env vars
- remote `builder serve` must be able to boot far enough to expose transport and auth/bootstrap RPC before auth is configured
- remote attach must not depend on host-side metadata bindings or host/server absolute-path equality
- local same-machine fast paths may keep using workspace-root hints, but true remote flows must work without shared persistence and without matching host/container paths
- avoid wide TUI flow rewrites; prefer narrow protocol/server changes plus targeted remote-client attach/auth branching

Concrete tasks:

- [ ] split server startup into transport readiness vs auth readiness
  First code entrypoints to change:
  - `server/startup/startup.go`
  - `server/startup/headless.go`
  - `server/bootstrap/embedded.go`
  - `server/serve/*`
  Required direction:
  - allow `builder serve` / standalone app-server startup to reach `/rpc` and health/readiness surfaces before auth is configured
  - keep the server clearly marked as auth-not-ready rather than pretending it is fully usable
  - reject only auth-dependent operations with explicit server-auth-required errors instead of failing startup before transport exists
  - project/session metadata reads and other non-auth-dependent attach/bootstrap queries should remain legal before auth where possible
  - preserve the existing embedded/local UX where auth can still be satisfied during startup when that path is appropriate
  Discoverability requirement:
  - after `protocol.handshake`, the client must be able to discover whether remote auth bootstrap is supported, whether server auth is ready, and which pre-auth methods are legal
  - this must come either from handshake capability/status fields or from an explicit unscoped bootstrap/status RPC that is guaranteed legal before attach/auth
  Concrete contract to implement:
  - extend handshake or add guaranteed-pre-auth `auth.getBootstrapStatus`
  - the discoverability response must include at least:
    - `auth_ready`
    - `auth_bootstrap_supported`
    - `allowed_pre_auth_methods`
    - supported bootstrap modes such as `browser_callback_code`, `browser_callback_url`, `device_code`, and `api_key`

- [ ] add a server-owned remote-auth bootstrap flow
  First code entrypoints to change:
  - `cli/app/remote_server.go`
  - `cli/app/auth_gate.go`
  - `shared/protocol/handshake.go`
  - `shared/client/remote.go`
  - `server/transport/gateway.go`
  - `server/authflow/*` and/or a new server-owned auth bootstrap service surface
  Required direction:
  - move remote auth from host-local file/env assumptions to server-owned auth RPC
  - browser/device/paste UX remains client-driven, but the server remains auth owner
  - browser flow must be made explicit for remote use:
    - client opens browser or runs device/paste UX on the client machine
    - client collects callback URL, authorization code, or equivalent auth material on the client side
    - client sends that code/material to the server over the new auth/bootstrap RPC
    - server performs code exchange / token resolution as needed and writes the resulting auth method into the server auth store
  - do not assume the current localhost callback-listener flow can be reused unchanged across machines
  - `remoteAppServer` / remote auth helpers must stop treating host-local `~/.builder/auth.json` as the remote server's source of truth
  - define the minimal RPC surface needed for remote auth bootstrap, state inspection, and completion/error reporting
  Protocol direction:
  - auth/bootstrap must happen on an unscoped connection after `protocol.handshake`, before project/session attach is required
  - do not overload `project.attach` for remote auth bootstrap; use separate auth/bootstrap RPC
  Concrete contract to implement:
  - `auth.getBootstrapStatus` for unscoped pre-auth discoverability/status
  - `auth.completeBootstrap` for client-collected auth material handoff to the server
  - `auth.completeBootstrap` payload must support at least these explicit modes:
    - `browser_callback_url`
    - `browser_callback_code`
    - `device_code`
    - `api_key`
  - for browser/device modes, client sends collected callback/code/material; server performs any required exchange and persists the resulting auth method in the server auth store

- [ ] remove host-side binding dependence from remote attach
  First code entrypoints to change:
  - `cli/app/run_prompt_target.go`
  - `cli/app/session_server_target.go`
  - `shared/client/remote.go`
  - `shared/protocol/handshake.go`
  - `server/transport/gateway.go`
  Required direction:
  - `cli/app/run_prompt_target.go` and `cli/app/session_server_target.go` must stop requiring host-local metadata binding lookup before remote attach
  - remote dial must be able to connect unscoped, query server-owned project/workspace state, and then bind/attach using server-owned identifiers
  - the initial attach path must not require a host-known `project_id` derived from host persistence
  Protocol direction:
  - allow unscoped dial after `protocol.handshake` without immediate `project.attach`
  - keep `project.attach` scoped and explicit once the client has chosen a server-owned project/workspace target
  - if current project APIs are insufficient, add explicit server-owned workspace enumeration/selection to the project view surface rather than relying on host path resolution

- [ ] make remote attach resilient to host/server path mismatch
  First code entrypoints to change:
  - `server/transport/gateway.go`
  - `shared/client/remote.go`
  - `cli/app/run_prompt_target.go`
  - `cli/app/session_server_target.go`
  - `server/projectview/*`
  - `shared/serverapi/project_view.go`
  - `shared/clientui/project.go`
  Required direction:
  - project attach must no longer rely on exact host path == server path identity as a hard requirement for remote use
  - keep workspace-root path as an optional same-machine hint only
  - do not use a fuzzy or implicit server-chosen workspace default when a project has multiple workspaces/worktrees
  - require explicit server-owned workspace identity/query flow for remote attach when path hints are insufficient; `workspace_id` is the expected attach/selection identity
  - ensure session planning/run prompt execution operate against the explicitly selected server-side workspace once attached, even when the client machine has no matching absolute path
  Protocol direction:
  - extend project query surfaces so the client can enumerate/select server-owned workspaces for a project before scoped attach when needed
  - the explicit selection identity for this remote path is `workspace_id`
  - keep host path resolution as an optimization/hint, not the correctness path

- [ ] add transport/integration proof for the remote flow
  Required test scope:
  - first test files to extend:
    - `server/serve/serve_test.go`
    - `server/transport/gateway_test.go`
    - `cli/app/session_server_target_test.go`
    - `cli/app/run_prompt_test.go` or the closest remote run-prompt coverage file
  - cold-start remote server with no pre-seeded auth, then complete auth after transport connect through the new remote-auth bootstrap path
  - prove handshake/bootstrap discoverability: client can tell auth-not-ready plus available pre-auth methods before attach
  - attach from a client whose local persistence has no matching project binding metadata
  - attach from a client whose local workspace path differs from the server/container workspace path
  - attach to a project with multiple server-side workspaces and prove explicit workspace selection by server-owned identity rather than implicit defaulting
  - specifically cover multi-workspace explicit selection by `workspace_id` in `server/transport/gateway_test.go` and/or `server/projectview/service_test.go`
  - prove session launch and headless run prompt still execute against the server-owned workspace/auth state after those fixes

Exit criteria:

- remote server can boot unauthenticated and expose enough API for a client to finish auth against the server-owned auth store
- browser/device/paste remote auth flow is explicit: client collects callback/code/material, server stores resulting auth
- no host-local auth file is treated as the source of truth for a remote server
- remote attach no longer depends on host-local binding metadata
- host/server path mismatch no longer blocks project attach and run/session startup for the intended current-TUI remote flow
- multi-workspace remote attach uses explicit server-owned workspace identity rather than implicit server defaulting
- blocker scenarios from `docs/dev/app-server-migration/blockers.md` are either resolved or reduced to narrower documented follow-ups

### Phase 8: Frontend-Agnostic Transcript Semantics

Goal: improve transcript reliability systemically after shipment by defining one frontend-agnostic transcript semantics model, one reconciliation contract, and reproducible trace coverage that future Kotlin/Desktop/Web frontends can implement correctly.

Scope reduction:

- This phase is not about building Go-only shared frontend infrastructure.
- The valuable output is the semantics, invariants, trace fixtures, and reference behavior, not reusable Go packages for future frontends.
- The current Go TUI is only the first reference consumer/proving ground for those semantics.

Concrete tasks:

- [ ] consolidate committed-tail reconciliation so `eventTranscriptEntriesReconcileWithCommittedTail`-equivalent logic reasons in one place over session id, revision, committed count, committed start, and contiguous overlap
- [ ] replace event-kind-driven transcript handling with explicit transcript ops/invariants that can be implemented outside Go
- [ ] formalize one committed transcript model plus one live overlay model for frontend consumers regardless of implementation language
- [ ] define trace fixtures / replay cases that any frontend implementation can run against the transcript semantics
- [ ] use the current Go TUI as the first reference implementation/proof target for those semantics where helpful
- [ ] add deterministic transcript trace replay coverage so field failures can be reproduced against the semantics model rather than only against one frontend implementation
