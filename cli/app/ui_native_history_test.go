package app

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"builder/cli/tui"
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/tools"
	"builder/shared/clientui"
	"builder/shared/config"
	sharedtheme "builder/shared/theme"
	"builder/shared/transcript"
	"builder/shared/transcript/toolcodec"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

func stripANSIText(v string) string {
	return strings.Join(strings.Fields(xansi.Strip(v)), " ")
}

func stripANSIPreserve(v string) string {
	return xansi.Strip(v)
}

func pendingSpinnerFrame(frame int) string {
	if len(pendingToolSpinner.Frames) == 0 {
		return ""
	}
	index := frame % len(pendingToolSpinner.Frames)
	if index < 0 {
		index = 0
	}
	return strings.TrimSpace(pendingToolSpinner.Frames[index])
}

func TestNativeScrollbackStartupReplayIncludesFullTranscript(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{
			{Role: "user", Text: "first message"},
			{Role: "assistant", Text: "last message"},
		}),
	)

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

func TestNativeScrollbackStartupReplayRendersInterruptionAsUserFacingError(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: string(transcript.EntryRoleInterruption), Text: "User interrupted you"}}),
	)

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
	if strings.Contains(plain, "User interrupted you") {
		t.Fatalf("expected native replay to hide model-facing interruption wording, got %q", msg.Text)
	}
	if !strings.Contains(plain, "You interrupted") {
		t.Fatalf("expected native replay to show user-facing interruption wording, got %q", msg.Text)
	}
	tokens := sharedtheme.ResolvePalette(m.theme)
	expectedErrorText := lipgloss.NewStyle().Foreground(tokens.Transcript.Error.Lipgloss()).Render("You interrupted")
	if !strings.Contains(msg.Text, expectedErrorText) {
		t.Fatalf("expected native replay to use transcript error styling, got %q", msg.Text)
	}
}

func TestNativeScrollbackStartupReplayContinuesPastEmptyToolResult(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.transcriptEntries = []tui.TranscriptEntry{
		{Role: "user", Text: "before tool"},
		{Role: "tool_call", Text: "apply patch", ToolCallID: "call_patch", ToolCall: &transcript.ToolCallMeta{ToolName: "patch"}},
		{Role: "tool_result_ok", Text: "", ToolCallID: "call_patch"},
		{Role: "assistant", Text: "after empty result"},
	}
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})

	next, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected startup replay command")
	}
	msg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg after first window size, got %T", cmd())
	}
	plain := stripANSIText(msg.Text)
	if !strings.Contains(plain, "after empty result") {
		t.Fatalf("expected startup replay to continue past empty tool result, got %q", msg.Text)
	}
	if strings.Contains(plain, "tool_result_ok") {
		t.Fatalf("did not expect empty tool result entry to render, got %q", msg.Text)
	}
}

func TestNativeScrollbackStartupReplayKeepsPatchSuccessStateAfterEmptyToolResult(t *testing.T) {
	m := newProjectedStaticUIModel(WithUITheme("dark"))
	m.transcriptEntries = []tui.TranscriptEntry{
		{Role: "tool_call", Text: "apply patch", ToolCallID: "call_patch", ToolCall: &transcript.ToolCallMeta{ToolName: "patch", Command: "apply patch"}},
		{Role: "tool_result_ok", Text: "", ToolCallID: "call_patch"},
	}
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})

	_, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if cmd == nil {
		t.Fatal("expected startup replay command")
	}
	msg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg after first window size, got %T", cmd())
	}
	plain := stripANSIPreserve(msg.Text)
	if !strings.Contains(plain, "• apply patch") {
		t.Fatalf("expected patch replay to show tool call text, got %q", plain)
	}
	tokens := sharedtheme.ResolvePalette(m.theme)
	expectedSuccessBullet := lipgloss.NewStyle().Foreground(tokens.Transcript.ToolSuccess.Lipgloss()).Render("•")
	if !strings.Contains(msg.Text, expectedSuccessBullet) {
		t.Fatalf("expected patch replay to use success-colored bullet after empty result, got %q", msg.Text)
	}
}

func TestNativeScrollbackStartupEmptyConversationEmitsBlankScreenSpacer(t *testing.T) {
	m := newProjectedStaticUIModel()

	next, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	updated, ok := next.(*uiModel)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	m = updated
	if cmd == nil {
		t.Fatal("expected blank spacer command after first window size without transcript")
	}
	msg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg after first empty window size, got %T", cmd())
	}
	if !msg.AllowBlank {
		t.Fatal("expected blank spacer replay to allow whitespace-only flushes")
	}
	if got := strings.Count(msg.Text, "\n"); got != 30 {
		t.Fatalf("expected blank spacer to emit one empty screen worth of lines, got %d newlines", got)
	}
	if !m.nativeHistoryReplayed {
		t.Fatal("expected empty-history startup to mark native scrollback as replayed")
	}
	if m.nativeRenderedSnapshot != "" {
		t.Fatalf("expected empty-history startup to keep rendered history snapshot empty, got %q", m.nativeRenderedSnapshot)
	}
	if cmd := m.syncNativeHistoryFromTranscript(); cmd != nil {
		t.Fatalf("expected empty-history replay to emit spacer only once without resize, got %T", cmd())
	}
}

func TestNativeScrollbackEmitsOnlyNewTranscriptLines(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "old line"}}),
	)
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

func TestNativeScrollbackDoesNotReplaySameSessionNonAppendMutation(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt"}, {Role: "assistant", Text: "old line"}, {Role: "assistant", Text: "tail line"}}),
	)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}

	m.transcriptEntries[1].Text = "mutated line"
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})
	cmd := m.syncNativeHistoryFromTranscript()
	if cmd == nil {
		t.Fatal("expected same-session divergence to surface a transient error notice")
	}
	for _, msg := range collectCmdMessages(t, cmd) {
		if _, ok := msg.(nativeHistoryFlushMsg); ok {
			t.Fatalf("did not expect same-session divergence to replay normal-buffer history, got %+v", msg)
		}
	}
	if m.transientStatus != nativeHistoryDivergenceStatusMessage || m.transientStatusKind != uiStatusNoticeError {
		t.Fatalf("expected divergence status surfaced to user, got status=%q kind=%v", m.transientStatus, m.transientStatusKind)
	}
	if got := stripANSIText(m.nativeRenderedSnapshot); !strings.Contains(got, "mutated line") || strings.Contains(got, "old line") {
		t.Fatalf("expected rendered baseline rebased without replay, got %q", got)
	}
}

