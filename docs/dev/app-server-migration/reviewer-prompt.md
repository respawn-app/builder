You are acting like a very strong staff/principal engineer or PhD advisor reviewing a large architectural migration. Be rigorous, opinionated, and explicit. Challenge weak assumptions. If any locked decision appears wrong, risky, underspecified, internally inconsistent, or likely to create long-term pain, say so directly.

Do not optimize for politeness over correctness.

## Mission

Review the current `builder` application-server migration docs, improve them, answer unresolved questions where you can, identify hidden risks, and produce a practical implementation plan.

You are allowed and encouraged to do internet research where useful.

## Project Context

Repository: `builder-cli`

If this prompt is being used outside the original local environment, treat all file references below as repo-root-relative paths. If the files are attached directly instead, prefer the attached copies over filesystem assumptions.

This repo is `builder`, currently a minimal terminal coding agent focused on output quality, speed, and professional workflows.

Current product philosophy:

- minimal restrictions on model behavior,
- extensible/composable architecture,
- transparent agent activity,
- long-running work focus,
- no fluff/fancy UI.

Current stack:

- Go
- Bubble Tea TUI
- no TypeScript in core app

Current high-level repo layout:

- `cmd/builder/main.go`: CLI entrypoint
- `internal/app`: startup orchestration, auth gating, session selection, top-level UI composition
- `internal/runtime`: agent step loop, retries, transcript assembly, tool orchestration, interrupts
- `internal/session`: persistence (`session.json`, `events.jsonl`) and resume/list primitives
- `internal/tools`: tool contracts and concrete tools (`shell`, `patch`, `ask_question`)
- `internal/llm`: model-facing contracts and provider/client adapters
- `internal/auth`: auth state and OAuth/API-key plumbing
- `internal/tui`: mode-specific rendering/helpers
- `internal/config`: persistence root/workspace container resolution and app-level paths
- `internal/actions`: typed action registry scaffold
- `prompts`: embedded prompts
- `docs/dev/decisions.md`: source of truth for current major product/architecture decisions

Current system state:

- The app is still a monolithic CLI/TUI.
- Runtime, persistence, tool execution, auth, and presentation are tightly coupled in-process.
- Existing functionality must be preserved through the migration.
- We are still pre-implementation for the server split. No code has been written for the migration yet.

## Migration Goal

`builder` should evolve from a monolithic CLI into a single-process application server with attachable frontends.

The first frontend is CLI, but future frontends may include desktop, web, remote clients, etc.

The server should own runtime state and execution. Frontends should be replaceable clients.

## Primary Docs To Review

Start with these files:

- `docs/dev/app-server-migration/requirements.md`
- `docs/dev/app-server-migration/locked-decisions.md`
- `docs/dev/app-server-migration/session-run-model.md`
- `docs/dev/app-server-migration/behavior-preservation.md`
- `docs/dev/app-server-migration/open-questions.md`
- `docs/dev/app-server-migration/command-ownership.md`
- `docs/dev/app-server-migration/plan.md`

Then cross-check against:

- `docs/dev/decisions.md`
- `AGENTS.md`

You may inspect the codebase if needed to ground your advice in reality.

## Current Locked Migration Decisions

These are the major decisions already captured in the docs. If you think any are wrong, you should still challenge them, but be explicit about whether you recommend changing them now or carrying them as intentional debt.

- Primary protocol: JSON-RPC 2.0 over WebSocket.
- Single protocol version for the whole frontend/server contract.
- Handshake returns protocol version plus capability flags.
- Minimal extra HTTP surface is acceptable for health/auth/bootstrap.
- Dedicated health/readiness endpoint should exist outside the JSON-RPC/WebSocket surface.
- Method taxonomy should be resource-oriented: `project.*`, `session.*`, `run.*`, `process.*`, `approval.*`, `ask.*`, `subscription.*`, `system.*`, etc.
- Read/query APIs should use typed resource/view methods rather than one generic query endpoint.
- Frontend submissions should use structured request objects from day one.
- Submission shape should be a generic user-intent/request envelope rather than one RPC shape per submission style.
- Incompatible protocol versions should fail explicitly.
- Every mutating request carries `client_request_id` and is expected to be idempotent within scope.
- `project` is the primary top-level server resource.
- Each project is a durable server-local registration that permanently maps 1:1 to exactly one repository, one canonical workspace root, and one durable project/session container.
- Repository identity is part of the canonical project record, but protocol identity remains the opaque `project_id` rather than repo metadata.
- Persistence layout remains server implementation detail rather than protocol contract.
- Equivalent or symlinked path variants should canonicalize to the same project registration.
- Project registration is explicit; opening an unseen path must not implicitly create a project.
- Project root is immutable after registration.
- The server may host multiple projects and multiple sessions concurrently.
- v1 supports at most one active primary run per session.
- Server owns session persistence, agent execution, tool execution, background processes, provider credentials, and guarded-action policy enforcement.
- Frontends do not execute agent tools locally.
- Frontends own presentation and slash-command catalogs.
- The server should not provision built-in slash commands.
- `review` is frontend-owned and composed from generic server capabilities, not a dedicated server feature.
- `/status` should be backed by structured server data.
- `/ps` should be backed by first-class server process resources.
- `/new`/`/resume` are frontend affordances over server session operations.
- `/thinking` and `/fast` map to server-native session-wide live configuration operations.
- `/supervisor` and `/autocompaction` map to server-native session policy/configuration operations.
- `/exit` stays frontend-local.
- Session parent/child lineage is server-persisted.
- Child linkage should be set atomically at session creation time.
- Multiple frontends may control the same session; mutating commands serialize per session.
- Asks and approvals are first-class concepts; server blocks/enforces, frontend responds.
- Any attached frontend with access to a session may answer asks/approvals; first authoritative answer wins.
- Session attachment and event subscription are separate explicit protocol steps.
- `attach` should return only acknowledgment plus minimal attached-resource metadata, not snapshots.
- Project attachment establishes context only; project/session index data comes from explicit queries.
- Subscriptions are resource-scoped.
- Subscriptions are live-only; initial state comes from explicit queries.
- Typed hydration views are the source of truth for initial render and reconnect.
- Normal reconnect should refetch fresh snapshot/state.
- Replay and catch-up are best-effort within retention windows, not the standard reconnect path.
- Cursor expiry should return an explicit error and force snapshot refetch/resubscribe.
- CLI local discovery should use a well-known local control endpoint/socket plus compatibility handshake.
- Compatibility should be established through a dedicated handshake before other methods.
- Server binds locally by default; remote listener configuration is explicit and off by default.
- If CLI started an embedded local server, exit should prompt for lifecycle instead of assuming a policy.
- Embedded-server exit prompt should be neutral, with no recommended default.
- Project removal/archive is out of scope for this migration.

