package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"builder/internal/app/commands"
	"builder/internal/config"
	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/session"
	"builder/internal/tools"
	"builder/internal/tools/askquestion"
	shelltool "builder/internal/tools/shell"
	"builder/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func requestMessages(req llm.Request) []llm.Message {
	return llm.MessagesFromItems(req.Items)
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

func TestEmptyEnterFlushesOnlyNextQueuedItem(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.queued = []string{"/name queued title", "follow up"}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)

	if cmd == nil {
		t.Fatal("expected command from queued /name flush")
	}
	if updated.sessionName != "queued title" {
		t.Fatalf("expected only first queued item to execute, got session name %q", updated.sessionName)
	}
	if updated.busy {
		t.Fatal("did not expect follow-up prompt submission from empty-enter flush")
	}
	if len(updated.queued) != 1 || updated.queued[0] != "follow up" {
		t.Fatalf("expected follow-up prompt to remain queued, got %+v", updated.queued)
	}
}

func TestIdleTabWithExistingQueueFlushesOnlyNextQueuedItem(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.queued = []string{"/name queued title"}
	m.input = "follow up"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated := next.(*uiModel)

	if cmd == nil {
		t.Fatal("expected command from queued /name flush")
	}
	if updated.sessionName != "queued title" {
		t.Fatalf("expected queued /name to execute first, got %q", updated.sessionName)
	}
	if updated.busy {
		t.Fatal("did not expect appended prompt to auto-submit while idle tab is flushing one queued item")
	}
	if len(updated.queued) != 1 || updated.queued[0] != "follow up" {
		t.Fatalf("expected appended prompt to remain queued, got %+v", updated.queued)
	}
}

func TestCustomKeyCtrlEnterQueuesAndStartsSubmission(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "echo hi"

	next, _ := m.Update(customKeyMsg{Kind: customKeyCtrlEnter})
	updated := next.(*uiModel)

	if !updated.busy {
		t.Fatal("expected busy after ctrl+enter custom key")
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after ctrl+enter custom key, got %q", updated.input)
	}
}

func TestCustomKeyCtrlEnterXtermVariantQueuesAndStartsSubmission(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "echo hi"

	next, _ := m.Update(customKeyMsg{Kind: customKeyCtrlEnter})
	updated := next.(*uiModel)

	if !updated.busy {
		t.Fatal("expected busy after xterm ctrl+enter sequence")
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after xterm ctrl+enter sequence, got %q", updated.input)
	}
}

func TestCustomKeyCtrlEnterQueuesPostTurnWhenBusy(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "echo hi"

	next, _ := m.Update(customKeyMsg{Kind: customKeyCtrlEnter})
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

func TestCustomKeyShiftEnterInsertsNewline(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "hello"

	next, _ := m.Update(customKeyMsg{Kind: customKeyShiftEnter})
	updated := next.(*uiModel)

	if updated.busy {
		t.Fatal("did not expect busy after shift+enter CSI sequence")
	}
	if updated.input != "hello\n" {
		t.Fatalf("expected newline insertion from shift+enter CSI sequence, got %q", updated.input)
	}
}

func TestCustomKeyShiftEnterThenEnterDoesNotSubmitTrailingNewline(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "hello"

	next, _ := m.Update(customKeyMsg{Kind: customKeyShiftEnter})
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

func TestCustomKeyCtrlBackspaceDeletesCurrentLine(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "one\ntwo\nthree"
	m.inputCursor = 5 // inside "two"

	next, _ := m.Update(customKeyMsg{Kind: customKeyCtrlBackspace})
	updated := next.(*uiModel)

	if updated.input != "one\nthree" {
		t.Fatalf("expected ctrl+backspace CSI to remove current line, got %q", updated.input)
	}
	if updated.inputCursor != 4 {
		t.Fatalf("expected cursor at start of joined line after delete, got %d", updated.inputCursor)
	}
}

func TestCustomKeyCtrlBackspaceWithSubtypeDeletesCurrentLine(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "one\ntwo\nthree"
	m.inputCursor = 5 // inside "two"

	next, _ := m.Update(customKeyMsg{Kind: customKeyCtrlBackspace})
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
	if testAskFreeform(updated) {
		t.Fatal("expected picker mode first")
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)
	if !testAskFreeform(updated) {
		t.Fatal("expected tab to open freeform commentary")
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<64;55;24M[<64;56;26M[<65;56;26M")})
	updated = next.(*uiModel)
	if testAskInput(updated) != "" {
		t.Fatalf("expected mouse sgr sequence ignored in ask freeform input, got %q", testAskInput(updated))
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("custom")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	resp := <-reply
	if resp.response.Answer != "custom" {
		t.Fatalf("unexpected answer: %q", resp.response.Answer)
	}
	if resp.response.FreeformAnswer != "custom" {
		t.Fatalf("unexpected freeform answer: %q", resp.response.FreeformAnswer)
	}
	if resp.response.SelectedOptionNumber != 1 {
		t.Fatalf("expected selected option 1 preserved when switching to freeform, got %+v", resp.response)
	}
	if testActiveAsk(updated) != nil {
		t.Fatal("ask should be resolved")
	}
}

func TestAskQuestionPickerSubmitPreservesPendingFreeformDraft(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Pick one", Suggestions: []string{"a", "b"}}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("custom")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)

	if testAskFreeform(updated) {
		t.Fatal("expected tab to return to picker mode")
	}
	if testAskInput(updated) != "custom" {
		t.Fatalf("expected pending freeform draft preserved, got %q", testAskInput(updated))
	}
	promptLines := updated.renderAskPromptLines()
	hasDisabledDraftPreview := false
	hasHintLine := false
	for _, line := range promptLines {
		if line.Kind == askPromptLineKindInput && line.Disabled && line.InputText == "custom" {
			hasDisabledDraftPreview = true
		}
		if line.Kind == askPromptLineKindHint {
			hasHintLine = true
		}
	}
	if !hasDisabledDraftPreview {
		t.Fatalf("expected disabled draft preview in picker, got %+v", promptLines)
	}
	if hasHintLine {
		t.Fatalf("expected draft preview to replace picker hint, got %+v", promptLines)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	resp := <-reply
	if resp.response.SelectedOptionNumber != 2 {
		t.Fatalf("expected selected option number 2, got %+v", resp.response)
	}
	if resp.response.FreeformAnswer != "custom" {
		t.Fatalf("expected pending freeform draft submitted with picker answer, got %+v", resp.response)
	}
	if testActiveAsk(updated) != nil {
		t.Fatal("ask should be resolved")
	}
}

func TestAskQuestionTabRoundTripRestoresPendingFreeformDraftAndCursor(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Pick one", Suggestions: []string{"a", "b"}}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("custom")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyLeft})
	updated = next.(*uiModel)
	wantCursor := testAskInputCursor(updated)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)

	if !testAskFreeform(updated) {
		t.Fatal("expected tab to restore freeform editing")
	}
	if testAskCursor(updated) != 1 {
		t.Fatalf("expected changed picker selection preserved, got %d", testAskCursor(updated))
	}
	if testAskInput(updated) != "custom" {
		t.Fatalf("expected pending freeform draft restored, got %q", testAskInput(updated))
	}
	if testAskInputCursor(updated) != wantCursor {
		t.Fatalf("expected freeform cursor restored, got %d want %d", testAskInputCursor(updated), wantCursor)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	resp := <-reply
	if resp.response.SelectedOptionNumber != 2 {
		t.Fatalf("expected selected option number 2 after round-trip, got %+v", resp.response)
	}
	if resp.response.FreeformAnswer != "custoXm" {
		t.Fatalf("expected restored draft to remain editable, got %+v", resp.response)
	}
	if testActiveAsk(updated) != nil {
		t.Fatal("ask should be resolved")
	}
}

func TestAskQuestionPickerSubmitReturnsSelectedOptionNumber(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Pick one", Suggestions: []string{"a", "b"}}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	resp := <-reply
	if resp.response.SelectedOptionNumber != 2 {
		t.Fatalf("expected selected option number 2, got %+v", resp.response)
	}
	if resp.response.Answer != "" || resp.response.FreeformAnswer != "" {
		t.Fatalf("expected structured picker response without raw answer text, got %+v", resp.response)
	}
	if testActiveAsk(updated) != nil {
		t.Fatal("ask should be resolved")
	}
}

func TestAskQuestionFreeformSelectionEnterDropsIntoFreeformWhenEmpty(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Pick one", Suggestions: []string{"a", "b"}}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	if cmd != nil {
		t.Fatal("did not expect validation error when opening freeform from Freeform answer")
	}
	if !testAskFreeform(updated) {
		t.Fatal("expected Freeform answer to switch into freeform mode")
	}
	if updated.transientStatus != "" {
		t.Fatalf("did not expect transient status while opening freeform, got %q", updated.transientStatus)
	}
	if testActiveAsk(updated) == nil {
		t.Fatal("expected ask to remain active after switching to freeform")
	}
	select {
	case resp := <-reply:
		t.Fatalf("did not expect reply while opening freeform, got %+v", resp)
	default:
	}
}

func TestAskQuestionFreeformSelectionEmptySubmitRequiresCommentary(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Pick one", Suggestions: []string{"a", "b"}}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	next, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	if cmd == nil {
		t.Fatal("expected transient error status cmd")
	}
	if strings.TrimSpace(updated.transientStatus) == "" {
		t.Fatal("expected non-empty transient validation status")
	}
	if updated.transientStatusKind != uiStatusNoticeError {
		t.Fatalf("expected error notice kind, got %d", updated.transientStatusKind)
	}
	if testActiveAsk(updated) == nil {
		t.Fatal("expected ask to remain active after validation error")
	}
	select {
	case resp := <-reply:
		t.Fatalf("did not expect reply on validation error, got %+v", resp)
	default:
	}
}

func TestAskQuestionFreeformSelectionSubmitsFreeformOnly(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Pick one", Suggestions: []string{"a", "b"}}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("custom")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	resp := <-reply
	if resp.response.SelectedOptionNumber != 0 {
		t.Fatalf("expected freeform selection to submit without selected option number, got %+v", resp.response)
	}
	if resp.response.Answer != "custom" || resp.response.FreeformAnswer != "custom" {
		t.Fatalf("unexpected freeform selection response: %+v", resp.response)
	}
	if testActiveAsk(updated) != nil {
		t.Fatal("ask should be resolved")
	}
}

func TestAskFreeformUsesMainEditingStack(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Type answer"}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	if !testAskFreeform(updated) {
		t.Fatal("expected freeform ask input")
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello world")})
	updated = next.(*uiModel)
	for range 5 {
		next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyLeft})
		updated = next.(*uiModel)
	}
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
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	resp := <-reply
	if resp.response.Answer != ">hello _worl" {
		t.Fatalf("unexpected inline edit result: %q", resp.response.Answer)
	}
	if testActiveAsk(updated) != nil {
		t.Fatal("ask should be resolved")
	}
}

func TestAskFreeformViewRendersPromptBoxAndCursor(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 40
	m.termHeight = 16
	m.windowSizeKnown = true
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Type answer"}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello world")})
	updated = next.(*uiModel)
	updated.syncViewport()

	view := updated.View()
	if !strings.Contains(view, ansiHideCursor) {
		t.Fatalf("expected terminal cursor hidden in ask view: %q", view)
	}
	plain := stripANSIAndTrimRight(view)
	if !strings.Contains(plain, "› hello world") {
		t.Fatalf("expected ask input text preserved in view, got %q", plain)
	}
}

func TestAskPromptUsesCheckmarkAndSingleLineHint(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Pick one", Suggestions: []string{"a", "b"}}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	promptLines := updated.renderAskPromptLines()
	if len(promptLines) != 5 {
		t.Fatalf("expected question, 3 options, and hint; got %+v", promptLines)
	}
	if promptLines[0].Kind != askPromptLineKindQuestion {
		t.Fatalf("expected first prompt line to be question, got %+v", promptLines)
	}
	if promptLines[1].Kind != askPromptLineKindOption || !promptLines[1].Selected {
		t.Fatalf("expected first option selected, got %+v", promptLines[1])
	}
	if promptLines[3].Kind != askPromptLineKindOption {
		t.Fatalf("expected freeform option line, got %+v", promptLines[3])
	}
	if promptLines[4].Kind != askPromptLineKindHint || strings.TrimSpace(promptLines[4].Text) == "" {
		t.Fatalf("expected non-empty hint line, got %+v", promptLines[4])
	}
}

func TestAskPromptShowsRecommendedMarkerAndMutedLabel(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)

	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Pick one", Suggestions: []string{"a", "b"}, RecommendedOptionIndex: 2}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	promptLines := updated.renderAskPromptLines()
	recommendedLine := promptLines[2]
	if !recommendedLine.Recommended || recommendedLine.Selected {
		t.Fatalf("expected second option recommended and not selected, got %+v", recommendedLine)
	}
	if strings.TrimSpace(recommendedLine.MutedSuffix) == "" {
		t.Fatalf("expected recommended line to carry muted suffix metadata, got %+v", recommendedLine)
	}
	style := uiThemeStyles("dark")
	rendered := strings.Join(updated.renderInputLines(100, style), "\n")
	body := strings.TrimSuffix(recommendedLine.Text, recommendedLine.MutedSuffix)
	greenText := lipgloss.NewStyle().Foreground(uiPalette("dark").secondary).Render(body)
	note := uiThemeStyles("dark").meta.Faint(true).Render(recommendedLine.MutedSuffix)
	if !strings.Contains(rendered, greenText) {
		t.Fatalf("expected recommended option text rendered in green, got %q", rendered)
	}
	if !strings.Contains(rendered, note) {
		t.Fatalf("expected recommended note rendered faint, got %q", rendered)
	}
}

func TestAskPromptKeepsSelectedStylingForRecommendedActiveRow(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)

	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Pick one", Suggestions: []string{"a", "b"}, RecommendedOptionIndex: 2}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	promptLines := updated.renderAskPromptLines()
	activeLine := promptLines[2]
	if !activeLine.Selected || !activeLine.Recommended {
		t.Fatalf("expected active line to remain both selected and recommended, got %+v", activeLine)
	}
	style := uiThemeStyles("dark")
	rendered := strings.Join(updated.renderInputLines(100, style), "\n")
	selectedExpected := lipgloss.NewStyle().Foreground(uiPalette("dark").primary).Bold(true).Render(padANSIRight(activeLine.Text, 100))
	recommendedOnly := lipgloss.NewStyle().Foreground(uiPalette("dark").secondary).Render("★ ")
	if !strings.Contains(rendered, selectedExpected) {
		t.Fatalf("expected active recommended row to keep selected styling, got %q", rendered)
	}
	if strings.Contains(rendered, recommendedOnly) {
		t.Fatalf("did not expect active recommended row to keep the passive recommended star, got %q", rendered)
	}
}

