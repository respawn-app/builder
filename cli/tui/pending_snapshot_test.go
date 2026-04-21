package tui

import (
	"strings"
	"testing"

	"builder/shared/transcript"

	xansi "github.com/charmbracelet/x/ansi"
)

func TestRenderPendingOngoingSnapshotPreservesLeadingSpinnerCell(t *testing.T) {
	rendered := xansi.Strip(RenderPendingOngoingSnapshot([]TranscriptEntry{{
		Role:       "tool_call",
		Text:       "pwd",
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "pwd"},
	}}, "dark", 80, " ⡱"))

	if !strings.Contains(rendered, " ⡱pwd") {
		t.Fatalf("expected leading spinner cell preserved, got %q", rendered)
	}
}

func TestFlattenEntryWithSpinnerOverrideUsesSpinnerWidthForWrapBudget(t *testing.T) {
	rendered := xansi.Strip(RenderPendingOngoingSnapshot([]TranscriptEntry{{
		Role:       "tool_call",
		Text:       "1234 567",
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "patch"},
	}}, "dark", 8, "⢎ "))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected wrapped lines with spinner override, got %#v", lines)
	}
	if got := lines[0]; got != "⢎ 1234" {
		t.Fatalf("expected first line to use 2-cell spinner budget, got %q", got)
	}
	if got := lines[1]; got != "  567" {
		t.Fatalf("expected continuation line to stay aligned under spinner width, got %q", got)
	}
}
