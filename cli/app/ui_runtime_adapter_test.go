package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"builder/cli/tui"
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/transcript"
	"builder/shared/transcript/toolcodec"
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

type startupTranscriptRuntimeClient struct {
	runtimeControlFakeClient
	transcriptCalls int
	loadRequests    []clientui.TranscriptPageRequest
	view            clientui.RuntimeMainView
	page            clientui.TranscriptPage
}

func (c *startupTranscriptRuntimeClient) MainView() clientui.RuntimeMainView {
	if c.view.Session.SessionID == "" {
		c.view.Session.SessionID = "session-1"
	}
	return c.view
}

func (c *startupTranscriptRuntimeClient) Transcript() clientui.TranscriptPage {
	c.transcriptCalls++
	if c.page.SessionID == "" {
		c.page.SessionID = "session-1"
	}
	return c.page
}

func (c *startupTranscriptRuntimeClient) LoadTranscriptPage(req clientui.TranscriptPageRequest) (clientui.TranscriptPage, error) {
	c.loadRequests = append(c.loadRequests, req)
	if c.page.SessionID == "" {
		c.page.SessionID = "session-1"
	}
	return c.page, nil
}

func (c *startupTranscriptRuntimeClient) RefreshTranscriptPage(req clientui.TranscriptPageRequest) (clientui.TranscriptPage, error) {
	return c.LoadTranscriptPage(req)
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

func (f *refreshingRuntimeClient) RefreshTranscriptPage(req clientui.TranscriptPageRequest) (clientui.TranscriptPage, error) {
	return f.LoadTranscriptPage(req)
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

func TestProjectRuntimeEventIncludesReviewerTranscriptEntry(t *testing.T) {
	evt := projectRuntimeEvent(runtime.Event{
		Kind: runtime.EventReviewerCompleted,
		Reviewer: &runtime.ReviewerStatus{
			Outcome:          "applied",
			SuggestionsCount: 2,
		},
	})

	if len(evt.TranscriptEntries) != 1 {
		t.Fatalf("expected one transcript entry, got %d", len(evt.TranscriptEntries))
	}
	if got := evt.TranscriptEntries[0].Role; got != "reviewer_status" {
		t.Fatalf("reviewer transcript role = %q, want reviewer_status", got)
	}
	if got := evt.TranscriptEntries[0].Text; !strings.Contains(got, "2 suggestions, applied") {
		t.Fatalf("reviewer transcript text = %q", got)
	}
}

func TestProjectRuntimeEventIncludesBackgroundSystemTranscriptEntry(t *testing.T) {
	evt := projectRuntimeEvent(runtime.Event{
		Kind: runtime.EventBackgroundUpdated,
		Background: &runtime.BackgroundShellEvent{
			Type:        "completed",
			ID:          "1000",
			State:       "completed",
			NoticeText:  "Background shell 1000 completed.\nOutput:\nhello",
			CompactText: "Background shell 1000 completed",
		},
	})

	if len(evt.TranscriptEntries) != 1 {
		t.Fatalf("expected one transcript entry, got %d", len(evt.TranscriptEntries))
	}
	entry := evt.TranscriptEntries[0]
	if entry.Role != "system" {
		t.Fatalf("background transcript role = %q, want system", entry.Role)
	}
	if !strings.Contains(entry.Text, "Background shell 1000 completed") {
		t.Fatalf("background transcript text = %q", entry.Text)
	}
	if entry.OngoingText != "Background shell 1000 completed" {
		t.Fatalf("background transcript ongoing = %q", entry.OngoingText)
	}
}

func TestHandleProjectedRuntimeEventAppendsTranscriptEntriesImmediately(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})

	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(projectRuntimeEvent(runtime.Event{
		Kind:        runtime.EventUserMessageFlushed,
		StepID:      "step-1",
		UserMessage: "say hi",
	}))

	callMeta := transcript.ToolCallMeta{ToolName: "shell", Command: "pwd", CompactText: "pwd", IsShell: true}
	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(projectRuntimeEvent(runtime.Event{
		Kind:   runtime.EventToolCallStarted,
		StepID: "step-1",
		ToolCall: &llm.ToolCall{
			ID:           "call-1",
			Name:         string(tools.ToolShell),
			Presentation: toolcodec.EncodeToolCallMeta(callMeta),
		},
	}))

	if pending := nativePendingToolEntries(m.transcriptEntries); len(pending) != 1 {
		t.Fatalf("expected pending tool call visible immediately, got %d pending entries", len(pending))
	}

	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(projectRuntimeEvent(runtime.Event{
		Kind:   runtime.EventToolCallCompleted,
		StepID: "step-1",
		ToolResult: &tools.Result{
			CallID: "call-1",
			Name:   tools.ToolShell,
			Output: []byte("/tmp"),
		},
	}))

	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(projectRuntimeEvent(runtime.Event{
		Kind:   runtime.EventAssistantMessage,
		StepID: "step-1",
		Message: llm.Message{
			Role:    llm.RoleAssistant,
			Content: "**done**",
			Phase:   llm.MessagePhaseFinal,
		},
	}))

	if len(m.transcriptEntries) != 4 {
		t.Fatalf("expected four transcript entries, got %+v", m.transcriptEntries)
	}
	if got := m.transcriptEntries[0].Role; got != "user" {
		t.Fatalf("entry[0].Role = %q, want user", got)
	}
	if got := m.transcriptEntries[1].Role; got != "tool_call" {
		t.Fatalf("entry[1].Role = %q, want tool_call", got)
	}
	if got := m.transcriptEntries[1].Text; got != "pwd" {
		t.Fatalf("entry[1].Text = %q, want pwd", got)
	}
	if got := m.transcriptEntries[2].Role; got != "tool_result_ok" {
		t.Fatalf("entry[2].Role = %q, want tool_result_ok", got)
	}
	if got := m.transcriptEntries[2].Text; !strings.Contains(got, "/tmp") {
		t.Fatalf("entry[2].Text = %q, want tool output", got)
	}
	if got := m.transcriptEntries[3].Role; got != "assistant" {
		t.Fatalf("entry[3].Role = %q, want assistant", got)
	}
	if got := m.transcriptEntries[3].Text; got != "**done**" {
		t.Fatalf("entry[3].Text = %q, want final assistant text", got)
	}
	if pending := nativePendingToolEntries(m.transcriptEntries); len(pending) != 0 {
		t.Fatalf("expected pending tool call cleared after result, got %d pending entries", len(pending))
	}
	if loaded := m.view.LoadedTranscriptEntries(); len(loaded) != 4 {
		t.Fatalf("view loaded transcript length = %d, want 4", len(loaded))
	}
}

