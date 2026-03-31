# App Server Migration: Boundary Map

Status: refined repo-grounded seam analysis

This document maps the current codebase onto the first intended frontend/server boundary.

It is not a final package design. It is the Phase 1 extraction starting point grounded in the current repo.

## Current Composition Paths

Interactive path today:

- `cli/builder/main.go:rootCommand`
  - parses CLI flags and calls `app.Run(...)`
- `cli/app/app.go:Run`
  - selects interactive auth flow and shared bootstrap
- `cli/app/bootstrap.go:bootstrapApp`
  - loads config, resolves workspace/container, creates `auth.Manager`, runs auth/onboarding gates, creates background shell manager/router, creates fast-mode state
- `cli/app/session_lifecycle.go:runSessionLifecycle`
  - owns the cross-session UI loop
- `cli/app/launch_planner.go:PlanSession` / `PrepareRuntime`
  - opens or creates `session.Store`, derives effective settings and enabled tools, prepares status config, creates runtime wiring
- `cli/app/runtime_factory.go:newRuntimeWiringWithBackground`
  - builds tool registry, ask broker, provider clients, reviewer client, `runtime.Engine`, and runtime event bridge
- `cli/app/ui_runtime_adapter.go`
  - projects `runtime.Event` and `runtime.ChatSnapshot` directly into Bubble Tea/TUI state

Headless path today:

- `cli/builder/main.go:runSubcommand`
  - parses run-only flags and calls `app.RunPrompt(...)`
- `cli/app/app.go:RunPrompt`
  - selects headless auth flow and shared bootstrap
- `cli/app/run_prompt.go:runPrompt`
  - acts as a thin frontend adapter over `shared/client.RunPromptClient`
- `cli/app/headless_prompt_server.go` -> `server/runprompt`
  - wrapper entrypoint into the server-owned headless launch/runtime composition and duplicate-suppression layer

This makes two things clear:

- `cli/builder/main.go` is already a thin CLI shell and should stay frontend-only.
- `cli/app` is the real monolithic composition root today.

## Tightened Composition Roots

The repo already suggests three composition roots, but they are collapsed into `cli/app`.

## 1. CLI / Frontend Root

Should own only:

- flag parsing and command routing in `cli/builder/main.go`
- mode selection in `cli/app/app.go`
- Bubble Tea startup, overlays, and command mapping in `cli/app/session_lifecycle.go` and `cli/app/ui*.go`

Must not own:

- session persistence access
- runtime creation
- tool registry creation
- auth state mutation beyond invoking a client/server boundary

## 2. Server Composition Root

Currently scattered across:

- `cli/app/bootstrap.go`
- `cli/app/launch_planner.go`
- `cli/app/runtime_factory.go`

This root should own:

- config-derived runtime settings
- `session.Store` open/create/list operations
- `auth.Manager` creation and readiness checks
- background shell manager/router ownership
- tool registry construction from `server/tools`
- LLM provider client construction from `server/llm`
- `runtime.Engine` construction from `server/runtime`

This is already server composition in substance; it is only living in the wrong package.

## 3. Frontend View-Model Root

Currently concentrated in:

- `cli/app/session_lifecycle.go`
- `cli/app/ui_runtime_adapter.go`
- `cli/app/ui_status.go`
- `cli/app/ui_processes.go`

This root should own:

- frontend navigation
- TUI read-model state
- overlay state and input handling
- projection from client/resource events into TUI messages

It should eventually depend on client DTOs and hydration views, not `runtime.Event`, `runtime.ChatSnapshot`, `session.Store`, or `shelltool.Manager`.

## Current Package Direction

## Strong Server-Side Candidates

These packages are already aligned with future server authority and should stay behind the boundary:

- `server/runtime`
  - owns the agent step loop and run authority via `runtime.Engine`
  - depends directly on `session.Store`, `llm.Client`, and `tools.Registry`
  - emits rich runtime-only events in `server/runtime/events.go`
- `server/session`
  - owns persisted session metadata and event log (`session.json`, `events.jsonl`)
  - exposes create/open/list/fork primitives that frontend code must eventually stop calling directly
- `server/tools`
  - owns model-visible tool contracts, runtime availability, transcript formatting, and local runtime handler selection
  - `runtime.Engine.buildRequest` already treats it as authoritative tool-contract state
- `server/llm`
  - owns provider clients, request/response model, provider capability contracts, and transport variants
- `server/auth`
  - owns auth state, refresh, startup gate, and authorization-header resolution

Repo-grounded conclusion: these packages already form the server-side core; the missing piece is the application-service layer in front of them.

