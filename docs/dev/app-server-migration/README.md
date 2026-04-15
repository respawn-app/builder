# App Server Migration

Status: requirements locked enough for planning, with a small set of explicit planning blockers still to close.

This file group captures the product requirements and planning baseline for migrating `builder` from a monolithic CLI into an application server with attachable frontends.

The intended target remains:

- one long-lived single-process `builder` application server,
- multiple registered projects hosted by that server,
- multiple sessions per project,
- replaceable frontends attached through a stable protocol,
- CLI as the first frontend, not a privileged architectural special case.

Current locked reconnect direction:

- reconnect is snapshot/page based,
- transcript truth comes from hydration reads,
- future large-session handling should prefer pagination and compression over stream-history recovery or delta-based transcript delivery.

The doc set now distinguishes between:

- locked requirements and architecture constraints,
- concrete preservation obligations for existing behavior,
- the minimum resource/runtime model needed before implementation,
- a phased migration plan,
- true planning blockers versus later wire-schema details.

Planning can begin from this file group, but not by ignoring the blockers. Several questions that were previously framed as later protocol details are now explicitly treated as implementation-planning blockers.

Historical note:

- `execution/questions.md` and `execution/phase-3-todo.md` are now historical Phase 3 records.
- Their workspace-scoped discovery and handshake assumptions are not current migration targets anymore.
- Phase 4+ planning is authoritative for the global-server transition, `project > workspace > worktree`, workspace-first CLI startup flow, hybrid SQLite-plus-files persistence, staged startup migration, and explicit runtime-lease semantics.

Implementation note:

- Phase 3 transport work established live prompt delivery as a dedicated prompt activity stream alongside session activity and process output; the spec docs in this folder should treat that as part of the boundary rather than as a client-side polling convention.

Files:

`spec/`

- `spec/requirements.md`: full product requirements spec for the migration.
- `spec/locked-decisions.md`: decisions already locked for this feature.
- `spec/session-run-model.md`: minimum project/session/run/process model and queue semantics baseline.
- `spec/persistence-model.md`: hybrid SQLite-plus-files storage model, source-of-truth split, and staged migration design.
- `spec/behavior-preservation.md`: compatibility inventory and proof obligations for preserving current behavior.
- `spec/command-ownership.md`: command-surface inventory and ownership/mapping across the frontend-server boundary.
- `spec/open-questions.md`: split between planning blockers and later schema questions.

`planning/`

- `planning/plan.md`: active forward-looking migration plan with only open work.
- `planning/plan-completed.md`: archive of completed phases moved out of the active plan.
- `planning/phase-4-backlog.md`: concrete Phase 4 storage implementation backlog for project/workspace/worktree, cwd resolution, runtime leases, and migration steps.
- `planning/phase-0-checkpoint.md`: executable pre-refactor checklist for Phase 0.
- `planning/boundary-map.md`: initial repo-grounded frontend/server cut analysis.
- `planning/phase-0-workstreams.md`: agent-ready parallel work packets for the current Phase 0 step.

`analysis/`

- `analysis/persistence-audit.md`: initial audit of current on-disk session/persistence shape and migration pressure points.
