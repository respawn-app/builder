---
name: release-notes-writing
description: Write or update Builder release notes/changelogs, especially GitHub Releases. Use when requests mention release notes, changelog, GitHub release body, generated notes cleanup, or a version like v1.1.1.
---

## Goal

Turn generated commit/PR notes into release notes that help engineers quickly understand what changed, why they should care, and whether they need to do anything after upgrading.

The style is plain, confident, technical, and user-facing. It is not marketing copy and not a commit dump.

## Triggers

Use this skill when the user asks for any of these:

- "write release notes"
- "clean up generated changelog"
- "update GitHub release body"
- "make release notes for vX.Y.Z"
- "do the same for 1.1.1"
- "collapse old changelog in details"

Example requests:

- "Rewrite the v1.1.0 GitHub release notes and keep the generated notes collapsed."
- "Make the changelog for 1.2.0 readable."
- "Update https://github.com/respawn-app/builder/releases/edit/vX.Y.Z."

## Workflow

1. Keep drafts outside the repo unless the user explicitly asks for a committed `.md` file.
   - Use `/tmp/builder-release-notes/CHANGELOG-X.Y.Z.md`.
   - Feed that file directly to `gh release edit --notes-file`.
   - Do not add temporary release-note files or links to the repository.

2. Gather source material from the release and the tag range.
   - Current release body:
     ```bash
     gh release view vX.Y.Z --repo respawn-app/builder --json body,url,tagName,publishedAt | jq -r '.'
     ```
   - Merge PRs:
     ```bash
     git log --oneline --decorate vPREV..vX.Y.Z --merges --first-parent
     ```
   - PR summaries:
     ```bash
     gh pr view <number> --repo respawn-app/builder --json title,body,mergedAt
     ```
   - Commit list only to catch user-visible fixes that PR summaries miss:
     ```bash
     git log --oneline --no-merges vPREV..vX.Y.Z
     ```

3. Categorize changes by user-visible outcome.
   - Keep: new workflows, commands, config behavior, UI changes, reliability changes users can observe, upgrade warnings.
   - Collapse: development-only fixes, test changes, review follow-ups, branch stabilization, refactors that do not change user experience.
   - Translate implementation terms into user terms.

4. Preserve the generated changelog as a collapsed details section at the end when requested or when replacing an existing generated GitHub release body.
   - Use:
     ```md
     <details>
     <summary>Original generated changelog</summary>

     ...

     </details>
     ```
   - Keep a blank line after `<summary>`.

5. Verify the GitHub release after editing.
   ```bash
   gh release edit vX.Y.Z --repo respawn-app/builder --notes-file /tmp/builder-release-notes/CHANGELOG-X.Y.Z.md
   gh release view vX.Y.Z --repo respawn-app/builder --json body,url | jq -r '.url, "---", .body'
   git status --short
   ```

## Minimal End-To-End Example

```bash
mkdir -p /tmp/builder-release-notes
$EDITOR /tmp/builder-release-notes/CHANGELOG-1.2.0.md
gh release edit v1.2.0 --repo respawn-app/builder --notes-file /tmp/builder-release-notes/CHANGELOG-1.2.0.md
gh release view v1.2.0 --repo respawn-app/builder --json body,url | jq -r '.url, "---", .body'
git status --short
```

## Structure

Use this shape by default:

```md
# Builder X.Y.Z

One short paragraph describing the release in user terms.

## Highlights

### Feature Or Theme

Plain explanation. Include commands or config only when they help users act.

## Compatibility Notes

- Upgrade note if any.
- Migration note if any.
- Behavior caveat if any.

<details>
<summary>Original generated changelog</summary>

...

</details>
```

For patch releases, keep it shorter:

```md
# Builder X.Y.Z

Builder X.Y.Z is a focused follow-up release for [main themes].

## Highlights

### Concrete Fix Theme

What users will notice.

## Compatibility Notes

- No manual migration is required.
```

## Tone

- Write like a senior engineer explaining the release to another engineer.
- Prefer concrete user outcomes over implementation labels.
- Use short sections with natural prose and small bullet lists where the content is inherently list-shaped.
- Keep confidence high and adjectives low.
- Do not oversell. No "powerful", "seamless", "revolutionary", "game-changing".
- Do not say "generated release notes are preserved for traceability" in the intro. The details block already communicates that.
- Do not mention workflow, branch cleanup, review comments, or how the notes were made.

## Translation Rules

Translate implementation jargon into observed behavior: what users can do, what they will see, what gets faster/more reliable, or what action they need to take.

| Internal term | User-facing phrasing |
| --- | --- |
| `SSOT-backed ongoing delivery` | ongoing mode uses the same committed conversation source as the rest of the app |
| `runtime lease/liveness` | whether Builder believes a session is still actively running |
| `transcript hydration` | loading restored session transcript state |
| `cursor replay` / `activity cursor` | recover from missed session activity after reconnect |
| `service registration` | installed service definition |
| `same-machine socket optimization` | local connection reuse |
| `metadata store` | saved project/session/workspace state |
| `runtime takeover` | reconnecting to or taking control of an already-running session |
| `projection` / `render intent` | how Builder displays transcript entries |

For config keys:

- Explain the user goal first.
- Show the key only in a short example.
- Link docs for precedence and exhaustive reference.

Example pattern:

Explain the user goal first:

> Builder can use different system prompt files globally, per workspace, and per headless subagent role.

Then show the smallest useful config snippet:

```toml
[subagents.fast]
system_prompt_file = "prompts/fast-agent.md"
```

Then link the owning docs page:

> See the prompt docs for precedence, placeholders, and exact config keys: https://opensource.respawn.pro/builder/prompts/

## Filtering Rules

Exclude or collapse these from the main notes:

- "address review comments"
- "unflake test"
- "cover regression"
- "format"
- "update dependencies" unless security or user compatibility matters
- "Merge pull request"
- internal package/module refactors
- branch stabilization fixes that never shipped before the release

Keep fixes when users could have hit them in the previous public version:

- broken command behavior
- wrong UI output
- resume/reconnect/session continuity bugs
- install/update/service issues
- config or prompt behavior differences

## Quality Checklist

- Main body reads naturally without knowing commit history.
- Every section answers: "What can users do or what will users notice?"
- Compatibility notes say whether action is required.
- Generated changelog is collapsed, not deleted, when preserving old notes.
- Draft stayed outside the repo unless a repo file was explicitly requested.
- `git status --short` was checked after editing.
