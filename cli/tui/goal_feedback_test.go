package tui

import (
	"strings"
	"testing"

	"builder/shared/theme"
	"builder/shared/transcript"
)

func TestGoalFeedbackRendersInPrimaryColor(t *testing.T) {
	for _, themeName := range []string{"dark", "light"} {
		m := NewModel(WithTheme(themeName), WithPreviewLines(4))
		m = updateModel(t, m, SetViewportSizeMsg{Lines: 4, Width: 80})
		m = updateModel(t, m, AppendTranscriptMsg{Role: TranscriptRole(transcript.EntryRoleGoalFeedback), Text: "Goal paused"})
		assertGoalFeedbackView(t, m.View(), themeName, "ongoing")

		m = updateModel(t, m, ToggleModeMsg{})
		assertGoalFeedbackView(t, m.View(), themeName, "detail")
	}
}

func assertGoalFeedbackView(t *testing.T, view string, themeName string, mode string) {
	t.Helper()
	if !strings.Contains(view, "ℹ") || !strings.Contains(view, "Goal paused") {
		t.Fatalf("expected %s %s goal feedback info line, got %q", themeName, mode, view)
	}
	var goalLine string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Goal paused") {
			goalLine = line
			break
		}
	}
	if goalLine == "" {
		t.Fatalf("expected %s %s goal feedback line, got %q", themeName, mode, view)
	}
	primary := rgbColorFromHex(theme.ResolvePalette(themeName).App.Primary.TrueColor)
	if !containsColor(extractForegroundTrueColors(goalLine), primary) {
		t.Fatalf("expected %s %s goal feedback to use primary color %+v, got %q", themeName, mode, primary, view)
	}
}