func TestApprovalAskUsesSingleDenyOptionAndTabCommentary(t *testing.T) {
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
	event := askEvent{req: askquestion.Request{Question: "Approve?", Approval: true, ApprovalOptions: []askquestion.ApprovalOption{{Decision: askquestion.ApprovalDecisionAllowOnce, Label: "Allow once"}, {Decision: askquestion.ApprovalDecisionAllowSession, Label: "Allow for this session"}, {Decision: askquestion.ApprovalDecisionDeny, Label: "Deny"}}}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	promptLines := updated.renderAskPromptLines()
	optionLines := 0
	hintLines := 0
	for _, line := range promptLines {
		if line.Kind == askPromptLineKindOption {
			optionLines++
		}
		if line.Kind == askPromptLineKindHint {
			hintLines++
		}
	}
	if optionLines != 3 {
		t.Fatalf("expected exactly 3 approval options, got %+v", promptLines)
	}
	if hintLines != 1 {
		t.Fatalf("expected one approval picker hint line, got %+v", promptLines)
	}

	for i := 0; i < 2; i++ {
		next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
		updated = next.(*uiModel)
	}
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)
	if !testAskFreeform(updated) {
		t.Fatal("expected tab on deny selection to switch to commentary input")
	}
	promptLines = updated.renderAskPromptLines()
	if len(promptLines) != 2 || promptLines[0].Kind != askPromptLineKindHint || promptLines[1].Kind != askPromptLineKindInput {
		t.Fatalf("expected commentary prompt to collapse to hint+input, got %+v", promptLines)
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
	if resp.response.Approval == nil {
		t.Fatal("expected typed approval response")
	}
	if resp.response.Approval.Decision != askquestion.ApprovalDecisionDeny || resp.response.Approval.Commentary != "blocked by policy" {
		t.Fatalf("unexpected approval response: %+v", resp.response.Approval)
	}
	if len(updated.pendingInjected) != 1 || updated.pendingInjected[0] != "blocked by policy" {
		t.Fatalf("expected deny commentary injected into regular user-said flow, got %+v", updated.pendingInjected)
	}
	if testActiveAsk(updated) != nil {
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

	if !testRollbackSelecting(updated) {
		t.Fatal("expected rollback selection mode after double esc")
	}
	if testRollbackSelection(updated) != 1 {
		t.Fatalf("expected last user message selected by default, got %d", testRollbackSelection(updated))
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if !testRollbackEditing(updated) {
		t.Fatal("expected rollback editing mode after enter")
	}
	if testRollbackSelecting(updated) {
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
	testSetRollbackEditing(m, 1, 2)
	m.input = "edited"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := next.(*uiModel)
	if !testRollbackEditing(updated) {
		t.Fatal("expected rollback editing to stay active while input non-empty")
	}
	if testRollbackSelecting(updated) {
		t.Fatal("did not expect rollback selection mode while input non-empty")
	}

	updated.input = ""
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !testRollbackSelecting(updated) {
		t.Fatal("expected rollback selection mode after esc on empty input")
	}
	if testRollbackSelection(updated) != 1 {
		t.Fatalf("expected rollback selection preserved, got %d", testRollbackSelection(updated))
	}
}

func TestRollbackEditingSubmitQuitsIntoForkTransition(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	testSetRollbackEditing(m, 0, 3)
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
	if updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected rollback selection in detail overlay, got mode %q", updated.view.Mode())
	}

	for i := 0; i < 8; i++ {
		next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
		updated = next.(*uiModel)
	}

	selected := testRollbackCandidates(updated)[testRollbackSelection(updated)].Text
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
	if !testRollbackSelecting(updated) {
		t.Fatal("expected rollback mode after double esc")
	}
	if updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected rollback selection in detail overlay, got mode %q", updated.view.Mode())
	}

	for i := 0; i < 6; i++ {
		next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
		updated = next.(*uiModel)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if testRollbackSelecting(updated) {
		t.Fatal("expected rollback mode to be canceled")
	}
	if updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected return to ongoing mode, got %q", updated.view.Mode())
	}
}

func TestRollbackTransitionsUseDetailOverlayInNativeMode(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "u1"}, {Role: "assistant", Text: "a1"}, {Role: "user", Text: "u2"}}),
	).(*uiModel)
	m.termWidth = 100
	m.termHeight = 10
	m.syncViewport()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := next.(*uiModel)
	next, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !testRollbackSelecting(updated) {
		t.Fatal("expected rollback mode after double esc")
	}
	if updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected rollback selection in detail overlay, got mode %q", updated.view.Mode())
	}
	if !testRollbackOwnsTranscriptMode(updated) {
		t.Fatal("expected rollback overlay to be pushed in native mode")
	}
	if cmd == nil {
		t.Fatal("expected native rollback entry to emit detail overlay transition command")
	}

	selected := testRollbackCandidates(updated)[testRollbackSelection(updated)].Text
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
	if testRollbackSelecting(updated) {
		t.Fatal("expected rollback mode canceled")
	}
	if updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected cancel to return to ongoing mode, got %q", updated.view.Mode())
	}
	if testRollbackOwnsTranscriptMode(updated) {
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
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenNever),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "u1"}, {Role: "assistant", Text: "a1"}, {Role: "user", Text: "u2"}}),
	).(*uiModel)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := next.(*uiModel)
	next, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !testRollbackSelecting(updated) {
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
	if testRollbackSelecting(updated) {
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
		selected := testRollbackCandidates(model)[testRollbackSelection(model)].Text
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
	if !testRollbackSelecting(updated) || updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected rollback selection detail overlay, mode=%q rollback=%t", updated.view.Mode(), testRollbackSelecting(updated))
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
	if !testRollbackEditing(updated) || updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected rollback editing in ongoing mode, mode=%q editing=%t", updated.view.Mode(), testRollbackEditing(updated))
	}

	updated.input = ""
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !testRollbackSelecting(updated) || updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected esc from empty edit input to return to rollback overlay, mode=%q rollback=%t", updated.view.Mode(), testRollbackSelecting(updated))
	}
	assertSelectionCentered(updated)

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if testRollbackSelecting(updated) || updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected final esc to cancel rollback overlay back to ongoing, mode=%q rollback=%t", updated.view.Mode(), testRollbackSelecting(updated))
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
	if !testRollbackSelecting(updated) || updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected native rollback selection in detail overlay, mode=%q rollback=%t", updated.view.Mode(), testRollbackSelecting(updated))
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if !testRollbackEditing(updated) || updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected rollback editing in ongoing mode, mode=%q editing=%t", updated.view.Mode(), testRollbackEditing(updated))
	}

	updated.input = ""
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !testRollbackSelecting(updated) || updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected esc from empty edit input to restore rollback selection overlay, mode=%q rollback=%t", updated.view.Mode(), testRollbackSelecting(updated))
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if testRollbackSelecting(updated) {
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
	if !testRollbackSelecting(updated) {
		t.Fatal("expected rollback mode after double esc")
	}
	if updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected rollback selection in detail overlay, got mode %q", updated.view.Mode())
	}
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if !testRollbackEditing(updated) {
		t.Fatal("expected rollback editing mode after enter")
	}

	updated.input = ""
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !testRollbackSelecting(updated) {
		t.Fatal("expected rollback selection mode after esc on empty edit input")
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if testRollbackSelecting(updated) {
		t.Fatal("expected rollback mode canceled")
	}
	if updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected return to ongoing mode, got %q", updated.view.Mode())
	}

	beforeAppend := updated.view.OngoingScroll()
	updated.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "new tail"})
	afterAppend := updated.view.OngoingScroll()
	if afterAppend < beforeAppend {
		t.Fatalf("expected append not to move ongoing scroll away from tail, got %d from %d", afterAppend, beforeAppend)
	}
}

func TestNativeRollbackEditAnchorsToSelectedConversationPoint(t *testing.T) {
	entries := make([]UITranscriptEntry, 0, 40)
	for i := 0; i < 20; i++ {
		entries = append(entries,
			UITranscriptEntry{Role: "user", Text: fmt.Sprintf("u-%02d", i)},
			UITranscriptEntry{Role: "assistant", Text: fmt.Sprintf("a-%02d", i)},
		)
	}
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript(entries),
	).(*uiModel)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 14})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}
	if !m.startRollbackSelectionMode() {
		t.Fatal("expected rollback selection mode")
	}
	if overlayCmd := m.pushRollbackOverlayIfNeeded(); overlayCmd == nil {
		t.Fatal("expected rollback overlay transition command")
	}
	m.rollback.selection = 3
	m.applyRollbackSelectionHighlight()
	target := testRollbackCandidates(m)[testRollbackSelection(m)].TranscriptIndex
	laterTail := m.transcriptEntries[len(m.transcriptEntries)-1].Text

	cmd := m.inputController().beginRollbackEditingFlowCmd()
	if cmd == nil {
		t.Fatal("expected rollback edit transition command")
	}
	if !testRollbackEditing(m) || m.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected rollback editing in ongoing mode, mode=%q editing=%t", m.view.Mode(), testRollbackEditing(m))
	}
	expected := renderNativeCommittedSnapshot(m.transcriptEntries[:target+1], m.theme, m.nativeReplayRenderWidth())
	if m.nativeRenderedSnapshot != expected {
		t.Fatalf("expected native rendered snapshot anchored through selected entry")
	}
	if !strings.Contains(m.nativeRenderedSnapshot, m.transcriptEntries[target].Text) {
		t.Fatalf("expected anchored snapshot to include selected message %q, got %q", m.transcriptEntries[target].Text, m.nativeRenderedSnapshot)
	}
	if strings.Contains(m.nativeRenderedSnapshot, laterTail) {
		t.Fatalf("expected anchored snapshot to exclude later tail %q, got %q", laterTail, m.nativeRenderedSnapshot)
	}
}

func TestNativeRollbackEditCommandSequenceClearsBeforeAnchoredReplay(t *testing.T) {
	entries := make([]UITranscriptEntry, 0, 20)
	for i := 0; i < 10; i++ {
		entries = append(entries,
			UITranscriptEntry{Role: "user", Text: fmt.Sprintf("u-%02d", i)},
			UITranscriptEntry{Role: "assistant", Text: fmt.Sprintf("a-%02d", i)},
		)
	}
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenNever),
		WithUIInitialTranscript(entries),
	).(*uiModel)
	next, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 14})
	m = next.(*uiModel)
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}
	_ = collectCmdMessages(t, startupCmd)

	next, firstEscCmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(*uiModel)
	if firstEscCmd != nil {
		_ = collectCmdMessages(t, firstEscCmd)
	}
	next, secondEscCmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(*uiModel)
	if !testRollbackSelecting(m) || m.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected rollback selection in detail mode, mode=%q rollback=%t", m.view.Mode(), testRollbackSelecting(m))
	}
	_ = collectCmdMessages(t, secondEscCmd)

	m.rollback.selection = 2
	m.applyRollbackSelectionHighlight()
	target := testRollbackCandidates(m)[testRollbackSelection(m)].TranscriptIndex
	targetText := m.transcriptEntries[target].Text
	laterTail := m.transcriptEntries[len(m.transcriptEntries)-1].Text

	cmd := m.inputController().beginRollbackEditingFlowCmd()
	if cmd == nil {
		t.Fatal("expected rollback edit command")
	}
	msgs := collectCmdMessages(t, cmd)
	clearIndex := -1
	flushIndex := -1
	flushText := ""
	for idx, msg := range msgs {
		if clearIndex < 0 && strings.Contains(fmt.Sprintf("%T", msg), "clearScreenMsg") {
			clearIndex = idx
		}
		if flush, ok := msg.(nativeHistoryFlushMsg); ok {
			flushIndex = idx
			flushText = stripANSIPreserve(flush.Text)
			break
		}
	}
	if clearIndex < 0 {
		t.Fatalf("expected rollback edit command to clear screen before replay, got messages=%v", msgs)
	}
	if flushIndex < 0 {
		t.Fatalf("expected rollback edit command to emit anchored native replay, got messages=%v", msgs)
	}
	if clearIndex > flushIndex {
		t.Fatalf("expected clear screen before native replay, got messages=%v", msgs)
	}
	if !strings.Contains(flushText, targetText) {
		t.Fatalf("expected anchored replay to include selected message %q, got %q", targetText, flushText)
	}
	if strings.Contains(flushText, laterTail) {
		t.Fatalf("expected anchored replay to exclude later tail %q, got %q", laterTail, flushText)
	}
	if !testRollbackEditing(m) || m.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected rollback editing in ongoing mode after command, mode=%q editing=%t", m.view.Mode(), testRollbackEditing(m))
	}
}

