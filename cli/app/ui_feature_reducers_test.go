package app

import (
	"testing"

	"builder/shared/clientui"

	tea "github.com/charmbracelet/bubbletea"
)

func TestZeroValueUIModelUsesPromotedFeatureDefaultsSafely(t *testing.T) {
	m := &uiModel{}

	if got := m.inputMode(); got != uiInputModeMain {
		t.Fatalf("zero-value input mode = %q, want %q", got, uiInputModeMain)
	}
	if result := m.reduceFeatureMessage(tea.WindowSizeMsg{Width: 80, Height: 24}); !result.handled {
		t.Fatal("expected zero-value model to route window messages through feature reducers")
	}
	if m.termWidth != 80 || m.termHeight != 24 || !m.windowSizeKnown {
		t.Fatalf("expected promoted window fields updated, got width=%d height=%d known=%t", m.termWidth, m.termHeight, m.windowSizeKnown)
	}
}

func TestUIUpdateRoutesWorktreeMessagesThroughReducer(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.worktrees.open = true
	m.worktrees.loading = true
	m.worktrees.refreshToken = 7

	next, _ := m.Update(worktreeListDoneMsg{token: 7})
	updated := next.(*uiModel)

	if updated.worktrees.loading {
		t.Fatal("expected worktree list completion to be handled by worktree reducer")
	}
}

func TestUIUpdateRoutesProcessRefreshThroughReducer(t *testing.T) {
	m := newProjectedStaticUIModel(WithUIProcessClient(fixedUIProcessClient{
		entries: []clientui.BackgroundProcess{{ID: "proc-1", Command: "sleep 1"}},
	}))
	m.processList.open = true

	next, cmd := m.Update(processListRefreshTickMsg{})
	updated := next.(*uiModel)

	if len(updated.processList.entries) != 1 || updated.processList.entries[0].ID != "proc-1" {
		t.Fatalf("expected process refresh reducer to update entries, got %#v", updated.processList.entries)
	}
	if cmd == nil {
		t.Fatal("expected process refresh reducer to schedule follow-up refresh")
	}
}

func TestUIUpdateRoutesClipboardMessagesThroughReducer(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.mainInputDraftToken = 3

	next, cmd := m.Update(clipboardImagePasteDoneMsg{
		Target:         uiClipboardPasteTargetMain,
		MainDraftToken: 3,
		Path:           "/tmp/image.png",
	})
	updated := next.(*uiModel)

	if updated.input != "/tmp/image.png" {
		t.Fatalf("expected clipboard reducer to insert pasted image path, got %q", updated.input)
	}
	if cmd != nil {
		t.Fatalf("did not expect command after successful clipboard image paste, got %T", cmd())
	}

	next, cmd = updated.Update(clipboardTextCopyDoneMsg{})
	updated = next.(*uiModel)
	if updated.transientStatus != "Copied final answer to clipboard" || updated.transientStatusKind != uiStatusNoticeSuccess {
		t.Fatalf("expected clipboard copy reducer success status, got %q kind=%d", updated.transientStatus, updated.transientStatusKind)
	}
	if cmd == nil {
		t.Fatal("expected clipboard text copy reducer to schedule status clear")
	}
}
