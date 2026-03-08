package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"builder/internal/config"
	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/session"
	"builder/internal/tools"
	"builder/internal/tui"
)

type runtimeAdapterFakeClient struct {
	responses []llm.Response
	index     int
}

func (f *runtimeAdapterFakeClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	if f.index >= len(f.responses) {
		return llm.Response{}, errors.New("no fake response configured")
	}
	resp := f.responses[f.index]
	f.index++
	return resp, nil
}

func TestApplyChatSnapshotSetsOngoingFromSnapshot(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	_ = m.runtimeAdapter().applyChatSnapshot(runtime.ChatSnapshot{Ongoing: "hello", Activity: "Checking out repository"})

	if got := m.view.OngoingStreamingText(); got != "hello" {
		t.Fatalf("expected snapshot ongoing text, got %q", got)
	}
	if got := m.activityStatus; got != "Checking out repository" {
		t.Fatalf("expected activity status from snapshot, got %q", got)
	}
}

func TestAssistantDeltaAppendsStreamingText(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "hello"})
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: " world"})

	if got := m.view.OngoingStreamingText(); got != "hello world" {
		t.Fatalf("expected concatenated streaming text, got %q", got)
	}
}

func TestAssistantDeltaResetClearsStreamingText(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.forwardToView(tui.SetConversationMsg{Ongoing: "partial"})

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDeltaReset})

	if got := m.view.OngoingStreamingText(); got != "" {
		t.Fatalf("expected reset to clear streaming text, got %q", got)
	}
}

func TestReasoningDeltaUpdatesDetailTranscriptLive(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "u"})
	m.forwardToView(tui.ToggleModeMsg{})

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "Plan summary", Status: "Preparing patch"}})

	if detail := stripANSIAndTrimRight(m.view.View()); !strings.Contains(detail, "Plan summary") {
		t.Fatalf("expected live reasoning summary in detail view, got %q", detail)
	}
	if detail := stripANSIAndTrimRight(m.view.View()); strings.Contains(detail, "Preparing patch") {
		t.Fatalf("expected reasoning status omitted from detail view, got %q", detail)
	}
	if got := m.activityStatus; got != "Preparing patch" {
		t.Fatalf("expected reasoning status in status line activity, got %q", got)
	}
}

func TestReasoningDeltaPreservesStatusAcrossPlainSummaryUpdates(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "u"})
	m.forwardToView(tui.ToggleModeMsg{})

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "Initial summary", Status: "Preparing patch"}})
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "Expanded plain summary"}})

	if got := m.activityStatus; got != "Preparing patch" {
		t.Fatalf("expected reasoning status preserved across plain summary update, got %q", got)
	}
	if detail := stripANSIAndTrimRight(m.view.View()); !strings.Contains(detail, "Expanded plain summary") {
		t.Fatalf("expected updated plain reasoning summary in detail view, got %q", detail)
	}
}

func TestReasoningDeltaResetClearsLiveReasoningTranscript(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "u"})
	m.forwardToView(tui.ToggleModeMsg{})
	m.activityStatus = "Preparing patch"
	m.forwardToView(tui.UpsertStreamingReasoningMsg{Key: "rs_1:summary:0", Role: "reasoning", Text: "Plan summary"})

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDeltaReset})

	if detail := stripANSIAndTrimRight(m.view.View()); strings.Contains(detail, "Plan summary") {
		t.Fatalf("expected live reasoning summary cleared after reset, got %q", detail)
	}
	if m.activityStatus != "" {
		t.Fatalf("expected reasoning status cleared after reset, got %q", m.activityStatus)
	}
}

func TestReasoningDeltaStatusOnlyClearsLiveReasoningForKey(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "u"})
	m.forwardToView(tui.ToggleModeMsg{})

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "Plan summary", Status: "Preparing patch"}})
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "", Status: "Running checks"}})

	if detail := stripANSIAndTrimRight(m.view.View()); strings.Contains(detail, "Plan summary") {
		t.Fatalf("expected stale live reasoning cleared when key becomes status-only, got %q", detail)
	}
	if got := m.activityStatus; got != "Running checks" {
		t.Fatalf("expected latest status after status-only delta, got %q", got)
	}
}

func TestReasoningDeltaMixedPayloadShowsStatusOnlyInStatusLine(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "u"})
	m.forwardToView(tui.ToggleModeMsg{})

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Status: "Refactoring resultReceiver usage"}})
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "I am exploring ways to define atomic, low-level collection methods in NavResultStore that support reified filtering without reflection."}})
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "I am exploring ways to define atomic, low-level collection methods in NavResultStore that support reified filtering without reflection.", Status: "Preparing patch for navigation files"}})

	if detail := stripANSIAndTrimRight(m.view.View()); strings.Contains(detail, "Refactoring resultReceiver usage") || strings.Contains(detail, "Preparing patch for navigation files") {
		t.Fatalf("expected status-only reasoning lines omitted from detail view, got %q", detail)
	}
	if detail := stripANSIAndTrimRight(m.view.View()); !strings.Contains(detail, "I am exploring ways to define atomic, low-level collection methods") {
		t.Fatalf("expected plain reasoning summary in detail view, got %q", detail)
	}
	status := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(status, "Preparing patch for navigation files") {
		t.Fatalf("expected latest bold reasoning status in status line, got %q", status)
	}
	if strings.Contains(status, "I am exploring ways to define atomic") {
		t.Fatalf("expected plain reasoning summary omitted from status line, got %q", status)
	}
}

func TestConversationSnapshotCommitClearsSawAssistantDelta(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIScrollMode(config.TUIScrollModeNative)).(*uiModel)
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

	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent), WithUIScrollMode(config.TUIScrollModeNative)).(*uiModel)
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	eng.QueueUserMessage("steered message")
	if _, err := eng.SubmitUserMessage(context.Background(), "initial user"); err != nil {
		t.Fatalf("submit user message: %v", err)
	}

	cmd := m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventUserMessageFlushed, UserMessage: "steered message"})
	if cmd == nil {
		t.Fatal("expected native replay command for flushed user message")
	}
	flushMsg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
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

	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent), WithUIScrollMode(config.TUIScrollModeNative)).(*uiModel)
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	eng.QueueUserMessage("steered message")
	if _, err := eng.SubmitUserMessage(context.Background(), "initial user"); err != nil {
		t.Fatalf("submit user message: %v", err)
	}

	conversationCmd := m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventConversationUpdated})
	if conversationCmd == nil {
		t.Fatal("expected conversation update replay command")
	}
	conversationFlush, ok := conversationCmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", conversationCmd())
	}
	if !strings.Contains(stripANSIPreserve(conversationFlush.Text), "steered message") {
		t.Fatalf("expected conversation replay text to include steered message, got %q", conversationFlush.Text)
	}

	flushCmd := m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventUserMessageFlushed, UserMessage: "steered message"})
	if flushCmd != nil {
		t.Fatalf("expected no duplicate replay after already-synced conversation, got %T", flushCmd())
	}
}

func TestBackgroundUpdatedUsesTransientStatusLifecycle(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

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

func TestRunStateChangedClearsActivityStatusWhenTurnEnds(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.activityStatus = "modifying database config"
	m.activity = uiActivityRunning

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventRunStateChanged, RunState: &runtime.RunState{Busy: false}})

	if m.activityStatus != "" {
		t.Fatalf("expected activity status cleared after turn end, got %q", m.activityStatus)
	}
}

func TestUserRequestedKilledBackgroundUsesSuccessNotice(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

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