func TestRuntimeEventBatchCoalescesCommittedNativeFlushAndPreservesOrder(t *testing.T) {
	m := newProjectedTestUIModel(nil, closedProjectedRuntimeEvents(), nil,
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	_ = collectCmdMessages(t, startupCmd)

	callMeta := transcript.ToolCallMeta{ToolName: "shell", Command: "pwd", CompactText: "pwd", IsShell: true}
	firstBatch := []clientui.Event{
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventRunStateChanged, RunState: &runtime.RunState{Busy: true}}),
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventUserMessageFlushed, StepID: "step-1", UserMessage: "say hi"}),
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventReviewerCompleted, StepID: "step-1", Reviewer: &runtime.ReviewerStatus{Outcome: "applied", SuggestionsCount: 2}}),
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventBackgroundUpdated, StepID: "step-1", Background: &runtime.BackgroundShellEvent{Type: "completed", ID: "1000", State: "completed", NoticeText: "Background shell 1000 completed.\nOutput:\nhello", CompactText: "Background shell 1000 completed"}}),
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventToolCallStarted, StepID: "step-1", ToolCall: &llm.ToolCall{ID: "call_1", Name: string(tools.ToolShell), Presentation: toolcodec.EncodeToolCallMeta(callMeta)}}),
	}
	updated, cmd := m.Update(runtimeEventBatchMsg{events: firstBatch})
	m = updated.(*uiModel)
	msgs := collectCmdMessages(t, cmd)
	flushes := make([]nativeHistoryFlushMsg, 0)
	for _, msg := range msgs {
		flush, ok := msg.(nativeHistoryFlushMsg)
		if ok {
			flushes = append(flushes, flush)
		}
	}
	if len(flushes) != 1 {
		t.Fatalf("expected exactly one committed native flush for mixed batch, got %d msgs=%T", len(flushes), msgs)
	}
	plain := stripANSIPreserve(flushes[0].Text)
	if !containsInOrder(plain, "say hi", "Supervisor ran", "Background shell 1000 completed") {
		t.Fatalf("expected coalesced flush to preserve committed order, got %q", plain)
	}
	if strings.Contains(plain, "pwd") {
		t.Fatalf("expected pending tool call to stay out of committed flush, got %q", plain)
	}
	if view := stripANSIPreserve(m.View()); !strings.Contains(view, "pwd") {
		t.Fatalf("expected pending tool call still visible in live region, got %q", view)
	}
}

func TestRuntimeEventBatchDoesNotSequenceNativeFlushBehindTransientStatusTimer(t *testing.T) {
	m := newProjectedTestUIModel(nil, closedProjectedRuntimeEvents(), nil,
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	_ = collectCmdMessages(t, startupCmd)

	cmd := m.runtimeAdapter().handleProjectedRuntimeEventsBatch([]clientui.Event{
		projectRuntimeEvent(runtime.Event{
			Kind:   runtime.EventBackgroundUpdated,
			StepID: "step-1",
			Background: &runtime.BackgroundShellEvent{
				Type:        "completed",
				ID:          "1000",
				State:       "completed",
				NoticeText:  "Background shell 1000 completed.\nOutput:\nhello",
				CompactText: "Background shell 1000 completed",
			},
		}),
	})
	if cmd == nil {
		t.Fatal("expected runtime event batch command")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected runtime event batch to compose with tea.Batch, got %T", cmd())
	}
	flushFound := false
	for _, child := range batch {
		msg, immediate := immediateCmdMsg(child, 20*time.Millisecond)
		if !immediate {
			continue
		}
		flush, ok := msg.(nativeHistoryFlushMsg)
		if !ok {
			continue
		}
		if strings.Contains(stripANSIPreserve(flush.Text), "Background shell 1000 completed") {
			flushFound = true
			break
		}
	}
	if !flushFound {
		t.Fatal("expected immediate native history flush to be present alongside delayed transient-status timer")
	}
}

func immediateCmdMsg(cmd tea.Cmd, timeout time.Duration) (tea.Msg, bool) {
	if cmd == nil {
		return nil, false
	}
	ch := make(chan tea.Msg, 1)
	go func() {
		ch <- cmd()
	}()
	select {
	case msg := <-ch:
		return msg, true
	case <-time.After(timeout):
		return nil, false
	}
}

func TestHandleProjectedRuntimeEventSkipsAlreadyHydratedAssistantEntry(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.transcriptEntries = []tui.TranscriptEntry{{Role: "assistant", Text: "same", Phase: llm.MessagePhaseFinal}}
	m.transcriptBaseOffset = 0
	m.transcriptTotalEntries = 1
	m.transcriptRevision = 10
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})

	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(projectRuntimeEvent(runtime.Event{
		Kind:                runtime.EventAssistantMessage,
		StepID:              "step-1",
		TranscriptRevision:  10,
		CommittedEntryCount: 1,
		Message: llm.Message{
			Role:    llm.RoleAssistant,
			Content: "same",
			Phase:   llm.MessagePhaseFinal,
		},
	}))

	if len(m.transcriptEntries) != 1 {
		t.Fatalf("expected duplicate hydrated assistant entry to be skipped, got %+v", m.transcriptEntries)
	}
}

func TestHandleProjectedRuntimeEventDoesNotSuppressPendingToolCallStart(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.transcriptEntries = []tui.TranscriptEntry{{Role: "assistant", Text: "seed", Phase: llm.MessagePhaseCommentary}}
	m.transcriptBaseOffset = 0
	m.transcriptTotalEntries = 1
	m.transcriptRevision = 10
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})

	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(clientui.Event{
		Kind:                clientui.EventToolCallStarted,
		StepID:              "step-1",
		TranscriptRevision:  10,
		CommittedEntryCount: 1,
		TranscriptEntries: []clientui.ChatEntry{{
			Role:       "tool_call",
			Text:       "pwd",
			ToolCallID: "call-1",
			ToolCall:   &clientui.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "pwd"},
		}},
	})

	if got := len(m.transcriptEntries); got != 2 {
		t.Fatalf("expected pending tool call appended immediately, got %+v", m.transcriptEntries)
	}
	if got := m.transcriptEntries[1].Role; got != "tool_call" {
		t.Fatalf("second transcript role = %q, want tool_call", got)
	}
}

