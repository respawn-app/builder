---
name: builder-dogfooding
description: How to use `builder` cli or change your behavior/config. Read to learn `builder` commands; to debug project/workspace errors; when user asks to change builder config/settings/behavior.
---

Builder is the harness you are running inside, but it's also a server that runs agentic loops, a TUI, and a CLI interface that humans (users) see.

Source-of-truth for commands and public docs:

- Run `builder --help` and `builder <command> --help` for exact current CLI flags.
- Full docs index is at `https://opensource.respawn.pro/builder/llms.txt`.
- Configuration reference: `https://opensource.respawn.pro/builder/config.md`.
- Headless/subagent docs: `https://opensource.respawn.pro/builder/headless.md`.
- Prompt customization docs: `https://opensource.respawn.pro/builder/prompts.md`.
- Shell hook docs: `https://opensource.respawn.pro/builder/command-postprocessing.md`.
- Worktree docs: `https://opensource.respawn.pro/builder/worktrees.md`.

You can directly `curl -S` each of the docs pages' `.md` file to get its content.

## Projects And Workspace Bindings
Builder tracks projects and workspace roots so sessions can move across checkouts and remote/local server boundaries. If your subagent commands fail with errors about workspace binding or projects, simply attach a workspace folder where you want to run the subagent to the project where you are running:

```bash
$ builder attach <path/to/subagent/workspace>
```

Other commands:
- `builder project [path]` - print project bound to [path], or if omitted, to cwd
- `builder project list` to list all projects
- `builder project create --path <server-visible-path> --name <project-name>` - make a new one
- `builder rebind <session-id> <new-path>` - rebind an established session to a new workspace to change its cwd and continue in another workspace
 
For remote daemons, paths must be visible to the server machine, not the frontend shell.

