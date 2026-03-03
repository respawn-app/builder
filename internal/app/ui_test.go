package app

import (
	"context"
	"errors"
	"fmt"
	goruntime "runtime"
	"strings"
	"testing"

	"builder/internal/app/commands"
	"builder/internal/config"
	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/session"
	"builder/internal/tools"
	"builder/internal/tools/askquestion"
	"builder/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

func TestUnknownCSICtrlEnterQueuesPostTurnWhenBusy(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "echo hi"

	next, _ := m.Update(testUnknownCSISequence{rendered: "?CSI[49 51 59 53 117]?"}) // 13;5u
	updated := next.(*uiModel)

	if len(updated.queued) != 1 {
		t.Fatalf("expected one queued post-turn message, got %d", len(updated.queued))
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("did not expect injected steering messages, got %d", len(updated.pendingInjected))
	}
	if updated.inputSubmitLocked {
		t.Fatal("did not expect submit lock for ctrl+enter queue")
	}
}

func TestUnknownCSIShiftEnterInsertsNewline(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "hello"

	next, _ := m.Update(testUnknownCSISequence{rendered: "?CSI[50 55 59 50 59 49 51 117]?"}) // 27;2;13u
	updated := next.(*uiModel)

	if updated.busy {
		t.Fatal("did not expect busy after shift+enter CSI sequence")
	}
	if updated.input != "hello\n" {
		t.Fatalf("expected newline insertion from shift+enter CSI sequence, got %q", updated.input)
	}
}

func TestUnknownCSIShiftEnterThenEnterDoesNotSubmitTrailingNewline(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "hello"

	next, _ := m.Update(testUnknownCSISequence{rendered: "?CSI[49 51 59 50 117]?"}) // 13;2u
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	if !updated.busy {
		t.Fatal("expected submission started")
	}
	snapshot := stripANSIAndTrimRight(updated.view.OngoingCommittedSnapshot())
	if strings.Contains(snapshot, "❯ hello\n\n") {
		t.Fatalf("expected submitted user message without trailing blank line, got %q", snapshot)
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

func TestUnknownCSICtrlBackspaceWithSubtypeDeletesCurrentLine(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "one\ntwo\nthree"
	m.inputCursor = 5 // inside "two"

	next, _ := m.Update(testUnknownCSISequence{rendered: "?CSI[49 50 55 59 53 58 51 117]?"}) // 127;5:3u
	updated := next.(*uiModel)

	if updated.input != "one\nthree" {
		t.Fatalf("expected ctrl+backspace CSI with subtype to remove current line, got %q", updated.input)
	}
	if updated.inputCursor != 4 {
		t.Fatalf("expected cursor at start of joined line after delete, got %d", updated.inputCursor)
	}
}

func TestParseUserShellCommand(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantCmd string
		wantOK  bool
	}{
		{name: "basic", input: "$ pwd", wantCmd: "pwd", wantOK: true},
		{name: "leading spaces", input: "   $   echo hi", wantCmd: "echo hi", wantOK: true},
		{name: "empty", input: "$", wantCmd: "", wantOK: false},
		{name: "not shell prefix", input: "echo $HOME", wantCmd: "", wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotCmd, gotOK := parseUserShellCommand(tc.input)
			if gotOK != tc.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tc.wantOK)
			}
			if gotCmd != tc.wantCmd {
				t.Fatalf("command = %q, want %q", gotCmd, tc.wantCmd)
			}
		})
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

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<64;55;24M[<64;56;26M[<65;56;26M")})
	updated = next.(*uiModel)
	if updated.askInput != "" {
		t.Fatalf("expected mouse sgr sequence ignored in ask freeform input, got %q", updated.askInput)
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

func TestAskPromptUsesCheckmarkAndSingleLineHint(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Pick one", Suggestions: []string{"a", "b"}}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	lines := updated.renderInputLines(100, uiThemeStyles("dark"))
	plain := stripANSIAndTrimRight(strings.Join(lines, "\n"))

	if !strings.Contains(plain, "Pick one") {
		t.Fatalf("expected question text, got %q", plain)
	}
	if strings.Contains(plain, "question>") {
		t.Fatalf("expected no legacy question prefix, got %q", plain)
	}
	if strings.Contains(plain, "none of the above") {
		t.Fatalf("expected no fallback option, got %q", plain)
	}
	if !strings.Contains(plain, "✓ 1. a") {
		t.Fatalf("expected checkmark-selected first option, got %q", plain)
	}
	if strings.Contains(plain, "> 1. a") {
		t.Fatalf("expected no chevron selector, got %q", plain)
	}
	if !strings.Contains(plain, "Tab to switch to freeform • Enter to submit") {
		t.Fatalf("expected single-line hint, got %q", plain)
	}
}

func TestApprovalAskSupportsDenyWithCommentary(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Approve?", Suggestions: []string{"Allow once", "Allow for this session", "Deny"}, Approval: true}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	lines := updated.renderInputLines(120, uiThemeStyles("dark"))
	plain := stripANSIAndTrimRight(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "4. Deny, and add commentary") {
		t.Fatalf("expected deny-commentary option, got %q", plain)
	}
	if !strings.Contains(plain, "Tab to allow and add commentary • Enter to submit") {
		t.Fatalf("expected approval hint line, got %q", plain)
	}

	for i := 0; i < 3; i++ {
		next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
		updated = next.(*uiModel)
	}
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if !updated.askFreeform {
		t.Fatal("expected commentary option to switch to freeform")
	}
	lines = updated.renderInputLines(120, uiThemeStyles("dark"))
	plain = stripANSIAndTrimRight(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "Your comment:") {
		t.Fatalf("expected minimal deny-commentary prompt, got %q", plain)
	}
	if strings.Contains(plain, "Approve?") || strings.Contains(plain, "Tab to allow and add commentary") {
		t.Fatalf("expected no question/hint in deny-commentary prompt, got %q", plain)
	}
	select {
	case <-reply:
		t.Fatal("did not expect answer submission before commentary")
	default:
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("blocked by policy")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	resp := <-reply
	if resp.answer != "blocked by policy" {
		t.Fatalf("unexpected commentary answer: %q", resp.answer)
	}
	if updated.activeAsk != nil {
		t.Fatal("expected ask to resolve after commentary submit")
	}
}

func TestDetailModeHidesInputBox(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 16
	m.input = "draft input should be hidden"
	m.syncViewport()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	updated := next.(*uiModel)
	if updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("mode=%q want detail", updated.view.Mode())
	}

	view := ansi.Strip(updated.View())
	if strings.Contains(view, "draft input should be hidden") {
		t.Fatalf("expected detail mode to hide input text, got %q", view)
	}
	if strings.Contains(view, "› ") {
		t.Fatalf("expected detail mode to hide input prompt, got %q", view)
	}
}

func TestDoubleEscEntersRollbackSelectionAndEnterStartsEditing(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIInitialTranscript([]UITranscriptEntry{
		{Role: "user", Text: "u1"},
		{Role: "assistant", Text: "a1"},
		{Role: "user", Text: "u2"},
	})).(*uiModel)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)

	if !updated.rollbackMode {
		t.Fatal("expected rollback selection mode after double esc")
	}
	if updated.rollbackSelection != 1 {
		t.Fatalf("expected last user message selected by default, got %d", updated.rollbackSelection)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if !updated.rollbackEditing {
		t.Fatal("expected rollback editing mode after enter")
	}
	if updated.rollbackMode {
		t.Fatal("did not expect rollback selection mode while editing")
	}
	if updated.input != "u2" {
		t.Fatalf("expected selected message loaded into input, got %q", updated.input)
	}
}

func TestRollbackEditingEscRequiresEmptyInput(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIInitialTranscript([]UITranscriptEntry{
		{Role: "user", Text: "u1"},
		{Role: "assistant", Text: "a1"},
		{Role: "user", Text: "u2"},
	})).(*uiModel)
	m.rollbackEditing = true
	m.rollbackSelection = 1
	m.rollbackSelectedUserMessageIndex = 2
	m.input = "edited"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := next.(*uiModel)
	if !updated.rollbackEditing {
		t.Fatal("expected rollback editing to stay active while input non-empty")
	}
	if updated.rollbackMode {
		t.Fatal("did not expect rollback selection mode while input non-empty")
	}

	updated.input = ""
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !updated.rollbackMode {
		t.Fatal("expected rollback selection mode after esc on empty input")
	}
	if updated.rollbackSelection != 1 {
		t.Fatalf("expected rollback selection preserved, got %d", updated.rollbackSelection)
	}
}

func TestRollbackEditingSubmitQuitsIntoForkTransition(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.rollbackEditing = true
	m.rollbackSelectedUserMessageIndex = 3
	m.input = "edited user message"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if updated.exitAction != UIActionForkRollback {
		t.Fatalf("expected fork rollback action, got %q", updated.exitAction)
	}
	if updated.nextForkUserMessageIndex != 3 {
		t.Fatalf("expected rollback user index, got %d", updated.nextForkUserMessageIndex)
	}
	if updated.nextSessionInitialPrompt != "edited user message" {
		t.Fatalf("expected startup prompt to match edited input, got %q", updated.nextSessionInitialPrompt)
	}
}

func TestRollbackSelectionRecentersTranscript(t *testing.T) {
	entries := make([]UITranscriptEntry, 0, 80)
	for i := 0; i < 40; i++ {
		entries = append(entries, UITranscriptEntry{Role: "user", Text: fmt.Sprintf("u-%d", i)})
		entries = append(entries, UITranscriptEntry{Role: "assistant", Text: fmt.Sprintf("a-%d", i)})
	}
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIInitialTranscript(entries)).(*uiModel)
	m.termWidth = 100
	m.termHeight = 8
	m.syncViewport()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)

	before := updated.view.OngoingScroll()
	for i := 0; i < 8; i++ {
		next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
		updated = next.(*uiModel)
	}
	after := updated.view.OngoingScroll()
	if after >= before {
		t.Fatalf("expected rollback selection movement to recenter upwards, got %d from %d", after, before)
	}

	selected := updated.rollbackCandidates[updated.rollbackSelection].Text
	lines := strings.Split(stripANSIAndTrimRight(updated.view.View()), "\n")
	selectedLine := -1
	for idx, line := range lines {
		if strings.Contains(line, selected) {
			selectedLine = idx
			break
		}
	}
	if selectedLine < 0 {
		t.Fatalf("expected selected rollback message %q visible in viewport", selected)
	}
	mid := len(lines) / 2
	if diff := absInt(selectedLine - mid); diff > 2 {
		t.Fatalf("expected selected rollback message near viewport middle, line=%d mid=%d", selectedLine, mid)
	}
}

