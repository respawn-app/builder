package app

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

func TestNativeScrollbackRebasesFormatterSilentlyOnNonAppendMutation(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "old line"}, {Role: "assistant", Text: "tail line"}}),
	).(*uiModel)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}

	m.transcriptEntries[0].Text = "mutated line"
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})
	cmd := m.syncNativeHistoryFromTranscript()
	if cmd != nil {
		t.Fatalf("expected no native replay emission for non-append mutation, got %T", cmd())
	}
}

func TestNativeScrollbackResizeRebasesFormatterWidth(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "old line"}}),
	).(*uiModel)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}
	if m.nativeFormatterWidth != 40 {
		t.Fatalf("expected initial formatter width 40, got %d", m.nativeFormatterWidth)
	}

	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if m.nativeFormatterWidth != 100 {
		t.Fatalf("expected formatter width rebased to 100 after resize, got %d", m.nativeFormatterWidth)
	}

	m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "new line"})
	m.transcriptEntries = append(m.transcriptEntries, tui.TranscriptEntry{Role: "assistant", Text: "new line"})
	cmd := m.syncNativeHistoryFromTranscript()
	if cmd == nil {
		t.Fatal("expected delta command after append post-resize")
	}
	msg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
	plain := stripANSIText(msg.Text)
	if !strings.Contains(plain, "new line") {
		t.Fatalf("expected delta replay to include new entry, got %q", msg.Text)
	}
	if strings.Contains(plain, "old line") {
		t.Fatalf("expected delta replay to exclude previously flushed history, got %q", msg.Text)
	}
}

func TestNativeResizeReplayDebouncedToLatestResize(t *testing.T) {
	previousDebounce := nativeResizeReplayDebounce
	nativeResizeReplayDebounce = time.Millisecond
	t.Cleanup(func() {
		nativeResizeReplayDebounce = previousDebounce
	})

	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "old line"}}),
	).(*uiModel)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}

	next, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected debounced resize replay command")
	}
	firstToken := m.nativeResizeReplayToken

	next, cmd = m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected debounced resize replay command for later resize")
	}
	secondToken := m.nativeResizeReplayToken
	if secondToken <= firstToken {
		t.Fatalf("expected resize replay token to advance, first=%d second=%d", firstToken, secondToken)
	}

	next, staleCmd := m.Update(nativeResizeReplayMsg{token: firstToken})
	m = next.(*uiModel)
	if staleCmd != nil {
		t.Fatalf("expected stale resize replay token ignored, got %T", staleCmd)
	}

	next, replayCmd := m.Update(nativeResizeReplayMsg{token: secondToken})
	m = next.(*uiModel)
	if replayCmd == nil {
		t.Fatal("expected latest resize replay token to emit replay command")
	}
	if msg := replayCmd(); msg == nil {
		t.Fatal("expected replay command to return a message")
	}
	if m.nativeFormatterWidth != 100 {
		t.Fatalf("expected formatter width rebased to latest resize width 100, got %d", m.nativeFormatterWidth)
	}
}

func TestNativeResizeReplayInvalidatedAcrossModeSwitch(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	).(*uiModel)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}

	next, resizeCmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = next.(*uiModel)
	if resizeCmd == nil {
		t.Fatal("expected debounced resize replay command")
	}
	staleToken := m.nativeResizeReplayToken

	_ = m.toggleTranscriptModeWithNativeReplay(false)
	if m.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected detail mode, got %q", m.view.Mode())
	}
	_ = m.toggleTranscriptModeWithNativeReplay(false)
	if m.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected ongoing mode, got %q", m.view.Mode())
	}
	if m.nativeResizeReplayToken == staleToken {
		t.Fatalf("expected mode switch to invalidate stale resize replay token %d", staleToken)
	}

	next, staleCmd := m.Update(nativeResizeReplayMsg{token: staleToken})
	m = next.(*uiModel)
	if staleCmd != nil {
		t.Fatalf("expected stale resize replay ignored after mode switch, got %T", staleCmd)
	}
}

