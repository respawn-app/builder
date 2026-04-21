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

## Session Behavior

- Headless runs use the normal Builder session store and persistence model.
- A new unnamed headless session is auto-named `<session-id> subagent`.
- Continuing a session reuses the saved session state.
- `--workspace` and the usual model/config override flags still work in headless mode.

## Workspace Binding

Headless runs fail fast if the selected workspace is not already attached to a Builder project.

Use these CLI helpers to inspect or repair workspace bindings:

```bash
builder project [path]
builder attach [path]
builder attach --project <project-id> [path]
builder rebind <session-id> <new-path>
```

- `builder project` prints the project id for the bound workspace at `path` or `cwd`.
- `builder attach [path]` attaches another workspace to the project already bound to `cwd`.
- `builder attach --project <project-id> [path]` skips the `cwd` lookup and attaches explicitly.
- `builder rebind <session-id> <new-path>` retargets one session to a different workspace root.

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
  "duration_ms": 1234
}
```

On failure, JSON mode emits `status: "error"` and an `error` object instead of `result`.

---

Supported run-specific flags:

| Flag | Description |
| --- | --- |
| `--timeout` | Optional run timeout such as `30s`, `5m`, or `1h`. Default is no timeout. |
| `--output-mode` | `final-text` or `json`. Default is `final-text`. |
| `--progress-mode` | `quiet` or `stderr`. Default is `quiet`. |
| `--continue` | Continue a previous session by id. |


## Non-Interactive Constraint

Headless runs are non-interactive. They do not stop to ask the human operator questions mid-run or issue tool preambles.

That makes them suitable for background execution and automation and saves tokens, but it also means a headless run should be treated as a single unattended turn. If you continue the headless session as an interactive one (e.g. from the UI), expect the model to be less talkative going forward.

## Subagents In Interactive Builder

When the interactive Builder session uses subagents, it does so by launching separate headless Builder runs. In other words:

- there is no special in-process subagent runtime,
- subagents use the same `builder run` interface documented here,
- and the same headless session/output rules apply.

This keeps the subagent path transparent and scriptable: the feature Builder uses internally is also directly available to human users & scripting.
