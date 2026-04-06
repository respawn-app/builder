package runtime

import (
	"encoding/json"
	"strconv"
	"testing"

	"builder/server/llm"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/cachewarn"
)

func TestPersistedTranscriptScanCollectsRequestedPageOnly(t *testing.T) {
	scan := NewPersistedTranscriptScan(PersistedTranscriptScanRequest{Offset: 1, Limit: 2})
	events := []session.Event{
		mustPersistedScanEvent(t, "message", llm.Message{Role: llm.RoleUser, Content: "u1"}),
		mustPersistedScanEvent(t, "message", llm.Message{Role: llm.RoleAssistant, Content: "a1", Phase: llm.MessagePhaseFinal}),
		mustPersistedScanEvent(t, "message", llm.Message{Role: llm.RoleUser, Content: "u2"}),
		mustPersistedScanEvent(t, "message", llm.Message{Role: llm.RoleAssistant, Content: "a2", Phase: llm.MessagePhaseFinal}),
	}
	for _, evt := range events {
		if err := scan.ApplyPersistedEvent(evt); err != nil {
			t.Fatalf("ApplyPersistedEvent(%q): %v", evt.Kind, err)
		}
	}

	page := scan.CollectedPageSnapshot()
	if scan.TotalEntries() != 4 {
		t.Fatalf("TotalEntries() = %d, want 4", scan.TotalEntries())
	}
	if len(page.Entries) != 2 {
		t.Fatalf("len(page.Entries) = %d, want 2", len(page.Entries))
	}
	if page.Entries[0].Text != "a1" || page.Entries[1].Text != "u2" {
		t.Fatalf("unexpected page entries: %+v", page.Entries)
	}
}

func TestPersistedTranscriptScanTracksDormantOngoingTailWindow(t *testing.T) {
	scan := NewPersistedTranscriptScan(PersistedTranscriptScanRequest{TrackOngoingTail: true, TailLimit: 3})
	for i := 0; i < 5; i++ {
		if err := scan.ApplyPersistedEvent(mustPersistedScanEvent(t, "message", llm.Message{Role: llm.RoleUser, Content: "before-" + strconv.Itoa(i)})); err != nil {
			t.Fatalf("ApplyPersistedEvent before %d: %v", i, err)
		}
	}
	if err := scan.ApplyPersistedEvent(mustPersistedScanEvent(t, "history_replaced", historyReplacementPayload{Items: llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleUser, Content: "summary"}})})); err != nil {
		t.Fatalf("ApplyPersistedEvent(history_replaced): %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := scan.ApplyPersistedEvent(mustPersistedScanEvent(t, "message", llm.Message{Role: llm.RoleAssistant, Content: "after-" + strconv.Itoa(i), Phase: llm.MessagePhaseFinal})); err != nil {
			t.Fatalf("ApplyPersistedEvent after %d: %v", i, err)
		}
	}

	window := scan.OngoingTailSnapshot()
	if window.TotalEntries != 7 {
		t.Fatalf("window.TotalEntries = %d, want 7", window.TotalEntries)
	}
	if window.Offset != 4 {
		t.Fatalf("window.Offset = %d, want 4", window.Offset)
	}
	if len(window.Snapshot.Entries) != 3 {
		t.Fatalf("len(window.Snapshot.Entries) = %d, want 3", len(window.Snapshot.Entries))
	}
	if window.Snapshot.Entries[0].Text != "before-4" || window.Snapshot.Entries[1].Text != "after-0" || window.Snapshot.Entries[2].Text != "after-1" {
		t.Fatalf("unexpected tail entries: %+v", window.Snapshot.Entries)
	}
}