## What I Want From You

Please do all of the following.

### 1. Audit The Current Docs

- Find contradictions, hidden assumptions, hand-wavy areas, and likely failure modes.
- Identify any places where the docs say something nice-sounding but operationally ambiguous.
- Identify where “preserve all existing functionality” is not yet concretely enforced enough.
- Check whether any “open question” is actually a planning blocker disguised as a later detail.
- Check whether any “locked decision” is under-justified or likely wrong.

### 2. Improve The Requirements And Decisions

- Propose concrete improvements to `requirements.md`, `locked-decisions.md`, `open-questions.md`, and `command-ownership.md`.
- Tighten wording where the current docs are vague.
- Remove fake uncertainty where the answer is obvious.
- Add missing requirements if the migration would be dangerous without them.

### 3. Produce A Serious Migration Plan

Design a phased migration plan from the current monolith to the target app-server architecture.

I want:

- explicit phases,
- cut lines between current packages/modules and future boundaries,
- suggested intermediate states that keep the app working,
- risks per phase,
- rollback/fallback points,
- what can be parallelized vs what must be sequential,
- how to keep the CLI functional throughout.

Do not hand-wave “just extract a server.” I want a credible incremental path.

### 4. Propose A Target Architecture

Please propose a concrete target architecture, including:

- server subsystems,
- frontend/client layer,
- protocol boundary,
- session/project model,
- event model,
- query model,
- approval/ask lifecycle,
- process model,
- auth boundary,
- persistence/storage boundaries,
- how headless mode and subagent flows should map into the new architecture.

### 5. Review The Protocol Choice

Critique the current choice of JSON-RPC over WebSocket.

I want you to evaluate:

- whether it is still the best fit,
- what the main pitfalls are,
- how it compares to alternatives like SSE+HTTP, Connect/gRPC, or plain HTTP with long polling / streaming,
- whether the chosen explicit snapshot/query + live subscription split is a good design,
- whether the current method taxonomy and resource model are coherent.

If you think the chosen protocol is wrong, say so and explain why.

### 6. Do Internet Research

Research relevant examples/patterns from real systems where helpful. For example:

- architectures that split a local CLI/TUI from a long-lived app server,
- JSON-RPC over WebSocket patterns,
- event-stream plus snapshot/query designs,
- concurrency models for multiple clients controlling the same resource,
- agent/tooling systems that separate runtime from frontend,
- local daemon discovery/handshake approaches,
- durable session/project identity models.

Do not just dump links. Extract the lessons and apply them to this repo.

### 7. Answer The Hard Questions I Still Have

Please answer these as directly as you can:

1. What are the biggest architectural risks in the currently locked decisions?
2. Which current locked decisions would you keep unchanged?
3. Which current locked decisions would you change immediately before planning?
4. What is the cleanest incremental cut line from today’s codebase?
5. What is the most likely place this migration will accidentally re-couple frontend and server again?
6. What is the most dangerous source of hidden complexity: auth, persistence, event model, process control, or CLI compatibility?
7. Is the `project` model well chosen, or is it too rigid / too path-centric / too storage-centric?
8. Is the current reconnect model right, or are we underestimating the pain of not making replay the normal path?
9. Are we under-specifying how existing slash-command-heavy CLI behavior maps into a clean frontend architecture?
10. What acceptance criteria are missing that would actually prove the CLI is now just a frontend?

### 8. Tell Me What I’m Not Asking

I want a section specifically called `Blind Spots`.

Use it to tell me:

- what the current docs are not even talking about,
- what migration hazards I am likely underestimating,
- what problems will only become obvious halfway through implementation,
- where the current design is elegant in theory but risky in practice.

## Desired Output Format

Please structure your answer like this:

1. `Executive Summary`
2. `Critical Concerns`
3. `Recommended Changes To Current Docs`
4. `Target Architecture Proposal`
5. `Phased Migration Plan`
6. `Protocol Review`
7. `Answers To Open Questions`
8. `Blind Spots`
9. `Recommended Next Actions`

If useful, include:

- concrete API/resource suggestions,
- acceptance-test matrix suggestions.

## Additional Instructions

- Do not be passive.
- Do not merely summarize the current docs.
- Do not assume the existing decisions are correct just because they are written down.
- If you think we are about to make a bad architectural choice, say so clearly.
- Distinguish between: `must change now`, `acceptable for now`, and `defer safely`.
- If internet research changes your view, say that explicitly.
