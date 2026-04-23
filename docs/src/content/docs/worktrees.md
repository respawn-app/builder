---
title: Worktrees
description: Manage git worktrees from Builder with a dedicated full-screen UI, direct switch shortcuts, and model awareness of the active worktree.
---

Builder can manage git worktrees directly. Use this when you want isolated checkouts without manually juggling separate Builder sessions or separate terminal roots. Run `/wt` to get started.

## What Builder Does

- Keeps the current session identity while switching its execution target to another worktree.
- Rebinds Builder's runtime and local tools to the selected worktree.
- Tells the agent which worktree it is in, so follow-up actions happen in the correct checkout.
- Shows worktree management in a dedicated full-screen page instead of writing menus or confirmations into transcript scrollback.

In practice, that means you can move a running session into another worktree and continue there without starting over.

## Open The Worktrees Page

Use:

```text
/wt
```

or:

```text
/worktree
```

This opens the `Worktrees` page.

The page contains:

- A top `Create worktree` row.
- A paginated list of known worktrees.
- A muted one-line path for each worktree.
- Compact badges such as `current`, `main`, `branch:<name>`, `detached`, and `external`.

## Main Actions

On the `Worktrees` page:

- `Enter` switches to the selected worktree.
- `c` opens the create-worktree dialog.
- `d` opens delete confirmation for the selected worktree.
- `x` opens delete confirmation with branch deletion preselected when applicable.
- `r` refreshes the list.
- `Up` / `Down` move selection.
- `PgUp` / `PgDn` page through the list.
- `Home` / `End` jump to the first or last row.
- `Esc` or `q` closes the page.

## Create A Worktree

Open the page with `/wt`, select `Create worktree`, or press `c`.

The create dialog shows one form with all fields visible.

`Branch or ref`

- Builder only auto-suggests a target name from the sanitized session name.
- If there is no valid session-name suggestion, the field stays blank and you must choose one explicitly.
- Builder does not fall back to the current branch, `main`, or a generic placeholder.
- Builder resolves what you typed asynchronously and shows a live badge:
  - `✔︎ new branch`
  - `∴ existing branch`
  - `∴ detached ref`

`Base ref`

- Shown when Builder will create a new branch from the typed target.
- Rendered after `Branch or ref` because `HEAD` is usually left unchanged.
- `HEAD` is the normal default.

`Path`

- The current UI does not expose a path field.
- Builder chooses the worktree location from its configured defaults.

Navigation:

- `Up` / `Down` or `Tab` / `Shift+Tab` move between form sections.
- `Left` / `Right` change the selected action.
- `Enter` activates the current section.
- `Esc` goes back to the list.

After a successful create, Builder switches the session into the new worktree and closes the dialog.

## Delete A Worktree

Open the page with `/wt`, select a worktree, then press `d`.

Delete confirmation is shown in dedicated confirmation UI. It does not use transcript text prompts.

Depending on the selected worktree, Builder may offer:

- `Cancel`
- `Delete`
- `Delete + Branch`

After a successful delete, the page stays open and refreshes so you can keep managing remaining worktrees.

## Direct Shortcut Commands

You can still use one-shot commands when they are faster than opening the page.

### Switch directly

```text
/wt switch <target>
```

`<target>` can match a worktree id, path, display name, or branch name when that resolves uniquely.

### Open delete confirmation for a specific target

```text
/wt delete <target>
```

This opens the Worktrees page directly into delete confirmation for that target.

### Open create directly

```text
/wt create
```

This opens the create dialog immediately.

## `/wt list`

There is no dedicated list subcommand.

Use `/wt` to open the `Worktrees` page.

## Agent Awareness

When you create or switch into a worktree, Builder records that change and reminds the model about the active worktree context.

That gives the agent the important part of the environment state:

- which worktree is active,
- where that worktree lives,
- and what working directory Builder is using there.

You do not need to manually re-explain that you are now in another checkout every time you switch.

## When To Use Worktrees

Use worktrees when you want to:

- work on multiple branches at once,
- keep a review or investigation isolated from your main checkout,
- let one Builder session keep progressing while you open another code path elsewhere,
- or move the current session into a fresh branch without losing its context.

For the full slash-command reference, see [Slash Commands](../slash-commands/).
