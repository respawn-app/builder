package sessionview

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/runtimeview"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/serverapi"
)

type serviceFakeLLM struct {
	responses []llm.Response
}

func (f *serviceFakeLLM) Generate(context.Context, llm.Request) (llm.Response, error) {
	if len(f.responses) == 0 {
		return llm.Response{}, nil
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func (f *serviceFakeLLM) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}, nil
}

type serviceBlockingTool struct {
	started chan struct{}
	release chan struct{}
}

func (serviceBlockingTool) Name() tools.ID { return tools.ToolShell }

func (t serviceBlockingTool) Call(_ context.Context, c tools.Call) (tools.Result, error) {
	select {
	case <-t.started:
	default:
		close(t.started)
	}
	<-t.release
	out, _ := json.Marshal(map[string]any{"ok": true})
	return tools.Result{CallID: c.ID, Name: c.Name, Output: out}, nil
}

func TestServiceGetSessionMainViewUsesLiveRuntimeWhenAttached(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	client := &serviceFakeLLM{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)}},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	eng, err := runtime.New(store, client, tools.NewRegistry(serviceBlockingTool{started: started, release: release}), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	svc := NewService(NewStaticSessionResolver(store), NewStaticRuntimeResolver(eng))

	done := make(chan error, 1)
	go func() {
		_, submitErr := eng.SubmitUserMessage(context.Background(), "run tools")
		done <- submitErr
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for active run")
	}

	resp, err := svc.GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("get session main view: %v", err)
	}
	if resp.MainView.ActiveRun == nil || resp.MainView.ActiveRun.Status != "running" {
		t.Fatalf("expected live active run, got %+v", resp.MainView.ActiveRun)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("submit user message: %v", err)
	}
}

