# Goal Mode Implementation Plan

## Scope

Implement Builder goal mode from the locked decisions in `docs/dev/goal-prd.md` and `docs/dev/decisions.md`.

V1 must not add model-callable goal tools. Goal state is owned by Builder runtime/server surfaces. The model may only use visible shell CLI commands, and CLI commands must go through live server/runtime RPC rather than reading or mutating session files directly.

## Open Decision: External CLI Authority

Implementation is blocked until the `builder goal` auth/RPC shape is chosen.

Confirmed current code:

- Runtime-control mutations in `shared/serverapi/runtime_control.go` require `ControllerLeaseID`.
- TUI runtime calls in `cli/app/ui_runtime_client.go` own a controller lease and retry through `retryRuntimeControlCall`.
- `BUILDER_SESSION_ID` plumbing from main commit `9b54ad58` only identifies the caller session. It does not provide a controller lease or server endpoint.
- CLI goal commands must use live server/runtime RPC and must not read or mutate session store files directly.

Options to decide before CLI implementation:

- Add goal-specific runtime RPC methods that authenticate same-session shell callers through server-side caller/session context, not controller leases.
- Expose a scoped controller/runtime lease to model shell env. This is broader and riskier because it expands shell authority beyond session id.
- Split human CLI and agent CLI surfaces: TUI/user mutations stay lease-backed; agent shell gets narrow `show`/`complete` RPCs keyed by `BUILDER_SESSION_ID`.

Recommended v1: narrow goal-specific RPC authority for same-session shell callers, limited to `show` and confirm-gated `complete`; keep TUI/user mutations on existing controller leases.

## Workstream 1: Session Goal Domain

- [ ] Add session metadata shape for `goal_id`, `objective`, `status`, `created_at`, and `updated_at`.
- [ ] Add goal status enum/validation for `active`, `paused`, and `complete`.
- [ ] Add goal actor enum/validation for `user`, `agent`, and `system`.
- [ ] Add store-level mutation methods that atomically update metadata and append audit/projection events:
  - [ ] `goal_set` with full goal snapshot, actor, and optional replaced goal id.
  - [ ] `goal_status_updated` with full goal snapshot, actor, and previous status.
  - [ ] `goal_cleared` with previous goal snapshot and actor.
- [ ] Preserve objective text by trimming outer whitespace only; reject empty objective.
- [ ] Add tests for metadata persistence, event payloads, replacement id behavior, and resume-from-disk state.

Concrete integration points:

- `server/session/types.go`: extend `Meta` with one `Goal *GoalState` field plus typed `GoalStatus`, `GoalActor`, and snapshot structs. Keep current state in `session.json`; do not infer it by replaying `events.jsonl`.
- `server/session/store.go`: add store mutation methods beside existing metadata mutators like `SetName`, `SetParentSessionID`, and `SetWorktreeReminderState`. Use the store mutex, persist metadata, append goal event, and call `observePersistence` from the same public method.
- `server/session/event_log.go`: use existing append-only event machinery. Do not add boot-time goal hydration from events; event bootstrap currently tracks log state/compaction, not current feature state.
- `server/session/snapshot.go`: dormant/offline views read `Meta` and sometimes events. Goal state should be available from `Snapshot.Meta.Goal` without `ReadEvents()` for current status.

Tests to add:

- `server/session/store_test.go` or a new focused session goal test file: set/replace/status/clear persists `session.json`, appends exact `goal_*` payloads, preserves internal newlines, trims outer whitespace, rejects empty objective.
- Add reopen test through `session.Open` / `OpenByID` to prove current goal reads from metadata.
- Add a regression that current goal reads do not call `ReadEvents()` or require `events.jsonl` contents.

Pitfalls from existing architecture:

- `SnapshotFromStore` currently reads all events. That is acceptable for transcript snapshots but must not become the runtime current-goal source.
- `mutateAndPersist` only persists metadata. Goal methods need metadata plus event append, so a helper may be needed rather than layering `AppendEvent` after metadata with partial-failure ambiguity.
- `Summary` should not grow objective text unless a product decision says session pickers expose goal objectives.

