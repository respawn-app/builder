package app

import (
	"strings"
	"testing"

	"builder/cli/app/commands"
	"builder/cli/tui"
	"builder/server/llm"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSlashCommandEnterIgnoresWhitespaceImmediatelyAfterSlash(t *testing.T) {
	m := newProjectedStaticUIModel()
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
	m := newProjectedStaticUIModel(WithUICommandRegistry(r))
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
	m := newProjectedStaticUIModel()
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
	m := newProjectedStaticUIModel()
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

func TestSlashCommandPickerHidesResumeWithoutOtherSessions(t *testing.T) {
	m := newProjectedStaticUIModel(WithUIHasOtherSessions(true, false))
	m.input = "/re"
	m.refreshSlashCommandFilterFromInput()

	state := m.slashCommandPicker()
	if slashPickerContainsCommand(state, "resume") {
		t.Fatalf("did not expect /resume without other sessions, got %+v", slashPickerCommandNames(state))
	}
}

func TestResumeSlashCommandShowsErrorWithoutOtherSessions(t *testing.T) {
	m := newProjectedStaticUIModel(WithUIHasOtherSessions(true, false))
	m.input = "/resume"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status cmd for unavailable /resume")
	}
	if updated.Action() != UIActionNone {
		t.Fatalf("did not expect session transition action, got %q", updated.Action())
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared for unavailable /resume, got %q", updated.input)
	}
	if !strings.Contains(updated.transientStatus, resumeCommandUnavailableMessage) {
		t.Fatalf("expected unavailable /resume status, got %q", updated.transientStatus)
	}
	status := stripANSIAndTrimRight(updated.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(status, resumeCommandUnavailableMessage) {
		t.Fatalf("expected unavailable /resume status line, got %q", status)
	}
}

func TestSlashCommandPickerShowsCopyOnlyWhenFinalAnswerIsAvailable(t *testing.T) {
	hidden := newProjectedStaticUIModel()
	hidden.input = "/co"
	hidden.refreshSlashCommandFilterFromInput()
	if state := hidden.slashCommandPicker(); slashPickerContainsCommand(state, "copy") {
		t.Fatalf("did not expect /copy without a final answer, got %+v", slashPickerCommandNames(state))
	}

	visible := newProjectedStaticUIModel()
	visible.transcriptEntries = []tui.TranscriptEntry{{Role: "assistant", Text: "done", Phase: llm.MessagePhaseFinal}}
	visible.input = "/co"
	visible.refreshSlashCommandFilterFromInput()
	state := visible.slashCommandPicker()
	if !state.visible {
		t.Fatal("expected slash picker visible")
	}
	if !slashPickerContainsCommand(state, "copy") {
		t.Fatalf("expected /copy in slash picker, got %+v", slashPickerCommandNames(state))
	}
}

func TestRollbackEditHidesSlashCommandPicker(t *testing.T) {
	m := newProjectedStaticUIModel()
	testSetRollbackEditing(m, 0, 1)
	m.input = "/sta"
	m.refreshSlashCommandFilterFromInput()

	state := m.slashCommandPicker()
	if state.visible {
		t.Fatalf("did not expect slash picker visible while editing, got %+v", state)
	}
}

func TestRollbackEditRejectsSlashCommandSubmitAndAutocomplete(t *testing.T) {
	m := newProjectedStaticUIModel()
	testSetRollbackEditing(m, 0, 1)
	m.input = "/status"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status command for blocked edit-mode slash command")
	}
	if updated.busy {
		t.Fatal("did not expect slash command to submit while editing")
	}
	if updated.status.isOpen() {
		t.Fatal("did not expect /status to open while editing")
	}
	if updated.input != "/status" {
		t.Fatalf("expected blocked slash command to remain editable, got %q", updated.input)
	}
	if updated.transientStatus != slashCommandEditModeError {
		t.Fatalf("expected edit-mode slash error, got %q", updated.transientStatus)
	}

	updated.input = "/sta"
	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status command for blocked edit-mode slash autocomplete")
	}
	if updated.input != "/sta" {
		t.Fatalf("expected blocked slash autocomplete to preserve input, got %q", updated.input)
	}
	if updated.transientStatus != slashCommandEditModeError {
		t.Fatalf("expected edit-mode slash autocomplete error, got %q", updated.transientStatus)
	}
	status := stripANSIAndTrimRight(updated.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(status, slashCommandEditModeError) {
		t.Fatalf("expected edit-mode slash error in status line, got %q", status)
	}
}

func TestRollbackEditRejectsUnknownSlashInputWithoutSubmittingPrompt(t *testing.T) {
	m := newProjectedStaticUIModel()
	testSetRollbackEditing(m, 0, 1)
	m.input = "/nope"
	before := stripANSIAndTrimRight(m.view.OngoingSnapshot())

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status command for blocked unknown slash in edit mode")
	}
	if updated.busy {
		t.Fatal("did not expect unknown slash to submit while editing")
	}
	if len(updated.queued) != 0 {
		t.Fatalf("did not expect queued messages, got %+v", updated.queued)
	}
	if updated.Action() != UIActionNone {
		t.Fatalf("did not expect session transition action, got %q", updated.Action())
	}
	if updated.input != "/nope" {
		t.Fatalf("expected blocked unknown slash to remain editable, got %q", updated.input)
	}
	if updated.transientStatus != slashCommandEditModeError {
		t.Fatalf("expected edit-mode slash error, got %q", updated.transientStatus)
	}
	after := stripANSIAndTrimRight(updated.view.OngoingSnapshot())
	if after != before {
		t.Fatalf("did not expect blocked unknown slash to alter transcript, before=%q after=%q", before, after)
	}
}
