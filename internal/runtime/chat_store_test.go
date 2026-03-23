package runtime

import (
	"builder/internal/llm"
	"builder/internal/tools"
	"builder/internal/transcript"
	"builder/prompts"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestChatStoreSnapshotProjectsConversation(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})
	s.appendMessage(llm.Message{
		Role:    llm.RoleAssistant,
		Phase:   llm.MessagePhaseCommentary,
		Content: "Let me check.",
		ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "shell", Input: json.RawMessage(`{"command":"pwd","workdir":"/tmp","timeout_seconds":300}`)},
		},
	})
	s.recordToolCompletion(tools.Result{
		CallID:  "call_1",
		Name:    tools.ToolShell,
		IsError: false,
		Output:  json.RawMessage(`{"output":"/tmp","exit_code":0,"truncated":false}`),
	})
	s.appendMessage(llm.Message{
		Role:       llm.RoleTool,
		ToolCallID: "call_1",
		Name:       string(tools.ToolShell),
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
	if snap.Entries[2].ToolCall.TimeoutLabel != "timeout: 5m" {
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
			{ID: "call_1", Name: "shell", Input: json.RawMessage(`{"command":"pwd"}`)},
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
			{ID: "call_a", Name: "shell", Input: json.RawMessage(`{"command":"sleep 1"}`)},
			{ID: "call_b", Name: "shell", Input: json.RawMessage(`{"command":"pwd"}`)},
		},
	})
	s.recordToolCompletion(tools.Result{
		CallID: "call_b",
		Name:   tools.ToolShell,
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
		ID:    "call_patch",
		Name:  string(tools.ToolPatch),
		Input: json.RawMessage(`{"patch":` + strconv.Quote(patchText) + `}`),
	}
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

func TestPatchToolCallFormattingSingleFileUsesInlineEditedHeader(t *testing.T) {
	s := newChatStore()
	s.cwd = "/workspace"

	patchText := "*** Begin Patch\n*** Update File: dir/a.go\n-old\n+new\n*** End Patch\n"
	call := llm.ToolCall{
		ID:    "call_patch_single",
		Name:  string(tools.ToolPatch),
		Input: json.RawMessage(`{"patch":` + strconv.Quote(patchText) + `}`),
	}
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
		ID:    "call_patch_raw",
		Name:  string(tools.ToolPatch),
		Input: json.RawMessage(`{"patch":` + strconv.Quote(patchText) + `}`),
	}
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
		Name:  string(tools.ToolShell),
		Input: json.RawMessage(`{"command":"cat internal/tui/model.go"}`),
	}

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
	if rendered.ToolCall.RenderHint.Path != "internal/tui/model.go" {
		t.Fatalf("unexpected source render hint path: %+v", rendered.ToolCall.RenderHint)
	}
	if !rendered.ToolCall.RenderHint.ResultOnly {
		t.Fatalf("expected result-only shell render hint, got %+v", rendered.ToolCall.RenderHint)
	}
	if !strings.Contains(rendered.Text, "cat internal/tui/model.go") {
		t.Fatalf("expected command in rendered shell call, got %q", rendered.Text)
	}
}

func TestFormatToolCallShellCapturesUserInitiatedMarker(t *testing.T) {
	s := newChatStore()
	call := llm.ToolCall{
		ID:    "call_shell_user",
		Name:  string(tools.ToolShell),
		Input: json.RawMessage(`{"command":"pwd","user_initiated":true}`),
	}

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
		Name:  string(tools.ToolWriteStdin),
		Input: json.RawMessage(`{"session_id":1149,"yield_time_ms":2000}`),
	}

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
	if rendered.ToolCall.RenderHint != nil {
		t.Fatalf("did not expect render hint for write_stdin poll summary, got %+v", rendered.ToolCall.RenderHint)
	}
}

