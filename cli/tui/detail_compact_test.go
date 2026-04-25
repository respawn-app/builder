package tui

import (
	"fmt"
	"strings"
	"testing"

	"builder/shared/transcript"
	"builder/shared/uiglyphs"

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

func TestCompactDetailKeepsMultipleExpanded(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(12))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "first user\nhidden"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "first assistant\nhidden"})
	m = updateModel(t, m, ToggleModeMsg{})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m.detailSelectedEntry = 0
	m.detailSelectedActive = true
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	rendered := xansi.Strip(m.View())
	if !strings.Contains(rendered, "hidden") || !strings.Contains(rendered, "first assistant") {
		t.Fatalf("expected both messages expanded, got %q", rendered)
	}
	if strings.Contains(rendered, "▶︎") || strings.Contains(rendered, "▼") {
		t.Fatalf("expected compact detail without collapsed/expanded glyphs, got %q", rendered)
	}
}

func TestCompactDetailArrowScrollsExpandedItemByLineAndTracksCenterSelection(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(6))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 6, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "long-command",
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "exec_command", IsShell: true, Command: "long-command"},
	})
	outputLines := make([]string, 0, 30)
	for idx := 0; idx < 30; idx++ {
		outputLines = append(outputLines, fmt.Sprintf("output line %02d", idx))
	}
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_1", Text: strings.Join(outputLines, "\n")})
	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	beforeSelected := m.detailSelectedEntry

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if got, want := m.DetailScroll(), 1; got != want {
		t.Fatalf("expected arrow scroll to move by one rendered line, got %d want %d", got, want)
	}
	firstVisible := topVisibleSelectableDetailEntry(t, m)
	centerVisible := centerVisibleSelectableDetailEntry(t, m)
	if !m.detailSelectedActive || m.detailSelectedEntry != centerVisible {
		t.Fatalf("expected arrow scroll to select center visible entry %d, got active=%v entry=%d", centerVisible, m.detailSelectedActive, m.detailSelectedEntry)
	}
	if m.detailSelectedEntry != beforeSelected {
		t.Fatalf("expected one-line scroll inside expanded command to keep same selected item, got %d want %d", m.detailSelectedEntry, beforeSelected)
	}
	if firstVisible != beforeSelected {
		t.Fatalf("expected expanded command to remain top visible, got %d want %d", firstVisible, beforeSelected)
	}
}

func TestCompactDetailLineScrollRailTracksCenterInsideTallExpandedEntry(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(6))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 6, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "intro line 0\nintro line 1\nintro line 2"})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "long-command",
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "exec_command", IsShell: true, Command: "long-command"},
	})
	outputLines := make([]string, 0, 12)
	for idx := 0; idx < 12; idx++ {
		outputLines = append(outputLines, fmt.Sprintf("output line %02d", idx))
	}
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_1", Text: strings.Join(outputLines, "\n")})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "tail"})
	m = updateModel(t, m, ToggleModeMsg{})
	m.detailSelectedEntry = 1
	m.detailSelectedActive = true
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m.detailBottomAnchor = false
	m.detailScroll = 0
	m.refreshDetailViewport()
	m.detailSelectedEntry = 0
	m.detailSelectedActive = true

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})

	if got, want := m.DetailScroll(), 2; got != want {
		t.Fatalf("expected one-line scroll, got %d want %d", got, want)
	}
	if !m.detailSelectedActive || m.detailSelectedEntry != 1 {
		t.Fatalf("expected center selection to move to expanded tool entry, active=%v entry=%d", m.detailSelectedActive, m.detailSelectedEntry)
	}
	lines := strings.Split(xansi.Strip(m.View()), "\n")
	center := m.viewportLines / 2
	if center >= len(lines) {
		t.Fatalf("center line %d outside rendered lines %d", center, len(lines))
	}
	if !strings.HasPrefix(lines[center], uiglyphs.SelectionRailGlyph) || !strings.Contains(lines[center], "output line") {
		t.Fatalf("expected selected rail on center output line, got center=%q view=%q", lines[center], xansi.Strip(m.View()))
	}
}

