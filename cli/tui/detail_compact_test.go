package tui

import (
	"fmt"
	"strings"
	"testing"

	"builder/shared/transcript"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

func TestCompactDetailCollapsesToolOutputUntilExpanded(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(12))
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "cat large.txt",
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "exec_command", IsShell: true, Command: "cat large.txt"},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_1", Text: "line 1\nline 2\nline 3"})
	m = updateModel(t, m, ToggleModeMsg{})

	collapsed := xansi.Strip(m.View())
	if !strings.Contains(collapsed, "$ cat large.txt") {
		t.Fatalf("expected collapsed tool input, got %q", collapsed)
	}
	if strings.Contains(collapsed, "line 2") {
		t.Fatalf("expected collapsed detail to hide tool output, got %q", collapsed)
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	expanded := xansi.Strip(m.View())
	if !strings.Contains(expanded, "$ cat large.txt") || !strings.Contains(expanded, "│ line 1") || !strings.Contains(expanded, "└ line 3") {
		t.Fatalf("expected expanded tool input and output, got %q", expanded)
	}
}

func TestCompactDetailNavigatesByMessageAndKeepsMultipleExpanded(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(12))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "first user\nhidden"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "first assistant\nhidden"})
	m = updateModel(t, m, ToggleModeMsg{})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	rendered := xansi.Strip(m.View())
	if !strings.Contains(rendered, "hidden") || !strings.Contains(rendered, "first assistant") {
		t.Fatalf("expected both messages expanded, got %q", rendered)
	}
	if strings.Contains(rendered, "▶︎") || strings.Contains(rendered, "▼") {
		t.Fatalf("expected compact detail without collapsed/expanded glyphs, got %q", rendered)
	}
}

func TestCompactDetailReconcilesSelectionAndExpansionAfterRefresh(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(12))
	m = updateModel(t, m, SetConversationMsg{BaseOffset: 10, Entries: []TranscriptEntry{
		{Role: "user", Text: "older"},
		{Role: "assistant", Text: "newer"},
	}})
	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if _, ok := m.detailExpandedEntries[11]; !ok {
		t.Fatalf("expected entry 11 expanded, got %+v", m.detailExpandedEntries)
	}

	m = updateModel(t, m, SetConversationMsg{BaseOffset: 20, Entries: []TranscriptEntry{{Role: "assistant", Text: "replacement"}}})
	if !m.detailSelectedActive || m.detailSelectedEntry != 20 {
		t.Fatalf("expected detail selection re-anchored to replacement, got active=%v entry=%d", m.detailSelectedActive, m.detailSelectedEntry)
	}
	if len(m.detailExpandedEntries) != 0 {
		t.Fatalf("expected stale expanded entries cleared, got %+v", m.detailExpandedEntries)
	}
}

func TestCompactDetailCollapsesReviewerSuggestions(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(10))
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:        "reviewer_suggestions",
		Text:        "Supervisor suggested:\n1. Add app-level coverage.\n2. Rebuild before final answer.",
		OngoingText: "Supervisor made 2 suggestions.",
	})
	m = updateModel(t, m, ToggleModeMsg{})

	collapsed := xansi.Strip(m.View())
	if !strings.Contains(collapsed, "Supervisor made 2 suggestions.") {
		t.Fatalf("expected collapsed reviewer suggestions summary, got %q", collapsed)
	}
	if strings.Contains(collapsed, "Add app-level coverage") || strings.Contains(collapsed, "Rebuild before final answer") {
		t.Fatalf("expected collapsed reviewer suggestions to hide full suggestion text, got %q", collapsed)
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	expanded := xansi.Strip(m.View())
	if !strings.Contains(expanded, "Add app-level coverage") || !strings.Contains(expanded, "Rebuild before final answer") {
		t.Fatalf("expected expanded reviewer suggestions to show full text, got %q", expanded)
	}
}