func TestNativeStreamingContractViewportDuringStreamCommittedReplayOnFinish(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt once"}}),
	).(*uiModel)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}
	if len(m.transcriptEntries) != 1 {
		t.Fatalf("expected one committed transcript entry at start, got %d", len(m.transcriptEntries))
	}

	next, _ := m.Update(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "stream line"}})
	updated, ok := next.(*uiModel)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	m = updated
	if len(m.transcriptEntries) != 1 {
		t.Fatalf("expected streaming not to append committed transcript yet, got %d entries", len(m.transcriptEntries))
	}
	if !strings.Contains(stripANSIPreserve(m.View()), "stream line") {
		t.Fatalf("expected ongoing viewport to show streaming text")
	}

	m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "stream line\nfinal line"})
	m.transcriptEntries = append(m.transcriptEntries, tui.TranscriptEntry{Role: "assistant", Text: "stream line\nfinal line"})
	commitCmd := m.syncNativeHistoryFromTranscript()
	if commitCmd == nil {
		t.Fatal("expected native replay delta after committed assistant append")
	}
	flush, ok := commitCmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", commitCmd())
	}
	plain := stripANSIText(flush.Text)
	if strings.Count(plain, "stream line") != 1 || strings.Count(plain, "final line") != 1 {
		t.Fatalf("expected committed assistant text appended exactly once on finish, got %q", flush.Text)
	}
}

func TestNativeScrollbackShrinkRebasesWithoutReemittingHistory(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "line one"}, {Role: "assistant", Text: "line two"}}),
	).(*uiModel)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}

	m.transcriptEntries = m.transcriptEntries[:1]
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})
	cmd := m.syncNativeHistoryFromTranscript()
	if cmd != nil {
		t.Fatalf("expected no replay emission after transcript shrink, got %T", cmd())
	}
}

func TestNativeScrollbackRepeatedConversationRefreshDoesNotDuplicateUserPrompt(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt once"}}),
	).(*uiModel)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}
	startupMsg, ok := startupCmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", startupCmd())
	}
	combined := startupMsg.Text + "\n"

	for i := 0; i < 12; i++ {
		m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})
		if cmd := m.syncNativeHistoryFromTranscript(); cmd != nil {
			t.Fatalf("expected no replay emission on repeated conversation refresh #%d, got %T", i, cmd())
		}
	}

	m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "tail"})
	m.transcriptEntries = append(m.transcriptEntries, tui.TranscriptEntry{Role: "assistant", Text: "tail"})
	cmd := m.syncNativeHistoryFromTranscript()
	if cmd == nil {
		t.Fatal("expected tail delta command")
	}
	msg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
	combined += msg.Text
	plain := stripANSIText(combined)
	if count := strings.Count(plain, "prompt once"); count != 1 {
		t.Fatalf("expected prompt emitted once across repeated refreshes, got %d occurrences", count)
	}
}

func TestNativeScrollbackIncrementalFlushConcatenationMatchesFullSnapshot(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "line 1"}}),
	).(*uiModel)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}
	startupMsg, ok := startupCmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", startupCmd())
	}
	combined := startupMsg.Text + "\n"

	appendEntry := func(text string) {
		t.Helper()
		m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: text})
		m.transcriptEntries = append(m.transcriptEntries, tui.TranscriptEntry{Role: "assistant", Text: text})
		cmd := m.syncNativeHistoryFromTranscript()
		if cmd == nil {
			t.Fatalf("expected replay command after append %q", text)
		}
		msg, ok := cmd().(nativeHistoryFlushMsg)
		if !ok {
			t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
		}
		combined += msg.Text + "\n"
	}

	appendEntry("line 2\n\n```yaml\nroot:\n  key: value\n```")
	appendEntry("line 3 with `code`")

	combined = strings.TrimSuffix(combined, "\n")
	expected := renderNativeScrollbackSnapshot(m.transcriptEntries, m.theme, m.nativeFormatterWidth)
	if combined != expected {
		t.Fatalf("expected concatenated incremental flush output to match full snapshot\ncombined=%q\nexpected=%q", combined, expected)
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
	if got := m.view.OngoingScroll(); got >= start {
		t.Fatalf("expected pgup to scroll ongoing transcript state, got %d from %d", got, start)
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

func TestNativeCommittedEntriesStopsAtFirstUnresolvedToolCall(t *testing.T) {
	entries := []tui.TranscriptEntry{
		{Role: "user", Text: "prompt"},
		{Role: "tool_call", Text: "echo a", ToolCallID: "call_a", ToolCall: &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "echo a"}},
		{Role: "tool_call", Text: "echo b", ToolCallID: "call_b", ToolCall: &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "echo b"}},
		{Role: "tool_result_ok", Text: "out-b", ToolCallID: "call_b"},
	}

	committed := nativeCommittedEntries(entries)
	if len(committed) != 1 || committed[0].Text != "prompt" {
		t.Fatalf("expected only stable prefix committed, got %#v", committed)
	}
	pending := nativePendingEntries(entries)
	if len(pending) != 3 {
		t.Fatalf("expected unresolved tool tail to stay pending, got %d entries", len(pending))
	}

	entries = append(entries, tui.TranscriptEntry{Role: "tool_result_ok", Text: "out-a", ToolCallID: "call_a"})
	committed = nativeCommittedEntries(entries)
	if len(committed) != len(entries) {
		t.Fatalf("expected full transcript committed once first unresolved call completes, got %d of %d entries", len(committed), len(entries))
	}
}