func TestRollbackSelectionCancelRestoresPriorOngoingScroll(t *testing.T) {
	entries := make([]UITranscriptEntry, 0, 120)
	for i := 0; i < 60; i++ {
		entries = append(entries, UITranscriptEntry{Role: "user", Text: fmt.Sprintf("u-%d", i)})
		entries = append(entries, UITranscriptEntry{Role: "assistant", Text: fmt.Sprintf("a-%d", i)})
	}
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIInitialTranscript(entries)).(*uiModel)
	m.termWidth = 100
	m.termHeight = 10
	m.syncViewport()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	updated := next.(*uiModel)
	initialScroll := updated.view.OngoingScroll()
	if initialScroll <= 0 {
		t.Fatalf("expected non-zero ongoing scroll after page up, got %d", initialScroll)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !updated.rollbackMode {
		t.Fatal("expected rollback mode after double esc")
	}

	for i := 0; i < 6; i++ {
		next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
		updated = next.(*uiModel)
	}
	if movedScroll := updated.view.OngoingScroll(); movedScroll == initialScroll {
		t.Fatalf("expected rollback focus to move scroll from %d", initialScroll)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if updated.rollbackMode {
		t.Fatal("expected rollback mode to be canceled")
	}
	if got := updated.view.OngoingScroll(); got != initialScroll {
		t.Fatalf("expected ongoing scroll restored to %d, got %d", initialScroll, got)
	}
}

func TestRollbackTransitionsUseDetailOverlayInNativeMode(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIScrollMode(config.TUIScrollModeNative),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "u1"}, {Role: "assistant", Text: "a1"}, {Role: "user", Text: "u2"}}),
	).(*uiModel)
	m.termWidth = 100
	m.termHeight = 10
	m.syncViewport()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := next.(*uiModel)
	next, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !updated.rollbackMode {
		t.Fatal("expected rollback mode after double esc")
	}
	if updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected rollback selection in detail overlay, got mode %q", updated.view.Mode())
	}
	if !updated.rollbackOverlayPushed {
		t.Fatal("expected rollback overlay to be pushed in native mode")
	}
	if cmd == nil {
		t.Fatal("expected native rollback entry to emit detail overlay transition command")
	}

	selected := updated.rollbackCandidates[updated.rollbackSelection].Text
	lines := strings.Split(stripANSIAndTrimRight(updated.View()), "\n")
	selectedLine := -1
	for idx, line := range lines {
		if strings.Contains(line, selected) {
			selectedLine = idx
			break
		}
	}
	if selectedLine < 0 {
		t.Fatalf("expected selected rollback message %q visible in detail overlay", selected)
	}
	mid := len(lines) / 2
	if diff := absInt(selectedLine - mid); diff > 2 {
		t.Fatalf("expected selected rollback message near overlay center, line=%d mid=%d", selectedLine, mid)
	}

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if updated.rollbackMode {
		t.Fatal("expected rollback mode canceled")
	}
	if updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected cancel to return to ongoing mode, got %q", updated.view.Mode())
	}
	if updated.rollbackOverlayPushed {
		t.Fatal("expected rollback overlay state cleared after cancel")
	}
	if cmd == nil {
		t.Fatal("expected native rollback cancel to emit detail overlay exit command")
	}
}

