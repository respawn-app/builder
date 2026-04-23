# Worktree Management Plan

Status: planning baseline for https://github.com/respawn-app/builder/issues/68

This document finalizes the spec and implementation plan for Builder-managed git worktree workflows.

## Terminology

- Use `workspace`, not `repo`.
- The durable model remains `project > workspace > worktree`.
- A `worktree` belongs to a `workspace`, not directly to a `project`.
- A session belongs to a `project` and carries a mutable execution target `(workspace_id, worktree_id?, cwd_relpath)`.

Older issue comments that say `repo` should be read as stale terminology only.

## Why This Can Move Now

The prerequisites that previously blocked this issue are now present in the codebase:

- server-owned project/workspace/worktree/session metadata exists
- session execution target is already modeled and exposed in `clientui.SessionExecutionTarget`
- session identity is already decoupled from execution target
- the server already supports project/workspace attachment and session workspace retargeting

This means the remaining work is no longer domain-model invention. It is product flow, git integration, server mutation surfaces, and safety rules.

## Goals

- Let the user create, inspect, switch to, and delete git worktrees from Builder.
- Keep the same session identity when switching execution target to another worktree.
- Make the active execution target visible in session status and session-local worktree UX.
- Keep git as the source of truth for worktree topology.
- Keep mutation ownership on the server side so multi-client/session semantics stay coherent.

## Non-Goals

- No new top-level "teleport roots" abstraction in this slice.
- No mirrored authoritative Builder-owned worktree registry separate from git.
- No automatic remote filesystem browsing or remote worktree creation semantics beyond normal server path reachability.
- No implicit deletion of worktrees still in active use by other sessions.
- No background sync daemon for git topology; sync happens on read/mutation boundaries.

## Locked Product Spec

### Slash Command Surface

Canonical command: `/worktree`

Alias: `/wt`

Initial shipped subcommands:

- `/worktree`
  - if current execution target is a non-main worktree: show inline worktree status for the current session
  - otherwise: enter create flow
- `/worktree new`
  - always enter create flow
- `/worktree create`
  - alias for entering the create flow
- `/worktree list`
  - show known worktrees for the current workspace with current-target marker
- `/worktree status`
  - always show the current inline worktree status when on a non-main worktree, otherwise fall back to the workspace list
- `/worktree switch <selector>`
  - switch the current session execution target to an existing worktree under the current workspace
- `/worktree delete [selector]`
  - delete the current worktree when selector omitted and current target is a deletable worktree
  - otherwise delete the selected worktree after confirmation

Additive aliases shipped for operator convenience:

- `/worktree ls` -> `/worktree list`
- `/worktree remove` and `/worktree rm` -> `/worktree delete`

`<selector>` resolves in this order:

1. exact Builder `worktree_id`
2. exact canonical root path
3. exact worktree directory/display name within the current workspace
4. exact branch name when unambiguous within the current workspace

Ambiguous selectors fail with an explicit disambiguation list. No fuzzy destructive matching.

### Idle/Busy Rules

- Worktree mutations are idle-only.
- Read-only worktree status/list may be allowed while busy later, but the first slice keeps the whole command family idle-only.
- Builder must not change execution target while a run is active.

### Create Flow

The create flow collects and validates:

- a single `branch or ref` target field
- base ref/commitish to branch from, defaulting to current `HEAD` when Builder will create a new branch
- target worktree directory path

Defaults:

- Suggested target name comes from sanitized session name when present.
- If there is no valid session-name suggestion, the `branch or ref` field stays blank and the user must choose one explicitly.
- Suggested worktree directory name derives from the final branch slug.
- Worktree base directory is configurable via `worktrees.base_dir`.
- Default base directory lives under Builder's persistence root.
- If the configured base directory does not exist, Builder creates it.
- If the default target path already exists, Builder auto-picks a unique suffixed path.

Resolution rules:

- if the typed target resolves to an existing local branch, Builder reuses that branch
- if the typed target resolves to another valid ref/commit-ish, Builder creates the worktree detached at that ref
- otherwise Builder creates a new branch from `Base ref`

The dialog must surface this resolution live in the UI rather than hiding it behind submit-time heuristics.

