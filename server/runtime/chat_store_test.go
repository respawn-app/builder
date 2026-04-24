package runtime

import (
	"builder/server/llm"
	"builder/server/tools"
	"builder/shared/toolspec"
	"builder/shared/transcript"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func toolCallWithPresentation(t *testing.T, s *chatStore, call llm.ToolCall) llm.ToolCall {
	t.Helper()
	normalized := normalizeToolCallsForTranscript([]llm.ToolCall{call}, s.cwd)
	if len(normalized) != 1 {
		t.Fatalf("expected exactly one normalized tool call, got %d", len(normalized))
	}
	if len(normalized[0].Presentation) == 0 {
		t.Fatalf("expected normalized tool presentation for %+v", call)
	}
	return normalized[0]
}

func TestChatStoreSnapshotProjectsConversation(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})
	s.appendMessage(llm.Message{
		Role:    llm.RoleAssistant,
		Phase:   llm.MessagePhaseCommentary,
		Content: "Let me check.",
		ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "exec_command", Input: json.RawMessage(`{"command":"pwd","workdir":"/tmp","timeout_seconds":300}`)},
		},
	})
	s.recordToolCompletion(tools.Result{
		CallID:  "call_1",
		Name:    toolspec.ToolExecCommand,
		IsError: false,
		Output:  json.RawMessage(`{"output":"/tmp","exit_code":0,"truncated":false}`),
	})
	s.appendMessage(llm.Message{
		Role:       llm.RoleTool,
		ToolCallID: "call_1",
		Name:       string(toolspec.ToolExecCommand),
		Content:    `{"output":"/tmp","exit_code":0,"truncated":false}`,
	})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "done"})

	s.appendOngoingDelta("stream")
	s.setOngoingError("failed")
	s.appendLocalEntry("system", "note")

	snap := s.snapshot()
	if len(snap.Entries) != 6 {
		t.Fatalf("expected 6 entries, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Role != "user" || snap.Entries[0].Text != "hello" {
		t.Fatalf("unexpected first entry: %+v", snap.Entries[0])
	}
	if snap.Entries[1].Role != "assistant" || snap.Entries[1].Text != "Let me check." {
		t.Fatalf("unexpected commentary entry: %+v", snap.Entries[1])
	}
	if snap.Entries[2].Role != "tool_call" || !strings.Contains(snap.Entries[2].Text, "pwd") {
		t.Fatalf("unexpected tool_call entry: %+v", snap.Entries[2])
	}
	if snap.Entries[2].ToolCallID != "call_1" {
		t.Fatalf("unexpected tool_call id: %+v", snap.Entries[2])
	}
	if snap.Entries[2].ToolCall == nil || !snap.Entries[2].ToolCall.IsShell {
		t.Fatalf("expected shell tool metadata, got %+v", snap.Entries[2].ToolCall)
	}
	if snap.Entries[2].ToolCall.TimeoutLabel != "" {
		t.Fatalf("unexpected timeout label: %+v", snap.Entries[2].ToolCall)
	}
	if strings.Contains(snap.Entries[2].Text, "workdir:") {
		t.Fatalf("tool call should not include workdir line: %+v", snap.Entries[2])
	}
	if snap.Entries[3].Role != "tool_result_ok" || strings.TrimSpace(snap.Entries[3].Text) != "/tmp" {
		t.Fatalf("unexpected tool_result entry: %+v", snap.Entries[3])
	}
	if snap.Entries[3].ToolCallID != "call_1" {
		t.Fatalf("unexpected tool_result call id: %+v", snap.Entries[3])
	}
	if snap.Entries[4].Role != "assistant" || snap.Entries[4].Text != "done" {
		t.Fatalf("unexpected assistant entry: %+v", snap.Entries[4])
	}
	if snap.Entries[5].Role != "system" || snap.Entries[5].Text != "note" {
		t.Fatalf("unexpected local entry: %+v", snap.Entries[5])
	}
	if snap.Ongoing != "stream" {
		t.Fatalf("unexpected ongoing text: %q", snap.Ongoing)
	}
	if snap.OngoingError != "failed" {
		t.Fatalf("unexpected ongoing error: %q", snap.OngoingError)
	}
}

func TestChatStoreSnapshotKeepsShortCommentaryInTranscript(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})
	s.appendMessage(llm.Message{
		Role:    llm.RoleAssistant,
		Phase:   llm.MessagePhaseCommentary,
		Content: "Checking out repository",
		ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "exec_command", Input: json.RawMessage(`{"command":"pwd"}`)},
		},
	})

	snap := s.snapshot()
	for _, entry := range snap.Entries {
		if entry.Role == "assistant" && entry.Text == "Checking out repository" {
			return
		}
	}
	t.Fatalf("expected short commentary preserved in transcript entries, got %+v", snap.Entries)
}