func TestNativePendingToolEntriesTrackParallelCommitFrontier(t *testing.T) {
	entries := []tui.TranscriptEntry{
		{Role: "user", Text: "prompt"},
		{Role: "tool_call", Text: "echo a", ToolCallID: "call_a", ToolCall: &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "echo a"}},
		{Role: "tool_call", Text: "echo b", ToolCallID: "call_b", ToolCall: &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "echo b"}},
		{Role: "tool_result_ok", Text: "out-b", ToolCallID: "call_b"},
	}

	pending := nativePendingToolEntries(entries)
	if len(pending) != 2 {
		t.Fatalf("expected two pending tool entries, got %#v", pending)
	}
	if pending[0].ToolCallID != "call_a" || pending[0].Role != "tool_call" || pending[0].Text != "echo a" {
		t.Fatalf("unexpected first pending tool entry: %#v", pending[0])
	}
	if pending[1].ToolCallID != "call_b" || pending[1].Role != "tool_call" || pending[1].Text != "echo b" {
		t.Fatalf("unexpected second pending tool entry: %#v", pending[1])
	}

	rendered := renderNativePendingToolSnapshot(entries, "dark", 40)
	plain := stripANSIPreserve(rendered)
	if !strings.Contains(plain, "$ echo a") {
		t.Fatalf("expected first pending tool preview in live region, got %q", plain)
	}
	expectedLater := stripANSIPreserve(renderNativePendingToolSnapshot([]tui.TranscriptEntry{entries[2]}, "dark", 40))
	if expectedLater == "" {
		t.Fatal("expected standalone later pending tool preview")
	}
	if !strings.Contains(plain, "$ echo b") {
		t.Fatalf("expected later completed call to remain visually identical pending preview, got %q", plain)
	}
	if !strings.Contains(plain, expectedLater) {
		t.Fatalf("expected later completed call to reuse the plain pending tool-call preview, got %q want substring %q", plain, expectedLater)
	}
	if strings.Contains(plain, "waiting") {
		t.Fatalf("did not expect waiting annotation in pending tool preview, got %q", plain)
	}
}

func TestNativePendingMultilineShellPreviewStaysTwoLines(t *testing.T) {
	command := strings.Join([]string{
		"cat > /tmp/demo.txt <<'EOF'",
		"first line",
		"second line",
		"EOF",
	}, "\n")
	entries := []tui.TranscriptEntry{{
		Role:       "tool_call",
		Text:       command,
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: command},
	}}

	pending := nativePendingToolEntries(entries)
	if len(pending) != 1 {
		t.Fatalf("expected one pending tool entry, got %#v", pending)
	}

	rendered := strings.Split(renderNativePendingToolSnapshot(entries, "dark", 80), "\n")
	if len(rendered) != 2 {
		t.Fatalf("expected multiline pending shell preview capped to 2 lines, got %d (%q)", len(rendered), rendered)
	}
	plain := make([]string, 0, len(rendered))
	for _, line := range rendered {
		plain = append(plain, strings.TrimSpace(stripANSIPreserve(line)))
	}
	if plain[0] != "$ cat > /tmp/demo.txt <<'EOF'" {
		t.Fatalf("unexpected first collapsed line: %q", plain[0])
	}
	if plain[1] != "…" {
		t.Fatalf("expected ellipsis second line, got %q", plain[1])
	}
}