func TestHandleProjectedRuntimeEventSkipsReplayedToolCallStartWithSameToolCallID(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.transcriptEntries = []tui.TranscriptEntry{
		{Role: "assistant", Text: "seed", Phase: llm.MessagePhaseCommentary},
		{Role: "tool_call", Text: "pwd", ToolCallID: "call-1", ToolCall: &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "pwd"}},
	}
	m.transcriptBaseOffset = 0
	m.transcriptTotalEntries = len(m.transcriptEntries)
	m.transcriptRevision = 10
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})

	cmd := m.runtimeAdapter().handleProjectedRuntimeEvent(clientui.Event{
		Kind:                clientui.EventToolCallStarted,
		StepID:              "step-1",
		TranscriptRevision:  10,
		CommittedEntryCount: 1,
		TranscriptEntries: []clientui.ChatEntry{{
			Role:       "tool_call",
			Text:       "pwd",
			ToolCallID: "call-1",
			ToolCall:   &clientui.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "pwd"},
		}},
	})

	if got := len(m.transcriptEntries); got != 2 {
		t.Fatalf("expected replayed tool call start skipped, got %+v", m.transcriptEntries)
	}
	if cmd != nil {
		if _, ok := cmd().(nativeHistoryFlushMsg); ok {
			t.Fatal("expected no native replay for replayed tool call start")
		}
	}
}

func TestHandleProjectedRuntimeEventAppendsDistinctToolCallStartByToolCallID(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.transcriptEntries = []tui.TranscriptEntry{{Role: "tool_call", Text: "pwd", ToolCallID: "call-1", ToolCall: &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "pwd"}}}
	m.transcriptBaseOffset = 0
	m.transcriptTotalEntries = 1
	m.transcriptRevision = 10
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})

	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(clientui.Event{
		Kind:                clientui.EventToolCallStarted,
		StepID:              "step-1",
		TranscriptRevision:  10,
		CommittedEntryCount: 1,
		TranscriptEntries: []clientui.ChatEntry{{
			Role:       "tool_call",
			Text:       "pwd",
			ToolCallID: "call-2",
			ToolCall:   &clientui.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "pwd"},
		}},
	})

	if got := len(m.transcriptEntries); got != 2 {
		t.Fatalf("expected distinct tool call id to append, got %+v", m.transcriptEntries)
	}
	if got := m.transcriptEntries[1].ToolCallID; got != "call-2" {
		t.Fatalf("second tool call id = %q, want call-2", got)
	}
}

func TestHandleProjectedRuntimeEventDoesNotSuppressReviewerStatusEntry(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.transcriptEntries = []tui.TranscriptEntry{{Role: "assistant", Text: "seed", Phase: llm.MessagePhaseCommentary}}
	m.transcriptBaseOffset = 0
	m.transcriptTotalEntries = 1
	m.transcriptRevision = 10
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})

	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(clientui.Event{
		Kind:                clientui.EventReviewerCompleted,
		StepID:              "step-1",
		TranscriptRevision:  10,
		CommittedEntryCount: 1,
		TranscriptEntries: []clientui.ChatEntry{{
			Role: "reviewer_status",
			Text: "Supervisor ran and applied 2 suggestions.",
		}},
	})

	if got := len(m.transcriptEntries); got != 2 {
		t.Fatalf("expected reviewer status appended immediately, got %+v", m.transcriptEntries)
	}
	if got := m.transcriptEntries[1].Role; got != "reviewer_status" {
		t.Fatalf("second transcript role = %q, want reviewer_status", got)
	}
}

func TestHandleProjectedRuntimeEventDoesNotSuppressCompactionNoticeEntry(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.transcriptEntries = []tui.TranscriptEntry{{Role: "assistant", Text: "seed", Phase: llm.MessagePhaseCommentary}}
	m.transcriptBaseOffset = 0
	m.transcriptTotalEntries = 1
	m.transcriptRevision = 10
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})

	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(clientui.Event{
		Kind:                clientui.EventCompactionCompleted,
		StepID:              "step-1",
		TranscriptRevision:  10,
		CommittedEntryCount: 1,
		TranscriptEntries: []clientui.ChatEntry{{
			Role: "compaction_notice",
			Text: "context compacted for the 1st time",
		}},
	})

	if got := len(m.transcriptEntries); got != 2 {
		t.Fatalf("expected compaction notice appended immediately, got %+v", m.transcriptEntries)
	}
	if got := m.transcriptEntries[1].Role; got != "compaction_notice" {
		t.Fatalf("second transcript role = %q, want compaction_notice", got)
	}
}

func TestHandleProjectedRuntimeEventAppendsCompactionCleanupAndBackgroundEntriesImmediately(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})

	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(projectRuntimeEvent(runtime.Event{
		Kind:   runtime.EventCompactionCompleted,
		StepID: "step-1",
		Compaction: &runtime.CompactionStatus{
			Mode:  "auto",
			Count: 2,
		},
	}))
	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(projectRuntimeEvent(runtime.Event{
		Kind:   runtime.EventCompactionFailed,
		StepID: "step-1",
		Compaction: &runtime.CompactionStatus{
			Mode:  "manual",
			Error: "quota exceeded",
		},
	}))
	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(projectRuntimeEvent(runtime.Event{
		Kind:   runtime.EventInFlightClearFailed,
		StepID: "step-1",
		Error:  "mark in-flight false",
	}))
	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(projectRuntimeEvent(runtime.Event{
		Kind: runtime.EventBackgroundUpdated,
		Background: &runtime.BackgroundShellEvent{
			Type:        "completed",
			ID:          "1000",
			State:       "completed",
			NoticeText:  "Background shell 1000 completed.\nNo output",
			CompactText: "Background shell 1000 completed",
		},
	}))

	if len(m.transcriptEntries) != 4 {
		t.Fatalf("expected four immediate transcript entries, got %+v", m.transcriptEntries)
	}
	if got := m.transcriptEntries[0].Role; got != "compaction_notice" {
		t.Fatalf("entry[0].Role = %q, want compaction_notice", got)
	}
	if got := m.transcriptEntries[1].Role; got != "error" {
		t.Fatalf("entry[1].Role = %q, want error", got)
	}
	if got := m.transcriptEntries[2].Role; got != "error" {
		t.Fatalf("entry[2].Role = %q, want error", got)
	}
	if got := m.transcriptEntries[3].Role; got != "system" {
		t.Fatalf("entry[3].Role = %q, want system", got)
	}
	if got := m.transcriptEntries[3].OngoingText; got != "Background shell 1000 completed" {
		t.Fatalf("background ongoing text = %q", got)
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

func TestApplyProjectedTranscriptPageReplacesOngoingTailWindow(t *testing.T) {
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
	if strings.Contains(plain, "prompt") || strings.Contains(plain, "pwd") {
		t.Fatalf("expected bounded tail window to replace stale earlier entries, got %q", plain)
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

func TestApplyRuntimeTranscriptPageSkipsDuplicateDetailRefresh(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 12
	m.windowSizeKnown = true
	page := clientui.TranscriptPage{SessionID: "session-1", Offset: 300, TotalEntries: 500}
	for i := 0; i < 200; i++ {
		page.Entries = append(page.Entries, clientui.ChatEntry{Role: "assistant", Text: fmt.Sprintf("line %03d", 300+i)})
	}
	entries := transcriptEntriesFromPage(page)
	m.detailTranscript.replace(page)
	m.forwardToView(tui.SetConversationMsg{BaseOffset: page.Offset, TotalEntries: page.TotalEntries, Entries: entries})
	m.forwardToView(tui.SetModeMsg{Mode: tui.ModeDetail, SkipDetailWarmup: true})
	m.syncViewport()

	cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{Offset: page.Offset, Limit: len(page.Entries)}, page)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("expected duplicate detail page refresh to be skipped, got %T", msg)
		}
	}
	if m.view.TranscriptBaseOffset() != page.Offset || m.view.TranscriptTotalEntries() != page.TotalEntries {
		t.Fatalf("detail transcript metadata changed unexpectedly: base=%d total=%d", m.view.TranscriptBaseOffset(), m.view.TranscriptTotalEntries())
	}
}

