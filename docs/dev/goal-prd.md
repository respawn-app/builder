# Goal Mode PRD

## Problem

Builder needs a first-class way to run long-running objectives across multiple turns without making the model infer task state from a growing transcript. The feature should give the operator explicit control over what the session is trying to achieve, whether that objective is active, and when Builder should continue or stop goal-driven work.

## Locked Decisions

- Goal management is CLI/runtime-owned. Builder must not add model-callable goal tools, even dynamically.
- The model may receive goal context and continuation instructions, but it cannot create, update, pause, resume, clear, or complete goals via model tools.
- The Builder CLI is the authoritative control surface for goal lifecycle for both operator actions and model-reported lifecycle changes.
- User-facing goal management happens through TUI slash commands, primarily `/goal`.
- Model-facing goal management happens through Builder CLI commands executed via normal shell tool calls. Shell commands started by Builder carry the caller session id, so goal CLI commands can target the active session without exposing model tools.
- `/goal <objective>` is immediate: it sets the session goal and starts a model turn toward that goal right away.
- Budgets are out of scope for v1. Do not implement token budgets, time budgets, budget-limited status, or budget accounting in the first slice.
- Goal completion is reported through Builder CLI, not through model tools. The model should use `builder goal complete` from a shell command after auditing completion. Agent-env completion requires hidden `--confirm`; human CLI completion does not.
- Goal mode requires the `ask_question` tool while an active goal can start model work. Validate this at the boundary that starts the model loop and surface a normal runtime error if the parity check fails.
- Do not mutate session DB directly from CLI. Goal CLI commands must go through live server/runtime RPC and fail if the target session is not reachable.

## Goals

- Let the operator set a concrete session goal from the TUI.
- Persist the current goal with the session so resume keeps goal state.
- Let Builder continue goal-directed work across turns when goal mode is active.
- Keep goal lifecycle observable in UI and persisted session state.
- Prevent hidden model-side state mutation by keeping goal changes in runtime-owned commands.

## Non-Goals

- No new model tools for goal management.
- No dynamic tool exposure for goals.
- No MCP/plugin goal API in this slice.
- No hidden model-only goal state mutation. Model-initiated completion must happen through visible Builder CLI commands.
- No goal budgets or usage accounting in v1.
- No hidden model-tool completion workflow. Completion must be visible as a CLI command invocation.
- No `blocked` or `failed` goal status in v1. If blocked, the model asks the user for help.
- No headless goal loop in v1. Headless runs fail if the target session has a goal.

## User-Facing Command Shape

V1 command surface:

- `/goal` shows current goal and status.
- `/goal <objective>` sets or replaces the session goal and immediately starts work toward it. If a model turn is currently running, setting/replacing a goal is rejected.
- `/goal pause` pauses goal continuation.
- `/goal resume` marks goal active again. Resuming a completed goal reopens the same goal as active.
- `/goal clear` removes the goal.

Companion non-interactive CLI command surface:

- `builder goal show`
- `builder goal set <objective>`
- `builder goal pause`
- `builder goal resume`
- `builder goal clear`
- `builder goal complete`

`builder goal ...` defaults to the caller session when `BUILDER_SESSION_ID` is present. It should accept explicit `--session <id>` outside Builder-managed shell commands; do not support `--continue` for this command.

`builder goal ...` discovers the live runtime through existing app-server discovery/config only. `BUILDER_SESSION_ID` identifies the target session but is not enough by itself to locate a server. If the discovered/default server does not host the target session, the command fails with live-runtime-unavailable. The CLI must not read or mutate session DB directly.

Standalone `builder goal` commands do not acquire controller leases. They use narrow goal runtime RPCs against the live session. The TUI may still pass its current controller lease when invoking the same runtime authority.

When `BUILDER_SESSION_ID` is present, only `builder goal show` and `builder goal complete` are allowed. Agent attempts to run `set`, `pause`, `resume`, or `clear` fail nonzero with a prompt-backed warning from `prompts/goal/agent_command_denied.md`.

Agent `builder goal complete` has a tripwire: without hidden `--confirm`, it fails nonzero with `prompts/goal/complete_confirm_required.md`. The flag must not appear in nudge prompts, CLI help, or docs intended for the model before the tripwire error reveals it. Human/non-agent `builder goal complete` does not require `--confirm`.

`builder goal complete` may complete active or paused goals. If the goal is already complete, it succeeds idempotently. If there is no goal, it fails.

`builder goal show` prints plain text by default and supports `--json`; JSON contains the current goal and status only. `show` is always allowed from agent env.

The first slice must not add `/goal complete`; user completion is absent from TUI v1. Model completion reporting uses `builder goal complete`.

Implementation requirement: when the `builder goal ...` subcommand is implemented, update `cli/builder/help.go` and CLI docs to expose the commands and the `BUILDER_SESSION_ID` caller-session targeting rule. This worktree does not yet contain the `builder session-id` plumbing from `9b54ad58`, so help text should be patched when that change is present in this branch.

## Runtime Behavior

When a goal is active, Builder owns continuation. The runtime injects goal context as a developer message before goal-started and auto-continuation turns. Normal user-steered turns should not receive special goal handling; existing steering/queue logic stays intact.

