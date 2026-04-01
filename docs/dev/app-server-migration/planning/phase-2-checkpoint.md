# App Server Migration: Phase 2 Checkpoint

Status: in progress

This checkpoint tracks the first resource-model and hydration slice after the Phase 1 client-boundary completion.

## Landed Slice

- Introduced explicit active-run identity in `server/runtime` via `RunSnapshot`, with lifecycle-managed `run_id`, `step_id`, `started_at`, and run status.
- Extended `runtime.EventRunStateChanged` payloads so run-state events can carry explicit `run_id`, status, and timing instead of only a busy boolean.
- Added `runtimeview.RunViewFromRuntime(...)` so run identity is projected through the same client-facing seam as other runtime DTOs.
- Introduced `shared/clientui.RunView`, `RunStatus`, and `RuntimeMainView` as the first Phase 2 resource/hydration surface.
- Extended `shared/clientui.RuntimeClient` with `MainView()` so frontends can fetch a typed active-session hydration bundle that includes session, status, and active-run state together.
- Switched the CLI runtime client and local UI hydration helpers onto `RuntimeMainView`, so the new bundled hydration surface is exercised in production code rather than existing only as an unused type.

## What This Proves

- Run identity now exists as a typed concept on the live runtime boundary instead of being implied only by step-local busy state.
- The client-facing contract now has a single active-session hydration bundle that can grow into the Phase 2 `session.getMainView` shape.
- Phase 2 can proceed incrementally without introducing a durable run store or transport-level event redesign yet.

## Current Limitations

- Run identity is currently live-runtime state only. Historical runs and durable run indexing are not implemented yet.
- `RuntimeMainView` is still loopback-only. The future protocol/read-model work still needs a transport-neutral server read surface behind it.
- The UI is hydrating through `RuntimeMainView`, but it is not yet rendering run-specific UX beyond carrying the typed data.

## Next Slice

- Promote `RuntimeMainView` from loopback helper to a server-owned application read surface.
- Add durable session/run identifiers and minimum read models suitable for `session.getMainView` and `run.get`.
- Introduce explicit process/run ownership links and session/run hydration tests that assume more than one run over time.