func TestCompactDetailLineScrollSelectionTracksCenterItem(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(6))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 6, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "first-command",
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "exec_command", IsShell: true, Command: "first-command"},
	})
	outputLines := make([]string, 0, 20)
	for idx := 0; idx < 20; idx++ {
		outputLines = append(outputLines, fmt.Sprintf("first output line %02d", idx))
	}
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_1", Text: strings.Join(outputLines, "\n")})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "second-command",
		ToolCallID: "call_2",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "exec_command", IsShell: true, Command: "second-command"},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_2", Text: "second output"})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "third-command",
		ToolCallID: "call_3",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "exec_command", IsShell: true, Command: "third-command"},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_3", Text: "third output"})
	for idx := 0; idx < 10; idx++ {
		m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("tail entry %02d", idx)})
	}
	m = updateModel(t, m, ToggleModeMsg{})
	m.detailSelectedEntry = 0
	m.detailSelectedActive = true
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	for step := 1; step <= 10; step++ {
		m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
		if got := m.DetailScroll(); got != step {
			t.Fatalf("step %d: expected one-line scroll, got %d", step, got)
		}
		if firstVisible := topVisibleSelectableDetailEntry(t, m); firstVisible != 0 {
			t.Fatalf("step %d: expected first expanded item to remain first visible, got %d", step, firstVisible)
		}
		centerVisible := centerVisibleSelectableDetailEntry(t, m)
		if !m.detailSelectedActive || m.detailSelectedEntry != centerVisible {
			t.Fatalf("step %d: expected selection to track center visible item %d, active=%v entry=%d", step, centerVisible, m.detailSelectedActive, m.detailSelectedEntry)
		}
	}

	for guard := 0; guard < 40 && topVisibleSelectableDetailEntry(t, m) == 0; guard++ {
		before := m.DetailScroll()
		m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
		if got := m.DetailScroll(); got != before+1 {
			t.Fatalf("expected crossing scroll to move by one rendered line, got %d want %d", got, before+1)
		}
	}
	if firstVisible := topVisibleSelectableDetailEntry(t, m); firstVisible != 2 {
		t.Fatalf("expected second item first visible after crossing expanded item, got %d", firstVisible)
	}
	centerVisible := centerVisibleSelectableDetailEntry(t, m)
	if !m.detailSelectedActive || m.detailSelectedEntry != centerVisible {
		t.Fatalf("expected selection to track center visible item %d after crossing expanded item, active=%v entry=%d", centerVisible, m.detailSelectedActive, m.detailSelectedEntry)
	}
}

func TestCompactDetailLineScrollFocusesCenterVisibleSelection(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(6))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 6, Width: 80})
	for idx := 0; idx < 10; idx++ {
		m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("entry %02d", idx)})
	}
	m = updateModel(t, m, ToggleModeMsg{})
	m.ensureDetailScrollResolved()
	m.refreshDetailViewport()
	visible := m.visibleSelectableDetailEntries()
	if len(visible) < 2 {
		t.Fatalf("expected at least two visible detail entries, got %+v", visible)
	}
	selected := visible[1]
	m.detailSelectedEntry = selected
	m.detailSelectedActive = true
	beforeScroll := m.DetailScroll()

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.DetailScroll(); got != beforeScroll-1 {
		t.Fatalf("expected up to scroll by one line while selection remains visible, got %d want %d", got, beforeScroll-1)
	}
	centerVisible := centerVisibleSelectableDetailEntry(t, m)
	if !m.detailSelectedActive || m.detailSelectedEntry != centerVisible {
		t.Fatalf("expected line scroll to focus center visible selection %d, got active=%v entry=%d", centerVisible, m.detailSelectedActive, m.detailSelectedEntry)
	}
}