func TestChatStoreSnapshotSynthesizesCompletedToolResultBeforeToolMessage(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{
		Role:    llm.RoleAssistant,
		Content: "working",
		ToolCalls: []llm.ToolCall{
			{ID: "call_a", Name: "exec_command", Input: json.RawMessage(`{"command":"sleep 1"}`)},
			{ID: "call_b", Name: "exec_command", Input: json.RawMessage(`{"command":"pwd"}`)},
		},
	})
	s.recordToolCompletion(tools.Result{
		CallID: "call_b",
		Name:   toolspec.ToolExecCommand,
		Output: json.RawMessage(`{"output":"/tmp","exit_code":0,"truncated":false}`),
	})

	snap := s.snapshot()
	if len(snap.Entries) != 4 {
		t.Fatalf("expected assistant, two tool calls, and synthesized tool result, got %+v", snap.Entries)
	}
	if snap.Entries[1].Role != "tool_call" || snap.Entries[1].ToolCallID != "call_a" {
		t.Fatalf("unexpected first tool call entry: %+v", snap.Entries[1])
	}
	if snap.Entries[2].Role != "tool_call" || snap.Entries[2].ToolCallID != "call_b" {
		t.Fatalf("unexpected second tool call entry: %+v", snap.Entries[2])
	}
	if snap.Entries[3].Role != "tool_result_ok" || snap.Entries[3].ToolCallID != "call_b" || strings.TrimSpace(snap.Entries[3].Text) != "/tmp" {
		t.Fatalf("expected synthesized completed tool result for call_b, got %+v", snap.Entries[3])
	}
}

func TestChatStoreSnapshotKeepsSubstantiveCommentaryInTranscript(t *testing.T) {
	s := newChatStore()
	content := strings.Repeat("reasoning detail ", 20)
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Phase: llm.MessagePhaseCommentary, Content: content})

	snap := s.snapshot()
	if len(snap.Entries) != 1 || snap.Entries[0].Role != "assistant" || snap.Entries[0].Phase != llm.MessagePhaseCommentary || snap.Entries[0].Text != content {
		t.Fatalf("expected substantive commentary preserved in transcript, got %+v", snap.Entries)
	}
}

func TestChatStoreSnapshotPreservesLocalEntryOngoingText(t *testing.T) {
	s := newChatStore()
	s.appendLocalEntryWithOngoingText("reviewer_suggestions", "Supervisor suggested:\n1. First", "Supervisor made 1 suggestion.")

	snap := s.snapshot()
	if len(snap.Entries) != 1 {
		t.Fatalf("expected one entry, got %+v", snap.Entries)
	}
	if snap.Entries[0].Role != "reviewer_suggestions" || snap.Entries[0].Text != "Supervisor suggested:\n1. First" || snap.Entries[0].OngoingText != "Supervisor made 1 suggestion." {
		t.Fatalf("unexpected local entry snapshot: %+v", snap.Entries[0])
	}
}

func TestChatStoreTranscriptPageSnapshotCollectsRequestedWindow(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "u1"})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "a1"})
	s.appendLocalEntry("system", "note")
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "u2"})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "a2"})

	page := s.transcriptPageSnapshot(1, 3)
	if page.TotalEntries != 5 {
		t.Fatalf("total entries = %d, want 5", page.TotalEntries)
	}
	if page.Offset != 1 {
		t.Fatalf("offset = %d, want 1", page.Offset)
	}
	if len(page.Snapshot.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(page.Snapshot.Entries))
	}
	if page.Snapshot.Entries[0].Role != "assistant" || page.Snapshot.Entries[0].Text != "a1" {
		t.Fatalf("unexpected first page entry: %+v", page.Snapshot.Entries[0])
	}
	if page.Snapshot.Entries[1].Role != "system" || page.Snapshot.Entries[1].Text != "note" {
		t.Fatalf("unexpected local page entry: %+v", page.Snapshot.Entries[1])
	}
	if page.Snapshot.Entries[2].Role != "user" || page.Snapshot.Entries[2].Text != "u2" {
		t.Fatalf("unexpected trailing page entry: %+v", page.Snapshot.Entries[2])
	}
}

func TestChatStoreTranscriptPageSnapshotSynthesizesCompletedToolResultBeforeToolMessage(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{
		Role:    llm.RoleAssistant,
		Content: "working",
		ToolCalls: []llm.ToolCall{
			{ID: "call_b", Name: "exec_command", Input: json.RawMessage(`{"command":"pwd"}`)},
		},
	})
	s.recordToolCompletion(tools.Result{
		CallID: "call_b",
		Name:   toolspec.ToolExecCommand,
		Output: json.RawMessage(`{"output":"/tmp","exit_code":0,"truncated":false}`),
	})

	page := s.transcriptPageSnapshot(0, 0)
	if page.TotalEntries != 3 {
		t.Fatalf("total entries = %d, want 3", page.TotalEntries)
	}
	if len(page.Snapshot.Entries) != 3 {
		t.Fatalf("entries = %d, want 3 (%+v)", len(page.Snapshot.Entries), page.Snapshot.Entries)
	}
	if page.Snapshot.Entries[2].Role != "tool_result_ok" || page.Snapshot.Entries[2].ToolCallID != "call_b" || strings.TrimSpace(page.Snapshot.Entries[2].Text) != "/tmp" {
		t.Fatalf("expected synthesized completed tool result for call_b, got %+v", page.Snapshot.Entries[2])
	}
}

