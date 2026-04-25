package runtime

import (
	"builder/server/llm"
	"builder/server/tools"
	"builder/shared/toolspec"
	"builder/shared/transcript"
	"encoding/json"
	"reflect"
	"testing"
)

func TestChatStoreSnapshotKeepsLocalEntryOrderingWithDeveloperErrorFeedback(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "first"})
	s.appendLocalEntry("system", "local-between")
	s.appendMessage(llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: "warn"})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "done"})

	snap := s.snapshot()
	if len(snap.Entries) != 4 {
		t.Fatalf("expected 4 entries, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Role != "user" || snap.Entries[0].Text != "first" {
		t.Fatalf("unexpected entry[0]: %+v", snap.Entries[0])
	}
	if snap.Entries[1].Role != "system" || snap.Entries[1].Text != "local-between" {
		t.Fatalf("unexpected entry[1]: %+v", snap.Entries[1])
	}
	if snap.Entries[2].Role != string(transcript.EntryRoleDeveloperFeedback) || snap.Entries[2].Text != "warn" {
		t.Fatalf("unexpected entry[2]: %+v", snap.Entries[2])
	}
	if snap.Entries[3].Role != "assistant" || snap.Entries[3].Text != "done" {
		t.Fatalf("unexpected entry[3]: %+v", snap.Entries[3])
	}
}

func TestChatStoreSnapshotPlacesLocalEntriesAtInsertionPoint(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "first"})
	s.appendLocalEntry("error", "mid-error")
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "second"})

	snap := s.snapshot()
	if len(snap.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Role != "user" || snap.Entries[0].Text != "first" {
		t.Fatalf("unexpected first entry: %+v", snap.Entries[0])
	}
	if snap.Entries[1].Role != "error" || snap.Entries[1].Text != "mid-error" {
		t.Fatalf("expected local entry in middle, got %+v", snap.Entries[1])
	}
	if snap.Entries[2].Role != "assistant" || snap.Entries[2].Text != "second" {
		t.Fatalf("unexpected third entry: %+v", snap.Entries[2])
	}
}

func TestChatStoreSnapshotKeepsHistoryAcrossHistoryReplace(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "a"})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "b"})
	s.appendLocalEntry("error", "before replace")

	replacement := llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleUser, Content: "after replace"}})
	s.replaceHistory(replacement)
	s.appendLocalEntry("compaction_notice", "after replace notice")

	snap := s.snapshot()
	if len(snap.Entries) != 5 {
		t.Fatalf("expected preserved history plus projected replacement, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Role != "user" || snap.Entries[0].Text != "a" {
		t.Fatalf("unexpected first entry after replace: %+v", snap.Entries[0])
	}
	if snap.Entries[1].Role != "assistant" || snap.Entries[1].Text != "b" {
		t.Fatalf("unexpected second entry after replace: %+v", snap.Entries[1])
	}
	if snap.Entries[2].Role != "error" || snap.Entries[2].Text != "before replace" {
		t.Fatalf("unexpected third entry after replace: %+v", snap.Entries[2])
	}
	if snap.Entries[3].Role != "user" || snap.Entries[3].Text != "after replace" {
		t.Fatalf("unexpected projected replacement entry: %+v", snap.Entries[3])
	}
	if snap.Entries[4].Role != "compaction_notice" || snap.Entries[4].Text != "after replace notice" {
		t.Fatalf("expected new local entry after projected replacement, got %+v", snap.Entries[4])
	}
}

