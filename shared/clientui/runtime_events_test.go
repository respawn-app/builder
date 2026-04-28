package clientui

import "testing"

func TestReduceRuntimeEvent_UserMessageFlushedProducesPendingInputAndConversationUpdates(t *testing.T) {
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

	if update.Transcript.Sync != nil {
		t.Fatal("did not expect flushed user message to request session sync")
	}
	if !hasPendingInputCommand(update.PendingInput.Commands, RuntimePendingInputRecordPromptHistory) {
		t.Fatal("expected consumed injected text to be recorded in prompt history")
	}
	if !hasPendingInputCommand(update.PendingInput.Commands, RuntimePendingInputClearDraft) {
		t.Fatal("expected locked injected input to clear the draft input")
	}
	if update.PendingInput.State.InputSubmitLocked {
		t.Fatal("expected input submit lock cleared")
	}
	if update.PendingInput.State.LockedInjectText != "" {
		t.Fatalf("expected locked inject text cleared, got %q", update.PendingInput.State.LockedInjectText)
	}
	if len(update.PendingInput.State.PendingInjected) != 1 || update.PendingInput.State.PendingInjected[0] != "follow-up" {
		t.Fatalf("expected first injected item consumed, got %+v", update.PendingInput.State.PendingInjected)
	}
	if update.Conversation.State.Freshness != ConversationFreshnessEstablished {
		t.Fatalf("conversation freshness = %v, want established", update.Conversation.State.Freshness)
	}
}

