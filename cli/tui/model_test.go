package tui

import (
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/tools"
	triggerhandofftool "builder/server/tools/triggerhandoff"
	"builder/shared/theme"
	"builder/shared/toolspec"
	"builder/shared/transcript"
	patchformat "builder/shared/transcript/patchformat"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
)

func testPatchRender(lines ...patchformat.RenderedLine) *patchformat.RenderedPatch {
	return &patchformat.RenderedPatch{DetailLines: lines}
}

func TestModeToggleReturnsToLatestOngoingTail(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, StreamAssistantMsg{Delta: "l1\nl2\nl3\nl4"})
	m = updateModel(t, m, ScrollOngoingMsg{Delta: -1})

	if got := m.OngoingScroll(); got != 1 {
		t.Fatalf("scroll before toggle = %d, want 1", got)
	}

	before := m.View()
	linesBefore := strings.Split(before, "\n")
	if len(linesBefore) != 2 {
		t.Fatalf("ongoing lines = %d, want 2", len(linesBefore))
	}
	if strings.TrimSpace(linesBefore[0]) != "l2" || strings.TrimSpace(linesBefore[1]) != "l3" {
		t.Fatalf("unexpected ongoing view before toggle: %q", before)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	if got := m.Mode(); got != ModeDetail {
		t.Fatalf("mode after first toggle = %q, want %q", got, ModeDetail)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	if got := m.Mode(); got != ModeOngoing {
		t.Fatalf("mode after second toggle = %q, want %q", got, ModeOngoing)
	}
	if got, want := m.OngoingScroll(), m.maxOngoingScroll(); got != want {
		t.Fatalf("scroll after roundtrip toggle = %d, want latest %d", got, want)
	}

	after := strings.Split(m.View(), "\n")
	if len(after) != 2 {
		t.Fatalf("ongoing lines after toggle = %d, want 2", len(after))
	}
	if strings.TrimSpace(after[0]) != "l3" || strings.TrimSpace(after[1]) != "l4" {
		t.Fatalf("unexpected ongoing tail after toggle: %q", m.View())
	}
}

func TestModeToggleReSnapsTailAfterViewportShrink(t *testing.T) {
	m := NewModel(WithPreviewLines(7))
	for i := 1; i <= 20; i++ {
		m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "line"})
	}

	m = updateModel(t, m, ToggleModeMsg{}) // detail
	for i := 0; i < 10; i++ {
		m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "new"})
	}
	m = updateModel(t, m, ToggleModeMsg{}) // ongoing snaps using detail viewport

	beforeResize := m.OngoingScroll()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 4, Width: 80})
	afterResize := m.OngoingScroll()
	if afterResize <= beforeResize {
		t.Fatalf("expected viewport resize to re-snap ongoing tail, got %d from %d", afterResize, beforeResize)
	}
	if got, want := m.OngoingScroll(), m.maxOngoingScroll(); got != want {
		t.Fatalf("expected to stay at bottom after resize snap, got %d want %d", got, want)
	}
}

func TestModeToggleReturnsToBottomWhenConversationGrewInDetail(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	m = updateModel(t, m, ScrollOngoingMsg{Delta: -1})
	before := m.OngoingScroll()
	if before >= m.maxOngoingScroll() {
		t.Fatalf("expected to start above bottom, got %d", before)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a4"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a5"})
	m = updateModel(t, m, ToggleModeMsg{})

	if got, want := m.OngoingScroll(), m.maxOngoingScroll(); got != want {
		t.Fatalf("expected ongoing to snap to bottom after growth in detail: got %d want %d", got, want)
	}
	view := plainTranscript(m.View())
	if !strings.Contains(view, "a5") {
		t.Fatalf("expected newest entry visible after returning from detail, got %q", view)
	}
}

func TestToggleToDetailStartsAtBottom(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a4"})

	m = updateModel(t, m, ToggleModeMsg{})

	if m.DetailMetricsResolved() {
		t.Fatal("expected detail entry to remain lazily bottom-anchored until the first navigation action")
	}
	view := plainTranscript(m.View())
	if !strings.Contains(view, "a4") {
		t.Fatalf("expected detail toggle to show newest content, got %q", view)
	}
}

func TestToggleToDetailCanSkipWarmup(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})

	m = updateModel(t, m, ToggleModeMsg{SkipDetailWarmup: true})

	if got := m.Mode(); got != ModeDetail {
		t.Fatalf("mode after skip-warmup toggle = %q, want %q", got, ModeDetail)
	}
	if !m.detailDirty {
		t.Fatal("expected detail snapshot to remain dirty when warmup is skipped")
	}
	if len(m.detailLines) != 0 {
		t.Fatalf("expected no detail snapshot lines after skip-warmup toggle, got %d", len(m.detailLines))
	}
	if got := m.detailScroll; got != 0 {
		t.Fatalf("detail scroll after skip-warmup toggle = %d, want 0", got)
	}
}

func TestDetailSetConversationPreservesFocusedAbsoluteEntryAcrossBaseOffsetShift(t *testing.T) {
	m := NewModel(WithPreviewLines(8))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 8, Width: 80})
	m = updateModel(t, m, SetConversationMsg{
		BaseOffset:   0,
		TotalEntries: 1000,
		Entries:      transcriptEntriesRange(0, 1000),
	})
	m = updateModel(t, m, SetModeMsg{Mode: ModeDetail})
	m = updateModel(t, m, FocusTranscriptEntryMsg{EntryIndex: 500, Center: true})

	beforeStart, beforeEnd, ok := m.DetailVisibleEntryRange()
	if !ok {
		t.Fatal("expected visible range before base offset shift")
	}
	if beforeStart > 500 || beforeEnd < 500 {
		t.Fatalf("expected entry 500 visible before base offset shift, got range %d..%d", beforeStart, beforeEnd)
	}

	m = updateModel(t, m, SetConversationMsg{
		BaseOffset:   200,
		TotalEntries: 1200,
		Entries:      transcriptEntriesRange(200, 1200),
	})

	afterStart, afterEnd, ok := m.DetailVisibleEntryRange()
	if !ok {
		t.Fatal("expected visible range after base offset shift")
	}
	if afterStart > 500 || afterEnd < 500 {
		t.Fatalf("expected entry 500 to remain visible after base offset shift, got range %d..%d", afterStart, afterEnd)
	}
}

func TestOngoingShowsFullConversationContext(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "first question"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "first answer"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "second question"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "second answer"})

	view := plainTranscript(m.View())
	if !containsInOrder(view, "❯", "first question", "❮", "first answer", "❯", "second question", "❮", "second answer") {
		t.Fatalf("expected first user message in ongoing view, got %q", view)
	}
}

func TestOngoingDoesNotPinOngoingErrorToBottomLine(t *testing.T) {
	m := NewModel(WithPreviewLines(4))
	m = updateModel(t, m, SetConversationMsg{
		Entries:      []TranscriptEntry{{Role: "assistant", Text: "line one"}},
		Ongoing:      "line two",
		OngoingError: "error: should not pin",
	})

	view := plainTranscript(m.View())
	if strings.Contains(view, "should not pin") {
		t.Fatalf("did not expect ongoing error to consume a fixed viewport line, got %q", view)
	}
	if !containsInOrder(view, "line one", "line two") {
		t.Fatalf("expected transcript content to remain visible, got %q", view)
	}
}

func TestErrorEntryVisibleInDetailAndHiddenInOngoing(t *testing.T) {
	m := NewModel(WithPreviewLines(6))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "ready"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "error", Text: "boom trace"})

	ongoing := m.View()
	ongoingPlain := plainTranscript(ongoing)
	if strings.Contains(ongoingPlain, "boom trace") {
		t.Fatalf("expected error entry hidden in ongoing view, got %q", ongoingPlain)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := m.View()
	plain := plainTranscript(detail)
	if !containsInOrder(plain, "❮", "ready", "!", "boom trace") {
		t.Fatalf("expected error entry in detail transcript history, got %q", plain)
	}
	renderedError := m.palette().error.Render("boom trace")
	if !strings.Contains(detail, renderedError) {
		t.Fatalf("expected error text to use error style in detail, got %q", detail)
	}
}

func TestShellToolPreviewUsesShellHighlightingForWrappedHyphenatedPath(t *testing.T) {
	m := NewModel()
	out := m.renderEntryText("tool_shell", "./gradlew -p apps/respawn detektFormat > docs/tmp/build-triage-2026-03-15/detektFormat.log 2>&1", 56, &transcript.ToolCallMeta{
		RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindShell},
	}, false)
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "2026-03-15") || !strings.Contains(plain, "detektFormat.log") {
		t.Fatalf("expected wrapped shell path to remain visible, got %q", plain)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected shell command preview to be syntax-highlighted, got %q", out)
	}
}

func TestRenderEntryTextHighlightsShellCommandForShellHint(t *testing.T) {
	m := NewModel()
	meta := &transcript.ToolCallMeta{RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindShell}}

	out := m.renderEntryText("tool_shell_success", "./gradlew -p apps/respawn detektFormat > docs/tmp/build-triage-2026-03-15/detektFormat.log 2>&1", 120, meta, false)
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected shell-highlighted output, got %q", out)
	}
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "build-triage-2026-03-15") {
		t.Fatalf("expected highlighted shell command text preserved, got %q", plain)
	}
}

func TestBackgroundNoticeUsesCompactTextInOngoingAndFullTextInDetail(t *testing.T) {
	m := NewModel(WithPreviewLines(8))
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:        "system",
		Text:        "Background shell 1000 completed.\nExit code: 0\nLog lines: 5\nOutput:\nvery long output",
		OngoingText: "Background shell 1000 completed (exit 0)",
	})

	ongoing := plainTranscript(m.View())
	if !strings.Contains(ongoing, "Background shell 1000 completed (exit 0)") {
		t.Fatalf("expected compact background notice in ongoing, got %q", ongoing)
	}
	if strings.Contains(ongoing, "Log lines: 5") || strings.Contains(ongoing, "very long output") {
		t.Fatalf("did not expect detail background notice in ongoing, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := plainTranscript(m.View())
	if !strings.Contains(detail, "Background shell 1000 completed.") || !strings.Contains(detail, "Log lines: 5") || !strings.Contains(detail, "very long output") {
		t.Fatalf("expected full background notice in detail, got %q", detail)
	}
	if strings.Contains(detail, "Background shell 1000 completed (exit 0)") {
		t.Fatalf("did not expect compact background line in detail, got %q", detail)
	}
}

func TestDetailSnapshotIsStaticUntilRetoggle(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "question"})
	m = updateModel(t, m, StreamAssistantMsg{Delta: "alpha"})
	m = updateModel(t, m, ToggleModeMsg{})

	snapshot := plainTranscript(m.View())
	if !containsInOrder(snapshot, "❮", "alpha") {
		t.Fatalf("detail snapshot missing assistant stream: %q", snapshot)
	}

	m = updateModel(t, m, StreamAssistantMsg{Delta: " beta"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool", Text: "ran"})

	if got := plainTranscript(m.View()); got != snapshot {
		t.Fatalf("detail snapshot changed while in detail mode:\ninitial=%q\ncurrent=%q", snapshot, got)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, ToggleModeMsg{})
	refreshed := plainTranscript(m.View())

	if refreshed == snapshot {
		t.Fatalf("detail snapshot did not refresh after mode roundtrip")
	}
	if !containsInOrder(refreshed, "❮", "alpha beta") {
		t.Fatalf("refreshed snapshot missing full assistant stream: %q", refreshed)
	}
	if !containsInOrder(refreshed, "•", "ran") {
		t.Fatalf("refreshed snapshot missing new transcript entry: %q", refreshed)
	}
}

func TestDetailDoesNotScrollOnIncomingMessages(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, ScrollOngoingMsg{Delta: 1})

	before := plainTranscript(m.View())
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	after := plainTranscript(m.View())
	if after != before {
		t.Fatalf("detail view changed while new messages arrived:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestClearOngoingAssistantMsgDropsPartialStream(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, StreamAssistantMsg{Delta: "partial"})
	m = updateModel(t, m, ClearOngoingAssistantMsg{})
	m = updateModel(t, m, StreamAssistantMsg{Delta: "final"})
	m = updateModel(t, m, CommitAssistantMsg{})
	m = updateModel(t, m, ToggleModeMsg{})

	snapshot := plainTranscript(m.View())
	if strings.Contains(snapshot, "partial") {
		t.Fatalf("snapshot should not contain discarded attempt delta: %q", snapshot)
	}
	if !strings.Contains(snapshot, "final") {
		t.Fatalf("snapshot missing committed final assistant output: %q", snapshot)
	}
}

func TestOngoingShowsCommittedAssistantAfterCommit(t *testing.T) {
	m := NewModel(WithPreviewLines(3))
	m = updateModel(t, m, StreamAssistantMsg{Delta: "line1\nline2"})
	m = updateModel(t, m, CommitAssistantMsg{})

	view := plainTranscript(m.View())
	if !strings.Contains(view, "line1") || !strings.Contains(view, "line2") {
		t.Fatalf("ongoing view should keep committed assistant visible, got %q", view)
	}
}

func TestOngoingAutoFollowsWhenUserIsAtBottom(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	if got, want := m.OngoingScroll(), m.maxOngoingScroll(); got != want {
		t.Fatalf("expected to start at bottom, got %d want %d", got, want)
	}

	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	if got, want := m.OngoingScroll(), m.maxOngoingScroll(); got != want {
		t.Fatalf("scroll after growth = %d, want bottom %d", got, want)
	}
	view := plainTranscript(m.View())
	if !strings.Contains(view, "a3") {
		t.Fatalf("expected latest line visible at bottom, got %q", view)
	}
}

func TestOngoingDoesNotAutoFollowWhenUserScrolledUp(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a4"})
	if got, want := m.OngoingScroll(), m.maxOngoingScroll(); got != want {
		t.Fatalf("expected to start at bottom, got %d want %d", got, want)
	}

	m = updateModel(t, m, ScrollOngoingMsg{Delta: -1})
	pinned := m.OngoingScroll()
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a5"})
	if got := m.OngoingScroll(); got != pinned {
		t.Fatalf("scroll should stay pinned when user scrolled up, got %d want %d", got, pinned)
	}
	if m.OngoingScroll() == m.maxOngoingScroll() {
		t.Fatalf("expected to remain above bottom after new message")
	}
}

func TestMouseWheelScrollsOngoingView(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a4"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a5"})

	start := m.OngoingScroll()
	if start == 0 {
		t.Fatalf("expected ongoing mode to start at bottom, got ongoingScroll=%d", start)
	}

	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelUp, Type: tea.MouseWheelUp})
	afterUp := m.OngoingScroll()
	if afterUp >= start {
		t.Fatalf("expected wheel up to scroll ongoing view up, got %d from %d", afterUp, start)
	}

	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelDown, Type: tea.MouseWheelDown})
	if got := m.OngoingScroll(); got != start {
		t.Fatalf("expected wheel down to return ongoing scroll to start, got %d want %d", got, start)
	}
}