If the target branch is already checked out in another worktree, Builder fails with a clear error and asks the user to choose another branch/name.

### Session Semantics After Create/Switch

- Creating a worktree does not create a new session.
- Switching to another worktree does not create a new session.
- The current session keeps its identity and transcript.
- The session's execution target changes to `(workspace_id, worktree_id, cwd_relpath)`.
- `cwd_relpath` is preserved when the same relative path exists under the target; otherwise it clamps to `.`.

After a successful create or switch:

- subsequent tool execution uses the new effective workdir
- session main view/status reflects the new worktree immediately
- Builder may briefly detach/reattach runtime plumbing while keeping the same session identity
- Builder appends a user-visible local note about the switch that is visible in ongoing and detail transcript views

This slice does not add a separate teleport-root model. Execution-target switching plus a visible note is the intended behavior.

### Model Reminder Messages

Worktree transitions must also notify the model, not just the user.

Rule:

- inject a typed developer-context worktree reminder lazily on the next model submission after a worktree transition
- do this even for a blank/new session so the agent always knows the active worktree context
- if multiple switches happen before the next model submission, the latest pending reminder always wins and replaces any earlier pending worktree reminder for that conversation state
- after compaction, inject the current active-target reminder again once for the new compaction generation

These reminders are environment-style current-state messages, not historical logs.

Required prompt files in `prompts/`:

- `worktree_mode_prompt.md`
- `worktree_mode_exit_prompt.md`

Required code wiring for those prompts:

- add embedded prompt variables in `prompts/embed.go`
- add new typed `llm.MessageType` variants for worktree enter/exit reminders
- add meta-context builders parallel to the existing headless enter/exit helpers
- inject them through runtime-owned transition logic on next submission rather than as ad-hoc local entries
- classify them as developer-context transcript items with the same detail-only treatment as other typed runtime reminders

Required reminder behavior:

- enter/switch reminder when moving from main workspace to a worktree or from one worktree to another
- exit reminder when moving from a worktree back to the main workspace, including delete-triggered rebind

Rendered reminder content must include at minimum:

- branch name
- effective cwd after the switch
- worktree path when entering/switching to a worktree
- main workspace root when exiting back to it

These reminders should follow the same typed-message pattern as headless enter/exit prompts rather than being plain local transcript notes.

They must remain visible in detail mode.

Transcript-path rule:

- the immediate user-visible switch note and the lazy developer reminder are distinct surfaces
- the switch note exists for user feedback in ongoing/detail transcript
- the typed developer reminder exists for model context and detail-mode visibility only
- Builder must not append duplicate developer reminders for the same pending target before the next submit

### Worktree Status UX

`/worktree` on an active non-main worktree shows inline metadata at minimum:

- workspace display/root
- current worktree display/root
- branch name
- whether it is the current session target
- whether it is the main worktree
- current effective working directory
- origin hint when the current session created this worktree in the same session lineage

Existing git worktrees not created by Builder are still listed/switchable/deletable in the first slice, but they should be clearly visually marked wherever feasible.

`/status` must surface worktree context when present using the existing execution-target projection.

### Delete Semantics

- Main workspace root is not deletable via `/worktree delete`.
- Builder rejects explicit `main` / main-worktree delete targets before entering confirmation.
- Deletion always requires an inline confirmation step.
- Confirmation must state both filesystem removal and branch-removal intent.
- Confirmation input is explicit:
  - `delete` removes the worktree and keeps any non-proven branch by default
  - `delete branch` additionally opts into a branch-delete attempt when Builder cannot prove the branch belongs only to that worktree
- Builder performs deletion via git/admin cleanup flows, not blind filesystem removal.

Safety rules:

- If any other session currently targets that worktree, deletion is refused with a list of blocking sessions.
- If any background shell process is still running with a workdir at or under that worktree root, deletion is refused.
- If the current session targets that worktree, Builder first retargets the session to the owning workspace root, preserving `cwd_relpath` when possible.
- If retarget fails, deletion does not proceed.
- After successful rebind, Builder performs the remaining git cleanup even when the worktree directory is already gone.
- Branch cleanup is best-effort and conservative.
- Builder auto-attempts branch deletion only when provenance proves the branch was created for the deleted worktree (`builder_managed && created_branch`).
- Otherwise Builder keeps the branch by default and requires an explicit delete-confirmation opt-in before attempting `git branch -d`.
- If branch deletion is not attempted or fails, Builder leaves the branch intact and reports that cleanup remains.

