package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSharedInputEditKeyCtrlUUsesPlatformSpecificPolicy(t *testing.T) {
	var darwinAction string
	if !handleSharedInputEditKeyForGOOS(tea.KeyMsg{Type: tea.KeyCtrlU}, uiSharedInputEditActions{
		KillToLineStart:   func() bool { darwinAction = "kill-start"; return true },
		DeleteCurrentLine: func() bool { darwinAction = "delete-line"; return true },
	}, "darwin") {
		t.Fatal("expected darwin ctrl+u to be handled")
	}
	if darwinAction != "delete-line" {
		t.Fatalf("darwin ctrl+u action = %q, want delete-line", darwinAction)
	}

	var linuxAction string
	if !handleSharedInputEditKeyForGOOS(tea.KeyMsg{Type: tea.KeyCtrlU}, uiSharedInputEditActions{
		KillToLineStart:   func() bool { linuxAction = "kill-start"; return true },
		DeleteCurrentLine: func() bool { linuxAction = "delete-line"; return true },
	}, "linux") {
		t.Fatal("expected linux ctrl+u to be handled")
	}
	if linuxAction != "kill-start" {
		t.Fatalf("linux ctrl+u action = %q, want kill-start", linuxAction)
	}
}

func TestDeleteCurrentLineKeyCtrlUPlatformCheck(t *testing.T) {
	msg := tea.KeyMsg{Type: tea.KeyCtrlU}
	if !isDeleteCurrentLineKeyForGOOS(msg, "darwin") {
		t.Fatal("expected ctrl+u to delete current line on darwin")
	}
	if isDeleteCurrentLineKeyForGOOS(msg, "linux") {
		t.Fatal("did not expect ctrl+u to delete current line on linux")
	}
}