func TestMouseWheelScrollsDetailView(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u3"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	m = updateModel(t, m, ToggleModeMsg{})
	ongoingStart := m.ongoingScroll
	if m.DetailMetricsResolved() {
		t.Fatal("expected detail mode to defer global scroll metric resolution until navigation")
	}
	initial := plainTranscript(m.View())

	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelUp, Type: tea.MouseWheelUp})
	if m.DetailMetricsResolved() {
		t.Fatal("expected first detail navigation to stay lazy")
	}
	afterUp := m.DetailScroll()
	if afterUp <= 0 {
		t.Fatalf("expected wheel up to advance lazy detail offset, got %d", afterUp)
	}
	if plainTranscript(m.View()) == initial {
		t.Fatal("expected detail wheel up to change the visible viewport")
	}
	if got := m.ongoingScroll; got != ongoingStart {
		t.Fatalf("expected detail wheel scroll to leave ongoing scroll untouched, got %d want %d", got, ongoingStart)
	}

	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelDown, Type: tea.MouseWheelDown})
	if got := m.DetailScroll(); got != 0 {
		t.Fatalf("expected wheel down to return lazy detail offset to bottom, got %d", got)
	}
	if got := m.ongoingScroll; got != ongoingStart {
		t.Fatalf("expected detail wheel scroll to keep ongoing scroll unchanged, got %d want %d", got, ongoingStart)
	}
}

func TestPageKeysScrollActiveView(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a4"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a5"})

	start := m.OngoingScroll()
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if got := m.OngoingScroll(); got >= start {
		t.Fatalf("expected pgup to scroll up ongoing view, got %d from %d", got, start)
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if got, want := m.OngoingScroll(), m.maxOngoingScroll(); got != want {
		t.Fatalf("expected pgdown to return to bottom, got %d want %d", got, want)
	}
}

func TestFocusTranscriptEntryCentersOngoingViewport(t *testing.T) {
	m := NewModel(WithPreviewLines(6))
	for i := 0; i < 40; i++ {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		m = updateModel(t, m, AppendTranscriptMsg{Role: role, Text: fmt.Sprintf("line %d", i)})
	}

	entryIndex := 10
	start, end, ok := m.ongoingLineRangeForEntry(entryIndex)
	if !ok {
		t.Fatalf("expected line range for transcript entry %d", entryIndex)
	}
	midpoint := (start + end) / 2
	expected := clamp(midpoint-m.viewportLines/2, 0, m.maxOngoingScroll())

	m = updateModel(t, m, FocusTranscriptEntryMsg{EntryIndex: entryIndex, Center: true})
	if got := m.OngoingScroll(); got != expected {
		t.Fatalf("expected centered scroll %d for entry %d, got %d", expected, entryIndex, got)
	}
}

func TestFocusTranscriptEntryClampsNearTopAndBottom(t *testing.T) {
	m := NewModel(WithPreviewLines(6))
	for i := 0; i < 40; i++ {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		m = updateModel(t, m, AppendTranscriptMsg{Role: role, Text: fmt.Sprintf("line %d", i)})
	}

	topEntry := 0
	m = updateModel(t, m, FocusTranscriptEntryMsg{EntryIndex: topEntry, Center: true})
	if got := m.OngoingScroll(); got != 0 {
		t.Fatalf("expected top entry focus to clamp to scroll 0, got %d", got)
	}
	if start, end, ok := m.ongoingLineRangeForEntry(topEntry); !ok || end < m.OngoingScroll() || start >= m.OngoingScroll()+m.viewportLines {
		t.Fatalf("expected top entry visible after focus, range=(%d,%d) scroll=%d", start, end, m.OngoingScroll())
	}

	bottomEntry := len(m.transcript) - 1
	m = updateModel(t, m, FocusTranscriptEntryMsg{EntryIndex: bottomEntry, Center: true})
	if got, want := m.OngoingScroll(), m.maxOngoingScroll(); got != want {
		t.Fatalf("expected bottom entry focus to clamp to max scroll %d, got %d", want, got)
	}
	if start, end, ok := m.ongoingLineRangeForEntry(bottomEntry); !ok || end < m.OngoingScroll() || start >= m.OngoingScroll()+m.viewportLines {
		t.Fatalf("expected bottom entry visible after focus, range=(%d,%d) scroll=%d", start, end, m.OngoingScroll())
	}
}

func TestFocusTranscriptEntryCentersInDetailMode(t *testing.T) {
	m := NewModel(WithPreviewLines(4))
	for i := 0; i < 20; i++ {
		m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("line %d", i)})
	}
	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, FocusTranscriptEntryMsg{EntryIndex: 0, Center: true})
	if m.detailScroll != 0 {
		t.Fatalf("expected detail focus of first entry to clamp to top, got %d", m.detailScroll)
	}

	m = updateModel(t, m, FocusTranscriptEntryMsg{EntryIndex: 10, Center: true})
	if m.detailScroll <= 0 {
		t.Fatalf("expected detail focus of middle entry to scroll into transcript, got %d", m.detailScroll)
	}
	start, end, ok := m.detailLineRangeForEntry(10)
	if !ok {
		t.Fatal("expected detail line range for focused entry")
	}
	midpoint := (start + end) / 2
	visibleMid := m.detailScroll + m.viewportLines/2
	if diff := absInt(midpoint - visibleMid); diff > 2 {
		t.Fatalf("expected focused entry near viewport center, midpoint=%d visibleMid=%d", midpoint, visibleMid)
	}
}

func TestFocusTranscriptEntryCentersInDetailModeFromLazyEntry(t *testing.T) {
	m := NewModel(WithPreviewLines(4))
	for i := 0; i < 20; i++ {
		m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("line %d", i)})
	}
	m = updateModel(t, m, ToggleModeMsg{})
	if m.DetailMetricsResolved() {
		t.Fatal("expected detail entry to remain lazy before focus")
	}

	m = updateModel(t, m, FocusTranscriptEntryMsg{EntryIndex: 10, Center: true})
	if !m.DetailMetricsResolved() {
		t.Fatal("expected focus to resolve detail metrics on the authoritative model")
	}
	if m.detailScroll <= 0 {
		t.Fatalf("expected lazy detail focus to scroll into transcript, got %d", m.detailScroll)
	}
	if plain := plainTranscript(m.View()); !strings.Contains(plain, "line 10") {
		t.Fatalf("expected focused entry visible after lazy detail focus, got %q", plain)
	}
}

func TestFocusTranscriptEntryCentersFromLazyDetailEntry(t *testing.T) {
	m := NewModel(WithPreviewLines(4))
	for i := 0; i < 20; i++ {
		m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("line %d", i)})
	}
	m = updateModel(t, m, ToggleModeMsg{})
	if m.DetailMetricsResolved() {
		t.Fatal("expected detail entry to start lazy before focus")
	}

	m = updateModel(t, m, FocusTranscriptEntryMsg{EntryIndex: 10, Center: true})
	if !m.DetailMetricsResolved() {
		t.Fatal("expected focus to resolve detail metrics on the authoritative model")
	}
	if m.detailScroll <= 0 {
		t.Fatalf("expected focus from lazy detail entry to scroll into transcript, got %d", m.detailScroll)
	}
	plain := plainTranscript(m.View())
	if !strings.Contains(plain, "line 10") {
		t.Fatalf("expected focused entry visible after lazy detail focus, got %q", plain)
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestDetailUsesRequestedSymbolsAndDividers(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "hello"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "hi"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_call", Text: "call"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result", Text: "result"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := plainTranscript(m.View())
	if !containsInOrder(view, "❯", "hello") {
		t.Fatalf("expected user symbol, got %q", view)
	}
	if !containsInOrder(view, "❮", "hi") {
		t.Fatalf("expected assistant symbol, got %q", view)
	}
	if !strings.Contains(view, "•") || !strings.Contains(view, "call") || !strings.Contains(view, "result") {
		t.Fatalf("expected tool call/result pair with tool symbol, got %q", view)
	}
	if got := strings.Count(view, strings.Repeat("─", 24)); got != 2 {
		t.Fatalf("expected 2 dividers for 3 blocks, got %d in %q", got, view)
	}
}

func TestDetailShellToolUsesDollarPrefixAndKeepsSuccessColorRole(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "pwd",
		ToolCall: &transcript.ToolCallMeta{
			IsShell:      true,
			Command:      "pwd",
			TimeoutLabel: "timeout: 5m",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "/tmp"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := plainTranscript(m.View())
	if !containsInOrder(view, "$", "pwd") {
		t.Fatalf("expected shell tool to use $ prefix, got %q", view)
	}
	if strings.Contains(view, "• pwd") {
		t.Fatalf("expected no dot prefix for shell tool, got %q", view)
	}
}

func TestDetailMatchesParallelShellResultsByCallID(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "echo a",
		ToolCallID: "call_a",
		ToolCall: &transcript.ToolCallMeta{
			IsShell: true,
			Command: "echo a",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "echo b",
		ToolCallID: "call_b",
		ToolCall: &transcript.ToolCallMeta{
			IsShell: true,
			Command: "echo b",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_a", Text: "out-a"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_b", Text: "out-b"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := plainTranscript(m.View())
	idxCallA := strings.Index(view, "echo a")
	idxOutA := strings.Index(view, "out-a")
	idxCallB := strings.Index(view, "echo b")
	idxOutB := strings.Index(view, "out-b")
	if idxCallA < 0 || idxOutA < 0 || idxCallB < 0 || idxOutB < 0 {
		t.Fatalf("expected both calls and outputs in view, got %q", view)
	}
	if !(idxCallA < idxOutA && idxOutA < idxCallB && idxCallB < idxOutB) {
		t.Fatalf("expected each output to stay with matching call, got %q", view)
	}
	if strings.Contains(view, "• out-a") || strings.Contains(view, "• out-b") {
		t.Fatalf("expected no standalone tool result blocks for matched call IDs, got %q", view)
	}
}

func TestDetailDoesNotMatchAdjacentResultWhenCallIDMissing(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "echo missing-id",
		ToolCall: &transcript.ToolCallMeta{
			IsShell: true,
			Command: "echo missing-id",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_result_ok",
		ToolCallID: "call_other",
		Text:       "out-other",
	})
	m = updateModel(t, m, ToggleModeMsg{})

	view := plainTranscript(m.View())
	if !containsInOrder(view, "$", "echo missing-id", "•", "out-other") {
		t.Fatalf("expected unmatched result to remain standalone, got %q", view)
	}
}

func TestDetailAskQuestionRendersQuestionSuggestionsAndAnswer(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "question: Choose scope?\nsuggestions: - flat scan\n  - Recursive scan",
		ToolCall: &transcript.ToolCallMeta{
			ToolName:               "ask_question",
			Question:               "Choose scope?",
			Suggestions:            []string{"flat scan", "Recursive scan"},
			RecommendedOptionIndex: 1,
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "Use flat scan."})
	m = updateModel(t, m, ToggleModeMsg{})

	plain := plainTranscript(m.View())
	if strings.Contains(plain, "question:") || strings.Contains(plain, "suggestions:") {
		t.Fatalf("expected ask_question labels removed from detail view, got %q", plain)
	}
	if !containsInOrder(plain, "?", "Choose scope?", "- flat scan", "- Recursive scan", "Use flat scan.") {
		t.Fatalf("expected question, suggestions and answer in detail order, got %q", plain)
	}

	colored := m.View()
	if !strings.Contains(colored, m.palette().model.Render("- flat scan")) {
		t.Fatalf("expected recommended suggestion to be green in detail view, got %q", colored)
	}
	if !strings.Contains(colored, m.palette().preview.Faint(true).Render("- Recursive scan")) {
		t.Fatalf("expected non-recommended suggestions to stay muted in detail view, got %q", colored)
	}
	if !strings.Contains(colored, m.palette().user.Render("Use flat scan.")) {
		t.Fatalf("expected answer to use user color in detail view, got %q", colored)
	}
}

func TestOngoingAskQuestionRendersQuestionAndAnswerOnly(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "question: Choose scope?\nsuggestions: - flat scan\n  - Recursive scan",
		ToolCall: &transcript.ToolCallMeta{
			ToolName:               "ask_question",
			Question:               "Choose scope?",
			Suggestions:            []string{"flat scan", "Recursive scan"},
			RecommendedOptionIndex: 1,
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "Use flat scan."})

	plain := plainTranscript(m.View())
	if strings.Contains(plain, "question:") || strings.Contains(plain, "suggestions:") {
		t.Fatalf("expected ask_question labels removed from ongoing view, got %q", plain)
	}
	if strings.Contains(plain, "- flat scan") || strings.Contains(plain, "- Recursive scan") {
		t.Fatalf("expected ongoing view to omit ask_question suggestions, got %q", plain)
	}
	if !containsInOrder(plain, "?", "Choose scope?", "Use flat scan.") {
		t.Fatalf("expected question and answer in ongoing view, got %q", plain)
	}

	colored := m.View()
	if !strings.Contains(colored, m.palette().user.Render("Use flat scan.")) {
		t.Fatalf("expected answer to use user color in ongoing view, got %q", colored)
	}
}

func TestOngoingCompactsToolCallAndHidesThinking(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "run command"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "thinking", Text: "internal trace"})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "pwd",
		ToolCall: &transcript.ToolCallMeta{
			IsShell:      true,
			Command:      "pwd",
			TimeoutLabel: "timeout: 5m",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "/tmp"})

	view := m.View()
	if strings.Contains(view, "internal trace") {
		t.Fatalf("expected thinking trace hidden in ongoing view, got %q", view)
	}
	if !strings.Contains(view, "pwd") {
		t.Fatalf("expected compact one-line tool input in ongoing view, got %q", view)
	}
	if strings.Contains(view, "workdir: /tmp") {
		t.Fatalf("expected tool input to stay one line in ongoing view, got %q", view)
	}
	if strings.Contains(view, "/tmp") {
		t.Fatalf("expected tool output to be omitted in ongoing view, got %q", view)
	}
}

