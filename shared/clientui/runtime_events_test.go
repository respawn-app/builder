package clientui

import "testing"

func TestReduceRuntimeEvent_UserMessageFlushedConsumesInjectedInputAndUnlocksSubmit(t *testing.T) {
	update := ReduceRuntimeEvent(
		RuntimeEventState{ConversationFreshness: ConversationFreshnessFresh},
		PendingInputState{
			Input:             "steered message",
			PendingInjected:   []string{"steered message", "follow-up"},
			LockedInjectText:  "steered message",
			InputSubmitLocked: true,
		},
		false,
		Event{Kind: EventUserMessageFlushed, UserMessage: "steered message"},
	)

	if !update.SyncSessionView {
		t.Fatal("expected flushed user message to request session sync")
	}
	if !update.RecordPromptHistory {
		t.Fatal("expected consumed injected text to be recorded in prompt history")
	}
	if !update.ClearInput {
		t.Fatal("expected locked injected input to clear the draft input")
	}
	if update.Input.InputSubmitLocked {
		t.Fatal("expected input submit lock cleared")
	}
	if update.Input.LockedInjectText != "" {
		t.Fatalf("expected locked inject text cleared, got %q", update.Input.LockedInjectText)
	}
	if len(update.Input.PendingInjected) != 1 || update.Input.PendingInjected[0] != "follow-up" {
		t.Fatalf("expected first injected item consumed, got %+v", update.Input.PendingInjected)
	}
	if update.State.ConversationFreshness != ConversationFreshnessEstablished {
		t.Fatalf("conversation freshness = %v, want established", update.State.ConversationFreshness)
	}
}

func TestReduceRuntimeEvent_RunStateStoppedClearsReasoningAndReturnsToIdle(t *testing.T) {
	update := ReduceRuntimeEvent(
		RuntimeEventState{Busy: true, ReasoningStatusHeader: "Running checks"},
		PendingInputState{},
		true,
		Event{Kind: EventRunStateChanged, RunState: &RunState{Busy: false}},
	)

	if update.State.Busy {
		t.Fatal("expected busy cleared")
	}
	if !update.SetActivityIdle {
		t.Fatal("expected stopped run to return running activity to idle")
	}
	if update.State.ReasoningStatusHeader != "" {
		t.Fatalf("expected reasoning status header cleared, got %q", update.State.ReasoningStatusHeader)
	}
	if !update.ClearReasoningStream {
		t.Fatal("expected reasoning stream cleared when run stops")
	}
}

func TestReduceRuntimeEvent_BackgroundCompletionProducesNotice(t *testing.T) {
	update := ReduceRuntimeEvent(
		RuntimeEventState{},
		PendingInputState{},
		false,
		Event{Kind: EventBackgroundUpdated, Background: &BackgroundShellEvent{Type: "completed", ID: "1000", State: "completed"}},
	)

	if !update.RefreshProcesses {
		t.Fatal("expected background update to refresh process snapshots")
	}
	if update.BackgroundNotice == nil {
		t.Fatal("expected completion notice")
	}
	if update.BackgroundNotice.Kind != BackgroundNoticeSuccess {
		t.Fatalf("notice kind = %v, want success", update.BackgroundNotice.Kind)
	}
	if update.BackgroundNotice.Message != "background shell 1000 completed" {
		t.Fatalf("notice message = %q", update.BackgroundNotice.Message)
	}
}

func TestExtractReasoningStatusHeaderAcceptsWhitespaceWrappedBoldOnly(t *testing.T) {
	got := ExtractReasoningStatusHeader("  **Summarizing fix and investigation**  ")
	if got != "Summarizing fix and investigation" {
		t.Fatalf("expected bold-only header extracted, got %q", got)
	}
}

func TestExtractReasoningStatusHeaderUsesFirstBoldSpanInMixedContent(t *testing.T) {
	tests := map[string]string{
		"**Header**\nextra":                 "Header",
		"prefix **Header**":                 "Header",
		"**Header** suffix":                 "Header",
		"prefix **Header** suffix":          "Header",
		"before **First** after **Second**": "First",
	}
	for input, want := range tests {
		if got := ExtractReasoningStatusHeader(input); got != want {
			t.Fatalf("expected %q -> %q, got %q", input, want, got)
		}
	}
}

func TestExtractReasoningStatusHeaderRejectsInvalidContent(t *testing.T) {
	tests := []string{"****", "**   **", "**Header*", "*Header**", "plain text", "prefix **Header"}
	for _, input := range tests {
		if got := ExtractReasoningStatusHeader(input); got != "" {
			t.Fatalf("expected %q to be rejected, got %q", input, got)
		}
	}
}