func TestNativeScrollbackRebasesWhenNoSharedPrefixExists(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "old line"}, {Role: "assistant", Text: "tail line"}}),
	)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}

	m.transcriptEntries = []tui.TranscriptEntry{{Role: "user", Text: "fresh root"}, {Role: "assistant", Text: "rewritten tail"}}
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})
	cmd := m.syncNativeHistoryFromTranscript()
	if cmd == nil {
		t.Fatal("expected same-session zero-prefix divergence to surface a transient error notice")
	}
	for _, msg := range collectCmdMessages(t, cmd) {
		if _, ok := msg.(nativeHistoryFlushMsg); ok {
			t.Fatalf("did not expect same-session zero-prefix divergence to replay scrollback, got %+v", msg)
		}
	}
	if got := m.nativeRenderedSnapshot; got != m.nativeProjection.Render(tui.TranscriptDivider) {
		t.Fatalf("expected zero-prefix divergence to update rendered snapshot baseline without replay, got %q", got)
	}

	m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "next answer"})
	m.transcriptEntries = append(m.transcriptEntries, tui.TranscriptEntry{Role: "assistant", Text: "next answer"})
	appendCmd := m.syncNativeHistoryFromTranscript()
	if appendCmd == nil {
		t.Fatal("expected future append to resume after silent zero-prefix rebase")
	}
	appendMsg, ok := appendCmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", appendCmd())
	}
	appendPlain := stripANSIText(appendMsg.Text)
	if !strings.Contains(appendPlain, "next answer") {
		t.Fatalf("expected resumed append to include new assistant turn, got %q", appendPlain)
	}
	if strings.Contains(appendPlain, "fresh root") || strings.Contains(appendPlain, "rewritten tail") {
		t.Fatalf("expected resumed append to exclude already rebuilt history, got %q", appendPlain)
	}
}

func TestNativeScrollbackResizeRebasesFormatterWidth(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "old line"}}),
	)
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
	previousNow := nativeResizeReplayNow
	nativeResizeReplayDebounce = time.Millisecond
	now := time.Unix(1, 0)
	nativeResizeReplayNow = func() time.Time { return now }
	t.Cleanup(func() {
		nativeResizeReplayDebounce = previousDebounce
		nativeResizeReplayNow = previousNow
	})

	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "old line"}}),
	)
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

	now = now.Add(500 * time.Microsecond)
	next, replayCmd := m.Update(nativeResizeReplayMsg{token: secondToken})
	m = next.(*uiModel)
	if replayCmd == nil {
		t.Fatal("expected latest resize replay token to stay deferred until quiet period elapses")
	}
	deferredMsgs := collectCmdMessages(t, replayCmd)
	if len(deferredMsgs) != 1 {
		t.Fatalf("expected deferred resize replay to reschedule exactly one timer, got %d message(s)", len(deferredMsgs))
	}
	deferred, ok := deferredMsgs[0].(nativeResizeReplayMsg)
	if !ok {
		t.Fatalf("expected deferred nativeResizeReplayMsg, got %T", deferredMsgs[0])
	}
	if deferred.token != secondToken {
		t.Fatalf("expected deferred resize replay token %d, got %d", secondToken, deferred.token)
	}

	now = now.Add(500 * time.Microsecond)
	next, replayCmd = m.Update(nativeResizeReplayMsg{token: secondToken})
	m = next.(*uiModel)
	if replayCmd == nil {
		t.Fatal("expected latest resize replay token to trigger full history replay after quiet period")
	}
	msgs := collectCmdMessages(t, replayCmd)
	if len(msgs) != 2 {
		t.Fatalf("expected clear-screen plus native history flush for resize replay, got %d message(s)", len(msgs))
	}
	flush, ok := msgs[1].(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg as second replay message, got %T", msgs[1])
	}
	if !strings.Contains(stripANSIText(flush.Text), "old line") {
		t.Fatalf("expected full resize replay to include existing transcript, got %q", flush.Text)
	}
	if m.nativeFormatterWidth != 100 {
		t.Fatalf("expected formatter width rebased to latest resize width 100, got %d", m.nativeFormatterWidth)
	}
}

func TestNativeHeightOnlyResizeDoesNotScheduleFullReplay(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}

	next, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(*uiModel)
	if cmd != nil {
		t.Fatalf("expected height-only resize to avoid full native replay scheduling, got %T", cmd)
	}
	if m.nativeResizeReplayToken != 0 {
		t.Fatalf("expected height-only resize to avoid changing replay token, got %d", m.nativeResizeReplayToken)
	}
}

func TestNativeResizeReplayInvalidatedAcrossModeSwitch(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	)
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
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt once"}}),
	)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}
	if len(m.transcriptEntries) != 1 {
		t.Fatalf("expected one committed transcript entry at start, got %d", len(m.transcriptEntries))
	}

	next, _ := m.Update(projectedRuntimeEventMsg(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "stream line"}))
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
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "line one"}, {Role: "assistant", Text: "line two"}}),
	)
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
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt once"}}),
	)
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
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "line 1"}}),
	)
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
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript(entries),
	)
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