func TestDetailShowsReasoningSummaryAsSeparateEntry(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reasoning", Text: "Plan summary"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a"})

	ongoing := plainTranscript(m.View())
	if strings.Contains(ongoing, "Plan summary") {
		t.Fatalf("expected reasoning hidden in ongoing view, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	colored := m.View()
	detail := plainTranscript(m.View())
	if !strings.Contains(detail, "Plan summary") {
		t.Fatalf("expected reasoning summary entry in detail view, got %q", detail)
	}
	if strings.Contains(detail, "…") {
		t.Fatalf("expected reasoning entry without ellipsis prefix, got %q", detail)
	}
	if !strings.Contains(colored, "Plan summary") {
		t.Fatalf("expected reasoning summary visible in colored detail view, got %q", colored)
	}
}

func TestDetailReordersTrailingReasoningBeforeAssistantResponse(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "final answer"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reasoning", Text: "hidden plan"})
	m = updateModel(t, m, ToggleModeMsg{})

	detail := plainTranscript(m.View())
	if !containsInOrder(detail, "hidden plan", "❮", "final answer") {
		t.Fatalf("expected trailing reasoning rendered before assistant response, got %q", detail)
	}
}

func TestDetailReordersTrailingReasoningBeforeToolCalls(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_call", Text: "run"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reasoning", Text: "decide to call tool"})
	m = updateModel(t, m, ToggleModeMsg{})

	detail := plainTranscript(m.View())
	if !containsInOrder(detail, "decide to call tool", "•", "run") {
		t.Fatalf("expected trailing reasoning rendered before tool call, got %q", detail)
	}
}

func TestDetailGroupsReasoningEntriesWithoutMarkdownFormatting(t *testing.T) {
	m := NewModel(WithPreviewLines(30))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reasoning", Text: "**First step**"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reasoning", Text: "`second` details"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reasoning", Text: "**third**"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "done"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := plainTranscript(m.View())
	if !containsInOrder(view, "**First step**", "`second` details", "**third**") {
		t.Fatalf("expected grouped reasoning text to remain unformatted, got %q", view)
	}
	if !strings.Contains(view, "`second` details") {
		t.Fatalf("expected reasoning text to preserve inline formatting markers, got %q", view)
	}
	if got := strings.Count(view, strings.Repeat("─", 24)); got != 2 {
		t.Fatalf("expected 2 dividers for user/reasoning/assistant groups, got %d in %q", got, view)
	}
}

func TestDetailRefreshesForLiveStreamingReasoning(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u"})
	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, UpsertStreamingReasoningMsg{Key: "rs_1:summary:0", Role: "reasoning", Text: "Plan summary"})

	view := plainTranscript(m.View())
	if !strings.Contains(view, "Plan summary") {
		t.Fatalf("expected live reasoning to refresh detail snapshot, got %q", view)
	}

	m = updateModel(t, m, ClearStreamingReasoningMsg{})
	view = plainTranscript(m.View())
	if strings.Contains(view, "Plan summary") {
		t.Fatalf("expected live reasoning cleared from detail snapshot, got %q", view)
	}
}

func TestCompactionNoticeAndSummaryRenderingByMode(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "compaction_notice", Text: "context compacted for the 1st time"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "compaction_summary", Text: "line one\nline two"})

	ongoing := plainTranscript(m.View())
	if !containsInOrder(ongoing, "@", "context compacted for the 1st time") {
		t.Fatalf("expected compaction notice in ongoing view, got %q", ongoing)
	}
	if strings.Contains(ongoing, "line one") || strings.Contains(ongoing, "line two") {
		t.Fatalf("expected compaction summary hidden in ongoing view, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := plainTranscript(m.View())
	if !containsInOrder(detail, "@", "context compacted for the 1st time", "@", "line one", "line two") {
		t.Fatalf("expected compaction notice and full summary in detail view, got %q", detail)
	}
}

func TestDeveloperContextRendersDetailOnly(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: roleDeveloperContext, Text: "AGENTS context block"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "done"})

	ongoing := plainTranscript(m.View())
	if strings.Contains(ongoing, "AGENTS context block") {
		t.Fatalf("expected developer context hidden in ongoing view, got %q", ongoing)
	}
	if !strings.Contains(ongoing, "done") {
		t.Fatalf("expected assistant visible in ongoing view, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := plainTranscript(m.View())
	if !containsInOrder(detail, "AGENTS context block", "❮", "done") {
		t.Fatalf("expected developer context visible in detail view, got %q", detail)
	}
}

func TestDeveloperFeedbackAndInterruptionRenderInOngoing(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: roleDeveloperFeedback, Text: "phase mismatch warning"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: roleInterruption, Text: "User interrupted you"})

	ongoingRendered := m.View()
	ongoing := plainTranscript(ongoingRendered)
	if !containsInOrder(ongoing, "phase mismatch warning", interruptionUserVisibleText) {
		t.Fatalf("expected ongoing-visible developer feedback and interruption, got %q", ongoing)
	}
	if strings.Contains(ongoing, "User interrupted you") {
		t.Fatalf("expected ongoing interruption to hide model-facing wording, got %q", ongoing)
	}
	if !strings.Contains(ongoingRendered, m.palette().error.Render(interruptionUserVisibleText)) {
		t.Fatalf("expected ongoing interruption to use error style, got %q", ongoingRendered)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detailRendered := m.View()
	detail := plainTranscript(detailRendered)
	if !containsInOrder(detail, "phase mismatch warning", interruptionUserVisibleText) {
		t.Fatalf("expected developer feedback and interruption visible in detail view, got %q", detail)
	}
	if strings.Contains(detail, "User interrupted you") {
		t.Fatalf("expected detail interruption to hide model-facing wording, got %q", detail)
	}
	if !strings.Contains(detailRendered, m.palette().error.Render(interruptionUserVisibleText)) {
		t.Fatalf("expected detail interruption to use error style, got %q", detailRendered)
	}
}

