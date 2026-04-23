package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"builder/server/llm"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/toolspec"
)

func TestSubmitUserMessageDoesNotEmitCommittedConversationUpdatedAfterFlushedUserTurn(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	events := make([]Event, 0, 16)
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:   "gpt-5",
		OnEvent: func(evt Event) { events = append(events, evt) },
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "hello"); err != nil {
		t.Fatalf("submit user message: %v", err)
	}
	if got := committedConversationUpdatedCountAfterLastUserFlush(events); got != 0 {
		t.Fatalf("committed conversation_updated count after user flush = %d, want 0; events=%+v", got, events)
	}
}

func TestSubmitUserMessageWithToolCallDoesNotEmitCommittedConversationUpdatedAfterUserFlush(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{ID: "call-1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	events := make([]Event, 0, 32)
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:   "gpt-5",
		OnEvent: func(evt Event) { events = append(events, evt) },
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "run tool"); err != nil {
		t.Fatalf("submit user message: %v", err)
	}
	if got := committedConversationUpdatedCountAfterLastUserFlush(events); got != 0 {
		t.Fatalf("committed conversation_updated count after user flush = %d, want 0; events=%+v", got, events)
	}
	if !hasEventKind(events, EventToolCallCompleted) {
		t.Fatalf("expected tool_call_completed event, got %+v", events)
	}
	if !hasEventKind(events, EventAssistantMessage) {
		t.Fatalf("expected assistant_message event, got %+v", events)
	}
}

func TestHostedToolOnlyTurnEmitsCommittedConversationUpdatedBeforeFollowUpAssistantMessage(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: ""},
			OutputItems: []llm.ResponseItem{{
				Type: llm.ResponseItemTypeOther,
				Raw:  json.RawMessage(`{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"builder cli"}}`),
			}},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	client.caps = llm.ProviderCapabilities{
		ProviderID:                    "openai",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      true,
		SupportsNativeWebSearch:       true,
		SupportsReasoningEncrypted:    true,
		SupportsServerSideContextEdit: true,
		IsOpenAIFirstParty:            true,
	}
	events := make([]Event, 0, 24)
	autoCompactionEnabled := false
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                 "gpt-5",
		WebSearchMode:         "native",
		EnabledTools:          []toolspec.ID{toolspec.ToolWebSearch},
		AutoCompactionEnabled: &autoCompactionEnabled,
		OnEvent:               func(evt Event) { events = append(events, evt) },
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	msg, err := eng.SubmitUserMessage(context.Background(), "find latest")
	if err != nil {
		t.Fatalf("submit user message: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("assistant content = %q, want done", msg.Content)
	}
	if got := committedConversationUpdatedCountAfterLastUserFlush(events); got != 1 {
		t.Fatalf("committed conversation_updated count after hosted-tool-only turn = %d, want 1; events=%+v", got, events)
	}
	if !hasEventKind(events, EventConversationUpdated) {
		t.Fatalf("expected committed conversation_updated event, got %+v", events)
	}
	if !hasEventKind(events, EventAssistantMessage) {
		t.Fatalf("expected assistant message event after hosted-tool-only turn, got %+v", events)
	}
}

func TestHostedToolOnlyMissingPhaseTurnEmitsCommittedConversationUpdatedAfterHostedPersistence(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: ""},
			OutputItems: []llm.ResponseItem{
				{Type: llm.ResponseItemTypeMessage, Role: llm.RoleAssistant, Content: "working"},
				{Type: llm.ResponseItemTypeOther, Raw: json.RawMessage(`{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"builder cli"}}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	client.caps = llm.ProviderCapabilities{
		ProviderID:                    "openai",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      true,
		SupportsNativeWebSearch:       true,
		SupportsReasoningEncrypted:    true,
		SupportsServerSideContextEdit: true,
		IsOpenAIFirstParty:            true,
	}
	events := make([]Event, 0, 24)
	autoCompactionEnabled := false
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                 "gpt-5",
		WebSearchMode:         "native",
		EnabledTools:          []toolspec.ID{toolspec.ToolWebSearch},
		AutoCompactionEnabled: &autoCompactionEnabled,
		OnEvent:               func(evt Event) { events = append(events, evt) },
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	msg, err := eng.SubmitUserMessage(context.Background(), "find latest")
	if err != nil {
		t.Fatalf("submit user message: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("assistant content = %q, want done", msg.Content)
	}
	if got := committedConversationUpdatedCountAfterLastUserFlush(events); got != 2 {
		t.Fatalf("committed conversation_updated count after missing-phase hosted-only turn = %d, want 2; events=%+v", got, events)
	}
}

func TestReviewerTranscriptPathsUseRichEventsWithoutCommittedConversationUpdatedAfterUserFlush(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	mainClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "original final", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "updated final after review", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":["Add final verification notes."]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	events := make([]Event, 0, 48)
	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			VerboseOutput: true,
			Client:        reviewerClient,
		},
		OnEvent: func(evt Event) { events = append(events, evt) },
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "do the task"); err != nil {
		t.Fatalf("submit user message: %v", err)
	}
	if got := committedConversationUpdatedCountAfterLastUserFlush(events); got != 0 {
		t.Fatalf("committed conversation_updated count after user flush = %d, want 0; events=%+v", got, events)
	}
	if !hasReviewerLocalEntryRole(events, "reviewer_suggestions") {
		t.Fatalf("expected reviewer_suggestions local entry event, got %+v", events)
	}
	if !hasReviewerLocalEntryRole(events, "reviewer_status") {
		t.Fatalf("expected reviewer_status local entry event, got %+v", events)
	}
	if !hasEventKind(events, EventReviewerCompleted) {
		t.Fatalf("expected reviewer_completed event, got %+v", events)
	}
	for _, evt := range events {
		if evt.Kind != EventReviewerCompleted {
			continue
		}
		if evt.CommittedTranscriptChanged {
			t.Fatalf("expected reviewer_completed to avoid committed transcript advancement, got %+v", evt)
		}
		if got := TranscriptEntriesFromEvent(evt); len(got) != 0 {
			t.Fatalf("expected reviewer_completed transcript entries to be empty, got %+v", got)
		}
	}
}

func committedConversationUpdatedCountAfterLastUserFlush(events []Event) int {
	start := 0
	for idx, evt := range events {
		if evt.Kind == EventUserMessageFlushed {
			start = idx
		}
	}
	count := 0
	for _, evt := range events[start:] {
		if evt.Kind == EventConversationUpdated && evt.CommittedTranscriptChanged {
			count++
		}
	}
	return count
}

func hasEventKind(events []Event, kind EventKind) bool {
	for _, evt := range events {
		if evt.Kind == kind {
			return true
		}
	}
	return false
}

func hasReviewerLocalEntryRole(events []Event, role string) bool {
	for _, evt := range events {
		if evt.Kind != EventLocalEntryAdded || evt.LocalEntry == nil {
			continue
		}
		if evt.LocalEntry.Role == role {
			return true
		}
	}
	return false
}
