package runtimeview

import (
	"testing"
	"time"

	"builder/server/llm"
	"builder/server/runtime"
	"builder/shared/transcript"
)

func TestEventFromRuntimeProjectsReasoningAndBackground(t *testing.T) {
	exitCode := 17
	view := EventFromRuntime(runtime.Event{
		Kind:           runtime.EventBackgroundUpdated,
		StepID:         "step-1",
		AssistantDelta: "delta",
		ReasoningDelta: &llm.ReasoningSummaryDelta{Key: "k", Role: "reasoning", Text: "thinking"},
		RunState:       &runtime.RunState{Busy: true, RunID: "run-1", Status: runtime.RunStatusRunning},
		Background: &runtime.BackgroundShellEvent{
			Type:              "completed",
			ID:                "123",
			State:             "completed",
			Command:           "echo hi",
			Workdir:           "/tmp/work",
			LogPath:           "/tmp/work/run.log",
			NoticeText:        "done",
			CompactText:       "done compact",
			Preview:           "hi",
			Removed:           2,
			ExitCode:          &exitCode,
			UserRequestedKill: true,
			NoticeSuppressed:  true,
		},
	})
	if view.Kind != "background_updated" || view.StepID != "step-1" || view.AssistantDelta != "delta" {
		t.Fatalf("unexpected projected event: %+v", view)
	}
	if view.ReasoningDelta == nil || view.ReasoningDelta.Text != "thinking" {
		t.Fatalf("expected reasoning delta projection, got %+v", view.ReasoningDelta)
	}
	if view.RunState == nil || !view.RunState.Busy {
		t.Fatalf("expected busy run state, got %+v", view.RunState)
	}
	if view.RunState.RunID != "run-1" || view.RunState.Status != "running" {
		t.Fatalf("expected run identity in projected run state, got %+v", view.RunState)
	}
	if view.Background == nil || view.Background.ID != "123" {
		t.Fatalf("expected background projection, got %+v", view.Background)
	}
	if view.Background.ExitCode == nil || *view.Background.ExitCode != 17 {
		t.Fatalf("expected copied exit code, got %+v", view.Background.ExitCode)
	}
}

func TestRunViewFromRuntimeCopiesSnapshot(t *testing.T) {
	startedAt := time.Now().UTC().Add(-time.Minute)
	finishedAt := time.Now().UTC()
	view := RunViewFromRuntime("session-1", &runtime.RunSnapshot{
		RunID:      "run-1",
		StepID:     "step-1",
		Status:     runtime.RunStatusCompleted,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	})
	if view == nil {
		t.Fatal("expected run view")
	}
	if view.RunID != "run-1" || view.SessionID != "session-1" || view.StepID != "step-1" {
		t.Fatalf("unexpected run view ids: %+v", view)
	}
	if view.Status != "completed" || !view.StartedAt.Equal(startedAt) || !view.FinishedAt.Equal(finishedAt) {
		t.Fatalf("unexpected run view timing/status: %+v", view)
	}
}

func TestChatSnapshotFromRuntimeCopiesEntries(t *testing.T) {
	toolCall := &transcript.ToolCallMeta{
		ToolName:    "shell",
		Suggestions: []string{"a", "b"},
	}
	snapshot := ChatSnapshotFromRuntime(runtime.ChatSnapshot{
		Entries: []runtime.ChatEntry{{
			Role:        "assistant",
			Text:        "hello",
			OngoingText: "hel",
			Phase:       llm.MessagePhaseFinal,
			ToolCallID:  "call-1",
			ToolCall:    toolCall,
		}},
		Ongoing:      "ongoing",
		OngoingError: "warn",
	})
	if len(snapshot.Entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(snapshot.Entries))
	}
	entry := snapshot.Entries[0]
	if entry.Phase != string(llm.MessagePhaseFinal) || entry.ToolCall == nil || entry.ToolCall.ToolName != "shell" {
		t.Fatalf("unexpected projected entry: %+v", entry)
	}
	if len(entry.ToolCall.Suggestions) != 2 {
		t.Fatalf("expected copied suggestions, got %+v", entry.ToolCall.Suggestions)
	}
	toolCall.Suggestions[0] = "changed"
	if snapshot.Entries[0].ToolCall.Suggestions[0] != "a" {
		t.Fatalf("expected projection to copy suggestions, got %+v", snapshot.Entries[0].ToolCall.Suggestions)
	}
	if snapshot.Ongoing != "ongoing" || snapshot.OngoingError != "warn" {
		t.Fatalf("unexpected snapshot projection: %+v", snapshot)
	}
}
