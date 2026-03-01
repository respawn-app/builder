package app

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"builder/internal/config"
	"builder/internal/runtime"
	"builder/internal/transcript"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
)

func stripANSIText(v string) string {
	return strings.Join(strings.Fields(xansi.Strip(v)), " ")
}

func stripANSIPreserve(v string) string {
	return xansi.Strip(v)
}

func TestNativeScrollbackStartupReplayIncludesFullTranscript(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIScrollMode(config.TUIScrollModeNative),
		WithUIInitialTranscript([]UITranscriptEntry{
			{Role: "user", Text: "first message"},
			{Role: "assistant", Text: "last message"},
		}),
	).(*uiModel)

	if len(m.startupCmds) != 0 {
		t.Fatalf("expected startup native history replay deferred until window size, got %d startup cmd(s)", len(m.startupCmds))
	}
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	updated, ok := next.(*uiModel)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	m = updated
	if cmd == nil {
		t.Fatal("expected native replay command after first window size")
	}
	msg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg after first window size, got %T", cmd())
	}
	plain := stripANSIText(msg.Text)
	if !strings.Contains(plain, "first message") || !strings.Contains(plain, "last message") {
		t.Fatalf("expected startup native replay to include full transcript, got %q", msg.Text)
	}
}

func TestNativeScrollbackEmitsOnlyNewTranscriptLines(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIScrollMode(config.TUIScrollModeNative),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "old line"}}),
	).(*uiModel)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.nativeHistoryReplayed = true
	m.nativeFlushedEntryCount = len(m.transcriptEntries)

	if cmd := m.syncNativeHistoryFromTranscript(); cmd != nil {
		t.Fatal("expected no delta command without transcript changes")
	}

	m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "new line"})
	m.transcriptEntries = append(m.transcriptEntries, tui.TranscriptEntry{Role: "assistant", Text: "new line"})
	cmd := m.syncNativeHistoryFromTranscript()
	if cmd == nil {
		t.Fatal("expected native history delta command after transcript append")
	}
	msg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
	plain := stripANSIText(msg.Text)
	if !strings.Contains(plain, "new line") {
		t.Fatalf("expected delta replay to include new line, got %q", msg.Text)
	}
	if strings.Contains(plain, "old line") {
		t.Fatalf("expected delta replay to exclude old history, got %q", msg.Text)
	}
}

func TestNativeScrollbackFlowIntegration(t *testing.T) {
	entries := make([]UITranscriptEntry, 0, 120)
	for i := 1; i <= 120; i++ {
		entries = append(entries, UITranscriptEntry{Role: "assistant", Text: fmt.Sprintf("message %d", i)})
	}
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIScrollMode(config.TUIScrollModeNative),
		WithUIInitialTranscript(entries),
	).(*uiModel)
	nextModel, startupCmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	updatedModel, ok := nextModel.(*uiModel)
	if !ok {
		t.Fatalf("unexpected model type %T", nextModel)
	}
	m = updatedModel

	if startupCmd == nil {
		t.Fatal("expected startup replay command after initial window size")
	}
	startupMsg, ok := startupCmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg at startup, got %T", startupCmd())
	}
	startupPlain := stripANSIText(startupMsg.Text)
	if !strings.Contains(startupPlain, "message 1") || !strings.Contains(startupPlain, "message 120") {
		t.Fatalf("expected startup replay to contain earliest and latest entries")
	}
	if _, cmd := m.Update(startupMsg); cmd == nil {
		t.Fatal("expected non-nil command for startup flush")
	}

	modeBefore := m.view.Mode()
	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected detail mode after toggle, got %q", m.view.Mode())
	}
	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.view.Mode() != modeBefore {
		t.Fatalf("expected ongoing mode after second toggle, got %q", m.view.Mode())
	}

	start := m.view.OngoingScroll()
	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if got := m.view.OngoingScroll(); got != start {
		t.Fatalf("expected pgup to avoid in-app ongoing scroll in native mode, got %d from %d", got, start)
	}

	m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "message 121"})
	m.transcriptEntries = append(m.transcriptEntries, tui.TranscriptEntry{Role: "assistant", Text: "message 121"})
	deltaCmd := m.syncNativeHistoryFromTranscript()
	if deltaCmd == nil {
		t.Fatal("expected replay delta command after new message")
	}
	deltaMsg, ok := deltaCmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg delta, got %T", deltaCmd())
	}
	deltaPlain := stripANSIText(deltaMsg.Text)
	if !strings.Contains(deltaPlain, "message 121") {
		t.Fatalf("expected delta replay to contain only new tail content, got %q", deltaMsg.Text)
	}
	if _, cmd := m.Update(deltaMsg); cmd == nil {
		t.Fatal("expected non-nil command for delta flush")
	}
}