Exit criteria:

- [ ] Session metadata can persist, reopen, replace, status-update, and clear goals without loading full `events.jsonl`.
- [ ] Goal event payloads match PRD exactly, including actor and full goal snapshots.
- [ ] Targeted session package tests pass.

## Workstream 2: Runtime API And Boundary Validation

- [ ] Add serverapi request/response DTOs for goal show/set/pause/resume/clear/complete.
- [ ] Add runtime control service methods for goal mutations. They must resolve live runtime by session id and fail with typed runtime-unavailable when target runtime is not live.
- [ ] Add client/remote/gateway plumbing for new RPC methods.
- [ ] Keep controller lease and client request id semantics consistent with existing runtime control methods.
- [ ] Implement engine-level goal operations that call session store methods and append/persist developer-message transcript entries from `prompts/goal/`.
- [ ] Add model-loop boundary validation: if an active goal can start model work and `ask_question` is disabled, fail like normal runtime/model error before request dispatch.
- [ ] Add tests for RPC validation, runtime unavailable, lease validation, idempotent retries where applicable, ask_question parity failure, and no direct DB access from CLI path.

Concrete integration points:

- `shared/serverapi/runtime_control.go`: add goal DTOs and `RuntimeControlService` methods. Mirror existing request shape: `ClientRequestID`, `SessionID`, `ControllerLeaseID` for TUI-driven mutations, plus explicit validation funcs.
- `shared/client/runtime_control.go`: extend `RuntimeControlClient` and loopback client.
- `shared/protocol/handshake.go`: add `runtime.goalShow`, `runtime.goalSet`, `runtime.goalPause`, `runtime.goalResume`, `runtime.goalClear`, `runtime.goalComplete` constants.
- `shared/client/remote.go`: add remote wrappers after RPC method semantics are fixed. Follow existing runtime-control wrapper style rather than adding a second transport path.
- `server/transport/gateway.go`: add `decodeAndHandle` cases in runtime-control section and guard each with `requireSessionInActiveProject(ctx, state, params.SessionID)`.
- `server/core/core.go`: runtime control service already composes as `runtimecontrol.NewService(runtimeRegistry, runtimeRegistry).WithControllerLeaseVerifier(sessionRuntimeService)`. New service methods should flow through this existing client.
- `server/runtimecontrol/service.go`: add memo fields and methods beside existing setters. Reuse `Service.resolve`, `requestmemo.Memo`, `requireControllerLease`, and `acquirePrimaryRun` where a goal operation starts model work.
- `server/runtime/engine.go`: add engine methods for `GoalShow/Set/Pause/Resume/Clear/Complete`. Mutations call `session.Store` goal methods and append model-visible developer messages from `prompts/goal/`.
- `server/runtime/step_executor.go: defaultStepExecutor.prepareModelTurn`: validate active-goal + `ask_question` enabled before request build/model dispatch. Existing request tool exposure is based on `Engine.Config.EnabledTools`, so parity can check for `toolspec.ToolAskQuestion`.
- `server/runtime/engine_request.go: buildRequestPlanWithExtraItems` and `server/runtime/worktree_reminder.go`: reference pattern for request-time injected meta messages. Goal nudge differs because PRD requires persisted transcript entry; use this as caution, not blind copy.

Tests to add:

- `server/runtimecontrol/service_test.go`: validation, runtime unavailable via nil resolver result, lease failure, each goal RPC calls engine method.
- `server/runtimecontrol/service_idempotency_test.go`: replay same `ClientRequestID` and equivalent request returns memoized result; mismatched replay errors like existing runtime-control calls.
- `server/transport/gateway_part2_test.go`: gateway smoke tests for new methods, project/session scoping, invalid params.
- New focused goal tests under `server/runtime`: ask-question parity fails before model request; developer-message transcript entry persisted; active goal state returned from engine.

Pitfalls from existing architecture:

- Existing runtime mutations require `ControllerLeaseID`. External `builder goal` from shell will have `BUILDER_SESSION_ID` but not necessarily a controller lease; resolve the open decision above before implementing CLI mutations.
- `Service.resolve` only returns live runtimes. That matches PRD. Do not auto-activate dormant sessions from goal CLI.
- Runtime-control submit methods use `primaryrun.Gate`; goal set/resume may start model work and must avoid racing active steps.

Exit criteria:

- [ ] Live runtime RPC can show/set/pause/resume/clear/complete goals through server boundaries.
- [ ] Runtime-unavailable is typed and returned when the discovered/default server does not host the session.
- [ ] Ask-question parity fails at model-loop boundary before request dispatch.
- [ ] Targeted runtimecontrol, serverapi, transport, and runtime tests pass.

## Workstream 3: Goal Prompt Assets

- [ ] Keep goal prompt files under `prompts/goal/`:
  - [ ] `set.md`
  - [ ] `nudge.md`
  - [ ] `pause.md`
  - [ ] `resume.md`
  - [ ] `clear.md`
  - [ ] `complete.md`
  - [ ] `agent_command_denied.md`
  - [ ] `complete_confirm_required.md`
- [ ] Embed prompt files in `prompts/embed.go` with render helpers for templates that need objective/status.
- [ ] Add regression test that `set.md`, `nudge.md`, and `resume.md` do not contain `--confirm` or completion-tripwire copy.
- [ ] Add tests that rendered goal prompts have no leftover placeholders.

Concrete integration points:

- `prompts/goal/*.md`: prompt files already exist in this branch.
- `prompts/embed.go`: goal embed constants and render helpers already exist for set/nudge/resume. Add helpers only when a prompt has structured placeholders.
- `prompts/embed_test.go`: extend current render tests with leak and placeholder regressions.

Tests to add:

- `prompts/embed_test.go`: assert `GoalCompleteConfirmRequiredPrompt` is only place containing `--confirm`; set/nudge/resume rendered output has no `{{`.
- Prompt rendering tests should use public render helpers, not duplicate template substitution logic.

Pitfalls from existing architecture:

- Hidden `--confirm` must not leak through help, docs, nudge, set, or resume prompts. Keep `complete_confirm_required.md` as sole source.
- Do not add tool definitions to `server/tools/definitions.go`; goal is CLI/runtime surface only.

Exit criteria:

- [ ] All v1 goal prompt files are embedded and render through helpers where placeholders exist.
- [ ] Hidden completion tripwire copy appears only in `complete_confirm_required.md`.
- [ ] Targeted prompt tests pass.

## Workstream 4: CLI `builder goal`

- [ ] Add `builder goal show|set|pause|resume|clear|complete`.
- [ ] Target session from `BUILDER_SESSION_ID` when present; otherwise require explicit `--session <id>`.
- [ ] Discover live runtime using existing app-server discovery/config. If discovered/default server does not host target session, fail live-runtime-unavailable.
- [ ] Never open or mutate session store files from CLI command implementation.
- [ ] Enforce agent-env permissions:
  - [ ] Allow `show`.
  - [ ] Allow `complete` only through hidden `--confirm` tripwire.
  - [ ] Deny `set`, `pause`, `resume`, and `clear` with `prompts/goal/agent_command_denied.md`.
- [ ] Keep hidden `--confirm` out of CLI help and docs intended for the model.
- [ ] Human/non-agent `complete` does not require `--confirm`.
- [ ] Add `show --json` with current goal/status only.
- [ ] Update `cli/builder/help.go` and public CLI docs when command exists.

Concrete integration points:

- `cli/builder/main.go`: root dispatch currently handles `run`, `project`, `attach`, `rebind`, `serve`, `service`, and `session-id` on main commit `9b54ad58`. Add `goal` as sibling subcommand.
- `cli/builder/help.go`: add public help for `goal`, but do not mention hidden `--confirm`.
- Main commit `9b54ad58` adds a `shared/sessionenv` package with `LookupBuilderSessionID` and `BuilderSessionID`; use it for session targeting after this branch contains that commit.
- `server/tools/shell/shellenv/env.go` from `9b54ad58`: shell env will call `EnrichForSession(base, sessionID)` so agent commands can detect `BUILDER_SESSION_ID`.
- `shared/client/remote.go`: CLI should load config and call `client.DialConfiguredRemote(ctx, cfg)` / project-scoped variant if needed. It must not import `server/session` or call `OpenByID`.