func TestNativeRollbackOverlayUsesClearScreenWhenAltScreenNever(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIScrollMode(config.TUIScrollModeNative),
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenNever),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "u1"}, {Role: "assistant", Text: "a1"}, {Role: "user", Text: "u2"}}),
	).(*uiModel)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := next.(*uiModel)
	next, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !updated.rollbackMode {
		t.Fatal("expected rollback mode after double esc")
	}
	if updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected detail overlay mode, got %q", updated.view.Mode())
	}
	if cmd == nil {
		t.Fatal("expected explicit clear-screen command when native rollback overlay enters with alt-screen disabled")
	}

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if updated.rollbackMode {
		t.Fatal("expected rollback mode canceled")
	}
	if updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected return to ongoing mode, got %q", updated.view.Mode())
	}
	if cmd == nil {
		t.Fatal("expected explicit clear-screen command when native rollback overlay exits with alt-screen disabled")
	}
}

func TestNativeRollbackOverlayFullSelectionFlowPreservesHistory(t *testing.T) {
	entries := make([]UITranscriptEntry, 0, 200)
	for i := 0; i < 100; i++ {
		entries = append(entries, UITranscriptEntry{Role: "user", Text: fmt.Sprintf("u-%03d", i)})
		entries = append(entries, UITranscriptEntry{Role: "assistant", Text: fmt.Sprintf("a-%03d", i)})
	}
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIScrollMode(config.TUIScrollModeNative),
		WithUIInitialTranscript(entries),
	).(*uiModel)

	next, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 14})
	updated := next.(*uiModel)
	if startupCmd == nil {
		t.Fatal("expected native startup replay command")
	}
	committedBefore := stripANSIAndTrimRight(updated.view.OngoingCommittedSnapshot())

	assertSelectionCentered := func(model *uiModel) {
		t.Helper()
		selected := model.rollbackCandidates[model.rollbackSelection].Text
		lines := strings.Split(stripANSIAndTrimRight(model.View()), "\n")
		selectedLine := -1
		for idx, line := range lines {
			if strings.Contains(line, selected) {
				selectedLine = idx
				break
			}
		}
		if selectedLine < 0 {
			t.Fatalf("expected selected rollback message %q visible in overlay", selected)
		}
		mid := len(lines) / 2
		if diff := absInt(selectedLine - mid); diff > 3 {
			t.Fatalf("expected selected rollback message near overlay center, line=%d mid=%d", selectedLine, mid)
		}
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !updated.rollbackMode || updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected rollback selection detail overlay, mode=%q rollback=%t", updated.view.Mode(), updated.rollbackMode)
	}
	assertSelectionCentered(updated)

	for i := 0; i < 8; i++ {
		next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
		updated = next.(*uiModel)
		assertSelectionCentered(updated)
	}
	for i := 0; i < 3; i++ {
		next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
		updated = next.(*uiModel)
		assertSelectionCentered(updated)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if !updated.rollbackEditing || updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected rollback editing in ongoing mode, mode=%q editing=%t", updated.view.Mode(), updated.rollbackEditing)
	}

	updated.input = ""
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !updated.rollbackMode || updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected esc from empty edit input to return to rollback overlay, mode=%q rollback=%t", updated.view.Mode(), updated.rollbackMode)
	}
	assertSelectionCentered(updated)

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if updated.rollbackMode || updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected final esc to cancel rollback overlay back to ongoing, mode=%q rollback=%t", updated.view.Mode(), updated.rollbackMode)
	}

	committedAfter := stripANSIAndTrimRight(updated.view.OngoingCommittedSnapshot())
	if committedAfter != committedBefore {
		t.Fatal("expected committed history unchanged after rollback overlay cancel chain")
	}
	if cmd := updated.syncNativeHistoryFromTranscript(); cmd != nil {
		t.Fatalf("expected no native replay delta after rollback overlay cancel chain, got %T", cmd())
	}
}

func TestNativeRollbackEditCancelPreservesCommittedHistory(t *testing.T) {
	entries := make([]UITranscriptEntry, 0, 80)
	for i := 0; i < 40; i++ {
		entries = append(entries, UITranscriptEntry{Role: "user", Text: fmt.Sprintf("u-%d", i)})
		entries = append(entries, UITranscriptEntry{Role: "assistant", Text: fmt.Sprintf("a-%d", i)})
	}
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIScrollMode(config.TUIScrollModeNative),
		WithUIInitialTranscript(entries),
	).(*uiModel)

	next, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 14})
	updated := next.(*uiModel)
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}
	originalCommitted := stripANSIAndTrimRight(updated.view.OngoingCommittedSnapshot())

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !updated.rollbackMode || updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected native rollback selection in detail overlay, mode=%q rollback=%t", updated.view.Mode(), updated.rollbackMode)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if !updated.rollbackEditing || updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected rollback editing in ongoing mode, mode=%q editing=%t", updated.view.Mode(), updated.rollbackEditing)
	}

	updated.input = ""
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !updated.rollbackMode || updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected esc from empty edit input to restore rollback selection overlay, mode=%q rollback=%t", updated.view.Mode(), updated.rollbackMode)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if updated.rollbackMode {
		t.Fatal("expected rollback mode canceled")
	}
	if updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected ongoing mode after cancel chain, got %q", updated.view.Mode())
	}

	afterCommitted := stripANSIAndTrimRight(updated.view.OngoingCommittedSnapshot())
	if afterCommitted != originalCommitted {
		t.Fatalf("expected committed history preserved after rollback cancel chain")
	}
	if cmd := updated.syncNativeHistoryFromTranscript(); cmd != nil {
		t.Fatalf("expected no native replay delta after rollback cancel chain, got %T", cmd())
	}
}

