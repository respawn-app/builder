# App Server Migration: Boundary Map

Status: refined repo-grounded seam analysis

This document maps the current codebase onto the first intended frontend/server boundary.

It is not a final package design. It is the Phase 1 extraction starting point grounded in the current repo.

## Current Composition Paths

Interactive path today:

- `cmd/builder/main.go:rootCommand`
  - parses CLI flags and calls `app.Run(...)`
- `internal/app/app.go:Run`
  - selects interactive auth flow and shared bootstrap
- `internal/app/bootstrap.go:bootstrapApp`
  - loads config, resolves workspace/container, creates `auth.Manager`, runs auth/onboarding gates, creates background shell manager/router, creates fast-mode state
- `internal/app/session_lifecycle.go:runSessionLifecycle`
  - owns the cross-session UI loop
- `internal/app/launch_planner.go:PlanSession` / `PrepareRuntime`
  - opens or creates `session.Store`, derives effective settings and enabled tools, prepares status config, creates runtime wiring
- `internal/app/runtime_factory.go:newRuntimeWiringWithBackground`
  - builds tool registry, ask broker, provider clients, reviewer client, `runtime.Engine`, and runtime event bridge
- `internal/app/ui_runtime_adapter.go`
  - projects `runtime.Event` and `runtime.ChatSnapshot` directly into Bubble Tea/TUI state

Headless path today:

- `cmd/builder/main.go:runSubcommand`
  - parses run-only flags and calls `app.RunPrompt(...)`
- `internal/app/app.go:RunPrompt`
  - selects headless auth flow and shared bootstrap
- `internal/app/run_prompt.go:runPrompt`
  - plans session, prepares runtime, then directly calls `runtimePlan.Wiring.engine.SubmitUserMessage(...)`

This makes two things clear:

- `cmd/builder/main.go` is already a thin CLI shell and should stay frontend-only.
- `internal/app` is the real monolithic composition root today.

## Tightened Composition Roots

The repo already suggests three composition roots, but they are collapsed into `internal/app`.

## 1. CLI / Frontend Root

Should own only:

- flag parsing and command routing in `cmd/builder/main.go`
- mode selection in `internal/app/app.go`
- Bubble Tea startup, overlays, and command mapping in `internal/app/session_lifecycle.go` and `internal/app/ui*.go`

Must not own:

- session persistence access
- runtime creation
- tool registry creation
- auth state mutation beyond invoking a client/server boundary

## 2. Server Composition Root

Currently scattered across:

- `internal/app/bootstrap.go`
- `internal/app/launch_planner.go`
- `internal/app/runtime_factory.go`

This root should own:

- config-derived runtime settings
- `session.Store` open/create/list operations
- `auth.Manager` creation and readiness checks
- background shell manager/router ownership
- tool registry construction from `internal/tools`
- LLM provider client construction from `internal/llm`
- `runtime.Engine` construction from `internal/runtime`

This is already server composition in substance; it is only living in the wrong package.

## 3. Frontend View-Model Root

Currently concentrated in:

- `internal/app/session_lifecycle.go`
- `internal/app/ui_runtime_adapter.go`
- `internal/app/ui_status.go`
- `internal/app/ui_processes.go`

This root should own:

- frontend navigation
- TUI read-model state
- overlay state and input handling
- projection from client/resource events into TUI messages

It should eventually depend on client DTOs and hydration views, not `runtime.Event`, `runtime.ChatSnapshot`, `session.Store`, or `shelltool.Manager`.

## Current Package Direction

## Strong Server-Side Candidates

These packages are already aligned with future server authority and should stay behind the boundary:

- `internal/runtime`
  - owns the agent step loop and run authority via `runtime.Engine`
  - depends directly on `session.Store`, `llm.Client`, and `tools.Registry`
  - emits rich runtime-only events in `internal/runtime/events.go`
- `internal/session`
  - owns persisted session metadata and event log (`session.json`, `events.jsonl`)
  - exposes create/open/list/fork primitives that frontend code must eventually stop calling directly