Tests to add:

- `cli/builder/main_test.go`: session targeting from `BUILDER_SESSION_ID`, explicit `--session`, unknown/no session errors, JSON/plain show output, permission matrix for agent env.
- Add a CLI test double for runtime-control client/dialer so tests prove no session store path is opened.
- Regression: help output does not contain `--confirm`; first agent `complete` without confirm prints `prompts.GoalCompleteConfirmRequiredPrompt`.

Pitfalls from existing architecture:

- `9b54ad58` exposes only session id, not controller lease. Use the open decision above to choose deliberate server/RPC auth for same-session shell commands.
- Existing app-server discovery identifies endpoint, not session ownership. If default/discovered server does not host session, return typed live-runtime-unavailable from runtime-control path.
- Agent detection should be structured from env (`BUILDER_SESSION_ID`/`AGENT=builder`), not inferred from command text.

Exit criteria:

- [ ] `builder goal ...` succeeds only through live runtime RPC and never opens session store files directly.
- [ ] Agent-env permission matrix is enforced.
- [ ] Hidden `--confirm` is absent from help/docs and only revealed by the confirm-required error.
- [ ] Targeted CLI tests pass.

## Workstream 5: TUI Slash Commands And Dashboard

- [ ] Add `/goal`, `/goal <objective>`, `/goal pause`, `/goal resume`, and `/goal clear`.
- [ ] Do not add `/goal complete` in v1.
- [ ] `/goal` with no goal prints: `No goal to manage yet. First, start a goal with <command>`.
- [ ] `/goal` with goal opens read-only alt-screen dashboard.
- [ ] Dashboard exposes pause/resume/clear actions; pause/resume refresh state in place, clear closes after mutation.
- [ ] Add alt-screen confirmation for replacement and active/running clear. No goal text editor in v1.
- [ ] While a model turn is running, accept only pause and clear; reject set/replace/resume.
- [ ] Ensure slash commands call runtime RPC/client methods, not session store directly.

Concrete integration points:

- `cli/app/commands/commands.go`: add `ActionGoal` and register `/goal` with `RunWhileBusy: true`; handler should parse no-arg/dashboard, objective, and `pause|resume|clear` subcommands into typed `commands.Result` fields rather than stringly parsing in UI.
- `cli/app/ui_input_slash_commands.go`: current generic guard blocks `m.busy && !command.RunWhileBusy`. Because `/goal` has mixed busy behavior, command must run while busy and `blockedDeferredSlashCommand` / goal handler must enforce operation matrix.
- `cli/app/ui_input_controller_commands.go`: route `ActionGoal` to a dedicated handler, likely in a new goal command file under `cli/app`, like `ActionWorktree` routes to `handleWorktreeCommand`.
- `cli/app/ui_runtime_control.go`: add `uiModel` wrappers for goal RPC calls, matching `setRuntimeFastModeEnabled`, `interruptRuntime`, etc.
- `cli/app/ui_runtime_client.go`: extend `sessionRuntimeClient` with goal methods. Use `retryRuntimeControlCall` so controller lease recovery matches existing mutations.
- `shared/clientui/runtime.go`: extend `RuntimeClient` and `RuntimeStatus` with goal DTO/status.
- `cli/app/ui_worktrees.go` and `cli/app/ui_worktree_commands.go`: reuse alt-screen destination and confirmation patterns. Goal dashboard should open immediately with cached state, hydrate/refresh asynchronously, be scrollable if content grows, and not mutate normal-buffer transcript.

Tests to add:

- `cli/app/commands/commands_test.go`: `/goal` parsing, command matrix, absence of `/goal complete`.
- `cli/app/ui_busy_commands_test.go`: currently-running model turn accepts pause/clear and rejects set/replace/resume.
- New goal command/controller tests under `cli/app`: dashboard empty hint, replacement confirm, clear confirm, RPC call paths.

