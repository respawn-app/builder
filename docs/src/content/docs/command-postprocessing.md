---
title: Bash Hooks
description: Configure Builder's shell command post-processing and ship your own hook.
---

## Overview

Builder can post-process shell command output before it is shown to the model.
This is meant to reduce command noise and improve usefulness.
Builder keeps this separate from command execution itself:

- command runs normally
- Builder sanitizes output
- Builder optionally applies built-in processors and/or your hook
- The model can disable that when it needs the full output

## Config

Configure command post-processing under `[shell]` in `~/.builder/config.toml`:

```toml
[shell]
postprocessing_mode = "all" # none | builtin | user | all
postprocess_hook = "~/.builder/shell_postprocess_hook"
```

### `postprocessing_mode`

Allowed values:

- `none`: disable built-in processors and user hook
- `builtin`: run only Builder built-in processors
- `user`: run only your configured hook
- `all`: run Builder built-ins first, then your configured hook

When `postprocessing_mode = "all"`:

1. Builder built-in processors run first.
2. Your hook runs second.

## Hook Protocol

### Input

Builder sends JSON like:

```json
{
  "tool_name": "exec_command",
  "command": "go test ./...",
  "parsed_args": ["go", "test", "./..."],
  "command_name": "go",
  "workdir": "/abs/workdir",
  "original_output": "...sanitized command output...",
  "current_output": "...built-in processed output or original output...",
  "exit_code": 0,
  "backgrounded": false,
  "max_display_chars": 16000
}
```

Your hook receives both:

- `original_output`: sanitized command output before Builder semantic shaping
- `current_output`: current Builder output after built-ins, or the same as `original_output` if no built-in handled it

This lets your hook either add on top of Builder defaults or replace them completely.

Hook **must** return JSON like:

```json
{
  "processed": true,
  "replaced_output": "...new output..."
}
```

If `processed` is `false`, Builder treats the hook as a no-op.

If the hook path is missing, invalid, times out, or returns invalid JSON, Builder falls back to the next available option (built-in or none).
