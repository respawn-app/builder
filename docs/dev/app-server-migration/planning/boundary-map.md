# App Server Migration: Boundary Map

Status: initial repo-grounded cut analysis

This document maps the current codebase onto the first intended frontend/server boundary.

It is not a final package design. It is the starting point for Phase 1 extraction.

## Current Composition Path

The current high-level flow is:

- `cmd/builder/main.go`
  - CLI parsing for interactive and headless entrypoints
- `internal/app/app.go`
  - app-level composition root for both interactive and headless flows
- `internal/app/bootstrap.go`
  - bootstrap root for config, auth manager, onboarding, background shell manager, and fast-mode state
- `internal/app/launch_planner.go`
  - session/runtime planning root for store open/create, effective settings, tool selection, and runtime preparation
- `internal/app/session_lifecycle.go`
  - interactive session loop and cross-session transitions
- `internal/app/runtime_factory.go`
  - runtime, auth, tools, background shell manager, reviewer client, and event bridge wiring
- `internal/app/ui_runtime_adapter.go`
  - runtime event to TUI projection coupling

This confirms that `internal/app` is currently both composition root and architectural knot.

## Current Package Direction

## Strong Server-Side Candidates

These packages are already aligned with the future server authority boundary:

- `internal/runtime`
  - agent execution, step loop, compaction, reviewer pipeline, run-state transitions, background event handling, status inspection
- `internal/session`
  - persisted session store, event log, prompt history, forking, continuity metadata
- `internal/tools`
  - tool contracts and implementations, shell manager, patch machinery, ask-question tool plumbing
- `internal/llm`
  - provider clients and model/provider capability logic
- `internal/auth`
  - provider auth state and credential plumbing
- `internal/transcript`
  - transcript-specific structures/codecs used to represent durable conversational state

## Strong Frontend-Side Candidates

These areas should end up on the frontend side of the boundary:

- `internal/tui`
  - rendering surface and transcript projection/rendering details
- frontend parts of `internal/app`
  - input handling, slash-command picker, overlays, navigation, startup selection UX, runtime-to-view translation that should later become client-state translation
  - likely includes `ui*.go`, `session_picker.go`, `auth_picker.go`, `auth_success_screen.go`, `onboarding_*.go`, `mouse_sgr.go`, and `terminal_bell.go`

## Mixed / Needs Splitting

- `internal/app`
  - currently mixes startup/auth gating, runtime wiring, session lifecycle, command translation, UI projection, and direct runtime coupling
- `cmd/builder/main.go`
  - should eventually become mostly CLI-shell and client/bootstrap entrypoint logic
- `internal/config`
  - likely splits into server-owned persistence/runtime config versus frontend bootstrap and presentation config
- `internal/actions`
  - likely evolves toward frontend command catalog/request translation or becomes thinner if frontend command mapping consolidates elsewhere

Close-to-server but currently somewhat mixed:

- `internal/config`
  - broadly server-oriented today, but may later need a smaller frontend bootstrap/view subset
- `internal/auth`
  - auth state/refresh logic is server-oriented, while some browser/callback flow pieces are more UX-facing

## Highest-Risk Re-Coupling Hotspots

These are the places most likely to silently preserve the monolith under a different outer shape:

## 1. Runtime Wiring In `internal/app/runtime_factory.go`

Why it is dangerous:

- it directly composes runtime engine, auth, tools, reviewer client, shell manager, ask bridge, event bridge, and session store
- it is the easiest place for future CLI-only shortcuts to keep leaking into runtime concerns

Required outcome:

- this logic must migrate behind a transport-neutral server application-service boundary and server composition root

## 2. Session Loop In `internal/app/session_lifecycle.go`

Why it is dangerous:

- it currently owns frontend flow and also drives direct store/runtime transitions across sessions
- slash-command-driven navigation and session creation/resume/fork behavior are concentrated here

Required outcome:

- frontend session flow must use client-facing session/project operations instead of direct store/runtime orchestration

## 3. UI Runtime Adapter In `internal/app/ui_runtime_adapter.go`