func TestRollbackEditCancelChainRestoresPriorOngoingScroll(t *testing.T) {
	entries := make([]UITranscriptEntry, 0, 120)
	for i := 0; i < 60; i++ {
		entries = append(entries, UITranscriptEntry{Role: "user", Text: fmt.Sprintf("u-%d", i)})
		entries = append(entries, UITranscriptEntry{Role: "assistant", Text: fmt.Sprintf("a-%d", i)})
	}
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIInitialTranscript(entries)).(*uiModel)
	m.termWidth = 100
	m.termHeight = 10
	m.syncViewport()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	updated := next.(*uiModel)
	initialScroll := updated.view.OngoingScroll()
	if initialScroll <= 0 {
		t.Fatalf("expected non-zero ongoing scroll after page up, got %d", initialScroll)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !updated.rollbackMode {
		t.Fatal("expected rollback mode after double esc")
	}
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if !updated.rollbackEditing {
		t.Fatal("expected rollback editing mode after enter")
	}

	updated.input = ""
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !updated.rollbackMode {
		t.Fatal("expected rollback selection mode after esc on empty edit input")
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if updated.rollbackMode {
		t.Fatal("expected rollback mode canceled")
	}
	if got := updated.view.OngoingScroll(); got != initialScroll {
		t.Fatalf("expected ongoing scroll restored to %d, got %d", initialScroll, got)
	}

	beforeAppend := updated.view.OngoingScroll()
	updated.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "new tail"})
	afterAppend := updated.view.OngoingScroll()
	if afterAppend != beforeAppend {
		t.Fatalf("expected ongoing scroll to remain stable after append when not at bottom, got %d from %d", afterAppend, beforeAppend)
	}
}