func TestApplyRuntimeTranscriptPageInDetailModeDoesNotRebuildNativeHistoryState(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	ongoingPage := clientui.TranscriptPage{SessionID: "session-1", Offset: 300, TotalEntries: 500}
	for i := 0; i < 200; i++ {
		ongoingPage.Entries = append(ongoingPage.Entries, clientui.ChatEntry{Role: "assistant", Text: fmt.Sprintf("tail %03d", 300+i)})
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{Window: clientui.TranscriptWindowOngoingTail}, ongoingPage); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}
	baselineProjection := m.nativeProjection
	baselineRenderedProjection := m.nativeRenderedProjection
	baselineRenderedSnapshot := m.nativeRenderedSnapshot
	baselineFlushedEntryCount := m.nativeFlushedEntryCount

	m.forwardToView(tui.SetModeMsg{Mode: tui.ModeDetail, SkipDetailWarmup: true})
	detailPage := clientui.TranscriptPage{SessionID: "session-1", Offset: 0, TotalEntries: 500}
	for i := 0; i < 250; i++ {
		detailPage.Entries = append(detailPage.Entries, clientui.ChatEntry{Role: "assistant", Text: fmt.Sprintf("history %03d", i)})
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{Offset: 0, Limit: 250}, detailPage); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}

	if !reflect.DeepEqual(m.nativeProjection, baselineProjection) {
		t.Fatal("detail transcript apply unexpectedly changed native projection state")
	}
	if !reflect.DeepEqual(m.nativeRenderedProjection, baselineRenderedProjection) {
		t.Fatal("detail transcript apply unexpectedly changed rendered native projection state")
	}
	if m.nativeRenderedSnapshot != baselineRenderedSnapshot {
		t.Fatalf("detail transcript apply changed rendered native snapshot: %q -> %q", baselineRenderedSnapshot, m.nativeRenderedSnapshot)
	}
	if m.nativeFlushedEntryCount != baselineFlushedEntryCount {
		t.Fatalf("detail transcript apply changed native flushed entry count: %d -> %d", baselineFlushedEntryCount, m.nativeFlushedEntryCount)
	}
}

func TestApplyRuntimeTranscriptPageResetsDetailWindowOnSessionChange(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 12
	m.windowSizeKnown = true

	pageA := clientui.TranscriptPage{SessionID: "session-a", Offset: 100, TotalEntries: 400}
	for i := 0; i < 250; i++ {
		pageA.Entries = append(pageA.Entries, clientui.ChatEntry{Role: "assistant", Text: fmt.Sprintf("a-%03d", 100+i)})
	}
	m.detailTranscript.replace(pageA)
	m.forwardToView(tui.SetConversationMsg{BaseOffset: pageA.Offset, TotalEntries: pageA.TotalEntries, Entries: transcriptEntriesFromPage(pageA)})
	m.forwardToView(tui.SetModeMsg{Mode: tui.ModeDetail, SkipDetailWarmup: true})
	m.sessionID = "session-a"

	pageB := clientui.TranscriptPage{
		SessionID:    "session-b",
		SessionName:  "Session B",
		Offset:       0,
		TotalEntries: 2,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "b-000"}, {Role: "assistant", Text: "b-001"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{Offset: 0, Limit: 2}, pageB); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}

	if got := m.detailTranscript.sessionID; got != "session-b" {
		t.Fatalf("detail transcript session id = %q, want session-b", got)
	}
	if got := m.detailTranscript.offset; got != 0 {
		t.Fatalf("detail transcript offset = %d, want 0", got)
	}
	if got := m.detailTranscript.totalEntries; got != 2 {
		t.Fatalf("detail transcript total entries = %d, want 2", got)
	}
	if got := len(m.detailTranscript.entries); got != 2 {
		t.Fatalf("detail transcript entry count = %d, want 2", got)
	}
	if got := m.detailTranscript.entries[0].Text; got != "b-000" {
		t.Fatalf("first detail transcript entry = %q, want b-000", got)
	}
	if got := stripANSIAndTrimRight(m.View()); strings.Contains(got, "a-100") || !strings.Contains(got, "b-000") {
		t.Fatalf("detail view leaked prior session transcript, got %q", got)
	}
}

func TestApplyRuntimeTranscriptPageRejectsEqualRevisionTailReplacementAfterLiveAppend(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	baseline := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "seed"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, baseline); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}
	if got := m.transcriptRevision; got != 10 {
		t.Fatalf("transcript revision = %d, want 10", got)
	}

	if cmd, mutated := m.runtimeAdapter().applyProjectedTranscriptEntries(clientui.Event{Kind: clientui.EventAssistantMessage, TranscriptEntries: []clientui.ChatEntry{{Role: "assistant", Text: "live append"}}}, false); cmd != nil || !mutated {
		t.Fatalf("expected live append without extra command, mutated=%t cmd=%v", mutated, cmd)
	}
	if !m.transcriptLiveDirty {
		t.Fatal("expected live append to mark transcript live-dirty")
	}

	stale := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "seed"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, stale); cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("expected stale equal-revision page to be ignored, got %T", msg)
		}
	}
	if got, want := len(m.transcriptEntries), 2; got != want {
		t.Fatalf("transcript entry count = %d, want %d", got, want)
	}
	if got := m.transcriptEntries[1].Text; got != "live append" {
		t.Fatalf("second transcript entry = %q, want live append", got)
	}
	if got := stripANSIAndTrimRight(m.view.OngoingSnapshot()); !strings.Contains(got, "live append") {
		t.Fatalf("expected view to preserve live append, got %q", got)
	}
}

