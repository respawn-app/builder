# Phase 3 Execution Todo

Status: complete

Historical note:

- This file is a closed Phase 3 ledger.
- Its single-workspace daemon scope is intentionally historical.
- Phase 4 is responsible for the later app-global server transition and project/workspace/worktree model.

Purpose:

- preserve the final Phase 3 checklist state after the transport/daemon migration landed
- prevent future automated continues from reopening already-finished Phase 3 work

Completion rule:

- Phase 3 is complete only when the real transport is in place, the CLI can use it for preserved workflows, the repo test suite is green, and `./bin/builder` has been rebuilt from the completed tree.

## Locked Scope

- `builder serve` is strictly a server process.
- No interactive `serve` UX.
- Embedded mode continues using the same shared client boundary as daemon mode.
- The daemon remains single-workspace in this phase.
- Questions are recorded in `questions.md` instead of blocking on user interaction.

## Final Status

### A. Transport And Host Surface

- [x] Local transport bootstrap is a local-only HTTP server with WebSocket JSON-RPC plus health/readiness endpoints.
- [x] `server/serve` owns explicit startup, serving, shutdown, and discovery lifecycle.
- [x] The daemon binds locally by default (`127.0.0.1`).
- [x] Server identity is returned from protocol handshake and published in discovery.

### B. Protocol Gateway

- [x] JSON-RPC-over-WebSocket transport exists in `server/transport`.
- [x] The protocol method registry is defined in `shared/protocol/handshake.go`.
- [x] Handshake returns protocol version plus capability flags.
- [x] App methods avoid the reserved `rpc.*` namespace.
- [x] Dispatch uses the shared `shared/serverapi` and `shared/client` contracts.
- [x] Stable protocol stream errors map to gap / unavailable / failed codes.
- [x] Mutating shared contracts keep `client_request_id` requirements.
- [x] Server-initiated session activity, prompt activity, and process output stream over the transport.

### C. Connection / Attachment Lifecycle

- [x] Connect -> handshake -> attach flow is enforced.
- [x] Attach remains separate from subscribe.
- [x] Attach acknowledgements return attachment metadata rather than hydration payloads.
- [x] Project-level and session-level attachment both exist.
- [x] Session mutations remain serialized server-side through the existing runtime/session services.

### D. Subscription And Streaming

- [x] Session activity is exposed over the real transport.
- [x] Prompt activity is exposed over the real transport.
- [x] Process output is exposed over the real transport.
- [x] Stream error semantics preserve gap / unavailable / failed behavior.
- [x] EOF / cancellation semantics are normalized across remote subscriptions.
- [x] Slow subscribers receive explicit gap failure instead of silent loss.

### E. Typed Queries And Mutations

- [x] Project discovery reads cross the transport.
- [x] Session planning, lifecycle, runtime activation, and hydration reads cross the transport.
- [x] Runtime control crosses the transport.
- [x] Process read/control crosses the transport.
- [x] Ask/approval reads and answers cross the transport.
- [x] Headless `run.prompt` works over the transport.
- [x] Only shared transport-neutral contracts cross the boundary.

### F. Compatibility

- [x] A real external-daemon client exists in `shared/client/remote.go`.
- [x] Embedded mode and daemon mode both use the same shared client boundary.
- [x] Interactive CLI startup prefers a discovered compatible daemon, can start a local daemon, and otherwise falls back to embedded mode.
- [x] Headless `RunPrompt` can attach to or auto-start a daemon.
- [x] Preserved interactive workflows now run against daemon mode as well as embedded mode.
- [x] Remote logout/reauth continuity uses the same local auth store contract on the client machine.

### G. Proof

- [x] Host / health / readiness / discovery lifecycle coverage exists in `server/serve/serve_test.go`.
- [x] Handshake, attachment, and stream gateway coverage exists in `server/transport/gateway_test.go`.
- [x] Headless daemon attach coverage exists in `cli/app/run_prompt_test.go`.
- [x] Interactive daemon attach, prompt round-trip, lifecycle draft persistence, process flow, and parity coverage exist in `cli/app/session_server_target_test.go`.
- [x] Embedded-vs-daemon parity for the same interactive workflow exists in `cli/app/session_server_target_test.go`.
- [x] The repo-wide test suite passed via `./scripts/test.sh ./...`.
- [x] The binary was rebuilt via `bash ./scripts/build.sh --output ./bin/builder`.

## Notes

- Prompt delivery is no longer timer-polling over read APIs. It is a real transport-backed prompt activity stream.
- Remaining migration work now belongs to later phases: reconnect hardening, broader multi-client correctness, data adoption, and non-CLI frontend work.