func TestPersistedTranscriptScanWithoutLimitCollectsEntireDormantTranscript(t *testing.T) {
	scan := NewPersistedTranscriptScan(PersistedTranscriptScanRequest{})
	events := []session.Event{
		mustPersistedScanEvent(t, "message", llm.Message{Role: llm.RoleUser, Content: "u1"}),
		mustPersistedScanEvent(t, "message", llm.Message{Role: llm.RoleAssistant, Content: "a1", Phase: llm.MessagePhaseFinal}),
		mustPersistedScanEvent(t, "message", llm.Message{Role: llm.RoleUser, Content: "u2"}),
	}
	for _, evt := range events {
		if err := scan.ApplyPersistedEvent(evt); err != nil {
			t.Fatalf("ApplyPersistedEvent(%q): %v", evt.Kind, err)
		}
	}

	page := scan.CollectedPageSnapshot()
	if scan.TotalEntries() != 3 {
		t.Fatalf("TotalEntries() = %d, want 3", scan.TotalEntries())
	}
	if len(page.Entries) != 3 {
		t.Fatalf("len(page.Entries) = %d, want 3", len(page.Entries))
	}
	if page.Entries[0].Text != "u1" || page.Entries[1].Text != "a1" || page.Entries[2].Text != "u2" {
		t.Fatalf("unexpected unbounded page entries: %+v", page.Entries)
	}
}

func TestPersistedTranscriptScanEnrichesToolResultFromCompletion(t *testing.T) {
	scan := NewPersistedTranscriptScan(PersistedTranscriptScanRequest{Offset: 0, Limit: 10})
	toolOutput, err := json.Marshal(map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("marshal tool output: %v", err)
	}
	events := []session.Event{
		mustPersistedScanEvent(t, "tool_completed", map[string]any{"call_id": "call-1", "name": string(tools.ToolShell), "is_error": false, "output": json.RawMessage(toolOutput)}),
		mustPersistedScanEvent(t, "message", llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)}}}),
		mustPersistedScanEvent(t, "message", llm.Message{Role: llm.RoleTool, ToolCallID: "call-1", Name: string(tools.ToolShell)}),
	}
	for _, evt := range events {
		if err := scan.ApplyPersistedEvent(evt); err != nil {
			t.Fatalf("ApplyPersistedEvent(%q): %v", evt.Kind, err)
		}
	}

	page := scan.CollectedPageSnapshot()
	if len(page.Entries) != 2 {
		t.Fatalf("len(page.Entries) = %d, want 2", len(page.Entries))
	}
	if page.Entries[1].Role != "tool_result_ok" {
		t.Fatalf("page.Entries[1].Role = %q, want tool_result_ok", page.Entries[1].Role)
	}
	if page.Entries[1].Text == "" {
		t.Fatalf("expected enriched tool result text, got empty entry: %+v", page.Entries[1])
	}
}

