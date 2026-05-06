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

If `target` is omitted, Builder opens delete confirmation for the current worktree. The main workspace worktree cannot be deleted. Deletion is blocked when another session targets that worktree or when background processes are running inside it.

If you delete the active worktree, Builder moves the session back to the main workspace.

## Configuration

Since worktrees are basically raw git checkouts, you can set-up a custom worktree creation script that will prepare newly created checkouts with local data like `.env`, encryption credentials, gradle wrappers, or installed dependencies.

```toml
[worktrees]
# base_dir = "~/.builder/worktrees"
# setup_script = "scripts/setup-worktree.sh"
```

- `base_dir` sets the root directory for Builder-managed worktrees.
- `setup_script` runs after create in the background. Relative paths resolve from the workspace root. Absolute and `~/` paths also work.
- Create still succeeds if the setup script is missing, invalid, fails, or times out. Builder reports that as a local note.

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

It also receives JSON as stdin:

```json
{
    "source_workspace_root": "/path/to/main/workspace",
    "branch_name": "feature/name",
    "worktree_root": "/path/to/new/worktree",
    "session_id": "...",
    "project_id": "...",
    "workspace_id": "...",
    "worktree_id": "...",
    "created_branch": true
}
```
