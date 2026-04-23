---
name: docs-writing
description: How to write Builder's public product documentation. Use when editing README or `docs/src/content/docs` pages.
---

## Goal
Public docs should be stable, terse, standalone, and user-facing. Document the product as it is, not the story of how it got there.

## Core rules

### 1. Write timelessly
- Ban temporal or transition language unless it is part of a literal product contract.
- Avoid: `currently`, `now`, `no longer`, `still`, `yet`, `for now`, `initial`, `first rollout`, `later`, `recently`, `WIP` etc.
- Prefer present-state statements: what exists, what users can do, what happens on failure.

### 2. No changelog voice
- Do not mention work done, rollout stage, validation exercises, migration status, your mistakes, or why the team implemented something.
- Bad: "the first built-in processor is intentionally tiny".
- Good: describe the actual behavior and limits.

### 3. Keep each page self-contained
- A page must explain its own subject only without duplicating content across pages.
- Cross-link only for adjacent topics or exhaustive reference after the local explanation is already sufficient.

### 4. One fact, one place
- Pick an owning page for each concept.
- Do not repeat the same explanation, defaults, caveats, or flag semantics across multiple pages.
- If another page must mention the concept, keep it to one short contextual sentence.

### 5. Compress aggressively
- Prefer one precise sentence over setup, rationale, and restatement.
- Remove throat-clearing like `This is meant to`, `In other words`, `supported interface`, `feature Builder uses internally`.
- Keep examples few and representative. Do not stack near-duplicate examples.

### 6. Prefer user-visible behavior over internals
- Include mechanics only when they change usage, configuration, output, failure mode, or compatibility.
- Omit architecture-validation notes, internal rollout notes, and implementation details that do not change user action.
Bad: "builder creates a database table to track worktrees"; "builder resolves local directories and symlinks first, then root folder files".

### 7. Avoid roadmap, editorial, and legal speculation
- Do not document guesses, opinions, or future-facing caveats such as `will be supported`, `likely never`.
- Public docs should describe shipped behavior and stable constraints.

### 8. Assume an expert reader
- Do not explain generic UI conventions, obvious command shapes, or labels the user can already see.
- Avoid describing page layout, button placement, row contents, badges, or standard keys like `Enter`, arrows, `Esc`, `q`, `PgUp`, `PgDn` unless Builder behaves unexpectedly.
- Do not add action columns or prose for self-explanatory commands.
- Document only non-obvious semantics: matching rules, side effects, defaults, blockers, failure behavior, and configuration.
- Owner pages must not have page-tour, layout, or keybinding sections unless the UI itself is unusual enough that the interaction is not discoverable.
- Do not document autofill logic, suggestion logic, field enable/disable rules, or defaulting algorithms unless they create a non-obvious operator-visible constraint.
- Do not document visible defaults, autofill, or prefilled values unless the user must know them to operate safely or avoid destructive behavior.

## Bad vs good

### Temporal wording
- Bad: `Builder no longer rewrites command output.`
- Good: `Builder preserves command execution and can post-process displayed output.`
- Bad: `For now, only OpenAI is supported.`
- Good: `Builder supports OpenAI authentication via OAuth or API key.`

### Changelog voice
- Bad: `The first rollout is intentionally tiny to validate the architecture.`
- Good: `Successful direct simple \`go test ...\` commands collapse to \`PASS\`.`

### Page-link crutch
- Bad: `More info on the Subagents page.`
- Good: explain the local subagent behavior in one or two sentences, then add a link only for adjacent detail.

### Repeated explanations
- Bad: repeating `--fast` semantics in quickstart, config, and headless with the same full paragraph.
- Good: let `headless.md` own run-mode semantics; other pages mention only that `--fast` selects the built-in fast subagent role.

### Obvious UI
- Bad: a table row `| /wt | Open the Worktrees page |`.
- Good: list only the command forms when the syntax already explains the action.
- Bad: explaining that `Enter` confirms or that a page shows badges already visible on screen.
- Good: explain how worktree target matching works, what delete blocks on, and where managed worktrees are created.

## Page patterns

### Behavior page
- Short overview.
- How to invoke or use it, but only when syntax alone is insufficient.
- Key semantics and defaults.
- Failure behavior only if operator-relevant.
- One minimal example.

### Reference page
- Stable schema or table first.
- Short notes only where needed to prevent misuse.
- Do not re-explain neighboring features at length.

## Edit checklist
- Can this sentence survive unchanged six months from now?
- Did I remove process, history, and rollout language?
- Did I duplicate anything already owned elsewhere?
- Does the page stand on its own without `see X page`?
- Can I delete words without losing meaning?
