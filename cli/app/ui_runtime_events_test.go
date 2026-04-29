package app

import (
	"testing"

	"builder/shared/clientui"
)

func TestRuntimeEventCanDeferCommittedConversationFence(t *testing.T) {
	update := clientui.Event{
		Kind:                       clientui.EventConversationUpdated,
		CommittedTranscriptChanged: true,
		TranscriptRevision:         7,
		CommittedEntryCount:        2,
	}
	if !runtimeEventCanDeferCommittedConversationFence(update) {
		t.Fatal("expected empty committed conversation update to be deferrable fence")
	}
	update.TranscriptEntries = []clientui.ChatEntry{{Role: "assistant", Text: "done"}}
	if runtimeEventCanDeferCommittedConversationFence(update) {
		t.Fatal("expected conversation update with entries to be non-deferrable")
	}
	update.TranscriptEntries = nil
	update.RecoveryCause = clientui.TranscriptRecoveryCauseStreamGap
	if runtimeEventCanDeferCommittedConversationFence(update) {
		t.Fatal("expected recovery conversation update to be non-deferrable")
	}
}

func TestRuntimeEventCoversDeferredCommittedConversationUpdate(t *testing.T) {
	update := clientui.Event{
		Kind:                       clientui.EventConversationUpdated,
		StepID:                     "step-1",
		CommittedTranscriptChanged: true,
		TranscriptRevision:         7,
		CommittedEntryCount:        2,
	}
	next := clientui.Event{
		Kind:                       clientui.EventAssistantMessage,
		StepID:                     " step-1 ",
		CommittedTranscriptChanged: true,
		TranscriptRevision:         7,
		CommittedEntryCount:        2,
		TranscriptEntries:          []clientui.ChatEntry{{Role: "assistant", Text: "done"}},
	}
	if !runtimeEventCoversDeferredCommittedConversationUpdate(update, next) {
		t.Fatal("expected matching committed entry event to cover deferred update")
	}
	next.CommittedEntryCount = 3
	if runtimeEventCoversDeferredCommittedConversationUpdate(update, next) {
		t.Fatal("expected different committed count not to cover deferred update")
	}
	next.CommittedEntryCount = 2
	next.StepID = "other"
	if runtimeEventCoversDeferredCommittedConversationUpdate(update, next) {
		t.Fatal("expected different step not to cover deferred update")
	}
}

func TestRuntimeEventShouldBatchAfterCommittedConversationFence(t *testing.T) {
	update := clientui.Event{
		Kind:                       clientui.EventConversationUpdated,
		StepID:                     "step-1",
		CommittedTranscriptChanged: true,
		TranscriptRevision:         7,
		CommittedEntryCount:        2,
	}
	next := clientui.Event{
		Kind:                       clientui.EventAssistantMessage,
		StepID:                     "step-1",
		CommittedTranscriptChanged: true,
		TranscriptRevision:         8,
		CommittedEntryCount:        3,
		TranscriptEntries:          []clientui.ChatEntry{{Role: "assistant", Text: "done"}},
	}
	if !runtimeEventShouldBatchAfterCommittedConversationFence(update, next) {
		t.Fatal("expected same-step committed entry advance to batch after deferred update")
	}
	next.CommittedEntryCount = 2
	if runtimeEventShouldBatchAfterCommittedConversationFence(update, next) {
		t.Fatal("expected covering event not to be batched with deferred update")
	}
	next.CommittedEntryCount = 3
	next.TranscriptRevision = 6
	if runtimeEventShouldBatchAfterCommittedConversationFence(update, next) {
		t.Fatal("expected older revision not to batch after deferred update")
	}
}
