# App Server Migration: Phase 1 Checkpoint

Status: in progress

This checkpoint tracks the first real extraction slice after Phase 0 characterization.

## Landed Slice

- Added `shared/serverapi` as the first transport-neutral application-service surface for the headless `builder run` path.
- Added `shared/client` loopback client wiring so the headless frontend path now calls a client boundary instead of directly orchestrating runtime/session internals.
- Reduced `cli/app/run_prompt.go` to a thin frontend adapter that maps CLI/headless inputs onto `serverapi.RunPromptRequest` and result DTOs.
- Introduced `server/runprompt` as the first server-owned application-service package for the headless `builder run` path, with `cli/app/headless_prompt_server.go` reduced to a wrapper.
- Introduced `server/launch` as the server-owned home for bootstrap continuation resolution and session open/create/hydration planning, with `cli/app/bootstrap.go` and `cli/app/launch_planner.go` now acting as adapters around it.
- Introduced `server/runtimewire` as the server-owned home for runtime preparation, local tool registry construction, background-event routing, outside-workspace approvals, and runtime event bridging; `cli/app/runtime_factory.go` and `server/runprompt/headless.go` now delegate to it instead of owning those implementations directly.
- Added service- and client-level tests for the new seam.
- Established the first frontend-facing seam for `cli/app/run_prompt.go`, with future boundary enforcement still to be rebuilt in a less brittle form.
- `RunPromptRequest` now carries a required `client_request_id`, so the first mutating client contract matches the locked migration requirements.
- Added server-side duplicate suppression tests proving repeated `client_request_id` submissions share in-flight work, reject payload mismatches, and do not permanently cache cancellation failures.

## What This Proves

- The first non-TUI frontend path can already go through a client-facing boundary without changing product behavior.
- Transport-neutral request/response/progress DTOs can wrap current runtime launch and submission logic without exposing `runtime.Engine`, `session.Store`, or `runtime.Event` directly to the thin frontend adapter.
- The headless seam can be extracted incrementally before any WebSocket or daemon work starts.
- The first mutating seam now has real retry protection at the server boundary rather than only carrying the `client_request_id` shape.
- Session bootstrap continuation lookup and session open/create/hydration now have a server-owned implementation shared by both interactive and headless flows.
- Runtime preparation and local runtime/tool wiring now also have one server-owned implementation shared by both interactive and headless flows.

Current limitations:

- `server/runprompt`, `server/launch`, and `server/runtimewire` now own the first real server-side launch/runtime path, but `cli/app` still owns app-private embedded bootstrap/auth flow, interactive lifecycle mutations, and runtime-native UI adapters that need further extraction in later Phase 1 slices.
- The current duplicate suppression is process-local and scoped to the embedded server boundary; broader protocol-wide idempotency for future server methods remains Phase 2 work.

## Remaining Work In Phase 1

- Decide where the first stable embedded client bootstrap lives so future CLI and test clients do not need app-private construction knowledge.
- Extract app-private bootstrap/auth manager creation out of `cli/app/bootstrap.go` so embedded mode stops constructing server authority inside the frontend package.
- Extract privileged session/auth mutations out of `cli/app/session_lifecycle.go` and related helpers so the interactive lifecycle stops calling server internals directly.
- Expand import-boundary enforcement once more frontend files stop depending on mixed `cli/app` server composition.
- Prepare the first acceptance-style embedded test client so the same scenarios can later run against external daemon mode.
- Replace runtime-native UI event/snapshot consumption with client-facing read models and events once the embedded bootstrap boundary is stable.
