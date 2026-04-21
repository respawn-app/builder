---
title: Configuration
description: Settings locations, precedence, CLI and environment overrides, and the full Builder config reference.
---

## Precedence

Builder resolves settings in this order (higher no. = higher priority):

1. Built-in defaults
2. `~/.builder/config.toml`
3. Environment variables
4. CLI flags

When you `continue` a session, Builder reuses the saved workspace root and saved continuation `openai_base_url` unless you explicitly pass `--workspace` or `--openai-base-url`.

## Locations

### Persistence root

The settings file is always:

```text
~/.builder/config.toml
```

Builder also installs a user-editable ripgrep config at:

```text
~/.builder/rg.conf
```

Builder creates `~/.builder/rg.conf` when missing and exports it to shell tools via `RIPGREP_CONFIG_PATH` only when you have not already set `RIPGREP_CONFIG_PATH` yourself.

Changing `persistence_root` does not move `config.toml`. `persistence_root` controls where Builder stores its database & auth state. The default is `~/.builder`.


## Example

```toml
model = "gpt-5.4"
thinking_level = "medium" # low, medium, high, xhigh
model_verbosity = "medium" # or "low"
theme = "auto" # or light / dark
web_search = "native"
compaction_mode = "local" # or "native" (if supported)
cache_warning_mode = "default" # detail-only unwanted model cache invalidations; or "verbose" / "off"
server_host = "127.0.0.1"
server_port = 53082

[timeouts]
model_request_seconds = 400

[tools]
shell = true
patch = true
view_image = true
web_search = true
trigger_handoff = false

[shell]
postprocessing_mode = "builtin"
# postprocess_hook = "~/.builder/shell_postprocess_hook"

[skills]
"skill name" = true

[reviewer] # aka supervisor
frequency = "edits"
timeout_seconds = 60
verbose_output = false # show in ongoing transcript
```

`server_host` and `server_port` stay the durable TCP source of truth. On Unix platforms Builder may also derive a same-machine Unix domain socket for faster local RPC, but that socket is automatic local state only: there is no extra config knob, explicit `server_host` or `server_port` overrides still dial configured TCP, LAN/remote clients still use configured TCP, and health/readiness stay on configured HTTP/TCP.

## CLI Overrides

These flags overlay settings at startup.

| Flag | Overrides | Notes |
| --- | --- | --- |
| `--model` | `model` | |
| `--provider-override` | `provider_override` | |
| `--thinking-level` | `thinking_level` | |
| `--theme` | `theme` | |
| `--model-timeout-seconds` | `timeouts.model_request_seconds` | |
| `--tools` | entire tool set | CSV replacement, not a merge |
| `--openai-base-url` | `openai_base_url` | Also affects continuation behavior |


## Reference

### Core Settings

