---
title: Quickstart
description: Install Builder, authenticate on first launch, tune the most useful settings, and learn the main session workflows.
---

## Install

### Homebrew (macOS/Linux)

```bash
brew tap respawn-app/tap
brew install builder-cli
```

### Standalone binaries via GitHub Releases

```bash
curl -fsSL https://raw.githubusercontent.com/respawn-app/builder/main/scripts/install.sh | sh
```

Check the installed version with:

```bash
builder --version
```

## First Authentication

Start Builder CLI with: `builder`

On first launch, Builder requires authentication before startup completes.

Supported auth options:

- OpenAI/Codex subscription OAuth via the startup sign-in picker.
- OpenAI API-key auth via `OPENAI_API_KEY`. If you prefer API-key auth, export `OPENAI_API_KEY` before launch and builder will use it with your permission.

You can switch later with `/logout`.

## Main Workflows

- Use `Enter` to steer the model, `Tab` to queue messages.
- While the model is still working, `Enter` adds a pending steering message instead of hiding it. Pending steering stays visible in the queue area as `next: ...` until it is sent.
- Use `Shift+Tab` to toggle between detailed transcript mode and ongoing mode.
- Press `Esc` twice to enter Edit mode, which lets you go back in time, edit a previous message, and fork the session into a new one. File edits stay.
- Use the `Up`/`Down` arrow keys to select and resend previous prompts.
- Press `F1` to invoke help with other hotkeys.

Supervisor is a feature that will automatically review the edits made by the model. It increases costs by ~20% but improves results.

- Use `/supervisor` to toggle reviewer invocation for the current session. Initial value is config's `reviewer.frequency`, and default is on.
- `review flow`: use `/review` to start a review. In a non-empty session, Builder opens that review in a fresh child session. After the review finishes, you can use `/back` to teleport to the original session.
- `/name` will set your session name in the picker and terminal title.
- `/autocompaction` will toggle compaction, and `/compact` will trigger one. If autocompact is off, you can go above 100% context usage if model allows it, it may incur additional costs.
- `/status` opens a read-only inspection overlay for the current account/session/config/git/context state and progressively fills in quotas, skills, and AGENTS files.

For the full command reference, see [Slash Commands](/slash-commands/).

## Configuration

Builder reads settings from `~/.builder/config.toml`, which will be auto-created on first start.

The full reference is on the [Configuration](/config/) page.

The most useful options to review early are:
- `model` to choose your default model.
- `thinking_level` and `model_verbosity` to control reasoning effort and response density.
- `theme` to match your terminal workflow.
- `web_search` to enable or disable native web search.
- `compaction_mode` and `context_compaction_threshold_tokens` to control context management.
- `tools.ask_question` to control whether you want the agent to ask you questions.

## Skills And Custom Commands

Builder discovers skills from:

- `~/.builder/skills`
- `<workspace>/.builder/skills`

Each skill should live in its own directory and include a `SKILL.md` file.

Builder discovers custom slash commands from Markdown files in:

- `<workspace>/.builder/prompts`
- `<workspace>/.builder/commands`
- `~/.builder/prompts`
- `~/.builder/commands`

Each top-level `.md` file becomes a `/prompt:<name>` command.

The cleanest setup is usually to keep one provider-managed source of truth and symlink Builder's discovery directories to it. In practice, that means symlinking the whole `skills`, `prompts`, or `commands` directories, not individual entries.

Builder directly supports the standard `SKILL.md`-based skill layout, so existing Codex/Claude Code skills can be reused cleanly.

You can disable individual skills for new sessions in `~/.builder/config.toml`:

```toml
[skills]
apiresult = false
```

Disabled skills stay on disk, show up as `disabled` in `/status`, and are skipped only when Builder injects the skills developer message for a new conversation.