func TestCompactionSoonReminderRendersDetailOnly(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "warning", Text: "Compaction soon: 92% of context window used."})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "done"})

	ongoing := plainTranscript(m.View())
	if strings.Contains(ongoing, "Compaction soon") {
		t.Fatalf("expected compaction soon reminder hidden in ongoing view, got %q", ongoing)
	}
	if !strings.Contains(ongoing, "done") {
		t.Fatalf("expected assistant visible in ongoing view, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := plainTranscript(m.View())
	if !containsInOrder(detail, "Compaction soon: 92% of context window used.", "❮", "done") {
		t.Fatalf("expected compaction soon reminder visible in detail view, got %q", detail)
	}
}

func TestHeadlessModeContextVariantsRenderDetailOnly(t *testing.T) {
	m := NewModel(WithPreviewLines(30))
	m = updateModel(t, m, AppendTranscriptMsg{Role: roleDeveloperContext, Text: "headless mode instructions"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: roleDeveloperContext, Text: "interactive mode instructions"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "done"})

	ongoing := plainTranscript(m.View())
	if strings.Contains(ongoing, "headless mode instructions") || strings.Contains(ongoing, "interactive mode instructions") {
		t.Fatalf("expected headless context variants hidden in ongoing view, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := plainTranscript(m.View())
	if !containsInOrder(detail, "headless mode instructions", "interactive mode instructions", "❮", "done") {
		t.Fatalf("expected headless context variants visible in detail view, got %q", detail)
	}
}

func TestManualCompactionCarryoverRenderingByMode(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: roleManualCompactionCarryover,
		Text: "# Last user message before manual compaction\n\nplease keep tests green",
	})

	ongoing := plainTranscript(m.View())
	if strings.Contains(ongoing, "Last user message before manual compaction") || strings.Contains(ongoing, "please keep tests green") {
		t.Fatalf("expected manual compaction carryover hidden in ongoing view, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := plainTranscript(m.View())
	if !containsInOrder(detail, "@", "# Last user message before manual compaction", "please keep tests green") {
		t.Fatalf("expected manual compaction carryover visible in detail view, got %q", detail)
	}
}

func TestReviewerStatusRendersConciseWithoutSuggestionsEntry(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "run task"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reviewer_status", Text: "Supervisor ran: 2 suggestions, no changes applied."})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "done"})

	ongoing := plainTranscript(m.View())
	if !strings.Contains(ongoing, "Supervisor ran: 2 suggestions, no changes applied.") {
		t.Fatalf("expected short reviewer status in ongoing view, got %q", ongoing)
	}
	if strings.Contains(ongoing, "Supervisor suggested:") || strings.Contains(ongoing, "1. First") {
		t.Fatalf("expected reviewer suggestions hidden in ongoing view, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := plainTranscript(m.View())
	if !containsInOrder(detail, "❯", "run task", "§", "Supervisor ran: 2 suggestions, no changes applied.", "❮", "done") {
		t.Fatalf("expected concise reviewer status visible in detail view, got %q", detail)
	}
	if strings.Contains(detail, "Supervisor suggested:") || strings.Contains(detail, "1. First") {
		t.Fatalf("expected reviewer suggestions hidden in detail view, got %q", detail)
	}
}

func TestReviewerVerboseSuggestionsRenderWhenIssuedAndStatusStaysConcise(t *testing.T) {
	m := NewModel(WithPreviewLines(30))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "run task"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reviewer_suggestions", Text: "Supervisor suggested:\n1. First\n2. Second", OngoingText: "Supervisor suggested:\n1. First\n2. Second"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reviewer_status", Text: "Supervisor ran: 2 suggestions, applied."})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "done"})

	ongoing := plainTranscript(m.View())
	if !containsInOrder(ongoing, "Supervisor suggested:", "1. First", "2. Second") {
		t.Fatalf("expected verbose reviewer suggestions entry in ongoing view, got %q", ongoing)
	}
	if !strings.Contains(ongoing, "Supervisor ran: 2 suggestions, applied.") {
		t.Fatalf("expected concise reviewer status in ongoing view, got %q", ongoing)
	}
	if strings.Count(ongoing, "Supervisor suggested:") != 1 {
		t.Fatalf("expected reviewer suggestions details only at issuance time in ongoing view, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := plainTranscript(m.View())
	if strings.Count(detail, "Supervisor suggested:") != 1 {
		t.Fatalf("expected detailed suggestion text only in the dedicated reviewer_suggestions entry, got %q", detail)
	}
	if !containsInOrder(detail, "❯", "run task", "§", "Supervisor suggested:", "1. First", "2. Second", "§", "Supervisor ran: 2 suggestions, applied.", "❮", "done") {
		t.Fatalf("expected verbose reviewer detail ordering, got %q", detail)
	}
}

func TestOngoingWebSearchUsesAtPrefixAndVerboseQuery(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: `web search: "latest golang release"`,
		ToolCall: &transcript.ToolCallMeta{
			ToolName:    "web_search",
			Command:     `web search: "latest golang release"`,
			CompactText: `web search: "latest golang release"`,
		},
	})

	ongoing := plainTranscript(m.View())
	if !strings.Contains(ongoing, `@ web search: "latest golang release"`) {
		t.Fatalf("expected web search ongoing block to use @ prefix and verbose query, got %q", ongoing)
	}
}

func TestOngoingDividersAreInsertedOnlyBetweenRoleGroups(t *testing.T) {
	m := NewModel(WithPreviewLines(30))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "pwd",
		ToolCall: &transcript.ToolCallMeta{
			IsShell:      true,
			Command:      "pwd",
			TimeoutLabel: "timeout: 5m",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "/tmp"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_call", Text: "ls"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_error", Text: "failed"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u3"})

	view := plainTranscript(m.View())
	if got := strings.Count(view, strings.Repeat("─", 24)); got != 3 {
		t.Fatalf("expected 3 dividers for 4 role groups, got %d in %q", got, view)
	}
	if !containsInOrder(view, "❯", "u1", "u2", "❮", "a1", "a2", "$", "pwd", "•", "ls", "❯", "u3") {
		t.Fatalf("expected grouped ongoing transcript order, got %q", view)
	}
}

func TestDetailToolFormattingShowsTimeoutAndInlineOutput(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "pwd",
		ToolCall: &transcript.ToolCallMeta{
			IsShell:      true,
			Command:      "pwd",
			TimeoutLabel: "timeout: 5m",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "alpha\nbeta"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) == 0 || !strings.Contains(lines[0], "pwd") || !strings.Contains(lines[0], "timeout: 5m") {
		t.Fatalf("expected first detail line to contain command and timeout: %q", view)
	}
	if strings.Contains(view, "output:") {
		t.Fatalf("expected no output prefix in detail view, got %q", view)
	}
	if len(lines) < 2 || strings.TrimSpace(lines[1]) != "alpha" {
		t.Fatalf("expected output to start immediately after command line, got %q", view)
	}
}

func TestDetailExecCommandEmptyOutputRendersNoOutput(t *testing.T) {
	def, ok := tools.DefinitionFor(toolspec.ToolExecCommand)
	if !ok {
		t.Fatal("expected exec_command definition")
	}
	raw, err := json.Marshal("Process exited with code 0\nNo output")
	if err != nil {
		t.Fatalf("marshal exec result: %v", err)
	}
	formatted := def.FormatToolResult(tools.Result{Name: toolspec.ToolExecCommand, Output: raw})

	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "true",
		ToolCall: &transcript.ToolCallMeta{
			ToolName: "exec_command",
			IsShell:  true,
			Command:  "true",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: formatted})
	m = updateModel(t, m, ToggleModeMsg{})

	view := plainTranscript(m.View())
	if !strings.Contains(view, "Process exited with code 0") {
		t.Fatalf("expected exit code line in detail view, got %q", view)
	}
	if !strings.Contains(view, "No output") {
		t.Fatalf("expected No output in detail view, got %q", view)
	}
	if strings.Contains(view, "Output:") {
		t.Fatalf("did not expect dangling Output header in detail view, got %q", view)
	}
}

func TestWriteStdinPollFormattingShowsDurationInOngoingAndDetail(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "Polled session 1149 for 2s",
		ToolCall: &transcript.ToolCallMeta{
			ToolName: "write_stdin",
			IsShell:  true,
			Command:  "Polled session 1149 for 2s",
		},
	})

	ongoing := plainTranscript(m.View())
	if !strings.Contains(ongoing, "Polled session 1149 for 2s") {
		t.Fatalf("expected poll duration visible in ongoing view, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := plainTranscript(m.View())
	if !strings.Contains(detail, "Polled session 1149 for 2s") {
		t.Fatalf("expected poll duration visible in detail view, got %q", detail)
	}
}

func TestRenderEntryTextDoesNotShellHighlightWriteStdinPollSummary(t *testing.T) {
	m := NewModel(WithTheme("dark"))
	out := m.renderEntryText("tool_shell_success", "Polled session 1149 for 2s", 80, &transcript.ToolCallMeta{
		ToolName: "write_stdin",
		IsShell:  true,
		Command:  "Polled session 1149 for 2s",
	}, false)
	expected := applyDefaultForeground("Polled session 1149 for 2s", m.palette().foregroundColor)
	if out != expected {
		t.Fatalf("expected write_stdin poll summary to stay plain app-foreground text, got %q want %q", out, expected)
	}
}

func TestWriteStdinPollFormattingShowsSubSecondDurationInOngoingAndDetail(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "Polled session 1149 for 250ms",
		ToolCall: &transcript.ToolCallMeta{
			ToolName: "write_stdin",
			IsShell:  true,
			Command:  "Polled session 1149 for 250ms",
		},
	})

	ongoing := plainTranscript(m.View())
	if !strings.Contains(ongoing, "Polled session 1149 for 250ms") {
		t.Fatalf("expected sub-second poll duration visible in ongoing view, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := plainTranscript(m.View())
	if !strings.Contains(detail, "Polled session 1149 for 250ms") {
		t.Fatalf("expected sub-second poll duration visible in detail view, got %q", detail)
	}
}

func TestToolBlockRoleFromResult(t *testing.T) {
	if got := toolBlockRoleFromResult("tool_result_ok", "tool"); got != "tool_success" {
		t.Fatalf("unexpected role for success result: %q", got)
	}
	if got := toolBlockRoleFromResult("tool_result_error", "tool"); got != "tool_error" {
		t.Fatalf("unexpected role for error result: %q", got)
	}
	if got := toolBlockRoleFromResult("tool_result", "tool"); got != "tool_success" {
		t.Fatalf("unexpected role for legacy result: %q", got)
	}
	if got := toolBlockRoleFromResult("tool_result_ok", "tool_shell"); got != "tool_shell_success" {
		t.Fatalf("unexpected role for shell success result: %q", got)
	}
	if got := toolBlockRoleFromResult("tool_result_error", "tool_shell"); got != "tool_shell_error" {
		t.Fatalf("unexpected role for shell error result: %q", got)
	}
}

func TestPatchPayloadRendersSummaryInOngoingAndDetailDiffInDetail(t *testing.T) {
	summary := "Edited:\n./path/to/file/1.go +13 -9\n./path/to/file/2.go +386"
	detail := "Edited:\n/abs/path/to/file/1.go\n+new line\n-old line\n/abs/path/to/file/2.go\n+another line"

	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: summary,
		ToolCall: &transcript.ToolCallMeta{
			ToolName:     "patch",
			PatchSummary: summary,
			PatchDetail:  detail,
			PatchRender: testPatchRender(
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindHeader, Text: "Edited:", FileIndex: -1},
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindFile, Text: "/abs/path/to/file/1.go", FileIndex: 0, Path: "/abs/path/to/file/1.go"},
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindDiff, Text: "+new line", FileIndex: 0},
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindDiff, Text: "-old line", FileIndex: 0},
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindFile, Text: "/abs/path/to/file/2.go", FileIndex: 1, Path: "/abs/path/to/file/2.go"},
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindDiff, Text: "+another line", FileIndex: 1},
			),
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: ""})

	ongoing := m.View()
	if !strings.Contains(ongoing, "Edited:") || !strings.Contains(ongoing, "./path/to/file/1.go") || !strings.Contains(ongoing, "./path/to/file/2.go") {
		t.Fatalf("expected patch summary in ongoing mode, got %q", ongoing)
	}
	if strings.Contains(ongoing, "/abs/path/to/file/1.go") || strings.Contains(ongoing, "+new line") {
		t.Fatalf("did not expect detail diff in ongoing mode, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detailView := m.View()
	if !strings.Contains(detailView, "/abs/path/to/file/1.go") || !strings.Contains(detailView, "/abs/path/to/file/2.go") {
		t.Fatalf("expected absolute file paths in detail mode, got %q", detailView)
	}
	if !strings.Contains(detailView, "+new line") || !strings.Contains(detailView, "-old line") || !strings.Contains(detailView, "+another line") {
		t.Fatalf("expected full diff lines in detail mode, got %q", detailView)
	}
	if strings.Contains(detailView, "output:") {
		t.Fatalf("did not expect output prefix in detail mode, got %q", detailView)
	}
}

func TestDetailShowsRawPatchFallbackWhenOnlySummaryAvailableInOngoing(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	rawPatch := "*** Begin Patch\n*** Update File: a.go\n-old\n+new\n*** End Patch"
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "Edited:",
		ToolCall: &transcript.ToolCallMeta{
			ToolName:     "patch",
			PatchSummary: "Edited:",
			PatchDetail:  "Edited:\n" + rawPatch,
			PatchRender: func() *patchformat.RenderedPatch {
				rendered := patchformat.Raw(rawPatch)
				return &rendered
			}(),
			RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindDiff},
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: ""})

	ongoing := plainTranscript(m.View())
	if !strings.Contains(ongoing, "Edited:") {
		t.Fatalf("expected patch summary in ongoing, got %q", ongoing)
	}
	if strings.Contains(ongoing, "*** Begin Patch") {
		t.Fatalf("did not expect raw patch body in ongoing, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := plainTranscript(m.View())
	if !strings.Contains(detail, "*** Begin Patch") {
		t.Fatalf("expected raw patch body in detail, got %q", detail)
	}
}

func TestSetConversationTypedToolMetadataRendersWithoutLegacyInlineParsing(t *testing.T) {
	summary := "Edited:\n./main.go +1 -1"
	detail := "Edited:\n/abs/main.go\n-old\n+new"

	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, SetConversationMsg{Entries: []TranscriptEntry{
		{
			Role: "tool_call",
			Text: summary,
			ToolCall: &transcript.ToolCallMeta{
				ToolName:     "patch",
				PatchSummary: summary,
				PatchDetail:  detail,
				PatchRender: testPatchRender(
					patchformat.RenderedLine{Kind: patchformat.RenderedLineKindHeader, Text: "Edited:", FileIndex: -1},
					patchformat.RenderedLine{Kind: patchformat.RenderedLineKindFile, Text: "/abs/main.go", FileIndex: 0, Path: "/abs/main.go"},
					patchformat.RenderedLine{Kind: patchformat.RenderedLineKindDiff, Text: "-old", FileIndex: 0},
					patchformat.RenderedLine{Kind: patchformat.RenderedLineKindDiff, Text: "+new", FileIndex: 0},
				),
			},
		},
		{
			Role: "tool_result_ok",
			Text: "",
		},
		{
			Role: "tool_call",
			Text: "pwd",
			ToolCall: &transcript.ToolCallMeta{
				ToolName:     "shell",
				IsShell:      true,
				Command:      "pwd",
				TimeoutLabel: "timeout: 5m",
			},
		},
		{
			Role: "tool_result_ok",
			Text: "/tmp",
		},
	}})

	ongoing := plainTranscript(m.View())
	if !strings.Contains(ongoing, "./main.go +1 -1") {
		t.Fatalf("expected patch summary from typed metadata in ongoing view, got %q", ongoing)
	}
	if strings.Contains(ongoing, "/abs/main.go") || strings.Contains(ongoing, "+new") {
		t.Fatalf("did not expect patch detail in ongoing view, got %q", ongoing)
	}
	if !strings.Contains(ongoing, "pwd") {
		t.Fatalf("expected shell command from typed metadata in ongoing view, got %q", ongoing)
	}
	if strings.Contains(ongoing, "/tmp") {
		t.Fatalf("did not expect shell output in ongoing view, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detailView := plainTranscript(m.View())
	if !strings.Contains(detailView, "/abs/main.go") || !strings.Contains(detailView, "+new") || !strings.Contains(detailView, "-old") {
		t.Fatalf("expected patch detail from typed metadata in detail view, got %q", detailView)
	}
	if !containsInOrder(detailView, "$", "pwd", "timeout: 5m", "/tmp") {
		t.Fatalf("expected shell command, timeout and output from typed metadata in detail view, got %q", detailView)
	}
}

func TestTriggerHandoffTypedToolMetadataUsesCompactOngoingAndDetailedView(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, SetConversationMsg{Entries: []TranscriptEntry{
		{
			Role:       "tool_call",
			Text:       "trigger_handoff()",
			ToolCallID: "call_handoff_1",
			ToolCall: &transcript.ToolCallMeta{
				ToolName:    "trigger_handoff",
				CompactText: "Model requested compaction.",
				Command:     "Model requested compaction.\nInstructions:\nkeep API details\nFuture message:\nresume with tests",
			},
		},
		{
			Role:       "tool_result_ok",
			Text:       "Handoff scheduled. Context will be compacted before the next model turn and future-agent guidance was saved.",
			ToolCallID: "call_handoff_1",
		},
	}})

	ongoing := plainTranscript(m.View())
	if !strings.Contains(ongoing, "Model requested compaction.") {
		t.Fatalf("expected ongoing view to include concise compaction request, got %q", ongoing)
	}
	if strings.Contains(ongoing, "keep API details") || strings.Contains(ongoing, "resume with tests") {
		t.Fatalf("did not expect ongoing view to include detailed trigger_handoff parameters, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detailView := plainTranscript(m.View())
	if !strings.Contains(detailView, "Model requested compaction.") {
		t.Fatalf("expected detail view to include compaction request heading, got %q", detailView)
	}
	if !strings.Contains(detailView, "Instructions:") || !strings.Contains(detailView, "keep API details") {
		t.Fatalf("expected detail view to include trigger_handoff instructions, got %q", detailView)
	}
	if !strings.Contains(detailView, "Future message:") || !strings.Contains(detailView, "resume with tests") {
		t.Fatalf("expected detail view to include trigger_handoff future message, got %q", detailView)
	}
}

func TestTriggerHandoffRuntimeProjectedMetadataUsesCompactOngoingAndDetailedView(t *testing.T) {
	callEntries := runtime.TranscriptEntriesFromEvent(runtime.Event{
		Kind: runtime.EventToolCallStarted,
		ToolCall: &llm.ToolCall{
			ID:    "call_handoff_runtime_1",
			Name:  string(toolspec.ToolTriggerHandoff),
			Input: json.RawMessage(`{"summarizer_prompt":"keep API details","future_agent_message":"resume with tests"}`),
		},
	})
	if len(callEntries) != 1 {
		t.Fatalf("expected one runtime-projected tool call entry, got %+v", callEntries)
	}
	resultBody, err := json.Marshal(triggerhandofftool.ResultPayload{
		Summary:                 "Handoff scheduled. Context will be compacted before the next model turn and future-agent guidance was saved.",
		FutureAgentMessageAdded: true,
	})
	if err != nil {
		t.Fatalf("marshal trigger_handoff tool result: %v", err)
	}
	resultEntries := runtime.TranscriptEntriesFromEvent(runtime.Event{
		Kind: runtime.EventToolCallCompleted,
		ToolResult: &tools.Result{
			CallID: "call_handoff_runtime_1",
			Name:   toolspec.ToolTriggerHandoff,
			Output: resultBody,
		},
	})
	if len(resultEntries) != 1 {
		t.Fatalf("expected one runtime-projected tool result entry, got %+v", resultEntries)
	}

	m := NewModel(WithPreviewLines(20))
	for _, entry := range append(callEntries, resultEntries...) {
		m = updateModel(t, m, AppendTranscriptMsg{
			Role:       entry.Role,
			Text:       entry.Text,
			ToolCallID: entry.ToolCallID,
			ToolCall:   entry.ToolCall,
		})
	}

	ongoing := plainTranscript(m.View())
	if !strings.Contains(ongoing, "Model requested compaction.") {
		t.Fatalf("expected ongoing view to include concise runtime-projected compaction request, got %q", ongoing)
	}
	if strings.Contains(ongoing, "keep API details") || strings.Contains(ongoing, "resume with tests") {
		t.Fatalf("did not expect ongoing view to include detailed runtime-projected trigger_handoff parameters, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detailView := plainTranscript(m.View())
	if !strings.Contains(detailView, "Model requested compaction.") {
		t.Fatalf("expected detail view to include runtime-projected compaction request heading, got %q", detailView)
	}
	if !strings.Contains(detailView, "Instructions:") || !strings.Contains(detailView, "keep API details") {
		t.Fatalf("expected detail view to include runtime-projected trigger_handoff instructions, got %q", detailView)
	}
	if !strings.Contains(detailView, "Future message:") || !strings.Contains(detailView, "resume with tests") {
		t.Fatalf("expected detail view to include runtime-projected trigger_handoff future message, got %q", detailView)
	}
}

func TestStyleToolLineColorsPatchCountsAndDiff(t *testing.T) {
	m := NewModel()
	counts := m.styleToolLine("./file.go +13 -9")
	if !strings.Contains(counts, "+13") || !strings.Contains(counts, "-9") {
		t.Fatalf("expected patch counts preserved, got %q", counts)
	}
	added := m.styleToolLine("+added")
	if !strings.Contains(added, "+added") {
		t.Fatalf("expected addition line preserved, got %q", added)
	}
	removed := m.styleToolLine("-removed")
	if !strings.Contains(removed, "-removed") {
		t.Fatalf("expected removal line preserved, got %q", removed)
	}
}

func TestStyleToolLineStylesOnlyDiffMarkerWhenSyntaxPresent(t *testing.T) {
	m := NewModel()
	inputAdded := "+\x1b[38;5;81mpackage\x1b[0m main"
	inputRemoved := "-\x1b[38;5;81mfunc\x1b[0m main() {}"

	added := m.styleToolLine(inputAdded)
	removed := m.styleToolLine(inputRemoved)

	if !strings.Contains(added, m.palette().toolSuccess.Render("+")) {
		t.Fatalf("expected added diff marker to use tool success style, got %q", added)
	}
	if !strings.Contains(removed, m.palette().toolError.Render("-")) {
		t.Fatalf("expected removed diff marker to use tool error style, got %q", removed)
	}
	if !strings.Contains(added, "\x1b[38;5;81mpackage\x1b[0m") {
		t.Fatalf("expected syntax highlighting to remain intact for added line, got %q", added)
	}
	if !strings.Contains(removed, "\x1b[38;5;81mfunc\x1b[0m") {
		t.Fatalf("expected syntax highlighting to remain intact for removed line, got %q", removed)
	}
}

func TestDetailDiffBackgroundTintsFullRenderedLine(t *testing.T) {
	detail := "Edited:\n./main.go\n+package main\n-old"
	const viewportWidth = 40

	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: viewportWidth})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: detail,
		ToolCall: &transcript.ToolCallMeta{
			ToolName:    "patch",
			PatchDetail: detail,
			PatchRender: testPatchRender(
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindHeader, Text: "Edited:", FileIndex: -1},
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindFile, Text: "./main.go", FileIndex: 0, Path: "main.go"},
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindDiff, Text: "+package main", FileIndex: 0},
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindDiff, Text: "-old", FileIndex: 0},
			),
			RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindDiff},
		},
	})
	m = updateModel(t, m, ToggleModeMsg{})

	view := m.View()
	addBg, removeBg := m.diffLineBackgroundEscapes()
	var addLine string
	var removeLine string
	for _, line := range strings.Split(view, "\n") {
		plain := ansi.Strip(line)
		if addLine == "" && strings.Contains(plain, "+package main") {
			addLine = line
		}
		if removeLine == "" && strings.Contains(plain, "-old") {
			removeLine = line
		}
	}
	if addLine == "" || removeLine == "" {
		t.Fatalf("expected add/remove lines in detail output, got %q", view)
	}
	if got := runewidth.StringWidth(ansi.Strip(addLine)); got != viewportWidth {
		t.Fatalf("expected added line tint to span viewport width %d, got %d", viewportWidth, got)
	}
	if got := runewidth.StringWidth(ansi.Strip(removeLine)); got != viewportWidth {
		t.Fatalf("expected removed line tint to span viewport width %d, got %d", viewportWidth, got)
	}
	for _, line := range strings.Split(view, "\n") {
		if got := runewidth.StringWidth(ansi.Strip(line)); got > viewportWidth {
			t.Fatalf("expected rendered detail line width <= viewport width %d, got %d for %q", viewportWidth, got, line)
		}
	}
	if !strings.Contains(view, addBg+"  ") {
		t.Fatalf("expected added line background to include indentation prefix, got %q", view)
	}
	if !strings.Contains(view, removeBg+"  ") {
		t.Fatalf("expected removed line background to include indentation prefix, got %q", view)
	}
}