func TestRollbackTransitionsDoNotClearScreenWhenNotInAltScreen(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenAuto),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "u1"}, {Role: "assistant", Text: "a1"}, {Role: "user", Text: "u2"}}),
	).(*uiModel)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := next.(*uiModel)
	next, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !testRollbackSelecting(updated) {
		t.Fatal("expected rollback mode after double esc")
	}
	if cmd == nil {
		t.Fatal("expected overlay transition command when entering rollback selection")
	}

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if !testRollbackEditing(updated) {
		t.Fatal("expected rollback editing mode after enter")
	}
	if cmd == nil {
		t.Fatal("expected transition command when entering rollback edit outside alt-screen")
	}

	updated.input = ""
	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if !testRollbackSelecting(updated) {
		t.Fatal("expected rollback mode after esc from empty rollback edit")
	}
	if cmd == nil {
		t.Fatal("expected transition command when canceling rollback edit outside alt-screen")
	}

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if testRollbackSelecting(updated) {
		t.Fatal("expected rollback mode canceled")
	}
	if cmd == nil {
		t.Fatal("expected transition command when canceling rollback selection outside alt-screen")
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
	event := askEvent{req: askquestion.Request{Question: "Approve?", Approval: true, ApprovalOptions: []askquestion.ApprovalOption{{Decision: askquestion.ApprovalDecisionAllowOnce, Label: "Allow once"}, {Decision: askquestion.ApprovalDecisionAllowSession, Label: "Allow for this session"}, {Decision: askquestion.ApprovalDecisionDeny, Label: "Deny"}}}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)
	if !testAskFreeform(updated) {
		t.Fatal("expected tab to switch approval prompt to commentary freeform")
	}
	lines := updated.renderInputLines(120, uiThemeStyles("dark"))
	plain := stripANSIAndTrimRight(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "Commentary for Allow once:") {
		t.Fatalf("expected commentary prompt for selected approval option, got %q", plain)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ok but please keep it minimal")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	resp := <-reply
	if resp.response.Approval == nil {
		t.Fatal("expected typed approval response")
	}
	if resp.response.Approval.Decision != askquestion.ApprovalDecisionAllowOnce || resp.response.Approval.Commentary != "ok but please keep it minimal" {
		t.Fatalf("unexpected approval allow-with-commentary answer: %+v", resp.response.Approval)
	}
	if len(updated.pendingInjected) != 1 || updated.pendingInjected[0] != "ok but please keep it minimal" {
		t.Fatalf("expected queued user commentary injection, got %+v", updated.pendingInjected)
	}
	if testActiveAsk(updated) != nil {
		t.Fatal("expected ask to resolve after approval commentary submit")
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

	if testActiveAsk(updated) == nil || testActiveAsk(updated).req.Question != "First" {
		t.Fatalf("expected first ask to remain active, got %#v", testActiveAsk(updated))
	}
	if len(testAskQueue(updated)) != 1 {
		t.Fatalf("expected one queued ask, got %d", len(testAskQueue(updated)))
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	first := <-reply1
	if first.response.SelectedOptionNumber != 1 || first.response.Answer != "" || first.response.FreeformAnswer != "" {
		t.Fatalf("unexpected first answer: %+v", first.response)
	}
	if testActiveAsk(updated) == nil || testActiveAsk(updated).req.Question != "Second" {
		t.Fatalf("expected second ask to become active, got %#v", testActiveAsk(updated))
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	second := <-reply2
	if second.response.SelectedOptionNumber != 1 || second.response.Answer != "" || second.response.FreeformAnswer != "" {
		t.Fatalf("unexpected second answer: %+v", second.response)
	}
	if testActiveAsk(updated) != nil {
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

	next, _ := m.Update(customKeyMsg{Kind: customKeyCtrlBackspace})
	updated := next.(*uiModel)

	status := strings.TrimSpace(updated.transientStatus)
	if status == "" {
		t.Fatal("expected debug key status to be set")
	}
	if !strings.Contains(status, "src=custom_key") {
		t.Fatalf("expected custom key source in debug status, got %q", status)
	}
	if !strings.Contains(status, "type=-1026") {
		t.Fatalf("expected normalized ctrl+backspace key type in debug status, got %q", status)
	}
}

func TestShowErrorStatusSetsErrorNoticeKind(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	cmd := m.inputController().showErrorStatus("boom")
	if cmd == nil {
		t.Fatal("expected clear command")
	}
	if m.transientStatus != "boom" {
		t.Fatalf("unexpected transient status %q", m.transientStatus)
	}
	if m.transientStatusKind != uiStatusNoticeError {
		t.Fatalf("expected error notice kind, got %d", m.transientStatusKind)
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

func TestPromptHistoryUpDownBrowseSubmittedPrompts(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIPromptHistory([]string{"first prompt", "second line\nthird line", "/resume"}),
	).(*uiModel)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := next.(*uiModel)
	if updated.input != "/resume" {
		t.Fatalf("expected newest prompt selected first, got %q", updated.input)
	}
	if updated.cursorIndex() != len([]rune(updated.input)) {
		t.Fatalf("expected history recall to place cursor at end, got %d", updated.cursorIndex())
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = next.(*uiModel)
	if updated.input != "second line\nthird line" {
		t.Fatalf("expected previous prompt selected, got %q", updated.input)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.input != "/resume" {
		t.Fatalf("expected down to move toward newer prompt, got %q", updated.input)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.input != "" {
		t.Fatalf("expected down past newest to restore draft, got %q", updated.input)
	}
	if updated.inputCursor != -1 {
		t.Fatalf("expected restored empty draft to track tail cursor, got %d", updated.inputCursor)
	}
}

func TestPromptHistoryUpCanEnterFromNewDraftAndRestoreItAfterReuse(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIPromptHistory([]string{"hello"}),
	).(*uiModel)
	m.input = "world"
	m.inputCursor = -1

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := next.(*uiModel)
	if updated.input != "world" {
		t.Fatalf("expected first up from draft tail to stay on draft, got %q", updated.input)
	}
	if updated.inputCursor != 0 {
		t.Fatalf("expected first up from draft tail to move cursor to start, got %d", updated.inputCursor)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = next.(*uiModel)
	if updated.input != "hello" {
		t.Fatalf("expected second up from draft start to recall history, got %q", updated.input)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyHome})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Hi!!")})
	updated = next.(*uiModel)
	if updated.input != "Hi!!hello" {
		t.Fatalf("expected edited recalled prompt, got %q", updated.input)
	}

	next, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if updated.input != "world" {
		t.Fatalf("expected parked draft restored after submitting recalled prompt, got %q", updated.input)
	}
	if cmd == nil {
		t.Fatal("expected submission command")
	}
}

func TestPromptHistoryUpFromMultilineDraftTailMovesWithinDraftBeforeRecall(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIPromptHistory([]string{"hello"}),
	).(*uiModel)
	m.input = "one\ntwo"
	m.inputCursor = -1

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := next.(*uiModel)
	if updated.input != "one\ntwo" {
		t.Fatalf("expected first up from multiline draft tail to stay on draft, got %q", updated.input)
	}
	if updated.inputCursor != 3 {
		t.Fatalf("expected first up from multiline draft tail to move to previous line, got %d", updated.inputCursor)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = next.(*uiModel)
	if updated.input != "one\ntwo" {
		t.Fatalf("expected second up within multiline draft to stay on draft, got %q", updated.input)
	}
	if updated.inputCursor != 0 {
		t.Fatalf("expected second up within multiline draft to reach buffer start, got %d", updated.inputCursor)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = next.(*uiModel)
	if updated.input != "hello" {
		t.Fatalf("expected third up from multiline draft start to recall history, got %q", updated.input)
	}
}

func TestPromptHistoryUsesBoundaryNavigationForMultilineSelection(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIPromptHistory([]string{"one\ntwo\nthree", "older"}),
	).(*uiModel)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := next.(*uiModel)
	if updated.input != "older" {
		t.Fatalf("expected newest prompt selected, got %q", updated.input)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = next.(*uiModel)
	if updated.input != "one\ntwo\nthree" {
		t.Fatalf("expected multiline prompt selected, got %q", updated.input)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyHome})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.input != "older" {
		t.Fatalf("expected down at buffer start to browse newer history, got %q", updated.input)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyHome})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.input != "one\ntwo\nthree" {
		t.Fatalf("expected down after sideways edit intent to stay in selected prompt, got %q", updated.input)
	}
	if updated.inputCursor != 5 {
		t.Fatalf("expected down to move within multiline prompt after leaving history mode, got %d", updated.inputCursor)
	}
}

func TestPromptHistoryBellWritesRawTerminalBell(t *testing.T) {
	var out bytes.Buffer
	previous := writeTerminalSequence
	writeTerminalSequence = func(sequence string) {
		_, _ = out.WriteString(sequence)
	}
	t.Cleanup(func() {
		writeTerminalSequence = previous
	})

	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIPromptHistory([]string{"only prompt"}),
	).(*uiModel)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := next.(*uiModel)
	next, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected bell command")
	}
	_ = cmd()

	if got := out.String(); got != terminalBell {
		t.Fatalf("expected raw terminal bell, got %q", got)
	}
	if updated.input != "only prompt" {
		t.Fatalf("expected prompt selection unchanged after bell miss, got %q", updated.input)
	}
}

func TestInterruptedQueuedPromptDoesNotEnterHistoryBeforeFlush(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.input = "queued later"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated := next.(*uiModel)
	if len(updated.promptHistory) != 0 {
		t.Fatalf("expected no prompt history before queued prompt flushes, got %+v", updated.promptHistory)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated = next.(*uiModel)
	if len(updated.promptHistory) != 0 {
		t.Fatalf("expected interrupted queued prompt not to enter history, got %+v", updated.promptHistory)
	}
	if updated.input != "queued later" {
		t.Fatalf("expected queued draft restored after interrupt, got %q", updated.input)
	}
}

func TestPreSubmitCompactionQueuesPromptUntilCompactionCompletes(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &runtimeAdapterFakeClient{}
	eng, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{
		Model:                         "gpt-5",
		PreSubmitCompactionLeadTokens: 50,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "continue"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if !updated.busy {
		t.Fatal("expected busy while pre-submit compaction check is in flight")
	}
	if updated.pendingPreSubmitText != "continue" {
		t.Fatalf("expected pending pre-submit text preserved, got %q", updated.pendingPreSubmitText)
	}
	if len(updated.queued) != 1 || updated.queued[0] != "continue" {
		t.Fatalf("expected prompt queued before compaction decision, got %+v", updated.queued)
	}

	next, _ = updated.Update(preSubmitCompactionCheckDoneMsg{
		token:         updated.preSubmitCheckToken,
		text:          "continue",
		shouldCompact: true,
	})
	updated = next.(*uiModel)
	if !updated.compacting {
		t.Fatal("expected compaction state after pre-submit compaction decision")
	}
	if updated.pendingPreSubmitText != "continue" {
		t.Fatalf("expected pending pre-submit text kept while compaction runs, got %q", updated.pendingPreSubmitText)
	}

	next, _ = updated.Update(compactDoneMsg{})
	updated = next.(*uiModel)
	if !updated.busy {
		t.Fatal("expected queued prompt to resume submission immediately after compaction")
	}
	if updated.pendingPreSubmitText != "continue" {
		t.Fatalf("expected resumed queued prompt to become pending pre-submit text again, got %q", updated.pendingPreSubmitText)
	}
	if len(updated.queued) != 1 || updated.queued[0] != "continue" {
		t.Fatalf("expected resumed queued prompt buffered for submission, got %+v", updated.queued)
	}

	next, _ = updated.Update(preSubmitCompactionCheckDoneMsg{
		token:         updated.preSubmitCheckToken,
		text:          "continue",
		shouldCompact: false,
	})
	updated = next.(*uiModel)
	if len(updated.queued) != 0 {
		t.Fatalf("expected queued prompt consumed before final submit, got %+v", updated.queued)
	}
	if got := updated.promptHistory[len(updated.promptHistory)-1]; got != "continue" {
		t.Fatalf("expected resumed queued prompt recorded when final submit begins, got %+v", updated.promptHistory)
	}

	next, _ = updated.Update(submitDoneMsg{})
	updated = next.(*uiModel)
	if updated.busy {
		t.Fatal("expected idle state after resumed queued prompt submits")
	}
	if updated.pendingPreSubmitText != "" {
		t.Fatalf("expected pending pre-submit text cleared after submit, got %q", updated.pendingPreSubmitText)
	}
	if updated.compacting {
		t.Fatal("expected compacting state cleared after resumed queued prompt submits")
	}
}

func TestPreSubmitCompactionKeepsDuplicateQueuedPromptsInOrder(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &runtimeAdapterFakeClient{}
	eng, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "continue"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	updated.queued = append(updated.queued, "fix", "continue")

	next, _ = updated.Update(preSubmitCompactionCheckDoneMsg{
		token:         updated.preSubmitCheckToken,
		text:          "continue",
		shouldCompact: false,
	})
	updated = next.(*uiModel)
	if len(updated.queued) != 2 {
		t.Fatalf("expected two queued prompts to remain, got %+v", updated.queued)
	}
	if updated.queued[0] != "fix" || updated.queued[1] != "continue" {
		t.Fatalf("expected duplicate queued prompts to preserve order, got %+v", updated.queued)
	}
}

func TestCtrlCWhilePreSubmitCheckRestoresDraftAndIgnoresStaleDecision(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, &runtimeAdapterFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "continue"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	originalToken := updated.preSubmitCheckToken
	if len(updated.promptHistory) != 0 {
		t.Fatalf("expected no prompt history before pre-submit decision, got %+v", updated.promptHistory)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated = next.(*uiModel)
	if updated.busy {
		t.Fatal("expected busy=false after ctrl+c during pre-submit check")
	}
	if updated.activity != uiActivityInterrupted {
		t.Fatalf("expected interrupted activity, got %v", updated.activity)
	}
	if updated.input != "continue" {
		t.Fatalf("expected draft restored after ctrl+c, got %q", updated.input)
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected queued draft restored into input and cleared, got %+v", updated.queued)
	}
	if updated.pendingPreSubmitText != "" {
		t.Fatalf("expected pending pre-submit text cleared after ctrl+c, got %q", updated.pendingPreSubmitText)
	}
	if len(updated.promptHistory) != 0 {
		t.Fatalf("expected ctrl+c before submit start to avoid prompt history persistence, got %+v", updated.promptHistory)
	}

	next, cmd := updated.Update(preSubmitCompactionCheckDoneMsg{token: originalToken, text: "continue", shouldCompact: true})
	updated = next.(*uiModel)
	if cmd != nil {
		t.Fatal("expected stale pre-submit result to be ignored")
	}
	if updated.input != "continue" {
		t.Fatalf("expected stale result to leave restored draft untouched, got %q", updated.input)
	}
	if updated.busy {
		t.Fatal("expected stale result not to restart submission")
	}
	if len(updated.promptHistory) != 0 {
		t.Fatalf("expected stale result not to record prompt history, got %+v", updated.promptHistory)
	}
}

func TestPreSubmitCompactionFailureKeepsPromptOutOfHistory(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, &runtimeAdapterFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "continue"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	next, _ = updated.Update(preSubmitCompactionCheckDoneMsg{
		token:         updated.preSubmitCheckToken,
		text:          "continue",
		shouldCompact: true,
	})
	updated = next.(*uiModel)

	next, _ = updated.Update(compactDoneMsg{err: errors.New("compact failed")})
	updated = next.(*uiModel)
	if updated.busy {
		t.Fatal("expected busy=false after compaction failure")
	}
	if updated.activity != uiActivityError {
		t.Fatalf("expected error activity after compaction failure, got %v", updated.activity)
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected failed pre-submit prompt removed from queue, got %+v", updated.queued)
	}
	if updated.pendingPreSubmitText != "" {
		t.Fatalf("expected pending pre-submit text cleared after compaction failure, got %q", updated.pendingPreSubmitText)
	}
	if updated.input != "continue" {
		t.Fatalf("expected failed pre-submit prompt restored into input, got %q", updated.input)
	}
	if len(updated.promptHistory) != 0 {
		t.Fatalf("expected compaction failure before submit start not to record prompt history, got %+v", updated.promptHistory)
	}
}

func TestPreSubmitCheckErrorRestoresQueuedSteeringInput(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, &runtimeAdapterFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "continue"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	updated.input = "later"

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if updated.inputSubmitLocked {
		t.Fatal("did not expect follow-up enter during pre-submit check to lock input")
	}
	if updated.input != "" {
		t.Fatalf("expected queued steering input cleared immediately, got %q", updated.input)
	}
	if len(updated.pendingInjected) != 1 || updated.pendingInjected[0] != "later" {
		t.Fatalf("expected pending injected follow-up recorded, got %+v", updated.pendingInjected)
	}

	next, _ = updated.Update(preSubmitCompactionCheckDoneMsg{
		token: updated.preSubmitCheckToken,
		text:  "continue",
		err:   errors.New("pre-submit failed"),
	})
	updated = next.(*uiModel)
	if updated.inputSubmitLocked {
		t.Fatal("did not expect pre-submit check error to leave input locked")
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected pending injected follow-up cleared, got %+v", updated.pendingInjected)
	}
	if updated.pendingPreSubmitText != "" {
		t.Fatalf("expected pending pre-submit text cleared after error, got %q", updated.pendingPreSubmitText)
	}
	if updated.input != "later\n\ncontinue" {
		t.Fatalf("expected restored prompt and unlocked follow-up draft, got %q", updated.input)
	}
}

func TestPreSubmitCheckErrorRestoresQueuedSteeringAndDiscardsEngineQueue(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &requestCaptureFakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	eng, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "continue"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	updated.input = "later"
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	next, _ = updated.Update(preSubmitCompactionCheckDoneMsg{
		token: updated.preSubmitCheckToken,
		text:  "continue",
		err:   errors.New("pre-submit failed"),
	})
	updated = next.(*uiModel)

	if updated.input != "later\n\ncontinue" {
		t.Fatalf("expected restored steering and pre-submit text in input, got %q", updated.input)
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected UI pending steering cleared after restore, got %+v", updated.pendingInjected)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "fresh prompt")
	if err != nil {
		t.Fatalf("submit fresh prompt: %v", err)
	}
	if msg.Content != "ok" {
		t.Fatalf("assistant content = %q, want ok", msg.Content)
	}
	requests := client.Requests()
	if len(requests) != 1 {
		t.Fatalf("expected one model request without stale runtime steering, got %d", len(requests))
	}
	for _, message := range requestMessages(requests[0]) {
		if message.Role == llm.RoleUser && message.Content == "later" {
			t.Fatalf("did not expect restored steering to remain queued in runtime request: %+v", requestMessages(requests[0]))
		}
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
	if resp.response.Answer != "hello world" {
		t.Fatalf("expected freeform answer with space, got %q", resp.response.Answer)
	}
	if testActiveAsk(updated) != nil {
		t.Fatal("ask should be resolved")
	}
}

func TestApprovalAskTabInCommentaryDoesNotReturnToPicker(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Approve?", Approval: true, ApprovalOptions: []askquestion.ApprovalOption{{Decision: askquestion.ApprovalDecisionAllowOnce, Label: "Allow once"}, {Decision: askquestion.ApprovalDecisionAllowSession, Label: "Allow for this session"}, {Decision: askquestion.ApprovalDecisionDeny, Label: "Deny"}}}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)
	if !testAskFreeform(updated) {
		t.Fatal("expected approval tab to enter commentary mode")
	}
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("commentary")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)
	if !testAskFreeform(updated) {
		t.Fatal("did not expect approval commentary tab to return to picker")
	}
	if testAskInput(updated) != "commentary" {
		t.Fatalf("expected commentary preserved in approval freeform, got %q", testAskInput(updated))
	}
}

func TestApprovalAskTabCommentaryUsesCurrentSelection(t *testing.T) {
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
	event := askEvent{req: askquestion.Request{Question: "Approve?", Approval: true, ApprovalOptions: []askquestion.ApprovalOption{{Decision: askquestion.ApprovalDecisionAllowOnce, Label: "Allow once"}, {Decision: askquestion.ApprovalDecisionAllowSession, Label: "Allow for this session"}, {Decision: askquestion.ApprovalDecisionDeny, Label: "Deny"}}}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("session only")})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	resp := <-reply
	if resp.response.Approval == nil {
		t.Fatal("expected typed approval response")
	}
	if resp.response.Approval.Decision != askquestion.ApprovalDecisionAllowSession || resp.response.Approval.Commentary != "session only" {
		t.Fatalf("unexpected approval response: %+v", resp.response.Approval)
	}
	if len(updated.pendingInjected) != 1 || updated.pendingInjected[0] != "session only" {
		t.Fatalf("expected selected approval commentary injected, got %+v", updated.pendingInjected)
	}
}

func TestApprovalAskPickerSubmitIgnoresPendingCommentaryDraft(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	reply := make(chan askReply, 1)
	event := askEvent{req: askquestion.Request{Question: "Approve?", Approval: true, ApprovalOptions: []askquestion.ApprovalOption{{Decision: askquestion.ApprovalDecisionAllowOnce, Label: "Allow once"}, {Decision: askquestion.ApprovalDecisionAllowSession, Label: "Allow for this session"}, {Decision: askquestion.ApprovalDecisionDeny, Label: "Deny"}}}, reply: reply}

	next, _ := m.Update(askEventMsg{event: event})
	updated := next.(*uiModel)
	testSetAskInput(updated, "stale commentary")
	testSetAskInputCursor(updated, len([]rune(testAskInput(updated))))
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	resp := <-reply
	if resp.response.Approval == nil {
		t.Fatal("expected typed approval response")
	}
	if resp.response.Approval.Decision != askquestion.ApprovalDecisionAllowSession {
		t.Fatalf("unexpected approval decision: %+v", resp.response.Approval)
	}
	if resp.response.Approval.Commentary != "" {
		t.Fatalf("did not expect picker submission to include commentary draft, got %+v", resp.response.Approval)
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("did not expect picker submission to inject commentary, got %+v", updated.pendingInjected)
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
	m.windowSizeKnown = true
	m.input = "hello world"
	m.syncViewport()

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
	m.windowSizeKnown = true
	m.input = "hello"
	m.inputCursor = 2
	m.syncViewport()

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
	m.windowSizeKnown = true
	m.inputSubmitLocked = true
	m.input = "hello world"
	m.syncViewport()

	view := m.View()
	if !strings.Contains(view, ansiHideCursor) {
		t.Fatalf("expected terminal cursor hide sequence in view: %q", view)
	}
	plain := stripANSIAndTrimRight(view)
	if !strings.Contains(plain, "⨯ hello world") {
		t.Fatalf("expected locked input text preserved, got %q", plain)
	}
}

func TestMainInputViewportTracksCursorLine(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 20
	m.termHeight = 6
	m.windowSizeKnown = true
	m.input = "first\nsecond\nthird\nfourth"
	m.inputCursor = 1
	m.syncViewport()

	plain := stripANSIAndTrimRight(strings.Join(m.renderInputLines(20, uiThemeStyles("dark")), "\n"))
	if !strings.Contains(plain, "› first") || !strings.Contains(plain, "second") {
		t.Fatalf("expected viewport to keep cursor line visible, got %q", plain)
	}
	if strings.Contains(plain, "fourth") {
		t.Fatalf("expected viewport not to pin to tail while cursor is near top, got %q", plain)
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

func TestBusyEnterQueuesSteeringUntilFlushed(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "please continue with tests"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if updated.inputSubmitLocked {
		t.Fatal("did not expect input submit lock after enter while busy")
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after queueing steering, got %q", updated.input)
	}
	if len(updated.pendingInjected) != 1 {
		t.Fatalf("expected one pending injected message, got %d", len(updated.pendingInjected))
	}

	next, _ = updated.Update(runtimeEventMsg{event: runtime.Event{
		Kind:             runtime.EventUserMessageFlushed,
		UserMessage:      "please continue with tests",
		UserMessageBatch: []string{"please continue with tests"},
	}})
	updated = next.(*uiModel)
	if updated.inputSubmitLocked {
		t.Fatal("did not expect input lock after flush")
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after flush, got %q", updated.input)
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected queued steering cleared after flush, got %+v", updated.pendingInjected)
	}
}

func TestBusyEnterCanQueueMultipleSteeringMessages(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "first steering message"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	updated.input = "second steering message"

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if len(updated.pendingInjected) != 2 {
		t.Fatalf("expected two queued steering messages, got %+v", updated.pendingInjected)
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after queueing multiple steering messages, got %q", updated.input)
	}

	next, _ = updated.Update(runtimeEventMsg{event: runtime.Event{
		Kind:             runtime.EventUserMessageFlushed,
		UserMessage:      "first steering message\n\nsecond steering message",
		UserMessageBatch: []string{"first steering message", "second steering message"},
	}})
	updated = next.(*uiModel)
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected queued steering cleared after batched flush, got %+v", updated.pendingInjected)
	}
}

func TestBusySteeringBatchFlushPreservesPostTurnQueueOrder(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "first steering message"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	updated.input = "second steering message"

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	updated.input = "queued after turn"

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)
	if len(updated.pendingInjected) != 2 {
		t.Fatalf("expected two queued steering messages, got %+v", updated.pendingInjected)
	}
	if len(updated.queued) != 1 || updated.queued[0] != "queued after turn" {
		t.Fatalf("expected normal queued input preserved, got %+v", updated.queued)
	}

	next, _ = updated.Update(runtimeEventMsg{event: runtime.Event{
		Kind:             runtime.EventUserMessageFlushed,
		UserMessage:      "first steering message\n\nsecond steering message",
		UserMessageBatch: []string{"first steering message", "second steering message"},
	}})
	updated = next.(*uiModel)
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected steering queue cleared after batched flush, got %+v", updated.pendingInjected)
	}
	if len(updated.queued) != 1 || updated.queued[0] != "queued after turn" {
		t.Fatalf("expected post-turn queue preserved until turn completion, got %+v", updated.queued)
	}

	next, cmd := updated.Update(submitDoneMsg{})
	updated = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected post-turn queue to start draining after turn completion")
	}
	if !updated.busy {
		t.Fatal("expected queued post-turn input to begin submission after steering flush")
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected post-turn queue drained in order, got %+v", updated.queued)
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

func TestSubmitErrorRestoresQueuedSteeringInput(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "please continue with tests"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if updated.inputSubmitLocked {
		t.Fatal("did not expect input submit lock after enter while busy")
	}
	if len(updated.pendingInjected) != 1 {
		t.Fatalf("expected one pending injected message, got %d", len(updated.pendingInjected))
	}

	updated.queued = append(updated.queued, "follow-up")
	next, cmd := updated.Update(submitDoneMsg{err: errors.New("network failure")})
	updated = next.(*uiModel)
	if cmd != nil {
		t.Fatal("did not expect follow-up queued submission to start while restored steering input is present")
	}
	if updated.busy {
		t.Fatal("did not expect busy after submission error")
	}
	if updated.inputSubmitLocked {
		t.Fatal("did not expect submit lock after submission error")
	}
	if updated.input != "please continue with tests" {
		t.Fatalf("expected queued steering restored into input, got %q", updated.input)
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected pending injection queue cleared after restore, got %d", len(updated.pendingInjected))
	}
}

func TestSubmitErrorRestoresQueuedSteeringAndDiscardsEngineQueue(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &requestCaptureFakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	eng, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "restored steering"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	next, _ = updated.Update(submitDoneMsg{err: errors.New("network failure")})
	updated = next.(*uiModel)

	if updated.input != "restored steering" {
		t.Fatalf("expected steering restored into input, got %q", updated.input)
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected UI pending steering cleared after restore, got %+v", updated.pendingInjected)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "fresh prompt")
	if err != nil {
		t.Fatalf("submit fresh prompt: %v", err)
	}
	if msg.Content != "ok" {
		t.Fatalf("assistant content = %q, want ok", msg.Content)
	}
	requests := client.Requests()
	if len(requests) != 1 {
		t.Fatalf("expected one model request without stale runtime steering, got %d", len(requests))
	}
	for _, message := range requestMessages(requests[0]) {
		if message.Role == llm.RoleUser && message.Content == "restored steering" {
			t.Fatalf("did not expect restored steering to remain queued in runtime request: %+v", requestMessages(requests[0]))
		}
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

func TestQueueInjectedInputIgnoresBlankTextWithoutClearingInput(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "keep this draft"
	m.activity = uiActivityIdle

	m.queueInjectedInput("   \n\t  ")

	if m.input != "keep this draft" {
		t.Fatalf("expected blank injected input to leave draft untouched, got %q", m.input)
	}
	if len(m.pendingInjected) != 0 {
		t.Fatalf("expected no queued injected messages, got %+v", m.pendingInjected)
	}
	if m.activity != uiActivityIdle {
		t.Fatalf("expected blank injected input to leave activity unchanged, got %q", m.activity)
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

func TestCtrlCWhileBusyRestoresQueuedSlashCommandsIntoInput(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.queued = []string{"/name queued title"}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated := next.(*uiModel)

	if updated.busy {
		t.Fatal("expected busy=false after ctrl+c interrupt")
	}
	if updated.activity != uiActivityInterrupted {
		t.Fatalf("expected interrupted activity, got %v", updated.activity)
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected queued slash command restored into input and cleared, got %+v", updated.queued)
	}
	if updated.input != "/name queued title" {
		t.Fatalf("expected queued slash command restored into input, got %q", updated.input)
	}
}

func TestCtrlCWhileBusyRestoresMixedQueuedInputsIntoInput(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.queued = []string{"draft one", "draft two", "/name queued title", "later draft"}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated := next.(*uiModel)

	if updated.input != "draft one\n\ndraft two\n\n/name queued title\n\nlater draft" {
		t.Fatalf("expected all queued inputs restored into input, got %q", updated.input)
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected queue cleared after restore, got %+v", updated.queued)
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
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected pending injected queue restored into input and cleared, got %+v", updated.pendingInjected)
	}
	if updated.input != "another" {
		t.Fatalf("expected remaining queued steering restored into input, got %q", updated.input)
	}
}

func TestCtrlCRestoresQueuedSteeringAndDiscardsEngineQueue(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &requestCaptureFakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	eng, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "restored steering"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated = next.(*uiModel)

	if updated.input != "restored steering" {
		t.Fatalf("expected steering restored into input, got %q", updated.input)
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected UI pending steering cleared after restore, got %+v", updated.pendingInjected)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "fresh prompt")
	if err != nil {
		t.Fatalf("submit fresh prompt: %v", err)
	}
	if msg.Content != "ok" {
		t.Fatalf("assistant content = %q, want ok", msg.Content)
	}
	requests := client.Requests()
	if len(requests) != 1 {
		t.Fatalf("expected one model request without stale runtime steering, got %d", len(requests))
	}
	for _, message := range requestMessages(requests[0]) {
		if message.Role == llm.RoleUser && message.Content == "restored steering" {
			t.Fatalf("did not expect restored steering to remain queued in runtime request: %+v", requestMessages(requests[0]))
		}
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

func TestCompactDoneKeepsQueuedSteeringPending(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.input = "please continue with tests"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if updated.inputSubmitLocked {
		t.Fatal("did not expect input submit lock after enter while busy")
	}
	if len(updated.pendingInjected) != 1 {
		t.Fatalf("expected one pending injected message, got %d", len(updated.pendingInjected))
	}

	next, _ = updated.Update(compactDoneMsg{})
	updated = next.(*uiModel)
	if updated.inputSubmitLocked {
		t.Fatal("did not expect submit lock after compaction completion")
	}
	if len(updated.pendingInjected) != 1 || updated.pendingInjected[0] != "please continue with tests" {
		t.Fatalf("expected queued steering preserved across compaction completion, got %+v", updated.pendingInjected)
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
	m.windowSizeKnown = true
	m.busy = true
	m.sawAssistantDelta = true
	m.syncViewport()
	m.forwardToView(tui.SetConversationMsg{
		Entries: []tui.TranscriptEntry{
			{Role: "user", Text: "prior user"},
			{Role: "assistant", Text: "prior assistant"},
		},
		Ongoing: "streaming now",
	})

	view := stripANSIAndTrimRight(m.view.OngoingSnapshot())
	if !strings.Contains(view, "prior assistant") || !strings.Contains(view, "prior user") {
		t.Fatalf("expected ongoing render to keep committed transcript visible, got %q", view)
	}
	compact := stripANSIAndTrimRight(m.View())
	if !strings.Contains(compact, "streaming now") {
		t.Fatalf("expected ongoing compact render to include live streaming content, got %q", compact)
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

func TestRenderQueuedMessagesPaneShowsPendingInjectedAfterQueuedMessages(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.queued = []string{"queued later"}
	m.pendingInjected = []string{"please continue with tests"}

	lines := m.renderQueuedMessagesPane(80)
	plain := strings.Split(stripANSIAndTrimRight(strings.Join(lines, "\n")), "\n")
	want := []string{"queued later", "next: please continue with tests"}
	if len(plain) != len(want) {
		t.Fatalf("expected %d plain lines, got %d", len(want), len(plain))
	}
	for i := range want {
		if plain[i] != want[i] {
			t.Fatalf("line %d = %q want %q", i, plain[i], want[i])
		}
	}
}

func TestRenderQueuedMessagesPanePrioritizesPendingInjectedWithinVisibleLimit(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.queued = []string{"one", "two", "three", "four", "five", "six"}
	m.pendingInjected = []string{"first steering", "second steering"}

	lines := m.renderQueuedMessagesPane(80)
	plain := strings.Split(stripANSIAndTrimRight(strings.Join(lines, "\n")), "\n")
	want := []string{"3 more messages", "four", "five", "six", "next: first steering", "next: second steering"}
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
	m.windowSizeKnown = true
	m.input = "/"
	m.refreshSlashCommandFilterFromInput()
	m.queued = []string{"queued latest"}
	m.syncViewport()

	view := stripANSIAndTrimRight(m.View())
	if !containsInOrder(view, "/new", "queued latest", "› /") {
		t.Fatalf("expected slash picker above queued pane above input, got %q", view)
	}
}

func TestSlashPickerShowsFastForOpenAIFirstPartyResponsesProvider(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, statusLineFastClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/"
	m.refreshSlashCommandFilterFromInput()

	state := m.slashCommandPicker()
	if !state.visible {
		t.Fatal("expected slash picker visible")
	}
	if !slashPickerContainsCommand(state, "fast") {
		t.Fatalf("expected /fast in slash picker, got %+v", slashPickerCommandNames(state))
	}
}

func TestSlashPickerHidesFastForNonFirstPartyResponsesProvider(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, statusLineAzureClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/"
	m.refreshSlashCommandFilterFromInput()

	state := m.slashCommandPicker()
	if !state.visible {
		t.Fatal("expected slash picker visible")
	}
	if slashPickerContainsCommand(state, "fast") {
		t.Fatalf("did not expect /fast in slash picker, got %+v", slashPickerCommandNames(state))
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

func TestPSCommandOpensDetailOverlayInNativeMode(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
	).(*uiModel)
	m.termWidth = 100
	m.termHeight = 14
	m.windowSizeKnown = true
	m.input = "/ps"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if !testProcessListOpen(updated) {
		t.Fatal("expected /ps to open the process list")
	}
	if !testProcessListOwnsTranscriptMode(updated) {
		t.Fatal("expected /ps to push a dedicated overlay")
	}
	if updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected /ps to switch into detail mode, got %q", updated.view.Mode())
	}
	if cmd == nil {
		t.Fatal("expected /ps open to emit a screen transition command")
	}
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "Background Processes") {
		t.Fatalf("expected process list title in overlay, got %q", plain)
	}
	if !strings.Contains(plain, "Esc/q close") {
		t.Fatalf("expected process list help text in overlay, got %q", plain)
	}
	lines := strings.Split(plain, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected multi-line /ps overlay, got %q", plain)
	}
	if strings.Contains(lines[0], "Background Processes") || strings.Contains(lines[0], "Esc/q close") {
		t.Fatalf("expected /ps controls moved out of the header, top line=%q", lines[0])
	}
	footer := strings.Join(lines[max(0, len(lines)-3):], "\n")
	if !strings.Contains(footer, "Background Processes") || !strings.Contains(footer, "Esc/q close") {
		t.Fatalf("expected /ps controls near the bottom of the overlay, footer=%q", footer)
	}

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if testProcessListOpen(updated) {
		t.Fatal("expected esc to close the process list")
	}
	if testProcessListOwnsTranscriptMode(updated) {
		t.Fatal("expected process overlay state cleared after close")
	}
	if updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected process list close to restore ongoing mode, got %q", updated.view.Mode())
	}
	if cmd == nil {
		t.Fatal("expected /ps close to emit a screen transition command")
	}
}

func TestPSOverlayScrollKeepsEntryHeadersVisibleAtTop(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new background manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	workdir := t.TempDir()
	for i := 1; i <= 4; i++ {
		res, startErr := manager.Start(context.Background(), shelltool.ExecRequest{
			Command:        []string{"sh", "-c", fmt.Sprintf("printf 'job-%d\\n'; sleep 30", i)},
			DisplayCommand: fmt.Sprintf("job-%d", i),
			Workdir:        workdir,
			YieldTime:      250 * time.Millisecond,
		})
		if startErr != nil {
			t.Fatalf("start job-%d: %v", i, startErr)
		}
		if !res.Backgrounded {
			t.Fatalf("expected job-%d to move to background", i)
		}
	}

	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIBackgroundManager(manager),
	).(*uiModel)
	m.termWidth = 120
	m.termHeight = 17
	m.windowSizeKnown = true
	m.input = "/ps"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	for i := 0; i < 3; i++ {
		next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
		updated = next.(*uiModel)
	}

	lines := strings.Split(stripANSIAndTrimRight(updated.View()), "\n")
	if len(lines) == 0 {
		t.Fatal("expected rendered /ps overlay")
	}
	top := strings.TrimSpace(lines[0])
	if top == "" {
		t.Fatalf("expected top line to begin with a process header, got blank line in %q", lines[0])
	}
	if strings.Contains(top, "cwd:") || strings.Contains(top, "log:") || strings.Contains(top, "out:") {
		t.Fatalf("expected /ps overlay to start on a process header, got %q", top)
	}
	if !strings.Contains(top, "[") {
		t.Fatalf("expected /ps header line with process state at top, got %q", top)
	}
}

func TestPSOverlayScrollShowsSelectedEntryFullyNearBottom(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new background manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	for i := 1; i <= 4; i++ {
		workdir := filepath.Join(t.TempDir(), fmt.Sprintf("job-%d", i))
		if mkErr := os.MkdirAll(workdir, 0o755); mkErr != nil {
			t.Fatalf("mkdir workdir %d: %v", i, mkErr)
		}
		res, startErr := manager.Start(context.Background(), shelltool.ExecRequest{
			Command:        []string{"sh", "-c", fmt.Sprintf("printf 'job-%d-output\\n'; sleep 30", i)},
			DisplayCommand: fmt.Sprintf("job-%d-command", i),
			Workdir:        workdir,
			YieldTime:      250 * time.Millisecond,
		})
		if startErr != nil {
			t.Fatalf("start job-%d: %v", i, startErr)
		}
		if !res.Backgrounded {
			t.Fatalf("expected job-%d to move to background", i)
		}
	}

	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIBackgroundManager(manager),
	).(*uiModel)
	m.termWidth = 120
	m.termHeight = 17
	m.windowSizeKnown = true
	m.input = "/ps"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	for i := 0; i < 3; i++ {
		next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
		updated = next.(*uiModel)
	}

	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "> [running]") {
		t.Fatalf("expected selected entry header to remain visible near bottom, got %q", plain)
	}
	if !strings.Contains(plain, "job-1-command") {
		t.Fatalf("expected selected entry command visible near bottom, got %q", plain)
	}
	if !strings.Contains(plain, "cwd: ") || !strings.Contains(plain, "/job-1") {
		t.Fatalf("expected selected entry cwd visible near bottom, got %q", plain)
	}
	if !strings.Contains(plain, "out: job-1-output") {
		t.Fatalf("expected selected entry output visible near bottom, got %q", plain)
	}
}

func TestPSOverlayInlineAppendsOutputToInputAndReturnsToOngoing(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new background manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	workdir := t.TempDir()
	start := func(label string) string {
		res, startErr := manager.Start(context.Background(), shelltool.ExecRequest{
			Command:        []string{"sh", "-c", fmt.Sprintf("printf '%s\\n'; sleep 30", label)},
			DisplayCommand: label,
			Workdir:        workdir,
			YieldTime:      250 * time.Millisecond,
		})
		if startErr != nil {
			t.Fatalf("start %s: %v", label, startErr)
		}
		if !res.Backgrounded {
			t.Fatalf("expected %s to move to background", label)
		}
		return res.SessionID
	}

	firstID := start("first-job")
	secondID := start("second-job")

	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIBackgroundManager(manager),
	).(*uiModel)
	m.termWidth = 100
	m.termHeight = 14
	m.windowSizeKnown = true
	m.input = "/ps"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	selected, ok := updated.selectedProcess()
	if !ok {
		t.Fatal("expected a selected background process")
	}
	if selected.ID != secondID {
		t.Fatalf("expected newest process %s selected first, got %s", secondID, selected.ID)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	selected, ok = updated.selectedProcess()
	if !ok {
		t.Fatal("expected selection after moving down")
	}
	if selected.ID != firstID {
		t.Fatalf("expected moved selection to reach %s, got %s", firstID, selected.ID)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if testProcessListOpen(updated) {
		t.Fatal("expected inline paste to close the process overlay")
	}
	if updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected inline paste to return to ongoing mode, got %q", updated.view.Mode())
	}
	if !strings.Contains(updated.input, "Output of bg shell "+firstID+":") {
		t.Fatalf("expected inline paste prefix in input buffer, got %q", updated.input)
	}
	if !strings.Contains(updated.input, "first-job") {
		t.Fatalf("expected pasted shell content in input buffer, got %q", updated.input)
	}
	if !strings.Contains(stripANSIAndTrimRight(updated.renderStatusLine(120, uiThemeStyles("dark"))), "Pasted shell transcript") {
		t.Fatal("expected ongoing status line to show pasted shell transcript notice")
	}
}

func TestPSOverlayInlineUnlocksLockedInputBeforeAppending(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new background manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	workdir := t.TempDir()
	res, err := manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "printf 'locked-job\n'; sleep 30"},
		DisplayCommand: "locked-job",
		Workdir:        workdir,
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start locked-job: %v", err)
	}
	if !res.Backgrounded {
		t.Fatal("expected background process")
	}

	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIBackgroundManager(manager)).(*uiModel)
	m.termWidth = 100
	m.termHeight = 14
	m.windowSizeKnown = true
	m.busy = true
	m.input = "queued draft"
	m.inputSubmitLocked = true
	m.lockedInjectText = "queued draft"
	m.pendingInjected = []string{"queued draft"}
	controller := uiInputController{model: m}
	_ = controller.startProcessListFlowCmd()
	updated := m
	var next tea.Model
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	if updated.inputSubmitLocked {
		t.Fatal("expected inline paste to unlock the input box")
	}
	if updated.lockedInjectText != "" {
		t.Fatalf("expected lockedInjectText cleared, got %q", updated.lockedInjectText)
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected pending injected messages cleared, got %d", len(updated.pendingInjected))
	}
	if !strings.Contains(updated.input, "Output of bg shell "+res.SessionID+":") {
		t.Fatalf("expected pasted shell output in unlocked draft, got %q", updated.input)
	}
	if !strings.Contains(updated.input, "locked-job") {
		t.Fatalf("expected shell preview content in unlocked draft, got %q", updated.input)
	}
	if updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected inline paste to end in ongoing mode, got %q", updated.view.Mode())
	}
}

func TestPSOverlayRefreshTickUpdatesEntriesWhileOpen(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new background manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIBackgroundManager(manager)).(*uiModel)
	m.termWidth = 100
	m.termHeight = 14
	m.windowSizeKnown = true
	m.input = "/ps"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if got := len(updated.processList.entries); got != 0 {
		t.Fatalf("expected empty /ps list before refresh tick, got %d", got)
	}

	workdir := t.TempDir()
	res, err := manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "printf 'tick-job\n'; sleep 30"},
		DisplayCommand: "tick-job",
		Workdir:        workdir,
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start tick-job: %v", err)
	}
	if !res.Backgrounded {
		t.Fatal("expected tick-job to move to background")
	}

	next, cmd := updated.Update(processListRefreshTickMsg{})
	updated = next.(*uiModel)
	if got := len(updated.processList.entries); got != 1 {
		t.Fatalf("expected refresh tick to pull new process entry, got %d", got)
	}
	if updated.processList.entries[0].ID != res.SessionID {
		t.Fatalf("expected refresh tick to load session %s, got %s", res.SessionID, updated.processList.entries[0].ID)
	}
	if cmd == nil {
		t.Fatal("expected refresh tick to schedule the next refresh")
	}
}

func TestPSOverlayRefreshPreservesSelectionByProcessID(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new background manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	workdir := t.TempDir()
	first, err := manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "printf 'first\n'; sleep 30"},
		DisplayCommand: "first",
		Workdir:        workdir,
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start first job: %v", err)
	}
	second, err := manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "printf 'second\n'; sleep 30"},
		DisplayCommand: "second",
		Workdir:        workdir,
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start second job: %v", err)
	}

	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIBackgroundManager(manager)).(*uiModel)
	m.refreshProcessEntries()
	if len(m.processList.entries) != 2 {
		t.Fatalf("expected two process entries, got %d", len(m.processList.entries))
	}
	selectedID := first.SessionID
	if m.processList.entries[0].ID == selectedID {
		selectedID = second.SessionID
	}
	for idx, entry := range m.processList.entries {
		if entry.ID == selectedID {
			m.processList.selection = idx
			break
		}
	}

	_, err = manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "printf 'third\n'; sleep 30"},
		DisplayCommand: "third",
		Workdir:        workdir,
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start third job: %v", err)
	}

	m.refreshProcessEntries()
	if m.processList.entries[m.processList.selection].ID != selectedID {
		t.Fatalf("expected selection to remain on process %s, got %s", selectedID, m.processList.entries[m.processList.selection].ID)
	}
}

func TestOpenLogsFallsBackToEditorCommandWhenDefaultOpenFails(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new background manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	workdir := t.TempDir()
	res, err := manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "printf 'log-job\n'; sleep 30"},
		DisplayCommand: "log-job",
		Workdir:        workdir,
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start log-job: %v", err)
	}
	if !res.Backgrounded {
		t.Fatal("expected log-job to move to background")
	}

	originalOpenDefault := openDefault
	openDefault = func(string) error { return errors.New("forced open failure") }
	defer func() { openDefault = originalOpenDefault }()

	marker := filepath.Join(t.TempDir(), "editor-opened")
	oldVisual, hadVisual := os.LookupEnv("VISUAL")
	oldEditor, hadEditor := os.LookupEnv("EDITOR")
	oldShell, hadShell := os.LookupEnv("SHELL")
	if err := os.Setenv("VISUAL", "touch "+marker); err != nil {
		t.Fatalf("set VISUAL: %v", err)
	}
	if err := os.Unsetenv("EDITOR"); err != nil {
		t.Fatalf("unset EDITOR: %v", err)
	}
	if err := os.Setenv("SHELL", "/bin/sh"); err != nil {
		t.Fatalf("set SHELL: %v", err)
	}
	defer func() {
		if hadVisual {
			_ = os.Setenv("VISUAL", oldVisual)
		} else {
			_ = os.Unsetenv("VISUAL")
		}
		if hadEditor {
			_ = os.Setenv("EDITOR", oldEditor)
		} else {
			_ = os.Unsetenv("EDITOR")
		}
		if hadShell {
			_ = os.Setenv("SHELL", oldShell)
		} else {
			_ = os.Unsetenv("SHELL")
		}
	}()

	out := &bytes.Buffer{}
	model := NewUIModel(nil, closedRuntimeEvents(), closedAskEvents(), WithUIBackgroundManager(manager)).(*uiModel)
	model.input = "/ps"
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, runErr := program.Run()
		done <- runErr
	}()
	time.Sleep(40 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	time.Sleep(20 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyEnter})
	time.Sleep(20 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, statErr := os.Stat(marker); statErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expected editor fallback to execute via tea.ExecProcess")
		}
		time.Sleep(20 * time.Millisecond)
	}

	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("program run failed: %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}
}