func TestServiceGetSessionMainViewFallsBackToDurableSessionState(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.SetName("incident triage"); err != nil {
		t.Fatalf("set name: %v", err)
	}
	if err := store.SetParentSessionID("parent-1"); err != nil {
		t.Fatalf("set parent session id: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "hello"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: "final answer", Phase: llm.MessagePhaseFinal}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	startedAt := time.Now().UTC().Add(-time.Minute)
	if _, err := store.AppendRunStarted(session.RunRecord{RunID: "run-1", StepID: "step-1", StartedAt: startedAt}); err != nil {
		t.Fatalf("append run start: %v", err)
	}

	svc := NewService(NewStaticSessionResolver(store), nil)
	resp, err := svc.GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("get session main view: %v", err)
	}
	if resp.MainView.Session.SessionID != store.Meta().SessionID || resp.MainView.Session.SessionName != "incident triage" {
		t.Fatalf("unexpected dormant session view: %+v", resp.MainView.Session)
	}
	if len(resp.MainView.Session.Chat.Entries) != 0 {
		t.Fatalf("expected main view to omit transcript payload, got %+v", resp.MainView.Session.Chat)
	}
	if resp.MainView.Status.ParentSessionID != "parent-1" || resp.MainView.Status.LastCommittedAssistantFinalAnswer != "final answer" {
		t.Fatalf("unexpected dormant status: %+v", resp.MainView.Status)
	}
	if resp.MainView.ActiveRun == nil || resp.MainView.ActiveRun.RunID != "run-1" || resp.MainView.ActiveRun.Status != "running" {
		t.Fatalf("expected durable running active run, got %+v", resp.MainView.ActiveRun)
	}
	if resp.MainView.Session.Transcript.Revision != store.Meta().LastSequence {
		t.Fatalf("transcript revision = %d, want %d", resp.MainView.Session.Transcript.Revision, store.Meta().LastSequence)
	}
	if resp.MainView.Session.Transcript.CommittedEntryCount != 2 {
		t.Fatalf("committed entry count = %d, want 2", resp.MainView.Session.Transcript.CommittedEntryCount)
	}
}

func TestServiceGetSessionTranscriptPageUsesLiveRuntimeWhenAttached(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.SetName("incident triage"); err != nil {
		t.Fatalf("set name: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "hello"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: "one", Phase: llm.MessagePhaseFinal}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	eng, err := runtime.New(store, &serviceFakeLLM{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.AppendLocalEntry("assistant", "two")
	svc := NewService(NewStaticSessionResolver(store), NewStaticRuntimeResolver(eng))

	resp, err := svc.GetSessionTranscriptPage(context.Background(), serverapi.SessionTranscriptPageRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("get session transcript page: %v", err)
	}
	if resp.Transcript.SessionName != "incident triage" {
		t.Fatalf("session name = %q, want incident triage", resp.Transcript.SessionName)
	}
	if resp.Transcript.Revision != store.Meta().LastSequence {
		t.Fatalf("revision = %d, want %d", resp.Transcript.Revision, store.Meta().LastSequence)
	}
	if len(resp.Transcript.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(resp.Transcript.Entries))
	}
	if resp.Transcript.Entries[2].Text != "two" {
		t.Fatalf("unexpected tail entry: %+v", resp.Transcript.Entries[2])
	}
}

func TestServiceGetSessionTranscriptPageSupportsPagination(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.SetName("incident triage"); err != nil {
		t.Fatalf("set name: %v", err)
	}
	entries := []llm.Message{
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a1", Phase: llm.MessagePhaseFinal},
		{Role: llm.RoleUser, Content: "u2"},
		{Role: llm.RoleAssistant, Content: "a2", Phase: llm.MessagePhaseFinal},
	}
	for i, entry := range entries {
		if _, err := store.AppendEvent("step-1", "message", entry); err != nil {
			t.Fatalf("append message %d: %v", i, err)
		}
	}
	svc := NewService(NewStaticSessionResolver(store), nil)

	resp, err := svc.GetSessionTranscriptPage(context.Background(), serverapi.SessionTranscriptPageRequest{SessionID: store.Meta().SessionID, Offset: 1, Limit: 2})
	if err != nil {
		t.Fatalf("get session transcript page: %v", err)
	}
	if resp.Transcript.TotalEntries != 4 {
		t.Fatalf("total entries = %d, want 4", resp.Transcript.TotalEntries)
	}
	if !resp.Transcript.HasMore || resp.Transcript.NextOffset != 3 {
		t.Fatalf("unexpected pagination metadata: %+v", resp.Transcript)
	}
	if len(resp.Transcript.Entries) != 2 || resp.Transcript.Entries[0].Text != "a1" || resp.Transcript.Entries[1].Text != "u2" {
		t.Fatalf("unexpected transcript page entries: %+v", resp.Transcript.Entries)
	}
}

func TestServiceGetSessionTranscriptPageUsesDormantOngoingTailByDefault(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	for i := 0; i < runtimeview.OngoingTailEntryLimit+20; i++ {
		entry := llm.Message{Role: llm.RoleUser, Content: "u" + strconv.Itoa(i)}
		if _, err := store.AppendEvent("step-1", "message", entry); err != nil {
			t.Fatalf("append message %d: %v", i, err)
		}
	}
	svc := NewService(NewStaticSessionResolver(store), nil)

	resp, err := svc.GetSessionTranscriptPage(context.Background(), serverapi.SessionTranscriptPageRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("get session transcript page: %v", err)
	}
	if resp.Transcript.TotalEntries != runtimeview.OngoingTailEntryLimit+20 {
		t.Fatalf("total entries = %d, want %d", resp.Transcript.TotalEntries, runtimeview.OngoingTailEntryLimit+20)
	}
	if resp.Transcript.Offset != 20 {
		t.Fatalf("offset = %d, want 20", resp.Transcript.Offset)
	}
	if len(resp.Transcript.Entries) != runtimeview.OngoingTailEntryLimit {
		t.Fatalf("entries = %d, want %d", len(resp.Transcript.Entries), runtimeview.OngoingTailEntryLimit)
	}
	if resp.Transcript.HasMore || resp.Transcript.NextOffset != 0 {
		t.Fatalf("unexpected pagination metadata: %+v", resp.Transcript)
	}
	if first := resp.Transcript.Entries[0].Text; first != "u20" {
		t.Fatalf("first dormant tail entry = %q, want u20", first)
	}
	if last := resp.Transcript.Entries[len(resp.Transcript.Entries)-1].Text; last != fmt.Sprintf("u%d", runtimeview.OngoingTailEntryLimit+19) {
		t.Fatalf("last dormant tail entry = %q", last)
	}
}

func TestServiceGetSessionTranscriptPageKeepsDormantCompactionSummaryAndCarryover(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "before compaction"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "history_replaced", map[string]any{
		"engine": "local",
		"mode":   "manual",
		"items":  llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleUser, Content: "condensed provider summary", MessageType: llm.MessageTypeCompactionSummary}}),
	}); err != nil {
		t.Fatalf("append history replacement: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "local_entry", map[string]any{"role": "compaction_summary", "text": "condensed summary"}); err != nil {
		t.Fatalf("append compaction summary entry: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeManualCompactionCarryover, Content: "Last user message before handoff\n\ncarry this forward"}); err != nil {
		t.Fatalf("append manual carryover: %v", err)
	}
	svc := NewService(NewStaticSessionResolver(store), nil)

	resp, err := svc.GetSessionTranscriptPage(context.Background(), serverapi.SessionTranscriptPageRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("get session transcript page: %v", err)
	}
	if len(resp.Transcript.Entries) != 3 {
		t.Fatalf("entries = %d, want 3 (%+v)", len(resp.Transcript.Entries), resp.Transcript.Entries)
	}
	if resp.Transcript.Entries[1].Role != "compaction_summary" || resp.Transcript.Entries[1].Text != "condensed summary" {
		t.Fatalf("expected compaction summary entry, got %+v", resp.Transcript.Entries[1])
	}
	if resp.Transcript.Entries[2].Role != "manual_compaction_carryover" {
		t.Fatalf("expected manual carryover entry, got %+v", resp.Transcript.Entries[2])
	}
}

func TestServiceGetSessionTranscriptPageUsesDormantOngoingTailWindow(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	for i := 0; i < runtimeview.OngoingTailEntryLimit+20; i++ {
		if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "u" + strconv.Itoa(i)}); err != nil {
			t.Fatalf("append message %d: %v", i, err)
		}
	}
	svc := NewService(NewStaticSessionResolver(store), nil)

	resp, err := svc.GetSessionTranscriptPage(context.Background(), serverapi.SessionTranscriptPageRequest{
		SessionID: store.Meta().SessionID,
		Window:    "ongoing_tail",
	})
	if err != nil {
		t.Fatalf("get session transcript page: %v", err)
	}
	if resp.Transcript.TotalEntries != runtimeview.OngoingTailEntryLimit+20 {
		t.Fatalf("total entries = %d, want %d", resp.Transcript.TotalEntries, runtimeview.OngoingTailEntryLimit+20)
	}
	if resp.Transcript.Offset != 20 {
		t.Fatalf("offset = %d, want 20", resp.Transcript.Offset)
	}
	if len(resp.Transcript.Entries) != runtimeview.OngoingTailEntryLimit {
		t.Fatalf("entries = %d, want %d", len(resp.Transcript.Entries), runtimeview.OngoingTailEntryLimit)
	}
	if first := resp.Transcript.Entries[0].Text; first != "u20" {
		t.Fatalf("first tail entry = %q, want u20", first)
	}
	if last := resp.Transcript.Entries[len(resp.Transcript.Entries)-1].Text; last != "u519" {
		t.Fatalf("last tail entry = %q, want u519", last)
	}
}