## Strong Frontend-Side Candidates

These areas are already frontend-facing and should stay on the client side of the seam:

- `cli/builder/main.go`
  - CLI shell only; currently thin enough to keep as frontend entrypoint
- `cli/tui`
  - rendering surface and layout behavior
- `cli/app/ui*.go`
  - Bubble Tea state, layout, input handling, overlays, rendering helpers
- `cli/app/session_picker.go`
- `cli/app/auth_picker.go`
- `cli/app/auth_success_screen.go`
- `cli/app/mouse_sgr.go`
- `cli/app/terminal_bell.go`
- `cli/app/onboarding_*.go`

These files can stay where they are temporarily, but they should consume a client boundary rather than privileged runtime/session access.

## Mixed / Needs Splitting

- `cli/app/app.go`
  - good public entrypoints (`Run`, `RunPrompt`), but still reaches shared bootstrap directly
- `cli/app/bootstrap.go`
  - mixes server bootstrap (`auth.Manager`, background manager, fast mode) with frontend auth/onboarding interaction
- `cli/app/launch_planner.go`
  - mixes server launch decisions (`session.OpenByID`, `session.NewLazy`) with frontend session-picker and UI status setup
- `cli/app/run_prompt.go`
  - smallest end-to-end non-TUI workflow, but still reaches `runtime.Engine` directly
- `cli/app/session_lifecycle.go`
  - owns frontend navigation while also calling `session.ForkAtUserMessage`, `authManager.ClearMethod`, and `ensureAuthReady`
- `cli/app/auth_gate.go`
  - mixes server auth readiness with interactive browser/device UX and env-preference conflict resolution

## Highest-Risk Re-Coupling Hotspots

These are the places most likely to preserve the monolith behind a new transport wrapper.

## 1. Runtime Wiring In `cli/app/runtime_factory.go`

Why it is dangerous:

- `newRuntimeWiringWithBackground(...)` is already the server composition root in everything but name
- it constructs the tool registry, ask broker, background shell manager integration, provider client, reviewer client, `runtime.Engine`, and event bridge in one place
- `buildToolRegistry(...)` binds `server/tools` definitions to local runtime builders for `shell`, `exec_command`, `write_stdin`, `patch`, `ask_question`, `view_image`, and `multi_tool_use_parallel`

Required outcome:

- this logic must move behind the server boundary unchanged in authority
- frontend code must stop receiving `*runtime.Engine`, `*askBridge`, `*runtimeEventBridge`, or `*shelltool.Manager`

## 2. Launch Planning In `cli/app/launch_planner.go`

Why it is dangerous:

- `PlanBootstrap(...)` opens session storage to recover workspace root and continuation base URL from prior session metadata
- `PlanSession(...)` directly calls `session.OpenByID`, `session.ListSessions`, and `session.NewLazy`
- `PlanSession(...)` also returns `uiStatusConfig`, which means a persistence/runtime planning function already leaks frontend-specific view wiring
- `PrepareRuntime(...)` is the bridge from store/settings into runtime construction

Required outcome:

- split this into server launch use cases plus frontend-only selection UX
- do not let a future client call a transport wrapper that still returns `session.Store`, `config.Settings`, or `uiStatusConfig`

## 3. Headless Run Path In `cli/app/run_prompt.go`

Why it is dangerous:

- this is the smallest useful seam and therefore the easiest place to accidentally stop halfway
- the frontend adapter is now thin, but the surrounding launch/runtime composition is still only partially extracted from `cli/app`
- helper-level duplication between `cli/app` and `server/runprompt` can still blur ownership if it is left in place too long

Required outcome:

- keep this path as the first migrated workflow, with the frontend reaching only `shared/client` and the server-owned composition living under `server/runprompt`

## 4. Session Loop In `cli/app/session_lifecycle.go`

Why it is dangerous:

- it looks like frontend flow, but `resolveSessionAction(...)` still performs privileged operations directly
- it calls `session.ForkAtUserMessage(...)`
- it calls `boot.authManager.ClearMethod(...)` and `ensureAuthReady(...)`
- it assumes direct access to `session.Store` for input draft persistence

Required outcome:

- keep the Bubble Tea loop frontend-side, but route session/auth mutations through client-facing operations

## 5. UI Runtime Adapter In `cli/app/ui_runtime_adapter.go`

Why it is dangerous:

- it consumes `runtime.Event` directly
- it reads `runtime.ChatSnapshot` directly from `engine.ChatSnapshot()`
- it uses `session.ConversationFreshness` directly in frontend state
- this will become the main loophole for privileged runtime imports if not cut deliberately

