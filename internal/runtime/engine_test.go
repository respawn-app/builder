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
	"builder/prompts"
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

type fakeCompactionClient struct {
	mu sync.Mutex

	responses []llm.Response
	calls     []llm.Request

	compactionResponses []llm.CompactionResponse
	compactionErr       error
	compactionErrors    []error
	compactionCalls     []llm.CompactionRequest

	caps llm.ProviderCapabilities
}

func (f *fakeCompactionClient) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
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

func (f *fakeCompactionClient) Compact(_ context.Context, req llm.CompactionRequest) (llm.CompactionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.compactionCalls = append(f.compactionCalls, req)
	if len(f.compactionErrors) > 0 {
		err := f.compactionErrors[0]
		f.compactionErrors = f.compactionErrors[1:]
		if err != nil {
			return llm.CompactionResponse{}, err
		}
	}
	if f.compactionErr != nil {
		return llm.CompactionResponse{}, f.compactionErr
	}
	if len(f.compactionResponses) == 0 {
		return llm.CompactionResponse{}, nil
	}
	resp := f.compactionResponses[0]
	f.compactionResponses = f.compactionResponses[1:]
	return resp, nil
}

func (f *fakeCompactionClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	if strings.TrimSpace(f.caps.ProviderID) == "" {
		return llm.ProviderCapabilities{
			ProviderID:                    "openai",
			SupportsResponsesAPI:          true,
			SupportsResponsesCompact:      true,
			SupportsReasoningEncrypted:    true,
			SupportsServerSideContextEdit: true,
			IsOpenAIFirstParty:            true,
		}, nil
	}
	return f.caps, nil
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

func TestSubmitUserMessageSurfacesInFlightClearFailure(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	sessionDir := store.Dir()
	defer func() {
		_ = os.Chmod(sessionDir, 0o755)
	}()

	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	var (
		mu         sync.Mutex
		events     []Event
		chmodDone  bool
		chmodError error
	)
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			mu.Lock()
			events = append(events, evt)
			shouldLockDir := evt.Kind == EventAssistantMessage && !chmodDone
			if shouldLockDir {
				chmodDone = true
			}
			mu.Unlock()
			if shouldLockDir {
				if chmodErr := os.Chmod(sessionDir, 0o555); chmodErr != nil {
					mu.Lock()
					if chmodError == nil {
						chmodError = chmodErr
					}
					mu.Unlock()
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "hi")
	if msg.Content != "done" {
		t.Fatalf("assistant content = %q, want done", msg.Content)
	}
	if err == nil {
		t.Fatal("expected in-flight clear failure")
	}
	if !strings.Contains(err.Error(), "mark in-flight false") {
		t.Fatalf("expected mark in-flight clear error, got %v", err)
	}

	mu.Lock()
	gotChmodDone := chmodDone
	gotChmodErr := chmodError
	seenClearFailureEvent := false
	for _, evt := range events {
		if evt.Kind == EventInFlightClearFailed && strings.Contains(evt.Error, "mark in-flight false") {
			seenClearFailureEvent = true
			break
		}
	}
	mu.Unlock()

	if !gotChmodDone {
		t.Fatal("expected permission flip hook to run")
	}
	if gotChmodErr != nil {
		t.Fatalf("chmod hook failed: %v", gotChmodErr)
	}
	if !seenClearFailureEvent {
		t.Fatalf("expected %s event, got %+v", EventInFlightClearFailed, events)
	}

	reopened, openErr := session.Open(sessionDir)
	if openErr != nil {
		t.Fatalf("re-open session store: %v", openErr)
	}
	if !reopened.Meta().InFlightStep {
		t.Fatalf("expected persisted in-flight flag to remain true after clear failure")
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

func TestPersistedAssistantToolCallsContainNoUIDisplayMarkers(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working"},
			ToolCalls: []llm.ToolCall{
				{ID: "a", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "run tool"); err != nil {
		t.Fatalf("submit: %v", err)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}

	foundAssistantWithCall := false
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var msg llm.Message
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			t.Fatalf("decode message: %v", err)
		}
		if msg.Role != llm.RoleAssistant || len(msg.ToolCalls) == 0 {
			continue
		}
		foundAssistantWithCall = true
		for _, call := range msg.ToolCalls {
			if strings.Contains(call.Name, "shell_call") {
				t.Fatalf("assistant tool call name should not contain display marker: %+v", call)
			}
			if strings.Contains(string(call.Input), "shell_call") || strings.Contains(string(call.Input), "patch_payload") || strings.ContainsRune(string(call.Input), '\x1e') || strings.ContainsRune(string(call.Input), '\x1f') {
				t.Fatalf("assistant tool call input should not contain display markers: %+v", call)
			}
		}
	}
	if !foundAssistantWithCall {
		t.Fatal("expected persisted assistant message with tool_calls")
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
	if len(firstReq.Messages) < 5 {
		t.Fatalf("expected at least 5 messages in first request, got %d", len(firstReq.Messages))
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
	envMsg := firstReq.Messages[3]
	if envMsg.Role != llm.RoleDeveloper || !strings.Contains(envMsg.Content, environmentInjectedHeader) {
		t.Fatalf("expected fourth message to be environment developer injection, got %+v", envMsg)
	}
	for _, required := range []string{
		"OS: ",
		"Current TZ: ",
		"Date/time: ",
		"Shell: ",
		"CWD: ",
		"CPU arch: ",
	} {
		if !strings.Contains(envMsg.Content, required) {
			t.Fatalf("expected environment message to contain %q, got %q", required, envMsg.Content)
		}
	}
	if firstReq.Messages[4].Role != llm.RoleUser || firstReq.Messages[4].Content != "first" {
		t.Fatalf("expected user message after injections, got %+v", firstReq.Messages[4])
	}

	secondReq := client.calls[1]
	injectedCount := 0
	envInjectedCount := 0
	for _, msg := range secondReq.Messages {
		if msg.Role == llm.RoleDeveloper && strings.Contains(msg.Content, agentsInjectedHeader) {
			injectedCount++
		}
		if msg.Role == llm.RoleDeveloper && strings.Contains(msg.Content, environmentInjectedHeader) {
			envInjectedCount++
		}
	}
	if injectedCount != 2 {
		t.Fatalf("expected exactly two injected AGENTS developer messages to persist, got %d", injectedCount)
	}
	if envInjectedCount != 1 {
		t.Fatalf("expected exactly one injected environment developer message to persist, got %d", envInjectedCount)
	}
}

func TestInjectsEnvironmentInfoWithoutAnyAgentsFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := t.TempDir()
	storeRoot := t.TempDir()
	store, err := session.Create(storeRoot, "ws", workspace)
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

	if _, err := eng.SubmitUserMessage(context.Background(), "first"); err != nil {
		t.Fatalf("submit: %v", err)
	}

	if len(client.calls) != 1 {
		t.Fatalf("expected one model call, got %d", len(client.calls))
	}
	req := client.calls[0]
	if len(req.Messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != llm.RoleDeveloper || !strings.Contains(req.Messages[0].Content, environmentInjectedHeader) {
		t.Fatalf("expected first message to be environment injection, got %+v", req.Messages[0])
	}
	if req.Messages[1].Role != llm.RoleUser || req.Messages[1].Content != "first" {
		t.Fatalf("expected user message after environment injection, got %+v", req.Messages[1])
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

func TestSanitizeMessagesForLLMNormalizesToolJSONEscapes(t *testing.T) {
	input := []llm.Message{
		{Role: llm.RoleTool, Content: `{"exit_code":0,"output":"a =\u003e b \u003c c \u0026 d","truncated":false}`},
	}

	got := sanitizeMessagesForLLM(input)
	if len(got) != 1 {
		t.Fatalf("unexpected message count: %d", len(got))
	}
	if strings.Contains(got[0].Content, `\u003e`) || strings.Contains(got[0].Content, `\u003c`) || strings.Contains(got[0].Content, `\u0026`) {
		t.Fatalf("expected HTML escapes to be normalized, got %q", got[0].Content)
	}
	if !strings.Contains(got[0].Content, "=>") || !strings.Contains(got[0].Content, "<") || !strings.Contains(got[0].Content, "&") {
		t.Fatalf("expected decoded operators in normalized tool content, got %q", got[0].Content)
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

func TestDiscardQueuedUserMessagesMatchingRemovesQueuedEntries(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	eng.QueueUserMessage("same")
	eng.QueueUserMessage("other")
	eng.QueueUserMessage("same")

	removed := eng.DiscardQueuedUserMessagesMatching("same")
	if removed != 2 {
		t.Fatalf("removed=%d, want 2", removed)
	}

	eng.mu.Lock()
	defer eng.mu.Unlock()
	if len(eng.pendingInjected) != 1 || eng.pendingInjected[0] != "other" {
		t.Fatalf("unexpected pending queue after discard: %+v", eng.pendingInjected)
	}
}

func TestContextUsageUsesLastUsageWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5", ContextWindowTokens: 400_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 1234, OutputTokens: 66, WindowTokens: 399_000})

	usage := eng.ContextUsage()
	if usage.UsedTokens != 1300 {
		t.Fatalf("used tokens=%d, want 1300", usage.UsedTokens)
	}
	if usage.WindowTokens != 400_000 {
		t.Fatalf("window tokens=%d, want 400000", usage.WindowTokens)
	}
}

func TestContextUsageFallsBackToEstimatedTokens(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "estimate me"}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	usage := eng.ContextUsage()
	if usage.WindowTokens != 410_000 {
		t.Fatalf("window tokens=%d, want 410000", usage.WindowTokens)
	}
	if usage.UsedTokens <= 0 {
		t.Fatalf("expected estimated used tokens > 0, got %d", usage.UsedTokens)
	}
}

func TestAutoCompactionRecomputesUsageFromReplacementHistory(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{
		compactionResponses: []llm.CompactionResponse{
			{
				OutputItems: []llm.ResponseItem{
					{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "u1"},
					{Type: llm.ResponseItemTypeCompaction, ID: "cmp_1", EncryptedContent: "enc_1"},
				},
				Usage: llm.Usage{InputTokens: 190000, OutputTokens: 1000, WindowTokens: 200000},
			},
		},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.lastUsage = llm.Usage{InputTokens: 190000, OutputTokens: 0, WindowTokens: 200000}

	if err := eng.autoCompactIfNeeded(context.Background(), "step-1", compactionModeAuto); err != nil {
		t.Fatalf("auto compact failed: %v", err)
	}
	if eng.shouldAutoCompact() {
		t.Fatalf("expected auto compact threshold to be cleared after replacement, usage=%+v", eng.lastUsage)
	}
}

func TestCompactionPersistsSingleNoticeEntry(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{
		compactionResponses: []llm.CompactionResponse{
			{
				OutputItems: []llm.ResponseItem{
					{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "u1"},
					{Type: llm.ResponseItemTypeCompaction, ID: "cmp_1", EncryptedContent: "enc_1"},
				},
				Usage: llm.Usage{InputTokens: 190000, OutputTokens: 1000, WindowTokens: 200000},
			},
		},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.lastUsage = llm.Usage{InputTokens: 190000, OutputTokens: 0, WindowTokens: 200000}

	if err := eng.autoCompactIfNeeded(context.Background(), "step-1", compactionModeAuto); err != nil {
		t.Fatalf("auto compact failed: %v", err)
	}

	snap := eng.ChatSnapshot()
	notices := 0
	for _, entry := range snap.Entries {
		if entry.Role == "compaction_notice" {
			notices++
			if entry.Text != "context compacted for the 1st time" {
				t.Fatalf("unexpected compaction notice text: %q", entry.Text)
			}
		}
		if strings.Contains(strings.ToLower(entry.Text), "compaction started") || strings.Contains(strings.ToLower(entry.Text), "compaction completed") {
			t.Fatalf("unexpected start/completed status entry: %+v", entry)
		}
	}
	if notices != 1 {
		t.Fatalf("expected one compaction notice, got %d entries=%+v", notices, snap.Entries)
	}
}

func TestAutoCompactionRemoteReplacesHistoryAndCarriesCompactionItem(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{
		responses: []llm.Response{
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working"},
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)},
				},
				Usage: llm.Usage{InputTokens: 190000, OutputTokens: 2000, WindowTokens: 200000},
			},
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
				Usage:     llm.Usage{InputTokens: 2000, OutputTokens: 1000, WindowTokens: 200000},
			},
		},
		compactionResponses: []llm.CompactionResponse{
			{
				OutputItems: []llm.ResponseItem{
					{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "run tools"},
					{Type: llm.ResponseItemTypeCompaction, ID: "cmp_1", EncryptedContent: "enc_1"},
				},
				Usage: llm.Usage{InputTokens: 12000, OutputTokens: 1000, WindowTokens: 200000},
			},
		},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "run tools")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("assistant content = %q, want done", msg.Content)
	}
	if len(client.compactionCalls) != 1 {
		t.Fatalf("expected one remote compaction call, got %d", len(client.compactionCalls))
	}
	if len(client.calls) < 2 {
		t.Fatalf("expected second model call after compaction, got %d calls", len(client.calls))
	}

	foundCompactionItem := false
	for _, item := range client.calls[1].Items {
		if item.Type == llm.ResponseItemTypeCompaction && item.EncryptedContent == "enc_1" {
			foundCompactionItem = true
			break
		}
	}
	if !foundCompactionItem {
		t.Fatalf("expected compaction item in post-compaction request, got %+v", client.calls[1].Items)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	sawHistoryReplace := false
	for _, evt := range events {
		if evt.Kind == "history_replaced" {
			sawHistoryReplace = true
			break
		}
	}
	if !sawHistoryReplace {
		t.Fatalf("expected history_replaced event, got %+v", events)
	}
}

func TestAutoCompactionRetries400ByTrimmingOldestEligibleItems(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{
		responses: []llm.Response{
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working"},
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)},
				},
				Usage: llm.Usage{InputTokens: 390000, OutputTokens: 1000, WindowTokens: 400000},
			},
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
				Usage:     llm.Usage{InputTokens: 2000, OutputTokens: 500, WindowTokens: 400000},
			},
		},
		compactionErrors: []error{
			&llm.APIStatusError{StatusCode: 400, Body: "context window exceeded"},
			nil,
		},
		compactionResponses: []llm.CompactionResponse{
			{
				OutputItems: []llm.ResponseItem{
					{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "run tools"},
					{Type: llm.ResponseItemTypeCompaction, ID: "cmp_1", EncryptedContent: "enc_1"},
				},
				Usage: llm.Usage{InputTokens: 8000, OutputTokens: 500, WindowTokens: 400000},
			},
		},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5.3-codex"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "run tools")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("assistant content = %q, want done", msg.Content)
	}
	if len(client.compactionCalls) != 2 {
		t.Fatalf("expected two compact calls (retry after 400), got %d", len(client.compactionCalls))
	}
	first := len(client.compactionCalls[0].InputItems)
	second := len(client.compactionCalls[1].InputItems)
	if second >= first {
		t.Fatalf("expected trimmed retry input to shrink, first=%d second=%d", first, second)
	}
}