func TestChatStoreTranscriptPageSnapshotPreservesHistoryAcrossCompaction(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "before compaction"})
	s.appendLocalEntry("error", "before replace")
	s.replaceHistory(llm.ItemsFromMessages([]llm.Message{
		{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeEnvironment, Content: "environment info"},
		{Role: llm.RoleUser, MessageType: llm.MessageTypeCompactionSummary, Content: "condensed summary"},
	}))
	s.appendLocalEntry("compaction_notice", "after replace notice")

	page := s.transcriptPageSnapshot(0, 0)
	if got := len(page.Snapshot.Entries); got != 5 {
		t.Fatalf("entry count = %d, want 5 (%+v)", got, page.Snapshot.Entries)
	}
	if got := page.Snapshot.Entries[0]; got.Role != "user" || got.Text != "before compaction" {
		t.Fatalf("entry[0] = %+v, want preserved pre-compaction user entry", got)
	}
	if got := page.Snapshot.Entries[1]; got.Role != "error" || got.Text != "before replace" {
		t.Fatalf("entry[1] = %+v, want preserved pre-compaction local entry", got)
	}
	if got := page.Snapshot.Entries[2]; got.Role != string(transcript.EntryRoleDeveloperContext) || got.Text != "environment info" {
		t.Fatalf("entry[2] = %+v, want compacted developer context", got)
	}
	if got := page.Snapshot.Entries[3]; got.Role != string(transcript.EntryRoleCompactionSummary) || got.Text != "condensed summary" {
		t.Fatalf("entry[3] = %+v, want compacted summary", got)
	}
	if got := page.Snapshot.Entries[4]; got.Role != "compaction_notice" || got.Text != "after replace notice" {
		t.Fatalf("entry[4] = %+v, want post-compaction local entry", got)
	}
}

func TestChatStoreOngoingTailUsesLatestCompactionBoundaryAsFloor(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "before compaction"})
	s.replaceHistory(llm.ItemsFromMessages([]llm.Message{
		{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeEnvironment, Content: "environment info"},
		{Role: llm.RoleUser, MessageType: llm.MessageTypeCompactionSummary, Content: "condensed summary"},
	}))
	s.appendLocalEntry("compaction_notice", "after replace notice")

	window := s.ongoingTailSnapshot(1)
	if got := len(window.Snapshot.Entries); got != 3 {
		t.Fatalf("entry count = %d, want 3 (%+v)", got, window.Snapshot.Entries)
	}
	if got := window.TotalEntries; got != 4 {
		t.Fatalf("total entries = %d, want 4", got)
	}
	if got := window.Offset; got != 1 {
		t.Fatalf("offset = %d, want 1", got)
	}
	if got := window.Snapshot.Entries[0]; got.Role != string(transcript.EntryRoleDeveloperContext) || got.Text != "environment info" {
		t.Fatalf("entry[0] = %+v, want compacted developer context", got)
	}
	if got := window.Snapshot.Entries[1]; got.Role != string(transcript.EntryRoleCompactionSummary) || got.Text != "condensed summary" {
		t.Fatalf("entry[1] = %+v, want compacted summary", got)
	}
	if got := window.Snapshot.Entries[2]; got.Role != "compaction_notice" || got.Text != "after replace notice" {
		t.Fatalf("entry[2] = %+v, want post-compaction local entry", got)
	}
}

func TestChatStoreOngoingTailUsesMostRecentCompactionBoundary(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "before"})
	s.replaceHistory([]llm.ResponseItem{{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, MessageType: llm.MessageTypeCompactionSummary, Content: "summary-1"}})
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "between"})
	s.replaceHistory([]llm.ResponseItem{{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, MessageType: llm.MessageTypeCompactionSummary, Content: "summary-2"}})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "after"})

	window := s.ongoingTailSnapshot(1)
	if got := window.TotalEntries; got != 5 {
		t.Fatalf("total entries = %d, want 5", got)
	}
	if got := window.Offset; got != 3 {
		t.Fatalf("offset = %d, want 3", got)
	}
	if got := len(window.Snapshot.Entries); got != 2 {
		t.Fatalf("entry count = %d, want 2 (%+v)", got, window.Snapshot.Entries)
	}
	if got := window.Snapshot.Entries[0].Text; got != "summary-2" {
		t.Fatalf("entry[0] = %q, want summary-2", got)
	}
	if got := window.Snapshot.Entries[1].Text; got != "after" {
		t.Fatalf("entry[1] = %q, want after", got)
	}
}

