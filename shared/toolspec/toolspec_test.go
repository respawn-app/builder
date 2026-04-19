package toolspec

import "testing"

func TestParseID(t *testing.T) {
	tests := []struct {
		in   string
		want ID
		ok   bool
	}{
		{in: "shell", want: ToolShell, ok: true},
		{in: "bash", want: ToolShell, ok: true},
		{in: "bash_command", want: ToolShell, ok: true},
		{in: "shell_command", want: ToolShell, ok: true},
		{in: "exec_command", want: ToolExecCommand, ok: true},
		{in: "write_stdin", want: ToolWriteStdin, ok: true},
		{in: "view_image", want: ToolViewImage, ok: true},
		{in: "read_image", want: ToolViewImage, ok: true},
		{in: "patch", want: ToolPatch, ok: true},
		{in: "ask_question", want: ToolAskQuestion, ok: true},
		{in: "trigger_handoff", want: ToolTriggerHandoff, ok: true},
		{in: "web_search", want: ToolWebSearch, ok: true},
		{in: "multi_tool_use_parallel", want: ToolMultiToolUseParallel, ok: true},
		{in: "parallel", want: ToolMultiToolUseParallel, ok: true},
		{in: "unknown", ok: false},
	}

	for _, tt := range tests {
		got, ok := ParseID(tt.in)
		if ok != tt.ok {
			t.Fatalf("ParseID(%q) ok=%t want %t", tt.in, ok, tt.ok)
		}
		if ok && got != tt.want {
			t.Fatalf("ParseID(%q)=%q want %q", tt.in, got, tt.want)
		}
	}
}
