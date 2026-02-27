You are exploring a completely unknown repository for the first time. Your objective is to create a definitive, comprehensive AGENTS.md file at the root of this repository. This file must serve as an instant onboarding document for new human developers and future AI coding agents. The standard of success is: "I read AGENTS.md and am able to immediately develop a new feature end-to-end.". This file will serve as a "memory bank" for future developers, where important things devs should remember that are specific to the project that and not frequently changing over time will be located.

## Step 1: Repository Analysis
Before writing the document, systematically analyze the repository to determine what an engineer or agent needs to execute their immediate tasks. Assume the reader has zero prior context. Analyze the following:

- Other documentation: Search for files like .cursorrules, GEMINI.md, CLAUDE.md, README.md etc, and other existing documentation to be in the context and extract useful data from those.

- Environment & Prerequisites: Identify required language/runtime versions (e.g., .nvmrc, go.mod, .java-version), necessary background services (e.g., Docker, databases), and required environment variables (e.g., .env.example).

- Tech Stack & Architecture: Identify core languages, frameworks, architectural patterns, and dependency managers. Note high-level boundaries and where new code should reside.

- Setup & Bootstrapping: Determine the exact terminal commands required to install dependencies, initialize databases, and run the local development server.

- Testing & CI: Identify how tests are executed, where the CI pipeline is defined, and the standard commands to run the full suite and linters. Examine when and what functionality are tests written for, which rough % of coverage is currently maintained as a minimum, what are practices for testing in this repo and any testing stack specifics. 

- Repository Structure & Monorepo Strategy: Map the directories. If it is a monorepo, identify the workspace tool (e.g., Gradle multi-module, pnpm workspaces, Nx, Bazel, Turbo). Determine the command strategy: how to run commands at the root versus for a specific package/module, and note any dependency graph or ownership boundaries.

- Code Style & Conventions: Infer coding standards from the existing codebase (formatting rules, architectural boundaries, typing strictness).

- Git & PR Patterns: Inspect the git history and source control workflows to derive actual commit message formats, branch naming conventions, and pre-commit hooks.

Not everything from your analysis above will go into the resulting file, but you need this info to be able to create a truly excellent AGENTS.md file.

## Step 2: Generate AGENTS.md
Based on your analysis, generate the AGENTS.md file using GH-Flavored Markdown.
If the file already exists, then just update the existing one using the new structure and adding new information, removing stale/outdated/wrong information, but not anything useful or intentionally placed and up-to-date.

### Handling Unknowns & Gaps:

If information is missing, incomplete, or patterns cannot be confidently derived (e.g., no CI, missing test commands, undocumented commit formats), do not invent standards and do not add placeholder text like "Ask the team" or "TBD". Either omit the specific instruction entirely or explicitly document the missing piece under a "Known Gaps & Assumptions" section so agents know to work around it instead of hallucinating commands.

### Structure the file using the following sections (omit or combine if not applicable to the specific repository):

- Context: Begin the file with a short description in a few sentences of what this project is about.

- Commands/workflow guidance: Terminal commands recommended for running during development, testing, and building. This should include only commands that devs should be reminded about running, not those they already know how to run. This isn't "how to run tests in vite", as common generic knowledge should not be stored in AGENTS.md. This is guidance on how to achieve repository best practices and coding standards. Example snippet: "Run tests after making code changes using `./gradlew test` and verify the app builds using `./gradle assembleDebug`". 

- Testing instructions: Instructions on when/how to run the test suite and linters (if not yet mentioned above), and document testing practices: How and when should tests be written, and when should tests be run? (based on analysis)

- Project Layout & Module Map: High-level directory structure, dependency graph, and monorepo package boundaries. Don't explain the file locations in detail here, instead, give a "navigation & lookup cheatsheet" for devs to reduce the time they spent looking up files and noisiness of `grep` output.

- Dev environment tips: Crucial context for navigating the repository, monorepo command execution (root vs. package-level), and script usage, if anything in the repo is out of ordinary. Devs can often understand most things by looking at the actual code (e.g. how to build `go` projects), but anything not easily discoverable should be listed so they are aware.

- Architecture Notes: High-level architectural boundaries and patterns, e.g.: where to add new features, state management conventions, usage of particular uncommon frameworks, error handling practices, concurrency practices or the like.

- Code style: Specific, actionable rules inferred from the codebase and existing lint setup. Don't list common practices here that most devs already follow, only unconventional or specific, non-standard practices. For example, using `if`s without braces, or functinal programming approaches used.

- Common Workflows: Commands for migrating databases, generating code, seeding data, or running app variants. Don't include generic advice here, only project-specific things.

- Known Pitfalls / Footguns: Specific gotchas, fragile parts of the codebase, or anti-patterns to avoid. Usually this could be based on existing documentation.

- PR & Git instructions: commit formats derived from existing docs / git history, and branch naming. You could try using tools like github cli (`gh`) or other available tooling to get this info. Omit if no clear pattern exists in git history.

## Execution Rules:

- Be strictly factual and concise. Keep the file under ~1000 lines of text. Avoid fancy graphics, global headers (like "# AGENTS.md"), or eye-candy formatting, like tables, charts, and ascii file trees.
- Exclude high-level project marketing or boilerplate fluff.
- This file is instructions for devs as a reminder to maintain high standards and output quality, so use imperative tone where any guidance is issued.
- Don't put into AGENTS.md info that frequently changes, such as a full descriptive list of product features (since they can be added/removed), timestamps, update dates, any historical context or decisions, or unnecessarily specific commands like "how to execute a dependency upgrade script" (prefer more general like "useful scripts for deps update, jira are at `/scripts`") because that places a burden on maintaining the file and can go out of date and become misleading.
- Don't include generic boilerplate like common practices that most devs follow, already well established industry standards, guidance on using common popular technologies, or generic educational content (e.g. "how to make a git commit") since that can be inferred from the code and is implicitly assumed.
- Use relative paths everywhere and make no assumptions about environment or leak private data - this file will be checked into git and used by the entire team.
- If the repo is a fresh start, or there is not enough context to create even a minimal but functional AGENTS.md file, then the user wants you to create a file for a **future** project or documentation. In this case, ask the user for the missing information.
- If only some information is missing to populate a section, or you see a contradiction, and your tooling allows you to ask the user questions, then ask the user for missing pieces with best-practice defaults/suggestions. Avoid asking questions you can answer yourself. Ask questions in one batch before creating a file, not every time you find something new.
- Avoid focusing on specific code files in the file: "that one test references this data". Such information becomes stale. AGENTS.md is high-level guidance, not low-level analysis.
- When writing, don't include any "one-time setup" guidance. Assume the developer's machine is already fully set up with needed env variables, dependencies etc. This file is for fresh starts, but in an already prepared environment, so guides on how to prepare the project are redundant.
- IMPORTANT: At the end of the file, include a short but imperative instruction for developers to maintain and keep the AGENTS.md file up-to-date when they make any changes to the code, e.g. "Always keep this file up-to-date when you make changes to the project: prevent accumulation of outdated info, and add new fitting information as it becomes available", and other instructions outlined above, e.g. avoiding storing temporary info or generic guidance, so that the maintainers know what you know about how to maintain a good AGENTS.md file.

---

If your tooling allows you to create the AGENTS.md file, do it and do not duplicate it in the final response - provide the user with an absolute path to the file instead. Otherwise, if you cannot create files in this environment, then output the file content as your final response instead and no additional text like commentary or questions. Complete the task now.