func TestCompactDetailScrollFocusesFirstVisibleEntryForExpansion(t *testing.T) {
	tests := []struct {
		name   string
		setup  []tea.Msg
		scroll tea.Msg
	}{
		{
			name:   "mouse wheel up",
			scroll: tea.MouseMsg{Button: tea.MouseButtonWheelUp, Type: tea.MouseWheelUp},
		},
		{
			name:   "page up",
			scroll: tea.KeyMsg{Type: tea.KeyPgUp},
		},
		{
			name: "page down",
			setup: []tea.Msg{
				tea.KeyMsg{Type: tea.KeyPgUp},
			},
			scroll: tea.KeyMsg{Type: tea.KeyPgDown},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel(WithCompactDetail(), WithPreviewLines(4))
			for idx := 0; idx < 8; idx++ {
				m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("entry %d", idx)})
			}
			m = updateModel(t, m, ToggleModeMsg{})
			for _, msg := range tt.setup {
				m = updateModel(t, m, msg)
			}

			m = updateModel(t, m, tt.scroll)
			firstVisible := firstVisibleSelectableDetailEntry(t, m)
			if !m.detailSelectedActive || m.detailSelectedEntry != firstVisible {
				t.Fatalf("expected scroll to focus first visible entry %d, got active=%v entry=%d", firstVisible, m.detailSelectedActive, m.detailSelectedEntry)
			}

			m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if _, ok := m.detailExpandedEntries[firstVisible]; !ok {
				t.Fatalf("expected enter after scroll to expand first visible entry %d, got %+v", firstVisible, m.detailExpandedEntries)
			}
		})
	}
}

func TestCompactDetailSelectionUsesModeBackgroundWithoutForegroundOverride(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithTheme("dark"), WithPreviewLines(6))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "selected detail entry"})
	m = updateModel(t, m, ToggleModeMsg{})

	selectedLine := lineContaining(m.View(), "selected detail entry")
	if selectedLine == "" {
		t.Fatalf("expected selected detail line, got %q", m.View())
	}
	modeBg := themeModeBackgroundColor("dark")
	if !strings.Contains(selectedLine, fmt.Sprintf("48;2;%d;%d;%d", modeBg.r, modeBg.g, modeBg.b)) {
		t.Fatalf("expected compact detail selection to use mode background, got %q", selectedLine)
	}
	if strings.Contains(selectedLine, "38;2;215;218;224") {
		t.Fatalf("did not expect compact detail selection to force foreground, got %q", selectedLine)
	}
}

func TestCompactDetailCollapsedCompletedShellUsesSingleLinePreview(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(8))
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "printf 'one\\n'\nprintf 'two\\n'",
		ToolCallID: "call_1",
		ToolCall: &transcript.ToolCallMeta{
			ToolName: "exec_command",
			IsShell:  true,
			Command:  "printf 'one\\n'\nprintf 'two\\n'",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_1", Text: "done"})
	m = updateModel(t, m, ToggleModeMsg{})

	rendered := xansi.Strip(m.View())
	if !strings.Contains(rendered, "$ printf 'one\\n'…") {
		t.Fatalf("expected completed shell call to stay compact, got %q", rendered)
	}
	if strings.Contains(rendered, "printf 'two") {
		t.Fatalf("expected collapsed completed shell call to hide second command line, got %q", rendered)
	}
}

func TestCompactDetailDefaultLabelsCoverInternalRoles(t *testing.T) {
	tests := map[string]string{
		"thinking":          "Reasoning summary",
		"reasoning":         "Reasoning summary",
		"thinking_trace":    "Reasoning trace",
		"compaction_notice": "Context compacted",
	}

	for role, want := range tests {
		if got := defaultDetailLabelForRole(role); got != want {
			t.Fatalf("defaultDetailLabelForRole(%q) = %q, want %q", role, got, want)
		}
	}
}

func TestCompactDetailFirstLineStaysWithinViewportWidth(t *testing.T) {
	const viewportWidth = 24
	m := NewModel(WithCompactDetail(), WithPreviewLines(6))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 6, Width: viewportWidth})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: strings.Repeat("a", 80)})
	m = updateModel(t, m, ToggleModeMsg{})

	line := lineContaining(m.View(), "❮")
	if line == "" {
		t.Fatalf("expected collapsed detail row, got %q", m.View())
	}
	if width := lipgloss.Width(line); width > viewportWidth {
		t.Fatalf("expected detail row width <= %d, got %d in %q", viewportWidth, width, xansi.Strip(line))
	}
}

func TestCompactDetailTruncatedShellPreservesCommandPrefixSpacing(t *testing.T) {
	const viewportWidth = 40
	m := NewModel(WithCompactDetail(), WithPreviewLines(6))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 6, Width: viewportWidth})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "git status --short && git diff --stat && git add cli/tui/detail_compact_test.go cli/app/ui_scroll_keys_test.go",
		ToolCallID: "call_1",
		ToolCall: &transcript.ToolCallMeta{
			ToolName: "exec_command",
			IsShell:  true,
			Command:  "git status --short && git diff --stat && git add cli/tui/detail_compact_test.go cli/app/ui_scroll_keys_test.go",
		},
	})
	m = updateModel(t, m, ToggleModeMsg{})

	rendered := xansi.Strip(m.View())
	if !strings.Contains(rendered, "$ git status") {
		t.Fatalf("expected truncated shell row to preserve shell prefix spacing, got %q", rendered)
	}
}