func TestFormatToolOutputPreservesNumberedPrefixes(t *testing.T) {
	out := tools.FormatGenericOutput(json.RawMessage(`{"output":"  1\talpha\n  2\tbeta\n  3\tgamma","exit_code":0}`))
	if !strings.Contains(out, "1\talpha") || !strings.Contains(out, "2\tbeta") || !strings.Contains(out, "3\tgamma") {
		t.Fatalf("expected numbered prefixes preserved, got %q", out)
	}
	if out != "1\talpha\n  2\tbeta\n  3\tgamma" {
		t.Fatalf("unexpected normalized output: %q", out)
	}
}

func TestPatchToolCallFormattingCapturesSummaryAndDetailMeta(t *testing.T) {
	s := newChatStore()
	s.cwd = "/workspace"

	patchText := "*** Begin Patch\n*** Update File: dir/a.go\n line1\n-old\n+new\n*** Add File: b.go\n+hello\n*** End Patch\n"
	call := llm.ToolCall{
		ID:          "call_patch",
		Name:        string(toolspec.ToolPatch),
		Custom:      true,
		CustomInput: patchText,
	}
	call = toolCallWithPresentation(t, s, call)
	rendered := s.formatToolCall(call)
	if rendered.ToolCall == nil {
		t.Fatalf("expected tool metadata on patch call")
	}
	if rendered.ToolCall.RenderHint == nil || rendered.ToolCall.RenderHint.Kind != transcript.ToolRenderKindDiff {
		t.Fatalf("expected diff render hint for patch, got %+v", rendered.ToolCall.RenderHint)
	}
	if rendered.ToolCallID != "call_patch" {
		t.Fatalf("unexpected patch call id: %+v", rendered)
	}
	summary := rendered.ToolCall.PatchSummary
	detail := rendered.ToolCall.PatchDetail
	if !rendered.ToolCall.HasPatchSummary() || !rendered.ToolCall.HasPatchDetail() {
		t.Fatalf("expected patch summary/detail metadata, got %+v", rendered.ToolCall)
	}
	if rendered.ToolCall.PatchRender == nil {
		t.Fatalf("expected typed patch render metadata, got %+v", rendered.ToolCall)
	}
	if !strings.Contains(summary, "Edited:") || !strings.Contains(summary, "./dir/a.go +1 -1") || !strings.Contains(summary, "./b.go +1") {
		t.Fatalf("unexpected summary output: %q", summary)
	}
	if !strings.Contains(detail, "/workspace/dir/a.go") || !strings.Contains(detail, "/workspace/b.go") {
		t.Fatalf("unexpected detail paths: %q", detail)
	}
	if !strings.Contains(detail, "+new") || !strings.Contains(detail, "-old") || !strings.Contains(detail, "+hello") {
		t.Fatalf("unexpected detail diff: %q", detail)
	}
}

func TestCustomPatchToolCallFormattingUsesFreeformInput(t *testing.T) {
	s := newChatStore()
	s.cwd = "/workspace"

	patchText := "*** Begin Patch\n*** Update File: cli/app/ui_status.go\n@@\n type uiStatusAuthInfo struct {\n-\tSummary string\n+\tSummary string\n+\tReady bool\n }\n*** End Patch\n"
	call := llm.ToolCall{
		ID:          "call_patch_custom",
		Name:        string(toolspec.ToolPatch),
		Custom:      true,
		CustomInput: patchText,
	}
	call = toolCallWithPresentation(t, s, call)
	rendered := s.formatToolCall(call)
	if rendered.ToolCall == nil {
		t.Fatalf("expected tool metadata on custom patch call")
	}
	if rendered.Text != rendered.ToolCall.PatchDetail {
		t.Fatalf("expected custom patch call text to use rendered detail, text=%q detail=%q", rendered.Text, rendered.ToolCall.PatchDetail)
	}
	if strings.Contains(rendered.ToolCall.PatchSummary, "*** Begin Patch") {
		t.Fatalf("expected ongoing summary to hide raw patch payload, got %q", rendered.ToolCall.PatchSummary)
	}
	if rendered.ToolCall.PatchSummary != "Edited: ./cli/app/ui_status.go +2 -1" {
		t.Fatalf("unexpected custom patch summary: %q", rendered.ToolCall.PatchSummary)
	}
	if rendered.ToolCall.PatchRender == nil {
		t.Fatalf("expected typed patch render metadata, got %+v", rendered.ToolCall)
	}
}

func TestPatchToolCallFormattingSingleFileUsesInlineEditedHeader(t *testing.T) {
	s := newChatStore()
	s.cwd = "/workspace"

	patchText := "*** Begin Patch\n*** Update File: dir/a.go\n-old\n+new\n*** End Patch\n"
	call := llm.ToolCall{
		ID:          "call_patch_single",
		Name:        string(toolspec.ToolPatch),
		Custom:      true,
		CustomInput: patchText,
	}
	call = toolCallWithPresentation(t, s, call)
	rendered := s.formatToolCall(call)
	if rendered.ToolCall == nil {
		t.Fatalf("expected tool metadata on patch call")
	}
	summary := rendered.ToolCall.PatchSummary
	detail := rendered.ToolCall.PatchDetail
	if summary != "Edited: ./dir/a.go +1 -1" {
		t.Fatalf("unexpected one-line summary: %q", summary)
	}
	if rendered.ToolCall.PatchRender == nil {
		t.Fatalf("expected typed patch render metadata, got %+v", rendered.ToolCall)
	}
	if strings.Contains(summary, "\n") {
		t.Fatalf("expected one-line summary, got %q", summary)
	}
	if !strings.HasPrefix(detail, "Edited: /workspace/dir/a.go") {
		t.Fatalf("expected one-line detail header, got %q", detail)
	}
}

