Builder is a highly opinionated, minimal terminal coding agent for professional Agentic Engineers, focusing on output quality. Currently only supporting OpenAI/codex models.

## Install

### Homebrew (macOS/Linux)

```bash
brew tap respawn-app/tap
brew install builder
```

### Standalone binaries via GitHub Releases

```bash
curl -fsSL https://raw.githubusercontent.com/respawn-app/agent/main/scripts/install.sh | sh
```

Check the installed version with `builder --version`.

## Compatibility

- Published release binaries: macOS, Linux, Windows on `x64` (`amd64`) and `arm64`.
- Homebrew install path: macOS and Linux.
- Building from source currently requires Go `1.25`.

### Features:

- [x] Agentic loop with `shell`, `ask_question`, `patch` tools.
- [x] Native local image/PDF attachment tool (`view_image`) for path-based multimodal reading.
- [x] Support for Codex login and OpenAI api keys.
- [x] Compaction, including auto, using native Codex/OpenAI endpoints.
- [x] Compact UI mode for ongoing work, and detailed mode to review thinking, tool calls, prompts, summaries.
- [x] Queueing messages, steering the model (Tab to queue, Enter to steer)
- [x] Asking user questions via a tool
- [x] Terminal bell notifications for asks/approvals and tool-heavy turn completion
- [x] Config file with model selection, tool config, compact threshold, timeouts.
- [x] Local and global `AGENTS.md` support
- [x] Session and history persistence and resumption
- [x] Markdown rendering
- [x] Saved prompts
- [x] Info about agent environment, such as shell env, machine, os etc.
- [x] Syntax highlighting
- [x] Native Web search (for now only OpenAI)
- [x] Calling shell directly via `$`
- [x] Premade prompts for review, compaction, init.
- [x] Esc-esc-style editing of messages and history rewrites
- [x] Agent skills.
- [x] Background shells, which enable subagents via headless mode: `builder run`
- [x] Model verbosity for openai models
- [x] Native terminal scrollback, selection, copy-paste

### Important things not done yet

- [ ] Arrow-up in input box to scroll through previous prompts and repeat them.
- [ ] @-file mentioning
- [ ] Any other providers except Codex (since no other exist that I know that allow to use their subscription in a 3rd party harness)

### What will likely never be implemented

These features are controversial or questionable for model performance, and usually have a better replacement.
Here is where this project has to be highly opinionated:

- Native subagent orchestration inside one process; use separate headless Builder instances instead.
  - Supported path: `builder run "..."` for tmux/background subagent workflows. Agent already does this on its own.
- Plan mode - the model has native plan capabilities and can always ask questions, rest is just eye candy.
- MCPs - mcps are net negative on model performance, pollute context, and can be replaced with CLI scripts
- Extra UI candy tool calls. Less tools, less burden on the model.
- On the fly changing of toolsets or models. Changing models at runtime hurts model performance and invalidates caches.
- Microcompaction - this invalidates caches and drives costs up with marginal benefits
- Sandboxing - Codex's sandbox is annoying, doesn't work with many tools (gradle, java etc), junie's sandbox can be bypassed, claude code's sandbox is brittle and can also be bypassed. Frontier models are not so stupid anymore and are trained not to destroy your PC.
- WebFetch tool or similar. Just use [jina.ai](https://r.jina.ai) to fetch urls.
- Fancy summaries, UI, minimal mode, features for "vibe coding". The philosophy is to build something for professionals (agentic engineers)

## Project Docs

- Contribution guide: `CONTRIBUTING.md`
- Security policy: `SECURITY.md`
- Code of conduct: `CODE_OF_CONDUCT.md`


## License

Builder is licensed under `AGPL-3.0-only`. See `LICENSE`.

If you deploy a modified network-accessible version, AGPL section 13 requires
you to offer the corresponding source of that modified version to its users.
