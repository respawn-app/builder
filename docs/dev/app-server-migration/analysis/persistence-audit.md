# App Server Migration: Persistence Audit

Status: Phase 0 audit updated with repo-grounded restore/adoption findings

This document records the current persistence shape that the server migration must adopt rather than accidentally break.

It is intentionally conservative. The goal is to identify durable truth, current layout assumptions, restore-critical state, and real adoption sharp edges before any storage redesign is attempted.

## Current On-Disk Shape

Primary implementation:

- `server/session/store.go`
- `server/session/types.go`
- `server/session/event_log.go`
- `shared/config/config_workspace_index.go`
- `cli/app/launch_planner.go`
- `server/runtime/message_lifecycle.go`

Current layout is rooted under the configured persistence root:

- persistence root
  - `sessions/`
    - workspace container
      - session id
        - `session.json`
        - `events.jsonl`
        - `steps.log`

Adjacent/root-level persistence files that matter during migration:

- `workspaces.json`
  - read-only legacy workspace-container adoption input for `config.ResolveWorkspaceContainer`
- `auth.json`
  - auth state adjacent to sessions, but not part of session restore

Important current layout assumptions:

- `config.ResolveWorkspaceContainer` always creates/uses `persistenceRoot/sessions/...`
- workspace container names are deterministic from canonical workspace root unless a legacy `workspaces.json` mapping wins
- `session.OpenByID` reads only under `persistenceRoot/sessions/...`
- interactive picker listing is scoped to the current workspace container, not global session discovery

## Lazy Persistence Semantics Already Matter

Session creation is not uniformly eager today.

- `session.NewLazy` allocates session identity and metadata in memory only
- the session directory does not exist on disk until a write path eventually persists it
- metadata setters are intentionally asymmetric

Current setter behavior:

- `SetName` persists immediately through `persistMetaLocked`
- `SetParentSessionID` persists immediately through `persistMetaLocked`
- `SetInputDraft` stays lazy only for the empty-string case; non-empty draft persists
- `SetContinuationContext` updates in memory but does not persist while the store is still lazy

Migration consequence:

- “session created” and “session durably exists on disk” are not the same state today
- partial adoption logic must account for sessions that only ever existed in memory
- if a future server makes creation eager, that is a behavior change for abandoned/newly-opened sessions and interactive startup flows

## Durable Files

### `session.json`

`server/session/types.go` defines durable session metadata. Current fields are:

- `session_id`
- `name`
- `first_prompt_preview`
- `input_draft`
- `parent_session_id`
- `workspace_root`
- `workspace_container`
- `continuation.openai_base_url`
- `created_at`
- `updated_at`
- `last_sequence`
- `model_request_count`
- `in_flight_step`
- `agents_injected`
- `locked.*`

This file is not optional in practice today:

- `session.FindSessionDir` treats `session.json` presence as the existence check for a session directory
- `session.ListSessions` only reads `session.json`
- `session.Open` fails without `session.json`

### `events.jsonl`

`events.jsonl` is append-oriented durable conversation history. `server/session/event_log.go` currently treats it as authoritative enough to:

- rebuild `last_sequence` on open if `session.json` drifted
- rebuild conversation freshness on open
- repair/drop a trailing truncated EOF line
- compact the file back to canonical JSONL form

Current durable envelope:

- monotonically increasing `seq`
- `timestamp`
- `kind`
- optional `step_id`
- opaque JSON `payload`

Current durable event kinds that materially affect restore/adoption are:

- `message`
- `tool_completed`
- `local_entry`
- `history_replaced`
- `prompt_history`

### `steps.log`

`cli/app/runlog.go` writes `steps.log` as append-only operational diagnostics.

It is not restore-critical today:

- session restore does not read it
- engine/UI rebuild from `session.json` + `events.jsonl`
- background/process updates written there are observability only, not durable process resources

Migration consequence: server-owned process/approval/run state cannot rely on `steps.log` surviving as the only record.

## Durable Truth Versus Derived State

### Durable Source Of Truth Today

- session identity and launch metadata in `session.json`
- ordered event history in `events.jsonl`
- locked model/provider/tool contract in `session.json.locked`
- session lineage through `parent_session_id`
- persisted prompt-history entries through `prompt_history` events, plus legacy backfill rules
- compaction checkpoints and replayable transcript state inside `history_replaced` and `message` payloads
- persisted tool result payloads keyed by tool `call_id`

### Derived Or Reconstructed State Today

