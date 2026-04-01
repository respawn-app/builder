# App Server Migration: Phase 2 Checkpoint

Status: in progress

This checkpoint tracks the first resource-model and hydration slice after the Phase 1 client-boundary completion.

## Landed Slice

- Introduced explicit active-run identity in `server/runtime` via `RunSnapshot`, with lifecycle-managed `run_id`, `step_id`, `started_at`, and run status.
- Extended `runtime.EventRunStateChanged` payloads so run-state events can carry explicit `run_id`, status, and timing instead of only a busy boolean.
- Added `runtimeview.RunViewFromRuntime(...)` so run identity is projected through the same client-facing seam as other runtime DTOs.
- Introduced `shared/clientui.RunView`, `RunStatus`, and `RuntimeMainView` as the first Phase 2 resource/hydration surface.
- Extended `shared/clientui.RuntimeClient` with `MainView()` so frontends can fetch a typed active-session hydration bundle that includes session, status, and active-run state together.
- Promoted `RuntimeMainView` assembly into `server/runtimeview`, so session/status/active-run hydration is now projected on the server side rather than composed in the CLI loopback adapter.
- Added `server/runtimeview.Reader` as the first server-owned application read service for active-session hydration, with the CLI loopback runtime client delegating read paths through that service.
- Switched the CLI runtime client and local UI hydration helpers onto `RuntimeMainView`, so the new bundled hydration surface is exercised in production code rather than existing only as an unused type.
- Added durable run lifecycle entries to the existing session event log and `server/session` run reducers, so completed and interrupted runs can now be reconstructed after reopen through `ReadRuns()` / `LatestRun()`.
- Split live run-state emission from durable run-history persistence, so only explicit primary-run paths write durable run lifecycle entries.
- Added the first transport-neutral application read service via `shared/serverapi` + `shared/client` + `server/sessionview`, with typed `session.getMainView` / `run.get`-style requests backed by either a live runtime or durable session state.
- Reworked `server/sessionview` around explicit session/runtime resolvers keyed by `session_id`, so the read service is no longer a single pre-bound session helper.
- Made dormant session hydration side-effect-free by replaying against an isolated cloned store, and added proof that read APIs do not mutate persisted session files.
- Moved the CLI loopback read path onto that application read service and removed the direct runtime-projection fallback.
- Added read-only `server/session.Snapshot` loaders plus embedded-server runtime/session registries, so the production `sessionview` path now resolves dormant sessions from persistence root and active runtimes from server-owned state rather than static one-session wiring.
- Added focused lifecycle coverage proving `EventRunStateChanged` emits stable `run_id`, status, and timing for both completed and interrupted runs.
- Added a real-engine loopback test proving `RuntimeClient.MainView()` exposes active-run hydration while a run is in flight.
- Added integration coverage proving the real `cli/app` `PrepareRuntime(...)` path registers the live runtime into the shared `SessionViewClient` read surface, rather than only through manual test registration.

## What This Proves

- Run identity now exists as a typed concept on the live runtime boundary instead of being implied only by step-local busy state.
- The client-facing contract now has a single active-session hydration bundle backed by a server-owned read/projection surface that can grow into the Phase 2 `session.getMainView` shape.
- Run lifecycle metadata now survives engine teardown through the existing session log, giving Phase 2 its first durable `run.get` building block without introducing a new storage subsystem.
- The first transport-neutral read boundary now exists for session main-view hydration and run lookup rather than reads living only as live-engine helpers.
- The read boundary now resolves resources by ID and can hydrate dormant sessions without mutating persisted state, which is the minimum correctness bar for future daemon/web clients.
- The embedded server now owns the production resolver path for session hydration, which is the first concrete move from loopback-only helpers toward a real multi-session app-server read layer.
- Phase 2 can proceed incrementally without introducing a durable run store or transport-level event redesign yet.

## Current Limitations

- Durable run history currently covers lifecycle metadata only. There is still no richer run-scoped index for processes, asks, approvals, or delegated task state.
- Reopen semantics currently reconstruct unfinished durable runs from `run_started` without a matching `run_finished`, but that state is not yet surfaced through a higher-level application read API.
- The new application read service still uses partial dormant reconstruction rather than richer persisted read models for settings/process/approval state, and the current server-owned registries are embedded-mode only rather than shared daemon infrastructure.
- The UI is hydrating through `RuntimeMainView`, but it is not yet rendering run-specific UX beyond carrying the typed data.

## Next Slice

- Promote the current embedded-mode registries into reusable server infrastructure so session selection, detached hydration, and later transport handlers can all resolve sessions/runtimes through the same server-owned registry layer.
- Introduce explicit process/run ownership links and session/run hydration tests that assume more than one run over time.
