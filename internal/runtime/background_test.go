package runtime

import "testing"

func TestFormatBackgroundShellNoticeUsesStructuredTexts(t *testing.T) {
	evt := BackgroundShellEvent{
		ID:          "1000",
		State:       "completed",
		NoticeText:  "Background shell 1000 completed.\nExit code: 0\nOutput:\ndone",
		CompactText: "Background shell 1000 completed (exit 0)",
	}

	if got := formatBackgroundShellNotice(evt); got != evt.NoticeText {
		t.Fatalf("unexpected detail notice: %q", got)
	}
	if got := formatBackgroundShellCompact(evt); got != evt.CompactText {
		t.Fatalf("unexpected compact notice: %q", got)
	}
}
