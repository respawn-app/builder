package runtimecontrol

import (
	"context"
	"encoding/json"
	"testing"

	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/serverapi"
)

func TestServiceSubmitUserMessageReplaysSuccessfulRetryAfterLeaseRotation(t *testing.T) {
	store, err := session.Create(t.TempDir(), "workspace-x", "/tmp/workspace-x")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	client := &runtimeControlFakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	engine, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("create runtime engine: %v", err)
	}
	service := NewService(stubRuntimeResolver{engine: engine}, nil)
	first := serverapi.RuntimeSubmitUserMessageRequest{ClientRequestID: "req-1", SessionID: store.Meta().SessionID, ControllerLeaseID: "lease-1", Text: "hello"}

	firstResp, err := service.SubmitUserMessage(context.Background(), first)
	if err != nil {
		t.Fatalf("SubmitUserMessage first: %v", err)
	}
	second := first
	second.ControllerLeaseID = "lease-2"
	secondResp, err := service.SubmitUserMessage(context.Background(), second)
	if err != nil {
		t.Fatalf("SubmitUserMessage replay after lease rotation: %v", err)
	}
	if firstResp != secondResp {
		t.Fatalf("responses = (%+v, %+v), want identical replay", firstResp, secondResp)
	}
	if client.calls != 1 {
		t.Fatalf("generate call count = %d, want 1", client.calls)
	}
}

func TestServiceSubmitUserShellCommandReplaysSuccessfulRetryAfterLeaseRotation(t *testing.T) {
	store, err := session.Create(t.TempDir(), "workspace-x", "/tmp/workspace-x")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	engine, err := runtime.New(store, &runtimeControlFakeClient{}, tools.NewRegistry(fakeShellHandler{}), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("create runtime engine: %v", err)
	}
	service := NewService(stubRuntimeResolver{engine: engine}, nil)
	first := serverapi.RuntimeSubmitUserShellCommandRequest{ClientRequestID: "req-1", SessionID: store.Meta().SessionID, ControllerLeaseID: "lease-1", Command: "pwd"}

	if err := service.SubmitUserShellCommand(context.Background(), first); err != nil {
		t.Fatalf("SubmitUserShellCommand first: %v", err)
	}
	second := first
	second.ControllerLeaseID = "lease-2"
	if err := service.SubmitUserShellCommand(context.Background(), second); err != nil {
		t.Fatalf("SubmitUserShellCommand replay after lease rotation: %v", err)
	}
	if got := countDirectShellCommandMessages(t, store, "pwd"); got != 1 {
		t.Fatalf("direct shell message count = %d, want 1", got)
	}
}

func TestServiceAppendLocalEntryDedupesSuccessfulRetry(t *testing.T) {
	store, err := session.Create(t.TempDir(), "workspace-x", "/tmp/workspace-x")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	engine, err := runtime.New(store, &runtimeControlFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("create runtime engine: %v", err)
	}
	service := NewService(stubRuntimeResolver{engine: engine}, nil)
	req := serverapi.RuntimeAppendLocalEntryRequest{ClientRequestID: "req-1", SessionID: store.Meta().SessionID, ControllerLeaseID: "lease-1", Role: "warning", Text: "be careful"}

	if err := service.AppendLocalEntry(context.Background(), req); err != nil {
		t.Fatalf("AppendLocalEntry first: %v", err)
	}
	if err := service.AppendLocalEntry(context.Background(), req); err != nil {
		t.Fatalf("AppendLocalEntry replay: %v", err)
	}
	count := 0
	for _, entry := range engine.ChatSnapshot().Entries {
		if entry.Role == "warning" && entry.Text == "be careful" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("local entry count = %d, want 1", count)
	}
}