func TestStyleNativeReplayDividersKeepsRawRuleLikeLinesAsContent(t *testing.T) {
	out := styleNativeReplayDividers("───\nbody", "dark", 10)
	lines := strings.Split(stripANSIPreserve(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected two lines, got %q", out)
	}
	if lines[0] != "───" {
		t.Fatalf("expected raw divider-like content preserved, got %q", lines[0])
	}
}

func TestRenderNativeScrollbackSnapshotPreservesAskQuestionStructuredAnswerText(t *testing.T) {
	out := renderNativeScrollbackSnapshot([]tui.TranscriptEntry{
		{Role: "tool_call", Text: "Choose scope?", ToolCallID: "call_ask", ToolCall: &transcript.ToolCallMeta{ToolName: "ask_question", Question: "Choose scope?", Suggestions: []string{"full", "Fast only"}, RecommendedOptionIndex: 1}},
		{Role: "tool_result_ok", Text: "ask result summary", ToolCallID: "call_ask"},
	}, "dark", 100)
	plain := stripANSIText(out)
	if !strings.Contains(plain, "Choose scope?") {
		t.Fatalf("expected ask question preserved, got %q", out)
	}
	if !strings.Contains(plain, "ask result summary") {
		t.Fatalf("expected ask result text preserved, got %q", out)
	}
	if strings.Contains(plain, "full") || strings.Contains(plain, "Fast only") {
		t.Fatalf("expected native ongoing snapshot to omit ask suggestions, got %q", out)
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
	if len(pending) != 3 {
		t.Fatalf("expected pending tool calls plus matching completed result, got %#v", pending)
	}
	if pending[0].ToolCallID != "call_a" || pending[0].Role != "tool_call" || pending[0].Text != "echo a" {
		t.Fatalf("unexpected first pending tool entry: %#v", pending[0])
	}
	if pending[1].ToolCallID != "call_b" || pending[1].Role != "tool_call" || pending[1].Text != "echo b" {
		t.Fatalf("unexpected second pending tool entry: %#v", pending[1])
	}
	if pending[2].ToolCallID != "call_b" || pending[2].Role != "tool_result_ok" || pending[2].Text != "out-b" {
		t.Fatalf("unexpected pending tool result entry: %#v", pending[2])
	}

	rendered := renderNativePendingToolSnapshot(entries, "dark", 40, 0)
	plain := stripANSIPreserve(rendered)
	if !strings.Contains(plain, pendingSpinnerFrame(0)+" echo a") {
		t.Fatalf("expected first pending tool preview in live region, got %q", plain)
	}
	if !strings.Contains(plain, "$ echo b") {
		t.Fatalf("expected completed pending call to use its final symbol, got %q", plain)
	}
	if strings.Contains(plain, pendingSpinnerFrame(0)+" echo b") {
		t.Fatalf("did not expect completed pending call to keep spinner state, got %q", plain)
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

	rendered := strings.Split(renderNativePendingToolSnapshot(entries, "dark", 80, 0), "\n")
	if len(rendered) != 1 {
		t.Fatalf("expected multiline pending shell preview collapsed to one truncated line, got %d (%q)", len(rendered), rendered)
	}
	plain := make([]string, 0, len(rendered))
	for _, line := range rendered {
		plain = append(plain, strings.TrimSpace(stripANSIPreserve(line)))
	}
	if !strings.HasPrefix(plain[0], pendingSpinnerFrame(0)+" cat > /tmp/demo.txt <<'EOF") {
		t.Fatalf("unexpected first collapsed line: %q", plain[0])
	}
	if !strings.HasSuffix(plain[0], "…") {
		t.Fatalf("expected inline ellipsis on truncated pending shell preview, got %q", plain[0])
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

	rendered := strings.Split(renderNativePendingToolSnapshot(entries, "dark", 28, 0), "\n")
	if len(rendered) != 1 {
		t.Fatalf("expected wrapped multiline pending shell preview collapsed to one truncated line, got %d (%q)", len(rendered), rendered)
	}
	if got := strings.TrimSpace(stripANSIPreserve(rendered[0])); !strings.HasPrefix(got, pendingSpinnerFrame(0)+" ") {
		t.Fatalf("expected wrapped multiline pending shell preview to use spinner icon, got %q", rendered[0])
	}
	if got := strings.TrimSpace(stripANSIPreserve(rendered[0])); !strings.HasSuffix(got, "…") {
		t.Fatalf("expected wrapped multiline pending shell preview first line to end with ellipsis, got %q", rendered[0])
	}
}

func TestNativePendingWebSearchPreviewUsesAtPrefixAndVerboseQuery(t *testing.T) {
	entries := []tui.TranscriptEntry{{
		Role:       "tool_call",
		Text:       `web search: "latest golang release"`,
		ToolCallID: "call_web",
		ToolCall: &transcript.ToolCallMeta{
			ToolName:    "web_search",
			Command:     `web search: "latest golang release"`,
			CompactText: `web search: "latest golang release"`,
		},
	}}

	rendered := renderNativePendingToolSnapshot(entries, "dark", 80, 0)
	plain := stripANSIPreserve(rendered)
	if !strings.Contains(plain, pendingSpinnerFrame(0)+` web search: "latest golang release"`) {
		t.Fatalf("expected pending web search preview to use spinner and verbose query, got %q", plain)
	}
	if strings.Contains(plain, `web search: invalid query`) {
		t.Fatalf("did not expect invalid query fallback for valid web search preview, got %q", plain)
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
	if len(pending) != 3 {
		t.Fatalf("expected pending tool calls plus matching completed result, got %#v", pending)
	}

	rendered := strings.Split(renderNativePendingToolSnapshot(entries, "dark", 80, 0), "\n")
	plain := make([]string, 0, len(rendered))
	for _, line := range rendered {
		plain = append(plain, strings.TrimSpace(stripANSIPreserve(line)))
	}
	joined := strings.Join(plain, "\n")
	if !strings.Contains(joined, "$ cat > /tmp/demo.txt <<'EOF") {
		t.Fatalf("expected completed multiline pending shell preview header, got %q", plain)
	}
	if !strings.Contains(joined, "…") {
		t.Fatalf("expected ellipsis line in completed pending multiline preview, got %q", plain)
	}
	if strings.Contains(joined, "waiting") {
		t.Fatalf("did not expect waiting annotation in completed pending multiline preview, got %q", plain)
	}
}

func TestNativeScrollbackReviewerUsesSectionSignPrefix(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "reviewer_status", Text: "Supervisor ran: 2 suggestions, no changes applied."}}),
	)

	_, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if cmd == nil {
		t.Fatal("expected startup replay command")
	}
	msg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
	plain := stripANSIPreserve(msg.Text)
	if !strings.Contains(plain, "§ Supervisor ran: 2 suggestions, no changes applied.") {
		t.Fatalf("expected reviewer replay to use section-sign prefix, got %q", plain)
	}
	if strings.Contains(plain, "@ Supervisor ran: 2 suggestions, no changes applied.") {
		t.Fatalf("did not expect reviewer replay to keep @ prefix, got %q", plain)
	}
}

func TestNativeScrollbackWarningStaysHiddenFromOngoingReplayAndShowsInDetail(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUITheme("dark"),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "warning", Text: "Heads-up warning text."}}),
	)

	_, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if cmd == nil {
		t.Fatal("expected startup replay command")
	}
	msg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
	plain := stripANSIPreserve(msg.Text)
	if strings.Contains(plain, "Heads-up warning text.") {
		t.Fatalf("did not expect warning in ongoing native replay, got %q", plain)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	detail := stripANSIPreserve(next.(*uiModel).View())
	if !strings.Contains(detail, "⚠ Heads-up warning text.") {
		t.Fatalf("expected warning with caution prefix in detail view, got %q", detail)
	}
}

