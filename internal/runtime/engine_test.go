package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	name  tools.ID
	delay time.Duration
}

func (t fakeTool) Name() tools.ID { return t.name }
func (t fakeTool) Call(_ context.Context, c tools.Call) (tools.Result, error) {
	time.Sleep(t.delay)
	out, _ := json.Marshal(map[string]any{"tool": string(t.name)})
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

type authFailClient struct {
	mu    sync.Mutex
	calls int
}

func (c *authFailClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return llm.Response{}, &llm.APIStatusError{StatusCode: 401, Body: `{"error":"invalid_api_key"}`}
}

func (c *authFailClient) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type statusFailClient struct {
	mu     sync.Mutex
	calls  int
	status int
}

func (c *statusFailClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	c.mu.Lock()
	c.calls++
	status := c.status
	c.mu.Unlock()
	return llm.Response{}, &llm.APIStatusError{StatusCode: status, Body: `{"error":"request_failed"}`}
}

func (c *statusFailClient) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:         "gpt-5",
		Temperature:   1,
		ThinkingLevel: "xhigh",
		EnabledTools:  []tools.ID{tools.ToolShell},
	})
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
	if meta.Locked.ThinkingLevel != "xhigh" {
		t.Fatalf("locked thinking level = %q", meta.Locked.ThinkingLevel)
	}
	if len(meta.Locked.EnabledTools) != 1 || meta.Locked.EnabledTools[0] != string(tools.ToolShell) {
		t.Fatalf("locked enabled tools = %+v", meta.Locked.EnabledTools)
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
				{ID: "a", Name: string(tools.ToolShell), Input: json.RawMessage(`{}`)},
				{ID: "b", Name: string(tools.ToolPatch), Input: json.RawMessage(`{}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	eng, err := New(store, client, tools.NewRegistry(
		fakeTool{name: tools.ToolShell, delay: 40 * time.Millisecond},
		fakeTool{name: tools.ToolPatch, delay: 1 * time.Millisecond},
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

func TestExecuteToolCallsFailsOnToolCompletionPersistence(t *testing.T) {
	tests := []struct {
		name     string
		registry *tools.Registry
		callName string
	}{
		{
			name:     "unknown tool name",
			registry: tools.NewRegistry(),
			callName: "not_a_tool",
		},
		{
			name:     "known tool without handler",
			registry: tools.NewRegistry(),
			callName: string(tools.ToolShell),
		},
		{
			name:     "registered tool handler",
			registry: tools.NewRegistry(fakeTool{name: tools.ToolShell}),
			callName: string(tools.ToolShell),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := session.Create(dir, "ws", dir)
			if err != nil {
				t.Fatalf("create store: %v", err)
			}

			eng, err := New(store, &fakeClient{}, tc.registry, Config{Model: "gpt-5"})
			if err != nil {
				t.Fatalf("new engine: %v", err)
			}

			sessionDir := store.Dir()
			if err := os.Chmod(sessionDir, 0o555); err != nil {
				t.Fatalf("chmod read-only session dir: %v", err)
			}
			defer func() {
				_ = os.Chmod(sessionDir, 0o755)
			}()

			_, err = eng.executeToolCalls(context.Background(), "step", []llm.ToolCall{
				{ID: "call-1", Name: tc.callName, Input: json.RawMessage(`{}`)},
			})
			if err == nil {
				t.Fatal("expected persistence failure")
			}
			if !strings.Contains(err.Error(), "persist tool completion") {
				t.Fatalf("expected persistence error, got %v", err)
			}

			if len(eng.chat.toolCompletions) != 0 {
				t.Fatalf("expected no in-memory tool completions when persistence fails, got %+v", eng.chat.toolCompletions)
			}
		})
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
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

func TestAuthErrorsAreNotRetried(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &authFailClient{}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	_, err = eng.SubmitUserMessage(context.Background(), "trigger auth error")
	if err == nil {
		t.Fatal("expected auth failure")
	}
	if client.Calls() != 1 {
		t.Fatalf("expected single model attempt on auth error, got %d", client.Calls())
	}
}

func TestNonRetriableStatusCodesAreNotRetried(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			dir := t.TempDir()
			store, err := session.Create(dir, "ws", dir)
			if err != nil {
				t.Fatalf("create store: %v", err)
			}

			client := &statusFailClient{status: status}
			eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
				Model: "gpt-5",
			})
			if err != nil {
				t.Fatalf("new engine: %v", err)
			}

			_, err = eng.SubmitUserMessage(context.Background(), "trigger status error")
			if err == nil {
				t.Fatalf("expected status %d failure", status)
			}
			if client.Calls() != 1 {
				t.Fatalf("expected single model attempt on status %d, got %d", status, client.Calls())
			}
		})
	}
}

func TestInjectsGlobalAndWorkspaceAgentsAfterExistingMessagesAndBeforeFirstUserMessage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	globalDir := filepath.Join(home, ".builder")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global dir: %v", err)
	}
	globalPath := filepath.Join(globalDir, "AGENTS.md")
	if err := os.WriteFile(globalPath, []byte("global instructions"), 0o644); err != nil {
		t.Fatalf("write global AGENTS.md: %v", err)
	}

	workspace := t.TempDir()
	workspacePath := filepath.Join(workspace, "AGENTS.md")
	if err := os.WriteFile(workspacePath, []byte("workspace instructions"), 0o644); err != nil {
		t.Fatalf("write workspace AGENTS.md: %v", err)
	}

	storeRoot := t.TempDir()
	store, err := session.Create(storeRoot, "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("prior-step", "message", llm.Message{
		Role:    llm.RoleDeveloper,
		Content: "existing context",
	}); err != nil {
		t.Fatalf("append existing message: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok-1"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok-2"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "first"); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "second"); err != nil {
		t.Fatalf("second submit: %v", err)
	}

	if len(client.calls) < 2 {
		t.Fatalf("expected 2 model calls, got %d", len(client.calls))
	}

	firstReq := client.calls[0]
	if len(firstReq.Messages) < 4 {
		t.Fatalf("expected at least 4 messages in first request, got %d", len(firstReq.Messages))
	}
	if firstReq.Messages[0].Role != llm.RoleDeveloper || firstReq.Messages[0].Content != "existing context" {
		t.Fatalf("expected first message to be existing context, got %+v", firstReq.Messages[0])
	}
	if firstReq.Messages[1].Role != llm.RoleDeveloper || !strings.Contains(firstReq.Messages[1].Content, "source: "+globalPath) {
		t.Fatalf("expected second message to be global developer AGENTS injection, got %+v", firstReq.Messages[1])
	}
	if firstReq.Messages[2].Role != llm.RoleDeveloper || !strings.Contains(firstReq.Messages[2].Content, "source: "+workspacePath) {
		t.Fatalf("expected third message to be workspace developer AGENTS injection, got %+v", firstReq.Messages[2])
	}
	if firstReq.Messages[3].Role != llm.RoleUser || firstReq.Messages[3].Content != "first" {
		t.Fatalf("expected user message after AGENTS injections, got %+v", firstReq.Messages[3])
	}

	secondReq := client.calls[1]
	injectedCount := 0
	for _, msg := range secondReq.Messages {
		if msg.Role == llm.RoleDeveloper && strings.Contains(msg.Content, agentsInjectedHeader) {
			injectedCount++
		}
	}
	if injectedCount != 2 {
		t.Fatalf("expected exactly two injected AGENTS developer messages to persist, got %d", injectedCount)
	}
}

func TestQueuedUserMessageFlushesWhenAssistantReturnsWithoutTools(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "first"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "after flush"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	var seenFlushed bool
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			if evt.Kind == EventUserMessageFlushed && evt.UserMessage == "steer now" {
				seenFlushed = true
			}
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	eng.QueueUserMessage("steer now")
	msg, err := eng.SubmitUserMessage(context.Background(), "start")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "after flush" {
		t.Fatalf("assistant content = %q, want after flush", msg.Content)
	}
	if !seenFlushed {
		t.Fatal("expected user_message_flushed event")
	}
	if len(client.calls) < 2 {
		t.Fatalf("expected at least 2 model calls, got %d", len(client.calls))
	}
	second := client.calls[1]
	hasInjected := false
	for _, m := range second.Messages {
		if m.Role == llm.RoleUser && m.Content == "steer now" {
			hasInjected = true
			break
		}
	}
	if !hasInjected {
		t.Fatalf("expected flushed user message in second request, messages=%+v", second.Messages)
	}
}

func TestRequestMessagesNeverContainANSIEscapes(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "raw \x1b[31mansi\x1b[0m"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "plain user"); err != nil {
		t.Fatalf("submit: %v", err)
	}

	if len(client.calls) == 0 {
		t.Fatal("expected at least one model call")
	}

	for _, req := range client.calls {
		for _, msg := range req.Messages {
			if strings.Contains(msg.Content, "\x1b[") {
				t.Fatalf("request message contains ANSI escape sequence: role=%s content=%q", msg.Role, msg.Content)
			}
		}
	}
}

func TestReasoningSummaryVisibleAndEncryptedReasoningRoundTrips(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "first"},
			Reasoning: []llm.ReasoningEntry{
				{Role: "reasoning", Text: "Plan summary"},
			},
			ReasoningItems: []llm.ReasoningItem{
				{ID: "rs_1", EncryptedContent: "enc_1"},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "second"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "one"); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "two"); err != nil {
		t.Fatalf("second submit: %v", err)
	}

	if len(client.calls) < 2 {
		t.Fatalf("expected two model calls, got %d", len(client.calls))
	}
	secondReq := client.calls[1]
	foundReasoningItem := false
	for _, msg := range secondReq.Messages {
		if msg.Role != llm.RoleAssistant || msg.Content != "first" {
			continue
		}
		if len(msg.ReasoningItems) == 1 &&
			msg.ReasoningItems[0].ID == "rs_1" &&
			msg.ReasoningItems[0].EncryptedContent == "enc_1" {
			foundReasoningItem = true
		}
	}
	if !foundReasoningItem {
		t.Fatalf("expected prior assistant message to carry encrypted reasoning item, got %+v", secondReq.Messages)
	}
	for _, msg := range secondReq.Messages {
		if strings.Contains(msg.Content, "Plan summary") {
			t.Fatalf("reasoning summary text should not be sent back to model input, found in %+v", secondReq.Messages)
		}
	}

	snap := eng.ChatSnapshot()
	sawSummary := false
	for _, entry := range snap.Entries {
		if entry.Role == "reasoning" && strings.Contains(entry.Text, "Plan summary") {
			sawSummary = true
			break
		}
	}
	if !sawSummary {
		t.Fatalf("expected reasoning summary in chat snapshot entries, got %+v", snap.Entries)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	sawLocal := false
	for _, evt := range events {
		if evt.Kind != "local_entry" {
			continue
		}
		var entry storedLocalEntry
		if err := json.Unmarshal(evt.Payload, &entry); err != nil {
			t.Fatalf("decode local_entry: %v", err)
		}
		if entry.Role == "reasoning" && entry.Text == "Plan summary" {
			sawLocal = true
		}
	}
	if !sawLocal {
		t.Fatalf("expected persisted local_entry for reasoning summary, events=%+v", events)
	}
}
