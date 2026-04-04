package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"builder/cli/tui"
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/transcript"
	tea "github.com/charmbracelet/bubbletea"
)

type runtimeAdapterFakeClient struct {
	responses []llm.Response
	index     int
}

type refreshingRuntimeClient struct {
	runtimeControlFakeClient
	views       []clientui.RuntimeMainView
	transcripts []clientui.TranscriptPage
	errs        []error
	calls       int
}

func (f *refreshingRuntimeClient) MainView() clientui.RuntimeMainView {
	if f.calls == 0 {
		return clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1"}}
	}
	idx := f.calls - 1
	if idx >= len(f.views) {
		idx = len(f.views) - 1
	}
	if idx < 0 {
		return clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1"}}
	}
	return f.views[idx]
}

func (f *refreshingRuntimeClient) RefreshMainView() (clientui.RuntimeMainView, error) {
	idx := f.calls
	view := clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1"}}
	if idx < len(f.views) {
		view = f.views[idx]
	} else if len(f.views) > 0 {
		view = f.views[len(f.views)-1]
	}
	return view, nil
}

func (f *refreshingRuntimeClient) Transcript() clientui.TranscriptPage {
	if f.calls == 0 {
		return clientui.TranscriptPage{SessionID: "session-1"}
	}
	idx := f.calls - 1
	if idx >= len(f.transcripts) {
		idx = len(f.transcripts) - 1
	}
	if idx < 0 {
		return clientui.TranscriptPage{SessionID: "session-1"}
	}
	return f.transcripts[idx]
}

func (f *refreshingRuntimeClient) RefreshTranscript() (clientui.TranscriptPage, error) {
	idx := f.calls
	f.calls++
	page := clientui.TranscriptPage{SessionID: "session-1"}
	if idx < len(f.transcripts) {
		page = f.transcripts[idx]
	} else if len(f.transcripts) > 0 {
		page = f.transcripts[len(f.transcripts)-1]
	}
	if idx < len(f.errs) && f.errs[idx] != nil {
		return page, f.errs[idx]
	}
	return page, nil
}

func (f *refreshingRuntimeClient) LoadTranscriptPage(req clientui.TranscriptPageRequest) (clientui.TranscriptPage, error) {
	_ = req
	return f.RefreshTranscript()
}

func (f *runtimeAdapterFakeClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	if f.index >= len(f.responses) {
		return llm.Response{}, errors.New("no fake response configured")
	}
	resp := f.responses[f.index]
	f.index++
	return resp, nil
}

func (f *runtimeAdapterFakeClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return llm.ProviderCapabilities{
		ProviderID:                    "openai",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      true,
		SupportsReasoningEncrypted:    true,
		SupportsServerSideContextEdit: true,
		IsOpenAIFirstParty:            true,
	}, nil
}

func TestApplyChatSnapshotSetsOngoingFromSnapshot(t *testing.T) {
	m := newProjectedStaticUIModel()

	_ = m.runtimeAdapter().applyChatSnapshot(runtime.ChatSnapshot{Ongoing: "hello"})

	if got := m.view.OngoingStreamingText(); got != "hello" {
		t.Fatalf("expected snapshot ongoing text, got %q", got)
	}
}

func TestRuntimeSessionViewUsesLocalFallbackWhenRuntimeClientMissing(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUISessionName("incident triage"),
		WithUISessionID("session-123"),
		WithUIConversationFreshness(session.ConversationFreshnessEstablished),
	)
	m.transcriptEntries = []tui.TranscriptEntry{{Role: "assistant", Text: "hello"}}
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries, Ongoing: "streaming"})

	view := m.runtimeSessionView()
	if view.SessionName != "incident triage" {
		t.Fatalf("session name = %q, want incident triage", view.SessionName)
	}
	if view.SessionID != "session-123" {
		t.Fatalf("session id = %q, want session-123", view.SessionID)
	}
	if view.ConversationFreshness != clientui.ConversationFreshnessEstablished {
		t.Fatalf("conversation freshness = %v, want established", view.ConversationFreshness)
	}
	if len(view.Chat.Entries) != 1 || view.Chat.Entries[0].Text != "hello" {
		t.Fatalf("unexpected fallback chat entries: %+v", view.Chat.Entries)
	}
	if view.Chat.Ongoing != "streaming" {
		t.Fatalf("ongoing = %q, want streaming", view.Chat.Ongoing)
	}
}

