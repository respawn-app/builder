package app

import (
	"testing"

	"builder/internal/app/commands"
	"builder/internal/runtime"

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
	m.input = "/ review internal/app"
	expected := r.Execute("/review internal/app")
	if !expected.Handled || !expected.SubmitUser {
		t.Fatalf("expected /review command to submit injected user prompt, got %+v", expected)
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected quit cmd for whitespace-prefixed /review fresh-conversation handoff")
	}
	if updated.Action() != UIActionNewSession {
		t.Fatalf("expected UIActionNewSession, got %q", updated.Action())
	}
	if updated.nextSessionInitialPrompt != expected.User {
		t.Fatalf("expected handoff payload to match normalized /review command output\nwant: %q\n got: %q", expected.User, updated.nextSessionInitialPrompt)
	}
}