func TestApplyRuntimeTranscriptPageRejectsOlderRevisionTailReplacement(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	current := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     11,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "newer"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, current); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}

	older := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "older"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, older); cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("expected older-revision page to be ignored, got %T", msg)
		}
	}
	if got := m.transcriptRevision; got != 11 {
		t.Fatalf("transcript revision = %d, want 11", got)
	}
	if got, want := len(m.transcriptEntries), 1; got != want {
		t.Fatalf("transcript entry count = %d, want %d", got, want)
	}
	if got := m.transcriptEntries[0].Text; got != "newer" {
		t.Fatalf("transcript entry = %q, want newer", got)
	}
}

func TestApplyRuntimeTranscriptPageRejectsEqualRevisionTailReplacementThatClearsLiveOngoing(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	baseline := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "seed"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, baseline); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "working"})
	if got := m.view.OngoingStreamingText(); got != "working" {
		t.Fatalf("ongoing streaming text = %q, want working", got)
	}

	stale := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "seed"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, stale); cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("expected stale equal-revision page to be ignored, got %T", msg)
		}
	}
	if got := m.view.OngoingStreamingText(); got != "working" {
		t.Fatalf("expected live ongoing stream preserved, got %q", got)
	}
}

func TestApplyRuntimeTranscriptPageAcceptsNewerRevisionTailReplacementThatClearsLiveOngoing(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	baseline := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "seed"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, baseline); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "working"})

	fresh := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     11,
		Offset:       0,
		TotalEntries: 2,
		Entries: []clientui.ChatEntry{
			{Role: "assistant", Text: "seed"},
			{Role: "assistant", Text: "done", Phase: string(llm.MessagePhaseFinal)},
		},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, fresh); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}
	if got := m.view.OngoingStreamingText(); got != "" {
		t.Fatalf("expected fresh authoritative page to clear live ongoing, got %q", got)
	}
	if got := m.transcriptRevision; got != 11 {
		t.Fatalf("transcript revision = %d, want 11", got)
	}
}

func TestProjectedAssistantMessageAdvancesTranscriptRevisionForReplayDedupe(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.transcriptEntries = []tui.TranscriptEntry{{Role: "assistant", Text: "seed"}}
	m.transcriptBaseOffset = 0
	m.transcriptTotalEntries = 1
	m.transcriptRevision = 10
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})

	evt := clientui.Event{
		Kind:                clientui.EventAssistantMessage,
		TranscriptRevision:  11,
		CommittedEntryCount: 2,
		TranscriptEntries: []clientui.ChatEntry{{
			Role:  "assistant",
			Text:  "live append",
			Phase: string(llm.MessagePhaseFinal),
		}},
	}
	if cmd := m.runtimeAdapter().handleProjectedRuntimeEvent(evt); cmd == nil {
		t.Fatal("expected native replay command for projected assistant message")
	}
	if got := m.transcriptRevision; got != 11 {
		t.Fatalf("transcript revision after live append = %d, want 11", got)
	}
	if got := len(m.transcriptEntries); got != 2 {
		t.Fatalf("transcript entry count after live append = %d, want 2", got)
	}

	if cmd := m.runtimeAdapter().handleProjectedRuntimeEvent(evt); cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("expected replayed assistant message to be skipped, got %T", msg)
		}
	}
	if got := len(m.transcriptEntries); got != 2 {
		t.Fatalf("expected replayed assistant message to stay deduped, got %d entries", got)
	}
}

func TestApplyRuntimeTranscriptPageAcceptsEqualRevisionTailReplacementWhenRuntimeOnlyEntryChanged(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	baseline := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "seed"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, baseline); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}
	m.transcriptLiveDirty = true

	runtimeOnly := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 2,
		Entries: []clientui.ChatEntry{
			{Role: "assistant", Text: "seed"},
			{Role: "error", Text: "background continuation failed: boom"},
		},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, runtimeOnly); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}

	if got := len(m.transcriptEntries); got != 2 {
		t.Fatalf("transcript entry count = %d, want 2", got)
	}
	if got := m.transcriptEntries[1].Text; got != "background continuation failed: boom" {
		t.Fatalf("runtime-only entry text = %q, want background continuation failed: boom", got)
	}
	if m.transcriptLiveDirty {
		t.Fatal("expected accepted equal-revision tail refresh to clear transcriptLiveDirty")
	}
}

func TestApplyRuntimeTranscriptPageAcceptsEqualRevisionTailReplacementWhenAuthoritativePageCorrectsOverlap(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	baseline := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "user", Text: "prompt"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, baseline); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}

	if cmd, mutated := m.runtimeAdapter().applyProjectedTranscriptEntries(clientui.Event{Kind: clientui.EventToolCallStarted, TranscriptEntries: []clientui.ChatEntry{{
		Role:       "tool_call",
		Text:       "pwd",
		ToolCallID: "stale-call",
		ToolCall:   &clientui.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "pwd"},
	}}}, false); cmd != nil || !mutated {
		t.Fatalf("expected live append without extra command, mutated=%t cmd=%v", mutated, cmd)
	}
	if !m.transcriptLiveDirty {
		t.Fatal("expected live append to mark transcript live-dirty")
	}

	corrected := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 3,
		Entries: []clientui.ChatEntry{
			{Role: "user", Text: "prompt"},
			{Role: "tool_call", Text: "pwd", ToolCallID: "call-1", ToolCall: &clientui.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "pwd"}},
			{Role: "tool_result_ok", Text: "/tmp", ToolCallID: "call-1"},
		},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, corrected); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}

	if got, want := len(m.transcriptEntries), 3; got != want {
		t.Fatalf("transcript entry count = %d, want %d", got, want)
	}
	if got := m.transcriptEntries[1].ToolCallID; got != "call-1" {
		t.Fatalf("corrected tool call id = %q, want call-1", got)
	}
	if got := m.transcriptEntries[2].ToolCallID; got != "call-1" {
		t.Fatalf("corrected tool result id = %q, want call-1", got)
	}
	if m.transcriptLiveDirty {
		t.Fatal("expected corrective equal-revision refresh to clear transcriptLiveDirty")
	}
	rawCommitted := renderStyledNativeProjection(m.nativeProjection, m.theme, m.termWidth)
	if plain := stripANSIPreserve(rawCommitted); !strings.Contains(plain, "$ pwd") {
		t.Fatalf("expected corrected shell row in committed native projection, got %q", plain)
	}
	assertContainsColoredShellSymbol(t, rawCommitted, "dark success", transcriptToolSuccessColorHex("dark"))
	assertNoColoredShellSymbol(t, rawCommitted, "dark pending", transcriptToolPendingColorHex("dark"))
}

