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
| <code>/thinking &lt;low&#124;medium&#124;high&#124;xhigh&gt;</code> | optional single value | Set the thinking level. Empty input shows the current level. |
| <code>/fast [on&#124;off&#124;status]</code> | optional single value | Toggle or inspect Fast mode. |
| <code>/supervisor [on&#124;off]</code> | optional single value | Toggle reviewer invocation. |
| <code>/autocompaction [on&#124;off]</code> | optional single value | Toggle auto-compaction. |
| `/status` | none | Open a read-only status overlay with progressively loaded account, session, compact git, context, config, skills, disabled skill toggles, quota details, and a session-scoped server-ownership row when this CLI owns the server. |
| <code>/ps [kill&#124;inline&#124;logs] &lt;id&gt;</code> | optional action + id | Open the background-process picker, or manage a specific background shell. |
| `/copy` | none | Copy the latest committed model final answer to the system clipboard. |
| `/back` | none | Open the parent session when the current session was spawned from one. |
| `/review <what to review>` | optional free-form text | Use the built-in review prompt. In an empty session it submits in place; otherwise it starts a fresh child review session. Trailing text is appended to the prompt body. |
| `/init <instructions>` | optional free-form text | Use the built-in init prompt. In an empty session it submits in place; otherwise it starts a fresh child initialization session. Trailing text is appended to the prompt body. |
| `/prompt:<name>` | optional free-form text | Run a custom Markdown prompt discovered from disk. |

## Input Behavior

- While you are still typing the slash-command name, the picker keeps a selected best match.
- `Enter` runs the selected command immediately, even when the name is only partially typed.
- `Tab` on a partial command autocompletes the selected command and inserts a trailing space so you can continue with arguments.
- `Tab` on an exact known command uses the normal queued-input flow, so built-in, custom, and fresh-session commands all flush after the current turn finishes.
- The picker hides `/copy` until the current session has a committed final answer to copy.
- Unknown slash commands are still sent to the model as normal user prompts.

### 2. Built-In and Custom Prompts

Builder supports markdown file-backed custom prompt commands discovered from `.builder/prompts` or `.builder/commands`

- If the prompt body contains the exact token `$ARGUMENTS`, Builder replaces every occurrence with the trailing input.
- Otherwise, if trailing input was provided, Builder appends it to the end of the prompt body.

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