func TestPatchToolCallFormattingFallsBackToRawPatchWhenFileViewParseFails(t *testing.T) {
	s := newChatStore()
	s.cwd = "/workspace"

	patchText := "not a structured patch payload"
	call := llm.ToolCall{
		ID:          "call_patch_raw",
		Name:        string(toolspec.ToolPatch),
		Custom:      true,
		CustomInput: patchText,
	}
	call = toolCallWithPresentation(t, s, call)
	rendered := s.formatToolCall(call)
	if rendered.ToolCall == nil {
		t.Fatalf("expected tool metadata on patch call fallback")
	}
	if rendered.ToolCall.RenderHint == nil || rendered.ToolCall.RenderHint.Kind != transcript.ToolRenderKindDiff {
		t.Fatalf("expected diff render hint for patch fallback, got %+v", rendered.ToolCall.RenderHint)
	}
	if rendered.ToolCall.PatchSummary != "Edited:" {
		t.Fatalf("expected fallback patch summary, got %q", rendered.ToolCall.PatchSummary)
	}
	if rendered.ToolCall.PatchRender == nil {
		t.Fatalf("expected fallback typed patch render metadata, got %+v", rendered.ToolCall)
	}
	if !strings.Contains(rendered.ToolCall.PatchDetail, patchText) {
		t.Fatalf("expected fallback patch detail to include raw payload, got %q", rendered.ToolCall.PatchDetail)
	}
}

func TestFormatToolCallShellAddsShellMetadata(t *testing.T) {
	s := newChatStore()
	call := llm.ToolCall{
		ID:    "call_shell",
		Name:  string(toolspec.ToolExecCommand),
		Input: json.RawMessage(`{"command":"cat cli/tui/model.go"}`),
	}
	call = toolCallWithPresentation(t, s, call)

	rendered := s.formatToolCall(call)
	if rendered.ToolCall == nil || !rendered.ToolCall.IsShell {
		t.Fatalf("expected shell metadata, got %+v", rendered.ToolCall)
	}
	if rendered.ToolCallID != "call_shell" {
		t.Fatalf("unexpected shell call id: %+v", rendered)
	}
	if rendered.ToolCall.RenderHint == nil {
		t.Fatalf("expected shell render hint, got %+v", rendered.ToolCall)
	}
	if rendered.ToolCall.RenderHint.Kind != transcript.ToolRenderKindSource {
		t.Fatalf("expected source render hint kind, got %+v", rendered.ToolCall.RenderHint)
	}
	if rendered.ToolCall.RenderHint.Path != "cli/tui/model.go" {
		t.Fatalf("unexpected source render hint path: %+v", rendered.ToolCall.RenderHint)
	}
	if !rendered.ToolCall.RenderHint.ResultOnly {
		t.Fatalf("expected result-only shell render hint, got %+v", rendered.ToolCall.RenderHint)
	}
	if !strings.Contains(rendered.Text, "cat cli/tui/model.go") {
		t.Fatalf("expected command in rendered shell call, got %q", rendered.Text)
	}
}

func TestFormatToolCallShellCapturesUserInitiatedMarker(t *testing.T) {
	s := newChatStore()
	call := llm.ToolCall{
		ID:    "call_shell_user",
		Name:  string(toolspec.ToolExecCommand),
		Input: json.RawMessage(`{"command":"pwd","user_initiated":true}`),
	}
	call = toolCallWithPresentation(t, s, call)

	rendered := s.formatToolCall(call)
	if rendered.ToolCall == nil {
		t.Fatalf("expected tool metadata, got nil")
	}
	if !rendered.ToolCall.UserInitiated {
		t.Fatalf("expected user initiated shell metadata, got %+v", rendered.ToolCall)
	}
}

