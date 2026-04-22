---
title: Worktrees
description: Manage git worktrees from Builder with a dedicated UI, direct switch shortcuts, and model awareness of the active worktree.
---

Builder can manage git worktrees directly. Use this when you want to split work into isolated checkouts without manually juggling separate Builder sessions or separate terminal roots.

## What Builder Does

- Keeps the current session identity while switching its execution target to another worktree.
- Rebinds Builder's runtime and local tools to the selected worktree.
- Tells the agent which worktree it is in, so follow-up actions happen in the correct checkout.
- Shows worktree management in a dedicated full-screen page instead of writing menus or confirmations into transcript scrollback.

In practice, that means you can move a running session into another worktree and continue working there without starting over.

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
- A one-line muted path for each worktree.
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

The create dialog lets you set:

- `Base ref`: the starting point for a new branch. `HEAD` is the normal default.
- `Branch mode`: create a new branch or reuse an existing branch/ref.
- `Branch name` or `Existing branch/ref`: depends on the selected branch mode.
- `Path`: optional. Leave it blank to let Builder choose the default location.

Use `Tab` / `Shift+Tab` or arrow keys to move through the dialog, then confirm with `Enter` on `Create`.

After a successful create, Builder switches the session into the new worktree and closes the page.

## Delete A Worktree

Open the page with `/wt`, select a worktree, then press `d`.

Delete confirmation is shown in a dedicated confirmation UI. It does not use transcript text prompts.

Depending on the selected worktree, Builder may offer:

- `Delete`: remove the worktree.
- `Delete + Branch`: remove the worktree and also attempt branch cleanup.
- `Cancel`: go back.

After a successful delete, the page stays open and refreshes so you can keep managing remaining worktrees.

## Direct Shortcut Commands

You can still use one-shot commands when they are faster than opening the page.

### Switch directly

```text
/wt switch <target>
```

`<target>` can match a worktree id, path, display name, or branch name when that resolves uniquely.

This is useful when you already know exactly where you want to go.

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

Use `/wt` to open the Worktrees page.

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

If you want the full slash-command reference, see [Slash Commands](../slash-commands/).
