# App Server Migration

Status: requirements locked enough for planning, with a small set of explicit planning blockers still to close.

This file group captures the product requirements and planning baseline for migrating `builder` from a monolithic CLI into an application server with attachable frontends.

The intended target remains:

- one long-lived single-process `builder` application server,
- multiple registered projects hosted by that server,
- multiple sessions per project,
- replaceable frontends attached through a stable protocol,
- CLI as the first frontend, not a privileged architectural special case.

The doc set now distinguishes between:

- locked requirements and architecture constraints,
- concrete preservation obligations for existing behavior,
- the minimum resource/runtime model needed before implementation,
- a phased migration plan,
- true planning blockers versus later wire-schema details.

Planning can begin from this file group, but not by ignoring the blockers. Several questions that were previously framed as later protocol details are now explicitly treated as implementation-planning blockers.

Files:

- `requirements.md`: full product requirements spec for the migration.
- `locked-decisions.md`: decisions already locked for this feature.
- `session-run-model.md`: minimum project/session/run/process model and queue semantics baseline.
- `behavior-preservation.md`: compatibility inventory and proof obligations for preserving current behavior.
- `phase-0-checkpoint.md`: executable pre-refactor checklist for Phase 0.
- `boundary-map.md`: initial repo-grounded frontend/server cut analysis.
- `persistence-audit.md`: initial audit of current on-disk session/persistence shape and migration pressure points.
- `command-ownership.md`: command-surface inventory and ownership/mapping across the frontend-server boundary.
- `open-questions.md`: split between planning blockers and later schema questions.
- `plan.md`: phased migration plan derived from the current requirements set.
- `reviewer-prompt.md`: standalone prompt for external review, critique, and follow-up planning.