The first implementation should prefer conservative refusal over destructive convenience.

Cleanup goals for delete:

- session rebind back to main workspace when needed
- remove git worktree registration when still present
- prune stale/missing worktree registrations when the directory was manually deleted first
- remove the branch automatically only when provenance proves it belongs to the deleted worktree
- otherwise keep the branch unless the user explicitly confirms branch deletion too

This means users may manually delete the worktree directory first and still use `/worktree delete` later for the remaining cleanup.

Locked cleanup order:

1. if the current session targets the worktree, rebind it to the main workspace first
2. run git admin cleanup for stale registrations, including prune, so manually deleted directories do not block the rest
3. if the worktree is still registered, remove it via `git worktree remove ...`
4. attempt safe branch cleanup via `git branch -d ...` only when auto-safe or explicitly confirmed
5. if safe branch delete fails, leave the branch intact and report that cleanup remains

Delete-blocker matching rule:

- a background shell blocks delete when its recorded workdir is equal to the target worktree root or is a descendant of it
- the first shipped slice refuses delete rather than offering inline kill-then-continue
- if cleanup fails after session rebind has already succeeded, Builder keeps the session on the main workspace and reports the remaining cleanup failure; it does not roll the session back into the worktree

### Git Authority And Metadata Rules

- Git remains the source of truth for worktree topology.
- Builder stores only additive metadata needed for ids, session linkage, UX, and safe mutation handling.
- Reads and mutations refresh/sync worktree metadata from git before acting.
- Builder must not assume every git worktree was created by Builder.

The worktree table remains a Builder projection with stable ids and additive metadata, not a replacement registry.

### Optional Post-Create Setup Hook

Builder supports one optional configured shell-script hook that runs only for newly created worktrees, after successful worktree creation and after the session/runtime have switched to the new target.

Purpose:

- copy `.env` files
- install local secrets/config
- run workspace-specific setup

Contract:

- default is disabled
- hook path is configurable via `worktrees.setup_script`
- relative hook paths resolve from the source workspace root
- hook runs with cwd set to the new worktree root
- Builder invokes it asynchronously so worktree UX is not blocked
- Builder provides both positional args and stdin JSON
- Builder also mirrors the core fields as environment variables for shell convenience

Minimum payload fields:

- source workspace root
- branch name
- final worktree root

Recommended stdin JSON shape:

```json
{
  "source_workspace_root": "/abs/original/path",
  "branch_name": "feature/foo",
  "worktree_root": "/abs/final/worktree/path",
  "session_id": "session-123",
  "project_id": "project-123",
  "workspace_id": "workspace-123",
  "worktree_id": "worktree-123",
  "created_branch": true
}
```

Positional args in order:

1. source workspace root
2. branch name
3. final worktree root

Environment variable mirrors:

- `BUILDER_WORKTREE_SOURCE_WORKSPACE_ROOT`
- `BUILDER_WORKTREE_BRANCH_NAME`
- `BUILDER_WORKTREE_ROOT`
- `BUILDER_WORKTREE_SESSION_ID`
- `BUILDER_WORKTREE_PROJECT_ID`
- `BUILDER_WORKTREE_WORKSPACE_ID`
- `BUILDER_WORKTREE_WORKTREE_ID`
- `BUILDER_WORKTREE_CREATED_BRANCH`

`worktree_id` may be empty when no durable id is available yet at invocation time, but the field should still exist in the JSON payload.

Failure behavior:

- hook timeout is short, around 10 seconds in the first slice
- hook timeout or non-zero exit does not undo worktree creation and does not revert session switch
- Builder emits a transcript error/local notice with failure info and how to fix it
- that failure notice should not be exposed to the model by default

Builder must not attempt automatic rollback of arbitrary hook side effects.

## Locked Architecture Spec

### Ownership