Pitfalls from existing architecture:

- `m.busy` is UI state; active runtime run also arrives through `clientui.RunState.Busy`. Tests should cover event-driven state, not only direct field setup.
- No editor v1. Do not reuse worktree create text-input dialog for objective editing.
- Ongoing normal-buffer history is append-only; dashboard/confirm belongs in alt-screen per TUI rules.

Exit criteria:

- [ ] `/goal` command surface matches PRD and uses runtime client/RPC only.
- [ ] Dashboard and confirmation flows are non-slop alt-screen surfaces with tests for command outcomes.
- [ ] Currently-running model turn matrix is enforced in TUI.
- [ ] Targeted TUI command/controller tests pass.

## Workstream 6: Goal Loop Scheduling

- [ ] After `/goal <objective>`, create one transcript entry with dedicated ongoing text and developer detail text from `prompts/goal/set.md`; start goal work.
- [ ] When active and idle, immediately continue goal work by injecting `prompts/goal/nudge.md`.
- [ ] Queued user messages wait until goal is complete.
- [ ] Ctrl+C interrupts current turn, keeps persisted status `active`, and sets non-persisted runtime suspension until next user message/session resume.
- [ ] On TUI session resume with active goal, start idle, show `goal active`, and do not auto-start until next user message.
- [ ] Headless `builder run --continue <session> ...` fails when session has any goal and tells user to clear it first.
- [ ] Add tests for loop continuation, queue suppression, interrupt suspension, and headless failure.

Concrete integration points:

- `server/runtime/engine.go: SubmitUserMessage`: goal set should create first goal step through existing `stepLifecycle.Run` path so run-state/events stay consistent.
- `server/runtime/engine_queue_submission.go: SubmitQueuedUserMessages`: currently flushes pending injected user work. Active goal should suppress queued user work until goal status is complete/cleared.
- `server/runtime/engine.go: Interrupt`: add runtime-local goal suspension state here or in step lifecycle completion handling; persisted goal stays `active`.
- `server/runtime/run_snapshot.go` and `server/runtime/events.go`: run-state events already expose busy/running. Add goal-turn marker only if projection/status needs it; avoid duplicating run lifecycle.
- `server/runtime/step_executor.go: RunStepLoopWithOptions`: after assistant final/no-tool completion, scheduler must decide whether to continue goal with nudge, stop because status changed, or surface error.
- `server/runprompt/headless.go`: `headlessPromptLauncher.PrepareHeadlessPrompt` plans/open session before runtime creation. Check `plan.Store.Meta().Goal` here and fail before `prepareRuntime` registers live runtime.
- `cli/app/run_prompt.go` and `cli/app/headless_prompt_server.go`: surface server error cleanly in headless CLI output.

Tests to add:

- New focused goal loop tests under `server/runtime`: active goal nudge continuation, pause/complete/clear stop loop, user queue not flushed until complete.
- New focused interrupt tests under `server/runtime`: interrupt active goal leaves `Meta.Goal.Status == active` and runtime-local suspension prevents immediate continuation until next user message/resume.
- `server/runprompt/headless_test.go`: any goal state in meta causes headless `RunPrompt` failure before model call/runtime registration.

Pitfalls from existing architecture:

- Existing `SubmitQueuedUserMessages` retries while exclusive step busy. Goal loop must not create a hidden busy-wait or starve interrupts.
- Auto-continuation should use normal run lifecycle events so TUI spinner/status remains consistent.
- TUI resume with active goal must not auto-start. Keep persisted active goal separate from runtime-local "eligible to continue now" flag.

Exit criteria:

- [ ] Active goal auto-continues until paused/complete/clear/runtime error.
- [ ] Queued user messages wait until goal completion.
- [ ] Ctrl+C creates runtime-local suspension while persisted status stays `active`.
- [ ] Headless runs fail when a goal exists.
- [ ] Targeted runtime scheduling/headless tests pass.

