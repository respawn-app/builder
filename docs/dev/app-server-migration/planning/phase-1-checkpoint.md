# App Server Migration: Phase 1 Checkpoint

Status: in progress

This checkpoint tracks the first real extraction slice after Phase 0 characterization.

## Landed Slice

- Added `shared/serverapi` as the first transport-neutral application-service surface for the headless `builder run` path.
- Added `shared/client` loopback client wiring so the headless frontend path now calls a client boundary instead of directly orchestrating runtime/session internals.
- Reduced `cli/app/run_prompt.go` to a thin frontend adapter that maps CLI/headless inputs onto `serverapi.RunPromptRequest` and result DTOs.
- Introduced `server/runprompt` as the first server-owned application-service package for the headless `builder run` path, with `cli/app/headless_prompt_server.go` reduced to a wrapper.
- Introduced `server/bootstrap` as the server-owned home for embedded bootstrap composition: config/container resolution, auth-manager creation, and runtime-support setup now come from a server package instead of being constructed directly inside `cli/app/bootstrap.go`.
- Introduced `server/embedded` as the first explicit in-process app-server composition root: `cli/app` entrypoints now start a server-owned object and use its exported capabilities instead of carrying a frontend-local bootstrap bag of authoritative state.
- Introduced `server/authflow` as the server-owned home for auth readiness polling and env-backed auth-store policy, leaving `cli/app/auth_gate.go` with only frontend interaction behavior.
- Introduced `server/launch` as the server-owned home for bootstrap continuation resolution and session open/create/hydration planning, with `cli/app/bootstrap.go` and `cli/app/launch_planner.go` now acting as adapters around it.
- Introduced `server/lifecycle` as the server-owned home for interactive lifecycle mutations: draft persistence, rollback fork creation, and logout-state clearing now come from a server package instead of being performed directly in `cli/app/session_lifecycle.go`.
- Introduced `server/runtimewire` as the server-owned home for runtime preparation, local tool registry construction, background-event routing, outside-workspace approvals, and runtime event bridging; `cli/app/runtime_factory.go` and `server/runprompt/headless.go` now delegate to it instead of owning those implementations directly.
- Introduced `shared/clientui` plus `server/runtimeview` as the first client-facing UI projection seam: the TUI runtime adapter now consumes projected UI DTOs instead of reading `runtime.Event` and `runtime.ChatSnapshot` directly in its main update path.
- Tightened that first UI seam so the `uiModel` event channel path now consumes projected `shared/clientui.Event` values directly, and client-facing tool-call metadata no longer aliases mutable server transcript structures.
- Added service- and client-level tests for the new seam.
- Added the first acceptance-style embedded loopback test around `server/embedded.Start(...).RunPromptClient()` to prove the in-process server object is a real runnable boundary, not just a packaging wrapper.
- Established the first frontend-facing seam for `cli/app/run_prompt.go`, with future boundary enforcement still to be rebuilt in a less brittle form.
- `RunPromptRequest` now carries a required `client_request_id`, so the first mutating client contract matches the locked migration requirements.
- Added server-side duplicate suppression tests proving repeated `client_request_id` submissions share in-flight work, reject payload mismatches, and do not permanently cache cancellation failures.

## What This Proves

- The first non-TUI frontend path can already go through a client-facing boundary without changing product behavior.
- Transport-neutral request/response/progress DTOs can wrap current runtime launch and submission logic without exposing `runtime.Engine`, `session.Store`, or `runtime.Event` directly to the thin frontend adapter.
- The headless seam can be extracted incrementally before any WebSocket or daemon work starts.
- The first mutating seam now has real retry protection at the server boundary rather than only carrying the `client_request_id` shape.
- Session bootstrap continuation lookup and session open/create/hydration now have a server-owned implementation shared by both interactive and headless flows.
- Embedded bootstrap state for auth/runtime support now also comes from a server-owned package rather than being constructed ad hoc in the frontend package.
- Embedded CLI startup now goes through an explicit server-owned object, which is the first runnable target for future embedded acceptance tests.
- The first embedded acceptance-style loopback workflow now runs through that server object and persists a real session transcript through the server-owned path.
- Auth readiness polling and env-backed auth-store policy now also come from a server-owned package; the frontend only supplies the interactive auth UX.
- Interactive lifecycle mutations now also go through a server-owned package, leaving the frontend to translate `UITransition` and drive re-auth UX only.
- The first TUI adapter path now consumes client-facing projected UI DTOs instead of raw runtime-native event/snapshot structs.
- Runtime preparation and local runtime/tool wiring now also have one server-owned implementation shared by both interactive and headless flows.

Current limitations:

- `server/bootstrap`, `server/embedded`, `server/authflow`, `server/runprompt`, `server/launch`, `server/lifecycle`, `server/runtimewire`, and `server/runtimeview` now own the first real server-side launch/runtime path, but `cli/app` still owns auth/onboarding interaction flow and large parts of the runtime-native UI adapter surface that need further extraction in later Phase 1 slices.
- The current duplicate suppression is process-local and scoped to the embedded server boundary; broader protocol-wide idempotency for future server methods remains Phase 2 work.

## Remaining Work In Phase 1

- Decide how the remaining auth/onboarding interaction loop moves onto a stable client/server bootstrap boundary without reintroducing frontend ownership of server state.
- Continue replacing direct TUI/runtime coupling with client-facing projections and read models beyond the first runtime-event/chat-snapshot seam.
- Expand import-boundary enforcement once more frontend files stop depending on mixed `cli/app` server composition.
- Expand the first acceptance-style embedded test client coverage so the same scenarios can later run unchanged against external daemon mode.
- Replace runtime-native UI event/snapshot consumption with client-facing read models and events now that the embedded server bootstrap boundary is explicit.
