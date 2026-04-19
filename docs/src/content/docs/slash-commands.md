---
title: Slash Commands
description: Available slash commands, how their input is parsed, and how file-backed custom commands are discovered.
---


| Command | Input | What it does |
| --- | --- | --- |
| `/exit` | none | Exit Builder, same as Ctrl/CMD+C. |
| `/new` | none | Start a new session. |
| `/resume` | none | Return to the startup session picker. |
| `/login` | none | Open the auth picker again. You can re-authenticate or continue without Builder auth. |
| `/logout` | none | Alias for `/login`; clears saved auth first so re-auth starts from a clean choice. |
| `/compact <instructions>` | optional free-form text | Compact the current context. Trailing text is passed through as compaction instructions. |
| `/name <title>` | optional free-form text | Set the session title. Empty input resets it. |
| <code>/thinking &lt;low&#124;medium&#124;high&#124;xhigh&gt;</code> | optional single value | Set the thinking level. Empty input shows the current level. |
| <code>/fast [on&#124;off&#124;status]</code> | optional single value | Toggle or inspect Fast mode. |
| <code>/supervisor [on&#124;off]</code> | optional single value | Toggle supervisor invocation. |
| <code>/autocompaction [on&#124;off]</code> | optional single value | Toggle auto-compaction. |
| `/status` | none | Open a page with detailed information about the config, git, runtime, and model. |
| <code>/ps [kill&#124;inline&#124;logs] &lt;id&gt;</code> | optional action + id | Open the background-process picker, or manage a specific background shell. |
| `/copy` | none | Copy the latest committed model final answer to the system clipboard. |
| `/back` | none | Teleport back to the parent session, if present. |
| `/review <what to review>` | optional free-form text | Trigger Builder's native code review. Trailing text is appended to the prompt body. |
| `/init <instructions>` | optional free-form text | Use the built-in workspace creation prompt. Trailing text is appended to the prompt body. |
| `/prompt:<name>` | optional free-form text | Run a custom Markdown prompt discovered from disk. |

## Input Behavior

- `Enter` runs the selected command immediately, even when the name is only partially typed.
- `Tab` on a partial command autocompletes the selected command and inserts a trailing space so you can continue with arguments.
- `Tab` on an exact known command adds it into the queue. Use this to make chains of prompts and slash commands like /compact -> /review -> /prompts:commit.

### 2. Built-In and Custom Prompts

Builder supports markdown file-backed custom prompt commands discovered from `.builder/prompts` or `.builder/commands`

- If the prompt body contains the exact token `$ARGUMENTS`, Builder replaces every occurrence with the trailing input.
- Otherwise, if trailing input was provided, Builder appends it to the end of the prompt body.

To add a custom prompt, create a Markdown file in one of these directories:

- `<workspace>/.builder/prompts`
- `<workspace>/.builder/commands`
- `~/.builder/prompts`
- `~/.builder/commands`

The command id is derived from the filename as `prompt:<normalized_base_name>`. 
Duplicate command ids are deduplicated by first match, so repo-scoped commands override global command.

