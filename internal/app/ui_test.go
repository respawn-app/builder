package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/tools/askquestion"
	"builder/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type testUnknownCSISequence struct {
	rendered string
}

func (m testUnknownCSISequence) String() string {
	return m.rendered
}

func TestTabQueuesAndStartsSubmission(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "echo hi"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated := next.(*uiModel)

	if !updated.busy {
		t.Fatal("expected busy after tab queued submission")
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared, got %q", updated.input)
	}
}

func TestUnknownCSICtrlEnterQueuesAndStartsSubmission(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "echo hi"

	next, _ := m.Update(testUnknownCSISequence{rendered: "?CSI[49 51 59 53 117]?"}) // 13;5u
	updated := next.(*uiModel)

	if !updated.busy {
		t.Fatal("expected busy after ctrl+enter CSI sequence")
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after ctrl+enter CSI sequence, got %q", updated.input)
	}
}

func TestUnknownCSIXtermCtrlEnterQueuesAndStartsSubmission(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "echo hi"

	next, _ := m.Update(testUnknownCSISequence{rendered: "?CSI[50 55 59 53 59 49 51 126]?"}) // 27;5;13~
	updated := next.(*uiModel)

	if !updated.busy {
		t.Fatal("expected busy after xterm ctrl+enter sequence")
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after xterm ctrl+enter sequence, got %q", updated.input)
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

func TestTabIdleAppendsUserOnce(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "echo hi"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated := next.(*uiModel)

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	updated = next.(*uiModel)

	view := stripANSIAndTrimRight(updated.View())
	if count := strings.Count(view, "echo hi"); count != 1 {
		t.Fatalf("expected one user transcript entry, got %d", count)
	}
}

func TestSubmitErrorShowsFullMessageInDetailMode(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	longErr := "openai status 400: " + strings.Repeat("X", 320)

	next, _ := m.Update(submitDoneMsg{err: errors.New(longErr)})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	updated = next.(*uiModel)

	view := updated.View()
	if !strings.Contains(view, "openai status 400:") {
		t.Fatalf("expected status text in detail mode, got: %q", view)
	}
	if strings.Count(view, "X") < 320 {
		t.Fatalf("expected full wrapped body in detail mode, got: %q", view)
	}
}

func TestSubmitErrorShowsFullAPIStatusBodyWhenWrapped(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	body := strings.Repeat("AUTH_ERR_", 64)
	root := &llm.APIStatusError{StatusCode: 403, Body: body}
	wrapped := fmt.Errorf("model generation failed after retries: %w", root)

	next, _ := m.Update(submitDoneMsg{err: wrapped})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	updated = next.(*uiModel)

	view := updated.View()
	if !strings.Contains(view, "openai status 403") {
		t.Fatalf("expected status line, got: %q", view)
	}
	joined := strings.ReplaceAll(view, "\n", "")
	if strings.Count(joined, "AUTH_ERR_") < 64 {
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

func TestBusyInputRemainsEditableUntilSubmitLock(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "seed"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeySpace})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	updated = next.(*uiModel)

	if updated.input != "seedx" {
		t.Fatalf("expected input to remain editable while busy, got %q", updated.input)
	}
	if strings.Contains(updated.View(), "input locked while agent is running") {
		t.Fatalf("did not expect legacy locked hint in view: %q", updated.View())
	}
}

func TestViewRendersSoftCursorForEditableInput(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 40
	m.termHeight = 16
	m.input = "hello world"

	view := m.View()
	if !strings.Contains(view, ansiHideCursor) {
		t.Fatalf("expected terminal cursor hidden in view: %q", view)
	}
	plain := stripANSIAndTrimRight(view)
	if !strings.Contains(plain, "› hello world"+softCursorGlyph) {
		t.Fatalf("expected soft cursor rendered at input end, got %q", plain)
	}
}

func TestViewHidesCursorWhenInputLocked(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 40
	m.termHeight = 16
	m.inputSubmitLocked = true
	m.input = "hello world"

	view := m.View()
	if !strings.Contains(view, ansiHideCursor) {
		t.Fatalf("expected terminal cursor hide sequence in view: %q", view)
	}
	plain := stripANSIAndTrimRight(view)
	if strings.Contains(plain, softCursorGlyph) {
		t.Fatalf("did not expect soft cursor while input locked, got %q", plain)
	}
}

func TestBusyEnterLocksInputUntilFlushed(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "please continue with tests"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if !updated.inputSubmitLocked {
		t.Fatal("expected input submit lock after enter while busy")
	}
	if updated.input != "please continue with tests" {
		t.Fatalf("expected input text preserved while locked, got %q", updated.input)
	}
	if len(updated.pendingInjected) != 1 {
		t.Fatalf("expected one pending injected message, got %d", len(updated.pendingInjected))
	}

	next, _ = updated.Update(runtimeEventMsg{event: runtime.Event{
		Kind:        runtime.EventUserMessageFlushed,
		UserMessage: "please continue with tests",
	}})
	updated = next.(*uiModel)
	if updated.inputSubmitLocked {
		t.Fatal("expected input unlock after flush")
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after flush, got %q", updated.input)
	}
}

func TestBusyTabQueuesInjectionAndKeepsInputUnlocked(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "queue this"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated := next.(*uiModel)
	if len(updated.pendingInjected) != 1 {
		t.Fatalf("expected one pending injected message, got %d", len(updated.pendingInjected))
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after tab while busy, got %q", updated.input)
	}
	if updated.inputSubmitLocked {
		t.Fatal("did not expect submit lock for tab queue")
	}
}

func TestRenderInputLinesUsesHorizontalBordersOnly(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 40
	m.termHeight = 16
	m.input = "hello world"

	lines := m.renderInputLines(40, uiThemeStyles("dark"))
	if len(lines) < 3 {
		t.Fatalf("expected bordered input block, got %d lines", len(lines))
	}
	if !strings.Contains(lines[0], "─") {
		t.Fatalf("expected top horizontal border, got %q", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], "─") {
		t.Fatalf("expected bottom horizontal border, got %q", lines[len(lines)-1])
	}

	joined := strings.Join(lines, "\n")
	if strings.ContainsAny(joined, "│╭╮╰╯") {
		t.Fatalf("expected no vertical/corner border glyphs, got %q", joined)
	}
}

func TestCalcChatLinesShrinksWhenInputWraps(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 20
	m.termHeight = 12

	m.input = "short"
	chatShort := m.calcChatLines()

	m.input = strings.Repeat("x", 120)
	chatLong := m.calcChatLines()

	if chatLong >= chatShort {
		t.Fatalf("expected wrapped input to reduce chat lines: short=%d long=%d", chatShort, chatLong)
	}
}

func TestRenderChatPanelRendersFullWidthMetaDivider(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	style := uiThemeStyles("dark")

	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "hello"})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "world"})
	m.forwardToView(tui.ToggleModeMsg{})

	width := 44
	lines := m.renderChatPanel(width, 8, style)
	expected := style.meta.Render(strings.Repeat("─", width))

	found := false
	for _, line := range lines {
		if line == expected {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected full-width meta divider in chat panel, got %q", strings.Join(lines, "\n"))
	}
}