func TestNativePendingMultilineShellPreviewStaysTwoLinesWhenHeaderWraps(t *testing.T) {
	command := strings.Join([]string{
		"cat > /tmp/" + strings.Repeat("very-long-name-", 8) + "demo.txt <<'EOF'",
		"body line",
		"EOF",
	}, "\n")
	entries := []tui.TranscriptEntry{{
		Role:       "tool_call",
		Text:       command,
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: command},
	}}

	rendered := strings.Split(renderNativePendingToolSnapshot(entries, "dark", 28), "\n")
	if len(rendered) != 2 {
		t.Fatalf("expected wrapped multiline pending shell preview capped to 2 lines, got %d (%q)", len(rendered), rendered)
	}
	if got := strings.TrimSpace(stripANSIPreserve(rendered[1])); got != "…" {
		t.Fatalf("expected wrapped multiline pending shell preview second line to be ellipsis, got %q", rendered[1])
	}
}

func TestNativePendingCompletedMultilineShellPreviewStaysTwoLinesWithoutWaitingAnnotation(t *testing.T) {
	commandA := "echo a"
	commandB := strings.Join([]string{
		"cat > /tmp/demo.txt <<'EOF'",
		"body line",
		"EOF",
	}, "\n")
	entries := []tui.TranscriptEntry{
		{Role: "tool_call", Text: commandA, ToolCallID: "call_a", ToolCall: &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: commandA}},
		{Role: "tool_call", Text: commandB, ToolCallID: "call_b", ToolCall: &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: commandB}},
		{Role: "tool_result_ok", Text: "done", ToolCallID: "call_b"},
	}

	pending := nativePendingToolEntries(entries)
	if len(pending) != 2 {
		t.Fatalf("expected two pending tool entries, got %#v", pending)
	}

	rendered := strings.Split(renderNativePendingToolSnapshot(entries, "dark", 80), "\n")
	plain := make([]string, 0, len(rendered))
	for _, line := range rendered {
		plain = append(plain, strings.TrimSpace(stripANSIPreserve(line)))
	}
	joined := strings.Join(plain, "\n")
	if !strings.Contains(joined, "$ cat > /tmp/demo.txt <<'EOF'") {
		t.Fatalf("expected completed multiline pending shell preview header, got %q", plain)
	}
	if !strings.Contains(joined, "…") {
		t.Fatalf("expected ellipsis line in completed pending multiline preview, got %q", plain)
	}
	if strings.Contains(joined, "waiting") {
		t.Fatalf("did not expect waiting annotation in completed pending multiline preview, got %q", plain)
	}
}

func TestNativePendingToolCallStaysLiveUntilResultThenAppendsFinalBlock(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt once"}}),
	).(*uiModel)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}

	call := tui.TranscriptEntry{
		Role:       "tool_call",
		Text:       "pwd",
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "pwd"},
	}
	m.transcriptEntries = append(m.transcriptEntries, call)
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})
	m.syncViewport()
	if cmd := m.syncNativeHistoryFromTranscript(); cmd != nil {
		t.Fatalf("expected pending tool call to stay out of committed scrollback, got %T", cmd())
	}
	view := stripANSIPreserve(m.View())
	if !strings.Contains(view, "$ pwd") {
		t.Fatalf("expected pending tool call visible in native live region, got %q", view)
	}
	if strings.Contains(m.nativeRenderedSnapshot, "pwd") {
		t.Fatalf("expected pending tool call absent from committed snapshot, got %q", m.nativeRenderedSnapshot)
	}

	result := tui.TranscriptEntry{Role: "tool_result_ok", Text: "/tmp", ToolCallID: "call_1"}
	m.transcriptEntries = append(m.transcriptEntries, result)
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})
	m.syncViewport()
	cmd := m.syncNativeHistoryFromTranscript()
	if cmd == nil {
		t.Fatal("expected finalized tool block to append to native scrollback")
	}
	msg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
	plain := stripANSIText(msg.Text)
	if strings.Contains(plain, "prompt once") {
		t.Fatalf("expected tool completion delta without full replay, got %q", msg.Text)
	}
	if strings.Count(plain, "pwd") != 1 {
		t.Fatalf("expected finalized tool call emitted once, got %q", msg.Text)
	}
	if strings.Contains(plain, "/tmp") {
		t.Fatalf("did not expect native ongoing scrollback to start emitting shell output inline, got %q", msg.Text)
	}
	if cmd := m.syncNativeHistoryFromTranscript(); cmd != nil {
		t.Fatalf("expected no duplicate emission after finalized tool call flush, got %T", cmd())
	}
}