- `internal/tools`
  - owns model-visible tool contracts, runtime availability, transcript formatting, and local runtime handler selection
  - `runtime.Engine.buildRequest` already treats it as authoritative tool-contract state
- `internal/llm`
  - owns provider clients, request/response model, provider capability contracts, and transport variants
- `internal/auth`
  - owns auth state, refresh, startup gate, and authorization-header resolution

Repo-grounded conclusion: these packages already form the server-side core; the missing piece is the application-service layer in front of them.

## Strong Frontend-Side Candidates

These areas are already frontend-facing and should stay on the client side of the seam:

- `cmd/builder/main.go`
  - CLI shell only; currently thin enough to keep as frontend entrypoint
- `internal/tui`
  - rendering surface and layout behavior
- `internal/app/ui*.go`
  - Bubble Tea state, layout, input handling, overlays, rendering helpers
- `internal/app/session_picker.go`
- `internal/app/auth_picker.go`
- `internal/app/auth_success_screen.go`
- `internal/app/mouse_sgr.go`
- `internal/app/terminal_bell.go`
- `internal/app/onboarding_*.go`

These files can stay where they are temporarily, but they should consume a client boundary rather than privileged runtime/session access.

## Mixed / Needs Splitting

- `internal/app/app.go`
  - good public entrypoints (`Run`, `RunPrompt`), but still reaches shared bootstrap directly
- `internal/app/bootstrap.go`
  - mixes server bootstrap (`auth.Manager`, background manager, fast mode) with frontend auth/onboarding interaction
- `internal/app/launch_planner.go`
  - mixes server launch decisions (`session.OpenByID`, `session.NewLazy`) with frontend session-picker and UI status setup
- `internal/app/run_prompt.go`
  - smallest end-to-end non-TUI workflow, but still reaches `runtime.Engine` directly
- `internal/app/session_lifecycle.go`
  - owns frontend navigation while also calling `session.ForkAtUserMessage`, `authManager.ClearMethod`, and `ensureAuthReady`
- `internal/app/auth_gate.go`
  - mixes server auth readiness with interactive browser/device UX and env-preference conflict resolution

## Highest-Risk Re-Coupling Hotspots

These are the places most likely to preserve the monolith behind a new transport wrapper.

## 1. Runtime Wiring In `internal/app/runtime_factory.go`

Why it is dangerous:

- `newRuntimeWiringWithBackground(...)` is already the server composition root in everything but name
- it constructs the tool registry, ask broker, background shell manager integration, provider client, reviewer client, `runtime.Engine`, and event bridge in one place
- `buildToolRegistry(...)` binds `internal/tools` definitions to local runtime builders for `shell`, `exec_command`, `write_stdin`, `patch`, `ask_question`, `view_image`, and `multi_tool_use_parallel`

Required outcome:

- this logic must move behind the server boundary unchanged in authority
- frontend code must stop receiving `*runtime.Engine`, `*askBridge`, `*runtimeEventBridge`, or `*shelltool.Manager`

## 2. Launch Planning In `internal/app/launch_planner.go`

Why it is dangerous:

- `PlanBootstrap(...)` opens session storage to recover workspace root and continuation base URL from prior session metadata
- `PlanSession(...)` directly calls `session.OpenByID`, `session.ListSessions`, and `session.NewLazy`
- `PlanSession(...)` also returns `uiStatusConfig`, which means a persistence/runtime planning function already leaks frontend-specific view wiring
- `PrepareRuntime(...)` is the bridge from store/settings into runtime construction

Required outcome:

- split this into server launch use cases plus frontend-only selection UX
- do not let a future client call a transport wrapper that still returns `session.Store`, `config.Settings`, or `uiStatusConfig`

## 3. Headless Run Path In `internal/app/run_prompt.go`

Why it is dangerous:

- this is the smallest useful seam and therefore the most tempting place to ship a fake boundary
- it currently does the right high-level orchestration, but the critical line is still `runtimePlan.Wiring.engine.SubmitUserMessage(...)`
- it also reads dropped-event counts from the runtime event bridge and uses runtime logger details directly

Required outcome:

- keep this path as the first migrated workflow, but replace direct `runtime.Engine` access with client-facing use cases and DTOs

## 4. Session Loop In `internal/app/session_lifecycle.go`

Why it is dangerous:

- it looks like frontend flow, but `resolveSessionAction(...)` still performs privileged operations directly
- it calls `session.ForkAtUserMessage(...)`
- it calls `boot.authManager.ClearMethod(...)` and `ensureAuthReady(...)`
- it assumes direct access to `session.Store` for input draft persistence

Required outcome:

- keep the Bubble Tea loop frontend-side, but route session/auth mutations through client-facing operations

## 5. UI Runtime Adapter In `internal/app/ui_runtime_adapter.go`

Why it is dangerous:

- it consumes `runtime.Event` directly
- it reads `runtime.ChatSnapshot` directly from `engine.ChatSnapshot()`
- it uses `session.ConversationFreshness` directly in frontend state
- this will become the main loophole for privileged runtime imports if not cut deliberately

Required outcome:

- replace runtime-native events and snapshots with transport-neutral event/resource DTOs before interactive extraction is considered complete

## 6. Status And Process Surfaces In `internal/app/ui_status.go`, `internal/app/ui_status_repository.go`, And `internal/app/ui_processes.go`

Why it is dangerous:

- `ui_status.go` is not a pure view; it collects auth state, git state, skill/environment inspection, token estimates, and remote subscription usage HTTP
- `ui_processes.go` is a frontend overlay built directly on `shelltool.Manager` and `shelltool.Snapshot`
- these files create strong pressure to keep "just one more direct runtime call" available to the CLI

Required outcome:

- treat them as deferred frontend knots
- when they migrate, they should consume typed server-backed reads and process resources rather than direct auth/runtime/shell-manager access

## 7. Auth Gate In `internal/app/auth_gate.go`

Why it is dangerous:

- it currently owns both auth readiness checks and interactive browser/device/env-conflict UX
- that makes it easy to keep auth state management privileged in the frontend forever

Required outcome:

- separate server auth state/readiness from frontend auth method selection and callback UX

## First Transport-Neutral Boundary

The smallest high-value boundary is the current headless session/run workflow used by `builder run`.

That slice is the best first seam because:

- it is already a complete non-TUI flow
- it goes through the same core server packages as the interactive path
- it avoids prematurely untangling Bubble Tea event projection
- it gives a hard proof point: `cmd/builder/main.go` can stop reaching server internals first

## Recommended First Client-Facing Use Cases

These use cases should be expressed as transport-neutral application operations, not raw transport methods.

- `ResolveLaunchContext`
  - current source: `launch_planner.PlanBootstrap(...)`
  - responsibility: resolve canonical workspace/session continuation context from CLI inputs without exposing `session.Store`
- `OpenOrCreateSession`
  - current source: `launch_planner.openStore(...)` and `createSession(...)`
  - responsibility: open existing session or create a new one and return a client-safe session handle/summary
- `SubmitUserMessage`
  - current source: `run_prompt.go` plus `runtime.Engine.SubmitUserMessage(...)`
  - responsibility: submit a user prompt to the authoritative runtime for a session
- `GetSessionSnapshot`
  - current source: `runtime.Engine.ChatSnapshot()` and session metadata reads
  - responsibility: return transport-neutral session/read-model state instead of runtime-native structs
- `SubscribeSessionEvents`
  - current source: `runtimeEventBridge` and `runtime.Event`
  - responsibility: stream transport-neutral run/session events for CLI progress and future remote clients
- `InterruptRun`
  - current source: `runtime.Engine.Interrupt()`
  - responsibility: cancel the active run without exposing `runtime.Engine`

Boundary rule for these use cases:

