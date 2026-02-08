package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"builder/internal/llm"
	"builder/internal/session"
	"builder/internal/tools"
)

type fakeClient struct {
	mu        sync.Mutex
	responses []llm.Response
	calls     []llm.Request
}

func (f *fakeClient) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if len(f.responses) == 0 {
		return llm.Response{}, nil
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

type fakeTool struct {
	name  string
	delay time.Duration
}

func (t fakeTool) Name() string { return t.name }
func (t fakeTool) Definition() tools.Definition {
	return tools.Definition{Name: t.name, Schema: json.RawMessage(`{"type":"object"}`)}
}
func (t fakeTool) Call(_ context.Context, c tools.Call) (tools.Result, error) {
	time.Sleep(t.delay)
	out, _ := json.Marshal(map[string]any{"tool": t.name})
	return tools.Result{CallID: c.ID, Name: c.Name, Output: out}, nil
}

type fakeStreamClient struct {
	mu       sync.Mutex
	attempts int
	calls    []llm.Request
}

func (f *fakeStreamClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
}

func (f *fakeStreamClient) GenerateStream(_ context.Context, req llm.Request, onDelta func(string)) (llm.Response, error) {
	f.mu.Lock()
	attempt := f.attempts
	f.attempts++
	f.calls = append(f.calls, req)
	f.mu.Unlock()

	switch attempt {
	case 0:
		if onDelta != nil {
			onDelta("partial")
		}
		return llm.Response{}, errors.New("transient stream failure")
	default:
		if onDelta != nil {
			onDelta("final")
		}
		return llm.Response{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "final"},
			Usage:     llm.Usage{WindowTokens: 200000},
		}, nil
	}
}

func TestLocksAtFirstDispatch(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: "bash"}), Config{Model: "gpt-5", Temperature: 1})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "hi"); err != nil {
		t.Fatalf("submit: %v", err)
	}

	meta := store.Meta()
	if meta.Locked == nil {
		t.Fatalf("expected locked contract after first dispatch")
	}
	if meta.Locked.Model != "gpt-5" {
		t.Fatalf("locked model = %q", meta.Locked.Model)
	}
}

func TestParallelToolsReturnDeclaredOrder(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working"},
			ToolCalls: []llm.ToolCall{
				{ID: "a", Name: "slow", Input: json.RawMessage(`{}`)},
				{ID: "b", Name: "fast", Input: json.RawMessage(`{}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	eng, err := New(store, client, tools.NewRegistry(
		fakeTool{name: "slow", delay: 40 * time.Millisecond},
		fakeTool{name: "fast", delay: 1 * time.Millisecond},
	), Config{Model: "gpt-5", Temperature: 1})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "run tools"); err != nil {
		t.Fatalf("submit: %v", err)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}

	toolMessages := []llm.Message{}
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var msg llm.Message
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			t.Fatalf("decode message: %v", err)
		}
		if msg.Role == llm.RoleTool {
			toolMessages = append(toolMessages, msg)
		}
	}

	if len(toolMessages) != 2 {
		t.Fatalf("tool message count = %d, want 2", len(toolMessages))
	}
	if toolMessages[0].ToolCallID != "a" || toolMessages[1].ToolCallID != "b" {
		t.Fatalf("tool order mismatch: first=%s second=%s", toolMessages[0].ToolCallID, toolMessages[1].ToolCallID)
	}

	if len(client.calls) < 2 {
		t.Fatalf("expected at least 2 model requests, got %d", len(client.calls))
	}
	secondReq := client.calls[1]
	foundAssistantWithCalls := false
	for _, msg := range secondReq.Messages {
		if msg.Role == llm.RoleAssistant && len(msg.ToolCalls) == 2 {
			if msg.ToolCalls[0].ID == "a" && msg.ToolCalls[1].ID == "b" {
				foundAssistantWithCalls = true
				break
			}
		}
	}
	if !foundAssistantWithCalls {
		t.Fatalf("second request is missing assistant tool call metadata: %+v", secondReq.Messages)
	}

}

func TestStreamingRetryResetsAttemptDeltas(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeStreamClient{}

	var (
		mu     sync.Mutex
		events []Event
	)
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: "noop"}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "retry stream")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "final" {
		t.Fatalf("assistant content = %q, want final", msg.Content)
	}

	mu.Lock()
	defer mu.Unlock()

	firstDelta := -1
	reset := -1
	secondDelta := -1
	for i, evt := range events {
		if evt.Kind == EventAssistantDelta && evt.AssistantDelta == "partial" && firstDelta == -1 {
			firstDelta = i
		}
		if evt.Kind == EventAssistantDeltaReset && reset == -1 {
			reset = i
		}
		if evt.Kind == EventAssistantDelta && evt.AssistantDelta == "final" && secondDelta == -1 {
			secondDelta = i
		}
	}

	if firstDelta == -1 {
		t.Fatalf("missing first attempt delta event: %+v", events)
	}
	if reset == -1 {
		t.Fatalf("missing reset event: %+v", events)
	}
	if secondDelta == -1 {
		t.Fatalf("missing second attempt delta event: %+v", events)
	}
	if !(firstDelta < reset && reset < secondDelta) {
		t.Fatalf("unexpected delta/reset ordering first=%d reset=%d second=%d", firstDelta, reset, secondDelta)
	}
}