func TestFormatToolCallWriteStdinPollUsesDurationInTranscript(t *testing.T) {
	s := newChatStore()
	call := llm.ToolCall{
		ID:    "call_poll",
		Name:  string(toolspec.ToolWriteStdin),
		Input: json.RawMessage(`{"session_id":1149,"yield_time_ms":2000}`),
	}
	call = toolCallWithPresentation(t, s, call)

	rendered := s.formatToolCall(call)
	if rendered.Role != "tool_call" {
		t.Fatalf("expected tool_call role, got %+v", rendered)
	}
	if rendered.Text != "Polled session 1149 for 2s" {
		t.Fatalf("expected transcript poll summary, got %q", rendered.Text)
	}
	if rendered.ToolCall == nil {
		t.Fatalf("expected tool metadata, got nil")
	}
	if rendered.ToolCall.Command != "Polled session 1149 for 2s" {
		t.Fatalf("expected tool command to match transcript summary, got %+v", rendered.ToolCall)
	}
	if rendered.ToolCall.TimeoutLabel != "" {
		t.Fatalf("did not expect timeout label for write_stdin poll, got %+v", rendered.ToolCall)
	}
	if !rendered.ToolCall.IsShell {
		t.Fatalf("expected write_stdin to remain marked as shell-like, got %+v", rendered.ToolCall)
	}
	if rendered.ToolCall.RenderHint == nil || rendered.ToolCall.RenderHint.Kind != transcript.ToolRenderKindPlain {
		t.Fatalf("expected plain render hint for write_stdin poll summary, got %+v", rendered.ToolCall.RenderHint)
	}
}

func TestFormatToolCallAskQuestionUsesQuestionAndSuggestionsMeta(t *testing.T) {
	s := newChatStore()
	call := llm.ToolCall{
		ID:    "call_ask",
		Name:  string(toolspec.ToolAskQuestion),
		Input: json.RawMessage(`{"question":"Choose scope?","suggestions":["flat scan","Recursive scan"],"recommended_option_index":1}`),
	}
	call = toolCallWithPresentation(t, s, call)

	rendered := s.formatToolCall(call)
	if rendered.Role != "tool_call" {
		t.Fatalf("expected tool_call role, got %+v", rendered)
	}
	if rendered.ToolCallID != "call_ask" {
		t.Fatalf("unexpected ask_question call id: %+v", rendered)
	}
	if rendered.ToolCall == nil {
		t.Fatalf("expected ask_question metadata, got nil")
	}
	if rendered.ToolCall.ToolName != string(toolspec.ToolAskQuestion) {
		t.Fatalf("unexpected ask_question tool name: %+v", rendered.ToolCall)
	}
	if rendered.Text != "Choose scope?" {
		t.Fatalf("expected rendered question text only, got %q", rendered.Text)
	}
	if strings.Contains(rendered.Text, "question:") || strings.Contains(rendered.Text, "suggestions:") {
		t.Fatalf("expected rendered ask_question text without labels, got %q", rendered.Text)
	}
	if rendered.ToolCall.Question != "Choose scope?" {
		t.Fatalf("unexpected ask_question metadata question: %+v", rendered.ToolCall)
	}
	if rendered.ToolCall.Command != "Choose scope?" {
		t.Fatalf("unexpected ask_question metadata command: %+v", rendered.ToolCall)
	}
	if len(rendered.ToolCall.Suggestions) != 2 {
		t.Fatalf("expected 2 suggestions, got %+v", rendered.ToolCall.Suggestions)
	}
	if rendered.ToolCall.Suggestions[0] != "flat scan" || rendered.ToolCall.Suggestions[1] != "Recursive scan" {
		t.Fatalf("unexpected ask_question suggestions: %+v", rendered.ToolCall.Suggestions)
	}
	if rendered.ToolCall.RecommendedOptionIndex != 1 {
		t.Fatalf("unexpected ask_question recommended option index: %+v", rendered.ToolCall)
	}
}

func TestChatStoreSnapshotFormatsAskQuestionStructuredAnswer(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{
			ID:    "call_ask",
			Name:  string(toolspec.ToolAskQuestion),
			Input: json.RawMessage(`{"question":"Choose scope?","suggestions":["full","fast"]}`),
		}},
	})
	s.appendMessage(llm.Message{
		Role:       llm.RoleTool,
		ToolCallID: "call_ask",
		Name:       string(toolspec.ToolAskQuestion),
		Content:    `"ask result summary"`,
	})

	snap := s.snapshot()
	if len(snap.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %+v", snap.Entries)
	}
	if snap.Entries[1].Role != "tool_result_ok" {
		t.Fatalf("expected ask result entry, got %+v", snap.Entries[1])
	}
	want := "ask result summary"
	if snap.Entries[1].Text != want {
		t.Fatalf("unexpected ask result text: %q", snap.Entries[1].Text)
	}
}

func TestFormatToolCallAskQuestionRejectsApprovalShapeAtToolLayer(t *testing.T) {
	s := newChatStore()
	call := llm.ToolCall{
		ID:    "call_ask",
		Name:  string(toolspec.ToolAskQuestion),
		Input: json.RawMessage(`{"question":"Approve?","approval":true}`),
	}
	call = toolCallWithPresentation(t, s, call)

	rendered := s.formatToolCall(call)
	if rendered.Text != "Approve?" {
		t.Fatalf("expected ask question text preserved for invalid approval-shaped tool call, got %q", rendered.Text)
	}
	if rendered.ToolCall == nil || rendered.ToolCall.Question != "Approve?" {
		t.Fatalf("expected ask metadata question preserved, got %+v", rendered.ToolCall)
	}
}

