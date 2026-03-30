# App Server Migration: Behavior Preservation

Status: required proof surface

This migration claims that existing product functionality will be preserved.

That claim is only credible if it is backed by an explicit compatibility inventory and black-box acceptance tests. This file defines the minimum proof obligation.

## Scope

Preservation means preserving product capability, not exact TUI layout or slash-command syntax as an architectural primitive.

Allowed to change:

- rendering details,
- protocol shape,
- session picker UX,
- command implementation mechanics,
- whether a capability is reached through one RPC or multiple.

Not allowed to regress:

- ability to complete the same user-visible workflows,
- durable session continuity,
- asks and approvals,
- background process visibility and control,
- current review and init flows,
- behavior under frontend disconnect,
- ability to resume existing persisted sessions.

## Command-Surface Preservation

The built-in slash-command compatibility surface is defined in `command-ownership.md` and currently includes:

- `/exit`
- `/new`
- `/resume`
- `/logout`
- `/compact`
- `/name`
- `/thinking`
- `/fast`
- `/supervisor`
- `/autocompaction`
- `/status`
- `/ps`
- `/back`
- `/review`
- `/init`
- `/prompt:<name>` style file-backed prompt commands
- unknown slash-style input falling back to prompt submission

Repo cross-check result:

- The slash-command inventory is complete for the current interactive CLI once file-backed `/prompt:<name>` commands are counted.
- `internal/app/session_lifecycle.go` installs `commands.NewDefaultRegistryWithFilePrompts(...)`, so the live command surface is `NewDefaultRegistry()` built-ins plus discovered file-backed prompt commands.
- The adjacent UI/controller files inspected for this workstream do not register extra slash commands. They only add picker visibility rules, busy-state gating, queue or defer semantics, overlays, and lifecycle transitions.

Any newly discovered command or behavior must be added to the compatibility inventory before implementation starts for that area.

## Workflow Inventory

The migration must preserve at least the following end-to-end workflows:

| Workflow | Preservation Requirement | Proof Direction |
| --- | --- | --- |
| Create new session | User can create a session within a project and begin work immediately. | Black-box client flow against the server boundary. |
| Resume existing session | User can list and resume sessions within a project. | Black-box client flow using typed reads and attach. |
| Prompt submission | User can submit normal prompts and receive durable transcript results. | Protocol-level submit plus transcript assertions. |
| Unknown slash fallback | Unknown slash input still reaches the model as normal user input. | CLI and black-box client characterization. |
| Slash picker and partial-match execution | `/` opens the picker, arrow keys navigate matches, `Tab` autocompletes with a trailing space, and `Enter` executes the currently selected exact or partial match. | CLI characterization around `ui_slash_command_picker.go` behavior. |
| Slash-command normalization | Leading whitespace before the slash and whitespace immediately after `/` are normalized the same way as today. | CLI characterization for parse and execution parity. |
| Built-in `/review` | Frontend can create linked child session and start review prompt flow. | Child-session lineage plus initial submission test. |
| Built-in `/init` | Frontend can start init prompt flow in a fresh session. | Fresh-session prompt flow test. |
| File-backed prompt commands | Frontend-local prompt command expansion still works without server-side slash parsing. | CLI-side characterization plus structured submission assertion. |
| File-backed prompt discovery | Workspace and global prompt directories preserve precedence, filename normalization, top-level `.md` filtering, empty-file skipping, and `$ARGUMENTS` substitution. | CLI characterization around `NewDefaultRegistryWithFilePrompts`. |
| Rename session | Session title updates persist and remain visible across attach/resume. | Metadata mutation and reload test. |
| Busy-safe command behavior | Commands currently allowed while busy remain supported or are deliberately reclassified. | Characterization tests against active-run state. |
| Busy-blocked command behavior | Commands currently blocked while busy still fail or defer explicitly rather than silently misbehaving. | Characterization tests with active run. |
| Busy queued-command behavior | Queue-submit keys still queue exact known slash commands for post-turn drain, preserve drain order, and keep unknown slash input on the prompt-submission path. | Characterization tests around `ui_input_queue.go`. |
| Busy queue stop conditions | Auto-drain still stops when a queued action leaves non-empty input, opens an overlay, starts a run, asks a question, or exits. | CLI characterization for queue-drain control flow. |
| Status inspection | Frontend can render the equivalent of current `/status` from typed reads. | Read-model test with active and idle sessions. |
| Status overlay lifecycle | `/status` opens immediately into detail mode, records prompt history without blocking open, supports scrolling, closes with `Esc/q`, and preserves the current interrupt-vs-quit `Ctrl+C` behavior while open. | CLI characterization plus black-box client flow. |
| Status progressive refresh | Status content can seed cached/base data and progressively fill auth, git, and environment sections while rendering loading states and warnings. | Read-model and cache characterization. |
| Process inspection and control | Frontend can list, inspect, stream, and control background processes. | Process resource and output-stream tests. |
| Process overlay lifecycle | Bare `/ps` opens a dedicated detail overlay, owns transcript-mode transitions while open, refreshes periodically, keeps selection stable by process id, and ignores transcript-toggle keys until closed. | CLI characterization for overlay behavior. |
| Process sub-actions | `/ps kill|inline|logs <id>` preserve current user-visible effects, including inline paste into the input buffer, log opening fallback to `$VISUAL`/`$EDITOR`, and local status notices. | Process resource plus CLI characterization. |
| Approval and ask flows | Guarded operations pause, surface request state, and resume deterministically after response. | Multi-client race tests. |
| Fork and lineage | Child sessions retain parent linkage and remain navigable by the frontend. | Lineage metadata and attach tests. |
| Back-navigation teleport | `/back` still navigates to the parent session, seeds the parent draft from the latest committed final assistant answer when appropriate, preserves an existing parent draft, and never auto-submits on reopen. | Lineage and draft-handoff characterization. |
| Logout continuity | `/logout` still clears auth, runs re-auth, and resumes the same session when one is already attached. | Auth and session lifecycle characterization. |
| New-session continuity | `/new` still creates a child-linked session handoff and does not kill already-running background processes. | Session lifecycle and process characterization. |
| Headless continuity | Active work continues if the frontend disconnects. | Crash or disconnect test during active run. |
| Existing session adoption | Pre-migration persisted sessions remain resumable. | Fixture-based adoption test. |

