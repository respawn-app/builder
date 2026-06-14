package main

import (
	"path/filepath"
	"testing"

	"builder/server/session"
)

func TestRebaseUnderRoot(t *testing.T) {
	oldRoot := filepath.FromSlash("/home/u/.builder")
	newRoot := filepath.FromSlash("/home/u/.kent")

	cases := []struct {
		name   string
		value  string
		want   string
		wantOK bool
	}{
		{"exact root", oldRoot, newRoot, true},
		{"descendant", filepath.FromSlash("/home/u/.builder/worktrees/w1"), filepath.FromSlash("/home/u/.kent/worktrees/w1"), true},
		{"deep descendant", filepath.FromSlash("/home/u/.builder/projects/p/sessions/s"), filepath.FromSlash("/home/u/.kent/projects/p/sessions/s"), true},
		{"sibling substring not matched", filepath.FromSlash("/home/u/.builder-backup/x"), filepath.FromSlash("/home/u/.builder-backup/x"), false},
		{"external repo untouched", filepath.FromSlash("/home/u/code/myrepo"), filepath.FromSlash("/home/u/code/myrepo"), false},
		{"empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rebaseUnderRoot(tc.value, oldRoot, newRoot)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (value %q)", ok, tc.wantOK, tc.value)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPathStillUnderRoot(t *testing.T) {
	root := filepath.FromSlash("/home/u/.builder")
	under := []string{root, filepath.FromSlash("/home/u/.builder/db/main.sqlite3")}
	for _, p := range under {
		if !pathStillUnderRoot(p, root) {
			t.Fatalf("expected %q to be under %q", p, root)
		}
	}
	outside := []string{"", filepath.FromSlash("/home/u/.builder-backup"), filepath.FromSlash("/home/u/.kent/x"), filepath.FromSlash("/etc/passwd")}
	for _, p := range outside {
		if pathStillUnderRoot(p, root) {
			t.Fatalf("expected %q NOT to be under %q", p, root)
		}
	}
}

func TestRebaseSessionMeta(t *testing.T) {
	oldRoot := filepath.FromSlash("/home/u/.builder")
	newRoot := filepath.FromSlash("/home/u/.kent")

	t.Run("nil reminder", func(t *testing.T) {
		meta := session.Meta{}
		if rebaseSessionMeta(&meta, oldRoot, newRoot) {
			t.Fatal("expected no change for nil reminder")
		}
	})

	t.Run("rebases worktree paths only", func(t *testing.T) {
		externalRepo := filepath.FromSlash("/home/u/code/myrepo")
		meta := session.Meta{
			WorkspaceRoot: externalRepo,
			WorktreeReminder: &session.WorktreeReminderState{
				WorktreePath:  filepath.FromSlash("/home/u/.builder/worktrees/w1"),
				EffectiveCwd:  filepath.FromSlash("/home/u/.builder/worktrees/w1/sub"),
				WorkspaceRoot: externalRepo,
			},
		}
		if !rebaseSessionMeta(&meta, oldRoot, newRoot) {
			t.Fatal("expected change")
		}
		if got, want := meta.WorktreeReminder.WorktreePath, filepath.FromSlash("/home/u/.kent/worktrees/w1"); got != want {
			t.Fatalf("worktree_path = %q, want %q", got, want)
		}
		if got, want := meta.WorktreeReminder.EffectiveCwd, filepath.FromSlash("/home/u/.kent/worktrees/w1/sub"); got != want {
			t.Fatalf("effective_cwd = %q, want %q", got, want)
		}
		if meta.WorktreeReminder.WorkspaceRoot != externalRepo {
			t.Fatalf("reminder workspace_root was rebased: %q", meta.WorktreeReminder.WorkspaceRoot)
		}
		if meta.WorkspaceRoot != externalRepo {
			t.Fatalf("meta workspace_root was rebased: %q", meta.WorkspaceRoot)
		}
	})

	t.Run("no change when paths outside old root", func(t *testing.T) {
		meta := session.Meta{
			WorktreeReminder: &session.WorktreeReminderState{
				WorktreePath: filepath.FromSlash("/home/u/code/myrepo"),
			},
		}
		if rebaseSessionMeta(&meta, oldRoot, newRoot) {
			t.Fatal("expected no change for external worktree path")
		}
	})
}
