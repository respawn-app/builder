# App Server Migration: Phase 0 Checkpoint

Status: actionable pre-refactor checkpoint

This document turns Phase 0 from `plan.md` into a concrete checklist and work packet.

For parallel execution, pair this with `phase-0-workstreams.md`.

Phase 0 exists to freeze current behavior, define the proof surface, and remove the biggest unknowns before the server extraction begins.

No transport work should start before this checkpoint is complete.

## Objectives

- Prove what behavior exists today and what must not regress.
- Ground the migration in the actual current codebase instead of only architectural intent.
- Define the first real frontend/server cut line in terms of current packages and responsibilities.
- Make the next work items parallelizable without losing architectural coherence.

## Required Outputs

- completed compatibility inventory in `../spec/behavior-preservation.md`
- package and use-case cut analysis in `boundary-map.md`
- persistence and data-adoption audit with explicit risks
- characterization test list for behavior-heavy workflows
- acceptance-harness outline for a future black-box non-CLI client
- explicit busy-safe versus busy-blocked behavior table cross-checked against the current CLI

## Checklist

## 1. Compatibility Freeze

- [x] Verify the built-in slash-command inventory against the actual registry and any related frontend-only behaviors.
- [x] Cross-check `../spec/behavior-preservation.md` against current tests and add any missing workflow.
- [x] Capture explicit unknown-slash fallback behavior as a required compatibility case.
- [x] Confirm the current busy-safe vs busy-blocked command distinctions from the command registry and UI behavior.
- [x] Identify any non-slash workflow that is critical but not yet represented in the preservation matrix.

## 2. Persistence And Data Adoption Audit

- [x] Document the current on-disk layout and durable files used by `internal/session`.
- [x] Separate durable source-of-truth data from derived or cache-like data.
- [x] Identify which new metadata is minimally required for project registry, run identity, approval state, and process state.
- [x] Record explicit adoption risks for old `session.json` / `events.jsonl` data.
- [x] Decide whether adoption should be direct-read compatible, lazy-upgraded, or fixture-transformed in tests.

Current locked findings for Workstream B:

- Durable compatibility baseline is still `session.json` plus `events.jsonl`; `steps.log` is observability only.
- Restore-critical metadata today includes `workspace_root`, `continuation.openai_base_url`, `locked.*`, `in_flight_step`, `agents_injected`, `parent_session_id`, `input_draft`, picker-facing `name` / `first_prompt_preview`, and `updated_at` ordering.
- Restore-critical event payloads are broader than the old audit said: `message` persists transcript/tool-presentation state, `tool_completed` joins by `call_id`, `local_entry` preserves reviewer/local text verbatim, and `history_replaced` persists full `[]llm.ResponseItem` replacement payloads with a restore-semantic split between `reviewer_rollback` and non-rollback replay.
- Adoption strategy should stay direct-read compatible for existing session directories, with only lazy additive repair/metadata upgrade. Do not require eager migration of old sessions for Phase 1.
- Session creation itself is already asymmetric: `NewLazy` sessions may never hit disk, `SetName` / `SetParentSessionID` eagerly persist, and `SetContinuationContext` can remain memory-only until a later durable write.

Sharp edges now explicitly called out:

- `OpenByID` accepts `sessions/<sessionID>` and `sessions/<container>/<sessionID>`, but still ignores the older root-flat `/<container>/<sessionID>` layout.
- Prompt-history restore is already versionless but bifurcated: legacy user `message` backfill stops at the first explicit `prompt_history` event.
- Transcript rendering metadata already lives in persisted `message` payloads through `llm.ToolCall.Presentation` / `transcript.ToolCallMeta`.
- Opening a store can mutate legacy data by reconciling `last_sequence`, recreating missing `events.jsonl`, or compacting away a truncated EOF tail.
- `in_flight_step` recovery already has two durable outcomes: successful reopen clears it after appending the interruption message, but a clear/persist failure leaves the durable flag set.
- A session directory without `session.json` is effectively invisible even if `events.jsonl` exists.

Fixture/adoption checklist tightened in `../analysis/persistence-audit.md`:

