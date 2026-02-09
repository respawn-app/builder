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

func TestUnknownCSIShiftEnterInsertsNewline(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "hello"

	next, _ := m.Update(testUnknownCSISequence{rendered: "?CSI[49 51 59 50 117]?"}) // 13;2u
	updated := next.(*uiModel)

	if updated.busy {
		t.Fatal("did not expect busy after shift+enter CSI sequence")
	}
	if updated.input != "hello\n" {
		t.Fatalf("expected newline insertion from shift+enter CSI sequence, got %q", updated.input)
	}
}

func TestUnknownCSICtrlBackspaceDeletesCurrentLine(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "one\ntwo\nthree"
	m.inputCursor = 5 // inside "two"

	next, _ := m.Update(testUnknownCSISequence{rendered: "?CSI[49 50 55 59 53 117]?"}) // 127;5u
	updated := next.(*uiModel)

	if updated.input != "one\nthree" {
		t.Fatalf("expected ctrl+backspace CSI to remove current line, got %q", updated.input)
	}
	if updated.inputCursor != 4 {
		t.Fatalf("expected cursor at start of joined line after delete, got %d", updated.inputCursor)
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

func TestMainInputCtrlJInsertsNewline(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "line 1"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	updated := next.(*uiModel)

	if updated.busy {
		t.Fatal("did not expect submit on ctrl+j")
	}
	if updated.input != "line 1\n" {
		t.Fatalf("expected ctrl+j to insert newline, got %q", updated.input)
	}
}

func TestMainInputCtrlBackspaceDeletesCurrentLine(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "111\n22\n333"
	m.inputCursor = 5 // second line

	next, _ := m.Update(tea.KeyMsg{Type: keyTypeCtrlBackspaceCSI})
	updated := next.(*uiModel)

	if updated.input != "111\n333" {
		t.Fatalf("expected ctrl+backspace to remove current line, got %q", updated.input)
	}
	if updated.inputCursor != 4 {
		t.Fatalf("expected cursor at start of remaining line, got %d", updated.inputCursor)
	}
}

func TestMainInputCmdBackspaceDeletesCurrentLine(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "aaa\nbbb\nccc"
	m.inputCursor = 9 // third line

	next, _ := m.Update(tea.KeyMsg{Type: keyTypeSuperBackspaceCSI})
	updated := next.(*uiModel)

	if updated.input != "aaa\nbbb" {
		t.Fatalf("expected cmd+backspace to remove current line, got %q", updated.input)
	}
	if updated.inputCursor != 7 {
		t.Fatalf("expected cursor at end of remaining text, got %d", updated.inputCursor)
	}
}

func TestMainInputSupportsInlineCursorEditing(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "hello world"

	next := tea.Model(m)
	for range 5 {
		next, _ = next.(*uiModel).Update(tea.KeyMsg{Type: tea.KeyLeft})
	}
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("_")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyHome})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(">")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnd})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	updated = next.(*uiModel)

	if updated.input != ">hello _worl" {
		t.Fatalf("unexpected inline edit result: %q", updated.input)
	}
}

func TestMainInputSupportsWordNavigation(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "alpha beta gamma"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlLeft})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	updated = next.(*uiModel)

	if updated.input != "alpha beta Xgamma" {
		t.Fatalf("expected ctrl+left insertion near word boundary, got %q", updated.input)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Y")})
	updated = next.(*uiModel)

	if updated.input != "alpha beta YXgamma" {
		t.Fatalf("expected alt+left insertion near previous word boundary, got %q", updated.input)
	}
}

