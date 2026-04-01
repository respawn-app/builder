package sessionview

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/serverapi"
)

type serviceFastClient struct{}

type serviceFakeLLM struct {
	responses []llm.Response
}

func (serviceFastClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, nil
}

func (serviceFastClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}, nil
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
	svc := NewService(store, eng)

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

	svc := NewService(store, nil)
	resp, err := svc.GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("get session main view: %v", err)
	}
	if resp.MainView.Session.SessionID != store.Meta().SessionID || resp.MainView.Session.SessionName != "incident triage" {
		t.Fatalf("unexpected dormant session view: %+v", resp.MainView.Session)
	}
	if len(resp.MainView.Session.Chat.Entries) != 2 {
		t.Fatalf("expected restored chat entries, got %+v", resp.MainView.Session.Chat)
	}
	if resp.MainView.Status.ParentSessionID != "parent-1" || resp.MainView.Status.LastCommittedAssistantFinalAnswer != "final answer" {
		t.Fatalf("unexpected dormant status: %+v", resp.MainView.Status)
	}
	if resp.MainView.ActiveRun == nil || resp.MainView.ActiveRun.RunID != "run-1" || resp.MainView.ActiveRun.Status != "running" {
		t.Fatalf("expected durable running active run, got %+v", resp.MainView.ActiveRun)
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

	svc := NewService(store, nil)
	resp, err := svc.GetRun(context.Background(), serverapi.RunGetRequest{SessionID: store.Meta().SessionID, RunID: "run-1"})
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if resp.Run == nil || resp.Run.RunID != "run-1" || resp.Run.Status != "completed" {
		t.Fatalf("unexpected run response: %+v", resp.Run)
	}
}
