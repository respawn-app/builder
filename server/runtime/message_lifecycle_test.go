package runtime

import (
	"testing"
	"time"

	"builder/server/session"
)

func TestNewActiveMetaContextBuilderUsesWorkspaceRootForWorktreeExitDiscovery(t *testing.T) {
	meta := session.Meta{
		WorkspaceRoot: "/repo",
		WorktreeReminder: &session.WorktreeReminderState{
			Mode:          session.WorktreeReminderModeExit,
			WorktreePath:  "/repo/.builder/worktrees/feature",
			WorkspaceRoot: "/repo",
		},
	}
	builder := newActiveMetaContextBuilder(meta, "gpt-5", "medium", nil, time.Unix(0, 0))

	if builder.workspaceRoot != "/repo" {
		t.Fatalf("discovery workspace root = %q, want /repo", builder.workspaceRoot)
	}
	if builder.environmentCWD != "/repo" {
		t.Fatalf("environment cwd = %q, want /repo", builder.environmentCWD)
	}
}

func TestNewActiveMetaContextBuilderUsesEffectiveCwdForWorktreeExitEnvironment(t *testing.T) {
	meta := session.Meta{
		WorkspaceRoot: "/repo",
		WorktreeReminder: &session.WorktreeReminderState{
			Mode:          session.WorktreeReminderModeExit,
			WorktreePath:  "/repo/.builder/worktrees/feature",
			WorkspaceRoot: "/repo",
			EffectiveCwd:  "/repo/pkg",
		},
	}
	builder := newActiveMetaContextBuilder(meta, "gpt-5", "medium", nil, time.Unix(0, 0))

	if builder.workspaceRoot != "/repo" {
		t.Fatalf("discovery workspace root = %q, want /repo", builder.workspaceRoot)
	}
	if builder.environmentCWD != "/repo/pkg" {
		t.Fatalf("environment cwd = %q, want /repo/pkg", builder.environmentCWD)
	}
}