func TestPSOverlayIgnoresTranscriptModeTogglesWhileOpen(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
	).(*uiModel)
	m.termWidth = 100
	m.termHeight = 14
	m.windowSizeKnown = true
	m.input = "/ps"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if !testProcessListOpen(updated) || updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected /ps overlay open in detail mode, visible=%t mode=%q", testProcessListOpen(updated), updated.view.Mode())
	}

	next, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	updated = next.(*uiModel)
	if !testProcessListOpen(updated) || !testProcessListOwnsTranscriptMode(updated) {
		t.Fatalf("expected shift+tab ignored while /ps overlay open, visible=%t overlay=%t", testProcessListOpen(updated), testProcessListOwnsTranscriptMode(updated))
	}
	if updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected shift+tab to keep detail mode while /ps overlay open, got %q", updated.view.Mode())
	}
	if cmd != nil {
		t.Fatal("expected no transcript toggle command while /ps overlay is open")
	}

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	updated = next.(*uiModel)
	if !testProcessListOpen(updated) || !testProcessListOwnsTranscriptMode(updated) {
		t.Fatalf("expected ctrl+t ignored while /ps overlay open, visible=%t overlay=%t", testProcessListOpen(updated), testProcessListOwnsTranscriptMode(updated))
	}
	if updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected ctrl+t to keep detail mode while /ps overlay open, got %q", updated.view.Mode())
	}
	if cmd != nil {
		t.Fatal("expected no transcript toggle command for ctrl+t while /ps overlay is open")
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
	if updated.input != "/new" {
		t.Fatalf("expected first down to select /new, got %q", updated.input)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.input != "/exit" {
		t.Fatalf("expected second down to select /exit, got %q", updated.input)
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

func TestSlashCommandTabAutocompletesSelectedCommandAndAddsSpace(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/ne"
	m.refreshSlashCommandFilterFromInput()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated := next.(*uiModel)
	if updated.input != "/new " {
		t.Fatalf("expected tab to autocomplete /new with trailing space, got %q", updated.input)
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected no queued messages after autocomplete, got %d", len(updated.queued))
	}
	if updated.busy {
		t.Fatal("did not expect autocomplete to start submission")
	}
}

func TestSlashCommandEnterExecutesSelectedPartialMatch(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/ex"
	m.refreshSlashCommandFilterFromInput()

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected quit cmd for selected /exit partial match")
	}
	if updated.Action() != UIActionExit {
		t.Fatalf("expected UIActionExit, got %q", updated.Action())
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after slash command execution, got %q", updated.input)
	}
}

func TestBusyTabQueuesSlashCommandAndFlushesAfterTurn(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.input = "/name queued title"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated := next.(*uiModel)
	if len(updated.queued) != 1 || updated.queued[0] != "/name queued title" {
		t.Fatalf("expected queued slash command, got %+v", updated.queued)
	}
	if updated.sessionName != "" {
		t.Fatalf("did not expect queued slash command to execute immediately, got %q", updated.sessionName)
	}

	next, cmd := updated.Update(submitDoneMsg{})
	updated = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected follow-up command from queued /name execution")
	}
	if updated.sessionName != "queued title" {
		t.Fatalf("expected queued /name to execute after turn, got %q", updated.sessionName)
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected queued slash command drained, got %+v", updated.queued)
	}
}

func TestBusyQueuedSlashCommandDrainContinuesIntoQueuedPrompt(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.input = "/name queued title"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated := next.(*uiModel)
	updated.input = "follow up"

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)
	if len(updated.queued) != 2 {
		t.Fatalf("expected two queued items, got %+v", updated.queued)
	}

	next, _ = updated.Update(submitDoneMsg{})
	updated = next.(*uiModel)
	if updated.sessionName != "queued title" {
		t.Fatalf("expected queued /name to execute before queued prompt, got %q", updated.sessionName)
	}
	if !updated.busy {
		t.Fatal("expected queued prompt to auto-submit after queued slash command")
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected queued items fully drained, got %+v", updated.queued)
	}
	plain := stripANSIAndTrimRight(updated.view.OngoingSnapshot())
	if !strings.Contains(plain, "follow up") {
		t.Fatalf("expected queued prompt in transcript, got %q", plain)
	}
}