func TestRollbackTransitionsDoNotClearScreenWhenNotInAltScreen(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIScrollMode(config.TUIScrollModeAlt),
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenAuto),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "u1"}, {Role: "assistant", Text: "a1"}, {Role: "user", Text: "u2"}}),
	).(*uiModel)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := next.(*uiModel)
	next, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !updated.rollbackMode {
		t.Fatal("expected rollback mode after double esc")
	}
	if cmd != nil {
		t.Fatal("expected no clear-screen command when main UI is not in alt-screen")
	}

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if !updated.rollbackEditing {
		t.Fatal("expected rollback editing mode after enter")
	}
	if cmd != nil {
		t.Fatal("expected no clear-screen command when entering rollback edit outside alt-screen")
	}

	updated.input = ""
	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !updated.rollbackMode {
		t.Fatal("expected rollback mode after esc from empty rollback edit")
	}
	if cmd != nil {
		t.Fatal("expected no clear-screen command when canceling rollback edit outside alt-screen")
	}

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if updated.rollbackMode {
		t.Fatal("expected rollback mode canceled")
	}
	if cmd != nil {
		t.Fatal("expected no clear-screen command when canceling rollback selection outside alt-screen")
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestApprovalAskTabAllowsWithCommentary(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5", ContextWindowTokens: 400_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Approve?", Suggestions: []string{"Allow once", "Allow for this session", "Deny"}, Approval: true}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)
	if !updated.askFreeform {
		t.Fatal("expected tab to switch approval prompt to allow-commentary freeform")
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ok but please keep it minimal")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	resp := <-reply
	if resp.answer != approvalAllowWithCommentaryAnswerPrefix+"ok but please keep it minimal" {
		t.Fatalf("unexpected approval allow-with-commentary answer: %q", resp.answer)
	}
	if len(updated.pendingInjected) != 1 || updated.pendingInjected[0] != "ok but please keep it minimal" {
		t.Fatalf("expected queued user commentary injection, got %+v", updated.pendingInjected)
	}
	if updated.activeAsk != nil {
		t.Fatal("expected ask to resolve after allow-commentary submit")
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

func TestMainInputCtrlUDeletesCurrentLine(t *testing.T) {
	if goruntime.GOOS != "darwin" {
		t.Skip("ctrl+u alias for cmd+backspace is darwin-only")
	}
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "top\ncurrent\nbottom"
	m.inputCursor = 8 // inside "current"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	updated := next.(*uiModel)

	if updated.input != "top\nbottom" {
		t.Fatalf("expected ctrl+u alias to remove current line, got %q", updated.input)
	}
	if updated.inputCursor != 4 {
		t.Fatalf("expected cursor at start of joined line after delete, got %d", updated.inputCursor)
	}
}

func TestDebugKeysTransientStatusShowsNormalizationSource(t *testing.T) {
	t.Setenv("BUILDER_DEBUG_KEYS", "1")
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	next, _ := m.Update(testUnknownCSISequence{rendered: "?CSI[49 50 55 59 53 58 51 117]?"}) // 127;5:3u
	updated := next.(*uiModel)

	status := strings.TrimSpace(updated.transientStatus)
	if status == "" {
		t.Fatal("expected debug key status to be set")
	}
	if !strings.Contains(status, "src=unknown_csi") {
		t.Fatalf("expected unknown CSI source in debug status, got %q", status)
	}
	if !strings.Contains(status, "type=-1026") {
		t.Fatalf("expected normalized ctrl+backspace key type in debug status, got %q", status)
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

func TestBusyEnterWithUserShellPrefixQueuesInsteadOfInjecting(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "$ pwd"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if updated.inputSubmitLocked {
		t.Fatal("did not expect submit lock for queued user shell command")
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("did not expect pending injected messages, got %d", len(updated.pendingInjected))
	}
	if len(updated.queued) != 1 || updated.queued[0] != "$ pwd" {
		t.Fatalf("expected queued raw user shell input, got %+v", updated.queued)
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after queueing user shell command, got %q", updated.input)
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

func TestBusyTabQueuesPostTurnSubmissionAndKeepsInputUnlocked(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "queue this"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated := next.(*uiModel)
	if len(updated.queued) != 1 {
		t.Fatalf("expected one queued post-turn message, got %d", len(updated.queued))
	}
	if updated.queued[0] != "queue this" {
		t.Fatalf("unexpected queued message: %q", updated.queued[0])
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("did not expect injected steering message, got %d", len(updated.pendingInjected))
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after tab while busy, got %q", updated.input)
	}
	if updated.inputSubmitLocked {
		t.Fatal("did not expect submit lock for tab queue")
	}
}

func TestCtrlCWhileBusyRestoresQueuedMessagesIntoInput(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.queued = []string{"first queued", "second queued", "third queued"}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated := next.(*uiModel)

	if updated.busy {
		t.Fatal("expected busy=false after ctrl+c interrupt")
	}
	if updated.activity != uiActivityInterrupted {
		t.Fatalf("expected interrupted activity, got %v", updated.activity)
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected queued list to be restored into input and cleared, got %d", len(updated.queued))
	}
	if updated.input != "first queued\n\nsecond queued\n\nthird queued" {
		t.Fatalf("unexpected restored input text: %q", updated.input)
	}
	if updated.inputCursor != -1 {
		t.Fatalf("expected cursor moved to tail after restore, got %d", updated.inputCursor)
	}
}

func TestCtrlCWhileBusyUnlocksSubmitLockedInput(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.inputSubmitLocked = true
	m.lockedInjectText = "keep this message"
	m.pendingInjected = []string{"keep this message", "another"}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated := next.(*uiModel)

	if updated.inputSubmitLocked {
		t.Fatal("expected ctrl+c to unlock input")
	}
	if updated.lockedInjectText != "" {
		t.Fatalf("expected lockedInjectText cleared, got %q", updated.lockedInjectText)
	}
	if len(updated.pendingInjected) != 1 || updated.pendingInjected[0] != "another" {
		t.Fatalf("expected locked pending injection removed, got %+v", updated.pendingInjected)
	}
}

func TestInterruptedSubmitDoneRestoresQueueIntoInputAndDoesNotAutoDrain(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.queued = []string{"first", "second"}

	next, cmd := m.Update(submitDoneMsg{err: errSubmissionInterrupted})
	updated := next.(*uiModel)

	if cmd != nil {
		t.Fatal("did not expect follow-up submission command after interruption")
	}
	if updated.busy {
		t.Fatal("expected busy=false after interrupted submit completion")
	}
	if updated.activity != uiActivityInterrupted {
		t.Fatalf("expected interrupted activity, got %v", updated.activity)
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected queue restored into input and cleared, got %d", len(updated.queued))
	}
	if updated.input != "first\n\nsecond" {
		t.Fatalf("unexpected restored input text: %q", updated.input)
	}
	plain := stripANSIAndTrimRight(updated.View())
	if strings.Contains(strings.ToLower(plain), "interrupted") {
		t.Fatalf("did not expect interruption to be rendered as error transcript, got %q", plain)
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

func TestCalcChatLinesUsesFullHeightInDetailMode(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 20
	m.termHeight = 12
	m.input = strings.Repeat("x", 80)
	m.queued = []string{"one", "two", "three", "four", "five", "six"}
	m.refreshSlashCommandFilterFromInput()

	base := m.calcChatLines()
	if base >= m.termHeight-1 {
		t.Fatalf("expected ongoing chat lines to reserve non-chat panes, got %d", base)
	}

	m.forwardToView(tui.ToggleModeMsg{})
	detail := m.calcChatLines()
	if detail != m.termHeight-1 {
		t.Fatalf("expected detail chat lines to use full height minus status line: got %d want %d", detail, m.termHeight-1)
	}
}

func TestCalcChatLinesRemainsViewportBasedDuringActiveWork(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleUser, Content: "hello"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	eng, err := runtime.New(store, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 100
	m.termHeight = 24
	m.busy = true
	m.sawAssistantDelta = true

	if got := m.calcChatLines(); got <= 1 {
		t.Fatalf("expected viewport-based ongoing mode to keep multi-line chat area during active work, got %d", got)
	}
}

func TestViewDuringActiveWorkKeepsCommittedTranscriptVisible(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleUser, Content: "prior user"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleAssistant, Content: "prior assistant"}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	eng, err := runtime.New(store, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 100
	m.termHeight = 24
	m.busy = true
	m.sawAssistantDelta = true
	m.forwardToView(tui.SetConversationMsg{
		Entries: []tui.TranscriptEntry{
			{Role: "user", Text: "prior user"},
			{Role: "assistant", Text: "prior assistant"},
		},
		Ongoing: "streaming now",
	})

	view := stripANSIAndTrimRight(m.View())
	if !strings.Contains(view, "prior assistant") || !strings.Contains(view, "prior user") {
		t.Fatalf("expected ongoing render to keep committed transcript visible, got %q", view)
	}
	if !strings.Contains(view, "streaming now") {
		t.Fatalf("expected ongoing render to include live streaming content, got %q", view)
	}
}

func TestRenderQueuedMessagesPaneShowsNewestFiveAndOverflowLine(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.queued = []string{"one", "two", "three", "four", "five", "six", "seven"}

	lines := m.renderQueuedMessagesPane(40)
	if len(lines) != 6 {
		t.Fatalf("expected 6 queued pane lines, got %d", len(lines))
	}
	plain := strings.Split(stripANSIAndTrimRight(strings.Join(lines, "\n")), "\n")
	want := []string{"2 more messages", "three", "four", "five", "six", "seven"}
	if len(plain) != len(want) {
		t.Fatalf("expected %d plain lines, got %d", len(want), len(plain))
	}
	for i := range want {
		if plain[i] != want[i] {
			t.Fatalf("line %d = %q want %q", i, plain[i], want[i])
		}
	}
}

func TestRenderQueuedMessagesPaneTruncatesToOneLineWithEllipsis(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.queued = []string{"short\nsecond line", "abcdefghijklmnopqrstuvwxyz"}

	lines := m.renderQueuedMessagesPane(10)
	plain := strings.Split(stripANSIAndTrimRight(strings.Join(lines, "\n")), "\n")
	want := []string{"short…", "abcdefghi…"}
	if len(plain) != len(want) {
		t.Fatalf("expected %d plain lines, got %d", len(want), len(plain))
	}
	for i := range want {
		if plain[i] != want[i] {
			t.Fatalf("line %d = %q want %q", i, plain[i], want[i])
		}
	}
}

func TestViewPlacesQueuedPaneBetweenSlashPickerAndInput(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 18
	m.input = "/"
	m.refreshSlashCommandFilterFromInput()
	m.queued = []string{"queued latest"}

	view := stripANSIAndTrimRight(m.View())
	if !containsInOrder(view, "/new", "queued latest", "› /") {
		t.Fatalf("expected slash picker above queued pane above input, got %q", view)
	}
}

func TestCalcChatLinesShrinksForQueuedPane(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 40
	m.termHeight = 20
	m.input = "ok"

	base := m.calcChatLines()
	m.queued = []string{"a", "b", "c"}
	withThree := m.calcChatLines()
	if withThree != base-3 {
		t.Fatalf("expected chat lines to shrink by 3, base=%d withThree=%d", base, withThree)
	}
	m.queued = []string{"1", "2", "3", "4", "5", "6"}
	withOverflowLine := m.calcChatLines()
	if withOverflowLine != base-6 {
		t.Fatalf("expected chat lines to shrink by 6 with overflow line, base=%d withOverflowLine=%d", base, withOverflowLine)
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

func TestRenderChatPanelKeepsNewestLinesWhenContentOverflows(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	style := uiThemeStyles("dark")
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 6, Width: 80})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "a4"})

	lines := m.renderChatPanel(40, 2, style)
	plain := stripANSIAndTrimRight(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "a4") {
		t.Fatalf("expected clipped chat panel to include latest content, got %q", plain)
	}
	if strings.Contains(plain, "a1") {
		t.Fatalf("expected oldest clipped content to be dropped, got %q", plain)
	}
}

func TestSlashCommandPickerRendersSevenLines(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/"

	lines := m.renderSlashCommandPicker(80)
	if len(lines) != slashCommandPickerLines {
		t.Fatalf("expected %d picker lines, got %d", slashCommandPickerLines, len(lines))
	}
	plain := stripANSIAndTrimRight(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "/new - Create a new session") {
		t.Fatalf("expected /new picker entry, got %q", plain)
	}
}

func TestSlashCommandPickerHidesInArgumentMode(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("new")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeySpace})
	updated = next.(*uiModel)

	lines := updated.renderSlashCommandPicker(80)
	if len(lines) != 0 {
		t.Fatalf("expected hidden picker in argument mode, got %d lines", len(lines))
	}
}

func TestSlashCommandArrowKeysNavigatePickerAndReplaceInput(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	updated := next.(*uiModel)

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.input != "/exit" {
		t.Fatalf("expected first down to select /exit, got %q", updated.input)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.input != "/init" {
		t.Fatalf("expected second down to select /init, got %q", updated.input)
	}
}

func TestSlashCommandArrowKeysDoNotOverrideArgumentMode(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/new arg"
	m.inputCursor = -1

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := next.(*uiModel)
	if updated.input != "/new arg" {
		t.Fatalf("expected argument input unchanged, got %q", updated.input)
	}
	if updated.inputCursor != 0 {
		t.Fatalf("expected regular cursor navigation, got %d", updated.inputCursor)
	}
}

func TestUnknownSlashCommandIsSubmittedAsPrompt(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/nope"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if !updated.busy {
		t.Fatal("expected submission to start for unknown slash command")
	}
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "/nope") {
		t.Fatalf("expected unknown slash command in user transcript, got %q", plain)
	}
}

func TestFileSlashCommandSubmitsInjectedUserPrompt(t *testing.T) {
	r := commands.NewRegistry()
	r.Register("prompt:review", "", func(string) commands.Result {
		return commands.Result{Handled: true, SubmitUser: true, User: "# review\nexact body\n"}
	})
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUICommandRegistry(r),
	).(*uiModel)
	m.input = "/prompt:review"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if !updated.busy {
		t.Fatal("expected submission to start for file slash command")
	}
	plain := stripANSIAndTrimRight(updated.View())
	if strings.Contains(plain, "/prompt:review") {
		t.Fatalf("expected command text to be replaced by file prompt content, got %q", plain)
	}
	if !strings.Contains(plain, "review") || !strings.Contains(plain, "exact body") {
		t.Fatalf("expected file prompt content in transcript, got %q", plain)
	}
}

func TestBuiltInReviewSlashCommandSubmitsInjectedUserPrompt(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/review internal/app"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected quit cmd for /review fresh-conversation handoff")
	}
	if updated.Action() != UIActionNewSession {
		t.Fatalf("expected UIActionNewSession, got %q", updated.Action())
	}
	if strings.TrimSpace(updated.nextSessionInitialPrompt) == "" {
		t.Fatal("expected next-session prompt payload for /review")
	}
	if !strings.Contains(updated.nextSessionInitialPrompt, "Review guidelines:") ||
		!strings.Contains(updated.nextSessionInitialPrompt, "internal/app") {
		t.Fatalf("expected review prompt content and args in handoff payload, got %q", updated.nextSessionInitialPrompt)
	}
	plain := stripANSIAndTrimRight(updated.View())
	if strings.Contains(plain, "/review internal/app") {
		t.Fatalf("expected command text to be consumed by fresh-session handoff, got %q", plain)
	}
}

func TestBuiltInInitSlashCommandSubmitsInjectedUserPrompt(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/init starter repo"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected quit cmd for /init fresh-conversation handoff")
	}
	if updated.Action() != UIActionNewSession {
		t.Fatalf("expected UIActionNewSession, got %q", updated.Action())
	}
	if strings.TrimSpace(updated.nextSessionInitialPrompt) == "" {
		t.Fatal("expected next-session prompt payload for /init")
	}
	if !strings.Contains(updated.nextSessionInitialPrompt, "starter repo") {
		t.Fatalf("expected init args in handoff payload, got %q", updated.nextSessionInitialPrompt)
	}
	plain := stripANSIAndTrimRight(updated.View())
	if strings.Contains(plain, "/init starter repo") {
		t.Fatalf("expected command text to be consumed by fresh-session handoff, got %q", plain)
	}
}