func TestRenderNativeScrollbackEntriesPreservesMeaningfulWhitespace(t *testing.T) {
	text := "  \tline one\n\tline two\n"
	out := renderNativeScrollbackSnapshot([]tui.TranscriptEntry{{Role: "assistant", Text: text}}, "dark", 120)
	plain := stripANSIPreserve(out)
	if !strings.Contains(plain, "line one") {
		t.Fatalf("expected first line content preserved, got %q", out)
	}
	if !strings.Contains(plain, "line two") {
		t.Fatalf("expected second line content preserved, got %q", out)
	}
}

func TestNativeScrollbackSnapshotPreservesCodeBlockIndentation(t *testing.T) {
	text := "```yaml\nroot:\n  key: value\n```"
	out := renderNativeScrollbackSnapshot([]tui.TranscriptEntry{{Role: "assistant", Text: text}}, "dark", 100)
	plain := stripANSIPreserve(out)
	if !strings.Contains(plain, "root:") || !strings.Contains(plain, "  key: value") {
		t.Fatalf("expected yaml indentation preserved in formatted snapshot, got %q", out)
	}
}

func TestRenderNativeScrollbackSnapshotPreservesToolCallFormatting(t *testing.T) {
	out := renderNativeScrollbackSnapshot([]tui.TranscriptEntry{
		{
			Role: "tool_call",
			Text: `{"command":"echo hi"}`,
			ToolCall: &transcript.ToolCallMeta{
				ToolName: "shell",
				IsShell:  true,
				Command:  "echo hi",
			},
		},
		{Role: "tool_result_ok", Text: "hi"},
	}, "dark", 100)
	plain := stripANSIText(out)
	if !strings.Contains(plain, "echo hi") {
		t.Fatalf("expected tool call command preserved, got %q", out)
	}
	if !strings.Contains(plain, "hi") {
		t.Fatalf("expected tool result preserved, got %q", out)
	}
}

func renderNativeScrollbackSnapshotLegacy(entries []tui.TranscriptEntry, theme string, width int) string {
	if len(entries) == 0 {
		return ""
	}
	if width <= 0 {
		width = 120
	}
	tuiModel := tui.NewModel(tui.WithTheme(theme), tui.WithPreviewLines(200000))
	next, _ := tuiModel.Update(tui.SetViewportSizeMsg{Lines: 200000, Width: width})
	if casted, ok := next.(tui.Model); ok {
		tuiModel = casted
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.Text) == "" {
			continue
		}
		next, _ = tuiModel.Update(tui.AppendTranscriptMsg{
			Role:       entry.Role,
			Text:       entry.Text,
			Phase:      entry.Phase,
			ToolCallID: entry.ToolCallID,
			ToolCall:   entry.ToolCall,
		})
		if casted, ok := next.(tui.Model); ok {
			tuiModel = casted
		}
	}
	return styleNativeReplayDividers(tuiModel.OngoingCommittedSnapshot(), theme, width)
}

