package app

import (
	"testing"

	"builder/shared/clientui"
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