func TestNativeParallelToolCompletionWaitsForStablePrefixBeforeAppend(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt once"}}),
	).(*uiModel)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}

	entries := []tui.TranscriptEntry{
		{Role: "user", Text: "prompt once"},
		{Role: "tool_call", Text: "echo a", ToolCallID: "call_a", ToolCall: &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "echo a"}},
		{Role: "tool_call", Text: "echo b", ToolCallID: "call_b", ToolCall: &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "echo b"}},
		{Role: "tool_result_ok", Text: "out-b", ToolCallID: "call_b"},
	}
	m.transcriptEntries = entries
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})
	m.syncViewport()
	if cmd := m.syncNativeHistoryFromTranscript(); cmd != nil {
		t.Fatalf("expected no committed flush before first pending call resolves, got %T", cmd())
	}
	view := stripANSIPreserve(m.View())
	if !strings.Contains(view, "$ echo a") || !strings.Contains(view, "$ echo b") {
		t.Fatalf("expected pending rows to match committed shell preview formatting, got %q", view)
	}
	if strings.Contains(view, "waiting") {
		t.Fatalf("did not expect waiting annotation in live region, got %q", view)
	}

	m.transcriptEntries = append(m.transcriptEntries, tui.TranscriptEntry{Role: "tool_result_ok", Text: "out-a", ToolCallID: "call_a"})
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})
	m.syncViewport()
	cmd := m.syncNativeHistoryFromTranscript()
	if cmd == nil {
		t.Fatal("expected append once the stable prefix advances")
	}
	msg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
	plain := stripANSIText(msg.Text)
	if strings.Contains(plain, "prompt once") {
		t.Fatalf("expected delta append without prompt replay, got %q", msg.Text)
	}
	if strings.Count(plain, "echo a") != 1 || strings.Count(plain, "echo b") != 1 {
		t.Fatalf("expected both tool calls appended exactly once in order, got %q", msg.Text)
	}
	if strings.Index(plain, "echo a") > strings.Index(plain, "echo b") {
		t.Fatalf("expected parallel tool append to preserve declaration order, got %q", plain)
	}
	if strings.Contains(plain, "out-a") || strings.Contains(plain, "out-b") {
		t.Fatalf("did not expect shell outputs inline in ongoing scrollback delta, got %q", msg.Text)
	}
	postCommitView := stripANSIPreserve(m.View())
	if strings.Contains(postCommitView, "echo a") || strings.Contains(postCommitView, "echo b") {
		t.Fatalf("expected committed tool rows removed from volatile live region, got %q", postCommitView)
	}
	if cmd := m.syncNativeHistoryFromTranscript(); cmd != nil {
		t.Fatalf("expected no duplicate append after committing stable prefix, got %T", cmd())
	}
}