func TestTintToolDiffLineKeepsBackgroundOnPaddedSpacesAfterAnsiReset(t *testing.T) {
	const viewportWidth = 32
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 8, Width: viewportWidth})
	addBg, _ := m.diffLineBackgroundEscapes()
	input := "  +\x1b[38;5;81mpackage\x1b[0m main"
	if got := runewidth.StringWidth(ansi.Strip(input)); got >= viewportWidth {
		t.Fatalf("expected test input width to be smaller than viewport to exercise padding, got %d", got)
	}

	tinted := m.tintToolDiffLine(input, "add")
	if got := runewidth.StringWidth(ansi.Strip(tinted)); got != viewportWidth {
		t.Fatalf("expected tinted line width %d, got %d", viewportWidth, got)
	}
	if !strings.Contains(tinted, "\x1b[38;5;81mpackage\x1b[0m") {
		t.Fatalf("expected syntax token color to remain intact, got %q", tinted)
	}
	if !strings.Contains(tinted, "\x1b[0m"+addBg+" ") {
		t.Fatalf("expected background tint to be re-applied after ansi reset before trailing padding, got %q", tinted)
	}
	if !strings.HasSuffix(tinted, "\x1b[0m") {
		t.Fatalf("expected tinted line to end with reset, got %q", tinted)
	}
}

func TestDetailDiffRendersGoTokenAnsi(t *testing.T) {
	detail := "Edited:\n./main.go\n+package main\n+func main() {}"

	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: detail,
		ToolCall: &transcript.ToolCallMeta{
			ToolName:    "patch",
			PatchDetail: detail,
			PatchRender: testPatchRender(
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindHeader, Text: "Edited:", FileIndex: -1},
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindFile, Text: "./main.go", FileIndex: 0, Path: "main.go"},
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindDiff, Text: "+package main", FileIndex: 0},
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindDiff, Text: "+func main() {}", FileIndex: 0},
			),
			RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindDiff},
		},
	})
	m = updateModel(t, m, ToggleModeMsg{})

	view := m.View()
	if !regexp.MustCompile(`\x1b\[[0-9;]*mpackage`).MatchString(view) {
		t.Fatalf("expected detail view to contain ansi-colored go token for package, got %q", view)
	}
}

func TestDetailDiffLayeringKeepsBackgroundAndTokenColorForAddAndRemove(t *testing.T) {
	detail := "Edited:\n./main.go\n+package main\n-func removed() {}"

	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: detail,
		ToolCall: &transcript.ToolCallMeta{
			ToolName:    "patch",
			PatchDetail: detail,
			PatchRender: testPatchRender(
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindHeader, Text: "Edited:", FileIndex: -1},
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindFile, Text: "./main.go", FileIndex: 0, Path: "main.go"},
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindDiff, Text: "+package main", FileIndex: 0},
				patchformat.RenderedLine{Kind: patchformat.RenderedLineKindDiff, Text: "-func removed() {}", FileIndex: 0},
			),
			RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindDiff},
		},
	})
	m = updateModel(t, m, ToggleModeMsg{})

	view := m.View()
	addBg, removeBg := m.diffLineBackgroundEscapes()
	var addLine string
	var removeLine string
	for _, line := range strings.Split(view, "\n") {
		plain := ansi.Strip(line)
		if addLine == "" && strings.Contains(plain, "+package main") {
			addLine = line
		}
		if removeLine == "" && strings.Contains(plain, "-func removed() {}") {
			removeLine = line
		}
	}
	if addLine == "" || removeLine == "" {
		t.Fatalf("expected add/remove lines in detail output, got %q", view)
	}
	if !strings.Contains(addLine, addBg) || !strings.Contains(addLine, "\x1b[38;") {
		t.Fatalf("expected added line to include both background tint and token color, got %q", addLine)
	}
	if !strings.Contains(removeLine, removeBg) || !strings.Contains(removeLine, "\x1b[38;") {
		t.Fatalf("expected removed line to include both background tint and token color, got %q", removeLine)
	}
}

func TestIsEditedToolBlockDetectsAnsiHeader(t *testing.T) {
	if !isEditedToolBlock([]string{"", "\x1b[38;5;81mEdited:\x1b[0m", "./file.go +1"}) {
		t.Fatal("expected Edited header with ansi to be detected")
	}
	if isEditedToolBlock([]string{"", "regular output"}) {
		t.Fatal("did not expect non-Edited content to be detected as Edited block")
	}
}

func TestFormatOngoingErrorIsNotTruncated(t *testing.T) {
	input := strings.Repeat("e", 300)
	formatted := FormatOngoingError(errString(input))
	if formatted != "error: "+input {
		t.Fatalf("unexpected formatted error: %q", formatted)
	}
}

func TestDetailRendersMarkdownForUserAndAssistant(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "**bold** and `code`"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "- one\n- two"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := m.View()
	if !strings.Contains(view, "❯") || !strings.Contains(view, "❮") {
		t.Fatalf("expected user/assistant prefixes in view: %q", view)
	}
	if strings.Contains(view, "**bold**") || strings.Contains(view, "`code`") {
		t.Fatalf("expected markdown formatting to be rendered, got raw markdown: %q", view)
	}
	if !strings.Contains(view, "bold") || !strings.Contains(view, "code") {
		t.Fatalf("expected rendered markdown text to remain visible: %q", view)
	}
}

func TestDetailFormattedAssistantTextUsesAppForegroundDark(t *testing.T) {
	testDetailFormattedAssistantTextUsesAppForeground(t, "dark")
}

func TestDetailFormattedAssistantTextUsesAppForegroundLight(t *testing.T) {
	testDetailFormattedAssistantTextUsesAppForeground(t, "light")
}

func testDetailFormattedAssistantTextUsesAppForeground(t *testing.T, theme string) {
	t.Helper()
	m := NewModel(WithTheme(theme))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "plain transcript text"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "formatted **assistant** text"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := m.View()
	appForeground := foregroundEscape(m.palette().foregroundColor)
	plainLine := lineContaining(view, "plain transcript text")
	formattedLine := lineContaining(view, "formatted assistant text")
	if plainLine == "" || formattedLine == "" {
		t.Fatalf("expected detail view to contain both plain and formatted assistant lines, got %q", plainTranscript(view))
	}
	if !strings.Contains(plainLine, appForeground) {
		t.Fatalf("expected plain assistant detail line to use app foreground for %s theme, got %q", theme, plainLine)
	}
	if !strings.Contains(formattedLine, appForeground) {
		t.Fatalf("expected formatted assistant detail line to use app foreground for %s theme, got %q", theme, formattedLine)
	}
	if containsBackgroundSGR(formattedLine) {
		t.Fatalf("expected formatted assistant detail line to avoid background color escapes for %s theme, got %q", theme, formattedLine)
	}
	for _, unwanted := range oldFormatterBaseForegroundEscapes(theme) {
		if strings.Contains(formattedLine, unwanted) {
			t.Fatalf("expected formatted assistant detail line to avoid old formatter base foreground %q for %s theme, got %q", unwanted, theme, formattedLine)
		}
	}
}

func TestOngoingStreamingStaysPlainUntilCommit(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 6, Width: 60})
	m = updateModel(t, m, StreamAssistantMsg{Delta: "**bold**"})

	streaming := m.View()
	if !strings.Contains(streaming, "**bold**") {
		t.Fatalf("expected plain markdown while streaming, got %q", streaming)
	}

	m = updateModel(t, m, CommitAssistantMsg{})
	committed := m.View()
	if strings.Contains(committed, "**bold**") {
		t.Fatalf("expected markdown rendering after commit, got %q", committed)
	}
	if !strings.Contains(committed, "bold") {
		t.Fatalf("expected committed rendered text to remain visible, got %q", committed)
	}
}

func TestViewportWidthChangeAffectsMarkdownRender(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 10, Width: 24})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "This is **markdown** content that should wrap at different widths."})
	m = updateModel(t, m, ToggleModeMsg{})
	narrow := m.View()

	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 10, Width: 80})
	m = updateModel(t, m, ToggleModeMsg{})
	wide := m.View()

	if narrow == wide {
		t.Fatalf("expected markdown rendering to change with width; narrow and wide views are identical: %q", narrow)
	}
}

func TestNonMarkdownRolesStayPlain(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 8, Width: 60})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result", Text: "**raw**"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := m.View()
	if !strings.Contains(view, "**raw**") {
		t.Fatalf("expected tool text to remain plain, got %q", view)
	}
}

func TestRenderEntryTextUsesAppForegroundForPlainAssistantTextDark(t *testing.T) {
	testRenderEntryTextUsesAppForegroundForPlainAssistantText(t, "dark")
}

func TestRenderEntryTextUsesAppForegroundForPlainAssistantTextLight(t *testing.T) {
	testRenderEntryTextUsesAppForegroundForPlainAssistantText(t, "light")
}

func testRenderEntryTextUsesAppForegroundForPlainAssistantText(t *testing.T, theme string) {
	t.Helper()
	m := NewModel(WithTheme(theme))
	out := m.renderEntryText("assistant", "plain response text", 80, nil, false)
	if !strings.HasPrefix(out, foregroundEscape(m.palette().foregroundColor)) {
		t.Fatalf("expected plain assistant text to start with app foreground for %s theme, got %q", theme, out)
	}
	if got := ansi.Strip(out); got != "plain response text" {
		t.Fatalf("expected plain assistant text preserved, got %q", got)
	}
}

func TestRenderEntryTextUsesAppForegroundForMarkdownAssistantTextDark(t *testing.T) {
	testRenderEntryTextUsesAppForegroundForMarkdownAssistantText(t, "dark")
}

func TestRenderEntryTextUsesAppForegroundForMarkdownAssistantTextLight(t *testing.T) {
	testRenderEntryTextUsesAppForegroundForMarkdownAssistantText(t, "light")
}

