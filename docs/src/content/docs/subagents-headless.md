---
title: Subagents / Headless
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

`--session` and `--continue` both target an existing session. `--continue` is the continuation-oriented form and is what Builder emits in follow-up hints and JSON metadata.

## Session Behavior

- Headless runs use the normal Builder session store and persistence model.
- A new unnamed headless session is auto-named `<session-id> subagent`.
- Continuing a session reuses the saved session state.
- `--workspace` and the usual model/config override flags still work in headless mode.

For the full list of shared overrides, see [Configuration](/config/).

## Output Modes

The default output mode is plain final text:

```bash
builder run "write a one-line summary"
```

In `final-text` mode, Builder writes the final assistant text to `stdout`. When continuation metadata is available, Builder may append a follow-up hint such as:

```text
To continue this run, execute `builder run --continue <session-id> "follow-up"`.
```

For scripting, use JSON mode:

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

## Progress And Timeouts

Headless mode is quiet by default.

If you want runtime progress events, send them to `stderr`:

```bash
builder run --progress-mode=stderr "review this package"
```

You can bound execution with `--timeout`:

```bash
builder run --timeout=10m "run a full repo review"
```

Supported run-specific flags:

| Flag | Description |
| --- | --- |
| `--timeout` | Optional run timeout such as `30s`, `5m`, or `1h`. Default is no timeout. |
| `--output-mode` | `final-text` or `json`. Default is `final-text`. |
| `--progress-mode` | `quiet` or `stderr`. Default is `quiet`. |
| `--continue` | Continue a previous session by id. |

## Non-Interactive Constraint

Headless runs are non-interactive. They do not stop to ask the human operator questions mid-run.

That makes them suitable for background execution and automation, but it also means a headless run should be treated as a single unattended turn.

## Subagents In Interactive Builder

When the interactive Builder session uses subagents, it does so by launching separate headless Builder runs. In other words:

- there is no special in-process subagent runtime,
- subagents use the same `builder run` interface documented here,
- and the same headless session/output rules apply.

This keeps the subagent path transparent and scriptable: the feature Builder uses internally is also directly available to human users.
