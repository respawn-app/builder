---
name: skill-creator
description: Create or improve Builder skills. Use when the user wants to add a new skill, update an existing skill, choose a skill scope, structure skill files, write skill frontmatter, or decide what belongs in a skill versus existing docs or source-of-truth files.
---

# Skill Creator

Use this skill to create and improve Builder skills without duplicating existing source-of-truth documentation.

## Placement

Builder discovers skills from these roots:

- `<workspace>/.builder/skills`
- `~/.builder/skills`

Use workspace skills for project-specific workflows, repository conventions, local tools, or instructions that should travel with a codebase.

Use global skills for reusable personal workflows that apply across projects.

Do not edit `~/.builder/.generated`. Builder manages that directory and overwrites it on startup. To customize a generated skill, copy it to `<workspace>/.builder/skills/<skill-name>` or `~/.builder/skills/<skill-name>` and edit the copy.

## Folder Structure

A skill is a directory with a required `SKILL.md` file:

```text
my-skill/
├── SKILL.md
├── scripts/
├── references/
└── assets/
```

Use optional directories only when they reduce context or make repeatable work deterministic:

- `scripts/`: executable or interpreter-run scripts for deterministic, repetitive, or noisy tasks.
- `references/`: longer docs the model should read only when needed.
- `assets/`: templates, examples, or static files used by the skill.

Keep `SKILL.md` as the entry point. Put trigger guidance in frontmatter, core workflow in the body, and large variant-specific detail in referenced files.

## Frontmatter

`SKILL.md` starts with YAML frontmatter:

```markdown
---
name: my-skill
description: Do a specific workflow. Use when the user asks for concrete trigger phrases or contexts.
---
```

`name` and `description` are required.

Use a stable, lowercase, kebab-case `name` unless there is an existing compatibility reason not to.

Write `description` as trigger metadata. Include what the skill does and when to use it. Mention concrete user phrases, task types, tools, files, or domains that should activate the skill. Do not bury trigger rules only in the body; Builder injects only the name, description, and path until the model opens `SKILL.md`.

## Body

Write the body as operational guidance for the model:

- Start with the goal in one short paragraph.
- Explain the decision flow and required checks.
- Point to `scripts/`, `references/`, or `assets/` only when needed.
- Prefer reusable rules over one-off examples.
- Keep source-of-truth details in their owning docs or commands; link or delegate instead of copying long references.
- Include output format only when the skill must produce a specific structure.

Avoid documenting CLI help text, public docs, API docs, or web content verbatim when the model can read the source of truth directly.

## Creation Workflow

When creating a skill:

1. Identify the scope: workspace or global.
2. Check existing skills so the new one does not duplicate or conflict with them.
3. Choose a stable directory name and frontmatter `name`.
4. Draft a trigger-focused `description`.
5. Write the smallest useful `SKILL.md` body.
6. Add optional files only when they are needed for progressive disclosure or deterministic execution.
7. Validate that the skill can be discovered and that referenced files exist.

When scope is ambiguous, recommend workspace scope for repository-specific tooling or conventions, and global scope for reusable personal workflows.
