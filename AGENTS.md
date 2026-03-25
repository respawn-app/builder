This repository contains a minimal terminal coding agent focused on output quality, speed, and professional workflows.

The product philosophy is:
- minimal restrictions on model behavior: enabling the model to do its work unhindered.
- extensible architecture with low-friction composition that enables easy future feature additions.
- transparency of agent activity for power users, no fluff, no fancy UI tweaks, productivity and long-running work focus.

The scope is intentionally narrow and quality-oriented.

## Repository Layout

- `cmd/builder/main.go`
  - CLI entrypoint for launching the Bubble Tea app.
- `VERSION`
  - Source of truth for release version/tag used by the release workflow and versioned builds.
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
- `docs`
  - Public Astro/Starlight documentation site. Internal product/engineering docs stay under `docs/dev`, and scratch/internal working notes stay under `docs/tmp`. Keep docs up-to-date on your own and proactively.
- `prompts`
  - Embedded prompt source files.
- `internal/tools/definitions.go`
  - Centralized compile-time tool interface declarations (name, descriptions, JSON schemas).
- `~/.builder/config.toml`
  - Home settings file (auto-created on first run) for model, thinking level, tool toggles, timeouts, and theme.
- `scripts/update-brew-tap.sh`
  - Updates the Homebrew tap formula after a tagged release.

## Engineering Principles

- Keep the model unburdened.
  - Prefer runtime contracts and deterministic infrastructure over prompt complexity. Minimize extra tools.
- Design for composability.
  - New tools and handlers should require minimal boilerplate and minimal cross-cutting edits.
- Maximize API cache hits, avoid mutation of past conversation history.
- Keep TUI fast, avoid flicker, stable scroll, follow best practices, avoid affecting scrollback buffer in ongoing mode or re-emitting full history.
- Never use regex-based matching, parsing, replace hacks. Never use substring-based lookup to determine information presence. Avoid brittle and fragile text/string-based logic, and develop type-safe data structures, store structured data or metadata that can reliably be extracted instead.
-  Breaking changes are allowed, but the UX of migration should be straightforward, e.g. a migration note for config entries or a clear error message. Ask user what migration strat they want.

## Coding Guidelines

- Prefer robust, forward-compatible, reusable, well-architected implementations over hacks, one-shot, temporary fixes or features bolted onto the existing arch.
- Keep modules cohesive; each package should have one primary responsibility.
- Introduce interfaces where they reduce coupling, not by default.
- Make failure paths explicit, observable.
- Handle and surface errors cleanly
- Maintain good user experience when adding new features (e.g. display loading states, events or ongoing processes).
- Validate invariants at boundaries (input, filesystem, process execution, API responses).
- Keep behavior configurable only when it serves real operator value.

## When designing model prompts:

- Clearly explain **how** and **when** the model should use the tool in descriptions.
- Write tool schemas for parameters that specify whether it's optional, what's the default value, what the parameter does, what is its format (iso date/number etc)
- Minimize parameters, minimize required parameters even more.
- Handle common errors and hand the error message back to the model with a clear message and an instruction, e.g. avoid "status 124", instead: "tool timed out, try specifying a larger `timeout` param or adjusting the tool call to be faster".
- Keep frequently edited files easily accessible, like the global "system_prompt.md" or "tool_definitions.go" files.

## Commit guidelines

Format: `<type>[!]: [description]`, `!` = breaking change.
Use one of these types for all commits: `feat`, `fix`, `feat!`/`breaking`/`api`, `docs`,  `refactor`,  `chore`.
Examples: `feat: add state recovery`, `feat!: change Saver API`

## Important rules:

- All business logic covered by tests. Production code is written to be unit-testable.
- Before handing off to the user after code changes, rebuild the binary to `./bin/builder` and make sure tests are written and green. Don't ask for confirmation to write tests and run checks.
- Run Go tests via `./scripts/test.sh` passing normal go test arguments.
- Releases are driven by `VERSION` and `.github/workflows/release.yml`; keep Homebrew release plumbing in sync with `scripts/update-brew-tap.sh` and the tap formula. Tap formula lives in a separate repo.
- `docs/dev/decisions.md` is the source of truth for locked product and architecture decisions, keep it up to date if user makes a new decision.
- Ongoing mode must not use `?1007`.
- Ongoing normal-buffer transcript history is append-only after startup. Once a line is emitted into scrollback, it is immutable: never retroactively restyle it, rewrite it, clear-and-replay it, or re-emit the full buffer to reflect later tool state.
- Proactively keep documentation up-to-date on your own when you make UX or other user-facing changes. Example areas that warrant a docs check include setup, startup, config, env variables, slash commands, model providers, etc.
- if user asks you to fix a github issue and you commit the fix, use 'closes #xx' in description.
- Keep this AGENTS.md file up-to-date and comprehensive. Avoid adding info that can become outdated, otherwise keep this as project guidelines, rules, and learnings for future team members. Persist info that should be preserved here.