func TestSlashCommandSetsExitAction(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/exit"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected quit cmd for /exit")
	}
	updated := next.(*uiModel)
	if updated.Action() != UIActionExit {
		t.Fatalf("expected UIActionExit, got %q", updated.Action())
	}
}

func TestInitialTranscriptVisibleImmediately(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{
			{Role: "user", Text: "hello"},
			{Role: "assistant", Text: "world"},
		}),
	).(*uiModel)
	m.termWidth = 80
	m.termHeight = 20

	ongoing := stripANSIAndTrimRight(m.View())
	if !strings.Contains(ongoing, "world") {
		t.Fatalf("expected resumed content in ongoing mode, got %q", ongoing)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	detail := stripANSIAndTrimRight(next.(*uiModel).View())
	if !containsInOrder(detail, "❯", "hello", "❮", "world") {
		t.Fatalf("expected resumed transcript in detail mode, got %q", detail)
	}
}

func stripANSIAndTrimRight(view string) string {
	stripped := ansi.Strip(view)
	lines := strings.Split(stripped, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

func containsInOrder(text string, parts ...string) bool {
	offset := 0
	for _, part := range parts {
		idx := strings.Index(text[offset:], part)
		if idx < 0 {
			return false
		}
		offset += idx + len(part)
	}
	return true
}