func TestAutoDrainStopsAfterQueuedPSInlineAppendsToInput(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new background manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	workdir := t.TempDir()
	res, err := manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "printf 'queued-inline\n'; sleep 30"},
		DisplayCommand: "queued-inline",
		Workdir:        workdir,
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start queued-inline: %v", err)
	}
	if !res.Backgrounded {
		t.Fatal("expected background process")
	}

	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIBackgroundManager(manager)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.queued = []string{"/ps inline " + res.SessionID, "summarize this"}

	next, cmd := m.Update(submitDoneMsg{})
	updated := next.(*uiModel)

	if cmd == nil {
		t.Fatal("expected command batch from queued /ps inline execution")
	}
	if updated.busy {
		t.Fatal("did not expect queued /ps inline to auto-submit the follow-up prompt")
	}
	if !strings.Contains(updated.input, "Output of bg shell "+res.SessionID+":") {
		t.Fatalf("expected inline shell transcript pasted into input, got %q", updated.input)
	}
	if !strings.Contains(updated.input, "queued-inline") {
		t.Fatalf("expected pasted shell transcript content in input, got %q", updated.input)
	}
	if len(updated.queued) != 1 || updated.queued[0] != "summarize this" {
		t.Fatalf("expected follow-up prompt to remain queued after inline paste, got %+v", updated.queued)
	}
	plain := stripANSIAndTrimRight(updated.view.OngoingSnapshot())
	if strings.Contains(plain, "summarize this") {
		t.Fatalf("did not expect follow-up prompt submitted without pasted transcript, got %q", plain)
	}
}