func TestRenderNativeScrollbackSnapshotMatchesLegacyAppendPath(t *testing.T) {
	entries := []tui.TranscriptEntry{
		{Role: "user", Text: "show files"},
		{Role: "tool_call", Text: "ls -la", ToolCallID: "call_1", ToolCall: &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "ls -la"}},
		{Role: "tool_result_ok", Text: "total 8\n-rw-r--r-- a.txt", ToolCallID: "call_1"},
		{Role: "tool_call", Text: "Choose scope?", ToolCallID: "call_2", ToolCall: &transcript.ToolCallMeta{ToolName: "ask_question", Question: "Choose scope?", Suggestions: []string{"Recommended: full"}}},
		{Role: "tool_result_ok", Text: "Use full scope.", ToolCallID: "call_2"},
		{Role: "tool_call", Text: "Edited:\n./a.go +1 -1", ToolCallID: "call_3", ToolCall: &transcript.ToolCallMeta{ToolName: "patch", PatchSummary: "Edited:\n./a.go +1 -1", PatchDetail: "Edited:\n/work/a.go\n-old\n+new", RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindDiff}}},
		{Role: "tool_result_ok", Text: "", ToolCallID: "call_3"},
	}
	modern := renderNativeScrollbackSnapshot(entries, "dark", 120)
	legacy := renderNativeScrollbackSnapshotLegacy(entries, "dark", 120)
	if modern != legacy {
		t.Fatalf("expected native snapshot output to match legacy append path")
	}
}

func TestNativeScrollbackInitDoesNotClearScreen(t *testing.T) {
	native := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIScrollMode(config.TUIScrollModeNative)).(*uiModel)
	if native.shouldClearOnInit() {
		t.Fatal("expected native mode init to avoid clear screen")
	}
	alt := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIScrollMode(config.TUIScrollModeAlt)).(*uiModel)
	if !alt.shouldClearOnInit() {
		t.Fatal("expected alt mode init to clear screen")
	}
}

func TestNativeScrollbackReplayIsChunkedForLargeSessions(t *testing.T) {
	lines := make([]string, 0, 10000)
	for i := 0; i < 10000; i++ {
		lines = append(lines, fmt.Sprintf("entry-%d", i))
	}
	rendered := strings.Join(lines, "\n")
	chunks := splitNativeScrollbackChunks(rendered, 4096)
	if len(chunks) < 2 {
		t.Fatalf("expected chunked replay for large history, got %d chunk(s)", len(chunks))
	}
	for idx, chunk := range chunks {
		if len(chunk) > 8192 {
			t.Fatalf("expected bounded chunk size, chunk %d has %d bytes", idx, len(chunk))
		}
	}
}

func TestNativeScrollbackSnapshotFormattingPerformanceBound(t *testing.T) {
	entries := make([]tui.TranscriptEntry, 0, 200)
	for i := 0; i < 200; i++ {
		entries = append(entries, tui.TranscriptEntry{Role: "assistant", Text: "**bold** entry `code`"})
	}
	start := time.Now()
	out := renderNativeScrollbackSnapshot(entries, "dark", 120)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("expected formatted native snapshot under performance budget, took %s", elapsed)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("expected non-empty formatted snapshot")
	}
}

func TestNativeScrollbackReplayRespectsRenderWidth(t *testing.T) {
	entry := tui.TranscriptEntry{Role: "assistant", Text: "This line is intentionally long and should wrap differently for small render widths."}
	before := strings.Join(splitNativeScrollbackChunks(renderNativeScrollbackSnapshot([]tui.TranscriptEntry{entry}, "dark", 120), 64*1024), "")
	after := strings.Join(splitNativeScrollbackChunks(renderNativeScrollbackSnapshot([]tui.TranscriptEntry{entry}, "dark", 20), 64*1024), "")
	if before == after {
		t.Fatalf("expected native replay bytes to differ across render widths, got same output=%q", before)
	}
}

func TestNativeReplayChunksContainFormattedANSIText(t *testing.T) {
	rendered := renderNativeScrollbackSnapshot([]tui.TranscriptEntry{{Role: "assistant", Text: "**hello**\n`world`"}}, "dark", 120)
	chunks := splitNativeScrollbackChunks(rendered, 64*1024)
	joined := strings.Join(chunks, "")
	if !strings.Contains(joined, "\x1b[") {
		t.Fatalf("expected formatted native replay content with ansi escapes, got %q", joined)
	}
}

