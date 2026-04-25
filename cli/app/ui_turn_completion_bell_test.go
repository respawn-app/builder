package app

import (
	"errors"
	"testing"

	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/clientui"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSubmitDoneDefersTurnCompletionBellUntilQueuedTurnsFinish(t *testing.T) {
	ringer := &countRinger{}
	bells := newBellHooks(ringer, nil)
	m := newProjectedStaticUIModel(WithUITurnQueueHook(bells))
	m.busy = true
	m.queued = []string{"follow up"}

	next, _ := m.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"}})
	updated := next.(*uiModel)
	next, _ = updated.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"}})
	updated = next.(*uiModel)
	next, _ = updated.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventAssistantMessage, StepID: "step-1", TranscriptEntries: []clientui.ChatEntry{{Role: "assistant", Text: "first"}}}})
	updated = next.(*uiModel)

	next, cmd := updated.Update(submitDoneMsg{message: "first"})
	updated = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected queued follow-up to start when first turn completes")
	}
	if got := ringer.Count(); got != 0 {
		t.Fatalf("ring count = %d after first queued turn completion, want 0", got)
	}
	if !updated.busy {
		t.Fatal("expected queued follow-up submission to be running")
	}

	next, _ = updated.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-2"}})
	updated = next.(*uiModel)
	next, _ = updated.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-2"}})
	updated = next.(*uiModel)
	next, _ = updated.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventAssistantMessage, StepID: "step-2", TranscriptEntries: []clientui.ChatEntry{{Role: "assistant", Text: "second"}}}})
	updated = next.(*uiModel)

	next, _ = updated.Update(submitDoneMsg{message: "second"})
	updated = next.(*uiModel)
	if got := ringer.Count(); got != 1 {
		t.Fatalf("ring count = %d after queued turns drain, want 1", got)
	}
	if got := ringer.Last(); got != "builder: second" {
		t.Fatalf("last message = %q, want %q", got, "builder: second")
	}
	if updated.busy {
		t.Fatal("expected UI idle after queued turns drain")
	}
}

func TestPreSubmitCheckErrorAbortsPendingTurnCompletionBell(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, &runtimeAdapterFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	ringer := &countRinger{}
	bells := newBellHooks(ringer, nil)
	m := newProjectedEngineUIModel(eng, WithUITurnQueueHook(bells))

	next, _ := m.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"}})
	updated := next.(*uiModel)
	next, _ = updated.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"}})
	updated = next.(*uiModel)
	next, _ = updated.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventAssistantMessage, StepID: "step-1", TranscriptEntries: []clientui.ChatEntry{{Role: "assistant", Text: "first"}}}})
	updated = next.(*uiModel)

	updated.input = "continue"
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)

	next, _ = updated.Update(preSubmitCompactionCheckDoneMsg{
		token: updated.preSubmitCheckToken,
		text:  "continue",
		err:   errors.New("pre-submit failed"),
	})
	updated = next.(*uiModel)
	if updated.activity != uiActivityError {
		t.Fatalf("expected error activity after pre-submit check failure, got %v", updated.activity)
	}

	bells.OnTurnQueueDrained()
	if got := ringer.Count(); got != 0 {
		t.Fatalf("ring count = %d after pre-submit check abort, want 0", got)
	}
}

func TestNoopFinalAbortsPendingTurnCompletionBell(t *testing.T) {
	ringer := &countRinger{}
	bells := newBellHooks(ringer, nil)
	m := newProjectedStaticUIModel(WithUITurnQueueHook(bells))
	m.busy = true

	next, _ := m.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"}})
	updated := next.(*uiModel)
	next, _ = updated.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"}})
	updated = next.(*uiModel)
	next, _ = updated.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventAssistantMessage, StepID: "step-1", TranscriptEntries: []clientui.ChatEntry{{Role: "assistant", Text: "working"}}}})
	updated = next.(*uiModel)

	next, _ = updated.Update(newSubmitDoneMsg(0, uiNoopFinalToken, "", nil))
	updated = next.(*uiModel)
	if got := ringer.Count(); got != 0 {
		t.Fatalf("ring count = %d after NO_OP final, want 0", got)
	}
	bells.OnTurnQueueDrained()
	if got := ringer.Count(); got != 0 {
		t.Fatalf("ring count = %d after forced drain following NO_OP final, want 0", got)
	}
	if updated.busy {
		t.Fatal("expected UI idle after NO_OP final")
	}
}

func TestQueuedFollowUpAfterNoopFinalDoesNotLeakTurnCompletionBell(t *testing.T) {
	ringer := &countRinger{}
	bells := newBellHooks(ringer, nil)
	m := newProjectedStaticUIModel(WithUITurnQueueHook(bells))
	m.busy = true
	m.queued = []string{"follow up"}

	next, _ := m.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"}})
	updated := next.(*uiModel)
	next, _ = updated.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"}})
	updated = next.(*uiModel)
	next, _ = updated.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventAssistantMessage, StepID: "step-1", TranscriptEntries: []clientui.ChatEntry{{Role: "assistant", Text: "working"}}}})
	updated = next.(*uiModel)

	next, cmd := updated.Update(newSubmitDoneMsg(0, uiNoopFinalToken, "", nil))
	updated = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected queued follow-up to start after NO_OP final")
	}
	if got := ringer.Count(); got != 0 {
		t.Fatalf("ring count = %d after NO_OP final queued follow-up, want 0", got)
	}
	if !updated.busy {
		t.Fatal("expected queued follow-up submission to be running")
	}
	bells.OnTurnQueueDrained()
	if got := ringer.Count(); got != 0 {
		t.Fatalf("ring count = %d after forced drain following queued NO_OP final, want 0", got)
	}
}
