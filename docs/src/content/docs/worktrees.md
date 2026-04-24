---
title: Worktrees
description: Create, switch, and delete git worktrees from Builder.
---

Builder manages git worktrees for the current session. Creating or switching a worktree moves that session into the selected checkout and gives the agent necessary context. Managed worktrees are created according to config.toml's `[worktrees].base_dir`, and Builder switches the session into the new worktree after create. Run `/wt` to get started. 

## Switch

`<target>` must resolve uniquely. Builder matches, in order:

- worktree id
- canonical path
- display name
- branch name
- `main` for the main workspace worktree

## Delete

If `target` is omitted, Builder opens delete confirmation for the current worktree. The main workspace worktree cannot be deleted.

The confirmation page previews what the selected action deletes. `Delete + Branch` is available when the worktree has a branch name and adds the local branch to that preview.

Deletion is blocked when another session targets that worktree or when background processes are running inside it.

If you delete the active worktree, Builder moves the session back to the main workspace.

## Configuration

```toml
[worktrees]
# base_dir = "~/.builder/worktrees"
# setup_script = "scripts/setup-worktree.sh"
```

- `base_dir` sets the root directory for Builder-managed worktrees.
- `setup_script` runs after create in the background. Relative paths resolve from the workspace root. Absolute and `~/` paths also work.
- Create still succeeds if the setup script is missing, invalid, fails, or times out. Builder reports that as a local note.
