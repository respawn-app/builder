# Contributing

Builder is intentionally narrow and opinionated. We value changes that improve reliability, output quality, reviewability, and long-term maintainability. The best contributions are focused, technically coherent, and aligned with the product direction.

## Start With an Issue

External contributions should begin with an issue before a pull request is opened.

This helps avoid wasted work and gives maintainers a chance to confirm scope, approach, and fit. Once the issue has been triaged, a PR is welcome.

Changes are less likely to be accepted if they add broad configurability, plugin-style surface area, extra UI chrome, or product direction that conflicts with the repository's design principles.

## Development Setup

Current prerequisites:

- Go `1.25`
- Node `22` and `pnpm` `10` for docs work in `docs/`

If you want the repository pre-push hook locally, enable it with:

```bash
git config core.hooksPath .githooks
```

## Before Opening a Pull Request

For code changes, run:

```bash
./scripts/ci-check.sh all
```

For manual Go test runs outside the full check, use:

```bash
./scripts/test.sh ./...
```

This keeps successful runs silent while still printing the captured test log on failure.

If you changed docs under `docs/`, also run:

```bash
cd docs
pnpm install --frozen-lockfile
pnpm test
pnpm build
```

## Pull Request Expectations

Please keep pull requests small enough to review in one pass and make sure they are tied to a previously triaged issue.

A strong PR usually:

- solves one clear problem
- includes tests for behavior changes
- updates user-facing documentation when needed
- keeps `AGENTS.md` accurate when project guidance changes
- avoids unrelated cleanup in the same change set

Draft PRs are fine when they are clearly marked and linked to the issue.

## Questions

If you want to work on something and there is no issue yet, open one first. If an issue already exists, use that thread to discuss the work.

## AI Code Policy

AI-generated code is absolutely acceptable, **provided it matches the quality standards** of the repo.

Fully or mostly AI-generated PRs with no or little human review that do not adhere to the current project architecture or guidance & constraints (in short, "Slop") will be closed without notice or discussion with the author of the PR being blocked and reported.

Additionally:
- Do **not** leave "co-authored by <agent>" attributions
- Prefer to disclose the use of AI in the PR authorship
- Do **not** introduce additional AI configuration files such as `.cursorrules` to the project in an unrelated PR. Editing AGENTS.md is fine and encouraged.
- Do not leave elaborate AI-generated PR descriptions. Prompt the agent to leave succinct, readable, human-like descriptions that are to the point.
