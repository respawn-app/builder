package app

import (
	"builder/cli/tui"
	"builder/server/runtime"
	patchformat "builder/server/tools/patch/format"
	"builder/shared/transcript"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestDetailDiffBackgroundCoversSyntaxHighlightedCodeInAppView(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 18
	m.syncViewport()

	detail := "Edited:\n./main.go\n+package main\n-func removed() {}"
	m = updateUIModel(t, m, tui.AppendTranscriptMsg{
		Role: "tool_call",
		Text: detail,
		ToolCall: &transcript.ToolCallMeta{
			ToolName:    "patch",
			PatchDetail: detail,
			PatchRender: &patchformat.RenderedPatch{DetailLines: []patchformat.RenderedLine{
				{Kind: patchformat.RenderedLineKindHeader, Text: "Edited:", FileIndex: -1},
				{Kind: patchformat.RenderedLineKindFile, Text: "./main.go", FileIndex: 0, Path: "main.go"},
				{Kind: patchformat.RenderedLineKindDiff, Text: "+package main", FileIndex: 0},
				{Kind: patchformat.RenderedLineKindDiff, Text: "-func removed() {}", FileIndex: 0},
			}},
			RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindDiff},
		},
	})
	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlT})

	view := m.View()
	const addBg = "\x1b[48;2;31;42;34m"
	var addLine string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(ansi.Strip(line), "+package main") {
			addLine = line
			break
		}
	}
	if addLine == "" {
		t.Fatalf("expected detail diff line in app view, got %q", view)
	}
	if !strings.Contains(addLine, addBg+"  ") {
		t.Fatalf("expected app view to preserve diff background on detail indentation, got %q", addLine)
	}
	packageIdx := strings.Index(addLine, "package")
	if packageIdx < 0 {
		t.Fatalf("expected syntax-highlighted package token in app view, got %q", addLine)
	}
	bgIdx := strings.LastIndex(addLine[:packageIdx], addBg)
	if bgIdx < 0 {
		t.Fatalf("expected app view to apply diff background before syntax-highlighted token, got %q", addLine)
	}
	if strings.Contains(addLine[bgIdx:packageIdx], "\x1b[0") {
		t.Fatalf("expected no reset between diff background and first syntax-highlighted token, got %q", addLine)
	}
	if got := ansi.Strip(addLine); !strings.Contains(got, "+package main") {
		t.Fatalf("expected app view text preserved, got %q", got)
	}
}
