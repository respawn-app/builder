package app

import (
	"strings"
	"testing"

	"builder/cli/app/commands"
	"builder/server/runtime"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSlashCommandEnterIgnoresWhitespaceImmediatelyAfterSlash(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.sessionName = "existing"
	m.input = "/ name"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected /name command to update the window title")
	}
	if updated.sessionName != "" {
		t.Fatalf("expected / name to behave like /name with empty args, got %q", updated.sessionName)
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after slash command execution, got %q", updated.input)
	}
}

func TestBuiltInReviewSlashCommandWithWhitespaceAfterSlashDoesNotDuplicateArgs(t *testing.T) {
	r := commands.NewDefaultRegistry()
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUICommandRegistry(r),
	).(*uiModel)
	m.input = "/ review cli/app"
	if got := r.Execute("/review cli/app"); !got.Handled || !got.SubmitUser {
		t.Fatalf("expected /review command to submit injected user prompt, got %+v", got)
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected submission cmd for whitespace-prefixed /review")
	}
	if updated.Action() != UIActionNone {
		t.Fatalf("expected no session transition for empty-session /review, got %q", updated.Action())
	}
	if !updated.busy {
		t.Fatal("expected /review to submit in place for an empty session")
	}
	if updated.nextSessionInitialPrompt != "" {
		t.Fatalf("expected no handoff payload for empty-session /review, got %q", updated.nextSessionInitialPrompt)
	}
	plain := stripANSIAndTrimRight(updated.view.OngoingSnapshot())
	if strings.Contains(plain, "/ review cli/app") {
		t.Fatalf("expected normalized /review prompt content instead of raw command text, got %q", plain)
	}
	if !strings.Contains(plain, "cli/app") {
		t.Fatalf("expected /review args preserved in in-place prompt, got %q", plain)
	}
}

func TestBusyEnterRecognizesExactFastCommandEvenWhenPickerHidesIt(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.input = "/fast on"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status command for blocked busy /fast")
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected no queued messages, got %+v", updated.queued)
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected no pending injected messages, got %+v", updated.pendingInjected)
	}
	if updated.inputSubmitLocked {
		t.Fatal("did not expect locked input for blocked busy /fast")
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared for blocked busy /fast, got %q", updated.input)
	}
	status := stripANSIAndTrimRight(updated.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(status, "cannot run /fast while model is working") {
		t.Fatalf("expected busy /fast error in status line, got %q", status)
	}
}

func TestBusyTabBackWithoutParentShowsLocalErrorAndDoesNotQueue(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.input = "/back"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status command for rejected queued /back")
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected no queued messages, got %+v", updated.queued)
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected no pending injected messages, got %+v", updated.pendingInjected)
	}
	if updated.input != "/back" {
		t.Fatalf("expected input preserved for editing after rejected queued /back, got %q", updated.input)
	}
	if !strings.Contains(updated.transientStatus, "No parent session available") {
		t.Fatalf("expected transient error for rejected queued /back, got %q", updated.transientStatus)
	}
	status := stripANSIAndTrimRight(updated.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(status, "No parent session available") {
		t.Fatalf("expected queued /back error in status line, got %q", status)
	}
}
