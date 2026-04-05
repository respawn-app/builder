# App Server Migration: Phase 0 Workstreams

Status: agent-ready parallel work packets

This document breaks the current Phase 0 work into bounded parallelizable tracks.

Use this after `phase-0-checkpoint.md` is accepted as the current execution checklist.

## Rules For All Workstreams

- Do not start transport or protocol implementation.
- Do not start package moves/refactors yet.
- Ground findings in the current repo, not only the migration spec.
- Prefer additive doc updates over rewriting the migration direction.
- Escalate only if a finding contradicts a locked decision or would materially change the first extraction seam.

## Workstream A: Compatibility Freeze

Goal:

Turn the current behavior-preservation claim into a repo-grounded compatibility inventory.

Primary docs to update:

- `../spec/behavior-preservation.md`
- `../spec/command-ownership.md`

Primary code targets:

- `cli/app/commands/commands.go`
- `cli/app/commands/commands_test.go`
- `cli/app/ui_input_slash_commands.go`
- `cli/app/ui_input_controller_*.go`
- `cli/app/ui_status*.go`
- `cli/app/ui_processes.go`
- `cli/app/session_lifecycle.go`

Questions to answer:

- Is the current slash-command/workflow inventory complete?
- Which workflows are currently characterized in tests and which are not?
- What user-visible behaviors are missing from the preservation matrix?
- Which busy-state behaviors are enforced by command registry versus by UI flow?

Expected output:

- updated preservation matrix
- explicit missing-coverage list
- explicit busy-safe versus busy-blocked table grounded in code/tests

Non-goals:

- no implementation plan rewrite
- no storage redesign

## Workstream B: Persistence And Data Adoption

Goal:

Make old-session adoption concrete enough that Phase 1 cannot ignore it.

Primary docs to update:

- `../analysis/persistence-audit.md`
- `phase-0-checkpoint.md`

Primary code targets:

- `server/session/*`
- `shared/config/config_workspace_index.go`
- `cli/app/launch_planner.go`
- `shared/transcript/*`

Questions to answer:

- What exact session/event data is restore-critical today?
- What legacy layout or legacy-behavior adoption edges already exist?
- What fixture set is needed to prove old-session compatibility?
- Which new metadata additions are truly minimal for project/run/approval/process support?

Expected output:

- tightened persistence audit
- fixture/adoption checklist
- list of explicit migration sharp edges

Non-goals:

- no new storage format
- no eager migration design

## Workstream C: Boundary Extraction Map

Goal:

Refine the first frontend/server seam in current-package terms.

Primary docs to update:

- `boundary-map.md`
- `plan.md`

Primary code targets:

- `cli/builder/main.go`
- `cli/app/*`
- `server/runtime/*`
- `server/session/*`
- `server/tools/*`
- `server/llm/*`
- `server/auth/*`

Questions to answer:

- What is the smallest high-value transport-neutral boundary?
- Which `cli/app` files are frontend composition, server composition, or mixed knots?
- Which interfaces/use-cases need to exist before any WebSocket work?
- Where is the most likely re-coupling path during extraction?

Expected output:

- refined boundary map
- recommended first interface/use-case list
- explicit first extraction candidate and deferred knots

Non-goals:

- no final package naming dogma
- no actual code extraction yet

## Workstream D: Black-Box Acceptance Harness

Goal:

Define how we will prove the CLI is no longer privileged.

Primary docs to update:

- `phase-0-checkpoint.md`
- `../spec/behavior-preservation.md`

Primary code targets:

- current headless path in `cli/app/run_prompt.go`
- session lifecycle and runtime event handling in `cli/app/*`
- existing test helpers relevant to app/runtime/session flows

Questions to answer:

- What is the smallest non-CLI test client we need?
- Which acceptance cases must run against embedded mode and external-daemon mode?
- Which current tests are too UI-coupled to prove the boundary later?
- How should approval/process/disconnect races be represented in acceptance tests?

Expected output:

- acceptance-harness outline
- minimum black-box client capabilities list
- phased acceptance test matrix

Non-goals:

- no protocol payload design
- no WebSocket test harness yet

## Suggested Execution Order

Recommended start:

1. Workstream B and Workstream C in parallel
2. Workstream A once B/C findings tighten compatibility assumptions
3. Workstream D once the first boundary candidate is stable enough to target

Reason:

- persistence reality and the first extraction seam constrain the rest of the plan more than raw command enumeration does.

## Handoff Format For Any Subagent

Each workstream result should come back with:

- findings
- contradictions with current docs, if any
- proposed doc edits
- unresolved blockers that actually matter now
- exact file paths inspected