func TestFormatToolCallAskQuestionDropsImpossibleRecommendedMetadataAfterNormalization(t *testing.T) {
	s := newChatStore()
	call := llm.ToolCall{
		ID:    "call_ask",
		Name:  string(toolspec.ToolAskQuestion),
		Input: json.RawMessage(`{"question":"Choose scope?","suggestions":["", "beta"],"recommended_option_index":2}`),
	}
	call = toolCallWithPresentation(t, s, call)

	rendered := s.formatToolCall(call)
	if rendered.ToolCall == nil {
		t.Fatalf("expected ask metadata, got nil")
	}
	if len(rendered.ToolCall.Suggestions) != 1 || rendered.ToolCall.Suggestions[0] != "beta" {
		t.Fatalf("unexpected normalized suggestions: %+v", rendered.ToolCall)
	}
	if rendered.ToolCall.RecommendedOptionIndex != 0 {
		t.Fatalf("expected impossible recommended index to be dropped, got %+v", rendered.ToolCall)
	}
}

func TestFormatToolCallWebSearchUsesQueryOnly(t *testing.T) {
	s := newChatStore()
	call := llm.ToolCall{
		ID:    "call_web",
		Name:  string(toolspec.ToolWebSearch),
		Input: json.RawMessage(`{"query":"latest golang release"}`),
	}
	call = toolCallWithPresentation(t, s, call)

	rendered := s.formatToolCall(call)
	if rendered.Role != "tool_call" {
		t.Fatalf("expected tool_call role, got %+v", rendered)
	}
	if rendered.Text != `web search: "latest golang release"` {
		t.Fatalf("expected query-only text, got %q", rendered.Text)
	}
	if rendered.ToolCall == nil || rendered.ToolCall.Command != `web search: "latest golang release"` {
		t.Fatalf("expected command to match query, got %+v", rendered.ToolCall)
	}
}

func TestFormatToolResultWebSearchUsesCompactJSON(t *testing.T) {
	result := tools.Result{
		CallID: "call_web",
		Name:   toolspec.ToolWebSearch,
		Output: json.RawMessage(`{"type":"web_search_call","status":"completed","action":{"type":"search","query":"builder cli"}}`),
	}

	rendered := formatToolResult(result)
	if !strings.Contains(rendered, "\"type\":\"web_search_call\"") {
		t.Fatalf("expected compact json type field, got %q", rendered)
	}
	if !strings.Contains(rendered, "\"query\":\"builder cli\"") {
		t.Fatalf("expected compact json query field, got %q", rendered)
	}
}

func TestChatStoreShowsCompactionSummaryMessage(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeCompactionSummary, Content: "summary"})
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "real user input"})

	snap := s.snapshot()
	if len(snap.Entries) != 2 {
		t.Fatalf("expected 2 visible entries, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Role != string(transcript.EntryRoleCompactionSummary) || snap.Entries[0].Text != "summary" || snap.Entries[0].OngoingText != "" {
		t.Fatalf("unexpected compaction summary entry: %+v", snap.Entries[0])
	}
	if snap.Entries[1].Role != "user" || snap.Entries[1].Text != "real user input" {
		t.Fatalf("unexpected visible entry: %+v", snap.Entries[1])
	}
}

func TestChatStoreSnapshotIncludesDeveloperErrorFeedbackAsOngoingVisibleRole(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "task"})
	s.appendMessage(llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: "phase mismatch warning"})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "done"})

	snap := s.snapshot()
	if len(snap.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[1].Role != string(transcript.EntryRoleDeveloperFeedback) || snap.Entries[1].Text != "phase mismatch warning" {
		t.Fatalf("expected developer error feedback mapped to ongoing-visible role, got %+v", snap.Entries[1])
	}
}

func TestChatStoreSnapshotIncludesDeveloperContextAsDetailOnlyRole(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeAgentsMD, Content: "AGENTS context"})
	s.appendMessage(llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeEnvironment, Content: "Environment context"})

	snap := s.snapshot()
	if len(snap.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Role != string(transcript.EntryRoleDeveloperContext) || snap.Entries[0].Text != "AGENTS context" {
		t.Fatalf("unexpected developer context entry: %+v", snap.Entries[0])
	}
	if snap.Entries[1].Role != string(transcript.EntryRoleDeveloperContext) || snap.Entries[1].Text != "Environment context" {
		t.Fatalf("unexpected environment context entry: %+v", snap.Entries[1])
	}
	if snap.Entries[0].Visibility != transcript.EntryVisibilityDetailOnly || snap.Entries[1].Visibility != transcript.EntryVisibilityDetailOnly {
		t.Fatalf("expected developer context visibility to be detail-only, got %+v", snap.Entries)
	}
}

func TestChatStoreSnapshotIncludesUnknownDeveloperMessagesAsDetailOnlyContext(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageType("custom_internal"), Content: "Internal developer note"})

	snap := s.snapshot()
	if len(snap.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if got := snap.Entries[0]; got.Role != string(transcript.EntryRoleDeveloperContext) || got.Text != "Internal developer note" || got.Visibility != transcript.EntryVisibilityDetailOnly {
		t.Fatalf("unexpected unknown developer context entry: %+v", got)
	}
}

