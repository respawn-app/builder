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
	if !strings.Contains(collapsed, "$ cat large.txt") || !strings.Contains(collapsed, "▶︎") {
		t.Fatalf("expected collapsed tool input, got %q", collapsed)
	}
	if strings.Contains(collapsed, "line 2") {
		t.Fatalf("expected collapsed detail to hide tool output, got %q", collapsed)
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	expanded := xansi.Strip(m.View())
	if !strings.Contains(expanded, "$ cat large.txt") || !strings.Contains(expanded, "▼") || !strings.Contains(expanded, "line 2") {
		t.Fatalf("expected expanded tool input and output, got %q", expanded)
	}
}

func TestCompactDetailNavigatesByMessageAndKeepsMultipleExpanded(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(12))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "first user\nhidden\nhidden\nshown on expand"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "first assistant\nhidden\nhidden\nshown on expand"})
	m = updateModel(t, m, ToggleModeMsg{})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	rendered := xansi.Strip(m.View())
	if strings.Count(rendered, "▼") != 2 {
		t.Fatalf("expected both messages expanded, got %q", rendered)
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
	if m.detailSelectedActive {
		t.Fatalf("expected stale detail selection cleared, got entry %d", m.detailSelectedEntry)
	}
	if len(m.detailExpandedEntries) != 0 {
		t.Fatalf("expected stale expanded entries cleared, got %+v", m.detailExpandedEntries)
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
	if !strings.Contains(rendered, "$ printf 'one\\n'…") || !strings.Contains(rendered, "▶︎") {
		t.Fatalf("expected completed shell call to stay compact, got %q", rendered)
	}
	if strings.Contains(rendered, "printf 'two") {
		t.Fatalf("expected collapsed completed shell call to hide second command line, got %q", rendered)
	}
}

func TestCompactDetailDividersWrapExpandedRunsOnly(t *testing.T) {
	tests := []struct {
		name          string
		expandFirst   bool
		expandSecond  bool
		wantDividers  int
		forbidBetween bool
	}{
		{name: "collapsed collapsed", wantDividers: 0},
		{name: "collapsed expanded", expandSecond: true, wantDividers: 2},
		{name: "expanded collapsed", expandFirst: true, wantDividers: 2},
		{name: "expanded expanded", expandFirst: true, expandSecond: true, wantDividers: 3, forbidBetween: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel(WithCompactDetail(), WithPreviewLines(20))
			m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "first entry"})
			m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "second entry"})
			m = updateModel(t, m, ToggleModeMsg{})
			if tt.expandSecond {
				m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			}
			if tt.expandFirst {
				m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
				m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			}

			rendered := xansi.Strip(m.View())
			if got := strings.Count(rendered, detailDivider()); got != tt.wantDividers {
				t.Fatalf("divider count = %d, want %d in %q", got, tt.wantDividers, rendered)
			}
			if tt.forbidBetween && strings.Contains(rendered, "first entry\n"+detailDivider()+"\n❮ second entry") {
				t.Fatalf("did not expect duplicate divider between consecutive expanded entries, got %q", rendered)
			}
		})
	}
}

func TestCompactDetailHidesMarkerWhenExpansionDoesNotChangeEntry(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(6))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "short answer"})
	m = updateModel(t, m, ToggleModeMsg{})

	rendered := xansi.Strip(m.View())
	if strings.Contains(rendered, "▶︎") || strings.Contains(rendered, "▼") {
		t.Fatalf("did not expect marker for entry whose expansion is identical, got %q", rendered)
	}
}

func TestCompactDetailRightMarkerReservesRowSpace(t *testing.T) {
	m := NewModel(WithCompactDetail(), WithPreviewLines(6))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 6, Width: 24})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "abcdefghijklmnopqrstuvwxyz\n2\n3\n4"})
	m = updateModel(t, m, ToggleModeMsg{})

	line := lineContaining(m.View(), "abc")
	if line == "" {
		t.Fatalf("expected detail line, got %q", m.View())
	}
	plain := xansi.Strip(line)
	if !strings.HasSuffix(plain, "▶︎") {
		t.Fatalf("expected right-aligned marker, got %q", plain)
	}
	if strings.Contains(plain[:len(plain)-len("▶︎")], "▶︎") {
		t.Fatalf("expected marker only at row end, got %q", plain)
	}
	if got := lipgloss.Width(line); got != 24 {
		t.Fatalf("expected marker row width 24, got %d for %q", got, line)
	}
}