func TestMainInputUpDownSingleLineMoveToStartAndEnd(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "abcd"
	m.inputCursor = 2

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := next.(*uiModel)
	if updated.inputCursor != 0 {
		t.Fatalf("expected up to move cursor to start on single line, got %d", updated.inputCursor)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.inputCursor != len([]rune(updated.input)) {
		t.Fatalf("expected down to move cursor to end on single line, got %d", updated.inputCursor)
	}
}

func TestMainInputUpDownMultilineMoveAcrossLines(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "1111\n22\n3333"
	m.inputCursor = -1 // end of the input

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := next.(*uiModel)
	if updated.inputCursor != 7 {
		t.Fatalf("expected first up to land on previous line end, got %d", updated.inputCursor)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = next.(*uiModel)
	if updated.inputCursor != 2 {
		t.Fatalf("expected second up to keep column on first line, got %d", updated.inputCursor)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.inputCursor != 7 {
		t.Fatalf("expected down to return to second line at same column, got %d", updated.inputCursor)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.inputCursor != 10 {
		t.Fatalf("expected second down to land on third line at same column, got %d", updated.inputCursor)
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

func TestViewRendersOverlayCursorWithoutShiftingText(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 40
	m.termHeight = 16
	m.input = "hello world"

	view := m.View()
	if !strings.Contains(view, ansiHideCursor) {
		t.Fatalf("expected terminal cursor hidden in view: %q", view)
	}
	plain := stripANSIAndTrimRight(view)
	if !strings.Contains(plain, "› hello world") {
		t.Fatalf("expected input text preserved in view, got %q", plain)
	}
}

func TestViewCursorMovementDoesNotDropCharacters(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 40
	m.termHeight = 16
	m.input = "hello"
	m.inputCursor = 2

	plain := stripANSIAndTrimRight(m.View())
	if !strings.Contains(plain, "› hello") {
		t.Fatalf("expected all characters preserved while moving cursor, got %q", plain)
	}
}

func TestInputCursorDisplayPositionMovesToNextLineAfterNewline(t *testing.T) {
	line, col := inputCursorDisplayPosition("› ", "abc\n", -1, 40)
	if line != 1 || col != 0 {
		t.Fatalf("expected cursor to move to start of next line after trailing newline, got line=%d col=%d", line, col)
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
	if !strings.Contains(plain, "⨯ hello world") {
		t.Fatalf("expected locked input text preserved, got %q", plain)
	}
}

func TestArrowNavigationDoesNotMutateInput(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "abcdef"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyLeft})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyHome})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnd})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyCtrlLeft})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyCtrlRight})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	updated = next.(*uiModel)

	if updated.input != "abcdef" {
		t.Fatalf("expected navigation keys not to mutate input, got %q", updated.input)
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

func TestSubmitErrorUnlocksInputAndClearsLockedPendingState(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "please continue with tests"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if !updated.inputSubmitLocked {
		t.Fatal("expected input submit lock after enter while busy")
	}
	if len(updated.pendingInjected) != 1 {
		t.Fatalf("expected one pending injected message, got %d", len(updated.pendingInjected))
	}

	updated.queued = append(updated.queued, "follow-up")
	next, cmd := updated.Update(submitDoneMsg{err: errors.New("network failure")})
	updated = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected follow-up queued submission to start")
	}
	if !updated.busy {
		t.Fatal("expected busy after starting follow-up submission")
	}
	if updated.inputSubmitLocked {
		t.Fatal("expected submit lock cleared after submission error")
	}
	if updated.lockedInjectText != "" {
		t.Fatalf("expected lockedInjectText cleared, got %q", updated.lockedInjectText)
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected locked pending injection removed, got %d", len(updated.pendingInjected))
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

func TestCompactDoneUnlocksInputAndClearsLockedPendingState(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "please continue with tests"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if !updated.inputSubmitLocked {
		t.Fatal("expected input submit lock after enter while busy")
	}
	if len(updated.pendingInjected) != 1 {
		t.Fatalf("expected one pending injected message, got %d", len(updated.pendingInjected))
	}

	next, _ = updated.Update(compactDoneMsg{})
	updated = next.(*uiModel)
	if updated.inputSubmitLocked {
		t.Fatal("expected submit lock cleared after compaction completion")
	}
	if updated.lockedInjectText != "" {
		t.Fatalf("expected lockedInjectText cleared, got %q", updated.lockedInjectText)
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected locked pending injection removed, got %d", len(updated.pendingInjected))
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