func TestNativeScrollbackCommittedWebSearchUsesAtPrefixAndVerboseQuery(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.transcriptEntries = []tui.TranscriptEntry{{
		Role:       "tool_call",
		Text:       `web search: "latest golang release"`,
		ToolCallID: "call_web",
		ToolCall: &transcript.ToolCallMeta{
			ToolName:    "web_search",
			Command:     `web search: "latest golang release"`,
			CompactText: `web search: "latest golang release"`,
		},
	}, {
		Role:       "tool_result_ok",
		Text:       "done",
		ToolCallID: "call_web",
	}}
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})
	m.nativeHistoryReplayed = true
	m.windowSizeKnown = true
	m.termWidth = 100
	m.termHeight = 20
	cmd := m.syncNativeHistoryFromTranscript()
	if cmd == nil {
		t.Fatal("expected committed replay command")
	}
	msg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
	plain := stripANSIPreserve(msg.Text)
	if !strings.Contains(plain, `@ web search: "latest golang release"`) {
		t.Fatalf("expected committed web search replay to use @ prefix and verbose query, got %q", plain)
	}
	tokens := sharedtheme.ResolvePalette(m.theme)
	expectedSuccessPrefix := lipgloss.NewStyle().Foreground(tokens.Transcript.ToolSuccess.Lipgloss()).Render("@")
	if !strings.Contains(msg.Text, expectedSuccessPrefix) {
		t.Fatalf("expected committed web search replay to use success-colored prefix, got %q", msg.Text)
	}
}

func TestNativePendingCompletedErrorToolKeepsFinalStateWithoutSpinner(t *testing.T) {
	entries := []tui.TranscriptEntry{
		{Role: "tool_call", Text: "echo a", ToolCallID: "call_a", ToolCall: &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "echo a"}},
		{Role: "tool_call", Text: "exit 1", ToolCallID: "call_b", ToolCall: &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "exit 1"}},
		{Role: "tool_result_error", Text: "failed", ToolCallID: "call_b"},
	}

	rendered := renderNativePendingToolSnapshot(entries, "dark", 80, 0)
	plain := stripANSIPreserve(rendered)
	if !strings.Contains(plain, pendingSpinnerFrame(0)+" echo a") {
		t.Fatalf("expected unresolved pending call to keep spinner, got %q", plain)
	}
	if !strings.Contains(plain, "$ exit 1") {
		t.Fatalf("expected completed error call to use final shell symbol, got %q", plain)
	}
	if strings.Contains(plain, pendingSpinnerFrame(0)+" exit 1") {
		t.Fatalf("did not expect completed error call to keep spinner state, got %q", plain)
	}
	if strings.Contains(plain, "waiting") {
		t.Fatalf("did not expect waiting annotation in mixed pending/error preview, got %q", plain)
	}
}

func TestNativePendingToolCallStaysLiveUntilResultThenAppendsFinalBlock(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt once"}}),
	)
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
	if !strings.Contains(view, pendingSpinnerFrame(0)+" pwd") {
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

func TestNativePendingToolPreviewUsesBubbleTeaDotSpinnerWithoutCommittingScrollback(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt once"}}),
	)
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

	m.spinnerFrame = 0
	view0 := stripANSIPreserve(m.View())
	m.spinnerFrame = 1
	view1 := stripANSIPreserve(m.View())

	if !strings.Contains(view0, pendingSpinnerFrame(0)) {
		t.Fatalf("expected pending tool preview to use Bubble Tea Dot frame 0, got %q", view0)
	}
	if !strings.Contains(view1, pendingSpinnerFrame(1)) {
		t.Fatalf("expected pending tool preview to use Bubble Tea Dot frame 1, got %q", view1)
	}
	if view0 == view1 {
		t.Fatalf("expected pending tool preview to animate across frames, got %q", view0)
	}
	if strings.Contains(m.nativeRenderedSnapshot, "pwd") {
		t.Fatalf("expected pending tool spinner animation to leave committed scrollback untouched, got %q", m.nativeRenderedSnapshot)
	}
}

func TestNativeParallelToolCompletionWaitsForStablePrefixBeforeAppend(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt once"}}),
	)
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
	if !strings.Contains(view, pendingSpinnerFrame(0)+" echo a") || !strings.Contains(view, "$ echo b") {
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

func TestProjectedRuntimeBatchesPreserveImmediateLiveEventsAndLaterCommittedAppend(t *testing.T) {
	m := newProjectedTestUIModel(nil, closedProjectedRuntimeEvents(), nil,
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	)
	next, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = next.(*uiModel)
	_ = collectCmdMessages(t, startupCmd)

	callMeta := transcript.ToolCallMeta{ToolName: "shell", Command: "pwd", CompactText: "pwd", IsShell: true}
	firstBatch := []clientui.Event{
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventUserMessageFlushed, StepID: "step-1", UserMessage: "say hi"}),
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventReviewerCompleted, StepID: "step-1", Reviewer: &runtime.ReviewerStatus{Outcome: "applied", SuggestionsCount: 2}}),
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventBackgroundUpdated, StepID: "step-1", Background: &runtime.BackgroundShellEvent{Type: "completed", ID: "1000", State: "completed", NoticeText: "Background shell 1000 completed.\nOutput:\nhello", CompactText: "Background shell 1000 completed"}}),
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventToolCallStarted, StepID: "step-1", ToolCall: &llm.ToolCall{ID: "call-1", Name: string(tools.ToolShell), Presentation: toolcodec.EncodeToolCallMeta(callMeta)}}),
	}
	next, cmd := m.Update(runtimeEventBatchMsg{events: firstBatch})
	m = next.(*uiModel)
	msgs := collectCmdMessages(t, cmd)
	flushText := strings.Builder{}
	for _, msg := range msgs {
		if flush, ok := msg.(nativeHistoryFlushMsg); ok {
			flushText.WriteString(stripANSIPreserve(flush.Text))
			flushText.WriteString("\n")
		}
	}
	if !containsInOrder(flushText.String(), "say hi", "Supervisor ran", "Background shell 1000 completed") {
		t.Fatalf("expected first batch committed flush to preserve event order, got %q", flushText.String())
	}
	view := stripANSIPreserve(m.View())
	if !strings.Contains(view, "pwd") {
		t.Fatalf("expected pending tool call visible immediately in ongoing mode, got %q", view)
	}

	secondBatch := []clientui.Event{
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventToolCallCompleted, StepID: "step-1", ToolResult: &tools.Result{CallID: "call-1", Name: tools.ToolShell, Output: []byte("/tmp")}}),
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantMessage, StepID: "step-1", Message: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal}}),
	}
	next, cmd = m.Update(runtimeEventBatchMsg{events: secondBatch})
	m = next.(*uiModel)
	msgs = collectCmdMessages(t, cmd)
	flushText.Reset()
	for _, msg := range msgs {
		if flush, ok := msg.(nativeHistoryFlushMsg); ok {
			flushText.WriteString(stripANSIPreserve(flush.Text))
			flushText.WriteString("\n")
		}
	}
	if !containsInOrder(flushText.String(), "pwd", "done") {
		t.Fatalf("expected later committed append after tool completion, got %q", flushText.String())
	}
	view = stripANSIPreserve(m.View())
	if strings.Contains(view, "pwd") {
		t.Fatalf("expected pending tool preview cleared after completion, got %q", view)
	}
}