func testRenderEntryTextUsesAppForegroundForMarkdownAssistantText(t *testing.T, theme string) {
	t.Helper()
	m := NewModel(WithTheme(theme))
	out := m.renderEntryText("assistant", "plain and **bold**", 80, nil, false)
	if !strings.HasPrefix(out, foregroundEscape(m.palette().foregroundColor)) {
		t.Fatalf("expected markdown assistant text to start with app foreground for %s theme, got %q", theme, out)
	}
	if got := ansi.Strip(out); !strings.Contains(got, "plain and bold") {
		t.Fatalf("expected markdown assistant text preserved, got %q", got)
	}
}

func TestRenderEntryTextUsesAppForegroundForHighlightedToolTextDark(t *testing.T) {
	testRenderEntryTextUsesAppForegroundForHighlightedToolText(t, "dark")
}

func TestRenderEntryTextUsesAppForegroundForHighlightedToolTextLight(t *testing.T) {
	testRenderEntryTextUsesAppForegroundForHighlightedToolText(t, "light")
}

func testRenderEntryTextUsesAppForegroundForHighlightedToolText(t *testing.T, theme string) {
	t.Helper()
	m := NewModel(WithTheme(theme))
	meta := &transcript.ToolCallMeta{RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindSource, Path: "main.go", ResultOnly: true}}
	out := m.renderEntryText("tool_success", "package main\nfunc main() {}", 80, meta, false)
	if !strings.HasPrefix(out, foregroundEscape(m.palette().foregroundColor)) {
		t.Fatalf("expected highlighted tool text to start with app foreground for %s theme, got %q", theme, out)
	}
	if got := ansi.Strip(out); !strings.Contains(got, "package main") {
		t.Fatalf("expected highlighted tool text preserved, got %q", got)
	}
	if containsBackgroundSGR(out) {
		t.Fatalf("expected highlighted tool text to avoid background color escapes for %s theme, got %q", theme, out)
	}
}

func TestRenderEntryTextUsesSemanticForegroundForReviewerStatusByTheme(t *testing.T) {
	for _, theme := range []string{"dark", "light"} {
		t.Run(theme, func(t *testing.T) {
			m := NewModel(WithTheme(theme))
			out := m.renderEntryText("reviewer_status", "Supervisor ran: ok", 80, nil, false)
			if !strings.HasPrefix(out, foregroundEscape(themeSuccessColor(theme))) {
				t.Fatalf("expected reviewer status text to start with success foreground for %s theme, got %q", theme, out)
			}
			if got := ansi.Strip(out); got != "Supervisor ran: ok" {
				t.Fatalf("expected reviewer status text preserved, got %q", got)
			}
		})
	}
}

func TestRenderEntryTextUsesSemanticForegroundForWarningByTheme(t *testing.T) {
	for _, theme := range []string{"dark", "light"} {
		t.Run(theme, func(t *testing.T) {
			m := NewModel(WithTheme(theme))
			out := m.renderEntryText("warning", "Heads-up", 80, nil, false)
			if !strings.HasPrefix(out, foregroundEscape(themeWarningColor(theme))) {
				t.Fatalf("expected warning text to start with warning foreground for %s theme, got %q", theme, out)
			}
			if got := ansi.Strip(out); got != "Heads-up" {
				t.Fatalf("expected warning text preserved, got %q", got)
			}
		})
	}
}

func TestRenderEntryTextUsesSemanticForegroundForCacheWarningByTheme(t *testing.T) {
	for _, theme := range []string{"dark", "light"} {
		t.Run(theme, func(t *testing.T) {
			m := NewModel(WithTheme(theme))
			out := m.renderEntryText("cache_warning", "Cache miss", 80, nil, false)
			if !strings.HasPrefix(out, foregroundEscape(themeWarningColor(theme))) {
				t.Fatalf("expected cache warning text to start with warning foreground for %s theme, got %q", theme, out)
			}
			if got := ansi.Strip(out); got != "Cache miss" {
				t.Fatalf("expected cache warning text preserved, got %q", got)
			}
		})
	}
}

func TestReviewerAndWarningViewUseSemanticForegroundInLightTheme(t *testing.T) {
	m := NewModel(WithTheme("light"), WithPreviewLines(20))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reviewer_status", Text: "Supervisor ran: ok"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "warning", Text: "Heads-up"})

	rawOngoing := m.View()
	reviewerLine := lineContaining(rawOngoing, "Supervisor ran: ok")
	if reviewerLine == "" {
		t.Fatalf("expected ongoing view to contain reviewer status, got %q", plainTranscript(rawOngoing))
	}
	if !strings.Contains(reviewerLine, foregroundEscape(themeSuccessColor("light"))) {
		t.Fatalf("expected ongoing reviewer status to use success foreground, got %q", reviewerLine)
	}
	if strings.Contains(plainTranscript(rawOngoing), "Heads-up") {
		t.Fatalf("expected warning hidden in ongoing view, got %q", plainTranscript(rawOngoing))
	}

	m = updateModel(t, m, ToggleModeMsg{})
	rawDetail := m.View()
	reviewerLine = lineContaining(rawDetail, "Supervisor ran: ok")
	warningLine := lineContaining(rawDetail, "Heads-up")
	if reviewerLine == "" || warningLine == "" {
		t.Fatalf("expected detail view to contain reviewer and warning lines, got %q", plainTranscript(rawDetail))
	}
	if !strings.Contains(reviewerLine, foregroundEscape(themeSuccessColor("light"))) {
		t.Fatalf("expected detail reviewer status to use success foreground, got %q", reviewerLine)
	}
	if !strings.Contains(warningLine, foregroundEscape(themeWarningColor("light"))) {
		t.Fatalf("expected detail warning to use warning foreground, got %q", warningLine)
	}
}

func TestCacheWarningViewUsesGenericWarningPresentationInLightTheme(t *testing.T) {
	m := NewModel(WithTheme("light"), WithPreviewLines(20))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "cache_warning", Text: "Cache miss"})

	rawOngoing := m.View()
	if strings.Contains(plainTranscript(rawOngoing), "Cache miss") {
		t.Fatalf("expected cache warning hidden in ongoing view, got %q", plainTranscript(rawOngoing))
	}

	m = updateModel(t, m, ToggleModeMsg{})
	rawDetail := m.View()
	cacheWarningLine := lineContaining(rawDetail, "Cache miss")
	if cacheWarningLine == "" {
		t.Fatalf("expected detail view to contain cache warning, got %q", plainTranscript(rawDetail))
	}
	if !strings.Contains(cacheWarningLine, foregroundEscape(themeWarningColor("light"))) {
		t.Fatalf("expected detail cache warning to use warning foreground, got %q", cacheWarningLine)
	}
	if got := ansi.Strip(cacheWarningLine); !strings.HasPrefix(got, "⚠ Cache miss") {
		t.Fatalf("expected detail cache warning to use warning symbol, got %q", got)
	}
}

func TestCacheWarningVisibilityOverrideShowsInOngoingMode(t *testing.T) {
	m := NewModel(WithTheme("light"), WithPreviewLines(20))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "cache_warning",
		Text:       "Cache reuse disappeared for unknown reasons",
		Visibility: transcript.EntryVisibilityAll,
	})

	rawOngoing := m.View()
	cacheWarningLine := lineContaining(rawOngoing, "Cache reuse disappeared for unknown reasons")
	if cacheWarningLine == "" {
		t.Fatalf("expected cache warning visible in ongoing view, got %q", plainTranscript(rawOngoing))
	}
	if !strings.Contains(cacheWarningLine, foregroundEscape(themeWarningColor("light"))) {
		t.Fatalf("expected ongoing cache warning to use warning foreground, got %q", cacheWarningLine)
	}
	if got := ansi.Strip(cacheWarningLine); !strings.HasPrefix(got, "⚠ Cache reuse disappeared for unknown reasons") {
		t.Fatalf("expected ongoing cache warning to use warning symbol, got %q", got)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	if lineContaining(m.View(), "Cache reuse disappeared for unknown reasons") == "" {
		t.Fatalf("expected detail view to retain cache warning, got %q", plainTranscript(m.View()))
	}
}

func TestSelectedUserLineUsesCentralThemeSelectionTokens(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	previousBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(false)
	defer lipgloss.SetColorProfile(previousProfile)
	defer lipgloss.SetHasDarkBackground(previousBackground)

	m := NewModel(WithTheme("light"), WithPreviewLines(20))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "selected user prompt"})
	m.selectedTranscriptEntry = 0
	m.selectedTranscriptActive = true
	m = updateModel(t, m, ToggleModeMsg{})

	raw := m.View()
	selectedLine := lineContaining(raw, "selected user prompt")
	if selectedLine == "" {
		t.Fatalf("expected detail view to contain selected user line, got %q", plainTranscript(raw))
	}
	if got := lipgloss.Width(selectedLine); got != 80 {
		t.Fatalf("expected selected line to span viewport width 80, got %d in %q", got, selectedLine)
	}
	tokens := theme.ResolvePalette("light").Transcript
	selectionBackground := rgbColorFromHex(tokens.SelectionBackground.TrueColor)
	if !strings.Contains(selectedLine, fmt.Sprintf("48;2;%d;%d;%d", selectionBackground.r, selectionBackground.g, selectionBackground.b)) {
		t.Fatalf("expected selected line to use selection background %s, got %q", tokens.SelectionBackground.TrueColor, selectedLine)
	}
	selectionForeground := rgbColorFromHex(tokens.SelectionForeground.TrueColor)
	if !strings.Contains(selectedLine, fmt.Sprintf("38;2;%d;%d;%d", selectionForeground.r, selectionForeground.g, selectionForeground.b)) {
		t.Fatalf("expected selected line to use selection foreground %s, got %q", tokens.SelectionForeground.TrueColor, selectedLine)
	}
}

func TestDetailWrapsNonMarkdownRoles(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 10, Width: 28})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "compaction_summary", Text: "This compaction summary line is intentionally long and should wrap in detail mode."})
	m = updateModel(t, m, ToggleModeMsg{})

	view := plainTranscript(m.View())
	for _, line := range strings.Split(view, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "─") {
			continue
		}
		if got := runewidth.StringWidth(line); got > 28 {
			t.Fatalf("expected wrapped line width <= 28, got %d for line %q", got, line)
		}
	}
}

func TestDetailReflowsNonMarkdownRolesOnViewportResize(t *testing.T) {
	text := "Compaction notice should reflow when viewport width changes."
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 10, Width: 24})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "compaction_notice", Text: text})
	m = updateModel(t, m, ToggleModeMsg{})
	narrow := plainTranscript(m.View())

	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 10, Width: 80})
	m = updateModel(t, m, ToggleModeMsg{})
	wide := plainTranscript(m.View())

	if strings.Contains(narrow, text) {
		t.Fatalf("expected narrow detail view to wrap non-markdown line, got %q", narrow)
	}
	if !strings.Contains(wide, text) {
		t.Fatalf("expected wide detail view to reflow and contain single-line text, got %q", wide)
	}
}

func TestRenderEntryTextHighlightsOnlyResultForShellSourceHint(t *testing.T) {
	m := NewModel()
	meta := &transcript.ToolCallMeta{
		RenderHint: &transcript.ToolRenderHint{
			Kind:       transcript.ToolRenderKindSource,
			Path:       "main.go",
			ResultOnly: true,
		},
	}

	out := m.renderEntryText("tool_shell_success", "cat main.go\npackage main\nfunc main() {}", 120, meta, false)
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected command and highlighted output lines, got %q", out)
	}
	if ansi.Strip(lines[0]) != "cat main.go" {
		t.Fatalf("expected command line to stay plain, got %q", lines[0])
	}
	if colors := extractForegroundTrueColors(lines[0]); len(colors) > 1 {
		t.Fatalf("expected command line to avoid syntax highlighting, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "\x1b[") {
		t.Fatalf("expected highlighted result line, got %q", lines[1])
	}
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "package main") || !strings.Contains(plain, "func main() {}") {
		t.Fatalf("expected result text preserved after highlighting, got %q", plain)
	}
}

func TestRenderEntryTextSkipsHighlightWhenMuted(t *testing.T) {
	m := NewModel()
	meta := &transcript.ToolCallMeta{
		RenderHint: &transcript.ToolRenderHint{
			Kind:       transcript.ToolRenderKindSource,
			Path:       "main.go",
			ResultOnly: true,
		},
	}

	out := m.renderEntryText("tool_shell_success", "cat main.go\npackage main\nfunc main() {}", 120, meta, true)
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("expected muted tool text to skip syntax highlighting, got %q", out)
	}
}

func TestFlattenEntryWithMetaKeepsMutedShellHighlightWhenMuted(t *testing.T) {
	testFlattenEntryWithMetaKeepsMutedShellHighlightWhenMuted(t, "dark")
}

func TestFlattenEntryWithMetaKeepsMutedShellHighlightWhenMutedLight(t *testing.T) {
	testFlattenEntryWithMetaKeepsMutedShellHighlightWhenMuted(t, "light")
}

func testFlattenEntryWithMetaKeepsMutedShellHighlightWhenMuted(t *testing.T, theme string) {
	t.Helper()
	m := NewModel(WithTheme(theme))
	meta := &transcript.ToolCallMeta{
		RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindShell},
	}
	command := "./gradlew -p apps/respawn detektFormat > docs/tmp/build-triage-2026-03-15/detektFormat.log 2>&1"

	detail := m.renderEntryText("tool_shell_success", command, 120, meta, false)
	ongoingLines := m.flattenEntryWithMeta("tool_shell_success", command, true, meta)
	ongoing := strings.Join(ongoingLines, "\n")

	if !strings.Contains(detail, "\x1b[") {
		t.Fatalf("expected detail shell command to remain highlighted, got %q", detail)
	}
	expectedPrefix := m.roleSymbol("tool_shell_success") + " "
	if len(ongoingLines) == 0 || !strings.HasPrefix(ongoingLines[0], expectedPrefix) {
		t.Fatalf("expected ongoing shell preview to preserve the full-color shell prefix, got %q want prefix %q", ongoing, expectedPrefix)
	}
	if !strings.Contains(ongoing, "\x1b[") {
		t.Fatalf("expected ongoing shell command to keep muted highlighting, got %q", ongoing)
	}
	if !strings.Contains(ongoing, ";2m") {
		t.Fatalf("expected ongoing shell command to enforce faint styling, got %q", ongoing)
	}
	if !strings.Contains(ansi.Strip(ongoing), command) {
		t.Fatalf("expected ongoing shell command text preserved after muting, got %q", ansi.Strip(ongoing))
	}
	ongoingColors := extractForegroundTrueColors(ongoing)
	if !containsColor(ongoingColors, m.palette().previewColor) {
		t.Fatalf("expected ongoing shell command to restore preview foreground for uncolored spans, got %q", ongoing)
	}
	if !containsNonPreviewColor(ongoingColors, m.palette().previewColor) {
		t.Fatalf("expected ongoing shell command to preserve some syntax foreground colors under faint styling, got %q", ongoing)
	}
}

