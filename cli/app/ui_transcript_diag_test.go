package app

import (
	"strings"
	"testing"

	"builder/shared/clientui"
)

func TestProjectedRuntimeEventLogsTranscriptDiagnostics(t *testing.T) {
	logger := &testUILogger{}
	m := newProjectedStaticUIModel(
		WithUILogger(logger),
		WithUITranscriptDiagnostics(true),
		WithUISessionID("session-1"),
	)

	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(clientui.Event{
		Kind:           clientui.EventAssistantDelta,
		StepID:         "step-1",
		AssistantDelta: "working",
		TranscriptEntries: []clientui.ChatEntry{{
			Role: "assistant",
			Text: "working",
		}},
	})

	joined := strings.Join(logger.lines, "\n")
	if !strings.Contains(joined, "transcript.diag.client.apply_event") {
		t.Fatalf("expected event diagnostics, got %q", joined)
	}
	if !strings.Contains(joined, "transcript.diag.client.append_entries") {
		t.Fatalf("expected append diagnostics, got %q", joined)
	}
	if !strings.Contains(joined, "session_id=session-1") {
		t.Fatalf("expected session id in diagnostics, got %q", joined)
	}
}

func TestRuntimeTranscriptPageLogsRejectReason(t *testing.T) {
	logger := &testUILogger{}
	m := newProjectedStaticUIModel(
		WithUILogger(logger),
		WithUITranscriptDiagnostics(true),
		WithUISessionID("session-1"),
	)
	m.transcriptRevision = 10
	m.transcriptLiveDirty = true

	_ = m.runtimeAdapter().applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{Window: clientui.TranscriptWindowOngoingTail}, clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     10,
		TotalEntries: 1,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "seed"}},
	})

	joined := strings.Join(logger.lines, "\n")
	if !strings.Contains(joined, "transcript.diag.client.apply_page_reject") {
		t.Fatalf("expected reject diagnostics, got %q", joined)
	}
	if !strings.Contains(joined, "reason=live_dirty_same_or_older_revision") {
		t.Fatalf("expected reject reason, got %q", joined)
	}
}