## Busy-State Compatibility Baseline

From the current registry, UI controller flow, and tests, the migration must preserve this split unless deliberately changed and documented:

| Concern | Current Owner | Current Behavior |
| --- | --- | --- |
| Busy-safe registry bit | `internal/app/commands/commands.go` | `RunWhileBusy=true` only for `/name`, `/thinking`, `/supervisor`, `/autocompaction`, `/status`, and `/ps`. Everything else defaults to not-run-safe. |
| Busy `Enter` path | `internal/app/ui_input_slash_commands.go` | Exact known commands with `RunWhileBusy=false` clear the input and show `cannot run /<name> while model is working`. They do not queue. |
| Busy immediate execution | `internal/app/ui_input_controller_commands.go` | `/name`, `/thinking`, `/supervisor`, `/autocompaction`, `/status`, and `/ps` execute immediately while the run stays busy. `/supervisor` changes can affect in-flight run completion. |
| Busy queue-submit path | `internal/app/ui_input_slash_commands.go` + `internal/app/ui_input_queue.go` | Queue-submit keys can still queue exact known slash commands for post-turn drain even if `RunWhileBusy=false`. Drain later re-dispatches them as commands, not plain prompts. |
| Deferred queue rejection | `internal/app/ui_input_slash_commands.go` | Queueing is rejected early for `/back` without a parent session, unavailable `/fast`, and `/ps <action>` when no background manager exists. |
| Picker visibility vs execution | `internal/app/ui_slash_command_picker.go` | The picker hides `/fast` when unavailable and `/back` when there is no parent session, but exact typed commands still parse and execute or fail through the normal path. |
| Queue auto-drain stop conditions | `internal/app/ui_input_queue.go` | Auto-drain stops when a queued action starts a run, exits, opens a non-main input mode, shows an ask, or leaves non-empty input. `/ps inline <id>` is a current example: it pastes output into input and leaves later queued prompts pending. |

Operational baseline implied by that split:

- Busy-safe on `Enter`: `/status`, `/ps`, `/name`, `/thinking`, `/supervisor`, `/autocompaction`.
- Busy-blocked on `Enter`: `/fast`, `/compact`, `/new`, `/resume`, `/logout`, `/exit`, `/back`, `/review`, `/init`, file-backed `/prompt:<name>` commands, and starting another primary run.
- Busy-queueable through queue-submit keys: exact known slash commands, including commands that are blocked on `Enter`, unless rejected by the deferred queue checks above.

The new architecture may reimplement this behavior differently, but it must not erase the distinction accidentally.

## Gaps Closed By This Cross-Check

The previous preservation matrix under-described several current user-visible behaviors that are now explicitly in scope:

- slash-picker discovery, normalization, partial-match execution, and autocomplete behavior,
- file-backed prompt discovery rules, not just prompt expansion,
- the split between busy `Enter` behavior and busy queue-submit behavior,
- `/status` overlay lifecycle and progressive loading behavior,
- `/ps` overlay lifecycle, refresh loop, and sub-action side effects,
- `/back` draft-teleport semantics,
- `/logout` re-auth continuity and `/new` background-process continuity.

## Black-Box Acceptance Matrix

The migration is not proven until the acceptance suite can demonstrate all of the following through the real client boundary:

- CLI against embedded server mode
- CLI against external daemon mode
- second client attached to same session
- prompt submit, interrupt, resume, and transcript hydration
- approval and ask races
- process list, inspect, logs, and kill flows
- review child-session flow
- disconnect and reconnect with rehydrate
- existing persisted session adoption
- explicit gap handling for slow subscribers
- duplicate request retry without duplicate side effects

At least one non-CLI test client must exist for this suite. Otherwise the CLI remains too privileged to falsify the architecture claim.

## Acceptance Harness Outline

The first harness target should be the current headless seam in `internal/app/run_prompt.go` because it already avoids Bubble Tea and goes through session planning, runtime wiring, and `runtime.Engine.SubmitUserMessage(...)` directly.

The future acceptance suite should be structured as one shared scenario suite parameterized by execution mode:

- embedded mode: start the app server in-process and talk to it through the client boundary
- external-daemon mode: start the daemon as a child process and talk to it through the same client boundary

Only server startup differs between those modes. The scenario steps, assertions, and client helpers must stay identical. If a scenario needs CLI-only setup or reads `internal/runtime`, `internal/session`, or `internal/app/ui*` directly, it does not count as privilege-removal proof.

Recommended harness layers:

1. `AcceptanceTarget`: boot/shutdown wrapper for embedded and external targets.
2. `TestClient`: transport-neutral client used by every acceptance case.
3. `ScriptedServerFixture`: deterministic fake model/tool fixture on the server side for asks, long-running work, background processes, and retries.
4. `DurabilityAsserts`: typed transcript/session/process assertions that reload from the client boundary instead of from CLI state.
5. `ConnectionControl`: helpers for disconnect, reconnect, duplicate retry, and slow-subscriber scenarios.

## Minimum Non-CLI Test Client

The minimum non-CLI client needed for the first proof wave is intentionally smaller than the current CLI. It does not need slash-command parsing, Bubble Tea rendering, picker flows, or terminal history assertions.

It does need to support all of the following:

- create a session for a workspace
- list sessions and attach to an existing session by ID
- submit a prompt or typed workflow request and await a terminal result
- read transcript/history/session metadata after attach or reconnect
- observe coarse run lifecycle/events needed for completion, interruption, background updates, and ask/approval state
- interrupt active work
- receive and answer asks/approvals deterministically
- list, inspect, and kill background processes for a session
- disconnect and reconnect without losing the ability to rehydrate state
- retry a request with a stable request identity so duplicate side effects can be detected or prevented

For the first extraction seam, token-by-token rendering parity is not required. The current headless path only exposes a coarse progress stream via `writeRunProgressEvent(...)`, and the durable transcript remains the source of truth.

## First Acceptance Wave

The first acceptance wave should prove that the CLI is no longer privileged before it tries to cover every CLI workflow.

| Case | Must run in embedded and external mode | Why it is first-wave | Minimum client abilities |
| --- | --- | --- | --- |
| Create session, submit prompt, await final result, reload transcript | Yes | Direct replacement for today's `RunPrompt(...)` path. | Create session, submit, await completion, read transcript/meta. |
| Resume existing session by ID and continue work | Yes | Proves resume is boundary-driven rather than picker/UI-driven. | List or attach by session ID, submit again, hydrate transcript. |
| Attach a second client to the same session | Yes | Proves multiple frontends can rehydrate without privileged in-process state. | Attach existing session, read run state/events, reload transcript. |
| Interrupt active run and reconnect | Yes | Covers active-run lifecycle without relying on TUI state. | Submit long-running work, interrupt, disconnect/reconnect, inspect final state. |
| Ask/approval pause and deterministic resume | Yes | Current headless CLI cannot answer asks, so this is the first mandatory non-CLI-only proof. For the current monolith this should be read as live-process queued behavior only, not as evidence that pending ask/approval state already survives restart. | Observe pending ask, answer it, verify one resume path. |
| Background process list/inspect/kill across reconnect | Yes | Current repo already has owner-session routing and reconnect-sensitive behavior that must survive extraction. | Start process, list/inspect, reconnect, kill, verify final state. |
| Slow subscriber gap handling with transcript rehydrate | Yes | Current `runtimeEventBridge` drops under pressure; the boundary must make that recoverable. | Subscribe, lag intentionally, detect gap, reload transcript/read models. |
| Duplicate retry without duplicate side effects | Yes | Required to prove request identity lives at the boundary, not in the CLI process. | Retry submit/approval after timeout/disconnect, assert one durable outcome. |