func TestProjectedRuntimeBatchPreservesQueuedUserFlushBetweenToolCompletionAndAssistantFinal(t *testing.T) {
	m := newProjectedTestUIModel(nil, closedProjectedRuntimeEvents(), nil,
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	)
	next, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = next.(*uiModel)
	_ = collectCmdMessages(t, startupCmd)

	callMeta := transcript.ToolCallMeta{ToolName: "shell", Command: "pwd", CompactText: "pwd", IsShell: true}
	firstBatch := []clientui.Event{
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventUserMessageFlushed, StepID: "step-1", UserMessage: "say hi"}),
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventToolCallStarted, StepID: "step-1", ToolCall: &llm.ToolCall{ID: "call-1", Name: string(tools.ToolShell), Presentation: toolcodec.EncodeToolCallMeta(callMeta)}}),
	}
	next, cmd := m.Update(runtimeEventBatchMsg{events: firstBatch})
	m = next.(*uiModel)
	_ = collectCmdMessages(t, cmd)

	secondBatch := []clientui.Event{
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventToolCallCompleted, StepID: "step-1", ToolResult: &tools.Result{CallID: "call-1", Name: tools.ToolShell, Output: []byte("/tmp")}}),
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventUserMessageFlushed, StepID: "step-1", UserMessage: "steer now"}),
		projectRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantMessage, StepID: "step-1", Message: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal}}),
	}
	next, cmd = m.Update(runtimeEventBatchMsg{events: secondBatch})
	m = next.(*uiModel)
	msgs := collectCmdMessages(t, cmd)
	flushText := strings.Builder{}
	for _, msg := range msgs {
		if flush, ok := msg.(nativeHistoryFlushMsg); ok {
			flushText.WriteString(stripANSIPreserve(flush.Text))
			flushText.WriteString("\n")
		}
	}
	if !containsInOrder(flushText.String(), "pwd", "steer now", "done") {
		t.Fatalf("expected queued user flush preserved between tool completion and assistant final, got %q", flushText.String())
	}
	view := stripANSIPreserve(m.View())
	if strings.Contains(view, "pwd") {
		t.Fatalf("expected pending tool preview cleared after completion, got %q", view)
	}
}

func TestUIInitClearsScreen(t *testing.T) {
	m := newProjectedStaticUIModel()
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
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "line 1\nline 2\nline 3"
	m.syncViewport()
	firstPad := m.nativeLiveRegionPad
	first := strings.Split(m.View(), "\n")
	if len(first) != 20 {
		t.Fatalf("expected fresh conversation to fill terminal height before shrink, got %d lines", len(first))
	}
	m.input = ""
	m.syncViewport()
	secondPad := m.nativeLiveRegionPad
	second := strings.Split(m.View(), "\n")
	if len(second) != 20 {
		t.Fatalf("expected fresh conversation to keep filling terminal height after shrink, got %d lines", len(second))
	}
	if secondPad <= firstPad {
		t.Fatalf("expected top padding to grow after input shrink, first=%d second=%d", firstPad, secondPad)
	}
}

func TestNativeOngoingKeepsInputAndStatusAtBottomOfLiveRegion(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 12
	m.windowSizeKnown = true
	m.input = "hello"
	m.syncViewport()
	lines := strings.Split(stripANSIPreserve(m.View()), "\n")
	if len(lines) != 12 {
		t.Fatalf("expected fresh conversation to fill full terminal height, got %d lines", len(lines))
	}
	if !strings.Contains(lines[len(lines)-1], "ongoing") {
		t.Fatalf("expected status line at terminal bottom, got %q", lines[len(lines)-1])
	}
	if strings.TrimSpace(lines[0]) != "" {
		t.Fatalf("expected top of fresh conversation live region to stay blank, got %q", lines[0])
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
	m := newProjectedStaticUIModel()
	m.input = "hello"
	got := stripANSIPreserve(m.View())
	if got != "" {
		t.Fatalf("expected no native ongoing render before first window size, got %q", got)
	}
}

func TestNativeOngoingRendersWhenTrimmedToHeight(t *testing.T) {
	m := newProjectedStaticUIModel()
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
	m := newProjectedStaticUIModel()
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
	if m.nativeLiveRegionPad <= 0 {
		t.Fatalf("expected fresh conversation to restore top padding after streaming ends, got %d", m.nativeLiveRegionPad)
	}
	if m.nativeStreamingActive {
		t.Fatal("expected streaming inactive after ongoing clears")
	}
}

func TestNativeDeltaFlushForSingleLineUserMessageHasNoExtraBlankLine(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	)
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
	m := newProjectedStaticUIModel()
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
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "try again"}}),
	)
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

func TestNativeStreamingLinesKeepAssistantMarkdownPlain(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "try again"}}),
	)
	m.termWidth = 100
	m.termHeight = 24
	m.windowSizeKnown = true
	m.busy = true
	m.sawAssistantDelta = true
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries, Ongoing: "**hello**\n`world`"})
	m.syncViewport()

	raw := m.View()
	plain := stripANSIPreserve(raw)
	if !strings.Contains(plain, "**hello**") || !strings.Contains(plain, "`world`") {
		t.Fatalf("expected markdown markers preserved in live region while streaming, got %q", plain)
	}
	if !strings.Contains(plain, "❮ **hello**") {
		t.Fatalf("expected plain assistant text in live region, got %q", plain)
	}
	if strings.Contains(raw, "\x1b[") && !strings.Contains(raw, "\x1b[?25l") {
		t.Fatalf("expected live region to avoid rich markdown styling escapes, got raw=%q", raw)
	}
}

