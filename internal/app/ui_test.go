package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/tools/askquestion"
	tea "github.com/charmbracelet/bubbletea"
)

func TestCtrlEnterQueuesAndStartsSubmission(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "echo hi"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	updated := next.(*uiModel)

	if !updated.busy {
		t.Fatal("expected busy after ctrl+enter queued submission")
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared, got %q", updated.input)
	}
}

func TestAskQuestionTabFreeformFlow(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Pick one", Suggestions: []string{"a", "b"}}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	if updated.askFreeform {
		t.Fatal("expected picker mode first")
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)
	if !updated.askFreeform {
		t.Fatal("expected tab to switch to freeform")
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("custom")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	resp := <-reply
	if resp.answer != "custom" {
		t.Fatalf("unexpected answer: %q", resp.answer)
	}
	if updated.activeAsk != nil {
		t.Fatal("ask should be resolved")
	}
}

func TestAskEventsQueueUntilCurrentQuestionAnswered(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply1 := make(chan askReply, 1)
	reply2 := make(chan askReply, 1)

	ask1 := askEvent{req: askquestion.Request{Question: "First", Suggestions: []string{"one"}}, reply: reply1}
	ask2 := askEvent{req: askquestion.Request{Question: "Second", Suggestions: []string{"two"}}, reply: reply2}

	next, _ := m.Update(askEventMsg{event: ask1})
	updated := next.(*uiModel)
	next, _ = updated.Update(askEventMsg{event: ask2})
	updated = next.(*uiModel)

	if updated.activeAsk == nil || updated.activeAsk.req.Question != "First" {
		t.Fatalf("expected first ask to remain active, got %#v", updated.activeAsk)
	}
	if len(updated.askQueue) != 1 {
		t.Fatalf("expected one queued ask, got %d", len(updated.askQueue))
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	first := <-reply1
	if first.answer != "one" {
		t.Fatalf("unexpected first answer: %q", first.answer)
	}
	if updated.activeAsk == nil || updated.activeAsk.req.Question != "Second" {
		t.Fatalf("expected second ask to become active, got %#v", updated.activeAsk)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	second := <-reply2
	if second.answer != "two" {
		t.Fatalf("unexpected second answer: %q", second.answer)
	}
	if updated.activeAsk != nil {
		t.Fatal("expected no active ask after queue is drained")
	}
}

func TestCtrlEnterIdleAppendsUserOnce(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "echo hi"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	updated := next.(*uiModel)

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)

	if count := strings.Count(updated.View(), "user: echo hi"); count != 1 {
		t.Fatalf("expected one user transcript entry, got %d", count)
	}
}

func TestSubmitErrorShowsFullMessageInDetailMode(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	longErr := "openai status 400: " + strings.Repeat("X", 320)

	next, _ := m.Update(submitDoneMsg{err: errors.New(longErr)})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)

	view := updated.View()
	if !strings.Contains(view, "error: "+longErr) {
		t.Fatalf("expected full error in detail mode, got: %q", view)
	}
}

func TestSubmitErrorShowsFullAPIStatusBodyWhenWrapped(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	body := strings.Repeat("AUTH_ERR_", 64)
	root := &llm.APIStatusError{StatusCode: 403, Body: body}
	wrapped := fmt.Errorf("model generation failed after retries: %w", root)

	next, _ := m.Update(submitDoneMsg{err: wrapped})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)

	view := updated.View()
	if !strings.Contains(view, "error: openai status 403") {
		t.Fatalf("expected status line, got: %q", view)
	}
	if !strings.Contains(view, body) {
		t.Fatalf("expected full API body in detail mode, got: %q", view)
	}
}

func TestMainInputAcceptsSpaceKey(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeySpace})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("world")})
	updated = next.(*uiModel)

	if updated.input != "hello world" {
		t.Fatalf("expected input with space, got %q", updated.input)
	}
}

func TestAskFreeformAcceptsSpaceKey(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Type answer"}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeySpace})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("world")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	resp := <-reply
	if resp.answer != "hello world" {
		t.Fatalf("expected freeform answer with space, got %q", resp.answer)
	}
	if updated.activeAsk != nil {
		t.Fatal("ask should be resolved")
	}
}
