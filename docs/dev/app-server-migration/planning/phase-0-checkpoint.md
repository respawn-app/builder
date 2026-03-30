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

- [ ] Verify the built-in slash-command inventory against the actual registry and any related frontend-only behaviors.
- [ ] Cross-check `../spec/behavior-preservation.md` against current tests and add any missing workflow.
- [ ] Capture explicit unknown-slash fallback behavior as a required compatibility case.
- [ ] Confirm the current busy-safe vs busy-blocked command distinctions from the command registry and UI behavior.
- [ ] Identify any non-slash workflow that is critical but not yet represented in the preservation matrix.

## 2. Persistence And Data Adoption Audit

- [ ] Document the current on-disk layout and durable files used by `internal/session`.
- [ ] Separate durable source-of-truth data from derived or cache-like data.
- [ ] Identify which new metadata is minimally required for project registry, run identity, approval state, and process state.
- [ ] Record explicit adoption risks for old `session.json` / `events.jsonl` data.
- [ ] Decide whether adoption should be direct-read compatible, lazy-upgraded, or fixture-transformed in tests.

## 3. Boundary Map And First Cut Line

- [ ] Identify current composition roots and lifecycle entrypoints.
- [ ] Identify frontend-only packages and files.
- [ ] Identify server-only packages and files.
- [ ] Identify the highest-risk re-coupling hotspots inside `internal/app`.
- [ ] Define the first transport-neutral application service surface in terms of use cases, not transport methods.

## 4. Characterization Coverage Plan

- [ ] Enumerate characterization tests to add before behavior-heavy refactors.
- [ ] Mark which current tests already cover each required workflow.
- [ ] Identify the first set of black-box acceptance tests that will need a non-CLI test client.
- [ ] Record any existing areas where current tests are UI-coupled and will need abstraction later.

## 5. Boundary Enforcement Plan

- [ ] Decide how import-boundary enforcement will work once extraction starts.
- [ ] Identify the first packages that must be prevented from importing runtime/session/tools/auth internals directly.
- [ ] Define the proof that embedded mode and external-daemon mode will use the same client boundary.

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

## Exit Criteria

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
