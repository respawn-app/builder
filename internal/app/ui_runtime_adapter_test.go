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
	tea "github.com/charmbracelet/bubbletea"
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

	_ = m.runtimeAdapter().applyChatSnapshot(runtime.ChatSnapshot{Ongoing: "hello"})

	if got := m.view.OngoingStreamingText(); got != "hello" {
		t.Fatalf("expected snapshot ongoing text, got %q", got)
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

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "Plan summary"}})

	if detail := stripANSIAndTrimRight(m.view.View()); !strings.Contains(detail, "Plan summary") {
		t.Fatalf("expected live reasoning summary in detail view, got %q", detail)
	}
	if detail := stripANSIAndTrimRight(m.view.View()); strings.Contains(detail, "Preparing patch") {
		t.Fatalf("expected separate status field ignored for detail view, got %q", detail)
	}
}

func TestReasoningDeltaResetClearsLiveReasoningTranscript(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
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
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "u"})
	m.forwardToView(tui.ToggleModeMsg{})

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "Analyzing chat snapshot commentary insertion"}})

	if detail := stripANSIAndTrimRight(m.view.View()); !strings.Contains(detail, "Analyzing chat snapshot commentary insertion") {
		t.Fatalf("expected reasoning whitespace preserved, got %q", detail)
	}
}

func TestReasoningDeltaBoldOnlyUpdatesStatusLineHeader(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
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
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
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
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
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

func TestDeferredNativeReplayFlushesAutomaticallyOnDetailExit(t *testing.T) {
	policies := []config.TUIAlternateScreenPolicy{
		config.TUIAlternateScreenNever,
		config.TUIAlternateScreenAuto,
	}
	for _, policy := range policies {
		t.Run(string(policy), func(t *testing.T) {
			m := NewUIModel(
				nil,
				make(chan runtime.Event),
				make(chan askEvent),
				WithUIScrollMode(config.TUIScrollModeNative),
				WithUIAlternateScreenPolicy(policy),
				WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
			).(*uiModel)

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

func TestBackgroundUpdatedWhileBusyUsesCompletionStatus(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
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
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
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
			m := NewUIModel(
				nil,
				make(chan runtime.Event),
				make(chan askEvent),
				WithUIScrollMode(config.TUIScrollModeNative),
				WithUIAlternateScreenPolicy(policy),
				WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
			).(*uiModel)

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
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.activity = uiActivityRunning

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventRunStateChanged, RunState: &runtime.RunState{Busy: false}})

	if m.activity != uiActivityIdle {
		t.Fatalf("expected idle activity after turn end, got %v", m.activity)
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
