# App Server Migration: Phase 1 Checkpoint

Status: in progress

This checkpoint tracks the first real extraction slice after Phase 0 characterization.

## Landed Slice

- Added `shared/serverapi` as the first transport-neutral application-service surface for the headless `builder run` path.
- Added `shared/client` loopback client wiring so the headless frontend path now calls a client boundary instead of directly orchestrating runtime/session internals.
- Reduced `cli/app/run_prompt.go` to a thin frontend adapter that maps CLI/headless inputs onto `serverapi.RunPromptRequest` and result DTOs.
- Introduced `server/runprompt` as the first server-owned application-service package for the headless `builder run` path, with `cli/app/headless_prompt_server.go` reduced to a wrapper.
- Added service- and client-level tests for the new seam.
- Established the first frontend-facing seam for `cli/app/run_prompt.go`, with future boundary enforcement still to be rebuilt in a less brittle form.
- `RunPromptRequest` now carries a required `client_request_id`, so the first mutating client contract matches the locked migration requirements.
- Added server-side duplicate suppression tests proving repeated `client_request_id` submissions share in-flight work, reject payload mismatches, and do not permanently cache cancellation failures.

## What This Proves

- The first non-TUI frontend path can already go through a client-facing boundary without changing product behavior.
- Transport-neutral request/response/progress DTOs can wrap current runtime launch and submission logic without exposing `runtime.Engine`, `session.Store`, or `runtime.Event` directly to the thin frontend adapter.
- The headless seam can be extracted incrementally before any WebSocket or daemon work starts.
- The first mutating seam now has real retry protection at the server boundary rather than only carrying the `client_request_id` shape.

Current limitations:

- `server/runprompt` is authoritative for the headless `builder run` path, but `cli/app` still contains duplicated helper implementations and mixed interactive/server composition that need further extraction in later Phase 1 slices.
- The current duplicate suppression is process-local and scoped to the embedded server boundary; broader protocol-wide idempotency for future server methods remains Phase 2 work.

## Remaining Work In Phase 1

- Extract the next server-owned use cases around launch-context resolution, session open/create, and session hydration instead of keeping that composition trapped in `cli/app`.
- Decide where the first stable embedded client bootstrap lives so future CLI and test clients do not need app-private construction knowledge.
- Expand import-boundary enforcement once more frontend files stop depending on mixed `cli/app` server composition.
- Prepare the first acceptance-style embedded test client so the same scenarios can later run against external daemon mode.
- Collapse or delete the remaining duplicated helper implementations shared by `cli/app` and `server/runprompt` once the interactive extraction slice is ready.
