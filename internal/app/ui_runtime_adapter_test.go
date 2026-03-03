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