## Config Locations
Global config (applies to all projects) `~/.builder/config.toml` (`%USERPROFILE%\.builder\` on Windows), local config is at `<workspace-root>/.builder/config.toml`. Workspace root is usually your cwd.

Precedence, low to high:

1. Built-in defaults
2. `~/.builder/config.toml`
3. `<workspace-root>/.builder/config.toml`
4. Environment variables
5. `builder run` CLI flags

Most behavior changes affect only **new sessions** and only **after server restart**. Existing sessions will keep captured conversation logs and settings. After changing config, ask the user to restart the service `builder service restart`, restart the Builder GUI, and then start a new session, for changes to apply.

Important config notes:

- `persistence_root` controls Builder's DB/auth/session storage, not the location of `~/.builder/config.toml`.
- Workspace config is resolved from the session workspace root.
- Important: do not make changes to your configuration that were not authorized or directly asked for by the user. If your environment is buggy/broken, ask the user for help instead of messing around with your internals.

## Change Agent Behavior
Use prompt files for broad behavior changes, skills for reusable on-demand workflows, and subagent roles for specialized headless agents.

Prompt files:

- `~/.builder/AGENTS.md` is global instructions file injected into every session.
- `<workspace>/AGENTS.md` adds developer instructions that are specific to the current project.
- `~/.builder/SYSTEM.md` or `<workspace>/SYSTEM.md` or `config.system_prompt_file` replace the main system prompt for new sessions (yes, the full system instructions you received in this session). Prefer SYSTEM.md unless user wants a config-based prompt override.
- `reviewer.system_prompt_file` replaces the supervisor/reviewer prompt the same way.

`system_prompt_file` paths resolve relative to the containing `config.toml` file unless absolute.
Inside system prompt files, you can use these placeholders:

- `{{.BuilderRunCommand}}` - Command prefix for launching Builder subagents from shell, e.g. `path/to/builder.exe`
- `{{.EstimatedToolCallsForContext}}` - Estimated function/tool-call budget before compaction/handoff, exact number that varies with model context window, like `185`.
- `{{.EditingToolName}}` - Name of the tool you use to modify files, like `edit` or `patch`. Varies per model.
- `{{.DefaultSystemPrompt}}` - Full text of the original Builder system prompt, positioning you as an expert architect, product engineer, coding agent.

Note that you shouldn't be rewriting main agent's system prompt: the output can be biased and low-quality. System prompts need to be crafted carefully and vary strongly per LLM model family and use-case. Either the user should supply an existing prompt they want to use, or use `{{.DefaultSystemPrompt}}` for sane defaults, and add additional instructions to it.

## Subagent roles
User may ask you to define new "subagent roles". Subagents are `builder run` commands you call. You can also use them for scripting of user's personal builder-based workflows. More info at `builder run --help`.

- `--fast` role always exists, it's intended for quick small read-only tasks.
- `--agent <role>` selects `[subagents.<role>]` from config.

Define custom roles in config. Role names normalize to lowercase and may contain letters, digits, `-`, and `_`. Subagent subsections support most of general config property overrides, and inherit what is not overridden from the parent config.

Roles are needed to create specialized subagent types for different tasks. Treat them like different employees or specialists.

```toml
[subagents.research]
model = "gpt-5.5"
thinking_level = "xhigh"
system_prompt_file = "research-agent.md"
priority_request_mode = true

[subagents.research.tools]
patch = false

[subagents.research.skills]
"builder-dogfooding" = true
```

Useful role-specific keys include:

- `model`, `provider_override`, `openai_base_url`
- `thinking_level`, `model_verbosity`, `priority_request_mode`
- `system_prompt_file`
- `timeouts.model_request_seconds`
- `[subagents.<role>.tools]`
- `[subagents.<role>.skills]`
- `shell_output_max_chars`, `bg_shells_output`

## Worktrees
Builder manages worktrees you work in. You can customize the process of worktree creation:

```toml
[worktrees]
base_dir = "~/.builder/worktrees"
setup_script = "scripts/setup-worktree.sh"
```

`base_dir` is where Builder creates managed worktrees. `setup_script` runs in the background after creation; relative paths resolve from the main workspace root. Use the `setup_script` to set up a newly created worktree with files that a worktree checkout did not bring over, like `.env`, private credentials, encryption keys, symlinks to local docs or other files, install dependencies, etc. It's designed to go from "just did a git checkout" to "fully ready for development".

The script receives environment variables as input:

- `BUILDER_WORKTREE_SOURCE_WORKSPACE_ROOT` - Original/main workspace root that created the worktree, e.g. `/Users/user/Developer/app` or `C:\Users\user\dev\app`.
- `BUILDER_WORKTREE_BRANCH_NAME` - Branch/ref name selected for the new worktree, e.g. `feature/search-fix`.
- `BUILDER_WORKTREE_ROOT` - Filesystem path to the newly created worktree; setup script runs with this as cwd, e.g. `/Users/nek/.builder/worktrees/app/search-fix`.
- `BUILDER_WORKTREE_SESSION_ID` - Builder session id that requested the worktree, e.g. `b31234ab-78ce-43d1-8f4c-2d6c6d4adbc1`.
- `BUILDER_WORKTREE_PROJECT_ID` - Builder project id for the workspace/project, e.g. `project-94b18685-19ed-4513-96bb-bcffa10410ff`.
- `BUILDER_WORKTREE_WORKSPACE_ID` - Builder workspace binding id for the source workspace, e.g. `workspace-2f7b6d4a`.
- `BUILDER_WORKTREE_WORKTREE_ID` - Builder metadata id for the created worktree, e.g. `worktree-8c9a0e3f`.
- `BUILDER_WORKTREE_CREATED_BRANCH` - Whether Builder created a new branch for this worktree, e.g. `true` or `false`.
- `BUILDER_WORKTREE_PAYLOAD_JSON` - Full setup payload as one JSON string containing all fields above, e.g. `{"source_workspace_root":"/repo","branch_name":"feature/x","worktree_root":"/repo-wt","session_id":"...","project_id":"...","workspace_id":"...","worktree_id":"...","created_branch":true}`.

## Shell Postprocess Hooks
Builder can post-process shell command output before you see it. Configure a hook:

```toml
[shell]
postprocessing_mode = "all" # none | builtin | user | all
postprocess_hook = "~/.builder/shell_postprocess_hook"
```

Modes:

- `none`: no built-ins, no user hook.
- `builtin`: only Builder built-ins.
- `user`: only configured hook.
- `all`: built-ins first, then user hook.

The hook receives JSON on stdin with command metadata, original output, current output, exit code, background state, workdir, and max display chars. 

```json
{
  "tool_name": "exec_command",
  "command": "go test ./...",
  "parsed_args": ["go", "test", "./..."],
  "command_name": "go",
  "workdir": "/Users/user/Developer/project",
  "original_output": "ok  builder/server/runtime  0.532s\n",
  "current_output": "PASS",
  "exit_code": 0,
  "backgrounded": false,
  "max_display_chars": 16000
}
```

It must return JSON on stdout:

```json
{
  "processed": true,
  "replaced_output": "new output"
}
```

Return `{"processed": false}` for no-op passthrough. If the hook is missing, times out, exits nonzero, or returns invalid JSON, Builder falls back to the current output and reports a warning.

You can disable this feature with `raw=true` in your `exec_command` tool. This hook is intended to optimize, shrink, or log the commands that you run. For example, a user may want you to use a tool that makes outputs smaller. Builder also ships embedded optimizers (`builtin` mode toggle) out of the box.
