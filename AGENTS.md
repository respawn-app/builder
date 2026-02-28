This repository contains a minimal terminal coding agent focused on output quality, speed, and professional workflows.

The product philosophy is:
- minimal restrictions on model behavior: enabling the model to do its work unhindered.
- extensible architecture with low-friction composition that enables easy future feature additions.
- transparency of agent activity for power users, no fluff, no fancy UI tweaks, productivity and long-running work focus.

This is not a general plugin platform. The scope is intentionally narrow and quality-oriented.

## Repository Layout

- `cmd/builder/main.go`
  - CLI entrypoint for launching the Bubble Tea app.
- `internal/app`
  - Startup orchestration, auth gating, session selection, and top-level UI composition.
- `internal/runtime`
  - Agent step loop, retries, transcript assembly, tool orchestration, lock handling, interrupts.
- `internal/session`
  - Session persistence (`session.json`, `events.jsonl`) and resume/list primitives.
- `internal/tools`
  - Tool contracts and concrete tools (`shell`, `patch`, `ask_question`).
- `internal/llm`
  - Model-facing contracts and OpenAI transport/client adapters.
- `internal/auth`
  - Global auth state, method switching policy, startup gate, OAuth refresh plumbing.
- `internal/tui`
  - Mode-specific UI behavior (`ongoing`/`detail`) and rendering helpers.
- `internal/config`
  - Persistence root/workspace container resolution and app-level paths.
- `internal/actions`
  - Typed action registry scaffold for `ask_question` post-answer hooks.
- `prompts`
  - Embedded system prompt source file (`system_prompt.md`).
- `internal/tools/definitions.go`
  - Centralized compile-time tool interface declarations (name, descriptions, JSON schemas).
- `~/.builder/config.toml`
  - Home settings file (auto-created on first run) for model, thinking level, tool toggles, timeouts, and theme.

## Engineering Principles

- Keep the model unburdened.
  - Prefer runtime contracts and deterministic infrastructure over prompt complexity. Minimize extra tools.
- Design for composability.
  - New tools and handlers should require minimal boilerplate and minimal cross-cutting edits.
- Maximize API cache hits, avoid mutation of past conversation history.
- Keep TUI fast, avoid flicker, stable scroll, follow best practices.
- Never use regex-based matching, parsing, replace hacks. Never use substring-based lookup to determine information presence. Avoid brittle and fragile text/string-based logic, and develop type-safe data structures, store structured data or metadata that can reliably be extracted instead.
- Do not leave legacy fallbacks or preserve backward compatibility unless requested

## Coding Guidelines

- Prefer robust, forward-compatible, reusable, well-architected implementations over hacks, one-shot, temporary fixes or features bolted onto the existing arch.
- Keep modules cohesive; each package should have one primary responsibility.
- Introduce interfaces where they reduce coupling, not by default.
- Make failure paths explicit, observable.
- Handle and surface errors cleanly
- Maintain good user experience when adding new features (e.g. display loading states, events or ongoing processes).
- Validate invariants at boundaries (input, filesystem, process execution, API responses).
- Keep behavior configurable only when it serves real operator value.

## When designing model prompts (tool descriptions, etc.):

- Clearly explain **how** and **when** the model should use the tool in descriptions.
- Write tool schemas for parameters that specify whether it's optional, what's the default value, what the parameter does, what is its format (iso date etc)
- Minimize parameters, minimize required parameters even more.
- Handle common errors and hand the error message back to the model with a clear message and an instruction, e.g. avoid "status 124", instead: "tool timed out, try specifying a larger `timeout` param or adjusting the tool call to be faster".
- Keep frequently edited files easily accessible, like a global "system_prompt.md" or "tool_definitions.go" files.

## Important rules:

- All business logic covered by tests. Production code is written to be unit-testable. Don't ask to write or run tests
- Before handing off to the user after code changes, rebuild the binary to `./bin/builder` and make sure tests are written and green. Don't ask for confirmation to write tests and run checks.
- `docs/decisions.md` is the source of truth for locked product and architecture decisions.
- Keep this AGENTS.md file up-to-date and comprehensive. Avoid adding info that can become outdated, otherwise keep this as project guidelines, rules, and learnings for future team members. Persist info that should be preserved here.
- Do not enable terminal mouse capture modes (`tea.WithMouse*`, `EnableMouse*`) by default, because they break native text selection. If mouse behavior is required, preserve selection-first behavior and prefer non-capturing scroll paths.
- For terminal mode toggles (for example alternate-scroll `CSI ?1007 h/l`), do not rely on `tea.Printf`; emit raw ANSI control sequences via direct terminal writes (stdout) and use ordered command sequencing (`tea.Sequence`) for enter/exit transitions.

## Scroll Architecture Constraints (locked)

- No app-level wheel handling in transcript models (`tea.MouseMsg` wheel branches). Ongoing keeps terminal-native scroll/selection.
- Detail is fullscreen pager only: hide input/queued/picker; use alt-screen + alternate-scroll (`?1007h` on enter, `?1007l` on exit) with ordered sequencing and raw stdout ANSI writes (not `tea.Printf`).
- Structural transitions must hard-refresh and keep sizing/state coherent: clear on mode/rollback/session teleports, detail viewport uses full height minus status, and returning from detail after transcript growth snaps ongoing to latest.
- In normal-screen ongoing mode with history insertion enabled, committed transcript rendering is append-only via history insertion and must never be repainted in the ongoing panel.
- Ongoing panel in that mode is inline/footer-only (live streaming/error surface + controls/status), not a full-height transcript viewport.
- Session start/resume in that mode must backfill committed history once before delta insertion continues.
- Ongoing history insertion must reuse the exact same rendered chat line pipeline as visible ongoing mode (divider expansion, ANSI styling, width padding). Do not maintain a separate formatter for scrollback insertion.
- History insertion print payloads must be newline-terminated to avoid terminal line-merge/corruption artifacts during redraw.
- History insertion snapshots must exclude live `ongoing` assistant streaming content; only finalized transcript entries are insertable into scrollback history.