func TestBusySlashNameExecutesImmediatelyWithoutQueueing(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.input = "/name incident triage"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected window title update cmd from /name")
	}
	if !updated.busy {
		t.Fatal("expected busy state unchanged while command executes")
	}
	if updated.sessionName != "incident triage" {
		t.Fatalf("expected session name update, got %q", updated.sessionName)
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected no queued messages, got %d", len(updated.queued))
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected no pending injected messages, got %d", len(updated.pendingInjected))
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after /name, got %q", updated.input)
	}
}

func TestBusySlashThinkingExecutesImmediatelyWithoutQueueing(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.thinkingLevel = "high"
	m.input = "/thinking low"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd != nil {
		t.Fatal("did not expect extra command from /thinking")
	}
	if !updated.busy {
		t.Fatal("expected busy state unchanged while command executes")
	}
	if updated.thinkingLevel != "low" {
		t.Fatalf("expected thinking level update, got %q", updated.thinkingLevel)
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected no queued messages, got %d", len(updated.queued))
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected no pending injected messages, got %d", len(updated.pendingInjected))
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after /thinking, got %q", updated.input)
	}
}

func TestSlashSupervisorTogglesReviewerInvocationAndShowsStatus(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/supervisor"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status clear timer cmd")
	}
	if !updated.reviewerEnabled {
		t.Fatal("expected reviewer invocation enabled after toggle")
	}
	if updated.reviewerMode != "edits" {
		t.Fatalf("expected reviewer mode edits after toggle, got %q", updated.reviewerMode)
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after /supervisor, got %q", updated.input)
	}
	if !strings.Contains(updated.transientStatus, "Supervisor invocation enabled") {
		t.Fatalf("expected transient status for /supervisor toggle, got %q", updated.transientStatus)
	}
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "Supervisor invocation enabled") {
		t.Fatalf("expected transcript notice for /supervisor toggle, got %q", plain)
	}

	updated.input = "/supervisor off"
	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status clear timer cmd")
	}
	if updated.reviewerEnabled {
		t.Fatal("expected reviewer invocation disabled")
	}
	if updated.reviewerMode != "off" {
		t.Fatalf("expected reviewer mode off after disable, got %q", updated.reviewerMode)
	}
	if !strings.Contains(updated.transientStatus, "Supervisor invocation disabled") {
		t.Fatalf("expected disable transient status, got %q", updated.transientStatus)
	}
}