func TestUIInitClearsScreen(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected init command")
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

func TestNativeOngoingShrinksLiveRegionAfterInputShrinkWhenNotStreaming(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
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
	if len(second) >= firstLines {
		t.Fatalf("expected native live region to shrink after input shrink, got %d want < %d", len(second), firstLines)
	}
}

func TestNativeOngoingKeepsInputAndStatusAtBottomOfLiveRegion(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 12
	m.windowSizeKnown = true
	m.input = "hello"
	m.syncViewport()
	lines := strings.Split(stripANSIPreserve(m.View()), "\n")
	if len(lines) >= 12 || len(lines) < 2 {
		t.Fatalf("expected native ongoing view to render only compact live region, got %d lines", len(lines))
	}
	if !strings.Contains(lines[len(lines)-1], "ongoing") {
		t.Fatalf("expected status line at terminal bottom, got %q", lines[len(lines)-1])
	}
	start := 0
	if len(lines) > 5 {
		start = len(lines) - 5
	}
	windowTail := strings.Join(lines[start:], "\n")
	if !strings.Contains(windowTail, "› hello") {
		t.Fatalf("expected input region in bottom window tail, got %q", windowTail)
	}
}

func TestNativeOngoingDoesNotRenderBeforeWindowSizeKnown(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.input = "hello"
	got := stripANSIPreserve(m.View())
	if got != "" {
		t.Fatalf("expected no native ongoing render before first window size, got %q", got)
	}
}

func TestNativeOngoingRendersWhenTrimmedToHeight(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
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

func TestNativeOngoingClearsLiveRegionPadWhenStreamingEnds(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 12
	m.windowSizeKnown = true
	m.forwardToView(tui.SetConversationMsg{Ongoing: "line1\nline2"})
	m.syncViewport()
	if !m.nativeStreamingActive {
		t.Fatal("expected streaming active after ongoing stream snapshot")
	}
	m.forwardToView(tui.SetConversationMsg{Ongoing: ""})
	m.syncViewport()
	if m.nativeLiveRegionPad != 0 {
		t.Fatalf("expected no residual live region pad after streaming ends, got %d", m.nativeLiveRegionPad)
	}
	if m.nativeStreamingActive {
		t.Fatal("expected streaming inactive after ongoing clears")
	}
}

func TestNativeDeltaFlushForSingleLineUserMessageHasNoExtraBlankLine(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	).(*uiModel)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}

	entry := tui.TranscriptEntry{Role: "user", Text: "belissimo.commit"}
	m.forwardToView(tui.AppendTranscriptMsg{Role: entry.Role, Text: entry.Text})
	m.transcriptEntries = append(m.transcriptEntries, entry)
	cmd := m.syncNativeHistoryFromTranscript()
	if cmd == nil {
		t.Fatal("expected native delta flush command")
	}
	msg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
	plain := stripANSIPreserve(msg.Text)
	if strings.Contains(plain, "belissimo.commit\n\n") {
		t.Fatalf("expected no extra blank line after user message, got %q", plain)
	}
}

func TestNativeStreamingLinesHiddenWhenNotBusy(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 20
	m.windowSizeKnown = true
	m.forwardToView(tui.SetConversationMsg{Ongoing: "stale stream text"})
	m.busy = false
	view := stripANSIPreserve(m.View())
	if strings.Contains(view, "stale stream text") {
		t.Fatalf("expected stale streaming text hidden when not busy, got %q", view)
	}

	m.busy = true
	view = stripANSIPreserve(m.View())
	if !strings.Contains(view, "stale stream text") {
		t.Fatalf("expected streaming text visible while busy, got %q", view)
	}
}

func TestNativeStreamingLinesIncludeDividerAndAssistantPrefix(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "try again"}}),
	).(*uiModel)
	m.termWidth = 100
	m.termHeight = 24
	m.windowSizeKnown = true
	m.busy = true
	m.sawAssistantDelta = true
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries, Ongoing: "Second Stream Check"})
	m.syncViewport()

	plain := stripANSIPreserve(m.View())
	if !strings.Contains(plain, strings.Repeat("─", m.termWidth)) {
		t.Fatalf("expected streaming live region to include divider, got %q", plain)
	}
	if !strings.Contains(plain, "❮ Second Stream Check") {
		t.Fatalf("expected assistant prefix in streaming live region, got %q", plain)
	}
}

func TestNativeDeltaFlushDoesNotInsertBlankBeforeDivider(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "try again"}}),
	).(*uiModel)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}

	entry := tui.TranscriptEntry{Role: "assistant", Text: "Second Stream Check"}
	m.forwardToView(tui.AppendTranscriptMsg{Role: entry.Role, Text: entry.Text})
	m.transcriptEntries = append(m.transcriptEntries, entry)
	cmd := m.syncNativeHistoryFromTranscript()
	if cmd == nil {
		t.Fatal("expected native delta flush command")
	}
	msg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
	plain := stripANSIPreserve(msg.Text)
	if strings.HasPrefix(plain, "\n") {
		t.Fatalf("expected no leading blank line in delta flush, got %q", plain)
	}
	if strings.Contains(plain, "\n\n❮") {
		t.Fatalf("expected no blank line between divider and assistant line, got %q", plain)
	}
}