func TestReduceRuntimeEvent_RunStateStoppedClearsReasoningAndReturnsToIdle(t *testing.T) {
	update := ReduceRuntimeEvent(
		RuntimeEventState{Busy: true, ReasoningStatusHeader: "Running checks"},
		PendingInputState{},
		true,
		Event{Kind: EventRunStateChanged, RunState: &RunState{Busy: false}},
	)

	if update.RunState.State.Busy {
		t.Fatal("expected busy cleared")
	}
	if update.RunState.Activity != RuntimeActivityIdle {
		t.Fatal("expected stopped run to return running activity to idle")
	}
	if update.Reasoning.State.StatusHeader != "" {
		t.Fatalf("expected reasoning status header cleared, got %q", update.Reasoning.State.StatusHeader)
	}
	if !hasReasoningStreamCommand(update.Reasoning.Stream, RuntimeReasoningStreamClear) {
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

	if !update.RunState.State.Busy {
		t.Fatal("expected busy set")
	}
	if update.RunState.Activity != RuntimeActivityRunning {
		t.Fatal("expected started run to set running activity")
	}
	if update.Transcript.Sync != nil {
		t.Fatal("did not expect started run to request transcript sync")
	}
	if !hasPendingInputCommand(update.PendingInput.Commands, RuntimePendingInputClearPreSubmit) {
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
	if plain.Transcript.Sync != nil {
		t.Fatal("did not expect plain conversation_updated to request transcript sync")
	}
	committed := ReduceRuntimeEvent(
		RuntimeEventState{},
		PendingInputState{},
		false,
		Event{Kind: EventConversationUpdated, CommittedTranscriptChanged: true},
	)
	if committed.Transcript.Sync == nil || committed.Transcript.Sync.Reason != RuntimeTranscriptSyncCommittedAdvance {
		t.Fatal("expected committed conversation_updated to request transcript sync")
	}
	recovery := ReduceRuntimeEvent(
		RuntimeEventState{},
		PendingInputState{},
		false,
		Event{Kind: EventConversationUpdated, RecoveryCause: TranscriptRecoveryCauseStreamGap},
	)
	if recovery.Transcript.Sync == nil || recovery.Transcript.Sync.Reason != RuntimeTranscriptSyncRecovery {
		t.Fatal("expected recovery conversation_updated to request transcript sync")
	}
	gap := ReduceRuntimeEvent(
		RuntimeEventState{},
		PendingInputState{},
		false,
		Event{Kind: EventStreamGap, RecoveryCause: TranscriptRecoveryCauseStreamGap},
	)
	if gap.Transcript.Sync == nil || gap.Transcript.Sync.Reason != RuntimeTranscriptSyncStreamGap {
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
	if update.Transcript.Sync == nil || update.Transcript.Sync.Reason != RuntimeTranscriptSyncOngoingErrorUpdated {
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

	if !hasBackgroundProcessCommand(update.BackgroundProcesses.Commands, RuntimeBackgroundProcessRefresh) {
		t.Fatal("expected background update to refresh process snapshots")
	}
	notice := firstBackgroundNotice(update.Notices)
	if notice == nil {
		t.Fatal("expected completion notice")
	}
	if notice.Kind != BackgroundNoticeSuccess {
		t.Fatalf("notice kind = %v, want success", notice.Kind)
	}
	if notice.Message != "Background shell 1000 completed (exit 0)" {
		t.Fatalf("notice message = %q", notice.Message)
	}
}

func TestReduceRuntimeEvent_BackgroundCompletionFallsBackWithoutCompactText(t *testing.T) {
	update := ReduceRuntimeEvent(
		RuntimeEventState{},
		PendingInputState{},
		false,
		Event{Kind: EventBackgroundUpdated, Background: &BackgroundShellEvent{Type: "completed", ID: "1000", State: "completed"}},
	)

	notice := firstBackgroundNotice(update.Notices)
	if notice == nil {
		t.Fatal("expected completion notice")
	}
	if notice.Message != "background shell 1000 completed" {
		t.Fatalf("notice message = %q", notice.Message)
	}
}

func TestReduceRuntimeEvent_CompactionCompletedClearsCompactingWithoutSyntheticNotice(t *testing.T) {
	update := ReduceRuntimeEvent(
		RuntimeEventState{Compacting: true},
		PendingInputState{},
		false,
		Event{Kind: EventCompactionCompleted, Compaction: &CompactionStatus{Mode: "auto", Count: 2}},
	)

	if update.RunState.State.Compacting {
		t.Fatal("expected compaction completed to clear compacting state")
	}
	if update.Transcript.SyntheticOngoingEntry != nil {
		t.Fatalf("did not expect synthetic ongoing compaction notice, got %+v", update.Transcript.SyntheticOngoingEntry)
	}
}

func TestDomainReducersIgnoreUnownedEventConcerns(t *testing.T) {
	evt := Event{Kind: EventBackgroundUpdated, Background: &BackgroundShellEvent{Type: "completed", ID: "1000", State: "completed"}}

	if transcript := ReduceRuntimeTranscriptEvent(evt); transcript.Sync != nil || len(transcript.AssistantStream) != 0 {
		t.Fatalf("transcript reducer handled background event: %+v", transcript)
	}
	if reasoning := ReduceRuntimeReasoningEvent(RuntimeReasoningState{StatusHeader: "thinking"}, evt); reasoning.State.StatusHeader != "thinking" || len(reasoning.Stream) != 0 {
		t.Fatalf("reasoning reducer handled background event: %+v", reasoning)
	}
	processes := ReduceRuntimeBackgroundProcessEvent(evt)
	if !hasBackgroundProcessCommand(processes.Commands, RuntimeBackgroundProcessRefresh) {
		t.Fatalf("background process reducer did not own background refresh: %+v", processes)
	}
}

func hasPendingInputCommand(commands []RuntimePendingInputCommand, kind RuntimePendingInputCommandKind) bool {
	for _, command := range commands {
		if command.Kind == kind {
			return true
		}
	}
	return false
}

func hasReasoningStreamCommand(commands []RuntimeReasoningStreamCommand, kind RuntimeReasoningStreamCommandKind) bool {
	for _, command := range commands {
		if command.Kind == kind {
			return true
		}
	}
	return false
}

func hasBackgroundProcessCommand(commands []RuntimeBackgroundProcessCommand, kind RuntimeBackgroundProcessCommand) bool {
	for _, command := range commands {
		if command == kind {
			return true
		}
	}
	return false
}

func firstBackgroundNotice(commands []RuntimeNoticeCommand) *BackgroundNotice {
	for _, command := range commands {
		if command.Kind == RuntimeNoticeBackground {
			return command.BackgroundNotice
		}
	}
	return nil
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