func TestSyncConversationFromEngineUsesBundledSessionViewMetadata(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.SetName("incident triage"); err != nil {
		t.Fatalf("set name: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "hello user"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: "hello", Phase: llm.MessagePhaseFinal}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	eng, err := runtime.New(store, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	m := newProjectedEngineUIModel(eng)
	m.sessionName = "stale"
	m.sessionID = "stale"

	if len(m.startupCmds) != 1 || m.startupCmds[0] == nil {
		t.Fatalf("expected startup sync command, got %d command(s)", len(m.startupCmds))
	}
	cmd := m.startupCmds[0]
	m.startupCmds = nil
	msg, ok := cmd().(runtimeTranscriptRefreshedMsg)
	if !ok {
		t.Fatalf("expected runtimeTranscriptRefreshedMsg, got %T", cmd())
	}
	next, followUp := m.Update(msg)
	_ = followUp
	m = next.(*uiModel)
	if m.sessionName != "incident triage" {
		t.Fatalf("session name = %q, want incident triage", m.sessionName)
	}
	if m.sessionID != store.Meta().SessionID {
		t.Fatalf("session id = %q, want %q", m.sessionID, store.Meta().SessionID)
	}
	if m.conversationFreshness != clientui.ConversationFreshnessEstablished {
		t.Fatalf("conversation freshness = %v, want established", m.conversationFreshness)
	}
	if got := m.view.OngoingSnapshot(); !strings.Contains(got, "hello") {
		t.Fatalf("expected synced conversation in view, got %q", got)
	}
}

func TestSyncConversationFromEngineRetriesAfterRefreshError(t *testing.T) {
	oldDelay := uiRuntimeHydrationRetryDelay
	uiRuntimeHydrationRetryDelay = 0
	defer func() { uiRuntimeHydrationRetryDelay = oldDelay }()

	client := &refreshingRuntimeClient{
		transcripts: []clientui.TranscriptPage{
			{SessionID: "session-1"},
			{SessionID: "session-1", SessionName: "incident triage", Entries: []clientui.ChatEntry{{Role: "assistant", Text: "final answer"}}, TotalEntries: 1},
		},
		errs: []error{errors.New("temporary refresh failure"), nil},
	}
	m := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())

	if len(m.startupCmds) != 1 || m.startupCmds[0] == nil {
		t.Fatalf("expected startup sync command, got %d command(s)", len(m.startupCmds))
	}
	firstCmd := m.startupCmds[0]
	m.startupCmds = nil
	firstMsg, ok := firstCmd().(runtimeTranscriptRefreshedMsg)
	if !ok {
		t.Fatalf("expected runtimeTranscriptRefreshedMsg, got %T", firstCmd())
	}
	next, retryCmd := m.Update(firstMsg)
	if retryCmd == nil {
		t.Fatal("expected retry command after refresh error")
	}
	retryMsg, ok := retryCmd().(runtimeTranscriptRetryMsg)
	if !ok {
		t.Fatalf("expected runtimeTranscriptRetryMsg, got %T", retryCmd())
	}
	next, secondCmd := next.(*uiModel).Update(retryMsg)
	if secondCmd == nil {
		t.Fatal("expected second sync command after retry tick")
	}
	secondMsg, ok := secondCmd().(runtimeTranscriptRefreshedMsg)
	if !ok {
		t.Fatalf("expected runtimeTranscriptRefreshedMsg, got %T", secondCmd())
	}
	next, followUp := next.(*uiModel).Update(secondMsg)
	_ = followUp
	updated := next.(*uiModel)
	if updated.sessionName != "incident triage" {
		t.Fatalf("session name = %q, want incident triage", updated.sessionName)
	}
	if got := stripANSIAndTrimRight(updated.view.OngoingSnapshot()); !strings.Contains(got, "final answer") {
		t.Fatalf("expected retried sync to hydrate transcript, got %q", got)
	}
	if client.calls != 2 {
		t.Fatalf("refresh call count = %d, want 2", client.calls)
	}
}

func TestApplyProjectedTranscriptPageMergesTailSliceWithoutDroppingExistingEntries(t *testing.T) {
	m := newProjectedStaticUIModel()
	seed := []tui.TranscriptEntry{
		{Role: "user", Text: "prompt"},
		{Role: "tool_call", Text: "pwd"},
		{Role: "assistant", Text: "**done**"},
	}
	m.transcriptEntries = append([]tui.TranscriptEntry(nil), seed...)
	m.forwardToView(tui.SetConversationMsg{Entries: seed})

	cmd := m.runtimeAdapter().applyProjectedTranscriptPage(clientui.TranscriptPage{
		SessionID:    "session-1",
		TotalEntries: 3,
		Offset:       2,
		Entries: []clientui.ChatEntry{{
			Role: "assistant",
			Text: "**done**",
		}},
	})
	if cmd != nil {
		_ = cmd()
	}

	plain := stripANSIAndTrimRight(m.view.OngoingSnapshot())
	if !strings.Contains(plain, "prompt") {
		t.Fatalf("expected merged transcript to keep earlier user entry, got %q", plain)
	}
	if !strings.Contains(plain, "pwd") {
		t.Fatalf("expected merged transcript to keep earlier tool call, got %q", plain)
	}
	if !strings.Contains(plain, "done") {
		t.Fatalf("expected merged transcript to keep tail entry, got %q", plain)
	}
}

func TestApplyProjectedTranscriptPageReplacesTranscriptWhenPageIsComplete(t *testing.T) {
	m := newProjectedStaticUIModel()
	seed := []tui.TranscriptEntry{{Role: "assistant", Text: "old"}}
	m.transcriptEntries = append([]tui.TranscriptEntry(nil), seed...)
	m.forwardToView(tui.SetConversationMsg{Entries: seed})

	cmd := m.runtimeAdapter().applyProjectedTranscriptPage(clientui.TranscriptPage{
		SessionID:    "session-1",
		TotalEntries: 1,
		Offset:       0,
		Entries: []clientui.ChatEntry{{
			Role: "assistant",
			Text: "new",
		}},
	})
	if cmd != nil {
		_ = cmd()
	}

	plain := stripANSIAndTrimRight(m.view.OngoingSnapshot())
	if strings.Contains(plain, "old") {
		t.Fatalf("expected complete page to replace stale transcript, got %q", plain)
	}
	if !strings.Contains(plain, "new") {
		t.Fatalf("expected complete page to render new transcript, got %q", plain)
	}
}

func TestAssistantDeltaAppendsStreamingText(t *testing.T) {
	m := newProjectedStaticUIModel()

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "hello"})
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: " world"})

	if got := m.view.OngoingStreamingText(); got != "hello world" {
		t.Fatalf("expected concatenated streaming text, got %q", got)
	}
}