func TestNativeStreamingLinesPrefixOnlyFirstWrappedChunk(t *testing.T) {
	rendered := renderNativeStreamingAssistantLines(
		"This streaming line is intentionally long enough to wrap in the ongoing live region.",
		"dark",
		20,
	)
	if len(rendered) < 2 {
		t.Fatalf("expected wrapped streaming output, got %q", rendered)
	}
	if !strings.HasPrefix(rendered[0], "❮ ") {
		t.Fatalf("expected first wrapped chunk to keep assistant prefix, got %q", rendered[0])
	}
	for idx := 1; idx < len(rendered); idx++ {
		if !strings.HasPrefix(rendered[idx], "  ") {
			t.Fatalf("expected wrapped continuation to stay indented, got %q", rendered[idx])
		}
		if strings.HasPrefix(rendered[idx], "❮ ") {
			t.Fatalf("expected assistant prefix only on first wrapped chunk, got %q", rendered[idx])
		}
	}
}

func TestNativeDeltaFlushDoesNotInsertBlankBeforeDivider(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "try again"}}),
	)
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
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "try again"}}),
	)
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
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt"}}),
	)
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

func TestNativeHistoryFlushWaitsForTargetSequenceBeforeRearmingRuntimeEvents(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.pendingRuntimeEvents = []clientui.Event{{Kind: clientui.EventConversationUpdated}}
	m.waitRuntimeEventAfterFlushSequence = 2

	firstCmd := m.handleNativeHistoryFlush(nativeHistoryFlushMsg{Text: "first", Sequence: 1})
	if m.waitRuntimeEventAfterFlushSequence != 2 {
		t.Fatalf("expected runtime-event wait to remain armed for sequence 2, got %d", m.waitRuntimeEventAfterFlushSequence)
	}
	if got := len(m.pendingRuntimeEvents); got != 1 {
		t.Fatalf("expected pending runtime events preserved before target flush, got %d", got)
	}
	for _, msg := range collectCmdMessages(t, firstCmd) {
		if _, ok := msg.(runtimeEventBatchMsg); ok {
			t.Fatalf("did not expect runtime rearm before target flush, got %T", msg)
		}
	}

	secondCmd := m.handleNativeHistoryFlush(nativeHistoryFlushMsg{Text: "second", Sequence: 2})
	if secondCmd == nil {
		t.Fatal("expected target flush to rearm runtime events")
	}
	var rearmed runtimeEventBatchMsg
	foundRearm := false
	for _, msg := range collectCmdMessages(t, secondCmd) {
		batch, ok := msg.(runtimeEventBatchMsg)
		if !ok {
			continue
		}
		rearmed = batch
		foundRearm = true
	}
	if !foundRearm {
		t.Fatal("expected runtime event batch after target flush")
	}
	if got := len(rearmed.events); got != 1 {
		t.Fatalf("expected exactly one rearmed pending runtime event, got %d", got)
	}
	if got := rearmed.events[0].Kind; got != clientui.EventConversationUpdated {
		t.Fatalf("rearmed event kind = %q, want %q", got, clientui.EventConversationUpdated)
	}
	if m.waitRuntimeEventAfterFlushSequence != 0 {
		t.Fatalf("expected runtime-event wait cleared after target flush, got %d", m.waitRuntimeEventAfterFlushSequence)
	}
	if got := len(m.pendingRuntimeEvents); got != 0 {
		t.Fatalf("expected pending runtime events drained after target flush, got %d", got)
	}
}

func TestNativeHistoryReplayDefersWhileDetailAndFlushesOnReturn(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	)
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
	cmd := m.emitCurrentNativeHistorySnapshot(false, nativeHistoryReplayPermitNone)
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
}

func TestNativeHistorySnapshotDoesNotReplaySameSessionRewriteInOngoingMode(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.windowSizeKnown = true
	initial := tui.TranscriptProjection{Blocks: []tui.TranscriptProjectionBlock{
		{Role: "user", DividerGroup: "user", Lines: []string{"❯ commit/push"}},
		{Role: "assistant", DividerGroup: "assistant", Lines: []string{"❮ before"}},
	}}
	m.nativeProjection = initial
	m.nativeRenderedProjection = initial
	m.nativeRenderedSnapshot = initial.Render(tui.TranscriptDivider)
	m.nativeProjection = tui.TranscriptProjection{Blocks: []tui.TranscriptProjectionBlock{
		{Role: "user", DividerGroup: "user", Lines: []string{"❯ commit/push"}},
		{Role: "assistant", DividerGroup: "assistant", Lines: []string{"❮ after"}},
	}}

	cmd := m.emitCurrentNativeHistorySnapshot(false, nativeHistoryReplayPermitNone)
	if cmd == nil {
		t.Fatal("expected same-session rewrite to surface a transient error notice")
	}
	for _, msg := range collectCmdMessages(t, cmd) {
		if _, ok := msg.(nativeHistoryFlushMsg); ok {
			t.Fatalf("did not expect same-session rewrite to replay committed scrollback, got %+v", msg)
		}
	}
	if got := m.nativeRenderedSnapshot; got != m.nativeProjection.Render(tui.TranscriptDivider) {
		t.Fatalf("expected rendered snapshot updated without replay, got %q", got)
	}
}

func TestNativeScrollbackResumesAssistantFlushesAfterSameSessionRebase(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "commit/push"}, {Role: "assistant", Text: "before"}}),
	)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}

	m.transcriptEntries[1].Text = "after"
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})
	recoveryCmd := m.syncNativeHistoryFromTranscript()
	if recoveryCmd == nil {
		t.Fatal("expected same-session rewrite to surface a transient error notice")
	}
	for _, msg := range collectCmdMessages(t, recoveryCmd) {
		if _, ok := msg.(nativeHistoryFlushMsg); ok {
			t.Fatalf("did not expect same-session rewrite to replay committed history, got %+v", msg)
		}
	}
	if plain := stripANSIText(m.nativeRenderedSnapshot); !strings.Contains(plain, "commit/push") || !strings.Contains(plain, "after") || strings.Contains(plain, "before") {
		t.Fatalf("expected same-session rewrite to update rendered baseline without replay, got %q", plain)
	}

	m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "next answer"})
	m.transcriptEntries = append(m.transcriptEntries, tui.TranscriptEntry{Role: "assistant", Text: "next answer"})
	appendCmd := m.syncNativeHistoryFromTranscript()
	if appendCmd == nil {
		t.Fatal("expected native history append to resume after recovery")
	}
	appendMsg, ok := appendCmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", appendCmd())
	}
	appendPlain := stripANSIText(appendMsg.Text)
	if !strings.Contains(appendPlain, "next answer") {
		t.Fatalf("expected resumed append to include new assistant turn, got %q", appendPlain)
	}
	if strings.Contains(appendPlain, "commit/push") || strings.Contains(appendPlain, "after") {
		t.Fatalf("expected resumed append to exclude already rebased history, got %q", appendPlain)
	}
}

