# App Server Migration: Command Ownership

Status: compatibility baseline

This document records how today's CLI slash-command surface maps into the frontend-server split.

The goal is not to preserve slash syntax as an architecture boundary. The goal is to preserve behavior while deciding which capabilities become first-class server resources, which remain frontend affordances, and which require explicit concurrency or idempotency rules.

This file is part of the compatibility proof obligation. A partial inventory is not enough.

## Current Built-In Slash Command Inventory

Source of truth at the time of writing:

- `cli/app/commands/commands.go`
- `cli/app/commands/file_prompts.go`
- `cli/app/session_lifecycle.go`
- `cli/app/ui_input_slash_commands.go`
- `cli/app/ui_input_queue.go`
- `cli/app/ui_slash_command_picker.go`

Built-in commands currently registered there:

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
- `/copy`
- `/back`
- `/review`
- `/init`

Also in scope for compatibility:

- frontend-owned file-backed prompt commands such as `/prompt:<name>`
- unknown slash-like input falling back to normal prompt submission

Completeness cross-check:

- The live interactive CLI registry is `NewDefaultRegistryWithFilePrompts(...)`, not only `NewDefaultRegistry()`.
- No additional built-in slash commands were found in the adjacent UI/controller files inspected for this workstream.
- Those UI/controller files do add command-surface behavior that must be preserved separately: picker visibility, partial-match execution, busy gating, queue or defer behavior, overlays, local error messages, and session-lifecycle handoff.

Current file-backed prompt-command discovery behavior:

- Search order is workspace `.builder/prompts`, workspace `.builder/commands`, global `prompts`, then global `commands`.
- Earlier hits win across duplicate normalized command ids.
- Only top-level `.md` files are loaded.
- Empty or whitespace-only files are skipped.
- The command id is normalized as `prompt:<normalized_filename>`.
- Prompt payload expansion is frontend-local and supports either `$ARGUMENTS` replacement or appended free-form arguments.

## Mapping Table

| Command / Behavior | Classification | Protocol Mapping | Notes On Idempotency / Concurrency |
| --- | --- | --- | --- |
| `/status` | Frontend view over server data | Compose from typed `session`, `run`, `process`, `approval`, `ask`, and policy reads. Do not create `status.*`. | Read-only, but not just a read RPC: current CLI also owns detail-overlay open/close, scrolling, progressive section loading, prompt-history recording, and the current `Ctrl+C` interrupt-vs-quit behavior while the overlay is open. |
| `/ps` | Server-native capability, frontend-rendered | Current on-boundary subset: `process.list`, `process.get`, `process.kill`, `process.inlineOutput`. Future process output stream access remains later work. | Reads allowed while busy. `process.kill` is mutating and must carry `client_request_id`; current `inline` is a typed read-like control request and does not mutate server state. Current CLI also owns overlay lifecycle, periodic refresh, selection retention by process id, `inline` paste into the input buffer after reading output, and log-opening fallback to `$VISUAL`/`$EDITOR` using server-provided log paths. |
| `/new` | Frontend affordance over server operations | Project selection plus session creation or attach flow. | Session creation must be idempotent when retried. Current CLI exits through `UIActionNewSession`, forwards the current session id as parent lineage, and keeps already-running background processes alive across the handoff. |
| `/resume` | Frontend affordance over server operations | Query project/session lists and attach to chosen session. | Read-heavy. No special concurrency concerns. Current CLI exits through `UIActionResume` and reopens the picker with no extra payload. |
| `/logout` | Mixed | One or more of: detach from server, clear frontend-local auth/session selection, invalidate server-owned credentials. | Must distinguish frontend-local detach from server-global credential invalidation. Current CLI clears auth, reruns auth readiness, then reattaches the same session when one exists. |
| `/compact` | Frontend alias over server capability | Session or run compaction request. | Retry-safe via `client_request_id`. Current CLI keeps it busy-blocked on `Enter`, but exact known `/compact ...` commands are still queueable through the queue-submit path and execute after the active turn drains. |
| `/name` | Frontend affordance over server metadata | Session metadata update. | Safe to retry. Current CLI allows immediate execution while busy on `Enter`, but the same command is also queueable through queue-submit keys and drains later if the user queues it instead of pressing `Enter`. |
| `/thinking` | Frontend alias over server configuration | Session-wide live configuration update or readback. | Safe to retry. Current CLI allows it while busy, and queue-submit keys can also defer exact commands through the normal queue drain. |
| `/fast` | Frontend alias over server configuration | Session-wide live configuration update or readback. | Current CLI does not mark it run-safe while busy, so busy-state rule remains explicit even though the setting itself is session-scoped. The picker can hide `/fast` when unavailable, but exact typed `/fast` still parses and produces the current error path. |
| `/supervisor` | Frontend alias over server configuration | Session runtime policy update or readback. | Safe to retry. Current CLI allows it while busy, and runtime changes can affect the completion behavior of the in-flight run. |
| `/autocompaction` | Frontend alias over server configuration | Session runtime policy update or readback. | Safe to retry. Current CLI allows it while busy and surfaces local status text that depends on current compaction mode. |
| `/back` | Frontend-local navigation | Local navigation over server-owned lineage metadata. | No protocol feature beyond lineage data. Current CLI also hides it from the picker when there is no parent session, rejects queued `/back` locally when no parent exists, and on success teleports to the parent session while seeding the parent draft from the latest committed final assistant reply without auto-submit. |
| `/review` | Frontend-owned workflow | Create child session linked to current one, submit structured review request, attach to child session. | Multi-step frontend workflow. Child-session creation and initial submission need idempotent request keys. Current CLI submits in place only when the conversation is still fresh; otherwise it exits into a new child-linked session carrying the injected prompt payload. |
| `/init` | Frontend-local built-in prompt command | Expand to structured submission against a fresh session. | Frontend-owned command catalog; server never sees raw `/init`. Fresh-session handoff semantics come from the shared prompt-command path rather than a server-specific init feature. |
| `/exit` | Frontend-local | No server-specific command beyond detach or optional server lifecycle flow. | No special protocol requirements beyond detach and optional stop flow. |
| `/prompt:<name>` | Frontend-local file-backed prompt command | Expand into structured submission with optional `client_meta`. | Server should not provision or parse these commands. Discovery precedence, normalization, empty-file skipping, and `$ARGUMENTS` expansion are all current frontend-owned compatibility behavior. |
| Unknown slash input | Frontend fallback to prompt submission | Submit as normal user input when command lookup misses. | Preserve this fallback behavior both for direct `Enter` submission and for queued-input drain, where only exact known commands are re-dispatched as commands. |