func TestAssistantDeltaSkipsNoopFinalToken(t *testing.T) {
	m := newProjectedStaticUIModel()

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: uiNoopFinalToken})

	if got := m.view.OngoingStreamingText(); got != "" {
		t.Fatalf("expected noop final token to stay out of streaming text, got %q", got)
	}
	if m.sawAssistantDelta {
		t.Fatal("expected sawAssistantDelta to remain false for noop final token")
	}
}

func TestAssistantDeltaResetClearsStreamingText(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.forwardToView(tui.SetConversationMsg{Ongoing: "partial"})

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDeltaReset})

	if got := m.view.OngoingStreamingText(); got != "" {
		t.Fatalf("expected reset to clear streaming text, got %q", got)
	}
}

func TestReasoningDeltaUpdatesDetailTranscriptLive(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "u"})
	m.forwardToView(tui.ToggleModeMsg{})

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "Plan summary"}})

	if detail := stripANSIAndTrimRight(m.view.View()); !strings.Contains(detail, "Plan summary") {
		t.Fatalf("expected live reasoning summary in detail view, got %q", detail)
	}
	if detail := stripANSIAndTrimRight(m.view.View()); strings.Contains(detail, "Preparing patch") {
		t.Fatalf("expected separate status field ignored for detail view, got %q", detail)
	}
}