func TestFormatToolCallAskQuestionUsesQuestionAndSuggestionsMeta(t *testing.T) {
	s := newChatStore()
	call := llm.ToolCall{
		ID:    "call_ask",
		Name:  string(tools.ToolAskQuestion),
		Input: json.RawMessage(`{"question":"Choose scope?","suggestions":["Recommended: flat scan","Recursive scan"]}`),
	}

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
	if rendered.ToolCall.ToolName != string(tools.ToolAskQuestion) {
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
	if rendered.ToolCall.Suggestions[0] != "Recommended: flat scan" || rendered.ToolCall.Suggestions[1] != "Recursive scan" {
		t.Fatalf("unexpected ask_question suggestions: %+v", rendered.ToolCall.Suggestions)
	}
}

func TestFormatToolCallWebSearchUsesQueryOnly(t *testing.T) {
	s := newChatStore()
	call := llm.ToolCall{
		ID:    "call_web",
		Name:  string(tools.ToolWebSearch),
		Input: json.RawMessage(`{"query":"latest golang release"}`),
	}

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

func TestFormatToolResultWebSearchUsesPrettyJSON(t *testing.T) {
	result := tools.Result{
		CallID: "call_web",
		Name:   tools.ToolWebSearch,
		Output: json.RawMessage(`{"type":"web_search_call","status":"completed","action":{"type":"search","query":"builder cli"}}`),
	}

	rendered := formatToolResult(result)
	if !strings.Contains(rendered, "\"type\": \"web_search_call\"") {
		t.Fatalf("expected pretty json type field, got %q", rendered)
	}
	if !strings.Contains(rendered, "\"query\": \"builder cli\"") {
		t.Fatalf("expected pretty json query field, got %q", rendered)
	}
}

func TestChatStoreHidesSyntheticCompactionSummaryMessage(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: prompts.CompactionSummaryPrefix + "\n\nsummary"})
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "real user input"})

	snap := s.snapshot()
	if len(snap.Entries) != 1 {
		t.Fatalf("expected 1 visible entry, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Role != "user" || snap.Entries[0].Text != "real user input" {
		t.Fatalf("unexpected visible entry: %+v", snap.Entries[0])
	}
}

func TestChatStoreSnapshotIncludesDeveloperErrorFeedbackAsErrorRole(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "task"})
	s.appendMessage(llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: "phase mismatch warning"})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "done"})

	snap := s.snapshot()
	if len(snap.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[1].Role != "error" || snap.Entries[1].Text != "phase mismatch warning" {
		t.Fatalf("expected developer error feedback mapped to error role, got %+v", snap.Entries[1])
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
	if snap.Entries[2].Role != "error" || snap.Entries[2].Text != "warn" {
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

func TestChatStoreSnapshotKeepsLocalEntryOrderingAfterHistoryReplace(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "a"})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "b"})
	s.appendLocalEntry("error", "before replace")

	replacement := llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleUser, Content: "after replace"}})
	s.replaceHistory(replacement)
	s.appendLocalEntry("compaction_notice", "after replace notice")

	snap := s.snapshot()
	if len(snap.Entries) != 4 {
		t.Fatalf("expected 4 entries, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Role != "user" || snap.Entries[0].Text != "a" {
		t.Fatalf("unexpected first entry after replace: %+v", snap.Entries[0])
	}
	if snap.Entries[1].Role != "assistant" || snap.Entries[1].Text != "b" {
		t.Fatalf("unexpected second entry after replace: %+v", snap.Entries[1])
	}
	if snap.Entries[2].Role != "error" || snap.Entries[2].Text != "before replace" {
		t.Fatalf("expected old local entry after visible history, got %+v", snap.Entries[2])
	}
	if snap.Entries[3].Role != "compaction_notice" || snap.Entries[3].Text != "after replace notice" {
		t.Fatalf("expected new local entry after old local entry, got %+v", snap.Entries[3])
	}
}

func TestChatStoreProviderHistoryStartsAtLastCompactionCheckpoint(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "before-1"})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "before-2"})

	replacement := []llm.ResponseItem{
		{Type: llm.ResponseItemTypeMessage, Role: llm.RoleDeveloper, Content: "ctx"},
		{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "compact-summary"},
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
	if len(snap.Entries) != 3 {
		t.Fatalf("expected full visible transcript to remain intact, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Role != "user" || snap.Entries[0].Text != "before-1" {
		t.Fatalf("unexpected visible entry[0]: %+v", snap.Entries[0])
	}
	if snap.Entries[1].Role != "assistant" || snap.Entries[1].Text != "before-2" {
		t.Fatalf("unexpected visible entry[1]: %+v", snap.Entries[1])
	}
	if snap.Entries[2].Role != "user" || snap.Entries[2].Text != "after" {
		t.Fatalf("unexpected visible entry[2]: %+v", snap.Entries[2])
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