- picker summaries from `session.ListSessions`
- conversation freshness from visible user `message` events
- chat snapshot / transcript ordering rebuilt by `server/runtime/message_lifecycle.go`
- tool-call render hints rebuilt from persisted `llm.Message.ToolCalls[].Presentation`
- compaction count rebuilt by replaying `history_replaced`
- current UI ongoing text/state outside persisted `local_entry.ongoing_text`

### Important Split

The current system is already split-brain in a deliberate way:

- `session.json` is authoritative for launch/resume metadata
- `events.jsonl` is authoritative for conversation/transcript history

Neither file fully replaces the other. Any migration that tries to derive one entirely from the other will lose current behavior.

## Restore-Critical State Today

This is the concrete state that existing restore paths actually consume.

### `session.json` Fields That Affect Current Behavior

- `session_id`
  - identity used by `OpenByID`, status surfaces, and session transitions
- `workspace_root`
  - reused by bootstrap resume when a session id is supplied (`PlanBootstrap`)
- `workspace_container`
  - copied into forked sessions and preserved as durable metadata even though lookup uses directories
- `continuation.openai_base_url`
  - reused on resume unless CLI flags explicitly override it (`PlanBootstrap`, `PlanSession`)
- lazy continuation nuance
  - a continuation value may exist only in memory on a still-lazy store before the session has ever been durably written
- `locked.*`
  - restored into runtime lock state and used to derive effective settings/tool availability on resume
- `in_flight_step`
  - restart path appends an interruption developer message and then attempts to clear the flag
  - if the clear succeeds, reopen normalizes the session back to `false`
  - if persisting the clear fails, `in_flight_step=true` remains durably set and recovery stays incomplete
- `agents_injected`
  - suppresses re-injection of AGENTS/environment payload after restore
- `parent_session_id`
  - used by fork/back navigation and `/status` parent-session lookup
- `input_draft`
  - restored into the interactive input buffer on reopen
- `name`
  - shown in picker/UI and used for headless auto-naming behavior
- `first_prompt_preview`
  - used by the session picker; not required to reconstruct runtime state, but part of current visible compatibility
- `updated_at`
  - drives picker ordering

### `session.json` Fields That Are Durable But Not Currently Restore-Critical

- `model_request_count`
  - persisted and incremented, but there is no current restore consumer outside the metadata file itself
- `created_at`
  - durable metadata, but not used to reconstruct runtime behavior
- `last_sequence`
  - durable cache/checkpoint rather than absolute truth because open can reconcile it from `events.jsonl`

### `events.jsonl` Payload Details That Matter

`message` is broader than a plain chat line. Current restore replays full `llm.Message` payloads, so legacy compatibility includes at least:

- message `role`
- `message_type`
- `content`
- `compact_content`
- `source_path`
- `phase`
- assistant `tool_calls`
- reasoning items

This matters because transcript-related metadata is already embedded in durable message payloads:

- `server/runtime/tool_presentation.go` injects `ToolCall.Presentation` before persistence when missing
- `shared/transcript/toolcodec` round-trips `transcript.ToolCallMeta`
- old sessions therefore already carry transcript rendering hints inside persisted `message` payloads

`tool_completed` restore depends on:

- stable `call_id`
- tool `name`
- `is_error`
- raw `output`

The join between assistant tool calls and stored tool results is `call_id`-based, so call-id stability is restore-critical even though there is no explicit foreign-key layer.

`local_entry` restore depends on:

- `role`
- `text`
- optional `ongoing_text`

Current restore intentionally keeps stored reviewer/local entries verbatim. Legacy wording is not normalized during reopen.

`history_replaced` restore depends on:

- `engine`
- `mode`
- full `items []llm.ResponseItem`

Non-rollback `history_replaced` records are not just markers; they carry the replacement transcript payload itself. They also have engine-semantic meaning on restore:

- `reviewer_rollback` replays through `restoreHistoryItems`
- other engines reapply compaction-style replacement and increment compaction state

That is a stronger coupling to current `llm.ResponseItem` shape and restore semantics than the migration docs previously captured.

## Existing Adoption Logic Already In The Repo

### Workspace Container Adoption

`config.ResolveWorkspaceContainer` already supports two realities:

- deterministic container names from canonical workspace root
- legacy `workspaces.json` mappings keyed by either the raw workspace root or canonicalized root

Current behavior is intentionally strict:

- legacy container values are validated and rejected if they try to escape the sessions root
- symlinked workspace paths converge to the same deterministic container
- there is no writer for `workspaces.json`; compatibility is read-side only