func TestReasoningDeltaResetClearsLiveReasoningTranscript(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "u"})
	m.forwardToView(tui.ToggleModeMsg{})
	m.forwardToView(tui.UpsertStreamingReasoningMsg{Key: "rs_1:summary:0", Role: "reasoning", Text: "Plan summary"})

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDeltaReset})

	if detail := stripANSIAndTrimRight(m.view.View()); strings.Contains(detail, "Plan summary") {
		t.Fatalf("expected live reasoning summary cleared after reset, got %q", detail)
	}
}

func TestReasoningDeltaPreservesStreamingWhitespaceAcrossUpdates(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "u"})
	m.forwardToView(tui.ToggleModeMsg{})

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "Analyzing chat snapshot commentary insertion"}})

	if detail := stripANSIAndTrimRight(m.view.View()); !strings.Contains(detail, "Analyzing chat snapshot commentary insertion") {
		t.Fatalf("expected reasoning whitespace preserved, got %q", detail)
	}
}

func TestReasoningDeltaBoldOnlyUpdatesStatusLineHeader(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "u"})
	m.forwardToView(tui.ToggleModeMsg{})

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventRunStateChanged, RunState: &runtime.RunState{Busy: true}})
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "**Summarizing fix and investigation**"}})

	status := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(status, "Summarizing fix and investigation") {
		t.Fatalf("expected bold-only reasoning summary in status line, got %q", status)
	}
	if strings.Contains(status, "**Summarizing fix and investigation**") {
		t.Fatalf("expected status line header without markdown markers, got %q", status)
	}
}

func TestReasoningDeltaMixedContentUsesFirstBoldSpanForStatusLineHeader(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "u"})
	m.forwardToView(tui.ToggleModeMsg{})

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventRunStateChanged, RunState: &runtime.RunState{Busy: true}})
	text := "**Summarizing fix and investigation**\n\nregular reasoning details"
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: text}})

	status := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(status, "Summarizing fix and investigation") {
		t.Fatalf("expected first bold span in status line, got %q", status)
	}
	if detail := stripANSIAndTrimRight(m.view.View()); !strings.Contains(detail, "regular reasoning details") {
		t.Fatalf("expected mixed reasoning content to remain in detail view, got %q", detail)
	}
}

func TestReasoningDeltaRegularSummaryDoesNotReplaceStatusLineHeader(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "u"})
	m.forwardToView(tui.ToggleModeMsg{})

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventRunStateChanged, RunState: &runtime.RunState{Busy: true}})
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "**Preparing patch**"}})
	text := "I am exploring ways to define atomic, low-level collection methods in NavResultStore that support reified filtering without reflection."
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: text}})

	if detail := stripANSIAndTrimRight(m.view.View()); !strings.Contains(detail, "I am exploring ways to define atomic, low-level collection methods") {
		t.Fatalf("expected plain reasoning summary in detail view, got %q", detail)
	}
	status := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(status, "Preparing patch") {
		t.Fatalf("expected prior bold-only header to persist, got %q", status)
	}
	if strings.Contains(status, "I am exploring ways to define atomic") {
		t.Fatalf("did not expect regular reasoning summary in status line, got %q", status)
	}
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "**Running checks**"}})
	status = stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(status, "Running checks") || strings.Contains(status, "Preparing patch") {
		t.Fatalf("expected latest bold-only header to replace prior value, got %q", status)
	}
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventRunStateChanged, RunState: &runtime.RunState{Busy: false}})
	status = stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if strings.Contains(status, "Running checks") {
		t.Fatalf("expected status line header cleared when run stops, got %q", status)
	}
}

func TestConversationSnapshotCommitClearsSawAssistantDelta(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.busy = true
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "partial"})
	if !m.sawAssistantDelta {
		t.Fatal("expected sawAssistantDelta true after assistant delta")
	}

	_ = m.runtimeAdapter().applyChatSnapshot(runtime.ChatSnapshot{Entries: []runtime.ChatEntry{{Role: "assistant", Text: "partial"}}, Ongoing: ""})
	m.busy = false
	m.syncViewport()

	if m.sawAssistantDelta {
		t.Fatal("expected sawAssistantDelta cleared after commit snapshot")
	}
	if strings.Contains(stripANSIPreserve(m.View()), "partial") {
		t.Fatalf("expected no stale streaming text in live region after commit, got %q", stripANSIPreserve(m.View()))
	}
}

