---
title: Slash Commands
description: Available slash commands, how their input is parsed, and how file-backed custom commands are discovered.
---


| Command | Input | What it does |
| --- | --- | --- |
| `/exit` | none | Exit Builder. |
| `/new` | none | Start a new session. |
| `/resume` | none | Return to the startup session picker. |
| `/logout` | none | Clear auth and run login again. |
| `/compact <instructions>` | optional free-form text | Compact the current context. Trailing text is passed through as compaction instructions. |
| `/name <title>` | optional free-form text | Set the session title. Empty input resets it. |
| `/thinking <low|medium|high|xhigh>` | optional single value | Set the thinking level. Empty input shows the current level. |
| `/fast [on|off|status]` | optional single value | Toggle or inspect Fast mode. |
| `/supervisor [on|off]` | optional single value | Toggle reviewer invocation. |
| `/autocompaction [on|off]` | optional single value | Toggle auto-compaction. |
| `/ps [kill|inline|logs] <id>` | optional action + id | Open the background-process picker, or manage a specific background shell. |
| `/back` | none | Open the parent session when the current session was spawned from one. |
| `/review <what to review>` | optional free-form text | Start a fresh review session using the built-in review prompt. Trailing text is appended to the prompt body. |
| `/init <instructions>` | optional free-form text | Start a fresh initialization session using the built-in init prompt. Trailing text is appended to the prompt body. |
| `/prompt:<name>` | optional free-form text | Run a custom Markdown prompt discovered from disk. |


### 2. Built-In and Custom Prompts

Builder supports markdown file-backed custom prompt commands discovered from `.builder/prompts` or `.builder/commands`

- If the prompt body contains the exact token `$ARGUMENTS`, Builder replaces every occurrence with the trailing input.
- Otherwise, if trailing input was provided, Builder appends it to the end of the prompt body separated by a blank line.

To add a custom prompt, create a Markdown file in one of these directories:

- `<workspace>/.builder/prompts`
- `<workspace>/.builder/commands`
- `~/.builder/prompts`
- `~/.builder/commands`

## Discovery Rules

Custom commands are discovered with these rules:

1. Builder scans only top-level `.md` files in each prompt directory.
2. Nested directories are ignored.
3. The command id is derived from the filename as `prompt:<normalized_base_name>`.
4. Duplicate command ids are deduplicated by first match.
5. Discovery order is:
   1. `<workspace>/.builder/prompts`
   2. `<workspace>/.builder/commands`
   3. `~/.builder/prompts`
   4. `~/.builder/commands`
