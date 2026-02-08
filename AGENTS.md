This repository contains a minimal terminal coding agent focused on output quality, speed, and professional workflows.

The product philosophy is:
- minimal restrictions on model behavior: enabling the model to do its work unhindered.
- extensible architecture with low-friction composition that enables easy future feature additions.
- transparency of agent activity for power users, no fluff, no fancy UI tweaks, productivity and long-running work focus.

This is not a general plugin platform. The scope is intentionally narrow and quality-oriented.

## Repository Layout

<No layout yet, edit this when defined>

## Engineering Principles

- Keep the model unburdened.
  - Prefer runtime contracts and deterministic infrastructure over prompt complexity. Minimize extra tools.
- Design for composability.
  - New tools and handlers should require minimal boilerplate and minimal cross-cutting edits.
- Maximize API cache hits, avoid mutation of past conversation history.
- Keep TUI fast, avoid flicker, stable scroll, follow best practices.

## Coding Guidelines

- Prefer simple, readable implementations over clever abstractions.
- Keep modules cohesive; each package should have one primary responsibility.
- Introduce interfaces where they reduce coupling, not by default.
- Make failure paths explicit and observable.
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
- Before handing off to the user after code changes, rebuild the binary (an make sure it builds) and make sure tests are written and green. Don't ask for confirmation to write tests and run checks.
- `docs/decisions.md` is the source of truth for locked product and architecture decisions.
- Keep this file up-to-date and comprehensive. Avoid adding info that can become outdated, otherwise keep this as project guidelines, rules, and learnings for future team members. Persist info that should be preserved here.