func TestChatStoreProviderHistoryStartsAtLastCompactionCheckpoint(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "before-1"})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "before-2"})

	replacement := []llm.ResponseItem{
		{Type: llm.ResponseItemTypeMessage, Role: llm.RoleDeveloper, Content: "ctx"},
		{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, MessageType: llm.MessageTypeCompactionSummary, Content: "compact-summary"},
	}
	s.replaceHistory(replacement)
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "after"})

	items := s.snapshotItems()
	if len(items) != 3 {
		t.Fatalf("expected 3 provider items, got %d (%+v)", len(items), items)
	}
	if items[0].Role != llm.RoleDeveloper || items[0].Content != "ctx" {
		t.Fatalf("unexpected replacement item[0]: %+v", items[0])
	}
	if items[1].Role != llm.RoleUser || items[1].Content != "compact-summary" {
		t.Fatalf("unexpected replacement item[1]: %+v", items[1])
	}
	if items[2].Role != llm.RoleUser || items[2].Content != "after" {
		t.Fatalf("expected post-compaction tail in provider history, got %+v", items[2])
	}

	snap := s.snapshot()
	if len(snap.Entries) != 5 {
		t.Fatalf("expected full transcript history plus projected compaction entries, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Role != "user" || snap.Entries[0].Text != "before-1" {
		t.Fatalf("unexpected visible entry[0]: %+v", snap.Entries[0])
	}
	if snap.Entries[1].Role != "assistant" || snap.Entries[1].Text != "before-2" {
		t.Fatalf("unexpected visible entry[1]: %+v", snap.Entries[1])
	}
	if snap.Entries[2].Role != string(transcript.EntryRoleDeveloperContext) || snap.Entries[2].Text != "ctx" {
		t.Fatalf("unexpected visible entry[0]: %+v", snap.Entries[0])
	}
	if snap.Entries[3].Role != string(transcript.EntryRoleCompactionSummary) || snap.Entries[3].Text != "compact-summary" {
		t.Fatalf("unexpected visible entry[3]: %+v", snap.Entries[3])
	}
	if snap.Entries[4].Role != "user" || snap.Entries[4].Text != "after" {
		t.Fatalf("unexpected visible entry[4]: %+v", snap.Entries[4])
	}
}

func TestChatStoreSnapshotKeepsProjectedEntriesAcrossMultipleCompactions(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "before"})
	s.replaceHistory([]llm.ResponseItem{{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, MessageType: llm.MessageTypeCompactionSummary, Content: "summary-1"}})
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "between"})
	s.replaceHistory([]llm.ResponseItem{{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, MessageType: llm.MessageTypeCompactionSummary, Content: "summary-2"}})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "after"})

	snap := s.snapshot()
	if len(snap.Entries) != 5 {
		t.Fatalf("expected full transcript across compactions, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Text != "before" || snap.Entries[1].Text != "summary-1" || snap.Entries[2].Text != "between" || snap.Entries[3].Text != "summary-2" || snap.Entries[4].Text != "after" {
		t.Fatalf("unexpected multi-compaction transcript: %+v", snap.Entries)
	}
}

func TestChatStoreProviderHistoryUsesMostRecentCompactionCheckpoint(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "before"})

	s.replaceHistory([]llm.ResponseItem{{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "summary-1"}})
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "between"})

	s.replaceHistory([]llm.ResponseItem{{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "summary-2"}})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "after"})

	items := s.snapshotItems()
	if len(items) != 2 {
		t.Fatalf("expected 2 provider items, got %d (%+v)", len(items), items)
	}
	if items[0].Content != "summary-2" || items[1].Content != "after" {
		t.Fatalf("expected latest replacement + tail, got %+v", items)
	}
}

