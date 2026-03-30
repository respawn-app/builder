# App Server Migration: Command Ownership

Status: compatibility baseline

This document records how today's CLI slash-command surface maps into the frontend-server split.

The goal is not to preserve slash syntax as an architecture boundary. The goal is to preserve behavior while deciding which capabilities become first-class server resources, which remain frontend affordances, and which require explicit concurrency or idempotency rules.

This file is part of the compatibility proof obligation. A partial inventory is not enough.

## Current Built-In Slash Command Inventory

Source of truth at the time of writing:

- `internal/app/commands/commands.go`

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
- `/back`
- `/review`
- `/init`

Also in scope for compatibility:

- frontend-owned file-backed prompt commands such as `/prompt:<name>`
- unknown slash-like input falling back to normal prompt submission

## Mapping Table

| Command / Behavior | Classification | Protocol Mapping | Notes On Idempotency / Concurrency |
| --- | --- | --- | --- |
| `/status` | Frontend view over server data | Compose from typed `session`, `run`, `process`, `approval`, `ask`, and policy reads. Do not create `status.*`. | Read-only. Must work while a run is active. |
| `/ps` | Server-native capability, frontend-rendered | `process.list`, `process.get`, process control request(s), process output stream access. | Reads allowed while busy. Mutating control actions need `client_request_id`. |
| `/new` | Frontend affordance over server operations | Project selection plus session creation or attach flow. | Session creation must be idempotent when retried. |
| `/resume` | Frontend affordance over server operations | Query project/session lists and attach to chosen session. | Read-heavy. No special concurrency concerns. |
| `/logout` | Mixed | One or more of: detach from server, clear frontend-local auth/session selection, invalidate server-owned credentials. | Must distinguish frontend-local detach from server-global credential invalidation. |
| `/compact` | Frontend alias over server capability | Session or run compaction request. | Retry-safe via `client_request_id`. Current CLI keeps it busy-blocked, and that compatibility distinction must remain explicit. |
| `/name` | Frontend affordance over server metadata | Session metadata update. | Safe to retry. Should remain allowed during active run because current CLI allows it while busy. |
| `/thinking` | Frontend alias over server configuration | Session-wide live configuration update or readback. | Safe to retry. Current CLI allows it while busy, so active run should observe the new value. |
| `/fast` | Frontend alias over server configuration | Session-wide live configuration update or readback. | Current CLI does not mark it run-safe while busy, so busy-state rule remains explicit even though the setting itself is session-scoped. |
| `/supervisor` | Frontend alias over server configuration | Session runtime policy update or readback. | Safe to retry. Current CLI allows it while busy. |
| `/autocompaction` | Frontend alias over server configuration | Session runtime policy update or readback. | Safe to retry. Current CLI allows it while busy. |
| `/back` | Frontend-local navigation | Local navigation over server-owned lineage metadata. | No protocol feature beyond lineage data. Confirmation UX stays frontend-local. |
| `/review` | Frontend-owned workflow | Create child session linked to current one, submit structured review request, attach to child session. | Multi-step frontend workflow. Child-session creation and initial submission need idempotent request keys. |
| `/init` | Frontend-local built-in prompt command | Expand to structured submission against a fresh session. | Frontend-owned command catalog; server never sees raw `/init`. |
| `/exit` | Frontend-local | No server-specific command beyond detach or optional server lifecycle flow. | No special protocol requirements beyond detach and optional stop flow. |
| `/prompt:<name>` | Frontend-local file-backed prompt command | Expand into structured submission with optional `client_meta`. | Server should not provision or parse these commands. |
| Unknown slash input | Frontend fallback to prompt submission | Submit as normal user input when command lookup misses. | Preserve this fallback behavior. |

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
- Each project permanently maps 1:1 to exactly one repository, one canonical workspace root, and one durable project or session container.
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