func TestApplyRuntimeTranscriptPageAcceptsEqualRevisionTailReplacementWhenOngoingErrorChanged(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	baseline := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "seed"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, baseline); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}
	m.transcriptLiveDirty = true

	runtimeOnly := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "seed"}},
		OngoingError: "background continuation failed",
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, runtimeOnly); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}

	if got := m.view.OngoingErrorText(); got != "background continuation failed" {
		t.Fatalf("ongoing error text = %q, want background continuation failed", got)
	}
	if m.transcriptLiveDirty {
		t.Fatal("expected accepted equal-revision ongoing-error refresh to clear transcriptLiveDirty")
	}
}

func TestApplyRuntimeTranscriptPageAcceptsEqualRevisionTailReplacementWhenOngoingErrorCleared(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	baseline := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "seed"}},
		OngoingError: "background continuation failed",
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, baseline); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}
	m.transcriptLiveDirty = true

	cleared := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "seed"}},
		OngoingError: "",
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, cleared); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}

	if got := m.view.OngoingErrorText(); got != "" {
		t.Fatalf("ongoing error text = %q, want empty", got)
	}
	if m.transcriptLiveDirty {
		t.Fatal("expected accepted equal-revision ongoing-error clear to clear transcriptLiveDirty")
	}
	if got := len(m.transcriptEntries); got != 1 {
		t.Fatalf("transcript entry count = %d, want 1", got)
	}
}

func TestApplyRuntimeTranscriptPageRejectsEqualRevisionShiftedTailReplacement(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	baseline := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 2,
		Entries: []clientui.ChatEntry{
			{Role: "assistant", Text: "seed-0"},
			{Role: "assistant", Text: "seed-1"},
		},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, baseline); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}
	m.transcriptLiveDirty = true

	shifted := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       1,
		TotalEntries: 2,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "seed-1"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, shifted); cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("expected shifted equal-revision page to be ignored, got %T", msg)
		}
	}

	if got := m.transcriptBaseOffset; got != 0 {
		t.Fatalf("transcript base offset = %d, want 0", got)
	}
	if got := len(m.transcriptEntries); got != 2 {
		t.Fatalf("transcript entry count = %d, want 2", got)
	}
	if got := m.transcriptEntries[0].Text; got != "seed-0" {
		t.Fatalf("first transcript entry = %q, want seed-0", got)
	}
	if !m.transcriptLiveDirty {
		t.Fatal("expected rejected shifted equal-revision page to preserve transcriptLiveDirty")
	}
}

func TestApplyRuntimeTranscriptPageAcceptsNewerRevisionTailReplacementAfterLiveAppend(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	baseline := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "seed"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, baseline); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}
	if cmd, mutated := m.runtimeAdapter().applyProjectedTranscriptEntries(clientui.Event{Kind: clientui.EventAssistantMessage, TranscriptEntries: []clientui.ChatEntry{{Role: "assistant", Text: "live append"}}}, false); cmd != nil || !mutated {
		t.Fatalf("expected live append without extra command, mutated=%t cmd=%v", mutated, cmd)
	}

	fresh := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     11,
		Offset:       0,
		TotalEntries: 2,
		Entries: []clientui.ChatEntry{
			{Role: "assistant", Text: "seed"},
			{Role: "assistant", Text: "live append"},
		},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, fresh); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}
	if got := m.transcriptRevision; got != 11 {
		t.Fatalf("transcript revision = %d, want 11", got)
	}
	if m.transcriptLiveDirty {
		t.Fatal("expected fresh authoritative page to clear live-dirty state")
	}
	if got, want := len(m.transcriptEntries), 2; got != want {
		t.Fatalf("transcript entry count = %d, want %d", got, want)
	}
	if got := m.transcriptEntries[1].Text; got != "live append" {
		t.Fatalf("second transcript entry = %q, want live append", got)
	}
}

func TestApplyProjectedTranscriptEntriesUsesTailOffsetWhileViewingOlderDetailPage(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	ongoingTail := clientui.TranscriptPage{SessionID: "session-1", Offset: 300, TotalEntries: 500}
	for i := 0; i < 200; i++ {
		ongoingTail.Entries = append(ongoingTail.Entries, clientui.ChatEntry{Role: "assistant", Text: fmt.Sprintf("tail %03d", 300+i)})
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{Window: clientui.TranscriptWindowOngoingTail}, ongoingTail); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}

	m.forwardToView(tui.SetModeMsg{Mode: tui.ModeDetail, SkipDetailWarmup: true})
	olderDetailPage := clientui.TranscriptPage{SessionID: "session-1", Offset: 0, TotalEntries: 500}
	for i := 0; i < 250; i++ {
		olderDetailPage.Entries = append(olderDetailPage.Entries, clientui.ChatEntry{Role: "assistant", Text: fmt.Sprintf("history %03d", i)})
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{Offset: 0, Limit: 250}, olderDetailPage); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}

	if m.view.TranscriptBaseOffset() != 0 {
		t.Fatalf("expected detail view to remain on older page, got base=%d", m.view.TranscriptBaseOffset())
	}
	if got := m.transcriptBaseOffset; got != ongoingTail.Offset {
		t.Fatalf("live tail base offset = %d, want %d", got, ongoingTail.Offset)
	}

	appended := []clientui.ChatEntry{{Role: "assistant", Text: "tail 500"}, {Role: "assistant", Text: "tail 501"}}
	if cmd, mutated := m.runtimeAdapter().applyProjectedTranscriptEntries(clientui.Event{Kind: clientui.EventAssistantMessage, TranscriptEntries: appended}, false); cmd != nil || !mutated {
		t.Fatalf("expected projected append to mutate without extra command, mutated=%t cmd=%v", mutated, cmd)
	}

	if got, want := len(m.transcriptEntries), 202; got != want {
		t.Fatalf("live tail entry count = %d, want %d", got, want)
	}
	if got := m.transcriptEntries[len(m.transcriptEntries)-2].Text; got != "tail 500" {
		t.Fatalf("expected first appended tail entry at live tail end, got %q", got)
	}
	if got := m.transcriptEntries[len(m.transcriptEntries)-1].Text; got != "tail 501" {
		t.Fatalf("expected second appended tail entry at live tail end, got %q", got)
	}
	if got, want := m.transcriptTotalEntries, 502; got != want {
		t.Fatalf("live tail total entries = %d, want %d", got, want)
	}
	if got, want := m.detailTranscript.totalEntries, 502; got != want {
		t.Fatalf("detail transcript total entries = %d, want %d", got, want)
	}
	if got, want := m.detailTranscript.offset, 500; got != want {
		t.Fatalf("detail transcript offset = %d, want %d", got, want)
	}
	if got, want := len(m.detailTranscript.entries), 2; got != want {
		t.Fatalf("detail transcript entry count = %d, want %d", got, want)
	}
	if got := m.detailTranscript.entries[0].Text; got != "tail 500" {
		t.Fatalf("expected first appended detail transcript entry at live tail offset, got %q", got)
	}
	if got := m.detailTranscript.entries[1].Text; got != "tail 501" {
		t.Fatalf("expected second appended detail transcript entry at live tail offset, got %q", got)
	}
	if got := m.view.TranscriptBaseOffset(); got != 0 {
		t.Fatalf("view base offset changed unexpectedly after live append: %d", got)
	}
}