### Session Lookup Adoption

`session.OpenByID` / `FindSessionDir` currently accept:

- `persistenceRoot/sessions/<sessionID>`
- `persistenceRoot/sessions/<workspace-container>/<sessionID>`

and intentionally ignore:

- `persistenceRoot/<workspace-container>/<sessionID>`

That means old layouts are not uniformly supported. The exact accepted legacy shape is already baked into lookup code and must be preserved or replaced deliberately.

### Lazy Store Adoption

`cli/app/launch_planner.go` and `server/session/store.go` already depend on lazy-store behavior:

- interactive new sessions are created with `session.NewLazy`
- a brand-new interactive session can disappear entirely if the user never triggers a write
- headless sessions usually become durable quickly because `ensureSubagentSessionName` calls `SetName`, which persists
- continuation context can exist transiently in memory before the first durable write

This is an adoption edge for any server-side `session/create` API because today some logically-created sessions never become on-disk fixtures at all.

### Event Log Self-Healing

Open-time behavior already includes lazy repair/adoption:

- missing `events.jsonl` is recreated as an empty file
- stale `session.json.last_sequence` is reconciled from the event log
- trailing truncated EOF records are dropped and, on open, compacted away

This means opening a legacy session can mutate its files even before any new user action.

### Prompt-History Mixed Semantics

`server/session/prompt_history.go` already encodes a mixed adoption rule:

- before the first explicit `prompt_history` event, visible user `message` events backfill prompt history
- after the first explicit `prompt_history` event, later user `message` events are not auto-adopted into prompt history

This is a real compatibility contract, not just an implementation accident. The current tests lock it in.

## Fixture / Adoption Checklist

Phase 1 should not start until the migration proof has fixtures or characterization coverage for the following cases.

### Already Covered In Current Tests

- deterministic workspace container under `sessions/` and stable across repeated resolution
- legacy `workspaces.json` mapping reuse
- invalid legacy `workspaces.json` container rejection
- symlinked workspace roots resolving to the same container
- `OpenByID` scanning across workspace containers
- `OpenByID` rejecting the old root-flat layout outside `sessions/`
- prompt history from legacy visible user messages only
- prompt history after explicit `prompt_history` begins
- hybrid prompt history with legacy entries before first explicit `prompt_history`
- persisted `input_draft`
- persisted `continuation.openai_base_url`
- persisted first prompt preview from visible user message only
- compaction-summary user messages excluded from prompt preview/freshness
- legacy session with empty `first_prompt_preview` staying empty in picker
- fork persistence of `parent_session_id`, continuation, locked contract, and replay prefix
- truncated trailing EOF line ignored on read and repaired before append
- stale `last_sequence` reconciled from event log on open
- successful reopen of `in_flight_step=true` that appends interruption text and clears the flag
- failure path where clearing/persisting `in_flight_step=false` does not complete and the durable flag remains true

### Still Missing As Explicit Migration Fixtures

- session directory with `events.jsonl` present but missing `session.json`
- session directory with `session.json` present but missing `events.jsonl`
- accepted legacy layout at `sessions/<sessionID>`
- malformed `session.json` silently disappearing from `ListSessions`
- malformed event payload for one of the restore-critical kinds (`message`, `tool_completed`, `local_entry`, `history_replaced`)
- stored assistant tool call with legacy/non-empty `ToolCall.Presentation` payload
- `history_replaced` fixture with legacy `llm.ResponseItem` content that must still restore after server extraction
- persisted `in_flight_step=true` path that appends interruption text and clears successfully on reopen
- lazy `NewLazy` session with only in-memory continuation context and no durable files
- lazy `NewLazy` session made durable via `SetName` / `SetParentSessionID` before any events

## Explicit Migration Sharp Edges

### 1. `session.json` Is A Hard Visibility Gate

Current behavior:

- listing and lookup both key off `session.json`
- `events.jsonl` without `session.json` is effectively invisible

Inference from current write order:

- append paths write `events.jsonl` before rewriting `session.json`
- a crash/write failure between those steps can leave an eventful but undiscoverable session directory

Migration consequence:

- if the server introduces different atomicity rules, it must either preserve this invisibility contract or explicitly heal it

### 2. Interactive Discovery And Direct Resume Do Not Use The Same Lookup Rules

Current behavior:

- picker uses only the current workspace container directory
- explicit session-id resume uses `OpenByID` across all containers under `sessions/`

Migration consequence:

- project registry changes must not accidentally narrow session-id resume semantics to picker semantics

### 3. Prompt-History Semantics Are Versionless But Already Bifurcated

Current behavior:

- hybrid legacy/new prompt-history adoption depends on the first observed `prompt_history` event
- there is no schema/version field describing this switch

Migration consequence:

- any new server-side prompt-history model must preserve this boundary or normalize old data with an explicit rule

### 4. Transcript Rendering Metadata Already Lives Inside Stored Message Payloads

Current behavior:

- `shared/transcript` does not own files on disk
- but `transcript.ToolCallMeta` is serialized into `llm.ToolCall.Presentation` and persisted inside `message` events

Migration consequence:

- protocol cleanup cannot assume transcript hints are UI-only ephemera; they are already part of legacy persisted payloads

### 5. `history_replaced` Is Storage-Coupled To Current `llm.ResponseItem`

Current behavior:

- restore replays full replacement `items`
- `reviewer_rollback` uses a different semantic branch from other engines

Migration consequence:

- changing the internal `ResponseItem` shape without an adoption layer can break old compacted sessions even if plain message restore still works

### 6. Lazy Session Creation Is Observable Behavior

Current behavior:

- `NewLazy` sessions do not create directories/files until a later write path persists them
- different metadata setters have different persistence behavior
- `SetContinuationContext` is intentionally in-memory-only while the store is still lazy

Migration consequence:

- a server-side session/create boundary must decide whether to preserve ephemeral session creation or make session creation eagerly durable
- that choice affects adoption of abandoned sessions and parity with current interactive startup behavior

### 7. Open Can Mutate Legacy Data

Current behavior:

- opening a store may rewrite `session.json.last_sequence`
- opening a store may compact away a truncated trailing EOF line in `events.jsonl`
- missing `events.jsonl` is recreated on open

Migration consequence:

- “read-only adoption” is not fully true even today; server-side adoption logic must be explicit about which repairs are acceptable on read

### 8. `in_flight_step` Recovery Already Has Two Durable Outcomes

Current behavior:

- when reopening a session with `in_flight_step=true`, runtime appends the interruption developer message and attempts to persist `false`
- if that persist succeeds, the session is normalized and resumes cleanly
- if that persist fails, the durable flag can remain true

Migration consequence:

- resume recovery is not a single-path invariant; the server must preserve both success and failure outcomes or introduce an explicit replacement recovery contract

### 9. First Prompt Preview Hard-Cutover Is Already Locked In

Current behavior:

- `ListSessions` trusts `session.json.first_prompt_preview`
- it does not backfill from legacy events when that field is empty

Migration consequence:

- if the server wants richer session summaries, that is a behavior change and should be called out rather than treated as a transparent adoption improvement

### 10. Process/Approval/Run State Is Still Under-Modeled

Current behavior:

- durable event history captures final conversation-visible notices
- `steps.log` captures runtime diagnostics
- there is no durable first-class approval resource, process resource, or run record

Migration consequence:

- these concepts must be added additively; they cannot be assumed to exist implicitly in legacy storage

## Recommended Phase 0 Decision Direction

- keep old session directories directly readable
- prefer additive lazy adoption over eager migration
- preserve current session-id resume semantics across workspace containers
- treat `session.json` + `events.jsonl` as the compatibility baseline, not `steps.log`
- add server-owned metadata/resources incrementally instead of replacing the current session storage model in one step

## Recommended Minimum Additive Metadata

If the migration adds persistence fields, the conservative starting point is:

- `session.Meta.schema_version`
- `session.Event.event_version`
- `session.Meta.project_id`
- `session.Event.event_id` for idempotent import/sync
- `session.Event.run_id` in addition to current `step_id`
- `session.Meta.origin` such as `interactive`, `headless`, `server`, or `subagent`

For new capabilities, prefer additive event kinds or adjacent records over rewriting current message/tool semantics:

- approval lifecycle events keyed by `approval_id`
- process lifecycle/output metadata keyed by `process_id`
- run lifecycle metadata keyed by `run_id`

## Inspection Targets For Follow-Up

- `server/session/fork.go`
- `server/session/prompt_history.go`
- `server/session/conversation_freshness.go`
- `cli/app/launch_planner.go`
- `cli/app/session_lifecycle.go`
- `shared/config/config_workspace_index.go`
- `server/runtime/message_lifecycle.go`
- `server/runtime/tool_presentation.go`

This is the minimum persistence/adoption grounding needed before Phase 1 extraction begins.