func TestCompactDetailWrappedAssistantUsesTreeGuide(t *testing.T) {
	const viewportWidth = 24
	m := NewModel(WithCompactDetail(), WithPreviewLines(6))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 6, Width: viewportWidth})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: strings.Repeat("assistant ", 20)})
	m = updateModel(t, m, ToggleModeMsg{})

	rendered := xansi.Strip(m.View())
	firstLine := lineContaining(rendered, "❮")
	if !strings.HasPrefix(firstLine, "❮ assistant") {
		t.Fatalf("expected assistant first row to preserve role prefix, got %q", firstLine)
	}
	if !strings.Contains(rendered, "│ assistant") || !strings.Contains(rendered, "└ assistant") {
		t.Fatalf("expected wrapped assistant preview to use tree guide, got %q", rendered)
	}
	if width := lipgloss.Width(firstLine); width > viewportWidth {
		t.Fatalf("expected assistant first row width <= %d, got %d in %q", viewportWidth, width, firstLine)
	}
}

func TestCompactDetailNarrowWrappedAssistantKeepsTreeGuideWithinViewport(t *testing.T) {
	const viewportWidth = 8
	m := NewModel(WithCompactDetail(), WithPreviewLines(8))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 8, Width: viewportWidth})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "abcdef abcdef abcdef"})
	m = updateModel(t, m, ToggleModeMsg{})

	rendered := xansi.Strip(m.View())
	firstLine := lineContaining(rendered, "❮")
	if !strings.HasPrefix(firstLine, "❮ ") {
		t.Fatalf("expected narrow assistant first row to keep normal role prefix, got %q", firstLine)
	}
	if !strings.Contains(rendered, "│ ") || !strings.Contains(rendered, "└ ") {
		t.Fatalf("expected narrow wrapped assistant preview to keep tree guide, got %q", rendered)
	}
	for _, line := range splitLines(rendered) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if width := lipgloss.Width(line); width > viewportWidth {
			t.Fatalf("expected narrow detail line width <= %d, got %d in %q", viewportWidth, width, line)
		}
	}
}

func TestCompactDetailNarrowTruncatedShellPreservesCommandPrefixSpacing(t *testing.T) {
	const viewportWidth = 8
	m := NewModel(WithCompactDetail(), WithPreviewLines(6))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 6, Width: viewportWidth})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "git status --short",
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "exec_command", IsShell: true, Command: "git status --short"},
	})
	m = updateModel(t, m, ToggleModeMsg{})

	line := lineContaining(m.View(), "$")
	if !strings.HasPrefix(xansi.Strip(line), "$ ") {
		t.Fatalf("expected narrow truncated shell row to preserve shell prefix spacing, got %q", xansi.Strip(line))
	}
	if width := lipgloss.Width(line); width > viewportWidth {
		t.Fatalf("expected narrow shell row width <= %d, got %d in %q", viewportWidth, width, xansi.Strip(line))
	}
}

func TestCompactDetailCollapsedShellErrorKeepsSummary(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(8))
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "printf 'one\\n'\nprintf 'two\\n'",
		ToolCallID: "call_1",
		ToolCall: &transcript.ToolCallMeta{
			ToolName: "exec_command",
			IsShell:  true,
			Command:  "printf 'one\\n'\nprintf 'two\\n'",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:              "tool_result_error",
		ToolCallID:        "call_1",
		Text:              "full output hidden while collapsed",
		ToolResultSummary: "permission denied",
	})
	m = updateModel(t, m, ToggleModeMsg{})

	rendered := xansi.Strip(m.View())
	if !strings.Contains(rendered, "permission denied") {
		t.Fatalf("expected collapsed shell error summary, got %q", rendered)
	}
	if strings.Contains(rendered, "printf 'two") || strings.Contains(rendered, "full output hidden") {
		t.Fatalf("expected collapsed shell error to stay compact, got %q", rendered)
	}
}

func firstVisibleSelectableDetailEntry(t *testing.T, m Model) int {
	t.Helper()

	for _, entryIndex := range m.detailLineEntryIndices {
		if entryIndex < 0 || m.detailBlockIndexForEntry(entryIndex) < 0 {
			continue
		}
		return entryIndex
	}
	t.Fatalf("expected visible selectable detail entry, owners=%+v", m.detailLineEntryIndices)
	return -1
}
