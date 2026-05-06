---
title: Prompts
description: Prompt customization files, precedence, placeholders, and session snapshot behavior.
---

Builder reads prompt context from global and workspace files.

## Instruction Files

`AGENTS.md` files add developer-context instructions to each session:

- `~/.builder/AGENTS.md`
- `<workspace-root>/AGENTS.md`

Builder injects these files into the conversation as developer context once per session, not as the main system prompt.

## System Prompt

System prompt files replace Builder's built-in main system prompt for new sessions.

Priority, lowest to highest:

- Built-in system prompt
- `~/.builder/SYSTEM.md`
- `~/.builder/config.toml` `system_prompt_file`
- `<workspace-root>/.builder/SYSTEM.md`
- `<workspace-root>/.builder/config.toml` `system_prompt_file`
- Selected `[subagents.<role>]` `system_prompt_file`

`system_prompt_file` paths are resolved relative to the containing `config.toml` directory unless absolute.

Builder reads and renders the selected system prompt file once when the session sends its first model request, stores the fully rendered result in the `system_prompt` session metadata, and reuses that snapshot for later requests. After a session has stored that snapshot, editing prompt files affects new sessions only. Sessions without `system_prompt` capture the current prompt file on their next model request.

## Placeholders

System prompt files use Go template syntax with these fields:

| Placeholder | Value |
| --- | --- |
| `{{.BuilderRunCommand}}` | The command prefix for launching a Builder subagent from shell. |
| `{{.EstimatedToolCallsForContext}}` | Approximate tool-call count that fits in the locked context budget. |
| `{{.EditingToolName}}` | Active manual editing tool name: `patch`, `edit`, or `shell` when no dedicated edit tool is enabled. |
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
