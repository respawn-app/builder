Builder is a highly opinionated, minimal terminal coding agent for professional Agentic Engineers, focusing on output quality. Currently only supporting OpenAI/codex models.

### What's done:

- [x] Agentic loop with `shell` and `patch` tools.
- [x] Support for Codex login and OpenAI api keys.
- [x] Compaction using native Codex/OpenAI endpoints.
- [x] Compact UI mode for ongoing work, and detailed mode to review thinking, tool calls, prompts, summaries.
- [x] Queueing messages, steering the model (Tab to queue, Enter to steer)
- [x] Asking user questions via a tool
- [x] Terminal bell notifications for asks/approvals and tool-heavy turn completion
- [x] Config file with model selection, tool config, compact threshold, timeouts.
- [x] Local and global `AGENTS.md` support
- [x] Session and history persistence and resumption
- [x] Markdown rendering
- [x] Saved prompts
- [x] Custom, or at least well made, system prompt.
- [x] Info about agent environment, such as shell env, machine, os etc.
- [x] Syntax highlighting
- [x] Web search, especially native
- [x] UI for queued messages
- [x] Calling shell via `$`/`!` (optional)
- [x] Premade prompts for review, compaction.
- [x] Esc-esc-style editing of messages and history rewrites

### Notifications

- Terminal notifications are enabled through `notification_method` in `~/.builder/config.toml`.
- Supported values: `auto` (default), `osc9`, `bel`.
- `auto` prefers OSC 9 for compatible terminals (including Ghostty) and falls back to BEL.
- You can override via env var: `BUILDER_NOTIFICATION_METHOD`.

### Scroll Modes

- `tui_scroll_mode` controls Builder transcript behavior (`alt` or `native`).
- `tui_alternate_screen` controls terminal alt-screen policy (`auto|always|never`).
- In `tui_scroll_mode=native`, main UI startup always uses normal buffer even if `tui_alternate_screen=always`, so transcript replay remains visible in terminal scrollback.

### Important things not done yet

- [ ] @-file mentioning
- [ ] Any non-openai model support

### What will likely never be implemented

These features are controversial or questionable for model performance, and usually have a better replacement.
Here is where this project has to be highly opinionated:
- Native subagent orchestration inside one process; use separate headless Builder instances instead.
  - Supported path: `builder run "..."` for tmux/background subagent workflows.
- Plan mode - the model has native plan capabilities and can always ask questions, rest is just eye candy.
- MCPs - mcps are net negative on model performance, pollute context, and can be replaced with CLI scripts
- Skills - skills are controversial in performance and can easily be replaced with already supported AGENTS.md mentions.
- Extra UI candy tool calls - all the model needs is `shell`, `ask` and `patch`. Less tools, less burden on the model.
- On the fly changing of toolsets or models. Changing models at runtime hurts model performance and invalidates caches.
- Microcompaction - this invalidates caches and drives costs up with marginal benefits
- Sandboxing - Codex's sandbox is annoying, doesn't work with many tools (gradle, java etc), junie's sandbox can be bypassed, claude code's sandbox is brittle and can also be bypassed. Frontier models are not so stupid anymore and are trained not to destroy your PC.
- WebFetch tool or similar. Just use [jina.ai](https://r.jina.ai).
- Fancy summaries, UI, minimal mode, features for "vibe coding", eye candy. The philosophy is to build something for professionals (existing engineers)
