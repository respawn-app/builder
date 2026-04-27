---
title: Headless runs
description: Headless Builder runs, scriptable output modes, and how interactive Builder uses the same mechanism for subagents.
---

Builder supports a headless, non-interactive run mode via `builder run`.
When the interactive Builder session uses subagents, it does so by launching separate headless Builder runs. In other words:
This keeps the subagent path transparent and scriptable: the feature Builder uses internally is also directly available to human users.

For a shared local server that starts at login, use [`builder service`](../server/).

Run a single prompt:

```bash
builder run --agent fast "summarize the unstaged changes in this repo"
```

Continue an existing headless session:

```bash
builder run --continue <session-id> "<prompt>"
```

## Session Behavior

Headless runs are non-interactive. They do not stop to ask the human operator questions mid-run or issue tool preambles.
That makes them suitable for background execution and automation and saves tokens, but it also means a headless run should be treated as a single unattended turn. If you continue the headless session as an interactive one (e.g. from the UI), expect the model to be less talkative going forward.

- `--agent <role>` selects a named subagent role from `[subagents.<role>]` in `~/.builder/config.toml`.
- Subagent roles inherit the main config and then override only the keys you set in that role table.
- Continuing a session reuses most of initial config and parameters.
- The built-in `fast` role exists even without config. On exact OpenAI first-party setups, Builder heuristically switches it to a smaller/faster model profile.

## Workspace Binding

Headless runs fail if the selected workspace is not already attached to a Builder project.
This is needed to enable functionality related to project management and allows remote execution, but sometimes comes as a limitation where you want to run subagents in different repos. To fix the error, you simply need to bind a workspace (git repo, folder etc.) to a project:

- `builder project` prints the project id for the bound workspace at `path` or `cwd`. Use to learn project IDs.
- `builder attach <path>` attaches another workspace at [path] to the project already bound to `cwd`.
- `builder attach --project <project-id> [path]` attaches using the ID.
- `builder rebind <session-id> <new-path>` retargets one session to a different workspace root, for example when workspace has moved locally.

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
