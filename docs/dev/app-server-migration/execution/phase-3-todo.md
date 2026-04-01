# Phase 3 Execution Todo

Status: active execution tracker

Purpose:

- prevent drift across automated continue prompts and agent handoffs
- track every concrete Phase 3 obligation derived from `../planning/plan.md` and the migration specs
- serve as the only authoritative execution checklist until Phase 3 is complete

Completion rule:

- Do not consider Phase 3 complete until every item in `Phase 3 Core`, `Phase 3 Compatibility`, `Phase 3 Proof`, and `Phase 3 Polish` is checked, tests are green, `./bin/builder` is rebuilt, and the resulting product can actually run preserved CLI workflows against an external daemon path rather than only embedded mode.

## Locked Scope

- `builder serve` is strictly a server process.
- No interactive `serve` UX.
- Embedded mode must continue using the same client boundary, not direct server object access.
- The server currently remains single-workspace in this phase; do not expand into multi-workspace hosting during Phase 3.
- Questions must be written to `questions.md` instead of blocking on user interaction.

## Current Baseline

- [x] Reusable server core exists in `server/core`.
- [x] Transport-neutral startup exists in `server/startup.StartCore(...)`.
- [x] `builder serve` exists as a standalone host shell via `server/serve`.
- [x] `builder serve` uses server-owned headless startup handlers instead of `cli/app` startup UX.
- [x] Local-only HTTP host bootstrap exists in `server/serve` with discovery record publication.
- [x] Health/readiness endpoints exist for the daemon host.
- [ ] No real transport exists yet.
- [ ] No external daemon attach path exists yet.

## Recent Checkpoints

- [x] Shared lifecycle seam exists for session initial input, draft persistence, and transition resolution via `shared/serverapi/session_lifecycle.go`, `shared/client/session_lifecycle.go`, and `server/sessionlifecycle`.
- [x] `server/core` exposes `SessionLifecycleClient()` for embedded and future transport-backed clients.
- [x] Focused lifecycle coverage exists in `server/sessionlifecycle/service_test.go`, `server/core/core_test.go`, and `cli/app` characterization tests.
- [x] Shared session-planning seam exists via `shared/serverapi/session_launch.go`, `shared/client/session_launch.go`, and `server/sessionlaunch`.
- [x] The CLI planner now owns picker composition using `ProjectViewClient` summaries and sends explicit `client_request_id` plan requests.
- [x] `server/core` now owns a session-store registry so planning, lifecycle, and runtime prep resolve live sessions by `session_id`.

## Phase 3 Core

### A. Transport And Host Surface

- [ ] Define the concrete local transport bootstrap shape for v1 daemon mode.
- [x] Introduce a real listener/bootstrap layer for `server/serve`.
- [ ] Keep bind local-only by default.
- [x] Expose a health endpoint outside the RPC surface.
- [x] Expose a readiness endpoint outside the RPC surface.
- [ ] Make host lifecycle explicit: startup, serving, shutdown, cleanup.
- [ ] Ensure server identity is available to clients after connect.

### B. Protocol Gateway

- [ ] Add JSON-RPC-over-WebSocket transport package(s).
- [ ] Define the protocol method registry for v1.
- [ ] Implement protocol handshake before attach or query calls.
- [ ] Return protocol version and capability flags from handshake.
- [ ] Avoid `rpc.*` namespace for app methods.
- [ ] Implement request dispatch onto existing shared/client/shared/serverapi surfaces.
- [ ] Map server errors into stable protocol errors.
- [ ] Preserve idempotent mutating request contracts already present on the shared boundary.
- [ ] Support server-initiated asks/approvals/events through the transport.

### C. Connection / Attachment Lifecycle

- [ ] Implement explicit connect -> handshake -> attach flow.
- [ ] Keep attach separate from subscribe.
- [ ] Attach should acknowledge resource metadata only, not full snapshots.
- [ ] Support project-level context attachment.
- [ ] Support session-level attachment.
- [ ] Ensure attachment cleanup on disconnect.
- [ ] Ensure session mutating operations remain serialized server-side.