func TestBusySlashSupervisorExecutesImmediatelyWithoutQueueing(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.input = "/supervisor on"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status clear timer cmd")
	}
	if !updated.busy {
		t.Fatal("expected busy state unchanged while command executes")
	}
	if !updated.reviewerEnabled || updated.reviewerMode != "edits" {
		t.Fatalf("expected reviewer enabled in edits mode, got enabled=%v mode=%q", updated.reviewerEnabled, updated.reviewerMode)
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected no queued messages, got %d", len(updated.queued))
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected no pending injected messages, got %d", len(updated.pendingInjected))
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after /supervisor, got %q", updated.input)
	}
}

func TestSlashSupervisorWithEngineTogglesRuntimeReviewer(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{
		Model: "gpt-5",
		Reviewer: runtime.ReviewerConfig{
			Frequency:      "off",
			Model:          "gpt-5",
			ThinkingLevel:  "low",
			MaxSuggestions: 5,
			Client:         statusLineFakeClient{},
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/supervisor on"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status clear timer cmd")
	}
	if got := eng.ReviewerFrequency(); got != "edits" {
		t.Fatalf("expected runtime reviewer mode edits, got %q", got)
	}
	if !updated.reviewerEnabled || updated.reviewerMode != "edits" {
		t.Fatalf("expected ui reviewer enabled in edits mode, got enabled=%v mode=%q", updated.reviewerEnabled, updated.reviewerMode)
	}
	if !strings.Contains(updated.transientStatus, "Supervisor invocation enabled") {
		t.Fatalf("expected enable status message, got %q", updated.transientStatus)
	}

	updated.input = "/supervisor off"
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if got := eng.ReviewerFrequency(); got != "off" {
		t.Fatalf("expected runtime reviewer mode off, got %q", got)
	}
	if updated.reviewerEnabled || updated.reviewerMode != "off" {
		t.Fatalf("expected ui reviewer disabled in off mode, got enabled=%v mode=%q", updated.reviewerEnabled, updated.reviewerMode)
	}
}

func TestBusyUnsupportedSlashCommandShowsTransientErrorAndDoesNotQueue(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.input = "/compact keep details"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status clear timer cmd")
	}
	if updated.transientStatus == "" {
		t.Fatal("expected transient status message for unsupported busy command")
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected no queued messages, got %d", len(updated.queued))
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected no pending injected messages, got %d", len(updated.pendingInjected))
	}
	if updated.inputSubmitLocked {
		t.Fatal("did not expect input submit lock for blocked slash command")
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared for blocked slash command, got %q", updated.input)
	}
	status := stripANSIAndTrimRight(updated.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(status, "cannot run /compact while model is working") {
		t.Fatalf("expected transient status in status line, got %q", status)
	}

	next, _ = updated.Update(clearTransientStatusMsg{token: updated.transientStatusToken})
	cleared := next.(*uiModel)
	if cleared.transientStatus != "" {
		t.Fatalf("expected transient status to clear, got %q", cleared.transientStatus)
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

func TestSlashCommandSetsResumeAction(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/resume"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected quit cmd for /resume")
	}
	updated := next.(*uiModel)
	if updated.Action() != UIActionResume {
		t.Fatalf("expected UIActionResume, got %q", updated.Action())
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

func TestInitAutoSubmitsStartupPrompt(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIStartupSubmit("run review"),
	).(*uiModel)
	m.termWidth = 80
	m.termHeight = 20

	_ = m.Init()

	if !m.busy {
		t.Fatal("expected startup prompt to start submission immediately")
	}
	plain := stripANSIAndTrimRight(m.View())
	if !strings.Contains(plain, "run review") {
		t.Fatalf("expected startup prompt in transcript, got %q", plain)
	}
}

func TestReviewerStatusEndToEnd_OngoingShortDetailFull(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.AppendLocalEntry("reviewer_status", "Supervisor ran: 2 suggestions, no changes applied.\n\nSupervisor suggestions:\n1. First detailed suggestion text\n2. Second detailed suggestion text")

	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 100
	m.termHeight = 24

	ongoing := stripANSIAndTrimRight(m.View())
	if !strings.Contains(ongoing, "Supervisor ran: 2 suggestions, no changes applied.") {
		t.Fatalf("expected short reviewer status in ongoing mode, got %q", ongoing)
	}
	if strings.Contains(ongoing, "Supervisor suggestions:") || strings.Contains(ongoing, "First detailed suggestion") {
		t.Fatalf("expected full reviewer suggestions hidden in ongoing mode, got %q", ongoing)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	detail := stripANSIAndTrimRight(next.(*uiModel).View())
	if !containsInOrder(detail, "Supervisor ran: 2 suggestions, no changes applied.", "Supervisor suggestions:", "1. First detailed suggestion text", "2. Second detailed suggestion text") {
		t.Fatalf("expected full reviewer suggestions in detail mode, got %q", detail)
	}
}

func TestStatusLineShowsContextUsageWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5", ContextWindowTokens: 400_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	line := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(line, "cache --") {
		t.Fatalf("expected cache placeholder in status line, got %q", line)
	}
	if !strings.Contains(line, "0%") {
		t.Fatalf("expected context usage label in status line, got %q", line)
	}
	if !strings.Contains(line, "▯▯▯▯▯▯▯▯▯▯") {
		t.Fatalf("expected progress bar in status line, got %q", line)
	}
}

func TestStatusLineShowsThinkingLevelForReasoningModels(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIModelName("gpt-5.3.codex"),
		WithUIThinkingLevel("high"),
	).(*uiModel)

	line := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(line, "gpt-5.3.codex high") {
		t.Fatalf("expected status line to include model and thinking level, got %q", line)
	}
}

func TestStatusLineOmitsThinkingLevelForNonReasoningModels(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIModelName("claude-3-7-sonnet"),
		WithUIThinkingLevel("high"),
	).(*uiModel)

	line := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if strings.Contains(line, "claude-3-7-sonnet high") {
		t.Fatalf("did not expect status line to include thinking level for non-reasoning model, got %q", line)
	}
	if !strings.Contains(line, "claude-3-7-sonnet") {
		t.Fatalf("expected status line to include model name, got %q", line)
	}
}