func TestServiceGetRunReturnsDurableRunRecord(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	startedAt := time.Now().UTC().Add(-time.Minute)
	finishedAt := startedAt.Add(10 * time.Second)
	if _, err := store.AppendRunStarted(session.RunRecord{RunID: "run-1", StepID: "step-1", StartedAt: startedAt}); err != nil {
		t.Fatalf("append run start: %v", err)
	}
	if _, err := store.AppendRunFinished(session.RunRecord{RunID: "run-1", StepID: "step-1", Status: session.RunStatusCompleted, StartedAt: startedAt, FinishedAt: finishedAt}); err != nil {
		t.Fatalf("append run finish: %v", err)
	}

	svc := NewService(NewStaticSessionResolver(store), nil)
	resp, err := svc.GetRun(context.Background(), serverapi.RunGetRequest{SessionID: store.Meta().SessionID, RunID: "run-1"})
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if resp.Run == nil || resp.Run.RunID != "run-1" || resp.Run.Status != "completed" {
		t.Fatalf("unexpected run response: %+v", resp.Run)
	}
}

func TestServiceGetSessionMainViewDoesNotMutatePersistedSessionFiles(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "hello"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	startedAt := time.Now().UTC().Add(-time.Minute)
	if _, err := store.AppendRunStarted(session.RunRecord{RunID: "run-1", StepID: "step-1", StartedAt: startedAt}); err != nil {
		t.Fatalf("append run start: %v", err)
	}
	if err := store.MarkInFlight(true); err != nil {
		t.Fatalf("mark in-flight: %v", err)
	}

	sessionPath := filepath.Join(store.Dir(), "session.json")
	eventsPath := filepath.Join(store.Dir(), "events.jsonl")
	beforeSession, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read session file before: %v", err)
	}
	beforeEvents, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("read events file before: %v", err)
	}

	svc := NewService(NewStaticSessionResolver(store), nil)
	resp, err := svc.GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("get session main view: %v", err)
	}
	if resp.MainView.ActiveRun == nil || resp.MainView.ActiveRun.RunID != "run-1" {
		t.Fatalf("expected durable running active run, got %+v", resp.MainView.ActiveRun)
	}

	afterSession, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read session file after: %v", err)
	}
	afterEvents, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("read events file after: %v", err)
	}
	if string(beforeSession) != string(afterSession) {
		t.Fatalf("session file mutated during read\nbefore=%s\nafter=%s", string(beforeSession), string(afterSession))
	}
	if string(beforeEvents) != string(afterEvents) {
		t.Fatalf("events file mutated during read\nbefore=%s\nafter=%s", string(beforeEvents), string(afterEvents))
	}
}