- Slash parsing remains frontend-local.
- Git inspection, create/switch/delete mutations, confirmation validation, metadata sync, and session retargeting are server-owned.
- Frontend-local code may collect wizard/form input in the first slice, but mutation state must not live only in the client.
- Worktree enter/exit reminder injection is runtime-owned and uses typed developer messages, following the existing headless enter/exit pattern.

### New Server Surface

Add a dedicated server-owned worktree service instead of overloading unrelated packages.

Proposed contract family in `shared/serverapi/worktree.go`:

- `WorktreeListRequest/Response`
- `WorktreeStatusRequest/Response`
- `WorktreeCreateRequest/Response`
- `WorktreeSwitchRequest/Response`
- `WorktreeDeleteRequest/Response`

Expected response payloads should carry typed worktree summaries plus the resulting `SessionExecutionTarget` where relevant.

Suggested package: `server/worktree`

Responsibilities:

- git worktree discovery/parsing
- metadata projection/sync
- selector resolution
- branch/create/delete orchestration
- cross-session safety checks before delete
- session execution-target retargeting
- setup-hook execution

Reminder/runtime integration responsibilities:

- expose enough structured execution-target transition state for runtime reminder injection
- coalesce pending worktree reminders to the latest target when multiple switches happen before submit
- re-inject current target reminder after compaction generation changes
- provide typed worktree enter/exit message payloads that the transcript/UI can classify like other developer-context reminders

Runtime-switch responsibilities:

- retargeting is idle-only
- live runtime/tool registry must rebind to the new execution target after the move
- the first implementation should do this by rebuilding/rebinding runtime-local tool handlers against the new effective root during the same session attach lifecycle, rather than teaching every tool to resolve execution target dynamically on each call
- background shell processes keep their original workdir and continue running there
- shell completion notices reroute to the current session as they do today; deleting a worktree with running processes under it is blocked

### Metadata Additions

The current schema already has `worktrees`. This slice should extend metadata only where product behavior truly needs durable Builder-owned facts.

Durable facts likely needed:

- whether Builder created the worktree
- whether Builder created the branch during create flow
- last known branch name / head oid snapshot for UX acceleration if useful
- creation provenance sufficient for same-session origin hints
- last known cleanup state sufficient to continue delete cleanup after manual directory removal if needed

Prefer additive typed columns where the value drives behavior. Do not lean on ad-hoc string parsing of git output stored in JSON.

### Git Integration Rules

Use explicit git commands rather than direct filesystem manipulation:

- discovery: `git worktree list --porcelain`
- create: `git worktree add ...`
- delete: `git worktree remove ...` with missing-path cleanup support when possible
- optional branch delete: safe delete via `git branch -d ...`; no forced branch removal in the first slice
- stale registration cleanup: `git worktree prune` and related admin cleanup as needed

Implement parsing via a dedicated typed parser with characterization tests against porcelain fixtures.

### Current Workspace Constraint

The first shipped slice operates within the current session's current workspace.

- list/switch/create/delete are scoped to the current workspace
- no cross-workspace worktree switching from one command invocation
- project-wide worktree management can come later if needed

This keeps selectors, safety checks, and UX comprehensible.

### Reopen And Missing-Target Policy

- If a session is reopened later and its last worktree target is missing, Builder should reuse the existing rebind flow rather than silently auto-rebinding.
- Worktree retargeting preserves `cwd_relpath` only when the same relative path exists as a directory under the new target root; otherwise it falls back to the new root.

## Implementation Plan

### Phase 1: Git Topology And Metadata Sync

- add a typed git worktree inspection package with fixture coverage for main worktree, linked worktrees, detached HEAD, locked entries, and missing/prunable paths
- add metadata sync logic that upserts worktree projection rows for a workspace from git discovery
- define Builder-created provenance fields needed for later delete behavior

Exit criteria:

- server can list current-workspace worktrees with stable ids and branch/path metadata
- sync is idempotent and safe against pre-existing non-Builder worktrees

### Phase 2: Server Worktree Service

- add `shared/serverapi/worktree.go`
- implement `server/worktree` service for list/status/create/switch/delete
- integrate session retarget behavior using existing execution-target semantics
- add cross-session blocker checks for delete
- add async setup-hook runner and non-model failure surface
- add delete-cleanup flow that still works when the worktree directory has already been removed manually
- refuse delete while background processes still run under the target worktree