func TestPersistedTranscriptScanSynthesizesCompletedToolResultWithoutToolMessage(t *testing.T) {
	scan := NewPersistedTranscriptScan(PersistedTranscriptScanRequest{Offset: 0, Limit: 10})
	events := []session.Event{
		mustPersistedScanEvent(t, "message", llm.Message{Role: llm.RoleAssistant, Content: "working", ToolCalls: []llm.ToolCall{{ID: "call-1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)}}}),
		mustPersistedScanEvent(t, "tool_completed", map[string]any{"call_id": "call-1", "name": string(tools.ToolShell), "is_error": false, "output": json.RawMessage(`{"output":"/tmp","exit_code":0,"truncated":false}`)}),
	}
	for _, evt := range events {
		if err := scan.ApplyPersistedEvent(evt); err != nil {
			t.Fatalf("ApplyPersistedEvent(%q): %v", evt.Kind, err)
		}
	}

	page := scan.CollectedPageSnapshot()
	if len(page.Entries) != 3 {
		t.Fatalf("len(page.Entries) = %d, want 3", len(page.Entries))
	}
	if page.Entries[2].Role != "tool_result_ok" || page.Entries[2].ToolCallID != "call-1" {
		t.Fatalf("expected synthesized tool result, got %+v", page.Entries[2])
	}
	if page.Entries[2].Text == "" {
		t.Fatalf("expected synthesized tool result text, got empty entry: %+v", page.Entries[2])
	}
}

func TestFormatPersistedToolCallBuildsFallbackMetadata(t *testing.T) {
	entry := formatPersistedToolCall(llm.ToolCall{
		ID:    "call-1",
		Name:  string(tools.ToolShell),
		Input: json.RawMessage(`{"command":"pwd"}`),
	})
	if entry.Role != "tool_call" {
		t.Fatalf("entry role = %q, want tool_call", entry.Role)
	}
	if entry.Text != "pwd" {
		t.Fatalf("entry text = %q, want pwd", entry.Text)
	}
	if entry.ToolCall == nil || !entry.ToolCall.IsShell {
		t.Fatalf("expected shell tool metadata, got %+v", entry.ToolCall)
	}
	if entry.ToolCall.Command != "pwd" {
		t.Fatalf("tool command = %q, want pwd", entry.ToolCall.Command)
	}
}

func TestPersistedTranscriptScanKeepsCompactionSummaryAndCarryoverInDetailTranscript(t *testing.T) {
	scan := NewPersistedTranscriptScan(PersistedTranscriptScanRequest{})
	events := []session.Event{
		mustPersistedScanEvent(t, "message", llm.Message{Role: llm.RoleUser, Content: "before compaction"}),
		mustPersistedScanEvent(t, "history_replaced", historyReplacementPayload{Items: llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleUser, Content: "condensed provider summary", MessageType: llm.MessageTypeCompactionSummary}})}),
		mustPersistedScanEvent(t, "local_entry", storedLocalEntry{Role: "compaction_summary", Text: "condensed summary"}),
		mustPersistedScanEvent(t, "message", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeManualCompactionCarryover, Content: "Last user message before handoff\n\ncarry this forward"}),
	}
	for _, evt := range events {
		if err := scan.ApplyPersistedEvent(evt); err != nil {
			t.Fatalf("ApplyPersistedEvent(%q): %v", evt.Kind, err)
		}
	}

	page := scan.CollectedPageSnapshot()
	if len(page.Entries) != 3 {
		t.Fatalf("len(page.Entries) = %d, want 3 (%+v)", len(page.Entries), page.Entries)
	}
	if page.Entries[1].Role != "compaction_summary" || page.Entries[1].Text != "condensed summary" {
		t.Fatalf("expected persisted compaction summary entry, got %+v", page.Entries[1])
	}
	if page.Entries[2].Role != "manual_compaction_carryover" {
		t.Fatalf("expected manual compaction carryover entry, got %+v", page.Entries[2])
	}
}

func TestPersistedTranscriptScanReplaysCacheWarnings(t *testing.T) {
	scan := NewPersistedTranscriptScan(PersistedTranscriptScanRequest{})
	if err := scan.ApplyPersistedEvent(mustPersistedScanEvent(t, sessionEventCacheWarning, cachewarn.Warning{Scope: cachewarn.ScopeConversation, Reason: cachewarn.ReasonNonPostfix})); err != nil {
		t.Fatalf("ApplyPersistedEvent(cache_warning): %v", err)
	}

	page := scan.CollectedPageSnapshot()
	if len(page.Entries) != 1 {
		t.Fatalf("len(page.Entries) = %d, want 1", len(page.Entries))
	}
	if page.Entries[0].Role != cacheWarningTranscriptRole {
		t.Fatalf("entry role = %q, want %q", page.Entries[0].Role, cacheWarningTranscriptRole)
	}
	if page.Entries[0].Text != cachewarn.Text(cachewarn.Warning{Scope: cachewarn.ScopeConversation, Reason: cachewarn.ReasonNonPostfix}) {
		t.Fatalf("unexpected cache warning text: %+v", page.Entries[0])
	}
}

func mustPersistedScanEvent(t *testing.T, kind string, payload any) session.Event {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %q payload: %v", kind, err)
	}
	return session.Event{Kind: kind, Payload: body}
}