func TestBusyQueuedReviewSlashCommandStartsFreshSessionAfterTurn(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIConversationFreshness(session.ConversationFreshnessEstablished),
	).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.input = "/review internal/app"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated := next.(*uiModel)
	if len(updated.queued) != 1 || updated.queued[0] != "/review internal/app" {
		t.Fatalf("expected queued /review command, got %+v", updated.queued)
	}

	next, cmd := updated.Update(submitDoneMsg{message: "done"})
	updated = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected quit cmd for queued /review handoff")
	}
	if updated.Action() != UIActionNewSession {
		t.Fatalf("expected UIActionNewSession, got %q", updated.Action())
	}
	if strings.TrimSpace(updated.nextSessionInitialPrompt) == "" {
		t.Fatal("expected queued /review to populate the next-session prompt")
	}
	if !strings.Contains(updated.nextSessionInitialPrompt, "internal/app") {
		t.Fatalf("expected queued /review args in handoff payload, got %q", updated.nextSessionInitialPrompt)
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected queued /review drained, got %+v", updated.queued)
	}
}

func TestQueuedReviewUsesEngineConversationFreshnessWhenUIDidNotReceiveRuntimeUpdateYet(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, &runtimeAdapterFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.input = "/review internal/app"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated := next.(*uiModel)
	if len(updated.queued) != 1 || updated.queued[0] != "/review internal/app" {
		t.Fatalf("expected queued /review command, got %+v", updated.queued)
	}
	if updated.conversationFreshness != session.ConversationFreshnessFresh {
		t.Fatalf("expected UI freshness to remain fresh before runtime sync, got %v", updated.conversationFreshness)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleUser, Content: "first prompt"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if got := eng.ConversationFreshness(); got != session.ConversationFreshnessEstablished {
		t.Fatalf("expected engine freshness established after first prompt, got %v", got)
	}

	next, cmd := updated.Update(submitDoneMsg{message: "done"})
	updated = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected quit cmd for queued /review handoff")
	}
	if updated.Action() != UIActionNewSession {
		t.Fatalf("expected UIActionNewSession, got %q", updated.Action())
	}
	if strings.TrimSpace(updated.nextSessionInitialPrompt) == "" {
		t.Fatal("expected queued /review to populate the next-session prompt")
	}
	if updated.conversationFreshness != session.ConversationFreshnessEstablished {
		t.Fatalf("expected UI freshness synced from engine during drain, got %v", updated.conversationFreshness)
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
	plain := stripANSIAndTrimRight(updated.view.OngoingSnapshot())
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
	plain := stripANSIAndTrimRight(updated.view.OngoingSnapshot())
	if strings.Contains(plain, "/prompt:review") {
		t.Fatalf("expected command text to be replaced by file prompt content, got %q", plain)
	}
	if !strings.Contains(plain, "review") || !strings.Contains(plain, "exact body") {
		t.Fatalf("expected file prompt content in transcript, got %q", plain)
	}
}

func TestBuiltInReviewSlashCommandSubmitsInjectedUserPrompt(t *testing.T) {
	r := commands.NewDefaultRegistry()
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUICommandRegistry(r),
	).(*uiModel)
	m.input = "/review internal/app"
	if got := r.Execute("/review internal/app"); !got.Handled || !got.SubmitUser {
		t.Fatalf("expected /review command to submit injected user prompt, got %+v", got)
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected submission cmd for /review")
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
}

func TestBuiltInInitSlashCommandSubmitsInjectedUserPrompt(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/init starter repo"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected submission cmd for /init")
	}
	if updated.Action() != UIActionNone {
		t.Fatalf("expected no session transition for empty-session /init, got %q", updated.Action())
	}
	if !updated.busy {
		t.Fatal("expected /init to submit in place for an empty session")
	}
	if updated.nextSessionInitialPrompt != "" {
		t.Fatalf("expected no handoff payload for empty-session /init, got %q", updated.nextSessionInitialPrompt)
	}
}

func TestBuiltInReviewSlashCommandStartsFreshSessionWhenCurrentSessionHasVisibleUserPrompt(t *testing.T) {
	r := commands.NewDefaultRegistry()
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUICommandRegistry(r),
		WithUIConversationFreshness(session.ConversationFreshnessEstablished),
	).(*uiModel)
	m.input = "/review internal/app"
	expected := r.Execute("/review internal/app")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected quit cmd for non-empty-session /review handoff")
	}
	if updated.Action() != UIActionNewSession {
		t.Fatalf("expected UIActionNewSession, got %q", updated.Action())
	}
	if updated.nextSessionInitialPrompt != expected.User {
		t.Fatalf("expected handoff payload to match /review command output\nwant: %q\n got: %q", expected.User, updated.nextSessionInitialPrompt)
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

func TestSlashFastTogglesAndShowsStatus(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIFastModeAvailable(true),
	).(*uiModel)
	m.termWidth = 100
	m.termHeight = 24
	m.windowSizeKnown = true
	m.syncViewport()
	m.input = "/fast"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status clear timer cmd")
	}
	if !updated.fastModeEnabled {
		t.Fatal("expected fast mode enabled after toggle")
	}
	if !strings.Contains(updated.transientStatus, "Fast mode enabled") {
		t.Fatalf("expected transient status for /fast toggle, got %q", updated.transientStatus)
	}
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "Fast mode enabled") {
		t.Fatalf("expected transcript notice for /fast toggle, got %q", plain)
	}

	updated.input = "/fast off"
	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status clear timer cmd")
	}
	if updated.fastModeEnabled {
		t.Fatal("expected fast mode disabled")
	}
	if !strings.Contains(updated.transientStatus, "Fast mode disabled") {
		t.Fatalf("expected disable transient status, got %q", updated.transientStatus)
	}

	updated.input = "/fast status"
	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if cmd != nil {
		t.Fatal("did not expect transient status cmd for /fast status")
	}
	plain = stripANSIAndTrimRight(updated.view.OngoingSnapshot())
	if !strings.Contains(plain, "Fast mode is off") {
		t.Fatalf("expected status transcript entry, got %q", plain)
	}
	if updated.transientStatus != "Fast mode disabled" {
		t.Fatalf("did not expect /fast status to overwrite transient status, got %q", updated.transientStatus)
	}
}

func TestSlashFastUnavailableShowsError(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 100
	m.termHeight = 24
	m.windowSizeKnown = true
	m.syncViewport()
	m.input = "/fast on"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status clear timer cmd")
	}
	if updated.fastModeEnabled {
		t.Fatal("did not expect fast mode enabled")
	}
	if !strings.Contains(updated.transientStatus, "OpenAI-based Responses providers") {
		t.Fatalf("expected availability error status, got %q", updated.transientStatus)
	}
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "Fast mode is only available for OpenAI-based Responses providers") {
		t.Fatalf("expected transcript error for unavailable fast mode, got %q", plain)
	}
}

func TestSlashFastWithEngineTogglesRuntime(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, statusLineFastClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5.3-codex"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/fast on"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status clear timer cmd")
	}
	if !eng.FastModeEnabled() {
		t.Fatal("expected runtime fast mode enabled")
	}
	if !updated.fastModeEnabled {
		t.Fatal("expected ui fast mode enabled")
	}

	updated.input = "/fast off"
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if eng.FastModeEnabled() {
		t.Fatal("expected runtime fast mode disabled")
	}
	if updated.fastModeEnabled {
		t.Fatal("expected ui fast mode disabled")
	}
}