func TestStatusLineShowsLockedModelContractMarker(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIModelName("gpt-5.3.codex"),
		WithUIThinkingLevel("high"),
		WithUIModelContractLocked(true),
	).(*uiModel)

	line := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(line, "gpt-5.3.codex high (model locked)") {
		t.Fatalf("expected status line to include locked model contract marker, got %q", line)
	}
}

func TestStatusLineShowsCompactionProgressWarning(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	next, _ := m.Update(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventCompactionStarted}})
	started := next.(*uiModel)
	line := stripANSIAndTrimRight(started.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(strings.ToLower(line), "compacting") {
		t.Fatalf("expected compaction warning in status line, got %q", line)
	}

	next, _ = started.Update(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventCompactionCompleted}})
	completed := next.(*uiModel)
	line = stripANSIAndTrimRight(completed.renderStatusLine(120, uiThemeStyles("dark")))
	if strings.Contains(strings.ToLower(line), "compacting") {
		t.Fatalf("expected compaction warning cleared after completion, got %q", line)
	}
}

func TestStatusLineShowsReviewerProgressWarning(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	next, _ := m.Update(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventReviewerStarted}})
	started := next.(*uiModel)
	line := stripANSIAndTrimRight(started.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(strings.ToLower(line), "review") {
		t.Fatalf("expected reviewer warning in status line, got %q", line)
	}

	next, _ = started.Update(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventReviewerCompleted}})
	completed := next.(*uiModel)
	line = stripANSIAndTrimRight(completed.renderStatusLine(120, uiThemeStyles("dark")))
	if strings.Contains(strings.ToLower(line), "reviewing") || strings.Contains(strings.ToLower(line), "review in progress") {
		t.Fatalf("expected reviewer warning cleared after completion, got %q", line)
	}
}

func TestReviewerProgressKeepsInputEditable(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.input = "keep this draft"

	next, _ := m.Update(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventReviewerStarted}})
	started := next.(*uiModel)
	if !started.reviewerBlocking {
		t.Fatal("expected reviewer state to be marked running")
	}
	lines := started.renderInputLines(80, uiThemeStyles("dark"))
	plain := stripANSIAndTrimRight(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "keep this draft") {
		t.Fatalf("expected original draft visible while reviewer runs, got %q", plain)
	}

	next, _ = started.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	locked := next.(*uiModel)
	if locked.input != "keep this draftx" {
		t.Fatalf("expected key input accepted while reviewer runs, got %q", locked.input)
	}

	next, _ = locked.Update(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventReviewerCompleted}})
	completed := next.(*uiModel)
	if completed.reviewerBlocking {
		t.Fatal("expected reviewer state cleared after completion")
	}
	lines = completed.renderInputLines(80, uiThemeStyles("dark"))
	plain = stripANSIAndTrimRight(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "keep this draftx") {
		t.Fatalf("expected edited draft retained after reviewer completion, got %q", plain)
	}
}

func TestBusyEnterDuringReviewerUsesSteeringInjection(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.input = "steer after review"

	next, _ := m.Update(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventReviewerStarted}})
	started := next.(*uiModel)
	if !started.reviewerRunning {
		t.Fatal("expected reviewer to be running")
	}
	if started.isInputLocked() {
		t.Fatal("did not expect input lock while reviewer is running")
	}

	next, _ = started.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if len(updated.queued) != 0 {
		t.Fatalf("did not expect post-turn queue for reviewer steering, got %+v", updated.queued)
	}
	if len(updated.pendingInjected) != 1 || updated.pendingInjected[0] != "steer after review" {
		t.Fatalf("expected reviewer steering injected for earliest flush, got %+v", updated.pendingInjected)
	}
	if !updated.inputSubmitLocked {
		t.Fatal("expected submit lock while waiting for reviewer steering flush")
	}
	if updated.input != "steer after review" {
		t.Fatalf("expected input preserved while waiting for reviewer steering flush, got %q", updated.input)
	}
}

func TestMouseSGRReportRunesDoNotPolluteInput(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "draft"
	m.inputCursor = len([]rune(m.input))

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<64;74;25M")})
	updated := next.(*uiModel)
	if updated.input != "draft" {
		t.Fatalf("expected mouse sgr sequence ignored, got %q", updated.input)
	}

	longBurst := "[<64;81;40M[<64;81;40M[<64;80;40M[<64;80;40M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M[<65;80;39M"
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(longBurst)})
	updated = next.(*uiModel)
	if updated.input != "draft" {
		t.Fatalf("expected long mouse sgr burst ignored, got %q", updated.input)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	updated = next.(*uiModel)
	if updated.input != "draftx" {
		t.Fatalf("expected normal runes to still insert, got %q", updated.input)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<64;63;24M")})
	updated = next.(*uiModel)
	if updated.input != "draftx" {
		t.Fatalf("expected up-scroll mouse sgr ignored, got %q", updated.input)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<65;69;20M")})
	updated = next.(*uiModel)
	if updated.input != "draftx" {
		t.Fatalf("expected down-scroll mouse sgr ignored, got %q", updated.input)
	}
}

func TestMouseSGRSplitEscAndRunesDoNotArmRollback(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := next.(*uiModel)
	if updated.lastEscAt.IsZero() {
		t.Fatal("expected esc to arm rollback window before potential sgr continuation")
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<64;63;24M")})
	updated = next.(*uiModel)
	if !updated.lastEscAt.IsZero() {
		t.Fatal("expected split mouse sgr continuation to clear rollback esc arming")
	}
	if updated.input != "" {
		t.Fatalf("expected split sgr payload ignored, got %q", updated.input)
	}
}

func TestStatusContextZoneColorBoundaries(t *testing.T) {
	assertLightColor := func(percent int, want string) {
		t.Helper()
		adaptive, ok := statusContextZoneColor(percent).(lipgloss.CompleteAdaptiveColor)
		if !ok {
			t.Fatalf("unexpected color type for percent=%d", percent)
		}
		if adaptive.Light.TrueColor != want {
			t.Fatalf("percent=%d color=%s want=%s", percent, adaptive.Light.TrueColor, want)
		}
	}
	assertLightColor(49, "#22863A")
	assertLightColor(50, "#9A6700")
	assertLightColor(79, "#9A6700")
	assertLightColor(80, "#CB2431")
}

type statusLineFakeClient struct{}

func (statusLineFakeClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
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