## Busy-State Source Split

Current busy behavior is split across layers and should not be flattened accidentally during migration:

- Registry-owned: parsing, command presence, descriptions, and the `RunWhileBusy` bit.
- Picker-owned: visibility filtering for `/fast` availability, `/copy` final-answer availability, and `/back` parent-session availability.
- Immediate `Enter` path-owned: busy rejection for known commands with `RunWhileBusy=false`, with the current `cannot run /<name> while model is working` error text.
- Deferred queue path-owned: exact known commands can still be queued while busy even when they are blocked on `Enter`, except for the explicit deferred guards for `/back`, unavailable `/fast`, and `/ps <action>` without a background manager.
- Queue-drain-owned: later execution order and stop conditions, including the current behavior where input-mutating queued actions such as `/ps inline <id>` stop auto-drain and leave later queued prompts pending.
- Lifecycle-owned: `UIActionNewSession`, `UIActionResume`, `UIActionLogout`, and `UIActionOpenSession` resolution after the UI loop exits.

## Previously Locked Direction

These were already established and remain true:

| Behavior | Classification | Notes |
| --- | --- | --- |
| Forking | Server-native capability | Durable session state change. |
| Compaction | Primarily server-native capability | Durable runtime or session behavior. |
| Approvals | Split | Server blocks and enforces; frontend renders controls and responds. |
| Background process control | Server-native capability | Server owns process lifecycle. |
| Back-navigation / teleport | Frontend-local | UI navigation concern. |
| `review` | Frontend-owned built-in workflow | Compose from generic server capabilities rather than a dedicated server review feature. |

## Command Catalog Boundary

- The server must not carry or provision built-in slash commands.
- Frontends may own hardcoded or file-backed slash-command catalogs.
- A frontend slash command may do one of three things:
  - call one or more server capabilities,
  - mutate frontend-local UX state,
  - expand into and send a structured submission or normal prompt.
- Custom slash commands such as file-backed prompt commands stay frontend-local.
- Frontends own parsing of file-backed or custom command metadata.
- Future command catalogs may carry richer metadata than plain text, but that does not require server-side command provisioning in v1.

## Session Lineage Boundary

- The server persists durable parent and child session lineage links and related session metadata.
- Frontends own navigation history and teleport or back behavior.

## Session Discovery Boundary

- Session discovery and listing are first-class server query surfaces with enough metadata for startup and picker UIs.

## Project Boundary

- `project` is the primary top-level container.
- Each project permanently maps 1:1 to exactly one repository, one canonical workspace root, and one durable project/session container.
- Project discovery and registration are server-native.
- Reopening the same canonical root resolves to the same project.
- Project protocol identity is an opaque `project_id`, not a filesystem path.
- Canonical root path and repository metadata are server-owned project metadata.
- Project registration requires the root path to exist and be accessible.
- Project root is immutable after registration.
- Project display names are server-stored and decoupled from filesystem folder names.
- Persistence layout remains implementation detail rather than protocol identity, even though each project still owns one durable project container internally.

## Compatibility Gaps To Close Before Coding

- The migration plan must cross-check this table against the real CLI behavior before implementation starts.
- Any newly discovered built-in command or behavior must be added here before migration work for that area begins.
- The acceptance suite must cover the behaviors listed here, not just the protocol resources behind them.

## Decision Heuristic

Use this only as a guide, not as a substitute for per-command analysis:

- If a command changes durable session, run, policy, approval, or process state, it should usually become a server capability.
- If a command is primarily navigation or local shell/app behavior, it should usually stay frontend-local.
- If a command mixes auth or approval UX with durable state, it may need split ownership.