Why it is dangerous:

- it assumes direct access to `runtime.Event`, engine snapshots, and chat snapshots
- it can become the loophole through which the CLI keeps privileged runtime knowledge

Required outcome:

- frontend state updates must eventually depend on client protocol/resource models, not direct runtime package types

## 4. Status / Process Surfaces In `internal/app/ui_status*.go` And `internal/app/ui_processes.go`

Why it is dangerous:

- these are high-value UX surfaces that will tempt direct runtime inspection shortcuts
- they are rich enough that a weak boundary would immediately leak server internals

Required outcome:

- these views must be powered by typed hydration reads and process resources, not direct runtime inspection

## 5. Launch Planning In `internal/app/launch_planner.go`

Why it is dangerous:

- it already acts like a service boundary, but is still embedded inside the monolithic app package
- it mixes store/open-create logic, effective settings resolution, and runtime preparation with app-shell concerns

Required outcome:

- this should become part of the first transport-neutral server application-service seam rather than staying trapped in `internal/app`

## 6. Status Gathering In `internal/app/ui_status.go`

Why it is dangerous:

- it reaches across runtime state, auth state, git/filesystem/environment reads, and remote usage HTTP
- this is exactly the sort of rich view that can drag direct server-internal access back into the frontend

Required outcome:

- status must eventually become a frontend composition over typed server-backed reads rather than direct engine/auth/environment inspection

## First Transport-Neutral Service Boundary

The first cut should be expressed as application use cases, not RPC methods.

The best current starting seam is the already-headless execution path rather than the interactive UI loop.

Why:

- `builder run` already exercises a non-TUI flow
- it has smaller surface area than the interactive session lifecycle
- it provides immediate value for proving a transport-neutral runtime boundary without first untangling Bubble Tea UX

Minimum service groups:

## Project Service

Owns:

- register project
- list projects
- get project overview
- resolve canonical project for root path

## Session Service

Owns:

- create session
- list sessions for project
- get session overview
- attach to session context
- rename session
- fork/create child session with lineage
- load transcript page/main view

## Run Service

Owns:

- submit user request
- interrupt active run
- inspect active or historical run
- report busy state
- manage session-scoped live settings such as thinking/fast
- trigger compaction

## Process Service

Owns:

- list processes
- inspect process
- kill or control process
- read or stream process output

## Approval / Ask Service

Owns:

- list pending approvals/asks
- answer approval
- answer ask
- expose authoritative status and race outcomes

## System / Attachment Service

Owns:

- handshake and capability reporting
- attach/detach semantics
- discovery/bootstrap support
- frontend connection tracking if needed

## Recommended Phase 1 Package Direction

This is the initial split to aim for, not a final architecture decree.

- keep server-side internals under existing packages where possible:
  - `internal/runtime`
  - `internal/session`
  - `internal/tools`
  - `internal/llm`
  - `internal/auth`
- introduce a transport-neutral server application-service layer:
  - `internal/serverapi` or equivalent
- introduce a client-facing abstraction used even in loopback mode:
  - `internal/client`
- reserve wire types and transport adapters for later:
  - `internal/protocol`
- split `internal/app` into:
  - CLI/frontend composition and UX glue
  - server composition/bootstrap glue

## First Extraction Candidate

The first transport-neutral boundary should likely wrap the current headless path built from:

- `internal/app/run_prompt.go`
- `internal/app/launch_planner.go`
- `internal/app/runtime_factory.go`
- `internal/runtime/engine.go`

Suggested first use cases:

- `OpenOrResumeSession`
- `SubmitUserMessage`
- `GetSnapshot`
- `InterruptSession`

Suggested first collaborators behind that service boundary:

- config resolver
- session repository
- runtime factory
- ask handler
- event stream / snapshot reader

This is the smallest boundary with real product value and the least immediate UI churn.

## Phase 1 Success Condition

Phase 1 is successful only when the CLI can perform migrated flows through the client-facing boundary without importing runtime/session/tools/auth internals directly.

That is the first real proof that the new architecture exists.
