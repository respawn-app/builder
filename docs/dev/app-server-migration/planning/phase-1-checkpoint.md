# App Server Migration: Phase 1 Checkpoint

Status: complete

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
- Introduced `server/sessioncontrol` as the server-owned home for interactive session selection and transition orchestration, so open/create/resume/logout/fork decision rules now go through one server package while `cli/app` only supplies picker and reauth UX hooks.
- Introduced `server/startup` as the server-owned home for embedded startup request assembly and auth-ready reentry, so `cli/app/bootstrap.go` and `cli/app/auth_gate.go` now act as thin adapters over a server-owned startup façade instead of owning reusable startup/auth composition logic.
- Introduced `server/onboarding` as the server-owned home for onboarding policy, so the “settings file exists / headless writes defaults / interactive requires auth state and reloads config” rules now live on the server side while `cli/app/onboarding_run.go` only owns the Bubble Tea onboarding flow itself.
- Introduced `server/runtimewire` as the server-owned home for runtime preparation, local tool registry construction, background-event routing, outside-workspace approvals, and runtime event bridging; `cli/app/runtime_factory.go` and `server/runprompt/headless.go` now delegate to it instead of owning those implementations directly.
- Replaced the frontend-facing embedded-server seam with a frontend-shaped facade: `cli/app` launch planning, runtime preparation, and lifecycle transition handling now ask the embedded server to perform those operations instead of pulling raw `auth.Manager`, fast-mode, or background-process handles across the boundary.
- Introduced `shared/clientui` plus `server/runtimeview` as the first client-facing UI projection seam: the TUI runtime adapter now consumes projected UI DTOs instead of reading `runtime.Event` and `runtime.ChatSnapshot` directly in its main update path.
- Tightened that first UI seam so the `uiModel` event channel path now consumes projected `shared/clientui.Event` values directly, and client-facing tool-call metadata no longer aliases mutable server transcript structures.
- Replaced the TUI's concrete `*runtime.Engine` dependency with a frontend runtime interface inside `cli/app`: the UI model, submission flow, and status collector now depend on a loopback adapter boundary rather than a concrete runtime object.
- Moved that interactive runtime control/read contract into `shared/clientui`, leaving `cli/app` with only the loopback adapter implementation.
- Introduced `shared/clientui.RuntimeStatus` as the first bundled interactive read model beyond event/chat projection, and migrated status-line, slash-availability, `/back`, conversation-freshness, and status-collector reads onto that snapshot instead of a fanout of loopback getter calls.
- Introduced `shared/clientui.ProcessClient` plus `BackgroundProcess` as the first non-runtime interactive read surface, and migrated process-list hydration, status-line process counts, and process log-path lookups onto shared process snapshots instead of direct `shelltool.Manager.List()` reads in UI code.
- Introduced `shared/clientui.RuntimeSessionView` as the first bundled session/conversation hydration surface, and migrated runtime conversation sync to hydrate session metadata, conversation freshness, and transcript state from one shared view instead of pairing `Status()` with a separate chat snapshot read.
- Normalized the remaining interactive runtime control paths in `cli/app` around model helpers backed by `shared/clientui.RuntimeClient`, so command toggles, submission/compaction, queue draining, prompt-history persistence, and interrupt flows no longer branch directly on loopback runtime calls scattered through the UI controller files.
- Moved the remaining interactive runtime event state transitions out of `cli/app` into `shared/clientui.ReduceRuntimeEvent(...)`, so busy/compaction/reviewer state, user-message flush reconciliation, background completion notices, and reasoning-status header derivation are now shaped by a shared client-facing reducer rather than being re-derived inside the Bubble Tea adapter.
- Added a projected UI test helper and migrated representative TUI suites onto `NewProjectedUIModel(...)`, including the runtime-adapter, status, alt-screen, clipboard, diff-render, compaction-resume, render-diagnostic, layout-seam, ask-deferral, and mode-flow coverage.
- Drained the remaining non-monolithic UI suites off the compatibility constructor, including native-history, native-scrollback integration, slash-command picker, busy-command, scroll-key, session-lifecycle, mode-transition, and rollback-benchmark coverage.
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
- Interactive session selection and transition resolution now also go through a server-owned controller, leaving the frontend with session picker rendering and auth/onboarding interaction only.
- Embedded startup request mapping and auth-ready reentry now also go through a server-owned startup façade, leaving the frontend with auth/onboarding UX handlers only.
- Onboarding policy now also goes through a server-owned package, leaving the frontend with only the interactive onboarding flow implementation.
- The frontend no longer reaches into the embedded server for raw privileged handles during launch/runtime/session lifecycle orchestration; that seam is now a frontend-shaped facade rather than a bag of server-native dependencies.
- The first TUI adapter path now consumes client-facing projected UI DTOs instead of raw runtime-native event/snapshot structs.
- The TUI control/read path now also depends on a frontend runtime interface rather than a concrete `*runtime.Engine`, with the concrete loopback adapter isolated to one file.
- That interactive control/read path is now defined in a shared client-facing package rather than locally inside `cli/app`.
- The first bundled interactive read surface now exists: `cli/app` reads runtime status through `clientui.RuntimeStatus` in the main UI/status paths instead of directly mirroring a long list of engine getters.
- Process overlay hydration now also reads through a shared client-facing process surface instead of treating `shelltool.Snapshot` as the UI read model.
- Conversation re-sync now also reads through a shared client-facing session view instead of composing separate transcript and freshness reads by hand.
- Interactive UI command/submission flows now also depend on one model-level runtime control seam instead of treating `m.engine` as a special loopback object in each controller file.
- Interactive runtime event application now also depends on a shared client-facing reducer instead of frontend-local event-transition logic in `ui_runtime_adapter.go`.
- Direct `engine` references inside `cli/app` are now confined to the deliberate loopback adapter implementation in `ui_runtime_client.go`; the UI-side control/read helpers call through model-level runtime seams instead of reaching into loopback runtime methods ad hoc.
- The full existing UI characterization surface now exercises the projected/shared constructor directly.
- `NewProjectedUIModel(...)` is now the only UI constructor entrypoint in `cli/app`; the engine-shaped compatibility wrapper has been deleted rather than retained as long-term API debt.
- Repo-wide search now shows no remaining `NewUIModel(...)` callers in `cli/app`.
- `scripts/check-no-legacy-ui-constructor.sh`, invoked by `./scripts/test.sh`, now keeps that constructor-removal milestone from silently regressing.
- Runtime preparation and local runtime/tool wiring now also have one server-owned implementation shared by both interactive and headless flows.

