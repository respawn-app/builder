---
title: Prompts
description: Prompt customization files, precedence, placeholders, and session snapshot behavior.
---

Builder reads prompt context from global and workspace files.

## Instruction Files

`AGENTS.md` files add developer-context instructions to each session:

- `~/.builder/AGENTS.md`
- `<workspace-root>/AGENTS.md`

Workspace instructions are included after global instructions. Builder injects these files into the conversation as developer context, not as the main system prompt.

## System Prompt

`SYSTEM.md` replaces Builder's built-in main system prompt for new sessions:

- `~/.builder/SYSTEM.md`
- `<workspace-root>/.builder/SYSTEM.md`

The workspace file takes priority. If neither file exists, Builder uses the built-in system prompt.

Builder reads and renders `SYSTEM.md` once when the session sends its first model request, stores the fully rendered result in the `system_prompt` session metadata, and reuses that snapshot for later requests. After a session has stored that snapshot, editing `SYSTEM.md` affects new sessions only. Sessions without `system_prompt` capture the current `SYSTEM.md` on their next model request.

## Placeholders

`SYSTEM.md` uses Go template syntax with these fields:

| Placeholder | Value |
| --- | --- |
| `{{.BuilderRunCommand}}` | The command prefix for launching a Builder subagent from shell. |
| `{{.EstimatedToolCallsForContext}}` | Approximate tool-call count that fits in the locked context budget. |
| `{{.DefaultSystemPrompt}}` | Builder's rendered built-in system prompt, without tool preambles. |

Example:

```md
{{.DefaultSystemPrompt}}

# Team Rules

Prefer small, reviewable commits.
```

Tool preambles are appended after the rendered `SYSTEM.md` when `tool_preambles = true` for the locked session.

## Supervisor System Prompt

`reviewer.system_prompt_file` replaces Builder's built-in supervisor system prompt:

- `~/.builder/config.toml`
- `<workspace-root>/.builder/config.toml`

The workspace config value takes priority. Builder reads the referenced file when the supervisor first runs for a session, stores the prompt with the session, and reuses that snapshot for later supervisor requests. Editing the file affects only sessions that have not run the supervisor with that override.
