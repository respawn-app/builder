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

	if update.SyncSessionView {
		t.Fatal("did not expect flushed user message to request session sync")
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

func TestReduceRuntimeEvent_RunStateStartedDoesNotRequestTranscriptSync(t *testing.T) {
	update := ReduceRuntimeEvent(
		RuntimeEventState{Busy: false},
		PendingInputState{},
		false,
		Event{Kind: EventRunStateChanged, RunState: &RunState{Busy: true}},
	)

	if !update.State.Busy {
		t.Fatal("expected busy set")
	}
	if !update.SetActivityRunning {
		t.Fatal("expected started run to set running activity")
	}
	if update.SyncSessionView {
		t.Fatal("did not expect started run to request transcript sync")
	}
	if !update.ClearPendingPreSubmit {
		t.Fatal("expected started run to clear pending pre-submit text")
	}
}

func TestReduceRuntimeEvent_ConversationUpdatedRequiresExplicitCommittedAdvanceOrRecovery(t *testing.T) {
	plain := ReduceRuntimeEvent(
		RuntimeEventState{},
		PendingInputState{},
		false,
		Event{Kind: EventConversationUpdated},
	)
	if plain.SyncSessionView {
		t.Fatal("did not expect plain conversation_updated to request transcript sync")
	}
	committed := ReduceRuntimeEvent(
		RuntimeEventState{},
		PendingInputState{},
		false,
		Event{Kind: EventConversationUpdated, CommittedTranscriptChanged: true},
	)
	if !committed.SyncSessionView {
		t.Fatal("expected committed conversation_updated to request transcript sync")
	}
	recovery := ReduceRuntimeEvent(
		RuntimeEventState{},
		PendingInputState{},
		false,
		Event{Kind: EventConversationUpdated, RecoveryCause: TranscriptRecoveryCauseStreamGap},
	)
	if !recovery.SyncSessionView {
		t.Fatal("expected recovery conversation_updated to request transcript sync")
	}
	gap := ReduceRuntimeEvent(
		RuntimeEventState{},
		PendingInputState{},
		false,
		Event{Kind: EventStreamGap, RecoveryCause: TranscriptRecoveryCauseStreamGap},
	)
	if !gap.SyncSessionView {
		t.Fatal("expected explicit stream gap to request transcript sync")
	}
}

func TestReduceRuntimeEvent_OngoingErrorUpdatedRequestsSessionSync(t *testing.T) {
	update := ReduceRuntimeEvent(
		RuntimeEventState{},
		PendingInputState{},
		false,
		Event{Kind: EventOngoingErrorUpdated},
	)
	if !update.SyncSessionView {
		t.Fatal("expected ongoing_error_updated to request transcript sync")
	}
}

func TestReduceRuntimeEvent_BackgroundCompletionProducesNotice(t *testing.T) {
	update := ReduceRuntimeEvent(
		RuntimeEventState{},
		PendingInputState{},
		false,
		Event{Kind: EventBackgroundUpdated, Background: &BackgroundShellEvent{Type: "completed", ID: "1000", State: "completed", CompactText: "Background shell 1000 completed (exit 0)"}},
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
	if update.BackgroundNotice.Message != "Background shell 1000 completed (exit 0)" {
		t.Fatalf("notice message = %q", update.BackgroundNotice.Message)
	}
}

func TestReduceRuntimeEvent_BackgroundCompletionFallsBackWithoutCompactText(t *testing.T) {
	update := ReduceRuntimeEvent(
		RuntimeEventState{},
		PendingInputState{},
		false,
		Event{Kind: EventBackgroundUpdated, Background: &BackgroundShellEvent{Type: "completed", ID: "1000", State: "completed"}},
	)

	if update.BackgroundNotice == nil {
		t.Fatal("expected completion notice")
	}
	if update.BackgroundNotice.Message != "background shell 1000 completed" {
		t.Fatalf("notice message = %q", update.BackgroundNotice.Message)
	}
}

func TestReduceRuntimeEvent_CompactionCompletedClearsCompactingWithoutSyntheticNotice(t *testing.T) {
	update := ReduceRuntimeEvent(
		RuntimeEventState{Compacting: true},
		PendingInputState{},
		false,
		Event{Kind: EventCompactionCompleted, Compaction: &CompactionStatus{Mode: "auto", Count: 2}},
	)

	if update.State.Compacting {
		t.Fatal("expected compaction completed to clear compacting state")
	}
	if update.SyntheticOngoingEntry != nil {
		t.Fatalf("did not expect synthetic ongoing compaction notice, got %+v", update.SyntheticOngoingEntry)
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