func TestCompactDetailSelectionMovesWithinViewportAtTranscriptEnd(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(6))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 6, Width: 80})
	for idx := 0; idx < 8; idx++ {
		m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("entry %02d", idx)})
	}
	m = updateModel(t, m, ToggleModeMsg{})
	for guard := 0; guard < 20 && m.DetailScroll() < m.maxDetailScroll(); guard++ {
		m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got, want := m.DetailScroll(), m.maxDetailScroll(); got != want {
		t.Fatalf("expected setup to reach bottom scroll, got %d want %d", got, want)
	}
	firstVisible := topVisibleSelectableDetailEntry(t, m)
	m.detailSelectedEntry = firstVisible
	m.detailSelectedActive = true

	beforeScroll := m.DetailScroll()
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.DetailScroll(); got != beforeScroll {
		t.Fatalf("expected down at transcript bottom to keep line scroll pinned, got %d want %d", got, beforeScroll)
	}
	if !m.detailSelectedActive || m.detailSelectedEntry <= firstVisible {
		t.Fatalf("expected down at transcript bottom to move selection below first visible entry %d, got active=%v entry=%d", firstVisible, m.detailSelectedActive, m.detailSelectedEntry)
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

func TestCompactDetailScrollFocusesCenterVisibleEntryForExpansion(t *testing.T) {
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
			centerVisible := centerVisibleSelectableDetailEntry(t, m)
			if !m.detailSelectedActive || m.detailSelectedEntry != centerVisible {
				t.Fatalf("expected scroll to focus center visible entry %d, got active=%v entry=%d", centerVisible, m.detailSelectedActive, m.detailSelectedEntry)
			}

			m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if _, ok := m.detailExpandedEntries[centerVisible]; !ok {
				t.Fatalf("expected enter after scroll to expand center visible entry %d, got %+v", centerVisible, m.detailExpandedEntries)
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

func TestCompactDetailSelectedLongCollapsedRowKeepsWideRailWithinViewport(t *testing.T) {
	const viewportWidth = 16
	m := NewModel(WithCompactDetail(), WithPreviewLines(4), WithTheme("dark"))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 4, Width: viewportWidth})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: strings.Repeat("abcdef ", 20)})
	m = updateModel(t, m, ToggleModeMsg{})

	line := lineContaining(m.View(), uiglyphs.SelectionRailGlyph)
	if line == "" {
		t.Fatalf("expected selected compact detail row to include wide rail, got %q", m.View())
	}
	if width := lipgloss.Width(line); width != viewportWidth {
		t.Fatalf("expected selected row with wide rail to stay exactly viewport width %d, got %d in %q", viewportWidth, width, xansi.Strip(line))
	}
	if !strings.Contains(xansi.Strip(line), "❮ ") {
		t.Fatalf("expected selected row with wide rail to keep role symbol visible, got %q", xansi.Strip(line))
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
	if !strings.Contains(firstLine, "❮ assistant") {
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
	if !strings.Contains(firstLine, "❮ ") {
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
	if !strings.Contains(xansi.Strip(line), "$ ") {
		t.Fatalf("expected narrow truncated shell row to preserve shell prefix spacing, got %q", xansi.Strip(line))
	}
	if width := lipgloss.Width(line); width > viewportWidth {
		t.Fatalf("expected narrow shell row width <= %d, got %d in %q", viewportWidth, width, xansi.Strip(line))
	}
}

func TestCompactDetailCollapsedShellErrorKeepsSummary(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(8))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 8, Width: 220})
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

func topVisibleSelectableDetailEntry(t *testing.T, m Model) int {
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

func centerVisibleSelectableDetailEntry(t *testing.T, m Model) int {
	t.Helper()

	if len(m.detailLineEntryIndices) == 0 {
		t.Fatal("expected visible detail entries")
	}
	anchor := m.viewportLines / 2
	if anchor >= len(m.detailLineEntryIndices) {
		anchor = len(m.detailLineEntryIndices) - 1
	}
	bestEntry := -1
	bestDistance := len(m.detailLineEntryIndices) + 1
	for lineIndex, entryIndex := range m.detailLineEntryIndices {
		if entryIndex < 0 || m.detailBlockIndexForEntry(entryIndex) < 0 {
			continue
		}
		distance := detailLineDistance(lineIndex, anchor)
		if distance >= bestDistance {
			continue
		}
		bestEntry = entryIndex
		bestDistance = distance
	}
	if bestEntry < 0 {
		t.Fatalf("expected center visible selectable detail entry, owners=%+v", m.detailLineEntryIndices)
	}
	return bestEntry
}
