# App Server Migration: Phase 2 Checkpoint

Status: foundation slice complete; broader Phase 2 still in progress

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
- Threaded explicit process ownership through shell-backed background execution, so live background processes now carry owning `session_id`, `run_id`, and `step_id` rather than only a session-scoped manager identity.
- Added the first transport-neutral process read service via `shared/serverapi` + `shared/client` + `server/processview`, with embedded-mode production reads resolving through the server-owned background manager.
- Added the first transport-neutral process control service for `kill` and `inline-output` actions via the same `server/processview` / `shared/client` boundary.
- Switched the CLI `/ps` surface onto that shared process boundary for list hydration plus kill/inline control, while preserving local log opening as a frontend action over server-provided log paths.
- Added focused coverage proving process ownership is stamped at creation time, survives projection through the server read service, and is available through embedded-mode loopback reads.
- Added focused coverage proving the real embedded `PrepareRuntime(...)` path wires both process reads and process control through the shared client boundary rather than the local manager path.
- Promoted the embedded-only runtime and persistence resolvers into reusable `server/registry` infrastructure, so server-owned read and live-session services no longer depend on private one-off helper types trapped inside `server/embedded`.
- Added the first transport-neutral live session-activity subscription seam via `shared/serverapi` + `shared/client` + `server/sessionactivity`, backed by server-owned runtime registries rather than CLI-local event bridges.
- Added explicit lag handling for live session-activity subscribers: a subscriber that falls behind receives a deterministic stream failure and must rehydrate rather than silently missing events.
- Added focused coverage proving the real `cli/app` `PrepareRuntime(...)` path wires shared session-activity publication, so two shared clients can hydrate the same active session and observe the same runtime-originated session update through the embedded server boundary.

## What This Proves

- Run identity now exists as a typed concept on the live runtime boundary instead of being implied only by step-local busy state.
- The client-facing contract now has a single active-session hydration bundle backed by a server-owned read/projection surface that can grow into the Phase 2 `session.getMainView` shape.
- Run lifecycle metadata now survives engine teardown through the existing session log, giving Phase 2 its first durable `run.get` building block without introducing a new storage subsystem.
- The first transport-neutral read boundary now exists for session main-view hydration and run lookup rather than reads living only as live-engine helpers.
- The read boundary now resolves resources by ID and can hydrate dormant sessions without mutating persisted state, which is the minimum correctness bar for future daemon/web clients.
- The embedded server now owns the production resolver path for session hydration, which is the first concrete move from loopback-only helpers toward a real multi-session app-server read layer.
- Live process resources now have explicit session/run ownership on the server side, and `/ps` list hydration no longer depends on CLI-local snapshot projection of the background manager.
- `/ps` control actions now also flow through the same shared process boundary, so the CLI no longer owns direct kill/inline process mutations.
- Live session activity now has a shared server-owned observation seam with explicit lag failure semantics, rather than existing only as a CLI-local runtime event bridge.
- The Phase 2 foundation exit gate is now satisfied: a second shared client can hydrate and observe the same active session in tests.

## Current Limitations

- Durable run history currently covers lifecycle metadata only. There is still no durable run-scoped index for processes, asks, approvals, or delegated task state after process exit or restart.
- Process ownership/read metadata is currently live-only and in-memory. Restarting the app server loses process resources and their run/step ownership history.
- Reopen semantics currently reconstruct unfinished durable runs from `run_started` without a matching `run_finished`, but that state is not yet surfaced through a higher-level application read API.
- The new application read services still use partial dormant reconstruction rather than richer persisted read models for settings/approval state.
- Process control is only partially on the new boundary so far: `kill` and `inline-output` are shared, `kill` now carries `client_request_id` as a mutating contract, but log opening remains a frontend-local action over server-provided file paths.
- The UI is hydrating through `RuntimeMainView` and the process read service, but it is not yet rendering richer run/process-specific UX beyond carrying the typed data.
- The live session-activity stream is active-session-only, live-only, and non-authoritative for reconnect. Reconnect is expected to rehydrate from reads.
- This checkpoint does not mean all Phase 2 requirements are done. Still open from the broader spec: project registry/read surfaces, ask and approval resource/read surfaces, fuller event/live-feed classes, and the transcript paging/compression model for large-session hydration.

## Next Slice

- Continue broader Phase 2: land project registry/read surfaces, ask/approval resources, and the remaining live-feed split needed before transport work can be called complete.
