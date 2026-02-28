package app

import (
	"fmt"
	"strings"
	"testing"

	"builder/internal/config"
	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/session"
	"builder/internal/tools"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestScenarioDetailWhileAgentWorksReturnsToLatestOngoingTail(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 100
	m.termHeight = 18
	m.input = "/"
	m.refreshSlashCommandFilterFromInput()
	m.syncViewport()

	for i := 1; i <= 20; i++ {
		m = updateUIModel(t, m, tui.AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("line %02d", i)})
	}

	detail := updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if detail.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected detail mode, got %q", detail.view.Mode())
	}

	for i := 21; i <= 30; i++ {
		detail = updateUIModel(t, detail, tui.AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("line %02d", i)})
	}
	detail = updateUIModel(t, detail, tea.KeyMsg{Type: tea.KeyPgUp})

	ongoing := updateUIModel(t, detail, tea.KeyMsg{Type: tea.KeyShiftTab})
	if ongoing.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected ongoing mode, got %q", ongoing.view.Mode())
	}

	view := stripANSIAndTrimRight(ongoing.View())
	if !containsAny(view, "line 30", "line 29", "line 28") {
		t.Fatalf("expected latest content visible after returning from detail, got %q", view)
	}
	if !strings.Contains(view, "/new") {
		t.Fatalf("expected slash picker to remain visible, got %q", view)
	}
}

