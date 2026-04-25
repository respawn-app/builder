package tui

import (
	"testing"

	"builder/shared/transcript"
)

func newShellPreviewModel(t *testing.T, theme string, detail bool) Model {
	t.Helper()
	command := "./gradlew -p apps/respawn detektFormat > docs/tmp/build-triage-2026-03-15/detektFormat.log 2>&1"
	m := NewModel(WithTheme(theme), WithPreviewLines(20))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 36})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: command,
		ToolCall: &transcript.ToolCallMeta{
			ToolName:   "shell",
			IsShell:    true,
			Command:    command,
			RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindShell},
		},
	})
	if detail {
		m = updateModel(t, m, ToggleModeMsg{})
	}
	return m
}
