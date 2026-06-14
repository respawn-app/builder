package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"builder/server/session"
)

// cmdShellMetacharacters are the characters cmd.exe interprets as shell syntax
// rather than literal path content. The Windows compat junction is created with
// mklink, a cmd builtin invoked through `cmd /c`, so a junction path containing
// any of these cannot be passed through the shell safely. They are exceedingly
// rare in a Windows user-profile path but are valid in account names, so the
// migration refuses with actionable guidance instead of risking a mis-parsed or
// injected command. The check lives here (not in the windows-only file) so it is
// pure, dependency-free string logic that is unit-tested on every platform.
const cmdShellMetacharacters = "&|<>^\"()%!"

// ensureShellSafeJunctionPath returns an error when p contains a character that
// cmd.exe would interpret as shell syntax rather than a literal path component.
func ensureShellSafeJunctionPath(p string) error {
	if i := strings.IndexAny(p, cmdShellMetacharacters); i >= 0 {
		return fmt.Errorf("path %q contains %q, which cmd.exe cannot pass to mklink safely; create the compat junction manually, e.g. mklink /J %q <new-root>", p, string(p[i]), p)
	}
	return nil
}

// rebaseUnderRoot rewrites value from oldRoot to newRoot when value is oldRoot
// itself or a descendant of oldRoot. The match is made only at a path-separator
// boundary, never as a substring, so a sibling like "/home/u/.builder-backup"
// is left untouched when the root is "/home/u/.builder". It returns the
// rewritten path and true when a rewrite applied; otherwise it returns value
// unchanged and false.
func rebaseUnderRoot(value string, oldRoot string, newRoot string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return value, false
	}
	cleanValue := filepath.Clean(trimmed)
	cleanOld := filepath.Clean(oldRoot)
	cleanNew := filepath.Clean(newRoot)
	if cleanValue == cleanOld {
		return cleanNew, true
	}
	prefix := cleanOld + string(filepath.Separator)
	if strings.HasPrefix(cleanValue, prefix) {
		return filepath.Join(cleanNew, cleanValue[len(prefix):]), true
	}
	return value, false
}

// pathStillUnderRoot reports whether value is root itself or a descendant of
// root, matching only at a path-separator boundary. It is the predicate used by
// the migration verification pass to assert that no structured path field still
// references the old persistence root.
func pathStillUnderRoot(value string, root string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	cleanValue := filepath.Clean(trimmed)
	cleanRoot := filepath.Clean(root)
	if cleanValue == cleanRoot {
		return true
	}
	return strings.HasPrefix(cleanValue, cleanRoot+string(filepath.Separator))
}

// rebaseWorktreeReminder rewrites the absolute worktree paths held by a worktree
// reminder from oldRoot to newRoot. Only the two fields that hold absolute paths
// under the persistence root are touched: WorktreePath and EffectiveCwd. Fields
// that point at the user's external repo (WorkspaceRoot) and relative fields are
// left untouched — the separator-boundary prefix rule self-excludes them because
// their values do not lie under the old root. It returns true when any field
// changed. This is the single rebase rule shared by every persistence surface
// that stores a worktree reminder (legacy session.json files and the canonical
// sessions.metadata_json column).
func rebaseWorktreeReminder(reminder *session.WorktreeReminderState, oldRoot string, newRoot string) bool {
	if reminder == nil {
		return false
	}
	changed := false
	if rewritten, ok := rebaseUnderRoot(reminder.WorktreePath, oldRoot, newRoot); ok {
		reminder.WorktreePath = rewritten
		changed = true
	}
	if rewritten, ok := rebaseUnderRoot(reminder.EffectiveCwd, oldRoot, newRoot); ok {
		reminder.EffectiveCwd = rewritten
		changed = true
	}
	return changed
}

// rebaseSessionMeta rewrites the absolute worktree paths stored in a session
// meta from oldRoot to newRoot. It returns true when any field changed.
func rebaseSessionMeta(meta *session.Meta, oldRoot string, newRoot string) bool {
	if meta == nil {
		return false
	}
	return rebaseWorktreeReminder(meta.WorktreeReminder, oldRoot, newRoot)
}