func TestChatStoreSnapshotItemsPreservesMultiToolOutputOrdering(t *testing.T) {
	s := newChatStore()
	call1 := toolCallWithPresentation(t, s, llm.ToolCall{ID: "call-1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)})
	call2 := toolCallWithPresentation(t, s, llm.ToolCall{ID: "call-2", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"ls"}`)})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call1, call2}})
	s.recordToolCompletion(tools.Result{CallID: "call-1", Name: toolspec.ToolExecCommand, Output: json.RawMessage(`{"output":"/tmp"}`)})
	s.recordToolCompletion(tools.Result{CallID: "call-2", Name: toolspec.ToolExecCommand, Output: json.RawMessage(`{"output":"a.txt"}`)})

	items := s.snapshotItems()
	if len(items) != 4 {
		t.Fatalf("expected 4 provider items, got %d (%+v)", len(items), items)
	}
	if items[0].Type != llm.ResponseItemTypeFunctionCall || items[0].CallID != "call-1" {
		t.Fatalf("unexpected first item: %+v", items[0])
	}
	if items[1].Type != llm.ResponseItemTypeFunctionCall || items[1].CallID != "call-2" {
		t.Fatalf("unexpected second item: %+v", items[1])
	}
	if items[2].Type != llm.ResponseItemTypeFunctionCallOutput || items[2].CallID != "call-1" {
		t.Fatalf("unexpected third item: %+v", items[2])
	}
	if items[3].Type != llm.ResponseItemTypeFunctionCallOutput || items[3].CallID != "call-2" {
		t.Fatalf("unexpected fourth item: %+v", items[3])
	}
}

func TestChatStoreSnapshotItemsPreservesMixedMaterializedAndPendingToolOutputs(t *testing.T) {
	s := newChatStore()
	call1 := toolCallWithPresentation(t, s, llm.ToolCall{ID: "call-1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)})
	call2 := toolCallWithPresentation(t, s, llm.ToolCall{ID: "call-2", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"ls"}`)})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call1, call2}})
	s.recordToolCompletion(tools.Result{CallID: "call-1", Name: toolspec.ToolExecCommand, Output: json.RawMessage(`{"output":"/tmp"}`)})
	s.recordToolCompletion(tools.Result{CallID: "call-2", Name: toolspec.ToolExecCommand, Output: json.RawMessage(`{"output":"a.txt"}`)})
	s.appendMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "call-1", Name: string(toolspec.ToolExecCommand), Content: `{"output":"/tmp"}`})

	items := s.snapshotItems()
	if len(items) != 4 {
		t.Fatalf("expected 4 provider items, got %d (%+v)", len(items), items)
	}
	if items[0].Type != llm.ResponseItemTypeFunctionCall || items[0].CallID != "call-1" {
		t.Fatalf("unexpected item[0]: %+v", items[0])
	}
	if items[1].Type != llm.ResponseItemTypeFunctionCall || items[1].CallID != "call-2" {
		t.Fatalf("unexpected item[1]: %+v", items[1])
	}
	if items[2].Type != llm.ResponseItemTypeFunctionCallOutput || items[2].CallID != "call-1" {
		t.Fatalf("unexpected item[2]: %+v", items[2])
	}
	if items[3].Type != llm.ResponseItemTypeFunctionCallOutput || items[3].CallID != "call-2" {
		t.Fatalf("unexpected item[3]: %+v", items[3])
	}
}

func TestChatStoreSnapshotItemsMatchesItemsFromMessagesWhenFullyMaterialized(t *testing.T) {
	s := newChatStore()
	call1 := toolCallWithPresentation(t, s, llm.ToolCall{ID: "call-1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)})
	call2 := toolCallWithPresentation(t, s, llm.ToolCall{ID: "call-2", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"ls"}`)})
	messages := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call1, call2}},
		{Role: llm.RoleTool, ToolCallID: "call-1", Name: string(toolspec.ToolExecCommand), Content: `{"output":"/tmp"}`},
		{Role: llm.RoleTool, ToolCallID: "call-2", Name: string(toolspec.ToolExecCommand), Content: `{"output":"a.txt"}`},
	}
	for _, msg := range messages {
		s.appendMessage(msg)
	}
	want := llm.ItemsFromMessages(messages)
	if got := s.snapshotItems(); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshotItems mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

func TestChatStoreCommittedEntryCountTracksVisibleTranscript(t *testing.T) {
	s := newChatStore()
	if got := s.committedEntryCount(); got != 0 {
		t.Fatalf("initial committed entry count = %d, want 0", got)
	}

	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})
	if got := s.committedEntryCount(); got != 1 {
		t.Fatalf("after user message committed entry count = %d, want 1", got)
	}

	call := toolCallWithPresentation(t, s, llm.ToolCall{ID: "call-1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}})
	if got := s.committedEntryCount(); got != 2 {
		t.Fatalf("after assistant tool call committed entry count = %d, want 2", got)
	}

	s.recordToolCompletion(tools.Result{CallID: "call-1", Name: toolspec.ToolExecCommand, Output: json.RawMessage(`{"output":"/tmp"}`)})
	if got := s.committedEntryCount(); got != 3 {
		t.Fatalf("after synthesized tool result committed entry count = %d, want 3", got)
	}

	s.appendMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "call-1", Name: string(toolspec.ToolExecCommand), Content: `{"output":"/tmp"}`})
	if got := s.committedEntryCount(); got != 3 {
		t.Fatalf("materialized tool result should not double count, got %d want 3", got)
	}

	s.appendLocalEntry("system", "note")
	if got := s.committedEntryCount(); got != 4 {
		t.Fatalf("after local entry committed entry count = %d, want 4", got)
	}

	if got := len(s.snapshot().Entries); got != s.committedEntryCount() {
		t.Fatalf("snapshot entry count = %d, committed entry count = %d", got, s.committedEntryCount())
	}
}