func TestSlashSupervisorTogglesReviewerInvocationAndShowsStatus(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 100
	m.termHeight = 24
	m.windowSizeKnown = true
	m.syncViewport()
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

func TestBusySlashSupervisorOffAppliesToInFlightRunCompletion(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	mainClient := &busyToggleFakeClient{
		delay: 80 * time.Millisecond,
		responses: []llm.Response{{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		}},
	}
	reviewerClient := &busyToggleFakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":[]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	eng, err := runtime.New(store, mainClient, tools.NewRegistry(), runtime.Config{
		Model: "gpt-5",
		Reviewer: runtime.ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        reviewerClient,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning

	submitDone := make(chan error, 1)
	go func() {
		_, submitErr := eng.SubmitUserMessage(context.Background(), "hello")
		submitDone <- submitErr
	}()
	time.Sleep(10 * time.Millisecond)

	m.input = "/supervisor off"
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if updated.reviewerEnabled || updated.reviewerMode != "off" {
		t.Fatalf("expected ui reviewer disabled after /supervisor off, got enabled=%v mode=%q", updated.reviewerEnabled, updated.reviewerMode)
	}
	if got := eng.ReviewerFrequency(); got != "off" {
		t.Fatalf("expected runtime reviewer mode off, got %q", got)
	}

	if err := <-submitDone; err != nil {
		t.Fatalf("submit user message: %v", err)
	}
	if got := reviewerClient.CallCount(); got != 0 {
		t.Fatalf("expected no reviewer call for in-flight run after /supervisor off, got %d", got)
	}
}

func TestBusySlashSupervisorOnAppliesToInFlightRunCompletion(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	mainClient := &busyToggleFakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{ID: "call_patch_1", Name: string(tools.ToolPatch), Input: json.RawMessage(`{"patch":"*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"}`)}},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	reviewerClient := &busyToggleFakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":[]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	eng, err := runtime.New(store, mainClient, tools.NewRegistry(busyTogglePatchTool{delay: 80 * time.Millisecond}), runtime.Config{
		Model: "gpt-5",
		Reviewer: runtime.ReviewerConfig{
			Frequency:     "off",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        reviewerClient,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning

	submitDone := make(chan error, 1)
	go func() {
		_, submitErr := eng.SubmitUserMessage(context.Background(), "edit file")
		submitDone <- submitErr
	}()
	time.Sleep(10 * time.Millisecond)

	m.input = "/supervisor on"
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if !updated.reviewerEnabled || updated.reviewerMode != "edits" {
		t.Fatalf("expected ui reviewer enabled in edits mode after /supervisor on, got enabled=%v mode=%q", updated.reviewerEnabled, updated.reviewerMode)
	}
	if got := eng.ReviewerFrequency(); got != "edits" {
		t.Fatalf("expected runtime reviewer mode edits, got %q", got)
	}

	if err := <-submitDone; err != nil {
		t.Fatalf("submit user message: %v", err)
	}
	if got := reviewerClient.CallCount(); got != 1 {
		t.Fatalf("expected reviewer call for in-flight run after /supervisor on, got %d", got)
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
			Frequency:     "off",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        statusLineFakeClient{},
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

func TestSlashAutoCompactionTogglesAndShowsStatus(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 100
	m.termHeight = 24
	m.windowSizeKnown = true
	m.syncViewport()
	m.input = "/autocompaction"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status clear timer cmd")
	}
	if updated.autoCompactionEnabled {
		t.Fatal("expected auto-compaction disabled after toggle")
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after /autocompaction, got %q", updated.input)
	}
	if !strings.Contains(updated.transientStatus, "Auto-compaction disabled") {
		t.Fatalf("expected transient status for /autocompaction toggle, got %q", updated.transientStatus)
	}
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "Auto-compaction disabled") {
		t.Fatalf("expected transcript notice for /autocompaction toggle, got %q", plain)
	}

	updated.input = "/autocompaction on"
	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status clear timer cmd")
	}
	if !updated.autoCompactionEnabled {
		t.Fatal("expected auto-compaction enabled")
	}
	if !strings.Contains(updated.transientStatus, "Auto-compaction enabled") {
		t.Fatalf("expected enable transient status, got %q", updated.transientStatus)
	}
}

func TestBusySlashAutoCompactionExecutesImmediatelyWithoutQueueing(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.input = "/autocompaction off"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status clear timer cmd")
	}
	if !updated.busy {
		t.Fatal("expected busy state unchanged while command executes")
	}
	if updated.autoCompactionEnabled {
		t.Fatal("expected auto-compaction disabled")
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected no queued messages, got %d", len(updated.queued))
	}
	if len(updated.pendingInjected) != 0 {
		t.Fatalf("expected no pending injected messages, got %d", len(updated.pendingInjected))
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared after /autocompaction, got %q", updated.input)
	}
}

func TestSlashAutoCompactionWithEngineTogglesRuntime(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/autocompaction off"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected transient status clear timer cmd")
	}
	if got := eng.AutoCompactionEnabled(); got {
		t.Fatalf("expected runtime auto-compaction disabled, got %v", got)
	}
	if updated.autoCompactionEnabled {
		t.Fatal("expected ui auto-compaction disabled")
	}

	updated.input = "/autocompaction on"
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if got := eng.AutoCompactionEnabled(); !got {
		t.Fatalf("expected runtime auto-compaction enabled, got %v", got)
	}
	if !updated.autoCompactionEnabled {
		t.Fatal("expected ui auto-compaction enabled")
	}
}

func TestSlashAutoCompactionShowsCompactionModeNoneNote(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5", CompactionMode: "none"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "/autocompaction on"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if !strings.Contains(updated.transientStatus, "compaction_mode=none") {
		t.Fatalf("expected compaction_mode=none note in status, got %q", updated.transientStatus)
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

	ongoing := stripANSIAndTrimRight(m.view.OngoingSnapshot())
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
	plain := stripANSIAndTrimRight(m.view.OngoingSnapshot())
	if !strings.Contains(plain, "run review") {
		t.Fatalf("expected startup prompt in transcript, got %q", plain)
	}
}

func TestReviewerStatusEndToEnd_VerboseSuggestionsIssuedAndStatusConcise(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.AppendLocalEntryWithOngoingText("reviewer_suggestions", "Supervisor suggested:\n1. First detailed suggestion text\n2. Second detailed suggestion text", "Supervisor suggested:\n1. First detailed suggestion text\n2. Second detailed suggestion text")
	eng.AppendLocalEntry("reviewer_status", "Supervisor ran: 2 suggestions, no changes applied.")

	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent), WithUITheme("dark")).(*uiModel)
	m.termWidth = 100
	m.termHeight = 24

	rawOngoing := m.view.OngoingSnapshot()
	ongoing := stripANSIAndTrimRight(rawOngoing)
	if !containsInOrder(ongoing, "Supervisor suggested:", "1. First detailed suggestion text", "2. Second detailed suggestion text") {
		t.Fatalf("expected verbose reviewer suggestions in ongoing mode, got %q", ongoing)
	}
	if !strings.Contains(ongoing, "Supervisor ran: 2 suggestions, no changes applied.") {
		t.Fatalf("expected short reviewer status in ongoing mode, got %q", ongoing)
	}
	if strings.Count(ongoing, "Supervisor suggested:") != 1 {
		t.Fatalf("expected reviewer suggestions details only at issuance time in ongoing mode, got %q", ongoing)
	}
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("#98C379"))
	if !strings.Contains(rawOngoing, green.Render("Supervisor suggested:")) {
		t.Fatalf("expected reviewer suggestions to use success styling in ongoing mode, got %q", rawOngoing)
	}
	if !strings.Contains(rawOngoing, green.Render("Supervisor ran: 2 suggestions, no changes applied.")) {
		t.Fatalf("expected reviewer status to use success styling in ongoing mode, got %q", rawOngoing)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	rawDetail := next.(*uiModel).View()
	detail := stripANSIAndTrimRight(rawDetail)
	if !containsInOrder(detail, "Supervisor suggested:", "1. First detailed suggestion text", "2. Second detailed suggestion text", "Supervisor ran: 2 suggestions, no changes applied.") {
		t.Fatalf("expected full reviewer suggestions in detail mode, got %q", detail)
	}
	if !strings.Contains(rawDetail, green.Render("Supervisor suggested:")) {
		t.Fatalf("expected reviewer suggestions to use success styling in detail mode, got %q", rawDetail)
	}
	if !strings.Contains(rawDetail, green.Render("Supervisor ran: 2 suggestions, no changes applied.")) {
		t.Fatalf("expected reviewer status to use success styling in detail mode, got %q", rawDetail)
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
		WithUIModelName("gpt-5.3-codex"),
		WithUIThinkingLevel("high"),
	).(*uiModel)

	line := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(line, "gpt-5.3-codex high") {
		t.Fatalf("expected status line to include model and thinking level, got %q", line)
	}
}

func TestStatusLineShowsFastAfterThinkingLevelWhenAvailableAndEnabled(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIModelName("gpt-5.3-codex"),
		WithUIThinkingLevel("high"),
		WithUIFastModeAvailable(true),
		WithUIFastModeEnabled(true),
	).(*uiModel)

	line := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(line, "gpt-5.3-codex high fast") {
		t.Fatalf("expected status line to include fast marker, got %q", line)
	}
}

func TestStatusLineOmitsFastWhenUnavailable(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIModelName("gpt-5.3-codex"),
		WithUIThinkingLevel("high"),
		WithUIFastModeEnabled(true),
	).(*uiModel)

	line := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if strings.Contains(line, " high fast") {
		t.Fatalf("did not expect fast marker when unavailable, got %q", line)
	}
}

func TestStatusLineRightAlignsTransientNotice(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIModelName("gpt-5"),
	).(*uiModel)
	m.setTransientStatusWithKind("done", uiStatusNoticeSuccess)

	line := stripANSIPreserve(m.renderStatusLine(80, uiThemeStyles("dark")))
	if !strings.HasSuffix(strings.TrimRight(line, " "), "done") {
		t.Fatalf("expected notice at right edge, got %q", line)
	}
	if !containsInOrder(line, "ongoing", "gpt-5", "done") {
		t.Fatalf("expected notice after left metadata, got %q", line)
	}
	parts := strings.SplitN(line, "done", 2)
	if len(parts) < 2 || !strings.Contains(parts[0], "   ") {
		t.Fatalf("expected visible padding before right-aligned notice, got %q", line)
	}
}

func TestStatusLineTruncatesRightNoticeWithoutPushingOutContextUsage(t *testing.T) {
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
	m.setTransientStatusWithKind("this is a very long background completion notice that should truncate before it reaches the context progress bar", uiStatusNoticeSuccess)

	line := stripANSIAndTrimRight(m.renderStatusLine(70, uiThemeStyles("dark")))
	if !strings.Contains(line, "0%") {
		t.Fatalf("expected context usage label to remain visible, got %q", line)
	}
	if !strings.Contains(line, "▯") {
		t.Fatalf("expected context progress bar to remain visible, got %q", line)
	}
	if !strings.Contains(line, "…") {
		t.Fatalf("expected truncated notice ellipsis, got %q", line)
	}
	if strings.Contains(line, "this is a very long background completion notice that should truncate before it reaches the context progress bar") {
		t.Fatalf("expected long notice to be truncated, got %q", line)
	}
}

func TestStatusLineRendersReasoningHeaderBeforeContextUsage(t *testing.T) {
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
	m.reasoningStatusHeader = "Summarizing fix and investigation"

	line := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !containsInOrder(line, "Summarizing fix and investigation", "0%") {
		t.Fatalf("expected reasoning header immediately left of context usage, got %q", line)
	}
	if strings.Contains(line, m.statusHelpHint()) {
		t.Fatalf("did not expect help hint while reasoning header is present, got %q", line)
	}
}

func TestStatusLineShowsHelpHintWhenIdleInOngoingMode(t *testing.T) {
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
	if !containsInOrder(line, m.statusHelpHint(), "0%") {
		t.Fatalf("expected help hint immediately left of context usage while idle, got %q", line)
	}
}

func TestStatusLineFallsBackToF1HelpHintWhenQuestionMarkWouldType(t *testing.T) {
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
	m.input = "draft"

	line := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !containsInOrder(line, m.statusHelpHint(), "0%") {
		t.Fatalf("expected f1-only help hint immediately left of context usage while typing, got %q", line)
	}
	if strings.Contains(line, "F1 or ? for help") {
		t.Fatalf("did not expect question mark help hint while typing, got %q", line)
	}
}

func TestStatusLineHidesHelpHintWhenOngoingModeIsNotIdle(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5", ContextWindowTokens: 400_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	cases := []struct {
		name  string
		apply func(*uiModel)
	}{
		{name: "busy running", apply: func(m *uiModel) {
			m.busy = true
			m.activity = uiActivityRunning
		}},
		{name: "queued", apply: func(m *uiModel) {
			m.activity = uiActivityQueued
		}},
		{name: "question", apply: func(m *uiModel) {
			m.activity = uiActivityQuestion
		}},
		{name: "reviewer", apply: func(m *uiModel) {
			m.reviewerRunning = true
		}},
		{name: "compacting", apply: func(m *uiModel) {
			m.compacting = true
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
			tc.apply(m)

			line := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
			if strings.Contains(line, m.statusHelpHint()) || strings.Contains(line, "F1 or ? for help") || strings.Contains(line, "F1 for help") {
				t.Fatalf("did not expect help hint while ongoing mode is active but not idle, got %q", line)
			}
		})
	}
}

func TestStatusLineHidesHelpHintOutsideOngoingMode(t *testing.T) {
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
	m.forwardToView(tui.ToggleModeMsg{})

	line := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if strings.Contains(line, m.statusHelpHint()) || strings.Contains(line, "F1 or ? for help") || strings.Contains(line, "F1 for help") {
		t.Fatalf("did not expect help hint outside ongoing mode, got %q", line)
	}
}

func TestStatusLineShowsThinkingLevelForUnknownModels(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIModelName("claude-3-7-sonnet"),
		WithUIThinkingLevel("high"),
	).(*uiModel)

	line := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(line, "claude-3-7-sonnet high") {
		t.Fatalf("expected status line to include thinking level for unknown model, got %q", line)
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
		WithUIModelName("gpt-5.3-codex"),
		WithUIThinkingLevel("high"),
		WithUIModelContractLocked(true),
	).(*uiModel)

	line := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(line, "gpt-5.3-codex high (model locked)") {
		t.Fatalf("expected status line to include locked model contract marker, got %q", line)
	}
}