Current limitations:

- Bubble Tea auth/onboarding screens, terminal bell behavior, and direct background-process control remain frontend-owned adapters by design in Phase 1. They no longer own server policy or runtime state, but they are not yet transport-neutral client SDK surfaces.
- The current duplicate suppression is process-local, scoped to the embedded server boundary, and retained in memory for a bounded 10-minute window; broader protocol-wide idempotency scope, durability, and retention policy for future server methods remain Phase 2 work.
- The full-suite proof gate still includes a flaky native scrollback test (`TestNativeFinalizeDoesNotBlinkDuplicateTailTokens`): it failed once during this checkpoint, passed immediately in isolation, and the subsequent full rerun was green. Treat it as existing test instability unless it starts reproducing under focused changes in the native transcript path.

## Phase 1 Exit Gate

- `builder run` now goes through a transport-neutral client/service seam with idempotent request shape and server-side duplicate suppression.
- Interactive launch/open/hydration policy now routes through server-owned startup/session/onboarding packages rather than being authored directly inside `cli/app`.
- The interactive UI consumes client-facing runtime status/session/process read models and a shared runtime-event reducer; direct concrete-engine access in frontend code is confined to the deliberate loopback adapter.
- Full repo tests and the production build are green with this boundary in place.

## Phase 2 Entry Point

- Define the durable resource model for project, session, run, process, approval, and ask identities.
- Add typed hydration views and stream semantics that support multi-client attach/reconnect beyond the current loopback CLI path.
- Generalize idempotency, replay/gap handling, and process/output retention beyond the current `RunPrompt` duplicate-suppression slice, including a protocol-level retention/durability contract for `client_request_id` replay.