func TestApplyChatSnapshotShowsMixedParallelPendingStatesInLiveView(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.spinnerFrame = 0

	cmd := m.runtimeAdapter().applyChatSnapshot(runtime.ChatSnapshot{Entries: []runtime.ChatEntry{
		{Role: "assistant", Text: "working"},
		{Role: "tool_call", Text: "echo a", ToolCallID: "call_a", ToolCall: &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "echo a"}},
		{Role: "tool_call", Text: "echo b", ToolCallID: "call_b", ToolCall: &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "echo b"}},
		{Role: "tool_result_ok", Text: "out-b", ToolCallID: "call_b"},
	}})
	if cmd != nil {
		_ = cmd()
	}
	m.syncViewport()

	view := stripANSIPreserve(m.View())
	if !strings.Contains(view, pendingSpinnerFrame(0)+" echo a") {
		t.Fatalf("expected unresolved tool to keep spinner in live view, got %q", view)
	}
	if !strings.Contains(view, "$ echo b") {
		t.Fatalf("expected completed sibling to use final shell symbol in live view, got %q", view)
	}
	if strings.Contains(view, pendingSpinnerFrame(0)+" echo b") {
		t.Fatalf("did not expect completed sibling to keep spinner in live view, got %q", view)
	}
	if strings.Contains(view, "waiting") {
		t.Fatalf("did not expect waiting annotation in live view, got %q", view)
	}
}

func TestUserMessageFlushedSyncsConversationForNativeReplay(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &runtimeAdapterFakeClient{responses: []llm.Response{
		{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "first"}},
		{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "second"}},
	}}
	eng, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	m := newProjectedEngineUIModel(eng)
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	eng.QueueUserMessage("steered message")
	if _, err := eng.SubmitUserMessage(context.Background(), "initial user"); err != nil {
		t.Fatalf("submit user message: %v", err)
	}

	cmd := m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventUserMessageFlushed, UserMessage: "steered message"})
	if cmd == nil {
		t.Fatal("expected conversation sync command for flushed user message")
	}
	refreshMsg, ok := cmd().(runtimeTranscriptRefreshedMsg)
	if !ok {
		t.Fatalf("expected runtimeTranscriptRefreshedMsg, got %T", cmd())
	}
	next, flushCmd := m.Update(refreshMsg)
	m = next.(*uiModel)
	if flushCmd == nil {
		t.Fatal("expected native replay command after conversation sync")
	}
	flushMsg, ok := flushCmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", flushCmd())
	}
	if !strings.Contains(stripANSIPreserve(flushMsg.Text), "steered message") {
		t.Fatalf("expected flushed replay text to include steered message, got %q", flushMsg.Text)
	}
}

func TestUserMessageFlushedAfterConversationUpdatedDoesNotDuplicateNativeReplay(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &runtimeAdapterFakeClient{responses: []llm.Response{
		{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "first"}},
		{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "second"}},
	}}
	eng, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	m := newProjectedEngineUIModel(eng)
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	eng.QueueUserMessage("steered message")
	if _, err := eng.SubmitUserMessage(context.Background(), "initial user"); err != nil {
		t.Fatalf("submit user message: %v", err)
	}

	conversationCmd := m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventConversationUpdated})
	if conversationCmd == nil {
		t.Fatal("expected conversation sync command")
	}
	refreshMsg, ok := conversationCmd().(runtimeTranscriptRefreshedMsg)
	if !ok {
		t.Fatalf("expected runtimeTranscriptRefreshedMsg, got %T", conversationCmd())
	}
	next, flushCmd := m.Update(refreshMsg)
	m = next.(*uiModel)
	if flushCmd == nil {
		t.Fatal("expected native replay command after conversation sync")
	}
	conversationFlush, ok := flushCmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", flushCmd())
	}
	if !strings.Contains(stripANSIPreserve(conversationFlush.Text), "steered message") {
		t.Fatalf("expected conversation replay text to include steered message, got %q", conversationFlush.Text)
	}

	flushCmd = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventUserMessageFlushed, UserMessage: "steered message"})
	if flushCmd == nil {
		return
	}
	secondRefresh, ok := flushCmd().(runtimeTranscriptRefreshedMsg)
	if !ok {
		t.Fatalf("expected runtimeTranscriptRefreshedMsg, got %T", flushCmd())
	}
	next, duplicateFlush := m.Update(secondRefresh)
	m = next.(*uiModel)
	if duplicateFlush != nil {
		if _, ok := duplicateFlush().(nativeHistoryFlushMsg); ok {
			t.Fatal("expected no duplicate native replay after already-synced conversation")
		}
	}
}