func TestChatStoreSnapshotIncludesInterruptionAsOngoingVisibleRole(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeInterruption, Content: "Interrupted by user."})

	snap := s.snapshot()
	if len(snap.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Role != string(transcript.EntryRoleInterruption) || snap.Entries[0].Text != "Interrupted by user." {
		t.Fatalf("unexpected interruption entry: %+v", snap.Entries[0])
	}
}

func TestChatStoreSnapshotIncludesDeveloperCompactionSoonReminderAsWarningRole(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "task"})
	s.appendMessage(llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeCompactionSoonReminder, Content: "heads up"})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "done"})

	snap := s.snapshot()
	if len(snap.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[1].Role != "warning" || snap.Entries[1].Text != "heads up" {
		t.Fatalf("expected compaction reminder mapped to warning role, got %+v", snap.Entries[1])
	}
}

func TestChatStoreSnapshotIncludesHeadlessModeVariantsAsDeveloperContext(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHeadlessMode, Content: "headless mode instructions"})
	s.appendMessage(llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHeadlessModeExit, Content: "interactive mode instructions"})

	snap := s.snapshot()
	if len(snap.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Role != string(transcript.EntryRoleDeveloperContext) || snap.Entries[0].Text != "headless mode instructions" {
		t.Fatalf("unexpected headless mode context entry: %+v", snap.Entries[0])
	}
	if snap.Entries[1].Role != string(transcript.EntryRoleDeveloperContext) || snap.Entries[1].Text != "interactive mode instructions" {
		t.Fatalf("unexpected headless mode exit context entry: %+v", snap.Entries[1])
	}
}

func TestChatStoreSnapshotIncludesHandoffFutureMessageAsDeveloperContext(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHandoffFutureMessage, Content: "resume with tests"})

	snap := s.snapshot()
	if len(snap.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Role != string(transcript.EntryRoleDeveloperContext) || snap.Entries[0].Text != "resume with tests" {
		t.Fatalf("unexpected handoff future message entry: %+v", snap.Entries[0])
	}
}

func TestChatStoreSnapshotOmitsRawReviewerFeedbackDeveloperMessages(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "task"})
	s.appendMessage(llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeReviewerFeedback, Content: "reviewer internal prompt"})
	s.appendLocalEntryWithOngoingText("reviewer_suggestions", "Supervisor suggested:\n1. First", "Supervisor made 1 suggestion.")
	s.appendLocalEntry("reviewer_status", "Supervisor ran: 1 suggestion, applied.")

	snap := s.snapshot()
	if len(snap.Entries) != 3 {
		t.Fatalf("expected 3 visible transcript entries, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	for _, entry := range snap.Entries {
		if entry.Text == "reviewer internal prompt" || entry.Role == string(transcript.EntryRoleDeveloperFeedback) {
			t.Fatalf("expected raw reviewer feedback developer message to stay hidden, got %+v", snap.Entries)
		}
	}
	if snap.Entries[1].Role != "reviewer_suggestions" || snap.Entries[2].Role != "reviewer_status" {
		t.Fatalf("expected reviewer transcript roles to represent reviewer feedback, got %+v", snap.Entries)
	}
}

func TestChatStoreSnapshotIncludesCompactTextForBackgroundNotice(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{
		Role:           llm.RoleDeveloper,
		MessageType:    llm.MessageTypeBackgroundNotice,
		Content:        "Background shell 1000 completed.\nExit code: 0\nOutput:\nlong output",
		CompactContent: "Background shell 1000 completed (exit 0)",
	})

	snap := s.snapshot()
	if len(snap.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Role != "system" {
		t.Fatalf("expected system role, got %+v", snap.Entries[0])
	}
	if snap.Entries[0].Text != "Background shell 1000 completed.\nExit code: 0\nOutput:\nlong output" {
		t.Fatalf("unexpected detail text: %+v", snap.Entries[0])
	}
	if snap.Entries[0].OngoingText != "Background shell 1000 completed (exit 0)" {
		t.Fatalf("unexpected ongoing text: %+v", snap.Entries[0])
	}
}

func TestChatStoreSnapshotShowsManualCompactionCarryoverAsDetailOnlyMessage(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{
		Role:        llm.RoleDeveloper,
		MessageType: llm.MessageTypeManualCompactionCarryover,
		Content:     "# Last user message before manual compaction\n\nplease keep tests green",
	})

	snap := s.snapshot()
	if len(snap.Entries) != 1 {
		t.Fatalf("expected carryover message to project once into transcript, got %+v", snap.Entries)
	}
	if got := snap.Entries[0]; got.Role != string(transcript.EntryRoleManualCompactionCarryover) || got.Text != "# Last user message before manual compaction\n\nplease keep tests green" || got.Visibility != transcript.EntryVisibilityDetailOnly {
		t.Fatalf("unexpected carryover transcript entry: %+v", got)
	}
}

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