func TestNativePostCommitRedrawStableWithoutExtraBlankBeforeDivider(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "try again"}}),
	).(*uiModel)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}

	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "Second Stream Check"})
	preCommitView := stripANSIPreserve(m.View())
	if !strings.Contains(preCommitView, "❮ Second Stream Check") {
		t.Fatalf("expected live streaming assistant line before commit, got %q", preCommitView)
	}

	cmd := m.runtimeAdapter().applyChatSnapshot(runtime.ChatSnapshot{
		Entries: []runtime.ChatEntry{{Role: "user", Text: "try again"}, {Role: "assistant", Text: "Second Stream Check"}},
		Ongoing: "",
	})
	if cmd == nil {
		t.Fatal("expected native history flush command on commit snapshot")
	}
	flushMsg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
	flushPlain := stripANSIPreserve(flushMsg.Text)
	if strings.HasPrefix(flushPlain, "\n") {
		t.Fatalf("expected no leading blank line in commit delta flush, got %q", flushPlain)
	}
	if strings.Contains(flushPlain, "\n\n❮") {
		t.Fatalf("expected no blank line before assistant line in commit delta flush, got %q", flushPlain)
	}

	postCommitView := stripANSIPreserve(m.View())
	nextView := stripANSIPreserve(m.View())
	if postCommitView != nextView {
		t.Fatalf("expected stable post-commit live region across redraws\nfirst=%q\nsecond=%q", postCommitView, nextView)
	}
	if strings.Contains(postCommitView, "Second Stream Check") {
		t.Fatalf("expected live streaming lane to be cleared after commit, got %q", postCommitView)
	}
}

func TestNativeStreamingDividerPersistsInTightViewport(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt"}}),
	).(*uiModel)
	m.termWidth = 40
	m.termHeight = 6
	m.windowSizeKnown = true
	m.busy = true
	m.sawAssistantDelta = true
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries, Ongoing: "line1\nline2\nline3"})
	m.syncViewport()

	plain := stripANSIPreserve(m.View())
	if !strings.Contains(plain, strings.Repeat("─", m.termWidth)) {
		t.Fatalf("expected divider to remain visible in tight viewport, got %q", plain)
	}
	if !strings.Contains(plain, "❮ line1") {
		t.Fatalf("expected first streamed line to remain visible in tight viewport, got %q", plain)
	}
}

func TestNativeHistoryReplayDefersWhileDetailAndFlushesOnReturn(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	).(*uiModel)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}
	startupMsg, ok := startupCmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", startupCmd())
	}
	if !strings.Contains(stripANSIPreserve(startupMsg.Text), "seed") {
		t.Fatalf("expected startup replay to include seed, got %q", startupMsg.Text)
	}

	m.forwardToView(tui.ToggleModeMsg{})
	if m.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected detail mode, got %q", m.view.Mode())
	}

	steered := tui.TranscriptEntry{Role: "user", Text: "steered message"}
	m.transcriptEntries = append(m.transcriptEntries, steered)
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})
	if cmd := m.syncNativeHistoryFromTranscript(); cmd != nil {
		t.Fatalf("expected native replay to stay deferred while detail is active, got %T", cmd())
	}
	if strings.Contains(m.nativeRenderedSnapshot, "steered message") {
		t.Fatalf("expected rendered normal-buffer snapshot to remain stale while detail is active, got %q", m.nativeRenderedSnapshot)
	}

	m.forwardToView(tui.ToggleModeMsg{})
	if m.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected ongoing mode, got %q", m.view.Mode())
	}
	cmd := m.emitCurrentNativeHistorySnapshot(false)
	if cmd == nil {
		t.Fatal("expected deferred native replay when returning to ongoing")
	}
	flushMsg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
	plain := stripANSIPreserve(flushMsg.Text)
	if !strings.Contains(plain, "steered message") {
		t.Fatalf("expected deferred replay to include steered message, got %q", plain)
	}
	if strings.Contains(plain, "seed") {
		t.Fatalf("expected deferred replay to emit only the missing delta, got %q", plain)
	}
}