func TestDeferredNativeReplayFlushesAutomaticallyOnDetailExit(t *testing.T) {
	policies := []config.TUIAlternateScreenPolicy{
		config.TUIAlternateScreenNever,
		config.TUIAlternateScreenAuto,
	}
	for _, policy := range policies {
		t.Run(string(policy), func(t *testing.T) {
			m := newProjectedStaticUIModel(
				WithUIAlternateScreenPolicy(policy),
				WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
			)

			next, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
			m = next.(*uiModel)
			if startupCmd == nil {
				t.Fatal("expected startup replay command")
			}
			_ = collectCmdMessages(t, startupCmd)

			next, enterCmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
			m = next.(*uiModel)
			if m.view.Mode() != tui.ModeDetail {
				t.Fatalf("expected detail mode, got %q", m.view.Mode())
			}
			_ = collectCmdMessages(t, enterCmd)

			cmd := m.runtimeAdapter().applyChatSnapshot(runtime.ChatSnapshot{
				Entries: []runtime.ChatEntry{{Role: "assistant", Text: "seed"}, {Role: "user", Text: "steered later"}},
			})
			if cmd != nil {
				t.Fatalf("expected replay to stay deferred while detail is active, got %T", cmd())
			}

			next, leaveCmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
			m = next.(*uiModel)
			if m.view.Mode() != tui.ModeOngoing {
				t.Fatalf("expected ongoing mode, got %q", m.view.Mode())
			}
			msgs := collectCmdMessages(t, leaveCmd)
			flushCount := 0
			foundLater := false
			for _, msg := range msgs {
				flush, ok := msg.(nativeHistoryFlushMsg)
				if !ok {
					continue
				}
				flushCount++
				if strings.Contains(stripANSIPreserve(flush.Text), "steered later") {
					foundLater = true
				}
			}
			if flushCount == 0 {
				t.Fatalf("expected native replay flush on detail exit, got messages=%v", msgs)
			}
			if !foundLater {
				t.Fatalf("expected exit replay to include deferred transcript update, got messages=%v", msgs)
			}
		})
	}
}

func TestBackgroundUpdatedUsesTransientStatusLifecycle(t *testing.T) {
	m := newProjectedStaticUIModel()

	cmd := m.runtimeAdapter().handleRuntimeEvent(runtime.Event{
		Kind: runtime.EventBackgroundUpdated,
		Background: &runtime.BackgroundShellEvent{
			Type:  "completed",
			ID:    "1000",
			State: "completed",
		},
	})
	if cmd == nil {
		t.Fatal("expected transient status clear command")
	}
	if got := strings.TrimSpace(m.transientStatus); got != "background shell 1000 completed" {
		t.Fatalf("unexpected transient status %q", got)
	}
	if m.transientStatusKind != uiStatusNoticeSuccess {
		t.Fatalf("expected success notice kind, got %d", m.transientStatusKind)
	}
	clearMsg, ok := cmd().(clearTransientStatusMsg)
	if !ok {
		t.Fatalf("expected clearTransientStatusMsg, got %T", cmd())
	}
	next, _ := m.Update(clearMsg)
	updated := next.(*uiModel)
	if updated.transientStatus != "" {
		t.Fatalf("expected transient status to clear, got %q", updated.transientStatus)
	}
	if updated.transientStatusKind != uiStatusNoticeNeutral {
		t.Fatalf("expected transient status kind reset, got %d", updated.transientStatusKind)
	}
}