## Workstream 7: Projection, Status, And Transcript UX

- [ ] Add goal state to runtime status/client UI projection.
- [ ] Status line shows `goal active`, `goal paused`, or `goal complete`.
- [ ] Spinner progress word for active goal turn is `goal`.
- [ ] Detail transcript shows exact developer message model received.
- [ ] Ongoing transcript shows compact dedicated ongoing text, including goal set and continuation notices.
- [ ] Add projection/reducer tests for goal events and local/developer transcript entries.

Concrete integration points:

- `shared/clientui/runtime.go`: add `Goal *GoalStatus`/similar DTO to `RuntimeStatus`.
- `server/runtimeview/projection.go: StatusFromRuntime`: map `engine.Goal()` or `engine.GoalStatus()` into `clientui.RuntimeStatus`.
- `server/runtimeview/projection.go: EventFromRuntime`: if adding `runtime.EventGoalChanged`, project it to `clientui.Event`.
- `shared/clientui/runtime_events.go`: reducer should patch cached runtime status/goal from goal events, similar context usage patching in `sessionRuntimeClient.observeRuntimeEventStatus`.
- `cli/app/ui_runtime_events.go`: ensure goal events update `uiModel` cached status if event reducer does not already carry whole status.
- `cli/app/ui_layout_rendering_status.go`: add `goal active|paused|complete` segment near mode/model. Progress word should become `goal` during goal turns, not during unrelated turns.
- `server/runtime/engine_message_ops.go`: `appendPersistedLocalEntryWithOngoingText` already supports a single stored entry with detail text plus compact ongoing text. Goal lifecycle entries can use this for UI-visible ongoing text; model-visible developer messages still require `appendMessage`.
- `server/runtime/transcript_message_visibility.go`: inspect before implementation if using `llm.Message.CompactContent` instead of local entries for developer messages.

Tests to add:

- `server/runtimeview/projection_test.go`: goal state appears in `RuntimeStatus`; goal event projects correctly if event exists.
- `shared/clientui/runtime_events_test.go`: reducer patches goal state.
- `cli/app/ui_runtime_status_test.go` plus focused statusline rendering coverage under `cli/app`: status line includes goal segment; progress label switches to `goal` only for goal turn.
- Runtime transcript tests: detail transcript contains exact developer prompt; ongoing transcript uses dedicated compact text from same lifecycle action.

Pitfalls from existing architecture:

- Existing local entries are UI-visible but not model-visible; developer messages are model-visible. PRD says lifecycle action appends structured event and persisted developer-message transcript entry, so implementation may need both message and local/projection data or a typed message compact field.
- Do not rewrite ongoing normal-buffer history when goal status changes; append or statusline-update only.
- Keep projection DTO source server-owned; TUI should not read session metadata directly.

Exit criteria:

- [ ] Runtime status and client UI projections expose goal state.
- [ ] Statusline/progress word behavior matches PRD.
- [ ] Ongoing/detail transcript entries render from the same persisted entry data.
- [ ] Targeted projection/status rendering tests pass.

## Workstream 8: Verification And Docs

- [ ] Unit/integration tests for each workstream before merging slices.
- [ ] Run targeted tests via `./scripts/test.sh`.
- [ ] Run full build via `./scripts/build.sh --output ./bin/builder`.
- [ ] Update PRD/decisions if implementation discovers design ambiguity.
- [ ] Update public docs only after user-facing behavior exists.

Exit criteria:

- [ ] All touched packages pass targeted tests.
- [ ] `./scripts/build.sh --output ./bin/builder` passes.
- [ ] Public docs are updated only after CLI/TUI behavior exists.

## Suggested Implementation Order

1. Session metadata/events and prompt embedding/regression tests.
2. Runtime API/RPC and engine goal operations.
3. CLI `builder goal` using live RPC.
4. TUI slash command thin wrapper over runtime API.
5. Goal loop scheduler and ask_question parity validation.
6. Status/dashboard/projection polish.

This order keeps authority and persistence correct before UI, and prevents accidental CLI DB mutation.