func TestStatusLineHidesLockedModelMarkerWhenConfiguredModelMatches(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIModelName("gpt-5.3-codex"),
		WithUIConfiguredModelName("gpt-5.3-codex"),
		WithUIThinkingLevel("high"),
		WithUIFastModeAvailable(true),
		WithUIFastModeEnabled(true),
		WithUIModelContractLocked(true),
	).(*uiModel)

	line := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(line, "gpt-5.3-codex high fast") {
		t.Fatalf("expected status line to keep model label, got %q", line)
	}
	if strings.Contains(line, "(model locked)") {
		t.Fatalf("did not expect locked model contract marker when configured model matches, got %q", line)
	}
}

func TestStatusLineShowsLockedModelMarkerWhenConfiguredModelDiffers(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIModelName("gpt-5.3-codex"),
		WithUIConfiguredModelName("gpt-5.4"),
		WithUIThinkingLevel("low"),
		WithUIModelContractLocked(true),
	).(*uiModel)

	line := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(line, "gpt-5.3-codex low (model locked)") {
		t.Fatalf("expected status line to include locked model contract marker when configured model differs, got %q", line)
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
	if updated.inputSubmitLocked {
		t.Fatal("did not expect submit lock while waiting for reviewer steering flush")
	}
	if updated.input != "" {
		t.Fatalf("expected input cleared immediately after queueing reviewer steering, got %q", updated.input)
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

type statusLineFastClient struct{}

type statusLineAzureClient struct{}

type busyToggleFakeClient struct {
	mu        sync.Mutex
	responses []llm.Response
	calls     int
	delay     time.Duration
}

type requestCaptureFakeClient struct {
	mu        sync.Mutex
	responses []llm.Response
	requests  []llm.Request
}

func (f *busyToggleFakeClient) Generate(ctx context.Context, _ llm.Request) (llm.Response, error) {
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return llm.Response{}, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if len(f.responses) == 0 {
		return llm.Response{}, errors.New("no fake response configured")
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func (f *busyToggleFakeClient) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *requestCaptureFakeClient) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if len(f.responses) == 0 {
		return llm.Response{}, errors.New("no fake response configured")
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func (f *requestCaptureFakeClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return llm.ProviderCapabilities{
		ProviderID:                    "openai",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      true,
		SupportsReasoningEncrypted:    true,
		SupportsServerSideContextEdit: true,
		IsOpenAIFirstParty:            true,
	}, nil
}

func (f *requestCaptureFakeClient) Requests() []llm.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]llm.Request, len(f.requests))
	copy(out, f.requests)
	return out
}

type busyTogglePatchTool struct {
	delay time.Duration
}

func (t busyTogglePatchTool) Name() tools.ID {
	return tools.ToolPatch
}

func (t busyTogglePatchTool) Call(ctx context.Context, c tools.Call) (tools.Result, error) {
	if t.delay > 0 {
		select {
		case <-ctx.Done():
			return tools.Result{}, ctx.Err()
		case <-time.After(t.delay):
		}
	}
	return tools.Result{CallID: c.ID, Name: c.Name, Output: json.RawMessage(`{"ok":true}`)}, nil
}

func (statusLineFakeClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
}

func (statusLineFastClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
}

func (statusLineFastClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}, nil
}

func (statusLineAzureClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
}

func (statusLineAzureClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return llm.ProviderCapabilities{ProviderID: "azure-openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: false}, nil
}

func TestHelpToggleRendersBelowQueuedMessagesAndInput(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 120
	m.termHeight = 40
	m.windowSizeKnown = true
	m.queued = []string{"queued latest"}
	m.input = "draft"
	m.syncViewport()

	next, _ := m.Update(customKeyMsg{Kind: customKeyHelp})
	updated := next.(*uiModel)

	if !updated.helpVisible {
		t.Fatal("expected help pane visible")
	}
	plain := stripANSIAndTrimRight(updated.View())
	if !containsInOrder(plain, "queued latest", "Global", "› draft") {
		t.Fatalf("expected help above input and below queue, got %q", plain)
	}
	if !containsInOrder(plain, "Global\nF1 | ? (empty prompt) | Alt + / | Cmd + /", "\n\nTranscript\nPgUp", "\n\nPrompt Input\n$ <command>", "\n\nRollback Mode\nEsc | Esc") {
		t.Fatalf("expected blank line between visible help sections, got %q", plain)
	}
	if !containsInOrder(plain, "Alt + ←, → | Ctrl + ←, →", "move the cursor by word") {
		t.Fatalf("expected exact main-input arrow binding label, got %q", plain)
	}
	if !containsInOrder(plain, "$ <command>", "execute a shell command and show output to the model") {
		t.Fatalf("expected direct shell command help row, got %q", plain)
	}
	if !containsInOrder(plain, "↑, ↓", "move the rollback selection") {
		t.Fatalf("expected exact rollback arrow binding label, got %q", plain)
	}
	if !strings.Contains(plain, "cancel or go back") {
		t.Fatalf("expected rollback escape copy in help pane, got %q", plain)
	}
	if !strings.Contains(plain, "F1") || !strings.Contains(plain, "? (empty prompt)") || !strings.Contains(plain, "Alt + /") || !strings.Contains(plain, "Cmd + /") || !strings.Contains(plain, "Shift + Tab") {
		t.Fatalf("expected indexed shortcuts in help pane, got %q", plain)
	}
	for _, unwanted := range []string{"Keyboard Help", "Press any registered key to dismiss", "Slash Commands", "Ask Prompt", "Process List", "Printable keys", "start editing from the selected rollback point", "fork from the selected rollback point", "close rollback selection", "return to rollback selection when the edit prompt is empty"} {
		if strings.Contains(plain, unwanted) {
			t.Fatalf("did not expect %q in help pane, got %q", unwanted, plain)
		}
	}
}

func TestHelpRollbackModeRowsUseActiveStyles(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.helpVisible = true
	m.windowSizeKnown = true
	width := 180
	style := uiThemeStyles(m.theme)
	lines := m.layout().renderHelpPane(width, 80, style)
	sections := m.helpSections()
	keyColumnWidth := helpKeyColumnWidth(sections, width)
	activeKeyStyle := lipgloss.NewStyle().Foreground(uiPalette(m.theme).primary).Bold(true)
	activeDescStyle := style.input

	expectedLines := []string{
		padANSIRight(activeKeyStyle.Render(padRight("Esc | Esc", keyColumnWidth))+" "+activeDescStyle.Render("open rollback selection from an idle empty prompt"), width),
		padANSIRight(activeKeyStyle.Render(padRight("↑, ↓", keyColumnWidth))+" "+activeDescStyle.Render("move the rollback selection"), width),
		padANSIRight(activeKeyStyle.Render(padRight("PgUp | PgDn", keyColumnWidth))+" "+activeDescStyle.Render("scroll the transcript while selecting a rollback point"), width),
		padANSIRight(activeKeyStyle.Render(padRight("Esc", keyColumnWidth))+" "+activeDescStyle.Render("cancel or go back"), width),
	}

	joined := strings.Join(lines, "\n")
	for _, expected := range expectedLines {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected active-styled rollback help row %q in %q", stripANSIAndTrimRight(expected), joined)
		}
	}
}

func TestHelpDismissesOnRegisteredKeyAndAppliesAction(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 24
	m.windowSizeKnown = true
	m.syncViewport()

	next, _ := m.Update(customKeyMsg{Kind: customKeyHelp})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	updated = next.(*uiModel)

	if updated.helpVisible {
		t.Fatal("expected help dismissed by registered key")
	}
	if updated.input != "x" {
		t.Fatalf("expected keypress to keep its normal behavior, got %q", updated.input)
	}
}

func TestHelpDismissesOnAnyKeypress(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 24
	m.windowSizeKnown = true
	testSetActiveAsk(m, &askEvent{req: askquestion.Request{Question: "Proceed?", Suggestions: []string{"Yes", "No"}}})
	m.syncViewport()

	next, _ := m.Update(customKeyMsg{Kind: customKeyHelp})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	updated = next.(*uiModel)

	if updated.helpVisible {
		t.Fatal("expected any keypress to dismiss help")
	}
	if testAskFreeform(updated) {
		t.Fatal("did not expect plain rune key to alter ask prompt state")
	}
}

func TestQuestionMarkTogglesHelpWhenInputIsEmpty(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 24
	m.windowSizeKnown = true
	m.syncViewport()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	updated := next.(*uiModel)

	if !updated.helpVisible {
		t.Fatal("expected ? to open help from an empty prompt")
	}
}

func TestQuestionMarkInsertsLiteralWhenInputIsNotEmpty(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 24
	m.windowSizeKnown = true
	m.input = "draft"
	m.inputCursor = len([]rune(m.input))
	m.syncViewport()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	updated := next.(*uiModel)

	if updated.helpVisible {
		t.Fatal("did not expect ? to open help while a draft is present")
	}
	if updated.input != "draft?" {
		t.Fatalf("expected ? to be inserted into the draft, got %q", updated.input)
	}
}

func TestAltQuestionMarkTogglesHelp(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 24
	m.windowSizeKnown = true
	m.syncViewport()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}, Alt: true})
	updated := next.(*uiModel)

	if !updated.helpVisible {
		t.Fatal("expected alt+? to open help")
	}
}

func TestF1TogglesHelp(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 24
	m.windowSizeKnown = true
	m.syncViewport()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyF1})
	updated := next.(*uiModel)

	if !updated.helpVisible {
		t.Fatal("expected f1 to open help")
	}
}

func TestAltSlashTogglesHelp(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 24
	m.windowSizeKnown = true
	m.syncViewport()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}, Alt: true})
	updated := next.(*uiModel)

	if !updated.helpVisible {
		t.Fatal("expected alt+/ to open help")
	}
}

func TestHelpToggleClearsRollbackEscArming(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 24
	m.windowSizeKnown = true
	m.syncViewport()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := next.(*uiModel)
	if updated.lastEscAt.IsZero() {
		t.Fatal("expected first esc to arm rollback window")
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}, Alt: true})
	updated = next.(*uiModel)
	if !updated.helpVisible {
		t.Fatal("expected alt+/ to open help")
	}
	if !updated.lastEscAt.IsZero() {
		t.Fatal("expected help toggle to clear rollback esc arming")
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if updated.helpVisible {
		t.Fatal("expected esc to dismiss help")
	}
	if testRollbackSelecting(updated) {
		t.Fatal("did not expect esc after help toggle to open rollback selection")
	}
	if updated.lastEscAt.IsZero() {
		t.Fatal("expected esc after help toggle to start a fresh rollback arming window")
	}
}

func TestCmdSlashCSIUTogglesHelp(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 24
	m.windowSizeKnown = true
	m.syncViewport()

	next, _ := m.Update(adaptCustomKeyMsg(testBubbleTeaUnknownCSISequence("\x1b[47;10u")))
	updated := next.(*uiModel)

	if !updated.helpVisible {
		t.Fatal("expected cmd+/ CSI-u sequence to open help")
	}
}

func TestHelpToggleKeyHidesVisibleHelp(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 24
	m.windowSizeKnown = true
	m.syncViewport()

	next, _ := m.Update(customKeyMsg{Kind: customKeyHelp})
	updated := next.(*uiModel)
	next, _ = updated.Update(customKeyMsg{Kind: customKeyHelp})
	updated = next.(*uiModel)

	if updated.helpVisible {
		t.Fatal("expected help toggle key to hide visible help")
	}
}

func TestHelpToggleIgnoredInDetailMode(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 24
	m.windowSizeKnown = true
	m.forwardToView(tui.ToggleModeMsg{})
	m.syncViewport()

	next, _ := m.Update(customKeyMsg{Kind: customKeyHelp})
	updated := next.(*uiModel)

	if updated.helpVisible {
		t.Fatal("did not expect help to open in detail mode")
	}
}

func TestTranscriptToggleClosesVisibleHelp(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 24
	m.windowSizeKnown = true
	m.syncViewport()

	next, _ := m.Update(customKeyMsg{Kind: customKeyHelp})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	updated = next.(*uiModel)

	if updated.helpVisible {
		t.Fatal("expected transcript toggle to hide help")
	}
	if updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected detail mode after transcript toggle, got %q", updated.view.Mode())
	}
}

func TestHelpRollbackSelectionDismissesAndMovesSelection(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 24
	m.windowSizeKnown = true
	m.transcriptEntries = []tui.TranscriptEntry{{Role: "user", Text: "one"}, {Role: "assistant", Text: "a"}, {Role: "user", Text: "two"}}
	if !m.startRollbackSelectionMode() {
		t.Fatal("expected rollback selection mode to start")
	}
	m.syncViewport()

	next, _ := m.Update(customKeyMsg{Kind: customKeyHelp})
	updated := next.(*uiModel)
	updated.rollback.selection = 0
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)

	if updated.helpVisible {
		t.Fatal("expected rollback selection key to dismiss help")
	}
	if testRollbackSelection(updated) != 1 {
		t.Fatalf("expected rollback selection to move, got %d", testRollbackSelection(updated))
	}
}

func TestHelpRollbackEditDismissesAndReturnsToSelection(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 24
	m.windowSizeKnown = true
	m.transcriptEntries = []tui.TranscriptEntry{{Role: "user", Text: "one"}, {Role: "assistant", Text: "a"}, {Role: "user", Text: "two"}}
	if !m.startRollbackSelectionMode() {
		t.Fatal("expected rollback selection mode to start")
	}
	if _, ok := m.beginRollbackEditing(); !ok {
		t.Fatal("expected rollback editing mode to start")
	}
	m.input = ""
	m.syncViewport()

	next, _ := m.Update(customKeyMsg{Kind: customKeyHelp})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)

	if updated.helpVisible {
		t.Fatal("expected rollback edit key to dismiss help")
	}
	if !testRollbackSelecting(updated) || testRollbackEditing(updated) {
		t.Fatalf("expected esc to return to rollback selection, rollbackMode=%t rollbackEditing=%t", testRollbackSelecting(updated), testRollbackEditing(updated))
	}
}

func TestLockedInputEditKeysDismissHelpAndStillNoOp(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 24
	m.windowSizeKnown = true
	m.inputSubmitLocked = true
	m.busy = true
	m.input = "locked"
	m.syncViewport()

	next, _ := m.Update(customKeyMsg{Kind: customKeyHelp})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	updated = next.(*uiModel)

	if updated.helpVisible {
		t.Fatal("expected any keypress to dismiss help")
	}
	if updated.input != "locked" {
		t.Fatalf("expected locked input unchanged, got %q", updated.input)
	}
}

func slashPickerContainsCommand(state slashCommandPickerState, name string) bool {
	for _, command := range state.matches {
		if command.Name == name {
			return true
		}
	}
	return false
}

func slashPickerCommandNames(state slashCommandPickerState) []string {
	names := make([]string, 0, len(state.matches))
	for _, command := range state.matches {
		names = append(names, command.Name)
	}
	return names
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
