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