func TestNativeReplayDividerStyledAndExpandedToWidth(t *testing.T) {
	rendered := renderNativeScrollbackSnapshot([]tui.TranscriptEntry{
		{Role: "user", Text: "hello"},
		{Role: "assistant", Text: "world"},
	}, "dark", 80)
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("expected styled divider with ansi escapes, got %q", rendered)
	}
	plain := stripANSIPreserve(rendered)
	lines := strings.Split(plain, "\n")
	dividerRunes := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		allDivider := true
		for _, r := range trimmed {
			if r != '─' {
				allDivider = false
				break
			}
		}
		if allDivider {
			dividerRunes = utf8.RuneCountInString(trimmed)
			break
		}
	}
	if dividerRunes != 80 {
		t.Fatalf("expected divider width 80 runes, got %d in %q", dividerRunes, plain)
	}
}

func TestEnsureNativeFlushNewlineAppendsTerminator(t *testing.T) {
	if got := ensureNativeFlushNewline("line"); got != "line\n" {
		t.Fatalf("expected newline terminator appended, got %q", got)
	}
	if got := ensureNativeFlushNewline("line\n"); got != "line\n" {
		t.Fatalf("expected existing newline to be preserved, got %q", got)
	}
}

func TestNativeOngoingKeepsLiveRegionHeightStableAcrossInputShrink(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIScrollMode(config.TUIScrollModeNative)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "line 1\nline 2\nline 3"
	m.syncViewport()
	first := strings.Split(m.View(), "\n")
	firstLines := len(first)
	if firstLines < 4 {
		t.Fatalf("expected multi-line native region, got %d", firstLines)
	}
	m.input = ""
	m.syncViewport()
	second := strings.Split(m.View(), "\n")
	if len(second) != firstLines {
		t.Fatalf("expected stable native live region line count after shrink, got %d want %d", len(second), firstLines)
	}
}

func TestNativeOngoingKeepsInputAndStatusAtBottomOfLiveRegion(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIScrollMode(config.TUIScrollModeNative)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 12
	m.windowSizeKnown = true
	m.input = "hello"
	m.syncViewport()
	lines := strings.Split(stripANSIPreserve(m.View()), "\n")
	if len(lines) != 12 {
		t.Fatalf("expected native ongoing view to fill terminal height, got %d lines", len(lines))
	}
	if !strings.Contains(lines[len(lines)-1], "ongoing") {
		t.Fatalf("expected status line at terminal bottom, got %q", lines[len(lines)-1])
	}
	windowTail := strings.Join(lines[len(lines)-5:], "\n")
	if !strings.Contains(windowTail, "› hello") {
		t.Fatalf("expected input region in bottom window tail, got %q", windowTail)
	}
}

func TestNativeOngoingRendersBeforeWindowSizeKnownWithFallbackDimensions(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIScrollMode(config.TUIScrollModeNative)).(*uiModel)
	m.input = "hello"
	got := stripANSIPreserve(m.View())
	if strings.TrimSpace(got) == "" {
		t.Fatalf("expected native ongoing render before first window size, got %q", got)
	}
	if !strings.Contains(got, "ongoing") {
		t.Fatalf("expected fallback render to include status line, got %q", got)
	}
	if lines := len(strings.Split(got, "\n")); lines > 8 {
		t.Fatalf("expected bounded pre-size native render output, got %d lines", lines)
	}
}

func TestNativeOngoingRendersWhenTrimmedToHeight(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIScrollMode(config.TUIScrollModeNative)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 4
	m.windowSizeKnown = true
	m.nativeLiveRegionPad = 2
	m.input = "line1\nline2\nline3"
	view := m.View()
	if strings.TrimSpace(stripANSIPreserve(view)) == "" {
		t.Fatalf("expected non-empty native render under tight height, got %q", view)
	}
	if !strings.Contains(stripANSIPreserve(view), "ongoing") {
		t.Fatalf("expected status line visible under tight height, got %q", view)
	}
}