The following workflows still belong in the full preservation suite, but they do not need to block the first privilege-removal proof:

- CLI unknown-slash fallback
- file-backed prompt command expansion
- detailed TUI status/detail/ongoing rendering parity
- existing persisted session adoption

Those remain important, but they are CLI characterization concerns until they are expressed as typed frontend actions.

`existing persisted session adoption` is intentionally second-wave rather than first-wave. It is still a release-blocking preservation requirement, but it should follow immediately after the core boundary proof so fixture compatibility is validated against a boundary that already exists.

## Current Coverage Seeds And Gaps

Best current seed coverage for the future acceptance harness:

- `internal/app/launch_planner_test.go` for headless session creation, selected-session resume, and continuation context
- `internal/app/runtime_event_bridge_test.go` for lossy event-stream characterization
- `internal/app/runtime_factory_test.go` for background-process ownership, no-retroactive-notice behavior, and active-session routing
- `internal/app/run_prompt_test.go` for the headless ask prohibition and progress-event filtering
- `internal/runtime/engine_test.go` and `internal/runtime/engine_queue_submission_test.go` for deterministic fake model behavior and queued/retried run semantics
- `internal/session/store_test.go` plus `internal/session/fork.go` for durable fixture seeding, resume, and lineage state

Concrete helper patterns worth reusing when the harness is implemented:

- `fakeClient` and related stream fakes in `internal/runtime/engine_test.go` for deterministic model/run scripting
- `busyToggleFakeClient` in `internal/app/runtime_factory_test.go` for active-run and background-notice timing cases
- `session.Create(...)`, `session.NewLazy(...)`, `Store.AppendEvent(...)`, and `Store.AppendReplayEvents(...)` from `internal/session/*` tests for persisted fixture seeding
- `shelltool.NewManager(...)`-based fixtures in `internal/app/runtime_factory_test.go` and `internal/app/session_lifecycle_test.go` for background-process ownership and reconnect scenarios

Important current gap:

- there is no current black-box test that proves `RunPrompt(...)`-style create/submit/resume behavior through a non-CLI client boundary
- there is no current acceptance-style proof that pending ask/approval state survives process restart or reopen; current evidence points to broker-memory-only behavior today
- there is no client-boundary proof yet that embedded mode and daemon mode behave identically

## Current Tests That Are Too UI-Coupled

The following tests are valuable CLI characterization, but they are too tied to Bubble Tea or terminal rendering to count as proof that the CLI is no longer privileged:

- `internal/app/ui_test.go` and most other `internal/app/ui_*_test.go`: they encode workflow behavior through `NewUIModel(...)`, `tea.KeyMsg`, view modes, and render output.
- `internal/app/ui_native_scrollback_integration_test.go`: it proves normal-buffer transcript behavior, not transport-neutral session/runtime semantics.
- UI-driven portions of `internal/app/session_lifecycle_test.go`: cases such as `/back` draft handoff and fork startup replay currently prove lifecycle behavior through `UITransition`, `NewUIModel(...)`, and Bubble Tea quit mechanics.
- `internal/app/ask_bridge_test.go`: useful for current synchronous ask plumbing, but it proves a UI bridge, not a future non-CLI client contract.

Current clarification for planning:

- Non-UI ask/approval proof in Phase 0 should cover queued pending-state visibility and deterministic single completion while the process remains alive.
- Restart-durable pending ask/approval state appears not to be a current invariant of the monolith.
- If the app-server target should preserve pending asks/approvals across restart, that must be locked explicitly as a target behavior rather than inferred from today's product.

These tests should stay, but they need complementary client-boundary acceptance cases before the migration can claim that the CLI is just another frontend.

## Characterization-First Rule

Before touching a behavior-heavy area, capture characterization coverage for the current monolith where practical.

Priority areas:

- session create and resume flows
- prompt submission and unknown slash fallback
- busy-state command behavior
- approval and ask lifecycle
- background process behavior
- child-session and review flow
- old session loading

## Failure Conditions

The migration should be treated as failing its preservation requirement if any of the following happen without explicit product sign-off:

- an existing major CLI workflow disappears,
- a pre-migration session fixture cannot be resumed,
- disconnect kills active work unexpectedly,
- duplicate retries create duplicate side effects,
- multi-client approval races become nondeterministic,
- the CLI still relies on privileged runtime imports instead of the client boundary.
