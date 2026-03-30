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

Any newly discovered command or behavior must be added to the compatibility inventory before implementation starts for that area.

## Workflow Inventory

The migration must preserve at least the following end-to-end workflows:

| Workflow | Preservation Requirement | Proof Direction |
| --- | --- | --- |
| Create new session | User can create a session within a project and begin work immediately. | Black-box client flow against the server boundary. |
| Resume existing session | User can list and resume sessions within a project. | Black-box client flow using typed reads and attach. |
| Prompt submission | User can submit normal prompts and receive durable transcript results. | Protocol-level submit plus transcript assertions. |
| Unknown slash fallback | Unknown slash input still reaches the model as normal user input. | CLI and black-box client characterization. |
| Built-in `/review` | Frontend can create linked child session and start review prompt flow. | Child-session lineage plus initial submission test. |
| Built-in `/init` | Frontend can start init prompt flow in a fresh session. | Fresh-session prompt flow test. |
| File-backed prompt commands | Frontend-local prompt command expansion still works without server-side slash parsing. | CLI-side characterization plus structured submission assertion. |
| Rename session | Session title updates persist and remain visible across attach/resume. | Metadata mutation and reload test. |
| Busy-safe command behavior | Commands currently allowed while busy remain supported or are deliberately reclassified. | Characterization tests against active-run state. |
| Busy-blocked command behavior | Commands currently blocked while busy still fail or defer explicitly rather than silently misbehaving. | Characterization tests with active run. |
| Status inspection | Frontend can render the equivalent of current `/status` from typed reads. | Read-model test with active and idle sessions. |
| Process inspection and control | Frontend can list, inspect, stream, and control background processes. | Process resource and output-stream tests. |
| Approval and ask flows | Guarded operations pause, surface request state, and resume deterministically after response. | Multi-client race tests. |
| Fork and lineage | Child sessions retain parent linkage and remain navigable by the frontend. | Lineage metadata and attach tests. |
| Headless continuity | Active work continues if the frontend disconnects. | Crash or disconnect test during active run. |
| Existing session adoption | Pre-migration persisted sessions remain resumable. | Fixture-based adoption test. |

## Busy-State Compatibility Baseline

From the current command registry and tests, the migration must preserve this baseline unless deliberately changed and documented:

- Allowed while busy:
  - `/status`
  - `/ps`
  - `/name`
  - `/thinking`
  - `/supervisor`
  - `/autocompaction`
- Not currently run-safe while busy:
  - `/fast`
  - `/compact`
  - starting another primary run

The new architecture may reimplement this behavior differently, but it must not erase the distinction accidentally.

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
