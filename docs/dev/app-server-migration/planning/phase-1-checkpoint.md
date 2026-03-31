# App Server Migration: Phase 1 Checkpoint

Status: in progress

This checkpoint tracks the first real extraction slice after Phase 0 characterization.

## Landed Slice

- Added `shared/serverapi` as the first transport-neutral application-service surface for the headless `builder run` path.
- Added `shared/client` loopback client wiring so the headless frontend path now calls a client boundary instead of directly orchestrating runtime/session internals.
- Reduced `cli/app/run_prompt.go` to a thin frontend adapter that maps CLI/headless inputs onto `serverapi.RunPromptRequest` and result DTOs.
- Moved headless runtime launch/orchestration behind an app-owned launcher adapter in `cli/app/headless_prompt_server.go`.
- Added service- and client-level tests for the new seam.
- Ratcheted `internal/architecture/import_boundary_test.go` so `cli/app/run_prompt.go` cannot regain direct imports of server-authority packages.
- `RunPromptRequest` now carries a required `client_request_id`, so the first mutating client contract matches the locked migration requirements.

## What This Proves

- The first non-TUI frontend path can already go through a client-facing boundary without changing product behavior.
- Transport-neutral request/response/progress DTOs can wrap current runtime launch and submission logic without exposing `runtime.Engine`, `session.Store`, or `runtime.Event` directly to the thin frontend adapter.
- The headless seam can be extracted incrementally before any WebSocket or daemon work starts.

Current limitation:

- This slice establishes the required `client_request_id` contract, but it does not yet implement duplicate suppression or replay-safe deduplication for repeated `RunPrompt` submissions. Full idempotency semantics remain pending work behind the new request shape.

## Remaining Work In Phase 1

- Extract the next server-owned use cases around launch-context resolution, session open/create, and session hydration instead of keeping that composition trapped in `cli/app`.
- Decide where the first stable embedded client bootstrap lives so future CLI and test clients do not need app-private construction knowledge.
- Expand import-boundary enforcement once more frontend files stop depending on mixed `cli/app` server composition.
- Prepare the first acceptance-style embedded test client so the same scenarios can later run against external daemon mode.
- Implement scoped replay protection for `RunPrompt` so duplicate `client_request_id` retries do not execute the same prompt twice.