Required outcome:

- replace runtime-native events and snapshots with transport-neutral event/resource DTOs before interactive extraction is considered complete

## 6. Status And Process Surfaces In `cli/app/ui_status.go`, `cli/app/ui_status_repository.go`, And `cli/app/ui_processes.go`

Why it is dangerous:

- `ui_status.go` is not a pure view; it collects auth state, git state, skill/environment inspection, token estimates, and remote subscription usage HTTP
- `ui_processes.go` is a frontend overlay built directly on `shelltool.Manager` and `shelltool.Snapshot`
- these files create strong pressure to keep "just one more direct runtime call" available to the CLI

Required outcome:

- treat them as deferred frontend knots
- when they migrate, they should consume typed server-backed reads and process resources rather than direct auth/runtime/shell-manager access

## 7. Auth Gate In `cli/app/auth_gate.go`

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
- it gives a hard proof point: `cli/builder/main.go` can stop reaching server internals first

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
- session repository built on `server/session`
- runtime factory from `runtime_factory.go`
- auth manager and startup readiness from `server/auth` plus the server side of `auth_gate.go`
- event publication currently modeled by `runtimeEventBridge`
- runtime execution authority in `server/runtime.Engine`

## Deferred Knots After The First Seam

These should not block the first transport-neutral extraction:

- Bubble Tea runtime projection in `cli/app/ui_runtime_adapter.go`
- interactive session navigation in `cli/app/session_lifecycle.go`
- status overlay and caches in `cli/app/ui_status*.go`
- process overlay and controls in `cli/app/ui_processes.go`
- interactive auth pickers and browser/device UX in `cli/app/auth_gate.go`, `auth_picker.go`, and `auth_success_screen.go`
- onboarding/import UX in `cli/app/onboarding_*.go`

They are important, but they are not the right first proof of the architecture.

## Recommended Phase 1 Package Direction

This is the file/package direction to aim for, not a final naming decree.

- keep server-only authority where it already lives:
  - `server/runtime`
  - `server/session`
  - `server/tools`
  - `server/llm`
  - `server/auth`
- extract a transport-neutral server application-service layer in front of them:
  - `shared/serverapi`
- require a client-facing abstraction even for embedded mode:
  - `shared/client`
- keep real wire protocol types/adapters for later:
  - future `shared/protocol` transport-envelope types only if the RPC layer needs a separate home
- split current `cli/app` ownership along file seams:
  - frontend shell and Bubble Tea UX: `app.go`, `session_lifecycle.go`, `ui*.go`, pickers, onboarding
  - server composition/bootstrap: code extracted from `bootstrap.go`, `launch_planner.go`, and `runtime_factory.go`

## First Extraction Candidate

Status update:

- The first Phase 1 slice has landed.
- `cli/app/run_prompt.go` is now a thin frontend adapter over `shared/client` and `shared/serverapi` request/result DTOs for the headless `builder run` path.
- `RunPromptRequest` now includes a required `client_request_id`, and the server-owned headless seam already performs process-local duplicate suppression keyed by request scope.
- The remaining gap for this extraction target is that the interactive-side bootstrap/planner/runtime-factory code in `cli/app` is still mixed and partially duplicated. The next extraction step is to keep moving launch-context resolution, session open/create, and runtime preparation into server-owned packages so the embedded client no longer depends on mixed app-private construction.

The first concrete extraction should wrap the current headless path built from:

- `cli/builder/main.go:runSubcommand`
- `cli/app/app.go:RunPrompt`
- `cli/app/run_prompt.go`
- `cli/app/launch_planner.go`
- `cli/app/runtime_factory.go`

Recommended shape:

- `cli/builder/main.go`
  - remains CLI-only and talks to a client boundary
- `cli/app/run_prompt.go`
  - shrinks to headless CLI presentation/orchestration over client use cases
- server-side launch/runtime code
  - moves behind the new server application-service layer

This is the smallest boundary with immediate product value and the least UI churn. The first adapter slice is in place; the remaining work is moving server composition out of `cli/app` rather than leaving it behind the thin frontend adapter.

## Phase 1 Success Condition

Phase 1 starts proving itself when the `builder run` path no longer reaches `runtime.Engine`, `session.Store`, `auth.Manager`, `tools.Registry`, or `llm.Client` directly.

Phase 1 is fully successful only when migrated CLI/frontend flows depend on the client-facing boundary rather than privileged imports, the embedded client bootstrap and launch/open/hydration composition are server-owned instead of living in `cli/app`, and no new direct frontend imports of server internals are introduced while the deferred interactive knots are still being untangled.
