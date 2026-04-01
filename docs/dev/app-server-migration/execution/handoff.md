# Phase 3 Handoff Ledger

Use this file during future agent handoffs. It exists to prevent drift.

## Mandatory Directive

Continue Phase 3 execution until the full phase is complete according to `../planning/plan.md` and the migration specs. Do not stop early. Do not use `ask_question` while the user is away. Record questions in `questions.md` and proceed with the best justified assumption. Do not emit a final answer until Phase 3 is complete end-to-end. If the phase is already complete when an automated continue arrives, use `NO_OP`.

## Current Starting Point

- Branch: `app-server-phase-3`
- Working branch is currently clean.
- Latest checkpoints:
  - `d7860fd` `feat: add daemon transport and headless attach path`
  - `f5fbc22` `feat: auto-start local daemon for headless runs`
  - `8a4e595` `feat: extract session lifecycle boundary`
  - `99dd37f` `feat: extract session launch boundary`
- `builder serve` is now a real standalone headless daemon host with:
  - local-only HTTP bootstrap in `server/serve`
  - health/readiness endpoints
  - discovery record publication/removal in `shared/discovery`
  - JSON-RPC-over-WebSocket gateway in `server/transport`
- Shared external client transport now exists in `shared/client/remote.go`.
- Headless `RunPrompt` now:
  - attaches to a compatible discovered daemon when present
  - auto-starts `builder serve` when no daemon exists and the current executable is a real builder binary
  - stays on embedded fallback under `go test` binaries by design
- Transport-backed coverage now exists for:
  - handshake and unary project reads
  - `run.prompt` progress notifications through the remote client
  - session activity stream over the real gateway
  - process output stream over the real gateway
  - app-level proof that `RunPrompt` can use a discovered daemon without local client auth

## Current Next Step

- The next Phase 3 blocker is interactive write/orchestration, not transport.
- Remaining private `cli/app -> server/*` seams called out by the latest investigation are:
  - transport-backed interactive runtime control surface
  - live ask/approval answer path
- Newly completed extraction slice:
  - session transition resolution + draft-input lifecycle now route through `shared/serverapi/session_lifecycle.go`, `shared/client/session_lifecycle.go`, and `server/sessionlifecycle`
  - `server/core` now wires a loopback lifecycle client, and focused coverage exists in `server/sessionlifecycle/service_test.go`
  - session planning / launch orchestration now routes through `shared/serverapi/session_launch.go`, `shared/client/session_launch.go`, and `server/sessionlaunch`
  - the CLI planner now owns picker composition from `ProjectViewClient` summaries and sends explicit deterministic plan requests with `client_request_id`
  - `server/core` now owns a session-store registry so planning, lifecycle, and runtime prep resolve live sessions by opaque `session_id`
  - `server/sessionlaunch` now has direct duplicate-suppression coverage for new-session retries
- Recommended next cut:
  1. widen the interactive runtime-control API used by `shared/clientui.RuntimeClient` onto shared `serverapi` + `shared/client`
  2. expose the remaining live ask/approval answer path on the same shared boundary
  3. wire those new interactive surfaces through the real transport so external-daemon interactive mode can attach without embedded-only fallbacks

## Runtime-Control Mapping Notes

- `shared/clientui.RuntimeClient` methods currently still terminate in `cli/app/ui_runtime_client.go` against `*runtime.Engine` directly.
- Server-owned runtime operations that need a shared `serverapi` + `shared/client` surface next:
  - `SetSessionName`, `SetThinkingLevel`, `SetFastModeEnabled`, `SetReviewerEnabled`, `SetAutoCompactionEnabled`
  - `AppendLocalEntry`, `ShouldCompactBeforeUserMessage`
  - `SubmitUserMessage`, `SubmitUserShellCommand`, `CompactContext`, `CompactContextForPreSubmit`, `Interrupt`
  - `HasQueuedUserWork`, `SubmitQueuedUserMessages`, `QueueUserMessage`, `DiscardQueuedUserMessagesMatching`, `RecordPromptHistory`
- Frontend-local state that should remain frontend-owned even after runtime control moves server-side:
  - `uiModel.queued`
  - `pendingInjected`
  - input-lock bookkeeping and prompt-history draft reuse mechanics
- The next extraction should introduce a server runtime-control service over a runtime resolver (likely the existing runtime registry), then refactor `cli/app/ui_runtime_client.go` into a wrapper over shared read + control clients instead of a concrete engine wrapper.

## Resume Rules

1. Read `phase-3-todo.md` first.
2. Read `questions.md` second.
3. Check current git status.
4. Continue the highest-priority unchecked Phase 3 item.
5. Update these execution docs when assumptions or completion gates change.