### D. Subscription And Streaming

- [ ] Expose session activity subscription over the real transport.
- [ ] Expose process output subscription over the real transport.
- [ ] Preserve current stream error vocabulary: gap / unavailable / failed.
- [ ] Preserve EOF / context cancellation semantics where applicable.
- [ ] Implement transport-level stream lifecycle and close handling.
- [ ] Ensure slow-client / backlog failure is surfaced explicitly rather than hanging silently.

### E. Typed Queries And Mutations

- [ ] Expose project discovery reads over the real transport.
- [ ] Expose session hydration reads over the real transport.
- [ ] Expose process reads/control over the real transport.
- [ ] Expose pending ask/approval reads over the real transport.
- [ ] Expose headless run-prompt path over the real transport where appropriate.
- [ ] Ensure only existing transport-neutral shared contracts cross the boundary.

## Phase 3 Compatibility

### A. Client Transport Layer

- [ ] Add a real external-daemon client implementation alongside loopback clients.
- [ ] Keep client interfaces aligned with `shared/client` contracts.
- [ ] Implement transport connect/close lifecycle.
- [ ] Implement handshake/capability negotiation in the client.
- [ ] Implement attach helpers without leaking transport details into CLI UI code.
- [ ] Keep embedded mode on the same client boundary.

### B. CLI Attach-Or-Start Path

- [ ] Add local daemon discovery/bootstrap logic.
- [ ] Implement CLI attach to an already-running server.
- [ ] Implement CLI start-local-server when no daemon is available.
- [ ] Preserve current CLI workflows after attach.
- [ ] Ensure CLI can target external daemon mode for the same workflows.
- [ ] Avoid hidden fallback to privileged in-process objects once external daemon mode is selected.

### C. Embedded / External Parity

- [ ] Ensure embedded mode still works through the same boundary.
- [ ] Ensure external daemon mode uses the same client semantics.
- [ ] Keep feature behavior preserved across both modes.

## Phase 3 Proof

### A. Automated Tests

- [ ] Unit tests for host listener/bootstrap and health/readiness.
- [ ] Unit tests for handshake/capability exchange.
- [ ] Unit tests for connection attach/subscription lifecycle.
- [ ] Unit tests for JSON-RPC request/response and error mapping.
- [ ] Integration tests for external daemon startup and client attach.
- [ ] Integration tests for CLI attach-or-start logic.
- [ ] Integration tests proving embedded mode and daemon mode use the same client boundary.
- [ ] Integration tests for project/session/process/ask/approval reads over transport.
- [ ] Integration tests for session activity and process output streams over transport.
- [ ] Integration tests for disconnect/shutdown cleanup.

### B. Acceptance Gates

- [ ] A non-CLI client/test harness can talk to the external daemon over the real transport.
- [ ] CLI can perform preserved workflows against an external daemon, not only embedded mode.
- [ ] `builder serve` can be started independently and then attached to.
- [ ] Embedded mode still passes existing workflows through the same boundary.
- [ ] Health/readiness endpoints report correctly.
- [ ] Capability handshake is exercised in tests.

## Phase 3 Polish

- [ ] Update docs affected by real daemon mode and protocol bootstrapping.
- [ ] Update migration checkpoint docs if Phase 3 becomes complete.
- [ ] Keep `questions.md` up to date with unresolved ambiguities and assumptions made.
- [ ] Rebuild `./bin/builder` before final handoff.
- [ ] Ensure full test suite is green before final handoff.

## Deferred Until After Phase 3

- Multi-workspace hosting in one server process.
- Phase 4 reconnect hardening beyond the minimum needed for Phase 3 transport viability.
- Additional non-CLI frontend implementations.

## Immediate Next Slice

- [ ] Extract the remaining interactive runtime-control surface behind shared `serverapi` + `shared/client` contracts, then expose ask/approval answer mutations and wire those surfaces through the real transport for external interactive mode.