- keep current covered fixtures for workspace-container reuse, prompt-history mixed semantics, continuation restore, stale `last_sequence`, and truncated event-log repair
- add explicit migration fixtures for missing-file partial sessions, accepted `sessions/<sessionID>` layout, malformed session metadata, stored tool-presentation payloads, `history_replaced` compatibility, both `in_flight_step` reopen outcomes, and lazy-session persistence transitions

## 3. Boundary Map And First Cut Line

- [x] Identify current composition roots and lifecycle entrypoints.
- [x] Identify frontend-only packages and files.
- [x] Identify server-only packages and files.
- [x] Identify the highest-risk re-coupling hotspots inside `internal/app`.
- [x] Define the first transport-neutral application service surface in terms of use cases, not transport methods.

## 4. Characterization Coverage Plan

- [x] Enumerate characterization tests to add before behavior-heavy refactors.
- [x] Mark which current tests already cover each required workflow.
- [x] Identify the first set of black-box acceptance tests that will need a non-CLI test client.
- [x] Record any existing areas where current tests are UI-coupled and will need abstraction later.

Current characterization coverage map:

| Workflow | Current coverage already in repo | Missing coverage to add before behavior-heavy refactors |
| --- | --- | --- |
| Headless launch, create, resume, and continuation-context resolution | `internal/app/launch_planner_test.go`: `TestSessionLaunchPlannerHeadlessCreatesNewSessionAndAppliesContinuationContext`, `TestSessionLaunchPlannerSelectedSessionIDBypassesPicker`; `internal/app/run_prompt_test.go`: `TestRunPromptCreatesSessionAndPersistsDurableTranscript` | Keep this coverage green and extend it only if the headless seam picks up new lifecycle responsibilities that are not already characterized. |
| Unknown slash fallback and built-in command resolution | `internal/app/commands/commands_test.go`: `TestExecuteBuiltins`, `TestExecuteUnknown`, `TestMatchReturnsBestSubstringFirst`; `internal/app/ui_slash_command_picker_test.go`: whitespace-after-slash normalization cases; `internal/app/ui_test.go`: direct unknown-slash submission and queued unknown-slash post-turn drain characterization | Keep this coverage green. Extend it only if a new slash-resolution path appears that bypasses the current prompt-submission fallback contract. |
| File-backed prompt discovery and expansion | `internal/app/commands/file_prompts_test.go`: precedence, normalization collision, filtering, empty-file skipping, `$ARGUMENTS` replacement, append behavior, top-level-only discovery | No Phase 0 blocker beyond keeping this suite green. These tests already freeze the frontend-local contract well enough for the first extraction. |
| Busy-state command behavior and queue drain | `internal/app/ui_slash_command_picker_test.go`: busy `/fast` and `/back` cases; `internal/app/ui_compaction_resume_test.go`: queued steering resume; `internal/runtime/engine_queue_submission_test.go` and `internal/runtime/exclusive_step_test.go`: busy-run and interrupt lifecycle; `internal/app/ui_busy_commands_test.go`: registry contract, representative busy `Enter` behavior, representative busy queue-submit behavior, and queued `/compact` drain into compaction | Keep the new suite green and extend it only when newly discovered busy-path behavior is not already characterized. |
| Status overlay lifecycle and progressive loading | `internal/app/ui_status_test.go`: `TestStatusCommandOpensDetailOverlayInNativeMode`, `TestStatusCommandProgressivelyLoadsSections`, `TestStatusCommandPersistsPromptHistoryWithoutBlockingOpen` | No additional monolith characterization required before Phase 1. Future work belongs in the client-boundary acceptance suite, not more Bubble Tea-only tests. |
| Background process continuity and `/ps` overlay behavior | `internal/app/session_lifecycle_test.go`: `TestNewSessionTransitionKeepsBackgroundProcessesAlive`; `internal/app/runtime_factory_test.go`: background-event routing and owner-session behavior; `internal/app/ui_native_scrollback_integration_test.go`: `/ps` overlay open/close behavior in native mode; `internal/app/ui_test.go`: queued `/ps inline` drain, direct `/ps kill|inline|logs <id>` command-path behavior, log-opening fallback, and selection-retention behavior | Keep the command-path and overlay-path cases green. Extend this area only when a process action still depends on inferred behavior instead of explicit coverage. |
| Review, back-navigation, new-session, and logout lifecycle | `internal/app/session_lifecycle_test.go`: new session, fork rollback, back teleport, startup replay; `internal/app/auth_gate_test.go`: logout re-auth and same-session continuity; `internal/app/ui_test.go`: `/review` and `/init` fresh-session handoff characterization | Keep the `/review` and `/init` handoff cases symmetric. Extend this area only when a session-transition workflow is still inferred rather than explicitly characterized. |
| Existing-session adoption and persistence repair | `internal/session/store_test.go`: prompt history variants, input draft persistence, session naming, continuation persistence, `OpenByID`, truncated-tail repair, `last_sequence` reconciliation, canonical compaction rewrite, missing-file partial-session fixtures, malformed session metadata skip behavior, accepted `sessions/<sessionID>` lookup, and lazy-session metadata persistence transitions; `internal/runtime/engine_test.go`: stored tool-presentation restore, `history_replaced` compatibility, malformed restore payload failure, and both `in_flight_step` reopen outcomes | Keep this fixture batch green and extend it only when a newly discovered persistence edge is not already explicitly covered. |
| Ask/approval lifecycle | `internal/app/ask_bridge_test.go` for the current synchronous bridge; `internal/app/run_prompt_test.go`: headless ask prohibition; `internal/tools/askquestion/tool_test.go`: queued ask blocking, pending visibility, single completion, and duplicate-resolution rejection; `internal/app/patch_outside_workspace_approver_test.go`: queued approval blocking, deny path, and `allow_session` caching without the TUI; `internal/runtime/engine_test.go`: interrupted `ask_question` and shell tool-call attempts carried through reopen into the next model request | Current Phase 0 proof now covers both live-process non-UI ask/approval lifecycle and restart recovery of interrupted tool-call attempts. Current behavior is not “persist pending broker queue objects”; it is “persist the interrupted tool-call attempt in conversation state, reopen with interruption normalization, and let the next model turn decide anew.” Preserve that behavior in the migration. |
| Disconnect/reconnect and lossy event behavior | `internal/app/runtime_event_bridge_test.go`: lossy bridge characterization; `internal/app/runtime_factory_test.go`: reconnect-sensitive background routing | Add boundary-level acceptance scenarios for disconnect/reconnect, explicit gap handling, and duplicate retry semantics once the client boundary exists. The monolith does not currently prove these through a frontend-neutral seam. |