func TestMuteANSIOutputReappliesDefaultForegroundAfterReset(t *testing.T) {
	m := NewModel(WithTheme("dark"))
	base := m.palette().previewColor
	muted := muteANSIOutput("echo \x1b[38;5;81mfoo\x1b[0m bar", base)
	if !strings.Contains(muted, "\x1b[38;2;") {
		t.Fatalf("expected muted output to contain truecolor foreground escapes, got %q", muted)
	}
	if !strings.Contains(muted, "\x1b[0;"+strings.Join(foregroundParams(base), ";")+";2m bar") {
		t.Fatalf("expected reset to restore preview foreground and faint, got %q", muted)
	}
	if got := ansi.Strip(muted); got != "echo foo bar" {
		t.Fatalf("expected text preserved after muting, got %q", got)
	}
}

func TestMuteANSIOutputSupportsColonTrueColorSGRInRenderingPipeline(t *testing.T) {
	m := NewModel(WithTheme("light"))
	base := m.palette().previewColor
	muted := muteANSIOutput("\x1b[38:2:255:0:255mhello\x1b[39m world", base)
	if !strings.Contains(muted, "\x1b[38;2;") {
		t.Fatalf("expected colon-form truecolor sequence to be rewritten, got %q", muted)
	}
	if !strings.Contains(muted, "\x1b[38;2;255;0;255;2m") {
		if strings.Contains(muted, "\x1b[38:2:255:0:255m") {
			t.Fatalf("expected colon-form sequence to be normalized during rewrite, got %q", muted)
		}
	}
	if !strings.Contains(muted, "\x1b["+strings.Join(styleParams(ansiStyleTransform{DefaultForeground: &base, ForceFaint: true}, false), ";")+"m world") {
		t.Fatalf("expected default-foreground reset to restore preview+faint style, got %q", muted)
	}
	if got := ansi.Strip(muted); got != "hello world" {
		t.Fatalf("expected colon-form truecolor text preserved, got %q", got)
	}
}

func TestOngoingWrappedShellPreviewCollapsesAfterFirstVisualLine(t *testing.T) {
	m := NewModel(WithTheme("dark"))
	m.viewportWidth = 28
	meta := &transcript.ToolCallMeta{
		RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindShell},
	}
	command := "./gradlew -p apps/respawn detektFormat > docs/tmp/build-triage-2026-03-15/detektFormat.log 2>&1"

	lines := m.flattenEntryWithMeta("tool_shell_success", command, true, meta)
	if len(lines) != 1 {
		t.Fatalf("expected wrapped ongoing shell preview to collapse to one truncated line, got %d (%q)", len(lines), lines)
	}
	if expectedPrefix := m.roleSymbol("tool_shell_success") + " "; !strings.HasPrefix(lines[0], expectedPrefix) {
		t.Fatalf("expected wrapped shell preview to preserve the full-color shell prefix, got %q want prefix %q", lines[0], expectedPrefix)
	}
	joined := strings.Join(lines, "\n")
	if !strings.HasSuffix(strings.TrimSpace(ansi.Strip(lines[0])), "…") {
		t.Fatalf("expected wrapped shell preview first line to end with ellipsis, got %q", lines[0])
	}
	if !strings.Contains(joined, ";2m") {
		t.Fatalf("expected wrapped shell preview to remain faint after truncation, got %q", joined)
	}
	colors := extractForegroundTrueColors(joined)
	if !containsColor(colors, m.palette().previewColor) || !containsNonPreviewColor(colors, m.palette().previewColor) {
		t.Fatalf("expected wrapped shell preview to keep preview base plus syntax colors, got %q", joined)
	}
	if strings.Contains(ansi.Strip(joined), "detektFormat.log") {
		t.Fatalf("expected wrapped shell preview to omit wrapped tail after collapsing, got %q", ansi.Strip(joined))
	}
}

func TestWrappedMutedShellPreviewUsesEllipsisContinuation(t *testing.T) {
	m := NewModel(WithTheme("dark"))
	m.viewportWidth = 92
	meta := &transcript.ToolCallMeta{
		RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindShell},
	}
	command := "go test ./cli/tui -run 'Test(MuteANSIOutput|FlattenEntryWithMetaKeepsMutedShellHighlightWhenMuted|OngoingWrappedShellPreviewKeepsMutedHighlightAcrossVisualLines|ViewWrappedShellPreviewUsesMutedOngoingAndFullColorDetail|ViewSourceHintShellPreviewUsesMutedOngoing|OngoingSourceHintShellPreviewFallsBackToMutedShellHighlight)'"

	lines := m.flattenEntryWithMeta("tool_shell_success", command, true, meta)
	if len(lines) != 1 {
		t.Fatalf("expected wrapped muted shell preview to collapse to one truncated line, got %d (%q)", len(lines), lines)
	}
	if !strings.HasSuffix(strings.TrimSpace(ansi.Strip(lines[0])), "…") {
		t.Fatalf("expected wrapped muted shell preview first line to end with ellipsis, got %q", lines[0])
	}
	lineStartStates := mutedShellStyleStateAtLineStarts(strings.Join(lines, "\n"))
	if len(lineStartStates) != len(lines) {
		t.Fatalf("expected line-start style state for collapsed line, got %d want %d", len(lineStartStates), len(lines))
	}
	if !strings.Contains(lines[0], ";2m") || !containsColor(extractForegroundTrueColors(lines[0]), m.palette().previewColor) {
		t.Fatalf("expected truncated muted shell line to remain faint and preview-owned, got state=%+v line=%q", lineStartStates[0], lines[0])
	}
}

type sgrStyleState struct {
	hasForeground bool
	faint         bool
}

func mutedShellStyleStateAtLineStarts(text string) []sgrStyleState {
	parser := ansi.GetParser()
	defer ansi.PutParser(parser)

	states := []sgrStyleState{{}}
	state := byte(0)
	input := text
	current := sgrStyleState{}
	for len(input) > 0 {
		seq, width, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(input, state, parser)
		if n <= 0 {
			break
		}
		state = newState
		input = input[n:]
		if width > 0 {
			continue
		}
		if strings.Contains(seq, "\n") {
			for range strings.Count(seq, "\n") {
				states = append(states, current)
			}
			continue
		}
		if ansi.Cmd(parser.Command()).Final() != 'm' {
			continue
		}
		current = applySGRStyleState(current, parser.Params())
	}
	return states
}

func applySGRStyleState(current sgrStyleState, params ansi.Params) sgrStyleState {
	if len(params) == 0 {
		return sgrStyleState{}
	}
	for idx := 0; idx < len(params); {
		param, _, ok := params.Param(idx, 0)
		if !ok {
			break
		}
		switch {
		case param == 0:
			current = sgrStyleState{}
			idx++
		case param == 2:
			current.faint = true
			idx++
		case param == 22:
			current.faint = false
			idx++
		case param == 39:
			current.hasForeground = false
			idx++
		case (30 <= param && param <= 37) || (90 <= param && param <= 97):
			current.hasForeground = true
			idx++
		case param == 38:
			_, consumed, ok := parseANSIForegroundColor(params, idx)
			if !ok {
				idx++
				continue
			}
			current.hasForeground = true
			idx += consumed
		default:
			idx++
		}
	}
	return current
}

func TestOngoingSourceHintShellPreviewFallsBackToMutedShellHighlight(t *testing.T) {
	m := NewModel(WithTheme("dark"))
	meta := &transcript.ToolCallMeta{
		ToolName: "shell",
		IsShell:  true,
		RenderHint: &transcript.ToolRenderHint{
			Kind:       transcript.ToolRenderKindSource,
			Path:       "cli/app/app.go",
			ResultOnly: true,
		},
	}
	command := "sed -n '1,220p' cli/app/app.go"

	joined := strings.Join(m.flattenEntryWithMeta("tool_shell_success", command, true, meta), "\n")
	if !strings.Contains(joined, ";2m") {
		t.Fatalf("expected source-hinted shell preview to fall back to muted shell highlight, got %q", joined)
	}
	if colors := extractForegroundTrueColors(joined); !containsNonPreviewColor(colors, m.palette().previewColor) {
		t.Fatalf("expected source-hinted shell preview to preserve syntax colors under faint styling, got %q", joined)
	}
	if plain := strings.Join(strings.Fields(ansi.Strip(joined)), " "); plain != "$ "+command {
		t.Fatalf("expected source-hinted shell preview text preserved, got %q", plain)
	}
}

func TestViewWrappedShellPreviewUsesMutedOngoingAndFullColorDetail(t *testing.T) {
	command := "./gradlew -p apps/respawn detektFormat > docs/tmp/build-triage-2026-03-15/detektFormat.log 2>&1"
	m := NewModel(WithTheme("dark"), WithPreviewLines(20))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 28})
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

	ongoing := m.View()
	if !strings.Contains(ongoing, ";2m") {
		t.Fatalf("expected ongoing view to contain faint shell highlight, got %q", ongoing)
	}
	ongoingColors := extractForegroundTrueColors(ongoing)
	if len(ongoingColors) == 0 {
		t.Fatalf("expected ongoing view to contain parseable foreground colors, got %q", ongoing)
	}
	if !containsColor(ongoingColors, m.palette().previewColor) {
		t.Fatalf("expected ongoing view to restore preview foreground for resets/default spans, got %q", ongoing)
	}
	if !containsNonPreviewColor(ongoingColors, m.palette().previewColor) {
		t.Fatalf("expected ongoing view to preserve some syntax colors while fainting them, got %q", ongoing)
	}
	plainOngoing := plainTranscript(ongoing)
	if !strings.Contains(plainOngoing, "$ ./gradlew -p apps/respaw") || !strings.Contains(plainOngoing, "…") || strings.Contains(plainOngoing, "\n  …") {
		t.Fatalf("expected ongoing view to truncate wrapped shell preview on the first visual line, got %q", plainOngoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := m.View()
	if !strings.Contains(detail, "\x1b[38;5;") {
		t.Fatalf("expected detail view to keep full shell color output, got %q", detail)
	}
	if strings.Contains(detail, "\x1b[38;2;") && !strings.Contains(detail, "\x1b[38;5;") {
		t.Fatalf("expected detail view to avoid muted-only shell output, got %q", detail)
	}
}

func TestViewSourceHintShellPreviewUsesMutedOngoing(t *testing.T) {
	command := "sed -n '1,220p' cli/app/app.go"
	m := NewModel(WithTheme("dark"), WithPreviewLines(20))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: command,
		ToolCall: &transcript.ToolCallMeta{
			ToolName: "shell",
			IsShell:  true,
			Command:  command,
			RenderHint: &transcript.ToolRenderHint{
				Kind:       transcript.ToolRenderKindSource,
				Path:       "cli/app/app.go",
				ResultOnly: true,
			},
		},
	})

	ongoing := m.View()
	if !strings.Contains(ongoing, "\x1b[38;2;") {
		t.Fatalf("expected ongoing source-hinted shell preview to be muted-highlighted, got %q", ongoing)
	}
	if plain := plainTranscript(ongoing); !strings.Contains(plain, command) {
		t.Fatalf("expected ongoing source-hinted shell preview to show command text, got %q", plain)
	}
}

func TestViewMixedShellPreviewsUseMutedOngoingAndFullColorDetail(t *testing.T) {
	sedCommand := "sed -n '1,220p' cli/app/app.go"
	rgCommand := "rg -n \"func effectiveSettings|effectiveSettings\\(\" cli/app server/runtime server/session"
	m := NewModel(WithTheme("dark"), WithPreviewLines(20))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 120})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: sedCommand,
		ToolCall: &transcript.ToolCallMeta{
			ToolName: "shell",
			IsShell:  true,
			Command:  sedCommand,
			RenderHint: &transcript.ToolRenderHint{
				Kind:       transcript.ToolRenderKindSource,
				Path:       "cli/app/app.go",
				ResultOnly: true,
			},
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: rgCommand,
		ToolCall: &transcript.ToolCallMeta{
			ToolName:   "shell",
			IsShell:    true,
			Command:    rgCommand,
			RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindShell},
		},
	})

	ongoing := m.View()
	for _, command := range []string{sedCommand, rgCommand} {
		line := lineContaining(ongoing, command)
		if line == "" {
			t.Fatalf("expected ongoing view to contain command %q, got %q", command, plainTranscript(ongoing))
		}
		if !strings.Contains(line, "\x1b[38;2;") {
			t.Fatalf("expected ongoing command line to be muted-highlighted, got %q", line)
		}
		if strings.Contains(line, "\x1b[38;5;255m") {
			t.Fatalf("expected ongoing command line to avoid original full-color shell highlight, got %q", line)
		}
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := m.View()
	for _, command := range []string{sedCommand, rgCommand} {
		line := lineContaining(detail, command)
		if line == "" {
			t.Fatalf("expected detail view to contain command %q, got %q", command, plainTranscript(detail))
		}
		if !strings.Contains(line, "\x1b[38;5;") {
			t.Fatalf("expected detail command line to retain full shell highlight, got %q", line)
		}
	}
}

func lineContaining(text, substring string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(ansi.Strip(line), substring) {
			return line
		}
	}
	return ""
}

func oldFormatterBaseForegroundEscapes(theme string) []string {
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		return []string{"\x1b[38;5;234m"}
	}
	return []string{"\x1b[38;5;252m", "\x1b[97m", "\x1b[38;2;255;255;255m"}
}