func TestOpenAIModelCompact404DoesNotFallbackToLocalCompaction(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{
		responses: []llm.Response{
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working"},
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)},
				},
				Usage: llm.Usage{InputTokens: 190000, OutputTokens: 2000, WindowTokens: 200000},
			},
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "summary"},
				Usage:     llm.Usage{InputTokens: 8000, OutputTokens: 1000, WindowTokens: 200000},
			},
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
				Usage:     llm.Usage{InputTokens: 4000, OutputTokens: 1000, WindowTokens: 200000},
			},
		},
		compactionErr: &llm.APIStatusError{StatusCode: 404, Body: "not found"},
		caps: llm.ProviderCapabilities{
			ProviderID:                    "openai-compatible",
			SupportsResponsesAPI:          true,
			SupportsResponsesCompact:      false,
			SupportsReasoningEncrypted:    false,
			SupportsServerSideContextEdit: false,
			IsOpenAIFirstParty:            false,
		},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "run tools")
	if err == nil {
		t.Fatalf("expected compaction error, got success message %+v", msg)
	}
	if len(client.compactionCalls) != 1 {
		t.Fatalf("expected one compact call, got %d", len(client.compactionCalls))
	}
	for _, req := range client.calls {
		for _, item := range req.Items {
			if item.Type == llm.ResponseItemTypeMessage && item.Role == llm.RoleUser && strings.Contains(item.Content, prompts.CompactionSummaryPrefix) {
				t.Fatalf("did not expect local compaction summary fallback, request=%+v", req.Items)
			}
		}
	}
}
