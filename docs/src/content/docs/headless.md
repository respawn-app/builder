---
title: Headless runs
description: Headless Builder runs, scriptable output modes, and how interactive Builder uses the same mechanism for subagents.
---

Builder supports a headless, non-interactive run mode via `builder run`.

This is the supported interface for running Builder from shell scripts, tmux panes, background jobs, and similar workflows. It is also the mechanism the main interactive session uses when it launches subagents: subagents are separate headless Builder runs, not an in-process orchestration layer.

## Basic Usage

Run a single prompt:

```bash
builder run "summarize the unstaged changes in this repo"
```

Continue an existing headless session:

```bash
builder run --continue <session-id> "follow-up"
```

Use the built-in fast subagent role:

```bash
builder run --fast "scan the repo and list likely migration breakpoints"
```

## Session Behavior

- Headless runs use the normal Builder session store and persistence model.
- A new unnamed headless session is auto-named `<session-id> subagent`.
- Continuing a session reuses the saved session state.
- `--workspace` and the usual model/config override flags still work in headless mode.
- `--agent <role>` selects a named subagent role from `[subagents.<role>]` in `~/.builder/config.toml`.
- `--fast` is sugar for `--agent fast`.

## Workspace Binding

Headless runs fail fast if the selected workspace is not already attached to a Builder project.

Use these CLI helpers to inspect or repair workspace bindings:

```bash
builder project [path]
builder project list
builder project create --path <server-path> --name <project-name>
builder attach [path]
builder attach --project <project-id> [path]
builder rebind <session-id> <new-path>
builder serve [flags]
```

- `builder project [path]` prints the project id bound to `path` or the current directory.
- `builder project list` lists projects known to the current server. Output columns are project id, display name, and root path.
- `builder project create --path <server-path> --name <project-name>` creates a project and binds its first workspace root.
- `builder attach [path]` attaches another workspace to the project already bound to the current working directory.
- `builder attach --project <project-id> [path]` skips current-directory lookup and uses the explicit project id instead.
- `builder rebind <session-id> <new-path>` retargets one session to a different workspace root.
- `builder serve` starts the Builder app server and keeps serving until interrupted.

Path rules:

- In local loopback mode, command paths are local filesystem paths.
- Against a remote daemon, paths passed to `project create` or `attach` must be visible on the server machine.
- `builder serve --workspace` chooses the startup workspace root for config resolution.

Examples:

```bash
builder project
builder project list
builder project create --path /srv/repos/app --name app
builder attach ../other-checkout
builder attach --project <project-id> /srv/repos/other-checkout
builder rebind <session-id> ../moved-workspace
builder serve --workspace /srv/repos/app --model gpt-5.4-mini
```

For the full list of shared overrides, see [Configuration](../config/).

## Output Modes

The default output mode is plain final text.
In `final-text` mode, Builder writes the final assistant text to `stdout`. For scripting, use JSON mode:

```bash
builder run --output-mode=json "summarize the repo" | jq
```

JSON mode emits exactly one final object on `stdout`.

```json
{
  "status": "ok",
  "result": "...",
  "session_id": "...",
  "session_name": "...",
  "continue_id": "...",
  "continue_command": "builder run --continue ... \"follow-up\"",
  "warnings": ["..."],
  "duration_ms": 1234
}
```

On failure, JSON mode emits `status: "error"` and an `error` object instead of `result`.
If a selected subagent role emits startup warnings, `final-text` prints them above the model response and JSON mode returns them in `warnings`.

---

Supported run-specific flags:

| Flag | Description |
| --- | --- |
| `--timeout` | Optional run timeout such as `30s`, `5m`, or `1h`. Default is no timeout. |
| `--output-mode` | `final-text` or `json`. Default is `final-text`. |
| `--progress-mode` | `quiet` or `stderr`. Default is `quiet`. |
| `--continue` | Continue a previous session by id. |
| `--agent` | Select a named subagent role from `config.toml`. |
| `--fast` | Shortcut for the built-in `fast` subagent role. |

## Subagent Roles

Headless runs can select a file-defined subagent role with `--agent <role>`.

- Roles are configured under `[subagents.<role>]` in `~/.builder/config.toml`.
- Subagent roles inherit the main config and then override only the keys you set in that role table.
- The built-in `fast` role exists even without config. On exact OpenAI first-party setups, Builder heuristically switches it to a smaller/faster model profile and enables `priority_request_mode`.
- If `fast` ends up identical to the main agent config, Builder emits a warning so the calling agent can suggest tuning the config later.


## Non-Interactive Constraint

Headless runs are non-interactive. They do not stop to ask the human operator questions mid-run or issue tool preambles.

That makes them suitable for background execution and automation and saves tokens, but it also means a headless run should be treated as a single unattended turn. If you continue the headless session as an interactive one (e.g. from the UI), expect the model to be less talkative going forward.

## Subagents In Interactive Builder

When the interactive Builder session uses subagents, it does so by launching separate headless Builder runs. In other words:

- there is no special in-process subagent runtime,
- subagents use the same `builder run` interface documented here,
- and the same headless session/output rules apply.

This keeps the subagent path transparent and scriptable: the feature Builder uses internally is also directly available to human users & scripting.