func TestNativeDetailExitReplaysCommittedTranscriptWhenDetailChangedState(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenNever),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}
	_ = collectCmdMessages(t, startupCmd)

	enterCmd := m.toggleTranscriptModeWithNativeReplay(false)
	if m.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected detail mode, got %q", m.view.Mode())
	}
	_ = collectCmdMessages(t, enterCmd)

	cmd := m.runtimeAdapter().applyChatSnapshot(runtime.ChatSnapshot{
		Entries: []runtime.ChatEntry{{Role: "user", Text: "fresh root"}, {Role: "assistant", Text: "rewritten tail"}},
	})
	if cmd != nil {
		t.Fatalf("expected replay to stay deferred while detail is active, got %T", cmd())
	}

	leaveCmd := m.toggleTranscriptModeWithNativeReplay(true)
	if m.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected ongoing mode, got %q", m.view.Mode())
	}
	if leaveCmd == nil {
		t.Fatal("expected detail exit to restore committed normal-buffer history")
	}
	msgs := collectCmdMessages(t, leaveCmd)
	if len(msgs) != 2 {
		t.Fatalf("expected clear-screen plus native history flush for detail exit restore, got %d message(s)", len(msgs))
	}
	flush, ok := msgs[1].(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg as detail-exit replay payload, got %T", msgs[1])
	}
	plainReplay := stripANSIText(flush.Text)
	if !strings.Contains(plainReplay, "fresh root") || !strings.Contains(plainReplay, "rewritten tail") || strings.Contains(plainReplay, "seed") {
		t.Fatalf("expected detail exit replay to emit authoritative transcript, got %q", plainReplay)
	}
	plain := stripANSIText(m.nativeRenderedSnapshot)
	if !strings.Contains(plain, "fresh root") || !strings.Contains(plain, "rewritten tail") {
		t.Fatalf("expected detail exit restore to update rendered baseline, got %q", plain)
	}
	if strings.Contains(plain, "seed") {
		t.Fatalf("expected detail exit restore to discard stale transcript root from local baseline, got %q", plain)
	}

	m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "next answer"})
	m.transcriptEntries = append(m.transcriptEntries, tui.TranscriptEntry{Role: "assistant", Text: "next answer"})
	appendCmd := m.syncNativeHistoryFromTranscript()
	if appendCmd == nil {
		t.Fatal("expected future append to resume after zero-prefix detail exit rebase")
	}
	appendMsg, ok := appendCmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", appendCmd())
	}
	appendPlain := stripANSIText(appendMsg.Text)
	if !strings.Contains(appendPlain, "next answer") {
		t.Fatalf("expected resumed append after detail exit, got %q", appendPlain)
	}
	if strings.Contains(appendPlain, "fresh root") || strings.Contains(appendPlain, "rewritten tail") {
		t.Fatalf("expected resumed append to exclude already rebuilt transcript root, got %q", appendPlain)
	}
}

func TestNativeScrollbackPanicsInDebugModeOnSameSessionDivergence(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIDebug(true),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "commit/push"}, {Role: "assistant", Text: "before"}}),
	)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected debug-mode panic on same-session divergence")
		}
		if !strings.Contains(r.(string), "same-session committed transcript divergence requires root-cause fix") {
			t.Fatalf("unexpected debug-mode panic: %v", r)
		}
	}()

	m.transcriptEntries[1].Text = "after"
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})
	_ = m.syncNativeHistoryFromTranscript()
}

func TestNativeHistorySnapshotReplaysDuringContinuityRecovery(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.windowSizeKnown = true
	initial := tui.TranscriptProjection{Blocks: []tui.TranscriptProjectionBlock{
		{Role: "user", DividerGroup: "user", Lines: []string{"❯ commit/push"}},
		{Role: "assistant", DividerGroup: "assistant", Lines: []string{"❮ before"}},
	}}
	m.nativeProjection = initial
	m.nativeRenderedProjection = initial
	m.nativeRenderedSnapshot = initial.Render(tui.TranscriptDivider)
	m.nativeHistoryReplayPermit = nativeHistoryReplayPermitContinuityRecovery
	m.nativeProjection = tui.TranscriptProjection{Blocks: []tui.TranscriptProjectionBlock{
		{Role: "user", DividerGroup: "user", Lines: []string{"❯ commit/push"}},
		{Role: "assistant", DividerGroup: "assistant", Lines: []string{"❮ after"}},
	}}

	cmd := m.emitCurrentNativeHistorySnapshot(false, nativeHistoryReplayPermitContinuityRecovery)
	if cmd == nil {
		t.Fatal("expected continuity recovery to replay committed scrollback")
	}
	msgs := collectCmdMessages(t, cmd)
	if len(msgs) != 2 {
		t.Fatalf("expected clear-screen plus native history flush during continuity recovery, got %d message(s)", len(msgs))
	}
	msg, ok := msgs[1].(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg as replay payload, got %T", msgs[1])
	}
	plain := stripANSIPreserve(msg.Text)
	if !strings.Contains(plain, "commit/push") || !strings.Contains(plain, "after") || strings.Contains(plain, "before") {
		t.Fatalf("expected continuity recovery replay to emit authoritative transcript, got %q", plain)
	}
}

func TestNativeHistorySnapshotReplaysDuringAuthoritativeHydrateRepair(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.windowSizeKnown = true
	initial := tui.TranscriptProjection{Blocks: []tui.TranscriptProjectionBlock{
		{Role: "user", DividerGroup: "user", Lines: []string{"❯ commit/push"}},
		{Role: "assistant", DividerGroup: "assistant", Lines: []string{"❮ before"}},
	}}
	m.nativeProjection = initial
	m.nativeRenderedProjection = initial
	m.nativeRenderedSnapshot = initial.Render(tui.TranscriptDivider)
	m.nativeProjection = tui.TranscriptProjection{Blocks: []tui.TranscriptProjectionBlock{
		{Role: "user", DividerGroup: "user", Lines: []string{"❯ commit/push"}},
		{Role: "assistant", DividerGroup: "assistant", Lines: []string{"❮ after"}},
	}}

	cmd := m.emitCurrentNativeHistorySnapshot(false, nativeHistoryReplayPermitAuthoritativeHydrate)
	if cmd == nil {
		t.Fatal("expected authoritative hydrate repair to replay committed scrollback")
	}
	msgs := collectCmdMessages(t, cmd)
	if len(msgs) != 3 {
		t.Fatalf("expected status timer plus clear-screen plus native history flush for authoritative hydrate repair, got %d message(s)", len(msgs))
	}
	flush, ok := msgs[2].(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg as authoritative hydrate replay payload, got %T", msgs[2])
	}
	plain := stripANSIPreserve(flush.Text)
	if !strings.Contains(plain, "commit/push") || !strings.Contains(plain, "after") || strings.Contains(plain, "before") {
		t.Fatalf("expected authoritative hydrate replay to emit corrected transcript, got %q", plain)
	}
	if m.transientStatus != nativeHistoryDivergenceStatusMessage || m.transientStatusKind != uiStatusNoticeError {
		t.Fatalf("expected authoritative hydrate repair to surface divergence status, got status=%q kind=%v", m.transientStatus, m.transientStatusKind)
	}
}