| Key | Type | Default | Env | CLI | Description |
| --- | --- | --- | --- | --- | --- |
| `model` | string | `gpt-5.4` | `BUILDER_MODEL` | `--model` | Model name. If provider inference from the model name is not enough, set `provider_override` too. |
| `thinking_level` | string | `medium` | `BUILDER_THINKING_LEVEL` | `--thinking-level` | Provider-specific reasoning effort string. |
| `model_verbosity` | string | `medium` |  |  | Text verbosity hint for supported models. Allowed: `""`, `low`, `medium`, `high`. Unsupported models ignore it. |
| `theme` | string | `auto` | `BUILDER_THEME` | `--theme` | TUI theme. Allowed: `auto`, `light`, `dark`. `light` and `dark` force Builder's fixed palettes. `auto` or an omitted value falls back to terminal background detection. |
| `tui_alternate_screen` | string | `auto` | `BUILDER_TUI_ALTERNATE_SCREEN` |  | Alternate-screen policy. Allowed: `auto`, `always`, `never`. |
| `notification_method` | string | `auto` | `BUILDER_NOTIFICATION_METHOD` |  | Terminal notification backend. Allowed: `auto`, `osc9`, `bel`. `auto` chooses `osc9` on supported terminals and falls back to `bel`. |
| `tool_preambles` | bool | `true` | `BUILDER_TOOL_PREAMBLES` |  | Includes tool-usage preambles in the main system prompt for interactive runs. Headless `builder run` still suppresses them. |
| `priority_request_mode` | bool | `false` |  |  | Enables fast-mode requests where the provider supports them. |
| `debug` | bool | `false` | `BUILDER_DEBUG` |  | Enables global developer-oriented strictness and logging. Only use for development/debugging |
| `server_host` | string | `127.0.0.1` | `BUILDER_SERVER_HOST` |  | Exact TCP app-server host Builder will dial or listen on. Builder does not use discovery files or silent port rebinding. Same-machine Unix socket optimization, when supported, is derived automatically and does not override an explicit TCP target. |
| `server_port` | int | `53082` | `BUILDER_SERVER_PORT` |  | Exact TCP app-server port Builder will dial or listen on. Must match across clients attached to the same persistence root. Same-machine Unix socket optimization, when supported, is additive only and does not override an explicit TCP target. |
| `web_search` | string | `native` | `BUILDER_WEB_SEARCH` |  | Web search backend. Allowed: `off`, `native`. `custom` (e.g. Brave Search) is not implemented yet, on the roadmap. |
| `provider_override` | string | `""` | `BUILDER_PROVIDER_OVERRIDE` | `--provider-override` | Forces provider family for custom or alias model names. Allowed: `openai`, `anthropic`. Requires an explicit `model` override. |
| `openai_base_url` | string | `""` | `BUILDER_OPENAI_BASE_URL` | `--openai-base-url` | OpenAI-compatible base URL. Must be used with `provider_override=openai` or with no explicit provider override. Cannot be changed mid-session. |
| `store` | bool | `false` | `BUILDER_STORE` |  | Sets OpenAI Responses `store=true` for main model requests. |
| `allow_non_cwd_edits` | bool | `false` | `BUILDER_ALLOW_NON_CWD_EDITS` |  | Lets the `patch` tool edit files outside the workspace root. This is not sandboxing - model can still bypass this easily. |
| `model_context_window` | int | `272000` | `BUILDER_MODEL_CONTEXT_WINDOW` |  | Explicit context-window size used for compaction and token accounting. Must be `> 0`. |
| `context_compaction_threshold_tokens` | int | `258400` | `BUILDER_CONTEXT_COMPACTION_THRESHOLD_TOKENS` |  | Auto-compaction threshold. Must be `> 0`, `< model_context_window`, and at least `50%` of `model_context_window`. The default is derived from the default context window. |
| `pre_submit_compaction_lead_tokens` | int | `35000` | `BUILDER_PRE_SUBMIT_COMPACTION_LEAD_TOKENS` |  | Fixed pre-submit runway reserve before auto-compaction. Builder compacts before sending the next user prompt once (`context_compaction_threshold_tokens` - this threshold) is reached. |
| `minimum_exec_to_bg_seconds` | int | `15` | `BUILDER_MINIMUM_EXEC_TO_BG_SECONDS` |  | Default floor for `exec_command` yield time before it moves to background and lets Builder manage it asynchronously. Lower values are clamped up. Use if model frequently expects your commands to complete fast, they background, and force model to poll for them. |
| `compaction_mode` | string | `local` | `BUILDER_COMPACTION_MODE` |  | Allowed: `native`, `local`, `none`. `native` prefers provider-native compaction and falls back to local compaction. `local` always uses local summary compaction. `none` disables auto-compaction and makes manual compaction fail. |
| `cache_warning_mode` | string | `default` | `BUILDER_CACHE_WARNING_MODE` |  | Prompt-cache warning policy. Allowed: `off`, `default`, `verbose`. `default` catches unwanted invalidations and keeps them in detail mode. `verbose` includes everything from `default`, surfaces cache warnings in ongoing mode too, and a broader range of warnings. |
| `shell_output_max_chars` | int | `16000` | `BUILDER_SHELL_OUTPUT_MAX_CHARS` |  | Output budget for shell tools and background-shell notices before they are truncated. |
| `bg_shells_output` | string | `default` | `BUILDER_BG_SHELLS_OUTPUT` |  | Background-shell output mode (injection of shell outputs into model context). Allowed: `default`, `verbose`, `concise`. Verbose dumps all output into the main agent's model. Concise forces it to read output files. Default outputs truncated previews + gives a file path. |
| `shell.postprocessing_mode` | string | `builtin` | `BUILDER_SHELL_POSTPROCESSING_MODE` |  | Semantic post-processing mode for `exec_command`. Allowed: `none`, `builtin`, `user`, `all`. `builtin` enables Builder processors only. `all` runs Builder processors first, then your hook. |
| `shell.postprocess_hook` | string | `""` | `BUILDER_SHELL_POSTPROCESS_HOOK` |  | Optional executable/script path for a single local command post-processing hook. Builder sends JSON on stdin and expects JSON on stdout. |
| `persistence_root` | string | `~/.builder` | `BUILDER_PERSISTENCE_ROOT` |  | Root for auth, session, and workspace index storage. Does not change the location of `~/.builder/config.toml`. |

### Timeouts

| Key | Type | Default | Env | CLI | Description |
| --- | --- | --- | --- | --- | --- |
| `timeouts.model_request_seconds` | int | `400` | `BUILDER_TIMEOUTS_MODEL_REQUEST_SECONDS` | `--model-timeout-seconds` | HTTP timeout for model requests. Must be `> 0`. |

### Supervisor