The model can state in natural language that it believes the goal is complete, but Builder should only mark completion when the model runs `builder goal complete` or the operator uses an approved CLI/TUI path.

Goal prompt sources live under `prompts/goal/`. Do not hardcode non-trivial goal prompts or model-facing goal error copy in Go. V1 prompt inventory: `set.md`, `nudge.md`, `pause.md`, `resume.md`, `clear.md`, `complete.md`, `agent_command_denied.md`, and `complete_confirm_required.md`. One-line human UI labels may stay in Go.

Goal loop behavior:

- After `/goal <objective>`, Builder creates one transcript entry with dedicated ongoing text such as `You set a goal: "<objective preview>"`; detail mode shows the exact developer message the model received.
- While active and idle, Builder immediately continues goal work until the goal is paused, completed, cleared, interrupted, or a runtime error occurs.
- Queued user messages wait until the goal is complete. Direct user steering/interruption uses existing runtime behavior; do not add special queue semantics beyond goal-loop scheduling.
- While a model turn is currently running, only `/goal pause` and `/goal clear` are accepted among lifecycle commands. `/goal set`/replacement, `/goal resume`, and `/goal complete` are rejected while a model turn is currently running.
- `/goal pause` while a model request is in flight appends/persists the normal developer message immediately; the in-flight request only sees it if existing steering/follow-up logic would include it later.
- Ctrl+C interrupts the current turn, keeps persisted status `active`, and creates only runtime-local suspension until the next user message/session resume. `/goal pause` during runtime-local suspension changes persisted status to `paused`.
- On session resume with an active goal, start idle, show `goal active` in status/transcript, and continue the loop on the next user message.
- Headless `builder run --continue <session> ...` fails if that session has a goal and tells the user to clear the goal first.
- Before any model loop starts, validate active-goal/ask_question parity. If `ask_question` is disabled while an active goal can run, fail like a model/API runtime error. Suggested copy: `please allow the model to ask questions for goals to work. If model encounters a blocker, it will ask you for help instead of spinning forever or implementing hacky workarounds`.

## Persistence

Goal state is session-scoped and stored in session metadata for fast resume reads:

- `goal_id` string: stable id for the current goal instance. Replacing the objective creates a new id.
- `objective` string: operator-provided objective.
- `status` string: `active`, `paused`, or `complete`.
- `created_at` timestamp: current goal creation time.
- `updated_at` timestamp: last goal mutation time.

Clearing the goal removes the metadata goal object.

Objectives reject only empty/whitespace input in v1. Preserve exact text after trimming outer whitespace; internal newlines are kept. Goal IDs are opaque random IDs.

Goal events are appended for audit/projection. Each event carries an `actor` field with one of `user`, `agent`, or `system`; non-agent CLI invocations are `user`.

- `goal_set`: emitted when `/goal <objective>` creates or replaces the goal. Payload: `{ "actor": "<actor>", "goal": <goal>, "replaced_goal_id": "<previous id, if any>" }`.
- `goal_status_updated`: emitted when goal status changes through `/goal pause`, `/goal resume`, or `builder goal complete`. Payload: `{ "actor": "<actor>", "goal": <goal>, "previous_status": "<active|paused|complete>" }`.
- `goal_cleared`: emitted when `/goal clear` removes a goal. Payload: `{ "actor": "<actor>", "goal": <previous goal> }`.

Every lifecycle action appends both structured goal event and persisted developer-message transcript entry. One entry should carry both detail text and dedicated ongoing text when needed.

Resume read path: runtime reads session metadata goal state directly. Event log scanning is not required to rehydrate the current goal.

## UI

Goal status should be visible without reading transcript history. Initial display:

- Active: `goal active`
- Paused: `goal paused`
- Complete: `goal complete`

When a goal turn is running, the progress word next to the spinner should be `goal`.

`/goal` opens a read-only alt-screen dashboard when a goal exists. Dashboard v1 may expose pause/resume/clear actions via keybindings; mutation stays in the dashboard and refreshes state for pause/resume, and closes for clear. If no goal exists, `/goal` prints: `No goal to manage yet. First, start a goal with <command>`.

Replacement and active-goal clear use proper alt-screen confirmation UI, not a text editor. Confirmation uses Enter for selected action, Tab/arrow to toggle, and Esc to cancel. Active-goal clear includes clear while a model turn is currently running. Clear confirms only when the goal is active and not under runtime-local suspension; paused/complete/runtime-local-suspended clear immediately.

## UX

User-visible injected reminders should be explicit but low-noise:

- Ongoing transcript: show a compact local entry when Builder starts goal-driven continuation, for example `Goal: continue <objective preview>`.
- Detail transcript: include the full injected goal nudge prompt so power users can audit exactly what the model saw.
- Status line: show current goal state (`goal active`, `goal paused`, `goal complete`) without requiring transcript scanning.
- Slash command output: `/goal` shows the dashboard when a goal exists, otherwise the empty-state hint.

The model sees the full nudge prompt as a developer message. It should read as operational steering, not hidden state. It should push the model toward planning, editing, verifying, and auditing completion against evidence before reporting completion through the CLI.

## Deferred Decisions

- Usage statistics in the goal dashboard: time, turns, tokens, and related v2 accounting.
- Structured acceptance criteria; v1 stores one freeform objective string.
- Headless goal loop command, if any.
