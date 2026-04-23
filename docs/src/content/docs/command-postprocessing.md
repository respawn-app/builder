---
title: Command Post-Processing
description: Configure Builder's `exec_command` post-processing, user hook, and raw bypass behavior.
---

## Overview

Builder can post-process `exec_command` output before it is shown to the model.

This is meant to reduce command noise and improve usefulness. Examples:

- collapse noisy success output into a compact result
- reshape broad discovery commands into more useful summaries
- run your own local hook over command results

Builder keeps this separate from command execution itself:

- command runs normally
- Builder sanitizes output
- Builder optionally applies built-in processors and/or your hook
- `raw=true` disables semantic post-processing for that call
- tool output stays the normal plain-text command view; Builder only changes the shaped text itself

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

### `postprocess_hook`

Path to an executable/script Builder runs locally.

Builder sends JSON to stdin and expects JSON on stdout.

No sample hook file is auto-created in v1. Create any executable file you want and point config at it.

## Per-Call Raw Bypass

`exec_command` supports a `raw` parameter.

`raw=true` means:

- skip built-in post-processing
- skip user hook
- still sanitize ANSI/control bytes
- still apply generic safety truncation

Use this when you want the command's normal output instead of Builder-shaped output.

## Processing Order

When `postprocessing_mode = "all"`:

1. Builder built-in processors run first.
2. Your hook runs second.

Your hook receives both:

- `original_output`: sanitized command output before Builder semantic shaping
- `current_output`: current Builder output after built-ins, or the same as `original_output` if no built-in handled it

This lets your hook either add on top of Builder defaults or replace them completely.

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

### Output

Hook should return JSON like:

```json
{
  "processed": true,
  "replaced_output": "...new output..."
}
```

If `processed` is `false`, Builder treats the hook as a no-op.

## Broken Hook Behavior

Broken hook configuration must not fail the command itself.

If the hook path is missing, invalid, times out, or returns invalid JSON:

- `postprocessing_mode = "user"` falls back to `none`
- `postprocessing_mode = "all"` falls back to `builtin`
- `postprocessing_mode = "builtin"` does not run the hook
- `postprocessing_mode = "none"` does not run any post-processing

Builder keeps the command result itself usable and falls back to the next allowed mode.

## First Built-In Processor

The first built-in processor in the initial rollout is intentionally tiny. It exists to validate the architecture.

Initial behavior:

- direct simple `go test ...` command
- if exit code is `0`, Builder returns exact output `PASS`
- if the test command fails, Builder keeps unprocessed output

This is deliberate. The first rollout is about infra, not about shipping a giant command library immediately.

## Background `exec_command`

Builder uses the same selected processing mode for:

- initial `exec_command` inline output
- later `write_stdin` polls for that process
- completion notices for that process

So a background command does not silently switch between raw and processed modes mid-lifecycle.
