package toolcodec

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSplitInlineMeta(t *testing.T) {
	cmd, meta := SplitInlineMeta("pwd" + InlineMetaSeparator + "timeout: 5m")
	if cmd != "pwd" {
		t.Fatalf("command = %q, want pwd", cmd)
	}
	if meta != "timeout: 5m" {
		t.Fatalf("meta = %q, want timeout", meta)
	}
}

func TestCompactCallTextUsesInlineCommand(t *testing.T) {
	if got := CompactCallText("ls" + InlineMetaSeparator + "timeout: 5m\nworkdir: /tmp"); got != "ls" {
		t.Fatalf("expected compact command ls, got %q", got)
	}
}

func TestFormatInputAndOutput(t *testing.T) {
	cmd, timeout := FormatInput("shell", json.RawMessage(`{"command":"pwd"}`), DefaultShellTimeoutSecs)
	if cmd != "pwd" {
		t.Fatalf("cmd = %q, want pwd", cmd)
	}
	if timeout != "timeout: 5m" {
		t.Fatalf("timeout = %q, want timeout: 5m", timeout)
	}
	out := FormatOutput(json.RawMessage(`{"output":"  1\talpha\n  2\tbeta","exit_code":0}`))
	if out != "1\talpha\n  2\tbeta" {
		t.Fatalf("unexpected output = %q", out)
	}
}

func TestFormatInputWriteStdinPollIncludesYieldTime(t *testing.T) {
	cmd, timeout := FormatInput("write_stdin", json.RawMessage(`{"session_id":1149,"yield_time_ms":2000}`), DefaultShellTimeoutSecs)
	if cmd != "Polled session 1149 for 2s" {
		t.Fatalf("cmd = %q, want poll transcript summary", cmd)
	}
	if timeout != "" {
		t.Fatalf("timeout = %q, want empty timeout label", timeout)
	}
}

func TestFormatInputWriteStdinPollSubSecondUsesStandardDurationString(t *testing.T) {
	cmd, timeout := FormatInput("write_stdin", json.RawMessage(`{"session_id":1149,"yield_time_ms":250}`), DefaultShellTimeoutSecs)
	if cmd != "Polled session 1149 for 250ms" {
		t.Fatalf("cmd = %q, want poll transcript summary with standard duration", cmd)
	}
	if timeout != "" {
		t.Fatalf("timeout = %q, want empty timeout label", timeout)
	}
}

func TestFormatInputWriteStdinPollWithoutYieldTimeUsesLegacySummary(t *testing.T) {
	cmd, timeout := FormatInput("write_stdin", json.RawMessage(`{"session_id":1149}`), DefaultShellTimeoutSecs)
	if cmd != "poll session 1149" {
		t.Fatalf("cmd = %q, want legacy poll summary without explicit duration", cmd)
	}
	if timeout != "" {
		t.Fatalf("timeout = %q, want empty timeout label", timeout)
	}
}

func TestFormatOutputForTool_ViewImageSummarizesBinaryPayload(t *testing.T) {
	out := FormatOutputForTool("view_image", json.RawMessage(`[
		{"type":"input_image","image_url":"data:image/png;base64,AAAA"},
		{"type":"input_file","file_data":"Zm9v","filename":"doc.pdf"}
	]`))
	if out != "attached image\nattached PDF: doc.pdf" {
		t.Fatalf("unexpected output = %q", out)
	}
}

func TestFormatOutputDoesNotAppendDuplicateTruncationNote(t *testing.T) {
	out := FormatOutput(json.RawMessage(`{"output":"head\n\n...[Output is very large, omitted 21525 bytes. Consider using more targeted commands to reduce output size]...\n\ntail","exit_code":0,"truncated":true,"truncation_bytes":21525}`))
	if strings.Contains(out, "truncated 21525 bytes") {
		t.Fatalf("did not expect duplicate truncation note, output = %q", out)
	}
}

func TestFormatOutputDoesNotAppendTruncationNoteWithoutInlineBanner(t *testing.T) {
	out := FormatOutput(json.RawMessage(`{"output":"trimmed output","exit_code":0,"truncated":true,"truncation_bytes":42}`))
	if strings.Contains(out, "truncated 42 bytes") {
		t.Fatalf("did not expect truncation note, output = %q", out)
	}
}