Next unresolved Phase 0 execution target:

- none in the current monolith characterization backlog; remaining open items are Phase 1 extraction work and later client-boundary proof work

Ask/approval restart clarification after the runtime reopen batch:

- current proof covers broker-memory pending asks/approvals and deterministic single-completion behavior outside the TUI while the process stays alive
- current restart behavior is transcript-driven: interrupted tool-call attempts remain in conversation state, reopen appends the interruption marker, and the next model turn sees the prior attempt and decides anew how to proceed
- migration work should preserve that restart behavior rather than trying to persist broker queue state as the primary source of truth

## 5. Boundary Enforcement Plan

- [x] Decide how import-boundary enforcement will work once extraction starts.
- [x] Identify the first packages that must be prevented from importing runtime/session/tools/auth internals directly.
- [x] Define the proof that embedded mode and external-daemon mode will use the same client boundary.

Boundary-enforcement decision:

- Enforce the frontend/server import cut with a repo-local architecture test, not with prompt discipline and not with a heavyweight linter migration.
- The enforcement should use Go package metadata or parsed imports, not grep heuristics, so failures point to real package/file import violations.
- The test should run in normal `./scripts/test.sh` CI and fail the build as soon as a protected frontend package or file imports a banned server package.
- Start narrow and ratchet: enforce the first seam immediately, then expand the protected set as more frontend code is extracted out of the mixed `internal/app` package.

Initial enforcement shape to carry into Phase 1:

1. Add one architecture test package, for example `internal/architecture`, that asserts protected frontend code does not import banned server internals.
2. Source package/file metadata from the Go toolchain (`go list` or equivalent package inspection), so the rule follows real import edges.
3. Keep the deny list focused on server-authority packages for the first cut:
   - `builder/internal/runtime`
   - `builder/internal/session`
   - `builder/internal/auth`
   - `builder/internal/tools` except presentation-only leaves that are explicitly allowlisted for rendering, such as `builder/internal/tools/patch/format`
4. Do not block `internal/llm` in the first enforcement pass. Today some frontend rendering still depends on model-facing types, and that is a later DTO cleanup rather than the first server-authority cut.

First protected package/file set:

- `cmd/builder`
  - already thin and should remain a pure frontend shell
  - must continue to route through `internal/app` or a future client package, never directly into runtime/session/auth/tools internals
- extracted frontend packages that replace the current mixed `internal/app` UI files
  - this is the first real boundary target once Phase 1 starts
  - candidate sources already identified in `boundary-map.md`: `session_lifecycle.go`, `ui*.go`, `session_picker.go`, `auth_picker.go`, `auth_success_screen.go`, and `onboarding_*.go`
- `internal/tui`
  - once the first client DTOs exist, prevent it from importing runtime/session/auth or non-rendering tool packages directly

Important constraint:

- Do not try to enforce the whole current `internal/app` package at once. It is intentionally mixed today. The enforcement boundary must start with `cmd/builder` and with whichever new frontend package is created first during extraction, then grow as files move out of `internal/app`.

Phase 1 enforcement success condition:

- after the first `builder run` seam lands, `cmd/builder` and the migrated frontend package must be clean of direct imports from `internal/runtime`, `internal/session`, `internal/auth`, and runtime-bearing `internal/tools` packages
- CI must fail if any new direct import crosses that boundary

## Current Grounding In This Repo

The current codebase already points to the first extraction seam:

- CLI entrypoint: `cmd/builder/main.go`
- monolithic composition root: `internal/app/app.go`
- session lifecycle orchestration: `internal/app/session_lifecycle.go`
- runtime/tool/auth wiring knot: `internal/app/runtime_factory.go`
- UI/runtime event coupling: `internal/app/ui_runtime_adapter.go`
- current session persistence: `internal/session/store.go`

These files must be treated as primary Phase 0 inspection targets.

## Suggested Parallel Workstreams

These are safe to run in parallel because they are information-gathering and planning tasks with minimal overlap.

### Workstream A: Compatibility And Behavior Inventory

Focus:

- command inventory
- workflow preservation matrix
- busy-state behavior
- existing test coverage map

Inputs:

- `internal/app/commands/commands.go`
- `internal/app/ui_*`
- `docs/dev/app-server-migration/spec/behavior-preservation.md`
- `docs/dev/app-server-migration/spec/command-ownership.md`

Output:

- updated preservation matrix and explicit missing coverage list

### Workstream B: Persistence And Adoption Audit

Focus:

- `internal/session`
- `internal/transcript`
- any persistence-relevant runtime metadata

Output:

- audit of current storage truth, migration risks, and minimum metadata additions

### Workstream C: Boundary Map

Focus:

- `cmd/builder/main.go`
- `internal/app`
- `internal/runtime`
- `internal/session`
- `internal/tools`
- `internal/llm`
- `internal/auth`

Output:

- proposed transport-neutral service boundary and first package split map

### Workstream D: Acceptance Harness Design

Focus:

- black-box client needs
- embedded versus external execution proof
- deterministic approval/process race coverage

Output:

- acceptance-harness outline and required helper abstractions

Current locked findings for Workstream D:

