package runtimeview

import (
	"context"
	"errors"
	"testing"
	"time"

	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/clientui"
	"builder/shared/transcript"
)

type projectionFastClient struct{}

func (projectionFastClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
}

func (projectionFastClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}, nil
}

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

func TestMainViewFromRuntimeBundlesStatusAndSession(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.SetName("Session Name"); err != nil {
		t.Fatalf("set name: %v", err)
	}
	if err := store.SetParentSessionID("parent-123"); err != nil {
		t.Fatalf("set parent session id: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: "final answer", Phase: llm.MessagePhaseFinal}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	eng, err := runtime.New(store, projectionFastClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5", ContextWindowTokens: 400_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.SetThinkingLevel("high"); err != nil {
		t.Fatalf("set thinking level: %v", err)
	}
	if changed, err := eng.SetFastModeEnabled(true); err != nil {
		t.Fatalf("enable fast mode: %v", err)
	} else if !changed {
		t.Fatal("expected fast mode enable to report changed=true")
	}
	if changed, enabled := eng.SetAutoCompactionEnabled(false); !changed || enabled {
		t.Fatalf("expected auto-compaction disabled, changed=%v enabled=%v", changed, enabled)
	}

	view := MainViewFromRuntime(eng)
	if view.Session.SessionID != store.Meta().SessionID || view.Session.SessionName != "Session Name" {
		t.Fatalf("unexpected session hydration: %+v", view.Session)
	}
	if view.Status.ParentSessionID != "parent-123" || view.Status.LastCommittedAssistantFinalAnswer != "final answer" {
		t.Fatalf("unexpected status hydration: %+v", view.Status)
	}
	if view.Status.ThinkingLevel != "high" || !view.Status.FastModeEnabled || view.Status.AutoCompactionEnabled {
		t.Fatalf("unexpected runtime flags: %+v", view.Status)
	}
	if view.Status.ContextUsage.WindowTokens != 400_000 {
		t.Fatalf("context window tokens = %d, want 400000", view.Status.ContextUsage.WindowTokens)
	}
	if view.ActiveRun != nil {
		t.Fatalf("expected no active run in idle main view, got %+v", view.ActiveRun)
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

func TestTranscriptPageFromChatSupportsPageNumberPagination(t *testing.T) {
	snapshot := clientui.ChatSnapshot{Entries: []clientui.ChatEntry{
		{Role: "assistant", Text: "a0"},
		{Role: "assistant", Text: "a1"},
		{Role: "assistant", Text: "a2"},
		{Role: "assistant", Text: "a3"},
		{Role: "assistant", Text: "a4"},
	}}

	page := TranscriptPageFromChat("session-1", "incident triage", clientui.ConversationFreshnessEstablished, 7, snapshot, clientui.TranscriptPageRequest{Page: 1, PageSize: 2})
	if page.TotalEntries != 5 {
		t.Fatalf("total entries = %d, want 5", page.TotalEntries)
	}
	if page.Offset != 2 {
		t.Fatalf("offset = %d, want 2", page.Offset)
	}
	if !page.HasMore || page.NextOffset != 4 {
		t.Fatalf("unexpected pagination metadata: %+v", page)
	}
	if len(page.Entries) != 2 || page.Entries[0].Text != "a2" || page.Entries[1].Text != "a3" {
		t.Fatalf("unexpected page entries: %+v", page.Entries)
	}
}

func TestTranscriptPageFromRuntimeUsesOngoingTailWindow(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	for i := 0; i < 600; i++ {
		if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: "reply", Phase: llm.MessagePhaseFinal}); err != nil {
			t.Fatalf("append message %d: %v", i, err)
		}
	}
	eng, err := runtime.New(store, projectionFastClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	page := TranscriptPageFromRuntime(eng, clientui.TranscriptPageRequest{Window: clientui.TranscriptWindowOngoingTail})
	if page.TotalEntries != 600 {
		t.Fatalf("total entries = %d, want 600", page.TotalEntries)
	}
	if page.Offset != 100 {
		t.Fatalf("offset = %d, want 100", page.Offset)
	}
	if page.HasMore {
		t.Fatalf("expected ongoing tail page to terminate at end, got %+v", page)
	}
	if len(page.Entries) != 500 {
		t.Fatalf("entries = %d, want 500", len(page.Entries))
	}
}