func TestNativeHistorySnapshotAuthoritativeHydrateRepairDoesNotPanicInDebugMode(t *testing.T) {
	m := newProjectedStaticUIModel(WithUIDebug(true))
	m.termWidth = 80
	m.windowSizeKnown = true
	initial := tui.TranscriptProjection{Blocks: []tui.TranscriptProjectionBlock{
		{Role: "user", DividerGroup: "user", Lines: []string{"❯ commit/push"}},
		{Role: "assistant", DividerGroup: "assistant", Lines: []string{"❮ before"}},
	}}
	m.nativeProjection = initial
	m.nativeRenderedProjection = initial
	m.nativeRenderedSnapshot = initial.Render(tui.TranscriptDivider)
	m.nativeProjection = tui.TranscriptProjection{Blocks: []tui.TranscriptProjectionBlock{
		{Role: "user", DividerGroup: "user", Lines: []string{"❯ commit/push"}},
		{Role: "assistant", DividerGroup: "assistant", Lines: []string{"❮ after"}},
	}}

	cmd := m.emitCurrentNativeHistorySnapshot(false, nativeHistoryReplayPermitAuthoritativeHydrate)
	if cmd == nil {
		t.Fatal("expected authoritative hydrate repair replay in debug mode")
	}
	msgs := collectCmdMessages(t, cmd)
	if len(msgs) != 3 {
		t.Fatalf("expected status plus clear-screen plus native history flush during debug authoritative hydrate repair, got %d message(s)", len(msgs))
	}
	if m.transientStatus != nativeHistoryDivergenceStatusMessage || m.transientStatusKind != uiStatusNoticeError {
		t.Fatalf("expected debug authoritative hydrate repair to surface divergence status, got status=%q kind=%v", m.transientStatus, m.transientStatusKind)
	}
	flush, ok := msgs[2].(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg as debug authoritative hydrate replay payload, got %T", msgs[2])
	}
	plain := stripANSIPreserve(flush.Text)
	if !strings.Contains(plain, "commit/push") || !strings.Contains(plain, "after") || strings.Contains(plain, "before") {
		t.Fatalf("expected debug authoritative hydrate replay to emit corrected transcript, got %q", plain)
	}
}

func TestModeRestoreReplayPermitOverridesEarlierAuthoritativeHydratePermit(t *testing.T) {
	m := newProjectedStaticUIModel(
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenNever),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	)
	_, startupCmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if startupCmd == nil {
		t.Fatal("expected startup replay command")
	}
	_ = collectCmdMessages(t, startupCmd)

	enterCmd := m.toggleTranscriptModeWithNativeReplay(false)
	if m.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected detail mode, got %q", m.view.Mode())
	}
	_ = collectCmdMessages(t, enterCmd)

	m.armNativeHistoryReplayPermit(nativeHistoryReplayPermitAuthoritativeHydrate)
	cmd := m.runtimeAdapter().applyChatSnapshot(runtime.ChatSnapshot{
		Entries: []runtime.ChatEntry{{Role: "user", Text: "fresh root"}, {Role: "assistant", Text: "rewritten tail"}},
	})
	if cmd != nil {
		t.Fatalf("expected hydrate repair replay to stay deferred while detail is active, got %T", cmd())
	}
	if got := m.nativeHistoryReplayPermit; got != nativeHistoryReplayPermitAuthoritativeHydrate {
		t.Fatalf("expected authoritative hydrate permit to remain armed in detail mode, got %v", got)
	}

	leaveCmd := m.toggleTranscriptModeWithNativeReplay(true)
	if m.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected ongoing mode, got %q", m.view.Mode())
	}
	if leaveCmd == nil {
		t.Fatal("expected detail exit to restore committed normal-buffer history")
	}
	msgs := collectCmdMessages(t, leaveCmd)
	if len(msgs) != 2 {
		t.Fatalf("expected clear-screen plus native history flush for mode-restore replay, got %d message(s)", len(msgs))
	}
	flush, ok := msgs[1].(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg as mode-restore replay payload, got %T", msgs[1])
	}
	plain := stripANSIText(flush.Text)
	if !strings.Contains(plain, "fresh root") || !strings.Contains(plain, "rewritten tail") || strings.Contains(plain, "seed") {
		t.Fatalf("expected mode-restore replay to emit authoritative transcript, got %q", plain)
	}
	if m.transientStatus == nativeHistoryDivergenceStatusMessage {
		t.Fatalf("did not expect authoritative-hydrate warning to win after mode-restore replay, got status=%q", m.transientStatus)
	}
}

func TestNativeHistorySnapshotForceFullRewriteReplaysInOngoingMode(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.windowSizeKnown = true
	initial := tui.TranscriptProjection{Blocks: []tui.TranscriptProjectionBlock{{Role: "assistant", Lines: []string{"before"}}}}
	updated := tui.TranscriptProjection{Blocks: []tui.TranscriptProjectionBlock{{Role: "assistant", Lines: []string{"after"}}}}
	m.nativeProjection = updated
	m.nativeRenderedProjection = initial
	m.nativeRenderedSnapshot = initial.Render(tui.TranscriptDivider)

	cmd := m.emitCurrentNativeHistorySnapshot(true, nativeHistoryReplayPermitNone)
	if cmd == nil {
		t.Fatal("expected force-full native replay command")
	}
	msgs := collectCmdMessages(t, cmd)
	if len(msgs) != 2 {
		t.Fatalf("expected clear-screen plus native history flush for force-full replay, got %d message(s)", len(msgs))
	}
	flush, ok := msgs[1].(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg as second force-full replay message, got %T", msgs[1])
	}
	if !strings.Contains(stripANSIText(flush.Text), "after") {
		t.Fatalf("expected force-full replay to include updated history, got %q", flush.Text)
	}
	if got := m.nativeRenderedSnapshot; got != updated.Render(tui.TranscriptDivider) {
		t.Fatalf("expected rendered snapshot updated after force-full replay, got %q", got)
	}
}
