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

Supported auth options:

- OpenAI/Codex subscription OAuth via the startup sign-in picker.
- OpenAI API-key auth via `OPENAI_API_KEY`. If you prefer API-key auth, export `OPENAI_API_KEY` before launch and builder will use it with your permission.

You can switch later with `/logout`.

## Main Workflows

- Use `Enter` to steer the model, `Tab` to queue messages.
- Type `$ <command>` to execute a shell command and show its output to the model.
- Use `Shift+Tab` to toggle between detailed transcript mode and ongoing mode.
- Press `Esc` twice to enter Edit mode, which lets you go back in time, edit a previous message, and fork the session into a new one. File edits stay.
- Use the `Up`/`Down` arrow keys to select and resend previous prompts.
- Press `Ctrl+V` or `Ctrl+D` to paste a clipboard screenshot into the prompt as an image file path.
- Press `F1` to invoke help with other hotkeys.
- Use `/supervisor` to toggle reviewer invocation for the current session. Initial value is config's `reviewer.frequency`, and default is on. Supervisor is a feature that will automatically review the edits made by the model. It increases costs by ~20% but improves results.
- Use `/review` to start a code review. In a non-empty session, Builder opens that review in a fresh child session. After the review finishes, you can use `/back` to teleport to the original session.
- `/name` will set your session name in the picker and terminal title.
- `/autocompaction` will toggle compaction, and `/compact` will trigger one. If autocompact is off, you can go above 100% context usage if model allows it, it may incur additional costs.
- `/status` 

For the full command reference, see [Slash Commands](/slash-commands/).

## Configuration

Builder reads settings from `~/.builder/config.toml`.
The full reference is on the [Configuration](/config/) page.

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

Builder directly supports the standard `SKILL.md`-based skill layout, so existing Codex/Claude Code skills can be reused.

You can disable individual skills for new sessions in `~/.builder/config.toml`:

```toml
[skills]
apiresult = false
```

Changes will take effect when you start a new sesssion.
