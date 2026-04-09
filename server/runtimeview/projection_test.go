package runtimeview

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	patchformat "builder/server/tools/patch/format"
	"builder/shared/cachewarn"
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

type projectionPreciseClient struct {
	inputTokens int
}

func (c projectionPreciseClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{InputTokens: 900, OutputTokens: 100, WindowTokens: 400_000},
	}, nil
}

func (c projectionPreciseClient) CountRequestInputTokens(context.Context, llm.Request) (int, error) {
	return c.inputTokens, nil
}

func (c projectionPreciseClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}, nil
}

type projectionCountingPreciseClient struct {
	inputTokens int
	countCalls  int
}

func (c *projectionCountingPreciseClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{InputTokens: 900, OutputTokens: 100, WindowTokens: 400_000},
	}, nil
}

func (c *projectionCountingPreciseClient) CountRequestInputTokens(context.Context, llm.Request) (int, error) {
	c.countCalls++
	return c.inputTokens, nil
}

func (c *projectionCountingPreciseClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
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

func TestSessionViewFromRuntimeUsesCommittedEntryMetadata(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "hello"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "local_entry", map[string]any{"role": "system", "text": "local note", "ongoing_text": ""}); err != nil {
		t.Fatalf("append local entry: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: "warn"}); err != nil {
		t.Fatalf("append warning message: %v", err)
	}
	eng, err := runtime.New(store, projectionFastClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	view := SessionViewFromRuntime(eng)
	if view.Transcript.CommittedEntryCount != eng.CommittedTranscriptEntryCount() {
		t.Fatalf("projected committed entry count = %d, engine committed entry count = %d", view.Transcript.CommittedEntryCount, eng.CommittedTranscriptEntryCount())
	}
}

/*
func TestStatusFromRuntimeUsesFreshPreciseCurrentTokens(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, projectionPreciseClient{inputTokens: 180}, tools.NewRegistry(), runtime.Config{Model: "gpt-5", ContextWindowTokens: 400_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.SetOngoingError("")
	if err := eng.AppendLocalEntry("info", "noop"); err != nil {
		t.Fatalf("append local entry: %v", err)
	}
	if err := eng.RecordPromptHistory(""); err != nil {
		t.Fatalf("record prompt history: %v", err)
	}
	if err := eng.AppendLocalEntry("info", ""); err != nil {
		t.Fatalf("append empty local entry: %v", err)
	}
	if err := eng.CompactContextForPreSubmit(context.Background()); err == nil {
		// no-op path is fine if engine chooses to compact nothing; ignore the result.
	}
	if err := eng.AppendLocalEntry("info", "still noop"); err != nil {
		t.Fatalf("append local entry 2: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 2"); err != nil {
		t.Fatalf("append local entry 3: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 3"); err != nil {
		t.Fatalf("append local entry 4: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 4"); err != nil {
		t.Fatalf("append local entry 5: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 5"); err != nil {
		t.Fatalf("append local entry 6: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 6"); err != nil {
		t.Fatalf("append local entry 7: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 7"); err != nil {
		t.Fatalf("append local entry 8: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 8"); err != nil {
		t.Fatalf("append local entry 9: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 9"); err != nil {
		t.Fatalf("append local entry 10: %v", err)
	}
	if err := eng.CompactContext(context.Background(), ""); err == nil {
		// ignore; context is empty and not relevant to projection.
	}
	if err := eng.AppendLocalEntry("info", "still noop 10"); err != nil {
		t.Fatalf("append local entry 11: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 11"); err != nil {
		t.Fatalf("append local entry 12: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 12"); err != nil {
		t.Fatalf("append local entry 13: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 13"); err != nil {
		t.Fatalf("append local entry 14: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 14"); err != nil {
		t.Fatalf("append local entry 15: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 15"); err != nil {
		t.Fatalf("append local entry 16: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 16"); err != nil {
		t.Fatalf("append local entry 17: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 17"); err != nil {
		t.Fatalf("append local entry 18: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 18"); err != nil {
		t.Fatalf("append local entry 19: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 19"); err != nil {
		t.Fatalf("append local entry 20: %v", err)
	}
	if err := eng.AppendLocalEntry("info", "still noop 20"); err != nil {
		t.Fatalf("append local entry 21: %v", err)
	}
	if err := eng.appendUserMessage("", "prompt"); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.SetOngoingError("")
	eng.ClearOngoingError()
	eng.setLastUsage(llm.Usage{InputTokens: 900, OutputTokens: 100, WindowTokens: 400_000})
	if _, ok := eng.ShouldCompactBeforeUserMessage(context.Background(), "follow-up"); ok != nil {
	}
	view := StatusFromRuntime(eng)
	if view.ContextUsage.UsedTokens != 180 {
		t.Fatalf("projected used tokens=%d, want exact 180", view.ContextUsage.UsedTokens)
	}
}

func TestStatusFromRuntimeDoesNotCountTokensWithoutExactSnapshot(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &projectionCountingPreciseClient{inputTokens: 180}
	eng, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{
		Model:               "gpt-5",
		ContextWindowTokens: 400_000,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "prompt"); err != nil {
		t.Fatalf("submit user message: %v", err)
	}
	view := StatusFromRuntime(eng)
	if client.countCalls != 0 {
		t.Fatalf("expected status projection to avoid exact token counting, got %d calls", client.countCalls)
	}
	if view.ContextUsage.UsedTokens != 1_000 {
		t.Fatalf("projected used tokens=%d, want estimator-backed 1000", view.ContextUsage.UsedTokens)
	}
}

*/

func TestStatusFromRuntimeUsesFreshPreciseCurrentTokens(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, projectionPreciseClient{inputTokens: 180}, tools.NewRegistry(), runtime.Config{
		Model:                         "gpt-5",
		ContextWindowTokens:           400_000,
		AutoCompactTokenLimit:         1_000,
		PreSubmitCompactionLeadTokens: 100,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "prompt"); err != nil {
		t.Fatalf("submit user message: %v", err)
	}
	if _, err := eng.ShouldCompactBeforeUserMessage(context.Background(), "follow-up"); err != nil {
		t.Fatalf("warm exact count: %v", err)
	}
	view := StatusFromRuntime(eng)
	if view.ContextUsage.UsedTokens != 180 {
		t.Fatalf("projected used tokens=%d, want exact 180", view.ContextUsage.UsedTokens)
	}
}

func TestEventFromRuntimeCopiesCacheWarningLostInputTokens(t *testing.T) {
	event := EventFromRuntime(runtime.Event{
		Kind:                   runtime.EventCacheWarning,
		CacheWarningVisibility: transcript.EntryVisibilityAll,
		CacheWarning: &cachewarn.Warning{
			Scope:           cachewarn.ScopeReviewer,
			Reason:          cachewarn.ReasonNonPostfix,
			CacheKey:        "reviewer-cache-key",
			LostInputTokens: 12_000,
		},
	})
	if event.CacheWarning == nil {
		t.Fatal("expected projected cache warning")
	}
	if event.CacheWarning.LostInputTokens != 12_000 {
		t.Fatalf("cache warning lost input tokens = %d, want 12000", event.CacheWarning.LostInputTokens)
	}
	if event.CacheWarning.Scope != cachewarn.ScopeReviewer {
		t.Fatalf("cache warning scope = %q, want %q", event.CacheWarning.Scope, cachewarn.ScopeReviewer)
	}
	if event.CacheWarningVisibility != clientui.EntryVisibilityAll {
		t.Fatalf("cache warning visibility = %q, want %q", event.CacheWarningVisibility, clientui.EntryVisibilityAll)
	}
	if len(event.TranscriptEntries) != 1 {
		t.Fatalf("expected one projected transcript entry, got %d", len(event.TranscriptEntries))
	}
	if entry := event.TranscriptEntries[0]; entry.Role != "cache_warning" || entry.Visibility != clientui.EntryVisibilityAll {
		t.Fatalf("unexpected projected cache warning entry: %+v", entry)
	}
}

func TestChatSnapshotFromRuntimeCopiesEntries(t *testing.T) {
	toolCall := &transcript.ToolCallMeta{
		ToolName:    "shell",
		Suggestions: []string{"a", "b"},
	}
	snapshot := ChatSnapshotFromRuntime(runtime.ChatSnapshot{
		Entries: []runtime.ChatEntry{{
			Visibility:  transcript.EntryVisibilityDetailOnly,
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
	if entry.Visibility != clientui.EntryVisibilityDetailOnly {
		t.Fatalf("entry visibility = %q, want %q", entry.Visibility, clientui.EntryVisibilityDetailOnly)
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

func TestTranscriptPageFromChatClonesPatchRender(t *testing.T) {
	snapshot := clientui.ChatSnapshot{Entries: []clientui.ChatEntry{{
		Role: "tool_call",
		ToolCall: &clientui.ToolCallMeta{
			PatchRender: &patchformat.RenderedPatch{
				SummaryLines: []patchformat.RenderedLine{{Text: "before"}},
			},
		},
	}}}

	page := TranscriptPageFromChat("session-1", "session", clientui.ConversationFreshnessEstablished, 1, snapshot, clientui.TranscriptPageRequest{})
	if len(page.Entries) != 1 || page.Entries[0].ToolCall == nil || page.Entries[0].ToolCall.PatchRender == nil {
		t.Fatalf("expected patch render copied into transcript page, got %+v", page.Entries)
	}
	snapshot.Entries[0].ToolCall.PatchRender.SummaryLines[0].Text = "after"
	if page.Entries[0].ToolCall.PatchRender.SummaryLines[0].Text != "before" {
		t.Fatalf("expected transcript page to deep copy patch render, got %+v", page.Entries[0].ToolCall.PatchRender.SummaryLines)
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

func TestTranscriptPageFromRuntimeUsesOngoingTailWindowByDefault(t *testing.T) {
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

	page := TranscriptPageFromRuntime(eng, clientui.TranscriptPageRequest{})
	if page.TotalEntries != 600 {
		t.Fatalf("total entries = %d, want 600", page.TotalEntries)
	}
	if page.Offset != 100 {
		t.Fatalf("offset = %d, want 100", page.Offset)
	}
	if page.HasMore {
		t.Fatalf("expected default transcript request to return ongoing tail, got %+v", page)
	}
	if len(page.Entries) != 500 {
		t.Fatalf("entries = %d, want 500", len(page.Entries))
	}
}

func TestTranscriptPageFromRuntimeUsesPagedSnapshotForOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	for i := 0; i < 600; i++ {
		if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("reply-%03d", i), Phase: llm.MessagePhaseFinal}); err != nil {
			t.Fatalf("append message %d: %v", i, err)
		}
	}
	eng, err := runtime.New(store, projectionFastClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	page := TranscriptPageFromRuntime(eng, clientui.TranscriptPageRequest{Offset: 550, Limit: 25})
	if page.TotalEntries != 600 {
		t.Fatalf("total entries = %d, want 600", page.TotalEntries)
	}
	if page.Offset != 550 {
		t.Fatalf("offset = %d, want 550", page.Offset)
	}
	if !page.HasMore || page.NextOffset != 575 {
		t.Fatalf("unexpected pagination metadata: %+v", page)
	}
	if len(page.Entries) != 25 {
		t.Fatalf("entries = %d, want 25", len(page.Entries))
	}
	if first := page.Entries[0].Text; first != "reply-550" {
		t.Fatalf("first entry = %q, want reply-550", first)
	}
	if last := page.Entries[len(page.Entries)-1].Text; last != "reply-574" {
		t.Fatalf("last entry = %q, want reply-574", last)
	}
}