func TestBackgroundUpdatedWhileBusyUsesCompletionStatus(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.busy = true

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{
		Kind: runtime.EventBackgroundUpdated,
		Background: &runtime.BackgroundShellEvent{
			Type:  "completed",
			ID:    "1000",
			State: "completed",
		},
	})

	if got := strings.TrimSpace(m.transientStatus); got != "background shell 1000 completed" {
		t.Fatalf("unexpected transient status %q", got)
	}
}

func TestBackgroundUpdatedWithSuppressedNoticeSkipsTransientStatus(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.transientStatus = "existing"

	cmd := m.runtimeAdapter().handleRuntimeEvent(runtime.Event{
		Kind: runtime.EventBackgroundUpdated,
		Background: &runtime.BackgroundShellEvent{
			Type:             "completed",
			ID:               "1000",
			State:            "completed",
			NoticeSuppressed: true,
		},
	})

	if cmd != nil {
		t.Fatalf("did not expect transient status command when notice is suppressed, got %T", cmd())
	}
	if m.transientStatus != "existing" {
		t.Fatalf("expected transient status unchanged, got %q", m.transientStatus)
	}
}

func TestDeferredNativeReplayFlushesBackgroundNoticeOnDetailExit(t *testing.T) {
	policies := []config.TUIAlternateScreenPolicy{
		config.TUIAlternateScreenNever,
		config.TUIAlternateScreenAuto,
	}
	for _, policy := range policies {
		t.Run(string(policy), func(t *testing.T) {
			m := newProjectedStaticUIModel(
				WithUIAlternateScreenPolicy(policy),
				WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
			)

			next, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
			m = next.(*uiModel)
			if startupCmd == nil {
				t.Fatal("expected startup replay command")
			}
			_ = collectCmdMessages(t, startupCmd)

			next, enterCmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
			m = next.(*uiModel)
			if m.view.Mode() != tui.ModeDetail {
				t.Fatalf("expected detail mode, got %q", m.view.Mode())
			}
			_ = collectCmdMessages(t, enterCmd)

			cmd := m.runtimeAdapter().applyChatSnapshot(runtime.ChatSnapshot{
				Entries: []runtime.ChatEntry{
					{Role: "assistant", Text: "seed"},
					{Role: "system", Text: "Background shell 1000 completed.\nExit code: 0\nOutput:\ndone", OngoingText: "Background shell 1000 completed (exit 0)"},
				},
			})
			if cmd != nil {
				t.Fatalf("expected replay to stay deferred while detail is active, got %T", cmd())
			}

			next, leaveCmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
			m = next.(*uiModel)
			if m.view.Mode() != tui.ModeOngoing {
				t.Fatalf("expected ongoing mode, got %q", m.view.Mode())
			}
			msgs := collectCmdMessages(t, leaveCmd)
			flushCount := 0
			foundNotice := false
			for _, msg := range msgs {
				flush, ok := msg.(nativeHistoryFlushMsg)
				if !ok {
					continue
				}
				flushCount++
				plain := stripANSIPreserve(flush.Text)
				if strings.Contains(plain, "Background shell 1000 completed (exit 0)") {
					foundNotice = true
				}
			}
			if flushCount == 0 {
				t.Fatalf("expected native replay flush on detail exit, got messages=%v", msgs)
			}
			if !foundNotice {
				t.Fatalf("expected exit replay to include deferred background notice, got messages=%v", msgs)
			}
		})
	}
}

func TestRunStateChangedTransitionsRunningStateToIdleWhenTurnEnds(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.activity = uiActivityRunning

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventRunStateChanged, RunState: &runtime.RunState{Busy: false}})

	if m.activity != uiActivityIdle {
		t.Fatalf("expected idle activity after turn end, got %v", m.activity)
	}
}

func TestUserRequestedKilledBackgroundUsesSuccessNotice(t *testing.T) {
	m := newProjectedStaticUIModel()

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{
		Kind: runtime.EventBackgroundUpdated,
		Background: &runtime.BackgroundShellEvent{
			Type:              "killed",
			ID:                "1001",
			State:             "killed",
			UserRequestedKill: true,
		},
	})
	if m.transientStatusKind != uiStatusNoticeSuccess {
		t.Fatalf("expected success notice kind, got %d", m.transientStatusKind)
	}
}