- request/response/event types must not expose `runtime.Event`, `runtime.ChatSnapshot`, `session.Store`, `auth.Manager`, `tools.Registry`, or `llm.Client`

## Server-Internal Collaborators Behind The First Seam

The first seam does not need new domain logic; it needs a clean server-facing facade over logic that already exists.

Behind the new boundary, keep these collaborators server-owned:

- config/settings resolver from `bootstrap.go` and `app.go`
- session repository built on `internal/session`
- runtime factory from `runtime_factory.go`
- auth manager and startup readiness from `internal/auth` plus the server side of `auth_gate.go`
- event publication currently modeled by `runtimeEventBridge`
- runtime execution authority in `internal/runtime.Engine`

## Deferred Knots After The First Seam

These should not block the first transport-neutral extraction:

- Bubble Tea runtime projection in `internal/app/ui_runtime_adapter.go`
- interactive session navigation in `internal/app/session_lifecycle.go`
- status overlay and caches in `internal/app/ui_status*.go`
- process overlay and controls in `internal/app/ui_processes.go`
- interactive auth pickers and browser/device UX in `internal/app/auth_gate.go`, `auth_picker.go`, and `auth_success_screen.go`
- onboarding/import UX in `internal/app/onboarding_*.go`

They are important, but they are not the right first proof of the architecture.

## Recommended Phase 1 Package Direction

This is the file/package direction to aim for, not a final naming decree.

- keep server-only authority where it already lives:
  - `internal/runtime`
  - `internal/session`
  - `internal/tools`
  - `internal/llm`
  - `internal/auth`
- extract a transport-neutral server application-service layer in front of them:
  - `internal/serverapi` or equivalent
- require a client-facing abstraction even for embedded mode:
  - `internal/client`
- keep real wire protocol types/adapters for later:
  - `internal/protocol`
- split current `internal/app` ownership along file seams:
  - frontend shell and Bubble Tea UX: `app.go`, `session_lifecycle.go`, `ui*.go`, pickers, onboarding
  - server composition/bootstrap: code extracted from `bootstrap.go`, `launch_planner.go`, and `runtime_factory.go`

## First Extraction Candidate

Status update:

- The first Phase 1 slice has landed.
- `internal/app/run_prompt.go` is now a thin frontend adapter over `internal/client` and `internal/serverapi` request/result DTOs for the headless `builder run` path.
- `RunPromptRequest` now includes a required `client_request_id`, but duplicate suppression semantics are not implemented yet; the request-shape contract exists before replay protection.
- The remaining gap for this extraction target is that the authoritative launch/runtime composition still lives in `internal/app` via the current bootstrap/planner/runtime-factory code. The next extraction step is to move launch-context resolution, session open/create, and runtime preparation into a server-owned package so the embedded client no longer depends on mixed app-private construction.

The first concrete extraction should wrap the current headless path built from:

- `cmd/builder/main.go:runSubcommand`
- `internal/app/app.go:RunPrompt`
- `internal/app/run_prompt.go`
- `internal/app/launch_planner.go`
- `internal/app/runtime_factory.go`

Recommended shape:

- `cmd/builder/main.go`
  - remains CLI-only and talks to a client boundary
- `internal/app/run_prompt.go`
  - shrinks to headless CLI presentation/orchestration over client use cases
- server-side launch/runtime code
  - moves behind the new server application-service layer

This is the smallest boundary with immediate product value and the least UI churn. The first adapter slice is in place; the remaining work is moving server composition out of `internal/app` rather than leaving it behind the thin frontend adapter.

## Phase 1 Success Condition

Phase 1 starts proving itself when the `builder run` path no longer reaches `runtime.Engine`, `session.Store`, `auth.Manager`, `tools.Registry`, or `llm.Client` directly.

Phase 1 is fully successful only when migrated CLI/frontend flows depend on the client-facing boundary rather than privileged imports, the embedded client bootstrap and launch/open/hydration composition are server-owned instead of living in `internal/app`, and no new direct frontend imports of server internals are introduced while the deferred interactive knots are still being untangled.
