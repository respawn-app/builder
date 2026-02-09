package runtime

import (
	"builder/internal/llm"
	"builder/internal/tools"
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
		t.Fatalf("unexpected assistant preamble entry: %+v", snap.Entries[1])
	}
	if snap.Entries[2].Role != "tool_call" || !strings.Contains(snap.Entries[2].Text, "pwd") {
		t.Fatalf("unexpected tool_call entry: %+v", snap.Entries[2])
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

func TestFormatToolOutputStripsLineNumbers(t *testing.T) {
	out := formatToolOutput(json.RawMessage(`{"output":"  1\talpha\n  2\tbeta\n  3\tgamma","exit_code":0}`))
	if strings.Contains(out, "1\talpha") || strings.Contains(out, "2\tbeta") || strings.Contains(out, "3\tgamma") {
		t.Fatalf("expected numbered prefixes removed, got %q", out)
	}
	if out != "alpha\nbeta\ngamma" {
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
	summary := rendered.ToolCall.PatchSummary
	detail := rendered.ToolCall.PatchDetail
	if !rendered.ToolCall.HasPatchSummary() || !rendered.ToolCall.HasPatchDetail() {
		t.Fatalf("expected patch summary/detail metadata, got %+v", rendered.ToolCall)
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

func TestFormatToolCallShellAddsShellMetadata(t *testing.T) {
	s := newChatStore()
	call := llm.ToolCall{
		ID:    "call_shell",
		Name:  string(tools.ToolShell),
		Input: json.RawMessage(`{"command":"pwd"}`),
	}

	rendered := s.formatToolCall(call)
	if rendered.ToolCall == nil || !rendered.ToolCall.IsShell {
		t.Fatalf("expected shell metadata, got %+v", rendered.ToolCall)
	}
	if !strings.Contains(rendered.Text, "pwd") {
		t.Fatalf("expected command in rendered shell call, got %q", rendered.Text)
	}
}

func TestChatStoreFiltersInjectedAgentsMessage(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: agentsInjectedPrefix + "\nsource: /tmp/AGENTS.md"})
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "real"})

	snap := s.snapshot()
	if len(snap.Entries) != 1 {
		t.Fatalf("expected 1 visible entry, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Text != "real" {
		t.Fatalf("unexpected visible entry: %+v", snap.Entries[0])
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
