package app

import (
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	"builder/server/tools/askquestion"
	"builder/shared/clientui"
	"bytes"
	"context"
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
)

func TestDebugKeysTransientStatusShowsNormalizationSource(t *testing.T) {
	t.Setenv("BUILDER_DEBUG_KEYS", "1")
	m := newProjectedStaticUIModel()

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
	m := newProjectedStaticUIModel()
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

func TestTransientStatusQueuePromotesNextNoticeAfterClear(t *testing.T) {
	m := newProjectedStaticUIModel()
	first := m.enqueueTransientStatus("first", uiStatusNoticeSuccess)
	second := m.enqueueTransientStatus("second", uiStatusNoticeError)

	if first == nil {
		t.Fatal("expected first notice clear command")
	}
	if second != nil {
		t.Fatalf("expected queued notice to wait for active clear, got %T", second())
	}
	if m.transientStatus != "first" {
		t.Fatalf("active notice = %q, want first", m.transientStatus)
	}

	next, cmd := m.Update(clearTransientStatusMsg{token: m.transientStatusToken})
	updated := next.(*uiModel)
	if updated.transientStatus != "second" {
		t.Fatalf("promoted notice = %q, want second", updated.transientStatus)
	}
	if updated.transientStatusKind != uiStatusNoticeError {
		t.Fatalf("promoted notice kind = %d, want error", updated.transientStatusKind)
	}
	if cmd == nil {
		t.Fatal("expected promoted notice clear command")
	}
}

func TestTransientStatusReplaceUpdatesActiveNoticeImmediately(t *testing.T) {
	m := newProjectedStaticUIModel()
	first := m.setTransientStatusWithKind("first", uiStatusNoticeSuccess)
	second := m.setTransientStatusWithKind("second", uiStatusNoticeError)

	if first == nil || second == nil {
		t.Fatal("expected replacement notices to schedule clear commands")
	}
	if m.transientStatus != "second" {
		t.Fatalf("active notice = %q, want second", m.transientStatus)
	}
	if m.transientStatusKind != uiStatusNoticeError {
		t.Fatalf("active notice kind = %d, want error", m.transientStatusKind)
	}
}

func TestStartupUpdateNoticeShowsAvailableVersionOnce(t *testing.T) {
	client := &runtimeControlFakeClient{
		status: clientui.RuntimeStatus{
			Update: clientui.UpdateStatus{Checked: true, Available: true, LatestVersion: "1.2.3"},
		},
		sessionView: clientui.RuntimeSessionView{SessionID: "session-1"},
	}
	m := newProjectedTestUIModel(client, nil, nil, WithUIStartupUpdateNotice(true))

	msg := m.startupUpdateNoticeCmd(client.status.Update)()
	next, cmd := m.Update(msg)
	updated := next.(*uiModel)
	if updated.transientStatus != "update available: 1.2.3" {
		t.Fatalf("startup update notice = %q", updated.transientStatus)
	}
	if updated.transientStatusKind != uiStatusNoticeUpdateAvailable {
		t.Fatalf("startup update notice kind = %d, want update available", updated.transientStatusKind)
	}
	if cmd == nil {
		t.Fatal("expected update notice clear command")
	}

	next, _ = updated.Update(startupUpdateNoticeMsg{version: "1.2.4"})
	updated = next.(*uiModel)
	if updated.transientStatus != "update available: 1.2.3" {
		t.Fatalf("expected duplicate startup update notice suppressed, got %q", updated.transientStatus)
	}
}

func TestStartupUpdateNoticeMarksShownOnlyAfterDisplay(t *testing.T) {
	m := newProjectedStaticUIModel()
	initialClear := m.setTransientStatusWithKind("busy", uiStatusNoticeNeutral)
	if initialClear == nil {
		t.Fatal("expected initial notice clear command")
	}

	next, cmd := m.Update(startupUpdateNoticeMsg{version: "1.2.3"})
	updated := next.(*uiModel)
	if cmd != nil {
		t.Fatalf("expected queued update notice to wait for active clear, got %T", cmd())
	}
	if updated.startupUpdateShown {
		t.Fatal("did not expect startup update notice marked shown while queued")
	}
	if updated.transientStatus != "busy" {
		t.Fatalf("active notice = %q, want busy", updated.transientStatus)
	}

	next, cmd = updated.Update(clearTransientStatusMsg{token: updated.transientStatusToken})
	updated = next.(*uiModel)
	if updated.transientStatus != "update available: 1.2.3" {
		t.Fatalf("promoted startup update notice = %q", updated.transientStatus)
	}
	if !updated.startupUpdateShown {
		t.Fatal("expected startup update notice marked shown after promotion")
	}
	if cmd == nil {
		t.Fatal("expected promoted update notice clear command")
	}
}

func TestMainInputSupportsInlineCursorEditing(t *testing.T) {
	m := newProjectedStaticUIModel()
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
	m := newProjectedStaticUIModel()
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
	m := newProjectedStaticUIModel()
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
	m := newProjectedStaticUIModel()
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
	m := newProjectedStaticUIModel(
		WithUIPromptHistory([]string{"first prompt", "second line\nthird line", "/resume"}),
	)

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
	m := newProjectedStaticUIModel(
		WithUIPromptHistory([]string{"hello"}),
	)
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
	m := newProjectedStaticUIModel(
		WithUIPromptHistory([]string{"hello"}),
	)
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
	m := newProjectedStaticUIModel(
		WithUIPromptHistory([]string{"one\ntwo\nthree", "older"}),
	)

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

	m := newProjectedStaticUIModel(
		WithUIPromptHistory([]string{"only prompt"}),
	)

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
	m := newProjectedStaticUIModel()
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

	m := newProjectedEngineUIModel(eng)
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

func TestRuntimeClientSubmitShowsUserMessageInTranscriptWhenFlushedEventArrives(t *testing.T) {
	client := &runtimeControlFakeClient{}
	m := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "say hi"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if updated.pendingPreSubmitText != "say hi" {
		t.Fatalf("expected pending pre-submit text preserved, got %q", updated.pendingPreSubmitText)
	}

	next, _ = updated.Update(preSubmitCompactionCheckDoneMsg{
		token:         updated.preSubmitCheckToken,
		text:          "say hi",
		shouldCompact: false,
	})
	updated = next.(*uiModel)

	cmd := updated.runtimeAdapter().handleProjectedRuntimeEvent(projectRuntimeEvent(runtime.Event{
		Kind:        runtime.EventUserMessageFlushed,
		StepID:      "step-1",
		UserMessage: "say hi",
	}))
	if got := len(updated.transcriptEntries); got != 1 {
		t.Fatalf("expected one transcript entry after flushed user message, got %d", got)
	}
	if updated.transcriptEntries[0].Role != "user" || updated.transcriptEntries[0].Text != "say hi" {
		t.Fatalf("unexpected transcript entry: %+v", updated.transcriptEntries[0])
	}
	if updated.transcriptEntries[0].Transient != true {
		t.Fatalf("expected runtime-backed flushed user message to stay transient until hydrate, got %+v", updated.transcriptEntries[0])
	}
	msgs := collectCmdMessages(t, cmd)
	refreshFound := false
	for _, msg := range msgs {
		if _, ok := msg.(runtimeTranscriptRefreshedMsg); ok {
			refreshFound = true
			break
		}
	}
	if refreshFound {
		t.Fatalf("did not expect flushed runtime user message to trigger transcript hydration, got %+v", msgs)
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

	m := newProjectedEngineUIModel(eng)
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

	m := newProjectedEngineUIModel(eng)
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

	m := newProjectedEngineUIModel(eng)
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

	m := newProjectedEngineUIModel(eng)
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

	m := newProjectedEngineUIModel(eng)
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
	m := newProjectedStaticUIModel()
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
	m := newProjectedStaticUIModel()
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
	m := newProjectedEngineUIModel(eng)
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
	m := newProjectedStaticUIModel()
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
	m := newProjectedStaticUIModel()
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
	m := newProjectedStaticUIModel()
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
	m := newProjectedStaticUIModel()
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
	m := newProjectedStaticUIModel()
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