func TestServiceSubmitQueuedUserMessagesDedupesSuccessfulRetry(t *testing.T) {
	store, err := session.Create(t.TempDir(), "workspace-x", "/tmp/workspace-x")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	client := &runtimeControlFakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	engine, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("create runtime engine: %v", err)
	}
	engine.QueueUserMessage("hello")
	service := NewService(stubRuntimeResolver{engine: engine}, nil)
	req := serverapi.RuntimeSubmitQueuedUserMessagesRequest{ClientRequestID: "req-1", SessionID: store.Meta().SessionID, ControllerLeaseID: "lease-1"}

	first, err := service.SubmitQueuedUserMessages(context.Background(), req)
	if err != nil {
		t.Fatalf("SubmitQueuedUserMessages first: %v", err)
	}
	second, err := service.SubmitQueuedUserMessages(context.Background(), req)
	if err != nil {
		t.Fatalf("SubmitQueuedUserMessages replay: %v", err)
	}
	if first != second {
		t.Fatalf("responses = (%+v, %+v), want identical replay", first, second)
	}
	if client.calls != 1 {
		t.Fatalf("generate call count = %d, want 1", client.calls)
	}
	if got := countUserMessagesWithContent(t, store, "hello"); got != 1 {
		t.Fatalf("queued user flush count = %d, want 1", got)
	}
}

func TestServiceDiscardQueuedUserMessagesMatchingDedupesSuccessfulRetry(t *testing.T) {
	store, err := session.Create(t.TempDir(), "workspace-x", "/tmp/workspace-x")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	engine, err := runtime.New(store, &runtimeControlFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("create runtime engine: %v", err)
	}
	engine.QueueUserMessage("same")
	engine.QueueUserMessage("other")
	engine.QueueUserMessage("same")
	service := NewService(stubRuntimeResolver{engine: engine}, nil)
	req := serverapi.RuntimeDiscardQueuedUserMessagesMatchingRequest{ClientRequestID: "req-1", SessionID: store.Meta().SessionID, ControllerLeaseID: "lease-1", Text: "same"}

	first, err := service.DiscardQueuedUserMessagesMatching(context.Background(), req)
	if err != nil {
		t.Fatalf("DiscardQueuedUserMessagesMatching first: %v", err)
	}
	second, err := service.DiscardQueuedUserMessagesMatching(context.Background(), req)
	if err != nil {
		t.Fatalf("DiscardQueuedUserMessagesMatching replay: %v", err)
	}
	if first.Discarded != 2 || second.Discarded != 2 {
		t.Fatalf("discard counts = (%d, %d), want both 2", first.Discarded, second.Discarded)
	}
	if hasQueued := engine.HasQueuedUserWork(); !hasQueued {
		t.Fatal("expected unmatched queued message to remain after discard replay")
	}
	if removed := engine.DiscardQueuedUserMessagesMatching("other"); removed != 1 {
		t.Fatalf("remaining queued messages removed = %d, want 1", removed)
	}
}

func TestServiceRecordPromptHistoryDedupesSuccessfulRetry(t *testing.T) {
	store, err := session.Create(t.TempDir(), "workspace-x", "/tmp/workspace-x")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	engine, err := runtime.New(store, &runtimeControlFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("create runtime engine: %v", err)
	}
	service := NewService(stubRuntimeResolver{engine: engine}, nil)
	req := serverapi.RuntimeRecordPromptHistoryRequest{ClientRequestID: "req-1", SessionID: store.Meta().SessionID, ControllerLeaseID: "lease-1", Text: "/resume"}

	if err := service.RecordPromptHistory(context.Background(), req); err != nil {
		t.Fatalf("RecordPromptHistory first: %v", err)
	}
	if err := service.RecordPromptHistory(context.Background(), req); err != nil {
		t.Fatalf("RecordPromptHistory replay: %v", err)
	}
	if got := countPromptHistoryEvents(t, store, "/resume"); got != 1 {
		t.Fatalf("prompt history count = %d, want 1", got)
	}
}

func countPromptHistoryEvents(t *testing.T, store *session.Store, text string) int {
	t.Helper()
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	count := 0
	for _, evt := range events {
		if evt.Kind != "prompt_history" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(evt.Payload, &payload); err != nil {
			t.Fatalf("decode prompt_history: %v", err)
		}
		if payload["text"] == text {
			count++
		}
	}
	return count
}