Exit criteria:

- all worktree mutations can be exercised through server APIs without TUI code
- create/switch/delete rules are deterministic and unit-tested

### Phase 3: Slash Command And CLI Flow

- extend slash command registry with alias support so `/wt` is a first-class alias of `/worktree`
- add `/worktree` command family parsing and dispatch
- implement first-slice inline flow for create and delete confirmation
- show inline status/list output in chat UI without pretending these are model turns
- visually mark non-Builder-managed worktrees where feasible

Exit criteria:

- idle user can complete full create/switch/delete flows from the TUI
- no mutation path bypasses server ownership

### Phase 4: Status And Runtime Projection

- update `/status` rendering to show workspace + worktree context distinctly
- add local switch note rendering after create/switch/delete-retarget events in both ongoing and detail transcript surfaces
- add typed worktree enter/exit reminder prompts in `prompts/`
- add runtime injection logic mirroring headless enter/exit behavior, backed by typed `llm.MessageType` variants plus meta-context builders and lazy next-submit injection semantics
- ensure reconnect/hydration preserves the current execution-target display correctly

Exit criteria:

- current target is obvious in both normal status and post-action feedback
- worktree reminders represent only the latest active target and reappear correctly after compaction
- reconnect does not lose worktree context

### Phase 5: Tests And Proof

Coverage must include:

- git porcelain parsing fixtures
- metadata sync/upsert behavior
- create flow with and without branch creation
- configured base-path creation and unique path suffix selection
- setup-hook async success/failure/timeout behavior
- selector resolution and ambiguity failures
- switch preserving/clamping `cwd_relpath`
- reminder injection for blank sessions and existing sessions alike with correct structured message types
- reminder coalescing when multiple switches happen before next submit
- reminder reinjection after compaction generation change
- delete of current worktree retargeting to workspace root first
- delete cleanup when the worktree directory was manually removed before the command
- safe branch cleanup success/failure reporting
- delete refusal when background processes still run under the target worktree
- delete refusal when another session targets the worktree
- `/status` and session main-view projection including worktree info
- slash alias `/wt`

## Implementation Workstreams

This section is for the next implementation agent. The product questions are intentionally treated as locked unless the user reopens them.

### Workstream 1: Metadata And Git Projection

Primary goal:

- make `worktrees` a real read/write Builder projection derived from git, with stable ids and enough provenance for cleanup logic

Likely files/packages:

- `server/metadata/queries.sql`
- `server/metadata/store.go`
- `server/metadata/migrations/...`
- `server/worktree/git.go`
- `server/worktree/git_test.go`

Expected outputs:

- typed parser for `git worktree list --porcelain`
- metadata upsert/sync for current-workspace worktrees
- selector resolution helpers
- provenance fields for Builder-created worktree/branch and last-known branch identity

### Workstream 2: Server Worktree API

Primary goal:

- expose first-class server-owned create/list/status/switch/delete operations

Likely files/packages:

- `shared/serverapi/worktree.go`
- `server/worktree/service.go`
- `server/worktree/service_test.go`

Expected outputs:

- request/response types for list/status/create/switch/delete
- current-workspace scoping only
- delete blocker checks for sessions and background processes
- async setup-hook launch after successful create/switch

### Workstream 3: Runtime Retarget And Tool Rebind

Primary goal:

- make live same-session worktree switches actually affect tool cwd/workdir authority

Likely files/packages:

- `server/sessionruntime/service.go`
- `server/sessionlifecycle/service.go`
- `server/runtimewire/wiring.go`
- `server/runtimewire/tool_registry.go`
- `server/tools/shell/...`

Expected outputs:

- idle-only retarget enforcement aligned with controller-lease semantics
- runtime/tool rebind strategy implemented during same-session attach lifecycle
- no transcript replay/rewrite while retargeting
- background shell completion rerouting preserved

### Workstream 4: Reminder Injection And Transcript Notes

Primary goal:

- implement the lazy environment-style worktree reminder plus immediate user-facing switch notes

Likely files/packages:

- `prompts/embed.go`
- `server/llm/types.go`
- `server/runtime/meta_context.go`
- `server/runtime/engine_message_ops.go`
- `server/runtime/transcript_message_visibility.go`
- `cli/tui/...` or `cli/app/...` local note rendering paths

Expected outputs:

- prompt rendering helper for dynamic worktree fields
- typed worktree enter/exit message types
- latest-pending-target coalescing before submit
- re-injection once per compaction generation
- detail-mode visibility without duplicate pending reminders

### Workstream 5: CLI Command Flow

Primary goal:

- expose the feature to users cleanly in the TUI

Likely files/packages:

- `cli/app/commands/commands.go`
- `cli/app/ui_input_slash_commands.go`
- new `cli/app/...` worktree flow controller files
- `cli/app/ui_status.go`

Expected outputs:

- `/worktree` and `/wt`
- create/list/status/switch/delete command handling
- inline confirmation/error/status rendering
- visual marking for non-Builder-managed worktrees

Note:

- prefer direct registration of both `/worktree` and `/wt` unless generic alias support proves clearly cleaner; do not broaden scope without value

### Workstream 6: Delete Cleanup Robustness

Primary goal:

- make delete safe and useful even after manual directory removal

Likely files/packages:

- `server/worktree/service.go`
- `server/metadata/store.go`
- `server/tools/shell/manager.go` / snapshot accessors as needed

Expected outputs:

- running-background-process blocker by descendant-workdir match
- stale registration prune path
- post-rebind cleanup failure reporting without rolling session back
- safe branch delete only when inferable/`git branch -d` succeeds

### Workstream 7: Verification

Primary goal:

- prove the feature family, not just individual happy paths

Expected coverage:

- metadata + git parser tests
- runtime retarget/rebind tests
- reminder coalescing and compaction-generation reinjection tests
- setup-hook async success/failure/timeout tests
- delete blockers for sessions + background shells
- TUI slash-command/status coverage
- full build via `./scripts/build.sh --output ./bin/builder`

## Implementation Caveats

Current code has several concrete seams that must be addressed explicitly during implementation:

1. Live runtime/tool cwd is currently bound to the workspace root captured at runtime construction in `server/runtimewire/wiring.go`, `server/runtimewire/tool_registry.go`, `server/tools/shell/tool.go`, and `server/tools/shell/exec_command_tool.go`. Worktree retarget cannot be metadata-only.
2. `server/session/types.go` still persists only `WorkspaceRoot`, while metadata owns `worktree_id` and `cwd_relpath`. Runtime code still reads `store.Meta().WorkspaceRoot` in several places, including meta-context and reviewer request assembly.
3. `server/sessionlifecycle/service.go` currently allows `RetargetSessionWorkspace` without controller-lease checks, unlike transition mutations. Worktree switch/delete must align with active-session control semantics.
4. Prompt assets now require dynamic rendering for branch/cwd/path fields, but the prompt package currently only has one special-case text renderer for `{{builder_run_command}}`.
5. Existing UI status already projects `SessionExecutionTarget`, but other workspace-root keyed UI caches and path-reference state may need explicit reset when the execution target changes.

## Recommended File/Package Shape

- `shared/serverapi/worktree.go`
- `server/worktree/service.go`
- `server/worktree/service_test.go`
- `server/worktree/git.go`
- `server/worktree/git_test.go`
- `server/metadata/...` migrations and query updates as needed
- `cli/app/commands/...` for alias-aware slash registry changes
- `cli/app/...` worktree flow controller/rendering

## Deliberate Deferrals

- generic server-driven command-form/wizard framework
- project-wide worktree picker spanning multiple workspaces
- force-delete UX
- remote filesystem browse/create UX
- background git-topology watch/sync
- auto-switching busy sessions after queued completion

These are valid future follow-ups, but they should not block the core issue slice.

## Acceptance Summary

Issue #68 should be considered complete when:

- `/worktree` and `/wt` exist
- Builder can create a git worktree with optional new branch creation
- Builder can optionally run a post-create setup hook
- Builder switches the current session to the new/existing worktree without creating a new session
- Builder can list and inspect current-workspace worktrees
- Builder can safely delete a worktree with explicit confirmation
- `/status` shows worktree context when present
- multi-session safety rules are enforced for destructive actions
