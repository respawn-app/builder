package toolcodec

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeInlineCallAndSplitInlineMeta(t *testing.T) {
	encoded := EncodeInlineCall("pwd", "timeout: 5m", true)
	if !strings.HasPrefix(encoded, ShellCallPrefix) {
		t.Fatalf("expected shell prefix, got %q", encoded)
	}
	cmd, meta := SplitInlineMeta(encoded)
	if cmd != "pwd" {
		t.Fatalf("command = %q, want pwd", cmd)
	}
	if meta != "timeout: 5m" {
		t.Fatalf("meta = %q, want timeout", meta)
	}
}

func TestPatchPayloadRoundTrip(t *testing.T) {
	summary := "Edited:\n./a.go +1 -1"
	detail := "Edited:\n/work/a.go\n+new\n-old"
	encoded := EncodePatchPayload(summary, detail)
	gotSummary, gotDetail, ok := DecodePatchPayload(encoded)
	if !ok {
		t.Fatalf("expected patch payload decode")
	}
	if gotSummary != summary || gotDetail != detail {
		t.Fatalf("unexpected decoded payload: summary=%q detail=%q", gotSummary, gotDetail)
	}
}

func TestCompactCallTextPrefersPatchSummaryAndInlineCommand(t *testing.T) {
	payload := EncodePatchPayload("Edited:\n./a.go +1", "Edited:\n/work/a.go\n+new")
	if got := CompactCallText(payload); !strings.Contains(got, "./a.go +1") {
		t.Fatalf("expected patch summary compact text, got %q", got)
	}
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