- The current headless seam in `internal/app/run_prompt.go` is the first credible acceptance target because it already bypasses Bubble Tea and drives `launchPlanner.PlanSession(...)`, `PrepareRuntime(...)`, and `runtime.Engine.SubmitUserMessage(...)` directly.
- The future acceptance suite must run the exact same client contract against two targets: embedded in-process server and external daemon. Only server bootstrap changes across modes; test logic and assertions do not.
- The minimum non-CLI client for the first wave does not need slash-command parsing or TUI rendering. It does need typed capabilities for: create/list/attach session, submit prompt, await terminal result, inspect transcript/session metadata, observe coarse run events, interrupt active work, answer asks/approvals, list/inspect/kill background processes, and disconnect/reconnect.
- Ask/approval proof cannot rely on today's headless CLI path because `runPromptAskHandler(...)` intentionally hard-fails asks in background mode. Approval cases therefore need the future non-CLI client boundary, not `RunPrompt(...)` itself.
- The first acceptance wave should stay biased toward headless-compatible behavior already grounded in the repo: create session, resume by session ID, transcript hydration from durable storage, background-process ownership/reattach, disconnect/reconnect continuity, slow-subscriber handling, and idempotent retry protection.
- `internal/app/launch_planner_test.go`, `internal/app/runtime_event_bridge_test.go`, `internal/app/runtime_factory_test.go`, `internal/runtime/engine_test.go`, `internal/runtime/engine_queue_submission_test.go`, and `internal/session/store_test.go` are the best current seed coverage/helpers for the future harness.
- The following current tests are too UI-coupled to serve as privilege-removal proof and should be treated as CLI characterization only: `internal/app/ui_test.go`, `internal/app/ui_native_scrollback_integration_test.go`, most of `internal/app/ui_*_test.go`, and the `NewUIModel(...)` / `tea.KeyMsg` driven lifecycle coverage in `internal/app/session_lifecycle_test.go`.

First acceptance matrix to carry into extraction planning:

| Case | Why first | Required client abilities | Current grounding |
| --- | --- | --- | --- |
| Headless create + submit + durable transcript | Closest match to current `RunPrompt(...)` seam; proves CLI is not needed for the primary run path. | Create session, submit prompt, await completion, read transcript/session metadata. | `internal/app/run_prompt.go`, `internal/app/launch_planner_test.go`, `internal/session/store_test.go` |
| Resume existing session by ID | Proves session continuity without picker/UI ownership. | List or open by session ID, attach, submit again, hydrate transcript. | `internal/app/launch_planner_test.go`, `internal/session/store.go` |
| Second client attach during/after run | Proves attach/rehydrate is boundary-based rather than CLI-local state. | Attach existing session, read run state/events, reload transcript. | `internal/app/runtime_event_bridge_test.go`, `internal/runtime/events.go` |
| Ask/approval pause and deterministic resume | Required because headless CLI cannot answer asks; this is where non-CLI capability becomes mandatory. | Observe pending ask, answer it, verify single resume outcome. | `internal/app/ask_bridge_test.go`, `internal/tools/askquestion`, runtime tests with deterministic fake clients |
| Background process ownership, list, kill, and reconnect | Proves active work/process state is not privileged to the active CLI. | Start process, list/inspect process, reconnect, kill, observe final state. | `internal/app/runtime_factory_test.go` background-router coverage |
| Disconnect/reconnect during active work | Direct proof that frontend disconnect does not kill work. | Disconnect client, reconnect another client, hydrate transcript/run state. | `internal/app/runtime_factory_test.go`, `internal/runtime/engine_queue_submission_test.go` |
| Slow subscriber / event-gap handling | Current runtime event bridge is lossy under pressure, so the boundary must make loss explicit and recoverable via rehydrate. | Subscribe to events, intentionally lag, detect gap, reload transcript/read models. | `internal/app/runtime_event_bridge_test.go` |
| Duplicate retry without duplicate side effects | Needed to falsify client privilege and prove request idempotency. | Retry submit/approval after induced disconnect or timeout, assert one durable outcome. | current queue-submission/runtime tests plus future boundary request IDs |

## Exit Criteria

This checklist is now closed as a planning artifact. Phase 0 itself is still incomplete until the characterization additions, fixture additions, and boundary-enforcement test described above are executed in the repo.

Phase 0 is complete only when all of the following are true:

- the compatibility surface is explicit enough that a regression can be named and proven
- the first boundary extraction can be described in current-package terms
- old-session adoption risks are documented concretely
- the next implementation tasks can be split among agents without arguing about basic architecture
- there is a clear proof strategy for showing that the CLI is no longer privileged

## What This Checkpoint Does Not Do

- it does not finalize transport payload schemas
- it does not introduce `internal/protocol` or `internal/client` yet
- it does not start the refactor
- it does not replace the phased plan in `plan.md`

It simply makes the next step executable.