func TestStartupSeedsCachedTranscriptBeforeBoundedSync(t *testing.T) {
	client := &startupTranscriptRuntimeClient{
		view: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1", SessionName: "incident triage"}},
		page: clientui.TranscriptPage{SessionID: "session-1", Offset: 10, TotalEntries: 15, Entries: []clientui.ChatEntry{{Role: "assistant", Text: "cached tail"}}},
	}
	m := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())

	next, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	updated := next.(*uiModel)
	if startupCmd == nil {
		t.Fatal("expected startup transcript hydration command")
	}
	if client.transcriptCalls != 1 {
		t.Fatalf("expected startup to seed from cached RuntimeClient.Transcript(), got %d calls", client.transcriptCalls)
	}
	if got := stripANSIAndTrimRight(updated.view.OngoingSnapshot()); !strings.Contains(got, "cached tail") {
		t.Fatalf("expected cached transcript tail visible before bounded sync, got %q", got)
	}
	if updated.sessionName != "incident triage" {
		t.Fatalf("session name = %q, want incident triage", updated.sessionName)
	}
	if got := len(client.loadRequests); got != 0 {
		t.Fatalf("expected no bounded transcript load before startup cmd executes, got %d", got)
	}
	flushMsg, ok := startupCmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected startup window-size update to replay native history, got %T", startupCmd())
	}
	if !strings.Contains(stripANSIAndTrimRight(flushMsg.Text), "cached tail") {
		t.Fatalf("expected startup native replay to include cached tail, got %q", stripANSIAndTrimRight(flushMsg.Text))
	}
	if len(updated.startupCmds) != 1 || updated.startupCmds[0] == nil {
		t.Fatalf("expected queued bounded transcript sync command, got %d command(s)", len(updated.startupCmds))
	}
	refreshed, ok := updated.startupCmds[0]().(runtimeTranscriptRefreshedMsg)
	if !ok {
		t.Fatalf("expected queued startup sync to return runtimeTranscriptRefreshedMsg, got %T", updated.startupCmds[0]())
	}
	if refreshed.req.Window != clientui.TranscriptWindowOngoingTail {
		t.Fatalf("startup transcript request window = %q, want ongoing_tail", refreshed.req.Window)
	}
	if got, want := len(client.loadRequests), 1; got != want {
		t.Fatalf("load request count = %d, want %d", got, want)
	}
	if client.loadRequests[0].Window != clientui.TranscriptWindowOngoingTail {
		t.Fatalf("startup load request window = %q, want ongoing_tail", client.loadRequests[0].Window)
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

func TestApplyRuntimeTranscriptPageRejectsEqualRevisionReasoningClear(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "u"})
	m.forwardToView(tui.ToggleModeMsg{})

	baseline := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "user", Text: "u"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, baseline); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "Plan summary"}})
	if detail := stripANSIAndTrimRight(m.view.View()); !strings.Contains(detail, "Plan summary") {
		t.Fatalf("expected live reasoning visible before stale page apply, got %q", detail)
	}

	stale := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "user", Text: "u"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, stale); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}
	if detail := stripANSIAndTrimRight(m.view.View()); !strings.Contains(detail, "Plan summary") {
		t.Fatalf("expected stale equal-revision page to preserve live reasoning, got %q", detail)
	}
}

func TestApplyRuntimeTranscriptPageAcceptsNewerRevisionReasoningClear(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.forwardToView(tui.SetViewportSizeMsg{Lines: 20, Width: 80})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "u"})
	m.forwardToView(tui.ToggleModeMsg{})

	baseline := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		Offset:       0,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "user", Text: "u"}},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, baseline); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventReasoningDelta, ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "Plan summary"}})

	fresh := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     11,
		Offset:       0,
		TotalEntries: 2,
		Entries: []clientui.ChatEntry{
			{Role: "user", Text: "u"},
			{Role: "assistant", Text: "done", Phase: string(llm.MessagePhaseFinal)},
		},
	}
	if cmd := m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, fresh); cmd != nil {
		_ = collectCmdMessages(t, cmd)
	}
	if detail := stripANSIAndTrimRight(m.view.View()); strings.Contains(detail, "Plan summary") {
		t.Fatalf("expected newer authoritative page to clear live reasoning, got %q", detail)
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

	rawView := m.View()
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
	assertContainsColoredShellSymbol(t, rawView, "dark success", transcriptToolSuccessColorHex("dark"))
	assertNoColoredShellSymbol(t, rawView, "dark pending", transcriptToolPendingColorHex("dark"))
}

