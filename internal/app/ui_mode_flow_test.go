package app

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"builder/internal/config"
	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/session"
	"builder/internal/tools"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
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
	ongoing.termWidth = 100
	ongoing.termHeight = 18
	ongoing.windowSizeKnown = true
	ongoing.syncViewport()

	view := stripANSIAndTrimRight(ongoing.view.OngoingSnapshot())
	if !containsAny(view, "line 30", "line 29", "line 28") {
		t.Fatalf("expected latest content visible after returning from detail, got %q", view)
	}
	compact := stripANSIAndTrimRight(ongoing.View())
	if !strings.Contains(compact, "/new") {
		t.Fatalf("expected slash picker to remain visible, got %q", compact)
	}
}

func TestCtrlTTogglesTranscriptModeLikeShiftTab(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 100
	m.termHeight = 16
	m.syncViewport()

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlT})
	if m.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected ctrl+t to enter detail mode, got %q", m.view.Mode())
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlT})
	if m.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected ctrl+t to return to ongoing mode, got %q", m.view.Mode())
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

	first := stripANSIAndTrimRight(m.view.OngoingSnapshot())
	if !strings.Contains(first, "a2 tail") {
		t.Fatalf("expected resumed tail in ongoing mode, got %q", first)
	}

	eng.AppendLocalEntry("assistant", "post-resume live update")
	next, _ := m.Update(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventConversationUpdated}})
	updated := next.(*uiModel)
	live := stripANSIAndTrimRight(updated.view.OngoingSnapshot())
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

	afterRestart := stripANSIAndTrimRight(m2.view.OngoingSnapshot())
	if !strings.Contains(afterRestart, "a2 tail") {
		t.Fatalf("expected resumed transcript after harness restart, got %q", afterRestart)
	}
	if strings.Contains(afterRestart, "post-resume live update") {
		t.Fatalf("did not expect non-persisted local update to survive restart, got %q", afterRestart)
	}

	m2 = updateUIModel(t, m2, tea.KeyMsg{Type: tea.KeyShiftTab})
	m2 = updateUIModel(t, m2, tea.KeyMsg{Type: tea.KeyShiftTab})
	backToOngoing := stripANSIAndTrimRight(m2.view.OngoingSnapshot())
	if !strings.Contains(backToOngoing, "a2 tail") {
		t.Fatalf("expected transcript preserved across detail roundtrip after restart, got %q", backToOngoing)
	}
}

func TestScenarioSessionResumeNormalizesLegacyReviewerEntriesInOngoingMode(t *testing.T) {
	workspace := t.TempDir()
	store, err := session.Create(workspace, "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("legacy-step", "local_entry", map[string]any{
		"role":         "reviewer_suggestions",
		"text":         "Supervisor suggested:\n1. Add final verification notes.",
		"ongoing_text": "Supervisor made 1 suggestion.",
	}); err != nil {
		t.Fatalf("append legacy reviewer_suggestions: %v", err)
	}
	if _, err := store.AppendEvent("legacy-step", "local_entry", map[string]any{
		"role": "reviewer_status",
		"text": "Supervisor ran, applied 1 suggestion:\n1. Add final verification notes.",
	}); err != nil {
		t.Fatalf("append legacy reviewer_status: %v", err)
	}

	reopened, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	eng, err := runtime.New(reopened, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine after restart: %v", err)
	}
	m := NewUIModel(eng, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 90
	m.termHeight = 16
	m.syncViewport()

	ongoing := stripANSIAndTrimRight(m.view.OngoingSnapshot())
	if !containsInOrder(ongoing, "Supervisor suggested:", "1. Add final verification notes.", "Supervisor ran: 1 suggestion, applied.") {
		t.Fatalf("expected normalized reviewer entries after session resume, got %q", ongoing)
	}
	if strings.Contains(ongoing, "Supervisor made 1 suggestion.") {
		t.Fatalf("did not expect legacy compact reviewer suggestions text after session resume, got %q", ongoing)
	}
	if strings.Contains(ongoing, "Supervisor ran, applied 1 suggestion:") {
		t.Fatalf("did not expect legacy verbose reviewer status header after session resume, got %q", ongoing)
	}
	if strings.Contains(ongoing, "1. Add final verification notes.\n\n  1. Add final verification notes.") {
		t.Fatalf("did not expect suggestion details duplicated into final reviewer status after session resume, got %q", ongoing)
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
	viewA := stripANSIAndTrimRight(modelA.view.OngoingSnapshot())
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
	viewB := stripANSIAndTrimRight(modelB.view.OngoingSnapshot())
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
	viewA2 := stripANSIAndTrimRight(modelA2.view.OngoingSnapshot())
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
	plain := stripANSIAndTrimRight(ongoing.view.OngoingSnapshot())
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

func TestAlwaysAltScreenPolicyStartsInAltScreen(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenAlways),
	).(*uiModel)
	if !m.altScreenActive {
		t.Fatal("expected alt-screen active in always policy")
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

func collectCmdMessages(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	msgs := make([]tea.Msg, 0)
	var runMsg func(tea.Msg)
	var runCmd func(tea.Cmd)
	runCmd = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		runMsg(cmd())
	}
	runMsg = func(msg tea.Msg) {
		if msg == nil {
			return
		}
		switch typed := msg.(type) {
		case tea.BatchMsg:
			for _, child := range typed {
				runCmd(child)
			}
			return
		}
		value := reflect.ValueOf(msg)
		if value.IsValid() && value.Kind() == reflect.Slice {
			for i := 0; i < value.Len(); i++ {
				child, ok := value.Index(i).Interface().(tea.Cmd)
				if !ok {
					msgs = append(msgs, msg)
					return
				}
				runCmd(child)
			}
			return
		}
		msgs = append(msgs, msg)
	}
	runCmd(cmd)
	return msgs
}

func containsAny(text string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(text, part) {
			return true
		}
	}
	return false
}