func TestDetailShellUserInitiatedCallUsesUserRanLabel(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "pwd",
		ToolCallID: "call_1",
		ToolCall: &transcript.ToolCallMeta{
			ToolName:      "shell",
			IsShell:       true,
			UserInitiated: true,
			Command:       "pwd",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "/tmp", ToolCallID: "call_1"})

	ongoing := plainTranscript(m.View())
	if strings.Contains(ongoing, "User ran:") {
		t.Fatalf("did not expect ongoing view label to change, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := plainTranscript(m.View())
	if !strings.Contains(detail, "User ran: pwd") {
		t.Fatalf("expected detailed shell label to include user-ran text, got %q", detail)
	}
	if !strings.Contains(detail, "/tmp") {
		t.Fatalf("expected detailed shell block to include output, got %q", detail)
	}
}

func TestSuccessfulWrappedShellBlockUsesSuccessPrefixInOngoingAndDetail(t *testing.T) {
	command := `git add cli/app/ui_mode_flow_test.go cli/app/ui_native_scrollback_integration_test.go cli/app/ui_runtime_adapter_test.go cli/app/ui_runtime_client.go cli/app/ui_runtime_control_test.go cli/app/ui_runtime_sync.go cli/tui/model_reducer.go cli/tui/model_rendering.go cli/tui/model_rendering_style.go cli/tui/model_rendering_tools.go cli/tui/model_test.go cli/tui/roles.go docs/dev/decisions.md server/primaryrun/runtime_client.go server/primaryrun/runtime_client_test.go server/runtime/chat_store.go server/runtime/chat_store_test.go server/runtime/compaction.go server/runtime/engine_test.go server/runtime/step_executor.go server/runtime/transcript_event_entries.go server/runtime/transcript_projector_test.go server/runtime/transcript_scan.go server/runtime/transcript_message_visibility.go shared/clientui/runtime.go shared/transcript/roles.go && git commit -m "fix: align transcript visibility and refresh behavior"`
	m := NewModel(WithTheme("dark"), WithPreviewLines(20))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 120})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       command,
		ToolCallID: "call_1",
		ToolCall: &transcript.ToolCallMeta{
			ToolName:   "shell",
			IsShell:    true,
			Command:    command,
			RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindShell},
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "ok", ToolCallID: "call_1"})

	blocks := m.buildOngoingBlocks(true)
	var shellBlock ongoingBlock
	found := false
	for _, block := range blocks {
		if block.role == "tool_shell_success" {
			shellBlock = block
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected merged ongoing shell success block, got %#v", blocks)
	}
	if len(shellBlock.lines) != 1 {
		t.Fatalf("expected wrapped ongoing shell success block to collapse to one truncated line, got %d (%q)", len(shellBlock.lines), shellBlock.lines)
	}
	if !strings.HasSuffix(strings.TrimSpace(ansi.Strip(shellBlock.lines[0])), "…") {
		t.Fatalf("expected wrapped ongoing shell success block first line to end with ellipsis, got %q", shellBlock.lines[0])
	}
	if !strings.HasPrefix(shellBlock.lines[0], m.roleSymbol("tool_shell_success")+" ") {
		t.Fatalf("expected ongoing shell success line to use success prefix, got %q", shellBlock.lines[0])
	}
	ongoingLine := shellBlock.lines[0]
	if !strings.Contains(ongoingLine, ";2m") {
		t.Fatalf("expected ongoing shell success line to stay faint, got %q", ongoingLine)
	}
	if !containsColor(extractForegroundTrueColors(ongoingLine), m.palette().successColor) {
		t.Fatalf("expected ongoing shell success line to contain success color for prefix, got %q", ongoingLine)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := m.View()
	detailLine := lineContaining(detail, "git add cli/app/ui_mode_flow_test.go")
	if detailLine == "" {
		t.Fatalf("expected detail view to contain successful shell command, got %q", plainTranscript(detail))
	}
	if !strings.HasPrefix(detailLine, m.roleSymbol("tool_shell_success")+" ") {
		t.Fatalf("expected detail shell success line to use success prefix, got %q", detailLine)
	}
	if strings.Contains(detailLine, ";2m") {
		t.Fatalf("expected detail shell success line to avoid faint styling, got %q", detailLine)
	}
	if !containsColor(extractForegroundTrueColors(detailLine), m.palette().successColor) {
		t.Fatalf("expected detail shell success line to contain success color for prefix, got %q", detailLine)
	}
	if !containsColor(extractForegroundTrueColors(detailLine), m.palette().foregroundColor) {
		t.Fatalf("expected detail shell success line to restore app foreground across resets, got %q", detailLine)
	}
}

func TestOngoingShellMultilineCommandPreviewIsCollapsedToTwoLines(t *testing.T) {
	command := strings.Join([]string{
		"cat > /tmp/demo.txt <<'EOF'",
		"first line",
		"second line",
		"third line",
		"EOF",
	}, "\n")

	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: command,
		ToolCall: &transcript.ToolCallMeta{
			ToolName: "shell",
			IsShell:  true,
			Command:  command,
		},
	})

	blocks := m.buildOngoingBlocks(true)
	shellBlockLines := []string{}
	for _, block := range blocks {
		if block.role == "tool_shell" {
			shellBlockLines = block.lines
			break
		}
	}
	if len(shellBlockLines) != 1 {
		t.Fatalf("expected ongoing shell preview to collapse to one truncated line, got %d (%q)", len(shellBlockLines), shellBlockLines)
	}
	if !strings.HasSuffix(strings.TrimSpace(ansi.Strip(shellBlockLines[0])), "…") {
		t.Fatalf("expected ongoing shell preview first line to end with ellipsis, got %q", shellBlockLines[0])
	}

	ongoing := plainTranscript(m.OngoingSnapshot())
	if !strings.Contains(ongoing, "…") || strings.Contains(ongoing, "\n  …") {
		t.Fatalf("expected ongoing shell preview to inline ellipsis on the first line, got %q", ongoing)
	}
	if strings.Contains(ongoing, "second line") || strings.Contains(ongoing, "third line") || strings.Contains(ongoing, "\n  EOF") {
		t.Fatalf("expected ongoing shell preview to omit heredoc tail, got %q", ongoing)
	}

	detail := plainTranscript(m.renderFlatDetailTranscript())
	if !strings.Contains(detail, "third line") || !strings.Contains(detail, "EOF") {
		t.Fatalf("expected detail transcript to keep full shell command, got %q", detail)
	}
}

func TestOngoingShellSingleLineCommandCollapsesAfterFirstVisualLine(t *testing.T) {
	command := "printf '%s' " + strings.Repeat("very-long-token-", 10)

	m := NewModel(WithPreviewLines(30))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 30, Width: 28})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: command,
		ToolCall: &transcript.ToolCallMeta{
			ToolName: "shell",
			IsShell:  true,
			Command:  command,
		},
	})

	blocks := m.buildOngoingBlocks(true)
	shellBlockLines := []string{}
	for _, block := range blocks {
		if block.role == "tool_shell" {
			shellBlockLines = block.lines
			break
		}
	}
	if len(shellBlockLines) != 1 {
		t.Fatalf("expected long single-line shell command to collapse to one truncated line, got %d lines (%q)", len(shellBlockLines), shellBlockLines)
	}
	if !strings.HasSuffix(strings.TrimSpace(ansi.Strip(shellBlockLines[0])), "…") {
		t.Fatalf("expected inline ellipsis collapse marker for single-line shell command, got %q", shellBlockLines)
	}
}

func TestOngoingShellMultilinePreviewStaysTwoLinesWhenHeaderWraps(t *testing.T) {
	command := strings.Join([]string{
		"cat > /tmp/" + strings.Repeat("very-long-name-", 8) + "demo.txt <<'EOF'",
		"body line",
		"EOF",
	}, "\n")

	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 28})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: command,
		ToolCall: &transcript.ToolCallMeta{
			ToolName: "shell",
			IsShell:  true,
			Command:  command,
		},
	})

	blocks := m.buildOngoingBlocks(true)
	shellBlockLines := []string{}
	for _, block := range blocks {
		if block.role == "tool_shell" {
			shellBlockLines = block.lines
			break
		}
	}
	if len(shellBlockLines) != 1 {
		t.Fatalf("expected wrapped multiline shell preview to collapse to one truncated line, got %d (%q)", len(shellBlockLines), shellBlockLines)
	}
	if !strings.HasSuffix(strings.TrimSpace(ansi.Strip(shellBlockLines[0])), "…") {
		t.Fatalf("expected wrapped multiline shell preview first line to end with ellipsis, got %q", shellBlockLines[0])
	}
}

func TestDetailSnapshotCachesLinesAcrossScrollUpdates(t *testing.T) {
	m := NewModel(WithTheme("dark"))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 24, Width: 100})
	m = updateModel(t, m, SetConversationMsg{Entries: []TranscriptEntry{
		{Role: "user", Text: "hello"},
		{Role: "assistant", Text: "world"},
	}})
	m = updateModel(t, m, ToggleModeMsg{})

	if len(m.detailLines) == 0 {
		t.Fatal("expected detail lines cache to be populated on detail entry")
	}
	startLen := len(m.detailLines)

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if got := len(m.detailLines); got != startLen {
		t.Fatalf("expected detail lines cache length to stay stable across scroll updates, got %d want %d", got, startLen)
	}
}

func TestDetailScrollStepAllocsStayBounded(t *testing.T) {
	entries := benchmarkDetailEntries(300)
	m := NewModel(WithTheme("dark"))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 40, Width: 120})
	m = updateModel(t, m, SetConversationMsg{Entries: entries})
	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, ScrollOngoingMsg{Delta: -120})

	allocs := testing.AllocsPerRun(20, func() {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(Model)
		_ = m.View()
	})
	if allocs > 50 {
		t.Fatalf("expected detail scroll allocations to stay bounded, got %.2f allocs/op", allocs)
	}
}

func TestOngoingScrollStepAllocsStayBounded(t *testing.T) {
	entries := benchmarkDetailEntries(300)
	m := NewModel(WithTheme("dark"))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 40, Width: 120})
	m = updateModel(t, m, SetConversationMsg{Entries: entries})
	m = updateModel(t, m, ScrollOngoingMsg{Delta: -120})

	allocs := testing.AllocsPerRun(20, func() {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(Model)
		_ = m.View()
	})
	if allocs > 100 {
		t.Fatalf("expected ongoing scroll allocations to stay bounded, got %.2f allocs/op", allocs)
	}
}

func TestDetailStreamingUpdateAllocsStayBounded(t *testing.T) {
	entries := benchmarkDetailEntries(300)
	m := NewModel(WithTheme("dark"))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 40, Width: 120})
	m = updateModel(t, m, SetConversationMsg{Entries: entries})
	m = updateModel(t, m, ToggleModeMsg{})

	base := m
	allocs := testing.AllocsPerRun(20, func() {
		local := base
		next, _ := local.Update(StreamAssistantMsg{Delta: "x"})
		local = next.(Model)
		_ = local.View()
	})
	if allocs > 80 {
		t.Fatalf("expected detail streaming update allocations to stay bounded, got %.2f allocs/op", allocs)
	}
}

func TestOngoingStreamingUpdateAllocsStayBounded(t *testing.T) {
	entries := benchmarkDetailEntries(300)
	m := NewModel(WithTheme("dark"))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 40, Width: 120})
	m = updateModel(t, m, SetConversationMsg{Entries: entries})

	base := m
	allocs := testing.AllocsPerRun(20, func() {
		local := base
		next, _ := local.Update(StreamAssistantMsg{Delta: "x"})
		local = next.(Model)
		_ = local.View()
	})
	if allocs > 120 {
		t.Fatalf("expected ongoing streaming update allocations to stay bounded, got %.2f allocs/op", allocs)
	}
}
func TestOngoingStreamingAccessorsStableAcrossModeTogglesAndRefresh(t *testing.T) {
	m := NewModel(WithTheme("dark"))
	m = updateModel(t, m, SetConversationMsg{Entries: []TranscriptEntry{{Role: "assistant", Text: "committed"}}, Ongoing: "stream one", OngoingError: "error one"})
	if got := m.OngoingStreamingText(); got != "stream one" {
		t.Fatalf("unexpected ongoing streaming text: %q", got)
	}
	if got := m.OngoingErrorText(); got != "error one" {
		t.Fatalf("unexpected ongoing error text: %q", got)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, ToggleModeMsg{})
	if got := m.OngoingStreamingText(); got != "stream one" {
		t.Fatalf("expected streaming text stable across mode roundtrip, got %q", got)
	}
	if got := m.OngoingErrorText(); got != "error one" {
		t.Fatalf("expected error text stable across mode roundtrip, got %q", got)
	}

	m = updateModel(t, m, SetConversationMsg{Entries: []TranscriptEntry{{Role: "assistant", Text: "committed"}}, Ongoing: "stream two", OngoingError: "error two"})
	if got := m.OngoingStreamingText(); got != "stream two" {
		t.Fatalf("expected streaming text updated after conversation refresh, got %q", got)
	}
	if got := m.OngoingErrorText(); got != "error two" {
		t.Fatalf("expected error text updated after conversation refresh, got %q", got)
	}
}

type errString string

func (e errString) Error() string {
	return string(e)
}

func updateModel(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()

	next, _ := m.Update(msg)
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	return updated
}

func transcriptEntriesRange(start, end int) []TranscriptEntry {
	entries := make([]TranscriptEntry, 0, max(0, end-start))
	for i := start; i < end; i++ {
		entries = append(entries, TranscriptEntry{Role: "assistant", Text: fmt.Sprintf("line %d", i)})
	}
	return entries
}

func plainTranscript(view string) string {
	stripped := ansi.Strip(view)
	lines := strings.Split(stripped, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

func containsInOrder(text string, parts ...string) bool {
	offset := 0
	for _, part := range parts {
		idx := strings.Index(text[offset:], part)
		if idx < 0 {
			return false
		}
		offset += idx + len(part)
	}
	return true
}