func TestUserMessageFlushedSyncsConversationForNativeReplay(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	cmd := m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventUserMessageFlushed, UserMessage: "steered message"})
	if cmd == nil {
		t.Fatal("expected native replay command for flushed user message")
	}
	flushMsg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
	if got := len(m.transcriptEntries); got != 1 {
		t.Fatalf("expected immediate transcript append, got %d entries", got)
	}
	if got := m.transcriptEntries[0].Text; got != "steered message" {
		t.Fatalf("transcript entry text = %q, want steered message", got)
	}
	if !strings.Contains(stripANSIPreserve(flushMsg.Text), "steered message") {
		t.Fatalf("expected flushed replay text to include steered message, got %q", flushMsg.Text)
	}
}

func TestUserMessageFlushedAlreadyCoveredByAuthoritativeTailDoesNotDuplicateNativeReplay(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.transcriptEntries = []tui.TranscriptEntry{{Role: "user", Text: "steered message"}}
	m.transcriptBaseOffset = 0
	m.transcriptTotalEntries = 1
	m.transcriptRevision = 10
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})

	cmd := m.runtimeAdapter().handleProjectedRuntimeEvent(clientui.Event{
		Kind:                clientui.EventUserMessageFlushed,
		UserMessage:         "steered message",
		TranscriptRevision:  10,
		CommittedEntryCount: 1,
		TranscriptEntries: []clientui.ChatEntry{{
			Role: "user",
			Text: "steered message",
		}},
	})
	if len(m.transcriptEntries) != 1 {
		t.Fatalf("expected stale flushed user message to be skipped, got %+v", m.transcriptEntries)
	}
	if cmd != nil {
		if _, ok := cmd().(nativeHistoryFlushMsg); ok {
			t.Fatal("expected no duplicate native replay after authoritative tail already covered the user message")
		}
	}
}

func TestProjectedUserMessageFlushedWithSameTextAndNewCommittedCountAppendsDistinctEntry(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.transcriptEntries = []tui.TranscriptEntry{{Role: "user", Text: "steered message"}}
	m.transcriptBaseOffset = 0
	m.transcriptTotalEntries = 1
	m.transcriptRevision = 10
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})

	cmd := m.runtimeAdapter().handleProjectedRuntimeEvent(clientui.Event{
		Kind:                clientui.EventUserMessageFlushed,
		UserMessage:         "steered message",
		TranscriptRevision:  11,
		CommittedEntryCount: 2,
		TranscriptEntries: []clientui.ChatEntry{{
			Role: "user",
			Text: "steered message",
		}},
	})
	if len(m.transcriptEntries) != 2 {
		t.Fatalf("expected repeated same-text user message to append distinctly, got %+v", m.transcriptEntries)
	}
	if cmd == nil {
		t.Fatal("expected native replay command for new committed user message")
	}
	if _, ok := cmd().(nativeHistoryFlushMsg); !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
}

func TestProjectedUserMessageFlushedDoesNotScheduleTranscriptRefresh(t *testing.T) {
	client := &runtimeControlFakeClient{}
	m := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	cmd := m.runtimeAdapter().handleProjectedRuntimeEvent(clientui.Event{
		Kind:        clientui.EventUserMessageFlushed,
		UserMessage: "steered message",
		TranscriptEntries: []clientui.ChatEntry{{
			Role: "user",
			Text: "steered message",
		}},
	})
	msgs := collectCmdMessages(t, cmd)
	for _, msg := range msgs {
		if _, ok := msg.(runtimeTranscriptRefreshedMsg); ok {
			t.Fatalf("did not expect transcript refresh after flushed user message, got %+v", msgs)
		}
	}
	if got := len(m.transcriptEntries); got != 1 {
		t.Fatalf("expected immediate transcript append, got %d entries", got)
	}
	if got := m.transcriptEntries[0].Text; got != "steered message" {
		t.Fatalf("transcript entry text = %q, want steered message", got)
	}
}

func TestProjectedUserMessageFlushedRecordsPromptHistoryWithoutTranscriptRefresh(t *testing.T) {
	client := &runtimeControlFakeClient{}
	m := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())
	m.pendingInjected = []string{"steered message", "follow-up"}
	m.input = "steered message"
	m.lockedInjectText = "steered message"
	m.inputSubmitLocked = true

	cmd := m.runtimeAdapter().handleProjectedRuntimeEvent(clientui.Event{
		Kind:        clientui.EventUserMessageFlushed,
		UserMessage: "steered message",
		TranscriptEntries: []clientui.ChatEntry{{
			Role: "user",
			Text: "steered message",
		}},
	})
	msgs := collectCmdMessages(t, cmd)
	for _, msg := range msgs {
		if _, ok := msg.(runtimeTranscriptRefreshedMsg); ok {
			t.Fatalf("did not expect transcript refresh after flushed injected user message, got %+v", msgs)
		}
	}
	if client.recordedPromptHistory != "steered message" {
		t.Fatalf("expected prompt history recorded, got %q", client.recordedPromptHistory)
	}
	if len(m.pendingInjected) != 1 || m.pendingInjected[0] != "follow-up" {
		t.Fatalf("expected pending injected queue advanced, got %+v", m.pendingInjected)
	}
	if m.input != "" {
		t.Fatalf("expected locked input cleared, got %q", m.input)
	}
	if m.inputSubmitLocked {
		t.Fatal("expected input submit lock cleared")
	}
}

func TestProjectedUserMessageFlushedDoesNotClobberLaterAssistantDelta(t *testing.T) {
	client := &runtimeControlFakeClient{
		transcript: clientui.TranscriptPage{
			SessionID: "session-1",
			Entries: []clientui.ChatEntry{{
				Role: "user",
				Text: "steered message",
			}},
		},
	}
	m := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true

	cmd := m.runtimeAdapter().handleProjectedRuntimeEvent(clientui.Event{
		Kind:        clientui.EventUserMessageFlushed,
		UserMessage: "steered message",
		TranscriptEntries: []clientui.ChatEntry{{
			Role: "user",
			Text: "steered message",
		}},
	})
	msgs := collectCmdMessages(t, cmd)
	for _, msg := range msgs {
		if _, ok := msg.(runtimeTranscriptRefreshedMsg); ok {
			t.Fatalf("did not expect transcript refresh after flushed user message, got %+v", msgs)
		}
	}

	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventAssistantDelta, AssistantDelta: "working"})
	if got := m.view.OngoingStreamingText(); got != "working" {
		t.Fatalf("ongoing streaming text = %q, want working", got)
	}
	if !strings.Contains(stripANSIPreserve(m.View()), "working") {
		t.Fatalf("expected assistant delta visible in view, got %q", stripANSIPreserve(m.View()))
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