func TestScenarioHarnessRestartAndSessionResumeKeepsTranscriptVisible(t *testing.T) {
	workspace := t.TempDir()
	store, err := session.Create(workspace, "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	appendTranscriptMessage(t, store, llm.RoleUser, "u1")
	appendTranscriptMessage(t, store, llm.RoleAssistant, "a1")
	appendTranscriptMessage(t, store, llm.RoleUser, "u2")
	appendTranscriptMessage(t, store, llm.RoleAssistant, "a2 tail")

	eng, err := runtime.New(store, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 90
	m.termHeight = 16
	m.syncViewport()

	first := stripANSIAndTrimRight(m.View())
	if !strings.Contains(first, "a2 tail") {
		t.Fatalf("expected resumed tail in ongoing mode, got %q", first)
	}

	eng.AppendLocalEntry("assistant", "post-resume live update")
	next, _ := m.Update(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventConversationUpdated}})
	updated := next.(*uiModel)
	live := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(live, "post-resume live update") {
		t.Fatalf("expected live update after conversation refresh, got %q", live)
	}

	reopened, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	eng2, err := runtime.New(reopened, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine after restart: %v", err)
	}
	m2 := NewUIModel(eng2, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m2.termWidth = 90
	m2.termHeight = 16
	m2.syncViewport()

	afterRestart := stripANSIAndTrimRight(m2.View())
	if !strings.Contains(afterRestart, "a2 tail") {
		t.Fatalf("expected resumed transcript after harness restart, got %q", afterRestart)
	}
	if strings.Contains(afterRestart, "post-resume live update") {
		t.Fatalf("did not expect non-persisted local update to survive restart, got %q", afterRestart)
	}

	m2 = updateUIModel(t, m2, tea.KeyMsg{Type: tea.KeyShiftTab})
	m2 = updateUIModel(t, m2, tea.KeyMsg{Type: tea.KeyShiftTab})
	backToOngoing := stripANSIAndTrimRight(m2.View())
	if !strings.Contains(backToOngoing, "a2 tail") {
		t.Fatalf("expected transcript preserved across detail roundtrip after restart, got %q", backToOngoing)
	}
}

func TestScenarioTeleportBetweenSessionsResetsVisibleConversation(t *testing.T) {
	workspace := t.TempDir()
	storeA, err := session.Create(workspace, "ws", workspace)
	if err != nil {
		t.Fatalf("create store A: %v", err)
	}
	appendTranscriptMessage(t, storeA, llm.RoleUser, "session-a-user")
	appendTranscriptMessage(t, storeA, llm.RoleAssistant, "session-a-tail")

	storeB, err := session.Create(workspace, "ws", workspace)
	if err != nil {
		t.Fatalf("create store B: %v", err)
	}
	appendTranscriptMessage(t, storeB, llm.RoleUser, "session-b-user")
	appendTranscriptMessage(t, storeB, llm.RoleAssistant, "session-b-tail")

	engA, err := runtime.New(storeA, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine A: %v", err)
	}
	modelA := NewUIModel(engA, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	modelA.termWidth = 80
	modelA.termHeight = 14
	modelA.syncViewport()
	viewA := stripANSIAndTrimRight(modelA.View())
	if !strings.Contains(viewA, "session-a-tail") {
		t.Fatalf("expected session A tail, got %q", viewA)
	}

	engB, err := runtime.New(storeB, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine B: %v", err)
	}
	modelB := NewUIModel(engB, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	modelB.termWidth = 80
	modelB.termHeight = 14
	modelB.syncViewport()
	viewB := stripANSIAndTrimRight(modelB.View())
	if !strings.Contains(viewB, "session-b-tail") || strings.Contains(viewB, "session-a-tail") {
		t.Fatalf("expected teleported session B view only, got %q", viewB)
	}

	reopenA, err := session.Open(storeA.Dir())
	if err != nil {
		t.Fatalf("reopen A: %v", err)
	}
	engA2, err := runtime.New(reopenA, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine A2: %v", err)
	}
	modelA2 := NewUIModel(engA2, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	modelA2.termWidth = 80
	modelA2.termHeight = 14
	modelA2.syncViewport()
	viewA2 := stripANSIAndTrimRight(modelA2.View())
	if !strings.Contains(viewA2, "session-a-tail") || strings.Contains(viewA2, "session-b-tail") {
		t.Fatalf("expected teleported-back session A view only, got %q", viewA2)
	}
}

func TestScenarioScrollAttemptsAcrossModesAfterLongDetailStay(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 10
	m.syncViewport()

	for i := 1; i <= 40; i++ {
		m = updateUIModel(t, m, tui.AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("line %02d", i)})
	}
	start := m.view.OngoingScroll()
	if start == 0 {
		t.Fatal("expected ongoing transcript to be scrollable")
	}

	updated := updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if got := updated.view.OngoingScroll(); got >= start {
		t.Fatalf("expected pgup to scroll ongoing up, got %d from %d", got, start)
	}

	detail := updateUIModel(t, updated, tea.KeyMsg{Type: tea.KeyShiftTab})
	if detail.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected detail mode, got %q", detail.view.Mode())
	}
	for i := 41; i <= 45; i++ {
		detail = updateUIModel(t, detail, tui.AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("line %02d", i)})
	}
	detail = updateUIModel(t, detail, tea.KeyMsg{Type: tea.KeyPgUp})
	detail = updateUIModel(t, detail, tea.KeyMsg{Type: tea.KeyPgDown})

	ongoing := updateUIModel(t, detail, tea.KeyMsg{Type: tea.KeyShiftTab})
	plain := stripANSIAndTrimRight(ongoing.View())
	if !containsAny(plain, "line 45", "line 44", "line 43") {
		t.Fatalf("expected latest line visible after returning from detail, got %q", plain)
	}

	ongoing = updateUIModel(t, ongoing, tea.KeyMsg{Type: tea.KeyUp})
	afterUp := ongoing.view.OngoingScroll()
	ongoing = updateUIModel(t, ongoing, tea.KeyMsg{Type: tea.KeyPgDown})
	if ongoing.view.OngoingScroll() < afterUp {
		t.Fatalf("expected pgdown to move toward latest tail, got %d from %d", ongoing.view.OngoingScroll(), afterUp)
	}
}

func TestHistoryInsertionQueuesInDetailAndFlushesOnReturnToOngoing(t *testing.T) {
	workspace := t.TempDir()
	store, err := session.Create(workspace, "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	appendTranscriptMessage(t, store, llm.RoleUser, "u1")
	appendTranscriptMessage(t, store, llm.RoleAssistant, "a1")

	eng, err := runtime.New(store, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 90
	m.termHeight = 16
	m.syncViewport()

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected detail mode, got %q", m.view.Mode())
	}

	eng.AppendLocalEntry("assistant", "while detail")
	m = updateUIModel(t, m, runtimeEventMsg{event: runtime.Event{Kind: runtime.EventConversationUpdated}})
	if strings.TrimSpace(m.pendingOngoingSnapshot) == "" {
		t.Fatal("expected pending ongoing snapshot queued while detail is active")
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected ongoing mode, got %q", m.view.Mode())
	}
	if m.pendingOngoingSnapshot != "" {
		t.Fatalf("expected pending ongoing snapshot flushed on return to ongoing, got %q", m.pendingOngoingSnapshot)
	}
	if m.pendingOngoingPrintable != "" {
		t.Fatalf("expected pending printable snapshot flushed on return to ongoing, got %q", m.pendingOngoingPrintable)
	}
}

func TestHistoryInsertionFlushesImmediatelyInOngoingMode(t *testing.T) {
	workspace := t.TempDir()
	store, err := session.Create(workspace, "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	appendTranscriptMessage(t, store, llm.RoleUser, "u1")
	appendTranscriptMessage(t, store, llm.RoleAssistant, "a1")

	eng, err := runtime.New(store, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 90
	m.termHeight = 16
	m.syncViewport()

	eng.AppendLocalEntry("assistant", "ongoing add")
	m = updateUIModel(t, m, runtimeEventMsg{event: runtime.Event{Kind: runtime.EventConversationUpdated}})
	if m.pendingOngoingSnapshot != "" {
		t.Fatalf("expected no pending snapshot in ongoing mode, got %q", m.pendingOngoingSnapshot)
	}
	if m.pendingOngoingPrintable != "" {
		t.Fatalf("expected no pending printable snapshot in ongoing mode, got %q", m.pendingOngoingPrintable)
	}
}

func TestOngoingSnapshotDeltaAppendsOnlyNewSuffix(t *testing.T) {
	prev := "❯ u1\n❮ a1\n• ls"
	curr := "❯ u1\n❮ a1\n• ls\n❮ a2\n! err"
	delta, ok := ongoingSnapshotDelta(prev, curr)
	if !ok {
		t.Fatal("expected prefix-compatible snapshots")
	}
	if delta != "❮ a2\n! err" {
		t.Fatalf("unexpected delta: %q", delta)
	}
}

func TestOngoingSnapshotDeltaRejectsReformattedSnapshots(t *testing.T) {
	prev := "❯ u1\n❮ a1\n• ls"
	curr := "❯ u1\n❮ a1 changed\n• ls\n❮ a2"
	delta, ok := ongoingSnapshotDelta(prev, curr)
	if ok || delta != "" {
		t.Fatalf("expected non-prefix snapshots to reject append delta, got ok=%v delta=%q", ok, delta)
	}
}

func TestOngoingSnapshotPrintableDeltaEndsWithNewline(t *testing.T) {
	delta, ok := ongoingSnapshotPrintableDelta("❮ a2", true)
	if !ok {
		t.Fatal("expected printable delta for valid non-empty snapshot delta")
	}
	if !strings.HasSuffix(delta, "\n") {
		t.Fatalf("expected printable delta to end with newline, got %q", delta)
	}
}

func TestCurrentOngoingSnapshotUsesChatPanelFormatting(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 72
	m.termHeight = 14
	m.syncViewport()
	m = updateUIModel(t, m, tui.AppendTranscriptMsg{Role: "user", Text: "show status"})
	m = updateUIModel(t, m, tui.AppendTranscriptMsg{Role: "assistant", Text: "status: ok"})

	canonical, printable := m.currentOngoingSnapshot()
	if !strings.Contains(printable, "\x1b[") {
		t.Fatalf("expected ANSI formatting in ongoing printable snapshot, got %q", printable)
	}

	plain := ansi.Strip(printable)
	if !strings.Contains(plain, strings.Repeat("─", m.termWidth)) {
		t.Fatalf("expected full-width divider in ongoing printable snapshot, got %q", plain)
	}

	lines := strings.Split(printable, "\n")
	for _, line := range lines {
		if lipgloss.Width(line) != m.termWidth {
			t.Fatalf("expected line width %d, got %d for line %q", m.termWidth, lipgloss.Width(line), line)
		}
	}

	canonicalLines := strings.Split(canonical, "\n")
	for _, line := range canonicalLines {
		if strings.HasSuffix(line, " ") {
			t.Fatalf("canonical snapshot should not include right padding, got line %q", line)
		}
	}
}

func TestCurrentOngoingSnapshotCanonicalStableAcrossResizeForShortLines(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 120
	m.termHeight = 16
	m.syncViewport()
	m = updateUIModel(t, m, tui.AppendTranscriptMsg{Role: "user", Text: "short user"})
	m = updateUIModel(t, m, tui.AppendTranscriptMsg{Role: "assistant", Text: "short assistant"})

	beforeCanonical, beforePrintable := m.currentOngoingSnapshot()
	m = updateUIModel(t, m, tea.WindowSizeMsg{Width: 78, Height: 16})
	afterCanonical, afterPrintable := m.currentOngoingSnapshot()

	if beforeCanonical != afterCanonical {
		t.Fatalf("expected canonical snapshot to remain stable across resize for short lines;\nbefore=%q\nafter=%q", beforeCanonical, afterCanonical)
	}
	if beforePrintable == afterPrintable {
		t.Fatal("expected printable snapshot to remain width-aware across resize")
	}
}

func TestHistoryInsertionDisabledInAlwaysAltScreenDoesNotQueuePending(t *testing.T) {
	workspace := t.TempDir()
	store, err := session.Create(workspace, "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	appendTranscriptMessage(t, store, llm.RoleUser, "u1")
	appendTranscriptMessage(t, store, llm.RoleAssistant, "a1")

	eng, err := runtime.New(store, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	m := NewUIModel(
		eng,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenAlways),
	).(*uiModel)
	if !m.altScreenActive {
		t.Fatal("expected alt-screen active in always policy")
	}

	for i := 0; i < 50; i++ {
		eng.AppendLocalEntry("assistant", fmt.Sprintf("tick %d", i))
		m = updateUIModel(t, m, runtimeEventMsg{event: runtime.Event{Kind: runtime.EventConversationUpdated}})
	}
	if m.pendingOngoingSnapshot != "" {
		t.Fatalf("expected no pending snapshot accumulation in always alt-screen mode, got %q", m.pendingOngoingSnapshot)
	}
	if m.pendingOngoingPrintable != "" {
		t.Fatalf("expected no pending printable snapshot accumulation in always alt-screen mode, got %q", m.pendingOngoingPrintable)
	}
}

func appendTranscriptMessage(t *testing.T, store *session.Store, role llm.Role, text string) {
	t.Helper()
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: role, Content: text}); err != nil {
		t.Fatalf("append %s message: %v", role, err)
	}
}

func updateUIModel(t *testing.T, m *uiModel, msg tea.Msg) *uiModel {
	t.Helper()
	next, _ := m.Update(msg)
	updated, ok := next.(*uiModel)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	return updated
}

func containsAny(text string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(text, part) {
			return true
		}
	}
	return false
}