| Key | Type | Default | Env | Description |
| --- | --- | --- | --- | --- |
| `reviewer.frequency` | string | `edits` | `BUILDER_REVIEWER_FREQUENCY` | Allowed: `off`, `all`, `edits`. `all` runs the reviewer after every completed assistant turn. `edits` runs it only after successful `patch` edits. |
| `reviewer.model` | string | inherits `model` | `BUILDER_REVIEWER_MODEL` | Separate model for the reviewer pass. If unset, Builder uses main `model`. |
| `reviewer.thinking_level` | string | inherits `thinking_level` | `BUILDER_REVIEWER_THINKING_LEVEL` | Allowed: `low`, `medium`, `high`, `xhigh`. |
| `reviewer.timeout_seconds` | int | `60` | `BUILDER_REVIEWER_TIMEOUT_SECONDS` | Reviewer HTTP timeout. Must be `> 0`. |
| `reviewer.verbose_output` | bool | `false` | `BUILDER_REVIEWER_VERBOSE_OUTPUT` | Controls whether reviewer suggestion text is shown at all. When `false`, Builder only shows the concise reviewer result/status line. When `true`, Builder shows the full suggestion list at the moment the reviewer issues it, and the later reviewer status stays concise after the follow-up is applied or ignored. |

### Model Capability Overrides

Use these only for custom or alias models when the built-in model registry is not enough.

| Key | Type | Default | Env | Description |
| --- | --- | --- | --- | --- |
| `model_capabilities.supports_reasoning_effort` | bool | `false` | `BUILDER_MODEL_CAPABILITIES_SUPPORTS_REASONING_EFFORT` | Override-marks the configured model as supporting reasoning effort / thinking levels. |
| `model_capabilities.supports_vision_inputs` | bool | `false` | `BUILDER_MODEL_CAPABILITIES_SUPPORTS_VISION_INPUTS` | Marks the configured model as supporting multimodal image and PDF inputs. |

If both values stay `false`, Builder falls back to the built-in model capability registry.

### Provider Capability Overrides

Use these only for custom providers or models.

| Key | Type | Default | Env | Description |
| --- | --- | --- | --- | --- |
| `provider_capabilities.provider_id` | string | `""` | `BUILDER_PROVIDER_CAPABILITIES_PROVIDER_ID` | Required whenever you set provider capability overrides. |
| `provider_capabilities.supports_responses_api` | bool | `false` | `BUILDER_PROVIDER_CAPABILITIES_SUPPORTS_RESPONSES_API` | Marks the provider as supporting the Responses API. |
| `provider_capabilities.supports_responses_compact` | bool | `false` | `BUILDER_PROVIDER_CAPABILITIES_SUPPORTS_RESPONSES_COMPACT` | Marks the provider as supporting server-side compaction. |
| `provider_capabilities.supports_native_web_search` | bool | `false` | `BUILDER_PROVIDER_CAPABILITIES_SUPPORTS_NATIVE_WEB_SEARCH` | Marks the provider as supporting native web search. |
| `provider_capabilities.supports_reasoning_encrypted` | bool | `false` | `BUILDER_PROVIDER_CAPABILITIES_SUPPORTS_REASONING_ENCRYPTED` | Marks the provider as supporting encrypted reasoning items. |
| `provider_capabilities.supports_server_side_context_edit` | bool | `false` | `BUILDER_PROVIDER_CAPABILITIES_SUPPORTS_SERVER_SIDE_CONTEXT_EDIT` | Marks the provider as supporting server-side context editing. |
| `provider_capabilities.is_openai_first_party` | bool | `false` | `BUILDER_PROVIDER_CAPABILITIES_IS_OPENAI_FIRST_PARTY` | Marks the provider as first-party OpenAI semantics, which gates some Responses-specific behavior such as fast mode and phase protocol features. |

### Tools

`[tools]` is a per-tool boolean table in `config.toml`.
File-based tool toggles merge with defaults. `BUILDER_TOOLS` and `--tools` behave differently: they replace the entire tool set with the CSV you provide.


| Key | Default | What enabling it exposes |
| --- | --- | --- |
| `tools.ask_question` | `true` | Tool to ask interactive questions |
| `tools.shell` | `true` | The primary shell tool. Internally this maps to `exec_command`. |
| `tools.patch` | `true` | The edit tool |
| `tools.trigger_handoff` | `false` | Experimental tool the agents can use to proactively compact their own context. |
| `tools.view_image` | `true` | Ability to view images and PDFs (if supported) |
| `tools.web_search` | `true` | Tool to search the web |
| `tools.write_stdin` | `true` | Interaction with background shells. |

Notes:

- `tools.web_search = true` does not force web search on. Native search still depends on `web_search = "native"` and provider support.

### Skills

`[skills]` is a file-only per-skill boolean table in `config.toml` to disable unneeded skills. Keys are matched case-insensitively.


| Key | Default | Description |
| --- | --- | --- |
| `skills.<skill name>` | `true` | Enables or disables a discovered skill for new sessions. Disabled skills are omitted from the injected skills developer message and shown as `disabled` in `/status`. |

Notes:

- Skill toggles are only applied when Builder creates a new conversation/session.
- Use `"quoted names"` to refer to skill keys containing spaces.
