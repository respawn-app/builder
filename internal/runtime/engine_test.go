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
	shelltool "builder/internal/tools/shell"
	"builder/prompts"
)

type fakeClient struct {
	mu        sync.Mutex
	responses []llm.Response
	calls     []llm.Request
	caps      llm.ProviderCapabilities
	capsErr   error
}

func requestMessages(req llm.Request) []llm.Message {
	return llm.MessagesFromItems(req.Items)
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

func (f *fakeClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.capsErr != nil {
		return llm.ProviderCapabilities{}, f.capsErr
	}
	if strings.TrimSpace(f.caps.ProviderID) != "" {
		return f.caps, nil
	}
	return llm.ProviderCapabilities{
		ProviderID:                    "openai",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      true,
		SupportsReasoningEncrypted:    true,
		SupportsServerSideContextEdit: true,
		IsOpenAIFirstParty:            true,
	}, nil
}

type fakeCompactionClient struct {
	mu sync.Mutex

	responses []llm.Response
	calls     []llm.Request

	inputTokenCount      int
	inputTokenCountFn    func(req llm.Request) int
	countInputTokenCalls int

	compactionResponses []llm.CompactionResponse
	compactionErr       error
	compactionErrors    []error
	compactionCalls     []llm.CompactionRequest

	caps llm.ProviderCapabilities
}

type preciseCompactionClient struct {
	inputTokenCount int
	contextWindow   int

	countCalls   int
	resolveCalls int
}

func (c *preciseCompactionClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, nil
}

func (c *preciseCompactionClient) CountRequestInputTokens(_ context.Context, _ llm.Request) (int, error) {
	c.countCalls++
	if c.inputTokenCount < 0 {
		return 0, nil
	}
	return c.inputTokenCount, nil
}

func (c *preciseCompactionClient) ResolveModelContextWindow(_ context.Context, _ string) (int, error) {
	c.resolveCalls++
	if c.contextWindow <= 0 {
		return 0, nil
	}
	return c.contextWindow, nil
}

func (c *preciseCompactionClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return llm.ProviderCapabilities{
		ProviderID:                    "openai",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      true,
		SupportsReasoningEncrypted:    true,
		SupportsServerSideContextEdit: true,
		IsOpenAIFirstParty:            true,
	}, nil
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

func (f *fakeCompactionClient) CountRequestInputTokens(_ context.Context, req llm.Request) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.countInputTokenCalls++
	if f.inputTokenCountFn != nil {
		count := f.inputTokenCountFn(req)
		if count < 0 {
			return 0, nil
		}
		return count, nil
	}
	if f.inputTokenCount < 0 {
		return 0, nil
	}
	return f.inputTokenCount, nil
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

type blockingTool struct {
	name    tools.ID
	started chan struct{}
	release chan struct{}
}

func (t blockingTool) Name() tools.ID { return t.name }

func (t blockingTool) Call(_ context.Context, c tools.Call) (tools.Result, error) {
	select {
	case <-t.started:
	default:
		close(t.started)
	}
	<-t.release
	out, _ := json.Marshal(map[string]any{"tool": string(t.name)})
	return tools.Result{CallID: c.ID, Name: c.Name, Output: out}, nil
}

type fakeStreamClient struct {
	mu       sync.Mutex
	attempts int
	calls    []llm.Request
}

type fakeAsyncLateDeltaClient struct{}

type fakeSimpleStreamClient struct{}

type fakeNoopStreamClient struct{}

type fakeReasoningStreamClient struct{}

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

func (fakeAsyncLateDeltaClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
}

func (fakeAsyncLateDeltaClient) GenerateStream(_ context.Context, _ llm.Request, onDelta func(string)) (llm.Response, error) {
	if onDelta != nil {
		onDelta("final")
		go func() {
			time.Sleep(10 * time.Millisecond)
			onDelta("late")
		}()
	}
	return llm.Response{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "final"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}, nil
}

func (fakeSimpleStreamClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
}

func (fakeSimpleStreamClient) GenerateStream(_ context.Context, _ llm.Request, onDelta func(string)) (llm.Response, error) {
	if onDelta != nil {
		onDelta("a")
		onDelta("b")
	}
	return llm.Response{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ab"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}, nil
}

func (fakeNoopStreamClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
}

func (fakeNoopStreamClient) GenerateStream(_ context.Context, _ llm.Request, onDelta func(string)) (llm.Response, error) {
	if onDelta != nil {
		onDelta(reviewerNoopToken)
	}
	return llm.Response{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: reviewerNoopToken, Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}, nil
}

func (fakeReasoningStreamClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
}

func (fakeReasoningStreamClient) GenerateStreamWithEvents(_ context.Context, _ llm.Request, callbacks llm.StreamCallbacks) (llm.Response, error) {
	if callbacks.OnReasoningSummaryDelta != nil {
		callbacks.OnReasoningSummaryDelta(llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "Plan"})
		callbacks.OnReasoningSummaryDelta(llm.ReasoningSummaryDelta{Key: "rs_1:summary:0", Role: "reasoning", Text: "Plan summary"})
	}
	if callbacks.OnAssistantDelta != nil {
		callbacks.OnAssistantDelta("done")
	}
	return llm.Response{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
		Reasoning: []llm.ReasoningEntry{{Role: "reasoning", Text: "Plan summary"}},
		Usage:     llm.Usage{WindowTokens: 200000},
	}, nil
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

type providerContractFailClient struct {
	mu    sync.Mutex
	calls int
}

type streamRequiredClient struct {
	mu          sync.Mutex
	streamCalls int
	requests    []llm.Request
	response    llm.Response
}

func (c *streamRequiredClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, &llm.APIStatusError{StatusCode: 400, Body: `{"detail":"Stream must be set to true"}`}
}

func (c *streamRequiredClient) GenerateStream(_ context.Context, req llm.Request, _ func(string)) (llm.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.streamCalls++
	c.requests = append(c.requests, req)
	return c.response, nil
}

func (c *streamRequiredClient) StreamCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.streamCalls
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

func (c *providerContractFailClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return llm.Response{}, &llm.ProviderAPIError{
		ProviderID: "openai",
		Code:       llm.UnifiedErrorCodeProviderContract,
		Message:    "provider contract is unavailable",
	}
}

func (c *providerContractFailClient) Calls() int {
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
		ToolPreambles: true,
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
	if len(meta.Locked.EnabledTools) != 1 || meta.Locked.EnabledTools[0] != string(tools.ToolShell) {
		t.Fatalf("locked enabled tools = %+v", meta.Locked.EnabledTools)
	}
	if meta.Locked.ToolPreambles == nil || !*meta.Locked.ToolPreambles {
		t.Fatalf("expected locked tool_preambles=true for normal session")
	}
	if !meta.Locked.ModelCapabilities.SupportsReasoningEffort {
		t.Fatalf("expected locked reasoning support for %q", meta.Locked.Model)
	}
	if !meta.Locked.ModelCapabilities.SupportsVisionInputs {
		t.Fatalf("expected locked vision support for %q", meta.Locked.Model)
	}
	if meta.Locked.ProviderContract.ProviderID != "openai" {
		t.Fatalf("expected locked openai provider contract, got %+v", meta.Locked.ProviderContract)
	}
	if !meta.Locked.ProviderContract.SupportsResponsesCompact || !meta.Locked.ProviderContract.IsOpenAIFirstParty {
		t.Fatalf("unexpected locked provider capabilities: %+v", meta.Locked.ProviderContract)
	}
}

func TestHeadlessSessionLocksToolPreamblesOff(t *testing.T) {
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
		ThinkingLevel: "high",
		EnabledTools:  []tools.ID{tools.ToolShell},
		HeadlessMode:  true,
		ToolPreambles: true,
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
	if meta.Locked.ToolPreambles == nil || *meta.Locked.ToolPreambles {
		t.Fatalf("expected locked tool_preambles=false for headless session")
	}
	if strings.Contains(client.calls[0].SystemPrompt, "## Intermediary updates") {
		t.Fatalf("did not expect intermediary updates in headless system prompt")
	}
}

func TestLockedToolPreamblesPersistAcrossResume(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	firstClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "first"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	firstEngine, err := New(store, firstClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:         "gpt-5",
		EnabledTools:  []tools.ID{tools.ToolShell},
		ToolPreambles: false,
	})
	if err != nil {
		t.Fatalf("new first engine: %v", err)
	}
	if _, err := firstEngine.SubmitUserMessage(context.Background(), "first"); err != nil {
		t.Fatalf("submit first: %v", err)
	}
	if strings.Contains(firstClient.calls[0].SystemPrompt, "## Intermediary updates") {
		t.Fatalf("did not expect intermediary updates in first locked prompt")
	}

	resumedClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "second"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	resumedEngine, err := New(store, resumedClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:         "gpt-5",
		EnabledTools:  []tools.ID{tools.ToolShell},
		ToolPreambles: true,
	})
	if err != nil {
		t.Fatalf("new resumed engine: %v", err)
	}
	if _, err := resumedEngine.SubmitUserMessage(context.Background(), "second"); err != nil {
		t.Fatalf("submit second: %v", err)
	}
	if strings.Contains(resumedClient.calls[0].SystemPrompt, "## Intermediary updates") {
		t.Fatalf("did not expect resumed session to change locked tool_preambles policy")
	}
}

func TestThinkingLevelCanChangeAfterLock(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "one"}, Usage: llm.Usage{WindowTokens: 200000}},
		{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "two"}, Usage: llm.Usage{WindowTokens: 200000}},
	}}

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
		t.Fatalf("submit first: %v", err)
	}
	if err := eng.SetThinkingLevel("low"); err != nil {
		t.Fatalf("set thinking level: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "again"); err != nil {
		t.Fatalf("submit second: %v", err)
	}

	if len(client.calls) != 2 {
		t.Fatalf("client calls = %d, want 2", len(client.calls))
	}
	if client.calls[0].ReasoningEffort != "xhigh" {
		t.Fatalf("first reasoning effort = %q, want xhigh", client.calls[0].ReasoningEffort)
	}
	if client.calls[1].ReasoningEffort != "low" {
		t.Fatalf("second reasoning effort = %q, want low", client.calls[1].ReasoningEffort)
	}
}

func TestSetThinkingLevelRejectsInvalidValue(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:         "gpt-5",
		ThinkingLevel: "high",
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.SetThinkingLevel("ultra"); err == nil {
		t.Fatal("expected invalid thinking level error")
	}
	if got := eng.ThinkingLevel(); got != "high" {
		t.Fatalf("thinking level after invalid set = %q, want high", got)
	}
}

func TestPoisonedLockedSessionFallsBackToModelReasoningSupport(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.MarkModelDispatchLocked(session.LockedContract{
		Model:          "gpt-5.4",
		Temperature:    1,
		MaxOutputToken: 0,
		ProviderContract: session.LockedProviderCapabilities{
			ProviderID:                 "chatgpt-codex",
			SupportsResponsesAPI:       true,
			SupportsResponsesCompact:   true,
			SupportsNativeWebSearch:    true,
			SupportsReasoningEncrypted: true,
			IsOpenAIFirstParty:         true,
		},
	}); err != nil {
		t.Fatalf("mark locked: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok"}}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:         "gpt-5.4",
		ThinkingLevel: "high",
		EnabledTools:  []tools.ID{tools.ToolShell},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "hi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("client calls = %d, want 1", len(client.calls))
	}
	if client.calls[0].ReasoningEffort != "high" {
		t.Fatalf("reasoning effort = %q, want high", client.calls[0].ReasoningEffort)
	}
	if !client.calls[0].SupportsReasoningEffort {
		t.Fatal("expected request to preserve reasoning support fallback for poisoned locked session")
	}
}

func TestFastModeCanChangeAfterLock(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{
		responses: []llm.Response{
			{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "one"}, Usage: llm.Usage{WindowTokens: 200000}},
			{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "two"}, Usage: llm.Usage{WindowTokens: 200000}},
		},
		caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:         "gpt-5.3-codex",
		Temperature:   1,
		ThinkingLevel: "high",
		EnabledTools:  []tools.ID{tools.ToolShell},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "hi"); err != nil {
		t.Fatalf("submit first: %v", err)
	}
	changed, err := eng.SetFastModeEnabled(true)
	if err != nil {
		t.Fatalf("set fast mode: %v", err)
	}
	if !changed {
		t.Fatal("expected fast mode change")
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "again"); err != nil {
		t.Fatalf("submit second: %v", err)
	}

	if len(client.calls) != 2 {
		t.Fatalf("client calls = %d, want 2", len(client.calls))
	}
	if client.calls[0].FastMode {
		t.Fatal("did not expect first request to enable fast mode")
	}
	if !client.calls[1].FastMode {
		t.Fatal("expected second request to enable fast mode")
	}
}

func TestSetFastModeRejectsUnsupportedProvider(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "azure-openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: false}}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5.3-codex",
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	changed, err := eng.SetFastModeEnabled(true)
	if err == nil {
		t.Fatal("expected fast mode unsupported error")
	}
	if changed {
		t.Fatal("did not expect changed=true for unsupported fast mode")
	}
	if eng.FastModeEnabled() {
		t.Fatal("did not expect fast mode enabled after failure")
	}
}

func TestSetFastModeTogglesRuntimeOnly(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	cfg := Config{Model: "gpt-5.3-codex"}
	eng, err := New(store, &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	changed, err := eng.SetFastModeEnabled(true)
	if err != nil {
		t.Fatalf("enable fast mode: %v", err)
	}
	if !changed || !eng.FastModeEnabled() {
		t.Fatalf("expected fast mode enabled, changed=%v enabled=%v", changed, eng.FastModeEnabled())
	}

	restarted, err := New(store, &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), cfg)
	if err != nil {
		t.Fatalf("new restarted engine: %v", err)
	}
	if restarted.FastModeEnabled() {
		t.Fatal("expected fast mode disabled after restart")
	}
}

func TestFastModeSharedStateAppliesAcrossEngines(t *testing.T) {
	dir := t.TempDir()
	state := NewFastModeState(false)
	storeA, err := session.Create(dir, "ws-a", dir)
	if err != nil {
		t.Fatalf("create store A: %v", err)
	}
	engA, err := New(storeA, &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:         "gpt-5.3-codex",
		FastModeState: state,
	})
	if err != nil {
		t.Fatalf("new engine A: %v", err)
	}

	changed, err := engA.SetFastModeEnabled(true)
	if err != nil {
		t.Fatalf("enable fast mode: %v", err)
	}
	if !changed || !state.Enabled() {
		t.Fatalf("expected shared fast mode enabled, changed=%v enabled=%v", changed, state.Enabled())
	}

	storeB, err := session.Create(dir, "ws-b", dir)
	if err != nil {
		t.Fatalf("create store B: %v", err)
	}
	engB, err := New(storeB, &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:         "gpt-5.3-codex",
		FastModeState: state,
	})
	if err != nil {
		t.Fatalf("new engine B: %v", err)
	}
	if !engB.FastModeEnabled() {
		t.Fatal("expected shared fast mode to carry into next engine")
	}
}

func TestSetAutoCompactionEnabledTogglesRuntimeOnly(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	cfg := Config{Model: "gpt-5"}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	changed, enabled := eng.SetAutoCompactionEnabled(false)
	if !changed || enabled {
		t.Fatalf("expected changed=true enabled=false, got changed=%v enabled=%v", changed, enabled)
	}
	if got := eng.AutoCompactionEnabled(); got {
		t.Fatalf("expected runtime auto-compaction disabled, got %v", got)
	}

	restarted, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), cfg)
	if err != nil {
		t.Fatalf("new restarted engine: %v", err)
	}
	if got := restarted.AutoCompactionEnabled(); !got {
		t.Fatalf("expected auto-compaction enabled after restart, got %v", got)
	}
}

func TestSetAutoCompactionDisabledConcurrentWithBusyStepSkipsCompactionForCurrentRun(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{
		responses: []llm.Response{
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
				ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)}},
				Usage:     llm.Usage{InputTokens: 390000, OutputTokens: 1000, WindowTokens: 400000},
			},
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
				Usage:     llm.Usage{WindowTokens: 400000},
			},
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

	started := make(chan struct{})
	release := make(chan struct{})
	eng, err := New(store, client, tools.NewRegistry(blockingTool{name: tools.ToolShell, started: started, release: release}), Config{
		Model:                 "gpt-5",
		AutoCompactTokenLimit: 350000,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	submitDone := make(chan error, 1)
	go func() {
		_, submitErr := eng.SubmitUserMessage(context.Background(), "run tools")
		submitDone <- submitErr
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for tool call to start")
	}
	changed, enabled := eng.SetAutoCompactionEnabled(false)
	if !changed || enabled {
		t.Fatalf("expected changed=true enabled=false, got changed=%v enabled=%v", changed, enabled)
	}
	close(release)

	if err := <-submitDone; err != nil {
		t.Fatalf("submit while disabling auto-compaction: %v", err)
	}
	if got := len(client.compactionCalls); got != 0 {
		t.Fatalf("expected no compaction call for in-flight run after disabling auto-compaction, got %d", got)
	}
}

func TestSetReviewerEnabledTogglesRuntimeOnly(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	cfg := Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "off",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        &fakeClient{},
		},
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	changed, mode, err := eng.SetReviewerEnabled(true)
	if err != nil {
		t.Fatalf("enable reviewer: %v", err)
	}
	if !changed || mode != "edits" {
		t.Fatalf("expected changed=true mode=edits, got changed=%v mode=%q", changed, mode)
	}
	if got := eng.ReviewerFrequency(); got != "edits" {
		t.Fatalf("reviewer frequency = %q, want edits", got)
	}

	restarted, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), cfg)
	if err != nil {
		t.Fatalf("new restarted engine: %v", err)
	}
	if got := restarted.ReviewerFrequency(); got != "off" {
		t.Fatalf("reviewer frequency after restart = %q, want off", got)
	}
}

func TestSetReviewerEnabledFailsWhenReviewerClientMissing(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "off",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        nil,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	changed, mode, err := eng.SetReviewerEnabled(true)
	if err == nil {
		t.Fatal("expected enable reviewer error when reviewer client is missing")
	}
	if changed {
		t.Fatal("did not expect changed=true when reviewer client is missing")
	}
	if mode != "off" {
		t.Fatalf("expected mode off on failure, got %q", mode)
	}
}

func TestSetReviewerEnabledLazyInitializesReviewerClient(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "off",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        nil,
			ClientFactory: func() (llm.Client, error) {
				return &fakeClient{}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	changed, mode, err := eng.SetReviewerEnabled(true)
	if err != nil {
		t.Fatalf("enable reviewer with lazy client init: %v", err)
	}
	if !changed || mode != "edits" {
		t.Fatalf("expected changed=true mode=edits, got changed=%v mode=%q", changed, mode)
	}
}

func TestSetReviewerEnabledConcurrentWithBusyStep(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{ID: "call_patch_1", Name: string(tools.ToolPatch), Input: json.RawMessage(`{"patch":"*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"}`)}},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":[]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolPatch, delay: 50 * time.Millisecond}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "off",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			ClientFactory: func() (llm.Client, error) {
				return reviewerClient, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	submitDone := make(chan error, 1)
	go func() {
		_, submitErr := eng.SubmitUserMessage(context.Background(), "edit file")
		submitDone <- submitErr
	}()

	time.Sleep(10 * time.Millisecond)
	if _, _, err := eng.SetReviewerEnabled(true); err != nil {
		t.Fatalf("enable reviewer while busy: %v", err)
	}

	if err := <-submitDone; err != nil {
		t.Fatalf("submit while enabling reviewer: %v", err)
	}
	if got := eng.ReviewerFrequency(); got != "edits" {
		t.Fatalf("reviewer frequency after concurrent enable = %q, want edits", got)
	}
	if got := len(reviewerClient.calls); got != 1 {
		t.Fatalf("expected reviewer to run for in-flight step after concurrent enable, got %d calls", got)
	}
}

func TestSetReviewerDisabledConcurrentWithBusyStepSkipsReviewerForCurrentRun(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{ID: "call_patch_1", Name: string(tools.ToolPatch), Input: json.RawMessage(`{"patch":"*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"}`)}},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":[]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolPatch, delay: 50 * time.Millisecond}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        reviewerClient,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	submitDone := make(chan error, 1)
	go func() {
		_, submitErr := eng.SubmitUserMessage(context.Background(), "edit file")
		submitDone <- submitErr
	}()

	time.Sleep(10 * time.Millisecond)
	if _, _, err := eng.SetReviewerEnabled(false); err != nil {
		t.Fatalf("disable reviewer while busy: %v", err)
	}

	if err := <-submitDone; err != nil {
		t.Fatalf("submit while disabling reviewer: %v", err)
	}
	if got := eng.ReviewerFrequency(); got != "off" {
		t.Fatalf("reviewer frequency after concurrent disable = %q, want off", got)
	}
	if got := len(reviewerClient.calls); got != 0 {
		t.Fatalf("expected reviewer to be skipped for in-flight step after concurrent disable, got %d calls", got)
	}
}

func TestHostedWebSearchExecutionFromOutputItem(t *testing.T) {
	item := llm.ResponseItem{
		Type: llm.ResponseItemTypeOther,
		Raw: json.RawMessage(`{
			"type":"web_search_call",
			"id":"ws_1",
			"status":"completed",
			"action":{"type":"search","query":"builder cli"}
		}`),
	}

	executions := hostedToolExecutionsFromOutputItems([]llm.ResponseItem{item}, tools.DefinitionsFor([]tools.ID{tools.ToolWebSearch}))
	if len(executions) != 1 {
		t.Fatal("expected hosted web search execution")
	}
	execution := executions[0]
	if execution.Call.Name != string(tools.ToolWebSearch) {
		t.Fatalf("unexpected hosted tool name: %+v", execution.Call)
	}
	if execution.Call.ID != "ws_1" {
		t.Fatalf("unexpected hosted call id: %+v", execution.Call)
	}
	var input map[string]string
	if err := json.Unmarshal(execution.Call.Input, &input); err != nil {
		t.Fatalf("decode hosted input: %v", err)
	}
	if input["query"] != "builder cli" {
		t.Fatalf("expected hosted query in input, got %+v", input)
	}
	if execution.Result.Name != tools.ToolWebSearch {
		t.Fatalf("unexpected hosted result tool name: %+v", execution.Result)
	}
	if execution.Result.IsError {
		t.Fatalf("expected hosted status completed to be non-error")
	}
}

func TestHostedWebSearchExecutionUsesURLAsQueryFallback(t *testing.T) {
	item := llm.ResponseItem{
		Type: llm.ResponseItemTypeOther,
		Raw: json.RawMessage(`{
			"type":"web_search_call",
			"id":"ws_2",
			"status":"completed",
			"action":{"type":"open_page","url":"https://example.com"}
		}`),
	}

	executions := hostedToolExecutionsFromOutputItems([]llm.ResponseItem{item}, tools.DefinitionsFor([]tools.ID{tools.ToolWebSearch}))
	if len(executions) != 1 {
		t.Fatal("expected hosted web search execution")
	}
	execution := executions[0]
	var input map[string]string
	if err := json.Unmarshal(execution.Call.Input, &input); err != nil {
		t.Fatalf("decode hosted input: %v", err)
	}
	if input["query"] != "https://example.com" {
		t.Fatalf("expected url fallback in query, got %+v", input)
	}
}

func TestHostedWebSearchExecutionRejectsWhitespaceSearchQuery(t *testing.T) {
	item := llm.ResponseItem{
		Type: llm.ResponseItemTypeOther,
		Raw: json.RawMessage(`{
			"type":"web_search_call",
			"id":"ws_3",
			"status":"completed",
			"action":{"type":"search","query":"   "}
		}`),
	}

	executions := hostedToolExecutionsFromOutputItems([]llm.ResponseItem{item}, tools.DefinitionsFor([]tools.ID{tools.ToolWebSearch}))
	if len(executions) != 1 {
		t.Fatal("expected hosted web search execution")
	}
	execution := executions[0]
	if !execution.Result.IsError {
		t.Fatalf("expected hosted whitespace query to fail, got %+v", execution.Result)
	}
	var output map[string]string
	if err := json.Unmarshal(execution.Result.Output, &output); err != nil {
		t.Fatalf("decode hosted output: %v", err)
	}
	if output["error"] != tools.InvalidWebSearchQueryMessage {
		t.Fatalf("expected invalid query error, got %+v", output)
	}
	var input map[string]string
	if err := json.Unmarshal(execution.Call.Input, &input); err != nil {
		t.Fatalf("decode hosted input: %v", err)
	}
	if _, ok := input["query"]; !ok {
		t.Fatalf("expected hosted input to preserve query field, got %+v", input)
	}
	if input["query"] != "" {
		t.Fatalf("expected hosted input query to stay empty, got %+v", input)
	}
}

func TestSubmitUserMessageContinuesAfterHostedToolOnlyTurn(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: ""},
			OutputItems: []llm.ResponseItem{
				{
					Type: llm.ResponseItemTypeOther,
					Raw:  json.RawMessage(`{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"builder cli"}}`),
				},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	client.caps = llm.ProviderCapabilities{
		ProviderID:                    "openai",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      true,
		SupportsNativeWebSearch:       true,
		SupportsReasoningEncrypted:    true,
		SupportsServerSideContextEdit: true,
		IsOpenAIFirstParty:            true,
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:         "gpt-5",
		WebSearchMode: "native",
		EnabledTools:  []tools.ID{tools.ToolWebSearch},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "find latest")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("assistant content = %q, want done", msg.Content)
	}
	if len(client.calls) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(client.calls))
	}
	if !client.calls[0].EnableNativeWebSearch {
		t.Fatalf("expected first request to enable native web search")
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	hostedCompletionCount := 0
	for _, evt := range events {
		if evt.Kind != "tool_completed" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(evt.Payload, &payload); err != nil {
			t.Fatalf("decode tool_completed payload: %v", err)
		}
		name, _ := payload["name"].(string)
		if strings.TrimSpace(name) == string(tools.ToolWebSearch) {
			hostedCompletionCount++
		}
	}
	if hostedCompletionCount != 1 {
		t.Fatalf("expected one hosted web_search tool completion, got %d", hostedCompletionCount)
	}

	secondReq := client.calls[1]
	foundHostedOutput := false
	for _, item := range secondReq.Items {
		if item.Type != llm.ResponseItemTypeFunctionCallOutput {
			continue
		}
		if item.CallID == "ws_1" {
			foundHostedOutput = true
			break
		}
	}
	if !foundHostedOutput {
		t.Fatalf("expected hosted tool output item in follow-up request, got %+v", secondReq.Items)
	}
}

func TestSubmitUserMessageCommentaryWithoutToolCallsForcesNextLoop(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "Working on it",
				Phase:   llm.MessagePhaseCommentary,
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "running",
				Phase:   llm.MessagePhaseCommentary,
			},
			ToolCalls: []llm.ToolCall{
				{ID: "call_shell_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "done",
				Phase:   llm.MessagePhaseFinal,
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
	}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do the task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("assistant content = %q, want done", msg.Content)
	}
	if len(client.calls) != 3 {
		t.Fatalf("expected 3 model calls, got %d", len(client.calls))
	}

	secondReq := client.calls[1]
	foundWarning := false
	for _, reqMsg := range requestMessages(secondReq) {
		if reqMsg.Role == llm.RoleDeveloper && strings.Contains(reqMsg.Content, commentaryWithoutToolCallsWarning) {
			if reqMsg.MessageType != llm.MessageTypeErrorFeedback {
				t.Fatalf("expected commentary warning message type error_feedback, got %+v", reqMsg)
			}
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected commentary warning in next request, got %+v", requestMessages(secondReq))
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	toolCompleted := 0
	for _, evt := range events {
		if evt.Kind == "tool_completed" {
			toolCompleted++
		}
	}
	if toolCompleted != 1 {
		t.Fatalf("expected exactly one tool execution, got %d", toolCompleted)
	}
}

func TestSubmitUserMessage_ExposesViewImageToolForVisionModels(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolViewImage}), Config{
		Model:        "gpt-5.3-codex",
		EnabledTools: []tools.ID{tools.ToolViewImage},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "analyze image"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected 1 model call, got %d", len(client.calls))
	}
	found := false
	for _, tool := range client.calls[0].Tools {
		if strings.TrimSpace(tool.Name) == string(tools.ToolViewImage) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected view_image tool in request tools: %+v", client.calls[0].Tools)
	}
}

func TestSubmitUserMessage_HidesViewImageToolForTextOnlyModels(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolViewImage}), Config{
		Model:        "gpt-3.5-turbo",
		EnabledTools: []tools.ID{tools.ToolViewImage},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "analyze image"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected 1 model call, got %d", len(client.calls))
	}
	for _, tool := range client.calls[0].Tools {
		if strings.TrimSpace(tool.Name) == string(tools.ToolViewImage) {
			t.Fatalf("did not expect view_image tool in request for text-only model: %+v", client.calls[0].Tools)
		}
	}
}

func TestSubmitUserMessage_HidesViewImageToolForCodexSpark(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
		Usage:     llm.Usage{WindowTokens: 128000},
	}}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolViewImage}), Config{
		Model:        "gpt-5.3-codex-spark",
		EnabledTools: []tools.ID{tools.ToolViewImage},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "analyze image"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected 1 model call, got %d", len(client.calls))
	}
	for _, tool := range client.calls[0].Tools {
		if strings.TrimSpace(tool.Name) == string(tools.ToolViewImage) {
			t.Fatalf("did not expect view_image tool in request for codex spark: %+v", client.calls[0].Tools)
		}
	}
	locked := store.Meta().Locked
	if locked == nil {
		t.Fatal("expected locked contract")
	}
	if locked.ModelCapabilities.SupportsVisionInputs {
		t.Fatalf("expected codex spark locked capabilities to remain text-only, got %+v", locked.ModelCapabilities)
	}
}

func TestSubmitUserMessage_ExposesViewImageToolForUnlistedVisionModelWithOverride(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolViewImage}), Config{
		Model:             "gpt-4o-2026-01-15",
		ModelCapabilities: session.LockedModelCapabilities{SupportsVisionInputs: true},
		EnabledTools:      []tools.ID{tools.ToolViewImage},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "analyze image"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected 1 model call, got %d", len(client.calls))
	}
	found := false
	for _, tool := range client.calls[0].Tools {
		if strings.TrimSpace(tool.Name) == string(tools.ToolViewImage) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected view_image tool in request tools for override-enabled alias: %+v", client.calls[0].Tools)
	}
	locked := store.Meta().Locked
	if locked == nil || !locked.ModelCapabilities.SupportsVisionInputs {
		t.Fatalf("expected locked model capability override to persist, got %+v", locked)
	}
}

func TestEnsureLocked_DoesNotPersistFallbackProviderContractOnTransientFailure(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{
		capsErr: errors.New("transient auth metadata failure"),
		responses: []llm.Response{{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
			Usage:     llm.Usage{WindowTokens: 200000},
		}},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5.3-codex"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "hello"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	locked := store.Meta().Locked
	if locked == nil {
		t.Fatal("expected session to lock")
	}
	if strings.TrimSpace(locked.ProviderContract.ProviderID) != "" {
		t.Fatalf("expected transient provider capability failure to avoid persisting fallback provider contract, got %+v", locked.ProviderContract)
	}

	client.mu.Lock()
	client.capsErr = nil
	client.caps = llm.ProviderCapabilities{
		ProviderID:                    "openai",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      true,
		SupportsNativeWebSearch:       true,
		SupportsReasoningEncrypted:    true,
		SupportsServerSideContextEdit: true,
		IsOpenAIFirstParty:            true,
	}
	client.mu.Unlock()

	caps, err := eng.providerCapabilities(context.Background())
	if err != nil {
		t.Fatalf("providerCapabilities after recovery: %v", err)
	}
	if caps.ProviderID != "openai" || !caps.SupportsNativeWebSearch || !caps.SupportsResponsesCompact {
		t.Fatalf("expected live provider capabilities after recovery, got %+v", caps)
	}
}

func TestEnsureLocked_PersistsProviderCapabilityOverrideOverTransportMetadata(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{
		caps: llm.ProviderCapabilities{
			ProviderID:                 "anthropic",
			SupportsResponsesAPI:       false,
			SupportsResponsesCompact:   false,
			SupportsNativeWebSearch:    false,
			SupportsReasoningEncrypted: false,
			IsOpenAIFirstParty:         false,
		},
		responses: []llm.Response{{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
			Usage:     llm.Usage{WindowTokens: 200000},
		}},
	}

	override := &llm.ProviderCapabilities{
		ProviderID:                    "custom-provider",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      true,
		SupportsNativeWebSearch:       true,
		SupportsReasoningEncrypted:    true,
		SupportsServerSideContextEdit: true,
		IsOpenAIFirstParty:            true,
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                        "gpt-5.4",
		ProviderCapabilitiesOverride: override,
		EnabledTools:                 []tools.ID{tools.ToolShell},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "hello"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	locked := store.Meta().Locked
	if locked == nil {
		t.Fatal("expected session to lock")
	}
	if locked.ProviderContract.ProviderID != override.ProviderID {
		t.Fatalf("expected override provider id to persist, got %+v", locked.ProviderContract)
	}
	if !locked.ProviderContract.SupportsResponsesCompact || !locked.ProviderContract.SupportsNativeWebSearch || !locked.ProviderContract.IsOpenAIFirstParty {
		t.Fatalf("expected override provider capabilities to persist, got %+v", locked.ProviderContract)
	}

	resumedCaps, err := eng.providerCapabilities(context.Background())
	if err != nil {
		t.Fatalf("providerCapabilities: %v", err)
	}
	if resumedCaps.ProviderID != override.ProviderID || !resumedCaps.SupportsResponsesCompact || !resumedCaps.SupportsNativeWebSearch || !resumedCaps.IsOpenAIFirstParty {
		t.Fatalf("expected locked override provider capabilities on subsequent reads, got %+v", resumedCaps)
	}
}

func TestSubmitUserMessageMissingPhaseDefaultsToCommentaryAndWarns(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "Working on it",
			},
			OutputItems: []llm.ResponseItem{
				{Type: llm.ResponseItemTypeMessage, Role: llm.RoleAssistant, Content: "Working on it"},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "running",
				Phase:   llm.MessagePhaseCommentary,
			},
			ToolCalls: []llm.ToolCall{
				{ID: "call_shell_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "done",
				Phase:   llm.MessagePhaseFinal,
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
	}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do the task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("assistant content = %q, want done", msg.Content)
	}
	if len(client.calls) != 3 {
		t.Fatalf("expected 3 model calls, got %d", len(client.calls))
	}

	secondReq := client.calls[1]
	foundWarning := false
	for _, reqMsg := range requestMessages(secondReq) {
		if reqMsg.Role == llm.RoleDeveloper && strings.Contains(reqMsg.Content, missingAssistantPhaseWarning) {
			if reqMsg.MessageType != llm.MessageTypeErrorFeedback {
				t.Fatalf("expected missing-phase warning message type error_feedback, got %+v", reqMsg)
			}
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected missing-phase warning in next request, got %+v", requestMessages(secondReq))
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	persistedAsCommentary := false
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var persisted llm.Message
		if err := json.Unmarshal(evt.Payload, &persisted); err != nil {
			t.Fatalf("decode message event: %v", err)
		}
		if persisted.Role == llm.RoleAssistant && strings.TrimSpace(persisted.Content) == "Working on it" {
			persistedAsCommentary = persisted.Phase == llm.MessagePhaseCommentary
			break
		}
	}
	if !persistedAsCommentary {
		t.Fatalf("expected missing-phase assistant message to be persisted as commentary")
	}
}

func TestSubmitUserMessageMissingPhaseLegacyClientRemainsTerminal(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "done",
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
	}}
	client.caps = llm.ProviderCapabilities{ProviderID: "anthropic", SupportsResponsesAPI: false, IsOpenAIFirstParty: false}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do the task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("assistant content = %q, want done", msg.Content)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected 1 model call, got %d", len(client.calls))
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var persisted llm.Message
		if err := json.Unmarshal(evt.Payload, &persisted); err != nil {
			t.Fatalf("decode message event: %v", err)
		}
		if persisted.Role == llm.RoleDeveloper && strings.Contains(persisted.Content, missingAssistantPhaseWarning) {
			t.Fatalf("did not expect missing-phase warning for legacy client response")
		}
	}
}

func TestSubmitUserMessageMissingPhaseOpenAILegacyResponseRemainsTerminal(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "done",
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
	}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do the task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("assistant content = %q, want done", msg.Content)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected 1 model call, got %d", len(client.calls))
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var persisted llm.Message
		if err := json.Unmarshal(evt.Payload, &persisted); err != nil {
			t.Fatalf("decode message event: %v", err)
		}
		if persisted.Role == llm.RoleDeveloper && strings.Contains(persisted.Content, commentaryWithoutToolCallsWarning) {
			t.Fatalf("did not expect commentary-without-tools warning for legacy OpenAI response")
		}
		if persisted.Role == llm.RoleDeveloper && strings.Contains(persisted.Content, finalWithoutContentWarning) {
			t.Fatalf("did not expect final-without-content warning for legacy OpenAI response")
		}
	}
}

func TestSubmitUserMessageCommentaryWithoutToolsNonOpenAIRemainsTerminal(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "progress update",
				Phase:   llm.MessagePhaseCommentary,
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
	}}
	client.caps = llm.ProviderCapabilities{ProviderID: "anthropic", SupportsResponsesAPI: false, IsOpenAIFirstParty: false}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "claude-3"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do the task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "progress update" {
		t.Fatalf("assistant content = %q, want progress update", msg.Content)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected 1 model call, got %d", len(client.calls))
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var persisted llm.Message
		if err := json.Unmarshal(evt.Payload, &persisted); err != nil {
			t.Fatalf("decode message event: %v", err)
		}
		if persisted.Role == llm.RoleDeveloper && strings.Contains(persisted.Content, commentaryWithoutToolCallsWarning) {
			t.Fatalf("did not expect commentary-phase warning for non-openai provider")
		}
	}
}

func TestSubmitUserMessageLegacyGarbageTokenRemainsTerminal(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "working #+#+#+#+#+ malformed",
				Phase:   llm.MessagePhaseFinal,
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
	}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do the task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "working #+#+#+#+#+ malformed" {
		t.Fatalf("assistant content = %q", msg.Content)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected 1 model call, got %d", len(client.calls))
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	persistedAsFinal := false
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var persisted llm.Message
		if err := json.Unmarshal(evt.Payload, &persisted); err != nil {
			t.Fatalf("decode message event: %v", err)
		}
		if persisted.Role == llm.RoleAssistant && persisted.Content == "working #+#+#+#+#+ malformed" {
			persistedAsFinal = persisted.Phase == llm.MessagePhaseFinal
		}
	}
	if !persistedAsFinal {
		t.Fatalf("expected garbage-token assistant message to remain final")
	}
}

func TestSubmitUserMessageLegacyEnvelopeLeakRemainsTerminal(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "assistant to=functions.shell commentary  {\"command\":\"pwd\"}",
				Phase:   llm.MessagePhaseFinal,
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
	}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do the task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "assistant to=functions.shell commentary  {\"command\":\"pwd\"}" {
		t.Fatalf("assistant content = %q", msg.Content)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected 1 model call, got %d", len(client.calls))
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	persistedEnvelopeAsFinal := false
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var persisted llm.Message
		if err := json.Unmarshal(evt.Payload, &persisted); err != nil {
			t.Fatalf("decode message event: %v", err)
		}
		if persisted.Role == llm.RoleAssistant && strings.Contains(strings.ToLower(persisted.Content), "assistant to=functions.") {
			persistedEnvelopeAsFinal = persisted.Phase == llm.MessagePhaseFinal
		}
	}
	if !persistedEnvelopeAsFinal {
		t.Fatalf("expected envelope leak assistant message to remain final")
	}
}

func TestSubmitUserMessageFinalAnswerWithoutContentForcesNextLoop(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "",
				Phase:   llm.MessagePhaseFinal,
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "done",
				Phase:   llm.MessagePhaseFinal,
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
	}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do the task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("assistant content = %q, want done", msg.Content)
	}
	if len(client.calls) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(client.calls))
	}

	secondReq := client.calls[1]
	foundWarning := false
	for _, reqMsg := range requestMessages(secondReq) {
		if reqMsg.Role == llm.RoleDeveloper && strings.Contains(reqMsg.Content, finalWithoutContentWarning) {
			if reqMsg.MessageType != llm.MessageTypeErrorFeedback {
				t.Fatalf("expected final-without-content warning message type error_feedback, got %+v", reqMsg)
			}
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected final-without-content warning in next request, got %+v", requestMessages(secondReq))
	}
}

func TestSubmitUserMessageFinalAnswerWithToolCallsIgnoresToolCalls(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "final response",
				Phase:   llm.MessagePhaseFinal,
			},
			ToolCalls: []llm.ToolCall{
				{ID: "call_shell_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
	}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do the task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "final response" {
		t.Fatalf("assistant content = %q, want final response", msg.Content)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected 1 model call, got %d", len(client.calls))
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}

	toolCompleted := 0
	developerWarningFound := false
	persistedFinalHasToolCalls := false
	for _, evt := range events {
		if evt.Kind == "tool_completed" {
			toolCompleted++
		}
		if evt.Kind != "message" {
			continue
		}
		var persisted llm.Message
		if err := json.Unmarshal(evt.Payload, &persisted); err != nil {
			t.Fatalf("decode message event: %v", err)
		}
		if persisted.Role == llm.RoleDeveloper && strings.Contains(persisted.Content, finalWithToolCallsIgnoredWarning) {
			if persisted.MessageType != llm.MessageTypeErrorFeedback {
				t.Fatalf("expected final-with-tools warning message type error_feedback, got %+v", persisted)
			}
			developerWarningFound = true
		}
		if persisted.Role == llm.RoleAssistant && strings.TrimSpace(persisted.Content) == "final response" && len(persisted.ToolCalls) > 0 {
			persistedFinalHasToolCalls = true
		}
	}
	if toolCompleted != 0 {
		t.Fatalf("expected no tool execution, got %d", toolCompleted)
	}
	if !developerWarningFound {
		t.Fatalf("expected developer warning persisted for model visibility")
	}
	if persistedFinalHasToolCalls {
		t.Fatalf("expected persisted final assistant message to have no tool calls")
	}
}

func TestReviewerSkippedWhenNoToolCalls(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":["x"]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "edits",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        reviewerClient,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "hello")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("assistant content = %q, want done", msg.Content)
	}
	if len(reviewerClient.calls) != 0 {
		t.Fatalf("expected reviewer not to be called, got %d calls", len(reviewerClient.calls))
	}
}

func TestReviewerRunsOnAllFrequencyWithoutToolCalls(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":[]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			VerboseOutput: true,
			Client:        reviewerClient,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "hello")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("assistant content = %q, want done", msg.Content)
	}
	if len(reviewerClient.calls) != 1 {
		t.Fatalf("expected reviewer to be called once for frequency=all, got %d", len(reviewerClient.calls))
	}
}

func TestReviewerSuggestionsRequestInheritsFastMode(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":[]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:           "gpt-5",
		FastModeEnabled: true,
		Reviewer: ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        reviewerClient,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "hello"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(reviewerClient.calls) != 1 {
		t.Fatalf("expected reviewer to be called once, got %d", len(reviewerClient.calls))
	}
	if !reviewerClient.calls[0].FastMode {
		t.Fatal("expected reviewer request to inherit fast mode")
	}
}

func TestFinalNoopAnswerIsInvisibleAndSkipsReviewer(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: reviewerNoopToken, Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":["x"]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	var (
		mu     sync.Mutex
		events []Event
	)
	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        reviewerClient,
		},
		OnEvent: func(evt Event) {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "hello")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "" {
		t.Fatalf("assistant content = %q, want empty", msg.Content)
	}
	if len(mainClient.calls) != 1 {
		t.Fatalf("expected one main model call, got %d", len(mainClient.calls))
	}
	if len(reviewerClient.calls) != 0 {
		t.Fatalf("expected reviewer not to run for NO_OP final, got %d calls", len(reviewerClient.calls))
	}

	finalAssistantContents := make([]string, 0)
	for _, persisted := range eng.snapshotMessages() {
		if persisted.Role == llm.RoleAssistant && persisted.Phase == llm.MessagePhaseFinal {
			finalAssistantContents = append(finalAssistantContents, persisted.Content)
		}
		if strings.Contains(persisted.Content, reviewerNoopToken) {
			t.Fatalf("noop token leaked into persisted messages: %+v", eng.snapshotMessages())
		}
	}
	if len(finalAssistantContents) != 0 {
		t.Fatalf("expected no persisted final assistant messages, got %q", finalAssistantContents)
	}

	snapshot := eng.ChatSnapshot()
	for _, entry := range snapshot.Entries {
		if strings.Contains(entry.Text, reviewerNoopToken) {
			t.Fatalf("noop token leaked into chat snapshot: %+v", snapshot.Entries)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	assistantEvents := 0
	modelResponseEvents := 0
	for _, evt := range events {
		if evt.Kind == EventAssistantMessage {
			assistantEvents++
		}
		if evt.Kind == EventModelResponse {
			modelResponseEvents++
		}
	}
	if assistantEvents != 0 {
		t.Fatalf("expected no assistant_message events for NO_OP final, got %d", assistantEvents)
	}
	if modelResponseEvents != 0 {
		t.Fatalf("expected no model_response_received events for NO_OP final, got %d", modelResponseEvents)
	}
}

func TestReviewerRunsOnEditsFrequencyOnlyWhenPatchApplied(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{ID: "call_patch_1", Name: string(tools.ToolPatch), Input: json.RawMessage(`{"patch":"*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"}`)}},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "final", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":[]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolPatch}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "edits",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        reviewerClient,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "edit file")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "final" {
		t.Fatalf("assistant content = %q, want final", msg.Content)
	}
	if len(reviewerClient.calls) != 1 {
		t.Fatalf("expected reviewer to be called once after patch edit, got %d", len(reviewerClient.calls))
	}
}

func TestReviewerSuggestionsTriggerFollowUpAndNoopKeepsOriginalAnswer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, agentsGlobalDirName)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global agents dir: %v", err)
	}
	globalPath := filepath.Join(globalDir, agentsFileName)
	if err := os.WriteFile(globalPath, []byte("global rule"), 0o644); err != nil {
		t.Fatalf("write global AGENTS: %v", err)
	}

	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{
				{ID: "call_shell_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "original final", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: reviewerNoopToken, Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":["Double-check test output before final handoff."]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			VerboseOutput: true,
			Client:        reviewerClient,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "original final" {
		t.Fatalf("assistant content = %q, want original final", msg.Content)
	}
	if len(reviewerClient.calls) != 1 {
		t.Fatalf("expected one reviewer call, got %d", len(reviewerClient.calls))
	}
	if len(mainClient.calls) != 3 {
		t.Fatalf("expected 3 main calls (tool loop + final + follow-up), got %d", len(mainClient.calls))
	}

	req := mainClient.calls[2]
	foundReviewInstruction := false
	for _, message := range requestMessages(req) {
		if message.Role == llm.RoleDeveloper && strings.Contains(message.Content, "Supervisor agent gave you suggestions") {
			if message.MessageType != llm.MessageTypeReviewerFeedback {
				t.Fatalf("expected reviewer feedback message type, got %+v", message)
			}
			foundReviewInstruction = true
			break
		}
	}
	if !foundReviewInstruction {
		t.Fatalf("expected reviewer suggestions developer message in follow-up request")
	}

	reviewerReq := reviewerClient.calls[0]
	if reviewerReq.SystemPrompt != prompts.ReviewerSystemPrompt {
		t.Fatalf("unexpected reviewer prompt")
	}
	if reviewerReq.SessionID != store.Meta().SessionID+"-review" {
		t.Fatalf("expected reviewer session id suffix, got %q", reviewerReq.SessionID)
	}
	if len(requestMessages(reviewerReq)) == 0 {
		t.Fatalf("expected reviewer request to include transcript entry messages")
	}
	if requestMessages(reviewerReq)[0].Role != llm.RoleDeveloper || requestMessages(reviewerReq)[0].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(requestMessages(reviewerReq)[0].Content, "source: "+globalPath) {
		t.Fatalf("expected reviewer message[0] to be AGENTS meta developer message, got %+v", requestMessages(reviewerReq)[0])
	}
	environmentIdx := -1
	boundaryIdx := -1
	skillsMetaIdx := -1
	for idx, message := range requestMessages(reviewerReq) {
		if message.Role == llm.RoleDeveloper && message.MessageType == llm.MessageTypeEnvironment {
			environmentIdx = idx
		}
		if message.Role == llm.RoleDeveloper && message.MessageType == llm.MessageTypeSkills {
			skillsMetaIdx = idx
		}
		if message.Role == llm.RoleDeveloper && message.Content == reviewerMetaBoundaryMessage {
			boundaryIdx = idx
			break
		}
	}
	if environmentIdx < 0 {
		t.Fatalf("expected reviewer metadata to include environment context, got %+v", requestMessages(reviewerReq))
	}
	if boundaryIdx < 0 {
		t.Fatalf("expected reviewer metadata to include transcript boundary message, got %+v", requestMessages(reviewerReq))
	}
	if environmentIdx >= boundaryIdx {
		t.Fatalf("expected environment metadata before boundary, env=%d boundary=%d", environmentIdx, boundaryIdx)
	}
	if skillsMetaIdx >= 0 && (skillsMetaIdx <= 0 || skillsMetaIdx >= environmentIdx) {
		t.Fatalf("expected skills metadata between AGENTS and environment when present, skills=%d env=%d", skillsMetaIdx, environmentIdx)
	}
	foundAgentLabel := false
	foundToolCallJSON := false
	foundToolOutputField := false
	foundSeparateToolOutput := false
	for _, message := range requestMessages(reviewerReq)[boundaryIdx+1:] {
		if message.Role != llm.RoleUser {
			t.Fatalf("expected reviewer transcript entries after metadata to be user role messages, got %q", message.Role)
		}
		if strings.Contains(message.Content, "Agent:") {
			foundAgentLabel = true
		}
		if strings.Contains(message.Content, "Tool calls:") && strings.Contains(message.Content, "Input:") && strings.Contains(message.Content, "pwd") {
			foundToolCallJSON = true
		}
		if strings.Contains(message.Content, "Output:") && strings.Contains(message.Content, "{\"tool\":\"shell\"}") {
			foundToolOutputField = true
		}
		if strings.Contains(message.Content, "Tool output:") {
			foundSeparateToolOutput = true
		}
	}
	if !foundAgentLabel {
		t.Fatalf("expected reviewer request to include agent labels, messages=%+v", requestMessages(reviewerReq))
	}
	if !foundToolCallJSON {
		t.Fatalf("expected reviewer request to include tool call json args, messages=%+v", requestMessages(reviewerReq))
	}
	if !foundToolOutputField {
		t.Fatalf("expected reviewer request to include tool output in tool call payload, messages=%+v", requestMessages(reviewerReq))
	}
	if foundSeparateToolOutput {
		t.Fatalf("did not expect separate tool output entries when output is paired, messages=%+v", requestMessages(reviewerReq))
	}
	if len(reviewerReq.Items) == 0 {
		t.Fatalf("expected reviewer request items to carry canonical transcript history")
	}
	if len(reviewerReq.Tools) != 0 {
		t.Fatalf("expected reviewer request with no tools")
	}
	if reviewerReq.StructuredOutput == nil {
		t.Fatalf("expected reviewer request structured output")
	}
	if reviewerReq.StructuredOutput.Name != "reviewer_suggestions" {
		t.Fatalf("unexpected reviewer structured output name: %+v", reviewerReq.StructuredOutput)
	}

	snapshot := eng.ChatSnapshot()
	foundReviewerStatus := false
	for _, entry := range snapshot.Entries {
		if strings.Contains(entry.Text, reviewerNoopToken) {
			t.Fatalf("noop token leaked into chat snapshot: %+v", snapshot.Entries)
		}
		if strings.Contains(entry.Text, "Supervisor agent gave you suggestions") {
			t.Fatalf("reviewer control instruction leaked into chat snapshot: %+v", snapshot.Entries)
		}
		if entry.Role == "reviewer_status" && strings.Contains(entry.Text, "Supervisor ran") {
			foundReviewerStatus = true
		}
	}
	if !foundReviewerStatus {
		t.Fatalf("expected reviewer status entry in snapshot, got %+v", snapshot.Entries)
	}
}

func TestReviewerNoSuggestionsPersistsStatusEntry(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{
				{ID: "call_shell_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "final", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":[]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			VerboseOutput: true,
			Client:        reviewerClient,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "final" {
		t.Fatalf("assistant content = %q, want final", msg.Content)
	}

	snapshot := eng.ChatSnapshot()
	foundNoSuggestionsStatus := false
	for _, entry := range snapshot.Entries {
		if entry.Role == "reviewer_status" && strings.Contains(entry.Text, "no suggestions") {
			foundNoSuggestionsStatus = true
			break
		}
	}
	if !foundNoSuggestionsStatus {
		t.Fatalf("expected no-suggestions reviewer status entry, got %+v", snapshot.Entries)
	}
}

func TestReviewerArrayPayloadIsIgnoredAsNoSuggestions(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{
				{ID: "call_shell_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "final", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `["should","be","ignored"]`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			VerboseOutput: true,
			Client:        reviewerClient,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "final" {
		t.Fatalf("assistant content = %q, want final", msg.Content)
	}

	snapshot := eng.ChatSnapshot()
	foundNoSuggestionsStatus := false
	for _, entry := range snapshot.Entries {
		if entry.Role == "reviewer_status" && strings.Contains(entry.Text, "no suggestions") {
			foundNoSuggestionsStatus = true
			break
		}
	}
	if !foundNoSuggestionsStatus {
		t.Fatalf("expected no-suggestions reviewer status entry for array payload, got %+v", snapshot.Entries)
	}
}

func TestReviewerUsesStreamingClientWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{
				{ID: "call_shell_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "original final", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: reviewerNoopToken, Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	reviewerClient := &streamRequiredClient{response: llm.Response{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":["Check output formatting."]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        reviewerClient,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "original final" {
		t.Fatalf("assistant content = %q, want original final", msg.Content)
	}
	if reviewerClient.StreamCalls() != 1 {
		t.Fatalf("expected one reviewer stream call, got %d", reviewerClient.StreamCalls())
	}
}

func TestReviewerAppliedFollowUpRemainsVisibleInTranscript(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{
				{ID: "call_shell_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "original final", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "updated final after review", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":["Add final verification notes."]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			VerboseOutput: true,
			Client:        reviewerClient,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "updated final after review" {
		t.Fatalf("assistant content = %q, want updated final after review", msg.Content)
	}

	snapshot := eng.ChatSnapshot()
	foundFollowUpAssistant := false
	foundAppliedStatus := false
	suggestionsIdx := -1
	followUpIdx := -1
	for idx, entry := range snapshot.Entries {
		if entry.Role == "reviewer_suggestions" && strings.Contains(entry.Text, "Supervisor suggested:") {
			suggestionsIdx = idx
			if entry.OngoingText != "Supervisor suggested:\n1. Add final verification notes." {
				t.Fatalf("expected full reviewer suggestions ongoing text, got %+v", entry)
			}
		}
		if entry.Role == "assistant" && strings.Contains(entry.Text, "updated final after review") {
			foundFollowUpAssistant = true
			if followUpIdx < 0 {
				followUpIdx = idx
			}
		}
		if entry.Role == "reviewer_status" && strings.Contains(entry.Text, "Supervisor ran: 1 suggestion, applied.") {
			foundAppliedStatus = true
		}
	}
	if suggestionsIdx < 0 {
		t.Fatalf("expected reviewer suggestions status entry in snapshot, got %+v", snapshot.Entries)
	}
	if !foundFollowUpAssistant {
		t.Fatalf("expected follow-up assistant message in snapshot, got %+v", snapshot.Entries)
	}
	if followUpIdx >= 0 && suggestionsIdx > followUpIdx {
		t.Fatalf("expected reviewer suggestions to appear before follow-up assistant output, got %+v", snapshot.Entries)
	}
	if !foundAppliedStatus {
		t.Fatalf("expected applied reviewer status entry in snapshot, got %+v", snapshot.Entries)
	}

	restored, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	restoredSnapshot := restored.ChatSnapshot()
	foundRestoredSuggestions := false
	for _, entry := range restoredSnapshot.Entries {
		if entry.Role != "reviewer_suggestions" || !strings.Contains(entry.Text, "Supervisor suggested:") {
			continue
		}
		foundRestoredSuggestions = true
		if entry.OngoingText != "Supervisor suggested:\n1. Add final verification notes." {
			t.Fatalf("expected restored full reviewer suggestions ongoing text, got %+v", entry)
		}
	}
	if !foundRestoredSuggestions {
		t.Fatalf("expected restored reviewer suggestions entry, got %+v", restoredSnapshot.Entries)
	}
}

func TestRestoreMessagesKeepsStoredReviewerEntriesVerbatim(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("legacy-step", "local_entry", storedLocalEntry{
		Role:        "reviewer_suggestions",
		Text:        "Supervisor suggested:\n1. Add final verification notes.",
		OngoingText: "Supervisor made 1 suggestion.",
	}); err != nil {
		t.Fatalf("append legacy reviewer_suggestions: %v", err)
	}
	if _, err := store.AppendEvent("legacy-step", "local_entry", storedLocalEntry{
		Role: "reviewer_status",
		Text: "Supervisor ran, applied 1 suggestion:\n1. Add final verification notes.",
	}); err != nil {
		t.Fatalf("append legacy reviewer_status: %v", err)
	}

	restored, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	snapshot := restored.ChatSnapshot()
	if len(snapshot.Entries) != 2 {
		t.Fatalf("expected 2 restored entries, got %+v", snapshot.Entries)
	}
	if snapshot.Entries[0].Role != "reviewer_suggestions" || snapshot.Entries[0].OngoingText != "Supervisor made 1 suggestion." {
		t.Fatalf("expected stored reviewer_suggestions entry, got %+v", snapshot.Entries[0])
	}
	if snapshot.Entries[1].Role != "reviewer_status" || snapshot.Entries[1].Text != "Supervisor ran, applied 1 suggestion:\n1. Add final verification notes." {
		t.Fatalf("expected stored reviewer_status entry, got %+v", snapshot.Entries[1])
	}
}

func TestReviewerDefaultOutputOmitsReviewerSuggestionsEntry(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "original final"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}, {
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "updated final after review"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":["Add final verification notes."]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        reviewerClient,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "updated final after review" {
		t.Fatalf("assistant content = %q, want updated final after review", msg.Content)
	}

	snapshot := eng.ChatSnapshot()
	for _, entry := range snapshot.Entries {
		if entry.Role == "reviewer_suggestions" {
			t.Fatalf("expected reviewer_suggestions entry to be omitted by default, got %+v", snapshot.Entries)
		}
		if entry.Role == "reviewer_status" && strings.Contains(entry.Text, "Supervisor suggested:") {
			t.Fatalf("expected concise reviewer status by default, got %+v", entry)
		}
	}
}

func TestReviewerVerboseOutputShowsSuggestionsWhenIssuedAndKeepsFinalStatusConcise(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "original final"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}, {
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "updated final after review"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":["Add final verification notes."]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			VerboseOutput: true,
			Client:        reviewerClient,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "do task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "updated final after review" {
		t.Fatalf("assistant content = %q, want updated final after review", msg.Content)
	}

	snapshot := eng.ChatSnapshot()
	foundVerboseSuggestions := false
	foundConciseStatus := false
	for _, entry := range snapshot.Entries {
		if entry.Role == "reviewer_suggestions" && entry.OngoingText == "Supervisor suggested:\n1. Add final verification notes." {
			foundVerboseSuggestions = true
		}
		if entry.Role == "reviewer_status" && entry.Text == "Supervisor ran: 1 suggestion, applied." {
			foundConciseStatus = true
		}
	}
	if !foundVerboseSuggestions {
		t.Fatalf("expected verbose reviewer suggestions entry in snapshot, got %+v", snapshot.Entries)
	}
	if !foundConciseStatus {
		t.Fatalf("expected concise reviewer status entry in snapshot, got %+v", snapshot.Entries)
	}

	restored, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	restoredSnapshot := restored.ChatSnapshot()
	foundRestoredVerboseSuggestions := false
	foundRestoredConciseStatus := false
	for _, entry := range restoredSnapshot.Entries {
		if entry.Role == "reviewer_suggestions" && entry.OngoingText == "Supervisor suggested:\n1. Add final verification notes." {
			foundRestoredVerboseSuggestions = true
		}
		if entry.Role == "reviewer_status" && entry.Text == "Supervisor ran: 1 suggestion, applied." {
			foundRestoredConciseStatus = true
		}
	}
	if !foundRestoredVerboseSuggestions {
		t.Fatalf("expected restored verbose reviewer suggestions entry, got %+v", restoredSnapshot.Entries)
	}
	if !foundRestoredConciseStatus {
		t.Fatalf("expected restored concise reviewer status entry, got %+v", restoredSnapshot.Entries)
	}
}

func TestParseReviewerSuggestionsObjectSupportsStructuredPayload(t *testing.T) {
	suggestions := parseReviewerSuggestionsObject(`{"suggestions":["one","two","one"," "]}`)
	if len(suggestions) != 4 || suggestions[0] != "one" || suggestions[1] != "two" || suggestions[2] != "one" || suggestions[3] != " " {
		t.Fatalf("unexpected suggestions from object payload: %+v", suggestions)
	}

	suggestions = parseReviewerSuggestionsObject(`["a","b"]`)
	if len(suggestions) != 0 {
		t.Fatalf("expected invalid non-object payload to be ignored, got %+v", suggestions)
	}

	suggestions = parseReviewerSuggestionsObject(`not-json`)
	if len(suggestions) != 0 {
		t.Fatalf("expected invalid payload to be ignored, got %+v", suggestions)
	}
}

func TestBuildReviewerTranscriptMessagesIncludesConversationAndToolCalls(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleAssistant, Phase: llm.MessagePhaseCommentary, Content: "I’ll inspect quickly."},
		{Role: llm.RoleUser, Content: "user request"},
		{Role: llm.RoleAssistant, Content: "Running command now.", Phase: llm.MessagePhaseCommentary, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "shell", Input: json.RawMessage(`{"command":"pwd"}`)}}},
		{Role: llm.RoleAssistant, Content: "assistant response", Phase: llm.MessagePhaseFinal},
		{Role: llm.RoleTool, Name: "shell", ToolCallID: "call_1", Content: "{\"output\":\"ok\"}"},
		{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeEnvironment, Content: environmentInjectedHeader + "\nOS: darwin"},
	}

	reviewerMessages := buildReviewerTranscriptMessages(messages)
	if len(reviewerMessages) != 4 {
		t.Fatalf("expected 4 reviewer transcript messages after filtering, got %d", len(reviewerMessages))
	}
	if reviewerMessages[0].Role != llm.RoleUser {
		t.Fatalf("expected reviewer transcript messages to use user role, got %q", reviewerMessages[0].Role)
	}
	if !strings.Contains(reviewerMessages[0].Content, "I’ll inspect quickly.") {
		t.Fatalf("expected short commentary preamble to be preserved, message=%q", reviewerMessages[0].Content)
	}
	if !strings.Contains(reviewerMessages[2].Content, "Running command now.") {
		t.Fatalf("expected short commentary preamble text to be preserved when tool calls exist, message=%q", reviewerMessages[2].Content)
	}
	if !strings.Contains(reviewerMessages[2].Content, "Tool calls:") || !strings.Contains(reviewerMessages[2].Content, "Input:") || !strings.Contains(reviewerMessages[2].Content, "pwd") {
		t.Fatalf("expected tool call arguments in typed format, message=%q", reviewerMessages[2].Content)
	}
	if strings.Contains(reviewerMessages[2].Content, "(id=") {
		t.Fatalf("did not expect tool call id in reviewer transcript, message=%q", reviewerMessages[2].Content)
	}
	if !strings.Contains(reviewerMessages[2].Content, "Output:") || !strings.Contains(reviewerMessages[2].Content, "\"ok\"") {
		t.Fatalf("expected paired tool output section in tool call payload, message=%q", reviewerMessages[2].Content)
	}
	if !strings.Contains(reviewerMessages[3].Content, "Agent:") {
		t.Fatalf("expected assistant final answer entry to use agent label, message=%q", reviewerMessages[3].Content)
	}
	if strings.Contains(reviewerMessages[3].Content, "Tool output:") {
		t.Fatalf("did not expect separate tool output entry when paired output exists, message=%q", reviewerMessages[3].Content)
	}
}

func TestBuildReviewerTranscriptMessagesKeepsOrphanToolOutputEntry(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleTool, Name: "shell", ToolCallID: "orphan_call", Content: "{\"output\":\"orphan\"}"},
	}

	reviewerMessages := buildReviewerTranscriptMessages(messages)
	if len(reviewerMessages) != 1 {
		t.Fatalf("expected one reviewer message for orphan tool output, got %d", len(reviewerMessages))
	}
	if !strings.Contains(reviewerMessages[0].Content, "Tool:") || !strings.Contains(reviewerMessages[0].Content, "Tool output:") {
		t.Fatalf("expected orphan tool output to remain as tool entry, message=%q", reviewerMessages[0].Content)
	}
}

func TestReviewerStatusTextIncludesReviewerCacheHitMetadata(t *testing.T) {
	text := reviewerStatusText(ReviewerStatus{
		Outcome:               "applied",
		SuggestionsCount:      2,
		CacheHitPercent:       85,
		HasCacheHitPercentage: true,
	}, []string{"one", "two"})
	if strings.Contains(text, "Supervisor suggested:") || strings.Contains(text, "1. one") {
		t.Fatalf("expected reviewer status text to stay concise even when suggestions are provided, got %q", text)
	}
	if !strings.Contains(text, "85% cache hit") {
		t.Fatalf("expected reviewer cache hit metadata in reviewer status text, got %q", text)
	}

	text = reviewerStatusText(ReviewerStatus{
		Outcome:               "applied",
		SuggestionsCount:      2,
		CacheHitPercent:       85,
		HasCacheHitPercentage: true,
	}, nil)
	if !strings.Contains(text, "85% cache hit") {
		t.Fatalf("expected reviewer cache hit metadata even without suggestions, got %q", text)
	}

	text = reviewerStatusText(ReviewerStatus{
		Outcome:          "followup_failed",
		SuggestionsCount: 2,
		Error:            "tool crashed",
	}, []string{"one", "two"})
	if text != "Supervisor ran: 2 suggestions, but follow-up failed: tool crashed" {
		t.Fatalf("expected concise follow-up failure status, got %q", text)
	}
}

func TestBuildReviewerTranscriptMessagesIncludesSupervisorControlDeveloperMessage(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleDeveloper, Content: "Supervisor agent gave you suggestions:\n1. run tests"},
	}

	reviewerMessages := buildReviewerTranscriptMessages(messages)
	if len(reviewerMessages) != 1 {
		t.Fatalf("expected one reviewer message, got %d", len(reviewerMessages))
	}
	if !strings.Contains(reviewerMessages[0].Content, "Supervisor agent gave you suggestions:") {
		t.Fatalf("expected supervisor control message to be included, got %q", reviewerMessages[0].Content)
	}
	if !strings.Contains(reviewerMessages[0].Content, "Developer:") {
		t.Fatalf("expected developer label in reviewer message, got %q", reviewerMessages[0].Content)
	}
}

func TestAppendMissingReviewerMetaContextPrependsAgentsAndEnvironmentWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, agentsGlobalDirName)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global agents dir: %v", err)
	}
	globalPath := filepath.Join(globalDir, agentsFileName)
	if err := os.WriteFile(globalPath, []byte("global rule"), 0o644); err != nil {
		t.Fatalf("write global AGENTS: %v", err)
	}

	workspace := t.TempDir()
	workspacePath := filepath.Join(workspace, agentsFileName)
	if err := os.WriteFile(workspacePath, []byte("workspace rule"), 0o644); err != nil {
		t.Fatalf("write workspace AGENTS: %v", err)
	}

	in := []llm.Message{{Role: llm.RoleUser, Content: "request"}}
	got, err := appendMissingReviewerMetaContext(in, workspace, "gpt-5", "high", false, nil)
	if err != nil {
		t.Fatalf("appendMissingReviewerMetaContext: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 2 prepended agents + 1 environment message plus original, got %d", len(got))
	}
	if got[0].Role != llm.RoleDeveloper || got[0].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(got[0].Content, "source: "+globalPath) {
		t.Fatalf("expected first prepended global AGENTS developer message, got %+v", got[0])
	}
	if got[1].Role != llm.RoleDeveloper || got[1].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(got[1].Content, "source: "+workspacePath) {
		t.Fatalf("expected second prepended workspace AGENTS developer message, got %+v", got[1])
	}
	if got[2].Role != llm.RoleDeveloper || got[2].MessageType != llm.MessageTypeEnvironment || !strings.Contains(got[2].Content, environmentInjectedHeader) {
		t.Fatalf("expected prepended environment developer message, got %+v", got[2])
	}
	if got[3].Role != llm.RoleUser || got[3].Content != "request" {
		t.Fatalf("expected original message at tail, got %+v", got[3])
	}
}

func TestAppendMissingReviewerMetaContextKeepsExistingMetaMessages(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := t.TempDir()
	existing := llm.Message{
		Role:        llm.RoleDeveloper,
		MessageType: llm.MessageTypeAgentsMD,
		Content:     agentsInjectedHeader + "\nsource: /tmp/AGENTS.md\n\n```md\nrule\n```",
	}
	existingEnv := llm.Message{
		Role:        llm.RoleDeveloper,
		MessageType: llm.MessageTypeEnvironment,
		Content:     environmentInjectedHeader + "\nOS: darwin",
	}
	in := []llm.Message{
		existing,
		existingEnv,
		{Role: llm.RoleUser, Content: "request"},
	}
	got, err := appendMissingReviewerMetaContext(in, workspace, "gpt-5", "high", false, nil)
	if err != nil {
		t.Fatalf("appendMissingReviewerMetaContext: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("expected no extra messages when AGENTS+environment already present, got %d", len(got))
	}
}

func TestAppendMissingReviewerMetaContextBackfillsSkillsBetweenAgentsAndEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := t.TempDir()
	writeTestSkill(t, filepath.Join(workspace, ".builder", "skills", "workspace-skill"), "workspace-skill", "from workspace")

	existingGlobalAgents := llm.Message{
		Role:        llm.RoleDeveloper,
		MessageType: llm.MessageTypeAgentsMD,
		SourcePath:  "/tmp/global/AGENTS.md",
		Content:     agentsInjectedHeader + "\nsource: /tmp/global/AGENTS.md\n\n```md\nglobal\n```",
	}
	existingWorkspaceAgents := llm.Message{
		Role:        llm.RoleDeveloper,
		MessageType: llm.MessageTypeAgentsMD,
		SourcePath:  "/tmp/workspace/AGENTS.md",
		Content:     agentsInjectedHeader + "\nsource: /tmp/workspace/AGENTS.md\n\n```md\nworkspace\n```",
	}
	existingEnv := llm.Message{
		Role:        llm.RoleDeveloper,
		MessageType: llm.MessageTypeEnvironment,
		Content:     environmentInjectedHeader + "\nOS: darwin",
	}
	in := []llm.Message{
		existingGlobalAgents,
		existingWorkspaceAgents,
		existingEnv,
		{Role: llm.RoleUser, Content: "request"},
	}

	got, err := appendMissingReviewerMetaContext(in, workspace, "gpt-5", "high", false, nil)
	if err != nil {
		t.Fatalf("appendMissingReviewerMetaContext: %v", err)
	}
	if len(got) != len(in)+1 {
		t.Fatalf("expected one skills message to be inserted, got len=%d", len(got))
	}
	if got[0].MessageType != llm.MessageTypeAgentsMD || got[1].MessageType != llm.MessageTypeAgentsMD {
		t.Fatalf("expected AGENTS metadata to remain first, got %+v %+v", got[0], got[1])
	}
	if got[2].MessageType != llm.MessageTypeSkills {
		t.Fatalf("expected skills metadata to be inserted after AGENTS, got %+v", got[2])
	}
	if got[3].MessageType != llm.MessageTypeEnvironment {
		t.Fatalf("expected environment metadata after skills, got %+v", got[3])
	}
	if got[4].Role != llm.RoleUser || got[4].Content != "request" {
		t.Fatalf("expected transcript content to stay at tail, got %+v", got[4])
	}
}

func TestAppendMissingReviewerMetaContextBackfillsSkillsBeforeEnvironmentWhenNoAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := t.TempDir()
	writeTestSkill(t, filepath.Join(workspace, ".builder", "skills", "workspace-skill"), "workspace-skill", "from workspace")

	existingEnv := llm.Message{
		Role:        llm.RoleDeveloper,
		MessageType: llm.MessageTypeEnvironment,
		Content:     environmentInjectedHeader + "\nOS: darwin",
	}
	in := []llm.Message{
		existingEnv,
		{Role: llm.RoleUser, Content: "request"},
	}

	got, err := appendMissingReviewerMetaContext(in, workspace, "gpt-5", "high", false, nil)
	if err != nil {
		t.Fatalf("appendMissingReviewerMetaContext: %v", err)
	}
	if len(got) != len(in)+1 {
		t.Fatalf("expected one skills message to be inserted, got len=%d", len(got))
	}
	if got[0].MessageType != llm.MessageTypeSkills {
		t.Fatalf("expected skills metadata first when agents are absent, got %+v", got[0])
	}
	if got[1].MessageType != llm.MessageTypeEnvironment {
		t.Fatalf("expected environment metadata after skills, got %+v", got[1])
	}
	if got[2].Role != llm.RoleUser || got[2].Content != "request" {
		t.Fatalf("expected transcript content to stay at tail, got %+v", got[2])
	}
}

func TestAppendMissingReviewerMetaContextBackfillsMissingWorkspaceAgentsSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, agentsGlobalDirName)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global agents dir: %v", err)
	}
	globalPath := filepath.Join(globalDir, agentsFileName)
	if err := os.WriteFile(globalPath, []byte("global rule"), 0o644); err != nil {
		t.Fatalf("write global AGENTS: %v", err)
	}

	workspace := t.TempDir()
	workspacePath := filepath.Join(workspace, agentsFileName)
	if err := os.WriteFile(workspacePath, []byte("workspace rule"), 0o644); err != nil {
		t.Fatalf("write workspace AGENTS: %v", err)
	}

	in := []llm.Message{
		{
			Role:        llm.RoleDeveloper,
			MessageType: llm.MessageTypeAgentsMD,
			SourcePath:  globalPath,
			Content:     agentsInjectedHeader + "\nsource: " + globalPath + "\n\n```md\nglobal rule\n```",
		},
		{Role: llm.RoleUser, Content: "request"},
	}
	got, err := appendMissingReviewerMetaContext(in, workspace, "gpt-5", "high", false, nil)
	if err != nil {
		t.Fatalf("appendMissingReviewerMetaContext: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected global+workspace agents, environment, and transcript, got %d", len(got))
	}
	if got[0].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(got[0].Content, "source: "+globalPath) {
		t.Fatalf("expected global AGENTS first, got %+v", got[0])
	}
	if got[1].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(got[1].Content, "source: "+workspacePath) {
		t.Fatalf("expected missing workspace AGENTS to be backfilled second, got %+v", got[1])
	}
	if got[2].MessageType != llm.MessageTypeEnvironment {
		t.Fatalf("expected environment after AGENTS, got %+v", got[2])
	}
	if got[3].Role != llm.RoleUser || got[3].Content != "request" {
		t.Fatalf("expected transcript content at tail, got %+v", got[3])
	}
}

func TestAppendMissingReviewerMetaContextLeavesUntypedLegacyMetaInTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, agentsGlobalDirName)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global agents dir: %v", err)
	}
	globalPath := filepath.Join(globalDir, agentsFileName)
	if err := os.WriteFile(globalPath, []byte("global rule"), 0o644); err != nil {
		t.Fatalf("write global AGENTS: %v", err)
	}

	workspace := t.TempDir()
	legacyWorkspacePath := filepath.Join(workspace, agentsFileName)
	in := []llm.Message{
		{
			Role:    llm.RoleDeveloper,
			Content: agentsInjectedHeader + "\nsource: " + legacyWorkspacePath + "\n\n```md\nlegacy workspace rule\n```",
		},
		{
			Role:    llm.RoleDeveloper,
			Content: skillsInjectedHeader + "\n" + skillsAvailableHeader + "\n- legacy-skill: legacy description (file: /tmp/legacy/SKILL.md)",
		},
		{
			Role:    llm.RoleDeveloper,
			Content: environmentInjectedHeader + "\nOS: darwin",
		},
		{Role: llm.RoleUser, Content: "request"},
	}

	got, err := appendMissingReviewerMetaContext(in, workspace, "gpt-5", "high", false, nil)
	if err != nil {
		t.Fatalf("appendMissingReviewerMetaContext: %v", err)
	}
	if len(got) != 6 {
		t.Fatalf("expected live metadata plus preserved legacy transcript entries, got %d", len(got))
	}
	if got[0].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(got[0].Content, "source: "+globalPath) {
		t.Fatalf("expected live global AGENTS to be backfilled first, got %+v", got[0])
	}
	if got[1].MessageType != llm.MessageTypeEnvironment || !strings.Contains(got[1].Content, environmentInjectedHeader) {
		t.Fatalf("expected live environment metadata second, got %+v", got[1])
	}
	if got[2].Role != llm.RoleDeveloper || !strings.Contains(got[2].Content, legacyWorkspacePath) {
		t.Fatalf("expected untyped legacy AGENTS text to remain transcript content, got %+v", got[2])
	}
	if got[3].Role != llm.RoleDeveloper || !strings.Contains(got[3].Content, "legacy-skill") {
		t.Fatalf("expected untyped legacy skills text to remain transcript content, got %+v", got[3])
	}
	if got[4].Role != llm.RoleDeveloper || !strings.Contains(got[4].Content, environmentInjectedHeader) {
		t.Fatalf("expected untyped legacy environment text to remain transcript content, got %+v", got[4])
	}
	if got[5].Role != llm.RoleUser || got[5].Content != "request" {
		t.Fatalf("expected transcript content at tail, got %+v", got[5])
	}
}

func TestFastExecCommandCompletionDoesNotQueueBackgroundNotice(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer func() {
		_ = manager.Close()
	}()
	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "running fast command",
			},
			ToolCalls: []llm.ToolCall{{
				ID:    "call_exec_1",
				Name:  string(tools.ToolExecCommand),
				Input: json.RawMessage(`{"cmd":"echo hi","shell":"/bin/sh","login":false,"yield_time_ms":1000}`),
			}},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "unexpected extra turn"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	registry := tools.NewRegistry(shelltool.NewExecCommandTool(dir, 16_000, manager, ""))
	eng, err := New(store, client, registry, Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	manager.SetEventHandler(func(evt shelltool.Event) {
		eng.HandleBackgroundShellEvent(BackgroundShellEvent{
			Type:    string(evt.Type),
			ID:      evt.Snapshot.ID,
			State:   evt.Snapshot.State,
			Command: evt.Snapshot.Command,
			Workdir: evt.Snapshot.Workdir,
			LogPath: evt.Snapshot.LogPath,
			Preview: evt.Preview,
			Removed: evt.Removed,
			ExitCode: func() *int {
				if evt.Snapshot.ExitCode == nil {
					return nil
				}
				out := *evt.Snapshot.ExitCode
				return &out
			}(),
		})
	})

	assistant, err := eng.SubmitUserMessage(context.Background(), "run fast command")
	if err != nil {
		t.Fatalf("submit user message: %v", err)
	}
	if assistant.Content != "done" {
		t.Fatalf("assistant content = %q, want done", assistant.Content)
	}
	time.Sleep(300 * time.Millisecond)
	client.mu.Lock()
	callCount := len(client.calls)
	client.mu.Unlock()
	if callCount != 2 {
		t.Fatalf("model call count = %d, want 2", callCount)
	}
	for _, msg := range eng.snapshotMessages() {
		if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeBackgroundNotice {
			t.Fatalf("did not expect background notice for foreground exec_command completion: %+v", msg)
		}
	}
}

func TestBackgroundShellNoticeFlushesOnFirstAvailableSlot(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)}},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "foreground done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	started := make(chan struct{})
	release := make(chan struct{})
	var (
		mu     sync.Mutex
		events []Event
	)
	eng, err := New(store, client, tools.NewRegistry(blockingTool{name: tools.ToolShell, started: started, release: release}), Config{
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

	submitDone := make(chan struct {
		assistant llm.Message
		err       error
	}, 1)
	go func() {
		assistant, submitErr := eng.SubmitUserMessage(context.Background(), "run tools")
		submitDone <- struct {
			assistant llm.Message
			err       error
		}{assistant: assistant, err: submitErr}
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for tool call to start")
	}

	eng.HandleBackgroundShellEvent(BackgroundShellEvent{
		Type:       "completed",
		ID:         "1000",
		State:      "completed",
		NoticeText: "Background shell 1000 completed.\nExit code: 0\nOutput:\ndone",
	})

	client.mu.Lock()
	callCountWhileBusy := len(client.calls)
	client.mu.Unlock()
	if callCountWhileBusy != 1 {
		t.Fatalf("expected queued notice to avoid immediate model call while busy, got %d calls", callCountWhileBusy)
	}

	close(release)
	result := <-submitDone
	if result.err != nil {
		t.Fatalf("submit: %v", result.err)
	}
	if result.assistant.Content != "foreground done" {
		t.Fatalf("assistant content = %q, want foreground done", result.assistant.Content)
	}

	client.mu.Lock()
	requests := append([]llm.Request(nil), client.calls...)
	client.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected 2 model calls with background notice injected into the next request, got %d", len(requests))
	}

	containsNotice := func(req llm.Request) bool {
		for _, msg := range requestMessages(req) {
			if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeBackgroundNotice && strings.Contains(msg.Content, "Background shell 1000 completed.") {
				return true
			}
		}
		return false
	}
	if !containsNotice(requests[1]) {
		t.Fatalf("expected background notice in first available in-turn follow-up, messages=%+v", requestMessages(requests[1]))
	}
	time.Sleep(300 * time.Millisecond)
	client.mu.Lock()
	callCountAfterReturn := len(client.calls)
	client.mu.Unlock()
	if callCountAfterReturn != 2 {
		t.Fatalf("did not expect a later batched continuation after turn completion, got %d calls", callCountAfterReturn)
	}

	mu.Lock()
	defer mu.Unlock()
	hasImmediateBackgroundUpdate := false
	for _, evt := range events {
		if evt.Kind == EventBackgroundUpdated && evt.Background != nil && evt.Background.ID == "1000" {
			hasImmediateBackgroundUpdate = true
			break
		}
	}
	if !hasImmediateBackgroundUpdate {
		t.Fatalf("expected immediate background_updated event, got %+v", events)
	}
}

func TestDeferredFinalWithBackgroundNoticeStillRunsReviewerAndEmitsAssistantEvent(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)}},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "foreground done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: reviewerNoopToken, Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":[]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	started := make(chan struct{})
	release := make(chan struct{})
	var (
		mu     sync.Mutex
		events []Event
	)
	eng, err := New(store, mainClient, tools.NewRegistry(blockingTool{name: tools.ToolShell, started: started, release: release}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        reviewerClient,
		},
		OnEvent: func(evt Event) {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	submitDone := make(chan struct {
		assistant llm.Message
		err       error
	}, 1)
	go func() {
		assistant, submitErr := eng.SubmitUserMessage(context.Background(), "run tools")
		submitDone <- struct {
			assistant llm.Message
			err       error
		}{assistant: assistant, err: submitErr}
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for tool call to start")
	}

	eng.HandleBackgroundShellEvent(BackgroundShellEvent{
		Type:       "completed",
		ID:         "1000",
		State:      "completed",
		NoticeText: "Background shell 1000 completed.\nExit code: 0\nOutput:\ndone",
	})

	close(release)
	result := <-submitDone
	if result.err != nil {
		t.Fatalf("submit: %v", result.err)
	}
	if result.assistant.Content != "foreground done" {
		t.Fatalf("assistant content = %q, want foreground done", result.assistant.Content)
	}
	if len(reviewerClient.calls) != 1 {
		t.Fatalf("expected reviewer to run once for deferred final, got %d", len(reviewerClient.calls))
	}

	mu.Lock()
	defer mu.Unlock()
	assistantMessages := 0
	for _, evt := range events {
		if evt.Kind != EventAssistantMessage {
			continue
		}
		assistantMessages++
		if evt.Message.Content != "foreground done" {
			t.Fatalf("assistant message content = %q, want foreground done", evt.Message.Content)
		}
	}
	if assistantMessages != 1 {
		t.Fatalf("expected one assistant_message event for deferred final, got %d events=%+v", assistantMessages, events)
	}
}

func TestDeferredFinalWithQueuedUserInjectionStillRunsReviewerAndEmitsAssistantEvent(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "foreground done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: reviewerNoopToken, Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":[]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	var (
		mu     sync.Mutex
		events []Event
	)
	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        reviewerClient,
		},
		OnEvent: func(evt Event) {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	eng.QueueUserMessage("steer now")
	result, err := eng.SubmitUserMessage(context.Background(), "run task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if result.Content != "foreground done" {
		t.Fatalf("assistant content = %q, want foreground done", result.Content)
	}
	if len(reviewerClient.calls) != 1 {
		t.Fatalf("expected reviewer to run once for deferred final, got %d", len(reviewerClient.calls))
	}
	if len(mainClient.calls) != 2 {
		t.Fatalf("expected two main model calls for deferred final path, got %d", len(mainClient.calls))
	}

	mu.Lock()
	defer mu.Unlock()
	assistantMessages := 0
	flushedQueuedUser := false
	for _, evt := range events {
		if evt.Kind == EventAssistantMessage {
			assistantMessages++
			if evt.Message.Content != "foreground done" {
				t.Fatalf("assistant message content = %q, want foreground done", evt.Message.Content)
			}
		}
		if evt.Kind == EventUserMessageFlushed && evt.UserMessage == "steer now" {
			flushedQueuedUser = true
		}
	}
	if assistantMessages != 1 {
		t.Fatalf("expected one assistant_message event for deferred final, got %d events=%+v", assistantMessages, events)
	}
	if !flushedQueuedUser {
		t.Fatalf("expected queued user injection flush event, got %+v", events)
	}
}

func TestDeferredFinalWithQueuedUserInjectionAndTrailingNoopStillUsesDeferredFinal(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "foreground done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: reviewerNoopToken, Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":[]}`},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

	var (
		mu     sync.Mutex
		events []Event
	)
	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "all",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        reviewerClient,
		},
		OnEvent: func(evt Event) {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	eng.QueueUserMessage("steer now")
	result, err := eng.SubmitUserMessage(context.Background(), "run task")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if result.Content != "foreground done" {
		t.Fatalf("assistant content = %q, want foreground done", result.Content)
	}
	if len(reviewerClient.calls) != 1 {
		t.Fatalf("expected reviewer to run once for deferred final, got %d", len(reviewerClient.calls))
	}

	mu.Lock()
	defer mu.Unlock()
	assistantMessages := 0
	for _, evt := range events {
		if evt.Kind != EventAssistantMessage {
			continue
		}
		assistantMessages++
		if evt.Message.Content != "foreground done" {
			t.Fatalf("assistant message content = %q, want foreground done", evt.Message.Content)
		}
	}
	if assistantMessages != 1 {
		t.Fatalf("expected one assistant_message event for deferred final, got %d events=%+v", assistantMessages, events)
	}
}

func TestBackgroundShellNoticeSameTurnNoopAddsNoAssistantMessage(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)}},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: reviewerNoopToken, Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	started := make(chan struct{})
	release := make(chan struct{})
	var (
		mu     sync.Mutex
		events []Event
	)
	eng, err := New(store, client, tools.NewRegistry(blockingTool{name: tools.ToolShell, started: started, release: release}), Config{
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

	submitDone := make(chan struct {
		assistant llm.Message
		err       error
	}, 1)
	go func() {
		assistant, submitErr := eng.SubmitUserMessage(context.Background(), "run tools")
		submitDone <- struct {
			assistant llm.Message
			err       error
		}{assistant: assistant, err: submitErr}
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for tool call to start")
	}

	eng.HandleBackgroundShellEvent(BackgroundShellEvent{
		Type:       "completed",
		ID:         "1000",
		State:      "completed",
		NoticeText: "Background shell 1000 completed.\nExit code: 0\nOutput:\ndone",
	})

	close(release)
	result := <-submitDone
	if result.err != nil {
		t.Fatalf("submit: %v", result.err)
	}
	if strings.TrimSpace(result.assistant.Content) != "" {
		t.Fatalf("assistant content = %q, want empty", result.assistant.Content)
	}

	client.mu.Lock()
	callCount := len(client.calls)
	requests := append([]llm.Request(nil), client.calls...)
	client.mu.Unlock()
	if callCount != 2 {
		t.Fatalf("expected 2 model calls within the same turn, got %d", callCount)
	}

	containsNotice := func(req llm.Request) bool {
		for _, msg := range requestMessages(req) {
			if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeBackgroundNotice && strings.Contains(msg.Content, "Background shell 1000 completed.") {
				return true
			}
		}
		return false
	}
	if !containsNotice(requests[1]) {
		t.Fatalf("expected background notice in same-turn follow-up, messages=%+v", requestMessages(requests[1]))
	}
	time.Sleep(300 * time.Millisecond)
	client.mu.Lock()
	callCountAfterReturn := len(client.calls)
	client.mu.Unlock()
	if callCountAfterReturn != 2 {
		t.Fatalf("did not expect a later batched continuation after turn completion, got %d calls", callCountAfterReturn)
	}

	finalAssistantContents := make([]string, 0)
	foundBackgroundNotice := false
	for _, persisted := range eng.snapshotMessages() {
		if persisted.Role == llm.RoleAssistant && persisted.Phase == llm.MessagePhaseFinal {
			finalAssistantContents = append(finalAssistantContents, persisted.Content)
		}
		if persisted.Role == llm.RoleDeveloper && persisted.MessageType == llm.MessageTypeBackgroundNotice && strings.Contains(persisted.Content, "Background shell 1000 completed.") {
			foundBackgroundNotice = true
		}
		if strings.Contains(persisted.Content, reviewerNoopToken) {
			t.Fatalf("noop token leaked into persisted messages: %+v", eng.snapshotMessages())
		}
	}
	if !foundBackgroundNotice {
		t.Fatalf("expected persisted background notice, got %+v", eng.snapshotMessages())
	}
	if len(finalAssistantContents) != 0 {
		t.Fatalf("expected no persisted final assistant message, got %q", finalAssistantContents)
	}

	mu.Lock()
	defer mu.Unlock()
	assistantEvents := 0
	for _, evt := range events {
		if evt.Kind == EventAssistantMessage {
			assistantEvents++
		}
	}
	if assistantEvents != 0 {
		t.Fatalf("expected no assistant_message events for same-turn noop background notice, got %d events=%+v", assistantEvents, events)
	}
}

func TestMultipleBackgroundShellNoticesFlushTogetherOnFirstAvailableSlot(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
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

	started := make(chan struct{})
	release := make(chan struct{})
	eng, err := New(store, client, tools.NewRegistry(blockingTool{name: tools.ToolShell, started: started, release: release}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	submitDone := make(chan struct {
		assistant llm.Message
		err       error
	}, 1)
	go func() {
		assistant, submitErr := eng.SubmitUserMessage(context.Background(), "run tools")
		submitDone <- struct {
			assistant llm.Message
			err       error
		}{assistant: assistant, err: submitErr}
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for tool call to start")
	}

	eng.HandleBackgroundShellEvent(BackgroundShellEvent{
		Type:       "completed",
		ID:         "1000",
		State:      "completed",
		NoticeText: "Background shell 1000 completed.\nExit code: 0\nOutput:\ndone-a",
	})
	eng.HandleBackgroundShellEvent(BackgroundShellEvent{
		Type:       "completed",
		ID:         "1001",
		State:      "completed",
		NoticeText: "Background shell 1001 completed.\nExit code: 0\nOutput:\ndone-b",
	})

	close(release)
	result := <-submitDone
	if result.err != nil {
		t.Fatalf("submit: %v", result.err)
	}
	if result.assistant.Content != "done" {
		t.Fatalf("assistant content = %q, want done", result.assistant.Content)
	}

	client.mu.Lock()
	requests := append([]llm.Request(nil), client.calls...)
	client.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected 2 model calls with both background notices injected into the next request, got %d", len(requests))
	}

	containsNotice := func(req llm.Request, shellID string) bool {
		for _, msg := range requestMessages(req) {
			if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeBackgroundNotice && strings.Contains(msg.Content, "Background shell "+shellID+" completed.") {
				return true
			}
		}
		return false
	}
	if !containsNotice(requests[1], "1000") || !containsNotice(requests[1], "1001") {
		t.Fatalf("expected both background notices in the same in-turn follow-up, messages=%+v", requestMessages(requests[1]))
	}

	time.Sleep(300 * time.Millisecond)
	client.mu.Lock()
	callCountAfterReturn := len(client.calls)
	client.mu.Unlock()
	if callCountAfterReturn != 2 {
		t.Fatalf("did not expect a later batched continuation after turn completion, got %d calls", callCountAfterReturn)
	}
}

func TestWriteStdinCompletionDoesNotQueueDuplicateBackgroundNotice(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer func() {
		_ = manager.Close()
	}()

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "start background", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{
				ID:    "call_exec_1",
				Name:  string(tools.ToolExecCommand),
				Input: json.RawMessage(`{"cmd":"sleep 1; echo done","shell":"/bin/sh","login":false,"yield_time_ms":250}`),
			}},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "wait for it", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{
				ID:    "call_poll_1",
				Name:  string(tools.ToolWriteStdin),
				Input: json.RawMessage(`{"session_id":1000,"yield_time_ms":2000}`),
			}},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "unexpected extra turn", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	registry := tools.NewRegistry(
		shelltool.NewExecCommandTool(dir, 16_000, manager, store.Meta().SessionID),
		shelltool.NewWriteStdinTool(16_000, manager),
	)
	eng, err := New(store, client, registry, Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	manager.SetEventHandler(func(evt shelltool.Event) {
		eng.HandleBackgroundShellUpdate(BackgroundShellEvent{
			Type:    string(evt.Type),
			ID:      evt.Snapshot.ID,
			State:   evt.Snapshot.State,
			Command: evt.Snapshot.Command,
			Workdir: evt.Snapshot.Workdir,
			LogPath: evt.Snapshot.LogPath,
			Preview: evt.Preview,
			Removed: evt.Removed,
			ExitCode: func() *int {
				if evt.Snapshot.ExitCode == nil {
					return nil
				}
				out := *evt.Snapshot.ExitCode
				return &out
			}(),
			NoticeSuppressed: evt.NoticeSuppressed,
		}, strings.TrimSpace(evt.Snapshot.OwnerSessionID) == store.Meta().SessionID && !evt.NoticeSuppressed)
	})

	assistant, err := eng.SubmitUserMessage(context.Background(), "run and wait")
	if err != nil {
		t.Fatalf("submit user message: %v", err)
	}
	if assistant.Content != "done" {
		t.Fatalf("assistant content = %q, want done", assistant.Content)
	}
	time.Sleep(300 * time.Millisecond)

	client.mu.Lock()
	callCount := len(client.calls)
	client.mu.Unlock()
	if callCount != 3 {
		t.Fatalf("model call count = %d, want 3", callCount)
	}
	for _, msg := range eng.snapshotMessages() {
		if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeBackgroundNotice {
			t.Fatalf("did not expect background notice after write_stdin harvested completion: %+v", msg)
		}
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

func TestSubmitUserShellCommandPersistsDeveloperNoticeAndToolEntries(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	result, err := eng.SubmitUserShellCommand(context.Background(), "pwd")
	if err != nil {
		t.Fatalf("submit user shell command: %v", err)
	}
	if result.Name != tools.ToolShell {
		t.Fatalf("unexpected tool result name: %+v", result)
	}

	messages := eng.snapshotMessages()
	if len(messages) == 0 {
		t.Fatal("expected persisted messages")
	}
	foundDeveloperNotice := false
	foundAssistantToolCall := false
	foundToolOutput := false
	for _, msg := range messages {
		switch msg.Role {
		case llm.RoleDeveloper:
			if strings.Contains(msg.Content, "User ran shell command directly:") && strings.Contains(msg.Content, "pwd") {
				foundDeveloperNotice = true
			}
		case llm.RoleAssistant:
			if len(msg.ToolCalls) == 1 && msg.ToolCalls[0].Name == string(tools.ToolShell) {
				foundAssistantToolCall = true
			}
		case llm.RoleTool:
			if msg.Name == string(tools.ToolShell) && strings.TrimSpace(msg.Content) != "" {
				foundToolOutput = true
			}
		}
	}
	if !foundDeveloperNotice {
		t.Fatalf("expected developer notice message in model context, messages=%+v", messages)
	}
	if !foundAssistantToolCall {
		t.Fatalf("expected assistant shell tool call message, messages=%+v", messages)
	}
	if !foundToolOutput {
		t.Fatalf("expected shell tool output message, messages=%+v", messages)
	}

	snapshot := eng.ChatSnapshot()
	foundUserShellCall := false
	for _, entry := range snapshot.Entries {
		if entry.Role != "tool_call" {
			continue
		}
		if entry.ToolCall == nil || !entry.ToolCall.IsShell {
			continue
		}
		if entry.ToolCall.UserInitiated && strings.Contains(entry.Text, "pwd") {
			foundUserShellCall = true
			break
		}
	}
	if !foundUserShellCall {
		t.Fatalf("expected user-initiated shell tool call in transcript snapshot, entries=%+v", snapshot.Entries)
	}
}

func TestSubmitUserShellCommandReturnsUnknownToolErrorWhenShellNotRegistered(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	result, err := eng.SubmitUserShellCommand(context.Background(), "pwd")
	if err == nil {
		t.Fatal("expected unknown tool error")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected unknown tool error, got %v", err)
	}
	if result.Name != tools.ToolShell || !result.IsError {
		t.Fatalf("expected shell error result, got %+v", result)
	}
	var payload struct {
		Error string `json:"error"`
	}
	if unmarshalErr := json.Unmarshal(result.Output, &payload); unmarshalErr != nil {
		t.Fatalf("decode result output: %v", unmarshalErr)
	}
	if strings.TrimSpace(payload.Error) != "unknown tool" {
		t.Fatalf("expected unknown tool output payload, got %v", payload)
	}

	messages := eng.snapshotMessages()
	foundToolOutput := false
	for _, msg := range messages {
		if msg.Role != llm.RoleTool {
			continue
		}
		if msg.Name != string(tools.ToolShell) {
			continue
		}
		foundToolOutput = true
		break
	}
	if !foundToolOutput {
		t.Fatalf("expected persisted shell tool output message, messages=%+v", messages)
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
	for _, msg := range requestMessages(secondReq) {
		if msg.Role == llm.RoleAssistant && len(msg.ToolCalls) == 2 {
			if msg.ToolCalls[0].ID == "a" && msg.ToolCalls[1].ID == "b" {
				foundAssistantWithCalls = true
				break
			}
		}
	}
	if !foundAssistantWithCalls {
		t.Fatalf("second request is missing assistant tool call metadata: %+v", requestMessages(secondReq))
	}

}

func TestParallelToolCompletionAppearsInChatSnapshotBeforeAllToolsFinish(t *testing.T) {
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

	slow := blockingTool{name: tools.ToolShell, started: make(chan struct{}), release: make(chan struct{})}
	toolCompleted := make(chan tools.Result, 4)
	eng, err := New(store, client, tools.NewRegistry(
		slow,
		fakeTool{name: tools.ToolPatch, delay: 1 * time.Millisecond},
	), Config{
		Model:       "gpt-5",
		Temperature: 1,
		OnEvent: func(evt Event) {
			if evt.Kind != EventToolCallCompleted || evt.ToolResult == nil {
				return
			}
			select {
			case toolCompleted <- *evt.ToolResult:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	submitDone := make(chan error, 1)
	go func() {
		_, submitErr := eng.SubmitUserMessage(context.Background(), "run tools")
		submitDone <- submitErr
	}()

	select {
	case <-slow.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for slow tool to start")
	}

	var completed tools.Result
	select {
	case completed = <-toolCompleted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fast tool completion")
	}
	if completed.CallID != "b" {
		t.Fatalf("expected fast patch tool to complete first, got %+v", completed)
	}

	snapshot := eng.ChatSnapshot()
	foundPendingA := false
	foundCompletedB := false
	for _, entry := range snapshot.Entries {
		switch {
		case entry.Role == "tool_call" && entry.ToolCallID == "a":
			foundPendingA = true
		case entry.Role == "tool_result_ok" && entry.ToolCallID == "b":
			foundCompletedB = true
		}
	}
	if !foundPendingA || !foundCompletedB {
		t.Fatalf("expected snapshot to expose pending a and completed b before slow tool finishes, got %+v", snapshot.Entries)
	}

	close(slow.release)
	select {
	case submitErr := <-submitDone:
		if submitErr != nil {
			t.Fatalf("submit: %v", submitErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for submit completion")
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

func TestExecuteToolCallsRejectsWhitespaceWebSearchQuery(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	results, err := eng.executeToolCalls(context.Background(), "step", []llm.ToolCall{{
		ID:    "call-web",
		Name:  string(tools.ToolWebSearch),
		Input: json.RawMessage(`{"query":"   "}`),
	}})
	if err != nil {
		t.Fatalf("execute tool calls: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if !results[0].IsError {
		t.Fatalf("expected invalid web search query to fail, got %+v", results[0])
	}
	var output map[string]string
	if err := json.Unmarshal(results[0].Output, &output); err != nil {
		t.Fatalf("decode result output: %v", err)
	}
	if output["error"] != tools.InvalidWebSearchQueryMessage {
		t.Fatalf("expected invalid query error, got %+v", output)
	}
	if completion, ok := eng.chat.toolCompletions["call-web"]; !ok {
		t.Fatal("expected tool completion to be recorded")
	} else if !completion.IsError {
		t.Fatalf("expected persisted completion to be error, got %+v", completion)
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

func TestStreamingEmitsReasoningSummaryDeltaEvents(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	var (
		mu     sync.Mutex
		events []Event
	)
	eng, err := New(store, fakeReasoningStreamClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
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

	if _, err := eng.SubmitUserMessage(context.Background(), "stream reasoning"); err != nil {
		t.Fatalf("submit: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var reasoningTexts []string
	for _, evt := range events {
		if evt.Kind != EventReasoningDelta || evt.ReasoningDelta == nil {
			continue
		}
		reasoningTexts = append(reasoningTexts, evt.ReasoningDelta.Text)
	}
	if len(reasoningTexts) != 2 || reasoningTexts[0] != "Plan" || reasoningTexts[1] != "Plan summary" {
		t.Fatalf("unexpected reasoning delta events: %+v", reasoningTexts)
	}
}

func TestStreamingIgnoresAsyncLateDeltasAfterGenerateReturns(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	var (
		mu     sync.Mutex
		events []Event
	)
	eng, err := New(store, fakeAsyncLateDeltaClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
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

	msg, err := eng.SubmitUserMessage(context.Background(), "test")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "final" {
		t.Fatalf("assistant content = %q, want final", msg.Content)
	}
	time.Sleep(40 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		t.Fatal("expected runtime events")
	}
	for _, evt := range events {
		if evt.Kind == EventAssistantDelta && evt.AssistantDelta == "late" {
			t.Fatalf("expected late delta to be ignored, got events: %+v", events)
		}
	}
}

func TestStreamingNoopFinalClearsLiveAssistantDelta(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	var (
		mu     sync.Mutex
		events []Event
	)
	eng, err := New(store, fakeNoopStreamClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
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

	msg, err := eng.SubmitUserMessage(context.Background(), "stream noop")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "" {
		t.Fatalf("assistant content = %q, want empty", msg.Content)
	}
	if ongoing := strings.TrimSpace(eng.ChatSnapshot().Ongoing); ongoing != "" {
		t.Fatalf("expected ongoing cleared after noop final, got %q", ongoing)
	}

	mu.Lock()
	defer mu.Unlock()
	hasDelta := false
	hasReset := false
	hasAssistantMessage := false
	hasModelResponse := false
	for _, evt := range events {
		switch evt.Kind {
		case EventAssistantDelta:
			if evt.AssistantDelta == reviewerNoopToken {
				hasDelta = true
			}
		case EventAssistantDeltaReset:
			hasReset = true
		case EventAssistantMessage:
			hasAssistantMessage = true
		case EventModelResponse:
			hasModelResponse = true
		}
	}
	if !hasDelta {
		t.Fatalf("expected streamed noop delta event, got %+v", events)
	}
	if !hasReset {
		t.Fatalf("expected assistant delta reset for noop final, got %+v", events)
	}
	if hasAssistantMessage {
		t.Fatalf("did not expect assistant_message event for noop final, got %+v", events)
	}
	if hasModelResponse {
		t.Fatalf("did not expect model_response_received event for noop final, got %+v", events)
	}
}

func TestStreamingDeltasDoNotEmitConversationSnapshotEvents(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	var (
		mu                   sync.Mutex
		events               []Event
		conversationWithLive int
	)
	var eng *Engine
	eng, err = New(store, fakeSimpleStreamClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, evt)
			if evt.Kind == EventConversationUpdated && eng != nil {
				if strings.TrimSpace(eng.ChatSnapshot().Ongoing) != "" {
					conversationWithLive++
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "stream")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "ab" {
		t.Fatalf("assistant content = %q, want ab", msg.Content)
	}

	mu.Lock()
	defer mu.Unlock()
	if conversationWithLive != 0 {
		t.Fatalf("expected no conversation_updated events carrying live ongoing snapshot, got %d events: %+v", conversationWithLive, events)
	}
}

func TestChatSnapshotOngoingTracksStreamingAndClearsOnCommit(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	var (
		mu             sync.Mutex
		deltaSnapshots []string
	)
	var eng *Engine
	eng, err = New(store, fakeSimpleStreamClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			if evt.Kind != EventAssistantDelta || eng == nil {
				return
			}
			mu.Lock()
			deltaSnapshots = append(deltaSnapshots, eng.ChatSnapshot().Ongoing)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	_, err = eng.SubmitUserMessage(context.Background(), "stream")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	mu.Lock()
	if len(deltaSnapshots) != 2 {
		mu.Unlock()
		t.Fatalf("expected two assistant delta snapshots, got %d", len(deltaSnapshots))
	}
	if deltaSnapshots[0] != "a" || deltaSnapshots[1] != "ab" {
		mu.Unlock()
		t.Fatalf("unexpected ongoing snapshots during streaming: %+v", deltaSnapshots)
	}
	mu.Unlock()

	if ongoing := strings.TrimSpace(eng.ChatSnapshot().Ongoing); ongoing != "" {
		t.Fatalf("expected ongoing cleared after commit, got %q", ongoing)
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

func TestProviderContractErrorsAreNotRetried(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &providerContractFailClient{}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	_, err = eng.SubmitUserMessage(context.Background(), "trigger provider contract error")
	if err == nil {
		t.Fatal("expected provider contract failure")
	}
	if !llm.IsNonRetriableModelError(err) {
		t.Fatalf("expected non-retriable provider contract error, got %v", err)
	}
	if client.Calls() != 1 {
		t.Fatalf("expected single model attempt on provider contract error, got %d", client.Calls())
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
	if len(requestMessages(firstReq)) < 5 {
		t.Fatalf("expected at least 5 messages in first request, got %d", len(requestMessages(firstReq)))
	}
	if requestMessages(firstReq)[0].Role != llm.RoleDeveloper || requestMessages(firstReq)[0].Content != "existing context" {
		t.Fatalf("expected first message to be existing context, got %+v", requestMessages(firstReq)[0])
	}
	if requestMessages(firstReq)[1].Role != llm.RoleDeveloper || !strings.Contains(requestMessages(firstReq)[1].Content, "source: "+globalPath) {
		t.Fatalf("expected second message to be global developer AGENTS injection, got %+v", requestMessages(firstReq)[1])
	}
	if requestMessages(firstReq)[1].MessageType != llm.MessageTypeAgentsMD {
		t.Fatalf("expected global AGENTS message type, got %+v", requestMessages(firstReq)[1])
	}
	if requestMessages(firstReq)[2].Role != llm.RoleDeveloper || !strings.Contains(requestMessages(firstReq)[2].Content, "source: "+workspacePath) {
		t.Fatalf("expected third message to be workspace developer AGENTS injection, got %+v", requestMessages(firstReq)[2])
	}
	if requestMessages(firstReq)[2].MessageType != llm.MessageTypeAgentsMD {
		t.Fatalf("expected workspace AGENTS message type, got %+v", requestMessages(firstReq)[2])
	}
	envMsg := requestMessages(firstReq)[3]
	if envMsg.Role != llm.RoleDeveloper || !strings.Contains(envMsg.Content, environmentInjectedHeader) {
		t.Fatalf("expected fourth message to be environment developer injection, got %+v", envMsg)
	}
	if envMsg.MessageType != llm.MessageTypeEnvironment {
		t.Fatalf("expected environment message type, got %+v", envMsg)
	}
	for _, required := range []string{
		"\ngpt-5\n",
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
	if requestMessages(firstReq)[4].Role != llm.RoleUser || requestMessages(firstReq)[4].Content != "first" {
		t.Fatalf("expected user message after injections, got %+v", requestMessages(firstReq)[4])
	}

	secondReq := client.calls[1]
	injectedCount := 0
	envInjectedCount := 0
	for _, msg := range requestMessages(secondReq) {
		if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeAgentsMD {
			injectedCount++
		}
		if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeEnvironment {
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
	if len(requestMessages(req)) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(requestMessages(req)))
	}
	if requestMessages(req)[0].Role != llm.RoleDeveloper || !strings.Contains(requestMessages(req)[0].Content, environmentInjectedHeader) {
		t.Fatalf("expected first message to be environment injection, got %+v", requestMessages(req)[0])
	}
	if !strings.Contains(requestMessages(req)[0].Content, "\ngpt-5\n") {
		t.Fatalf("expected environment injection to include model label, got %+v", requestMessages(req)[0])
	}
	if requestMessages(req)[1].Role != llm.RoleUser || requestMessages(req)[1].Content != "first" {
		t.Fatalf("expected user message after environment injection, got %+v", requestMessages(req)[1])
	}
}

func TestInjectsSkillsContextBeforeEnvironmentAndPersists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := t.TempDir()
	homeSkillPath := writeTestSkill(t, filepath.Join(home, ".builder", "skills", "home-skill"), "home-skill", "from home")
	workspaceSkillPath := writeTestSkill(t, filepath.Join(workspace, ".builder", "skills", "workspace-skill"), "workspace-skill", "from workspace")

	storeRoot := t.TempDir()
	store, err := session.Create(storeRoot, "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok-1"}, Usage: llm.Usage{WindowTokens: 200000}},
		{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok-2"}, Usage: llm.Usage{WindowTokens: 200000}},
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

	if len(client.calls) != 2 {
		t.Fatalf("expected two model calls, got %d", len(client.calls))
	}

	firstReq := client.calls[0]
	skillsIdx := -1
	envIdx := -1
	userIdx := -1
	for i, msg := range requestMessages(firstReq) {
		if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeSkills {
			skillsIdx = i
			if !strings.Contains(msg.Content, "- home-skill: from home (file: "+filepath.ToSlash(homeSkillPath)+")") {
				t.Fatalf("expected injected skills context to include home skill entry, got %q", msg.Content)
			}
			if !strings.Contains(msg.Content, "- workspace-skill: from workspace (file: "+filepath.ToSlash(workspaceSkillPath)+")") {
				t.Fatalf("expected injected skills context to include workspace skill entry, got %q", msg.Content)
			}
		}
		if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeEnvironment {
			envIdx = i
		}
		if msg.Role == llm.RoleUser && msg.Content == "first" {
			userIdx = i
		}
	}
	if skillsIdx < 0 {
		t.Fatalf("expected injected skills developer message in first request, messages=%+v", requestMessages(firstReq))
	}
	if envIdx < 0 {
		t.Fatalf("expected injected environment developer message in first request, messages=%+v", requestMessages(firstReq))
	}
	if userIdx < 0 {
		t.Fatalf("expected first user message in first request, messages=%+v", requestMessages(firstReq))
	}
	if !(skillsIdx < envIdx && envIdx < userIdx) {
		t.Fatalf("expected skills -> environment -> user ordering, got skills=%d env=%d user=%d", skillsIdx, envIdx, userIdx)
	}

	secondReq := client.calls[1]
	skillsInjectedCount := 0
	for _, msg := range requestMessages(secondReq) {
		if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeSkills {
			skillsInjectedCount++
		}
	}
	if skillsInjectedCount != 1 {
		t.Fatalf("expected exactly one injected skills message to persist, got %d", skillsInjectedCount)
	}
}

func TestDisabledSkillsAreNotInjectedIntoNewSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := t.TempDir()
	homeSkillPath := writeTestSkill(t, filepath.Join(home, ".builder", "skills", "home-skill"), "home-skill", "from home")
	writeTestSkill(t, filepath.Join(workspace, ".builder", "skills", "workspace-skill"), "Workspace Skill", "from workspace")

	storeRoot := t.TempDir()
	store, err := session.Create(storeRoot, "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok"}, Usage: llm.Usage{WindowTokens: 200000}}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		DisabledSkills: map[string]bool{"workspace skill": true},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "first"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected one model call, got %d", len(client.calls))
	}

	for _, msg := range requestMessages(client.calls[0]) {
		if msg.Role != llm.RoleDeveloper || msg.MessageType != llm.MessageTypeSkills {
			continue
		}
		if strings.Contains(msg.Content, "Workspace Skill") {
			t.Fatalf("did not expect disabled workspace skill in injected skills context, got %q", msg.Content)
		}
		if !strings.Contains(msg.Content, "- home-skill: from home (file: "+filepath.ToSlash(homeSkillPath)+")") {
			t.Fatalf("expected enabled home skill to remain, got %q", msg.Content)
		}
		return
	}
	t.Fatalf("expected skills developer message in first request, messages=%+v", requestMessages(client.calls[0]))
}

func TestBrokenSymlinkedSkillsAreSkippedAndWarnedInTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := t.TempDir()
	validSkillPath := writeTestSkill(t, filepath.Join(workspace, ".builder", "skills", "valid-skill"), "valid-skill", "from workspace")
	brokenLinkPath := filepath.Join(workspace, ".builder", "skills", "broken-skill")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing-skill-dir"), brokenLinkPath); err != nil {
		t.Fatalf("symlink broken skill dir: %v", err)
	}

	storeRoot := t.TempDir()
	store, err := session.Create(storeRoot, "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok"}, Usage: llm.Usage{WindowTokens: 200000}}}}
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

	foundSkills := false
	for _, msg := range requestMessages(client.calls[0]) {
		if msg.Role != llm.RoleDeveloper || msg.MessageType != llm.MessageTypeSkills {
			continue
		}
		foundSkills = true
		if !strings.Contains(msg.Content, "- valid-skill: from workspace (file: "+filepath.ToSlash(validSkillPath)+")") {
			t.Fatalf("expected valid skill to remain injected, got %q", msg.Content)
		}
		if strings.Contains(msg.Content, "broken-skill") {
			t.Fatalf("did not expect broken symlinked skill in injected context, got %q", msg.Content)
		}
	}
	if !foundSkills {
		t.Fatalf("expected skills developer message in first request, messages=%+v", requestMessages(client.calls[0]))
	}

	snapshot := eng.ChatSnapshot()
	foundWarning := false
	for _, entry := range snapshot.Entries {
		if entry.Role != "error" {
			continue
		}
		if strings.Contains(entry.Text, "Skipped skill \"broken-skill\"") && strings.Contains(entry.Text, filepath.ToSlash(brokenLinkPath)) {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected broken skill warning in transcript, entries=%+v", snapshot.Entries)
	}
}

func TestEnvironmentContextMessageIncludesStatusLineModelLabel(t *testing.T) {
	workspace := t.TempDir()
	msg := environmentContextMessage(workspace, "gpt-5.3-codex", "high", time.Unix(0, 0).UTC())
	if !strings.Contains(msg, "\ngpt-5.3-codex high\n") {
		t.Fatalf("expected environment message to include status-line model label, got %q", msg)
	}
}

func TestSubmitInjectsEnvironmentLineWithStatusModelLabel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := t.TempDir()
	storeRoot := t.TempDir()
	store, err := session.Create(storeRoot, "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Phase: llm.MessagePhaseFinal, Content: "ok"},
		OutputItems: []llm.ResponseItem{{
			Type:    llm.ResponseItemTypeMessage,
			Role:    llm.RoleAssistant,
			Phase:   llm.MessagePhaseFinal,
			Content: "ok",
		}},
		Usage: llm.Usage{WindowTokens: 200000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                 "gpt-5.3-codex",
		ThinkingLevel:         "high",
		AutoCompactTokenLimit: 1_000_000_000,
		CompactionMode:        "local",
	})
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
	if len(requestMessages(req)) < 2 {
		t.Fatalf("expected environment and user messages, got %d", len(requestMessages(req)))
	}
	envMsg := requestMessages(req)[0]
	if envMsg.Role != llm.RoleDeveloper || envMsg.MessageType != llm.MessageTypeEnvironment {
		t.Fatalf("expected first request message to be environment context, got %+v", envMsg)
	}
	if !strings.Contains(envMsg.Content, "\ngpt-5.3-codex high\n") {
		t.Fatalf("expected environment context to contain status model label line, got %q", envMsg.Content)
	}
}

func TestHeadlessModeTransitionDecisionsFollowLatestMarker(t *testing.T) {
	if headlessModeActive(nil) {
		t.Fatal("did not expect headless mode without history")
	}
	if !shouldInjectHeadlessModePrompt(nil) {
		t.Fatal("expected enter prompt when no headless marker exists")
	}
	if shouldInjectHeadlessModeExitPrompt(nil) {
		t.Fatal("did not expect exit prompt without an active headless phase")
	}

	headless := []llm.Message{{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHeadlessMode, Content: "headless"}}
	if !headlessModeActive(headless) {
		t.Fatal("expected headless mode to be active after headless marker")
	}
	if shouldInjectHeadlessModePrompt(headless) {
		t.Fatal("did not expect enter prompt during active headless phase")
	}
	if !shouldInjectHeadlessModeExitPrompt(headless) {
		t.Fatal("expected exit prompt during active headless phase")
	}

	exited := []llm.Message{
		{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHeadlessMode, Content: "headless"},
		{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHeadlessModeExit, Content: "exit"},
	}
	if headlessModeActive(exited) {
		t.Fatal("did not expect headless mode after exit marker")
	}
	if !shouldInjectHeadlessModePrompt(exited) {
		t.Fatal("expected enter prompt after exit marker")
	}
	if shouldInjectHeadlessModeExitPrompt(exited) {
		t.Fatal("did not expect exit prompt after exit marker")
	}
}

func TestSubmitUserMessageInjectsHeadlessEnterPromptWhenContinuingRegularSessionInHeadlessMode(t *testing.T) {
	prevHeadlessPrompt := prompts.HeadlessModePrompt
	prompts.HeadlessModePrompt = "headless mode instructions"
	defer func() {
		prompts.HeadlessModePrompt = prevHeadlessPrompt
	}()

	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	interactiveClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Phase: llm.MessagePhaseFinal, Content: "interactive-ok"},
		OutputItems: []llm.ResponseItem{{
			Type:    llm.ResponseItemTypeMessage,
			Role:    llm.RoleAssistant,
			Phase:   llm.MessagePhaseFinal,
			Content: "interactive-ok",
		}},
		Usage: llm.Usage{WindowTokens: 200000},
	}}}
	interactiveEngine, err := New(store, interactiveClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new interactive engine: %v", err)
	}
	if _, err := interactiveEngine.SubmitUserMessage(context.Background(), "regular start"); err != nil {
		t.Fatalf("interactive submit: %v", err)
	}

	headlessClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Phase: llm.MessagePhaseFinal, Content: "headless-ok-1"},
			OutputItems: []llm.ResponseItem{{
				Type:    llm.ResponseItemTypeMessage,
				Role:    llm.RoleAssistant,
				Phase:   llm.MessagePhaseFinal,
				Content: "headless-ok-1",
			}},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Phase: llm.MessagePhaseFinal, Content: "headless-ok-2"},
			OutputItems: []llm.ResponseItem{{
				Type:    llm.ResponseItemTypeMessage,
				Role:    llm.RoleAssistant,
				Phase:   llm.MessagePhaseFinal,
				Content: "headless-ok-2",
			}},
			Usage: llm.Usage{WindowTokens: 200000},
		},
	}}
	headlessEngine, err := New(store, headlessClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5", HeadlessMode: true})
	if err != nil {
		t.Fatalf("new headless engine: %v", err)
	}

	if _, err := headlessEngine.SubmitUserMessage(context.Background(), "continue headlessly"); err != nil {
		t.Fatalf("headless submit 1: %v", err)
	}
	if _, err := headlessEngine.SubmitUserMessage(context.Background(), "continue headlessly again"); err != nil {
		t.Fatalf("headless submit 2: %v", err)
	}

	if len(headlessClient.calls) != 2 {
		t.Fatalf("expected two headless calls, got %d", len(headlessClient.calls))
	}
	firstReq := headlessClient.calls[0]
	headlessIdx := -1
	userIdx := -1
	for i, msg := range requestMessages(firstReq) {
		if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeHeadlessMode {
			headlessIdx = i
		}
		if msg.Role == llm.RoleUser && msg.Content == "continue headlessly" {
			userIdx = i
		}
	}
	if headlessIdx < 0 {
		t.Fatalf("expected enter prompt when switching regular session into headless mode, messages=%+v", requestMessages(firstReq))
	}
	if userIdx < 0 || headlessIdx >= userIdx {
		t.Fatalf("expected headless enter prompt before user message, headless=%d user=%d messages=%+v", headlessIdx, userIdx, requestMessages(firstReq))
	}
	secondReq := headlessClient.calls[1]
	headlessCount := 0
	for _, msg := range requestMessages(secondReq) {
		if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeHeadlessMode {
			headlessCount++
		}
	}
	if headlessCount != 1 {
		t.Fatalf("expected exactly one persisted headless enter marker, got %d messages=%+v", headlessCount, requestMessages(secondReq))
	}
}

func TestSubmitUserMessageInjectsHeadlessExitPromptOnFirstInteractiveTurn(t *testing.T) {
	prevHeadlessPrompt := prompts.HeadlessModePrompt
	prevExitPrompt := prompts.HeadlessModeExitPrompt
	prompts.HeadlessModePrompt = "headless mode instructions"
	prompts.HeadlessModeExitPrompt = "interactive mode instructions"
	defer func() {
		prompts.HeadlessModePrompt = prevHeadlessPrompt
		prompts.HeadlessModeExitPrompt = prevExitPrompt
	}()

	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	headlessClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Phase: llm.MessagePhaseFinal, Content: "headless-ok"},
		OutputItems: []llm.ResponseItem{{
			Type:    llm.ResponseItemTypeMessage,
			Role:    llm.RoleAssistant,
			Phase:   llm.MessagePhaseFinal,
			Content: "headless-ok",
		}},
		Usage: llm.Usage{WindowTokens: 200000},
	}}}
	headlessEngine, err := New(store, headlessClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5", HeadlessMode: true})
	if err != nil {
		t.Fatalf("new headless engine: %v", err)
	}
	if _, err := headlessEngine.SubmitUserMessage(context.Background(), "run headless"); err != nil {
		t.Fatalf("headless submit: %v", err)
	}

	interactiveClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Phase: llm.MessagePhaseFinal, Content: "interactive-ok-1"},
			OutputItems: []llm.ResponseItem{{
				Type:    llm.ResponseItemTypeMessage,
				Role:    llm.RoleAssistant,
				Phase:   llm.MessagePhaseFinal,
				Content: "interactive-ok-1",
			}},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Phase: llm.MessagePhaseFinal, Content: "interactive-ok-2"},
			OutputItems: []llm.ResponseItem{{
				Type:    llm.ResponseItemTypeMessage,
				Role:    llm.RoleAssistant,
				Phase:   llm.MessagePhaseFinal,
				Content: "interactive-ok-2",
			}},
			Usage: llm.Usage{WindowTokens: 200000},
		},
	}}
	interactiveEngine, err := New(store, interactiveClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new interactive engine: %v", err)
	}

	if _, err := interactiveEngine.SubmitUserMessage(context.Background(), "continue interactively"); err != nil {
		t.Fatalf("interactive submit 1: %v", err)
	}
	if _, err := interactiveEngine.SubmitUserMessage(context.Background(), "continue again"); err != nil {
		t.Fatalf("interactive submit 2: %v", err)
	}

	if len(interactiveClient.calls) != 2 {
		t.Fatalf("expected two interactive model calls, got %d", len(interactiveClient.calls))
	}

	firstReq := interactiveClient.calls[0]
	headlessIdx := -1
	exitIdx := -1
	userIdx := -1
	for i, msg := range requestMessages(firstReq) {
		if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeHeadlessMode {
			headlessIdx = i
		}
		if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeHeadlessModeExit {
			exitIdx = i
		}
		if msg.Role == llm.RoleUser && msg.Content == "continue interactively" {
			userIdx = i
		}
	}
	if headlessIdx < 0 {
		t.Fatalf("expected prior headless prompt in first interactive request, messages=%+v", requestMessages(firstReq))
	}
	if exitIdx < 0 {
		t.Fatalf("expected exit prompt in first interactive request, messages=%+v", requestMessages(firstReq))
	}
	if userIdx < 0 {
		t.Fatalf("expected interactive user message in first request, messages=%+v", requestMessages(firstReq))
	}
	if !(headlessIdx < exitIdx && exitIdx < userIdx) {
		t.Fatalf("expected headless -> exit -> user ordering, got headless=%d exit=%d user=%d", headlessIdx, exitIdx, userIdx)
	}

	secondReq := interactiveClient.calls[1]
	exitCount := 0
	for _, msg := range requestMessages(secondReq) {
		if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeHeadlessModeExit {
			exitCount++
		}
	}
	if exitCount != 1 {
		t.Fatalf("expected exactly one persisted exit prompt in later requests, got %d messages=%+v", exitCount, requestMessages(secondReq))
	}
}

func TestSubmitUserMessageDoesNotInjectHeadlessExitPromptForNormalSession(t *testing.T) {
	prevExitPrompt := prompts.HeadlessModeExitPrompt
	prompts.HeadlessModeExitPrompt = "interactive mode instructions"
	defer func() {
		prompts.HeadlessModeExitPrompt = prevExitPrompt
	}()

	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Phase: llm.MessagePhaseFinal, Content: "ok"},
		OutputItems: []llm.ResponseItem{{
			Type:    llm.ResponseItemTypeMessage,
			Role:    llm.RoleAssistant,
			Phase:   llm.MessagePhaseFinal,
			Content: "ok",
		}},
		Usage: llm.Usage{WindowTokens: 200000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "plain user"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected one model call, got %d", len(client.calls))
	}
	for _, msg := range requestMessages(client.calls[0]) {
		if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeHeadlessModeExit {
			t.Fatalf("did not expect headless exit prompt in normal session, messages=%+v", requestMessages(client.calls[0]))
		}
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
	for _, m := range requestMessages(second) {
		if m.Role == llm.RoleUser && m.Content == "steer now" {
			hasInjected = true
			break
		}
	}
	if !hasInjected {
		t.Fatalf("expected flushed user message in second request, messages=%+v", requestMessages(second))
	}
}

func TestQueuedUserMessageFlushedEventPrecedesConversationUpdateForInjectedMessage(t *testing.T) {
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

	var (
		eng                   *Engine
		eventIndex            int
		flushIndex            = -1
		userConversationIndex = -1
	)
	eng, err = New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			eventIndex++
			if evt.Kind == EventUserMessageFlushed && evt.UserMessage == "steer now" && flushIndex < 0 {
				flushIndex = eventIndex
			}
			if evt.Kind != EventConversationUpdated || eng == nil || userConversationIndex >= 0 {
				return
			}
			snapshot := eng.ChatSnapshot()
			if len(snapshot.Entries) == 0 {
				return
			}
			last := snapshot.Entries[len(snapshot.Entries)-1]
			if last.Role == string(llm.RoleUser) && last.Text == "steer now" {
				userConversationIndex = eventIndex
			}
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	eng.QueueUserMessage("steer now")
	if _, err := eng.SubmitUserMessage(context.Background(), "start"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if flushIndex < 0 {
		t.Fatal("expected user_message_flushed event")
	}
	if userConversationIndex < 0 {
		t.Fatal("expected conversation_updated event for injected user message")
	}
	if flushIndex >= userConversationIndex {
		t.Fatalf("expected flushed event before conversation update, got flush=%d conversation=%d", flushIndex, userConversationIndex)
	}
}

func TestQueuedUserMessagesCoalesceIntoSingleFlush(t *testing.T) {
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

	var (
		flushCount int
		flushed    Event
	)
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			if evt.Kind == EventUserMessageFlushed {
				flushCount++
				flushed = evt
			}
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	eng.QueueUserMessage("steer now")
	eng.QueueUserMessage("and keep tests focused")
	msg, err := eng.SubmitUserMessage(context.Background(), "start")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "after flush" {
		t.Fatalf("assistant content = %q, want after flush", msg.Content)
	}
	if flushed.UserMessage != "steer now\n\nand keep tests focused" {
		t.Fatalf("unexpected flushed user message %q", flushed.UserMessage)
	}
	if len(flushed.UserMessageBatch) != 2 {
		t.Fatalf("expected two flushed user messages in batch, got %+v", flushed.UserMessageBatch)
	}
	if flushCount != 1 {
		t.Fatalf("expected one flush event, got %d", flushCount)
	}
	if len(client.calls) < 2 {
		t.Fatalf("expected at least 2 model calls, got %d", len(client.calls))
	}
	second := client.calls[1]
	userMessages := make([]llm.Message, 0, len(requestMessages(second)))
	for _, m := range requestMessages(second) {
		if m.Role == llm.RoleUser {
			userMessages = append(userMessages, m)
		}
	}
	if len(userMessages) < 2 {
		t.Fatalf("expected initial and flushed user messages, got %+v", requestMessages(second))
	}
	last := userMessages[len(userMessages)-1]
	if last.Content != "steer now\n\nand keep tests focused" {
		t.Fatalf("expected coalesced flushed user message, got %+v", userMessages)
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
		for _, msg := range requestMessages(req) {
			if strings.Contains(msg.Content, "\x1b[") {
				t.Fatalf("request message contains ANSI escape sequence: role=%s content=%q", msg.Role, msg.Content)
			}
		}
	}
}

func TestSanitizeMessagesForLLMNormalizesToolJSONEscapes(t *testing.T) {
	input := []llm.ResponseItem{
		{Type: llm.ResponseItemTypeFunctionCallOutput, CallID: "call_1", Output: json.RawMessage(`{"exit_code":0,"output":"a =\u003e b \u003c c \u0026 d","truncated":false}`)},
	}

	got := sanitizeItemsForLLM(input)
	if len(got) != 1 {
		t.Fatalf("unexpected item count: %d", len(got))
	}
	normalized := string(got[0].Output)
	if strings.Contains(normalized, `\u003e`) || strings.Contains(normalized, `\u003c`) || strings.Contains(normalized, `\u0026`) {
		t.Fatalf("expected HTML escapes to be normalized, got %q", normalized)
	}
	if !strings.Contains(normalized, "=>") || !strings.Contains(normalized, "<") || !strings.Contains(normalized, "&") {
		t.Fatalf("expected decoded operators in normalized tool content, got %q", normalized)
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
	for _, msg := range requestMessages(secondReq) {
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
		t.Fatalf("expected prior assistant message to carry encrypted reasoning item, got %+v", requestMessages(secondReq))
	}
	for _, msg := range requestMessages(secondReq) {
		if strings.Contains(msg.Content, "Plan summary") {
			t.Fatalf("reasoning summary text should not be sent back to model input, found in %+v", requestMessages(secondReq))
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

func TestContextUsageTracksWeightedCacheHitPercentageFromModelUsage(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if usage := eng.ContextUsage(); usage.HasCacheHitPercentage {
		t.Fatalf("expected cache hit percentage to be unavailable before model usage, got %+v", usage)
	}

	eng.setLastUsage(llm.Usage{InputTokens: 100, CachedInputTokens: 40, HasCachedInputTokens: true})
	eng.setLastUsage(llm.Usage{InputTokens: 300, CachedInputTokens: 60, HasCachedInputTokens: true})
	eng.setLastUsage(llm.Usage{InputTokens: 999})

	usage := eng.ContextUsage()
	if !usage.HasCacheHitPercentage {
		t.Fatalf("expected cache hit percentage to be available, got %+v", usage)
	}
	if usage.CacheHitPercent != 25 {
		t.Fatalf("cache hit percent=%d, want 25", usage.CacheHitPercent)
	}
}

func TestContextUsageUsesEstimatedTokensWhenLastUsageIsStale(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 100, OutputTokens: 0, WindowTokens: 410_000})
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: strings.Repeat("x", 1600)}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	estimated := estimateItemsTokens(eng.snapshotItems())
	if estimated <= 100 {
		t.Fatalf("expected estimated tokens above stale usage baseline, got %d", estimated)
	}

	usage := eng.ContextUsage()
	if usage.UsedTokens != estimated {
		t.Fatalf("used tokens=%d, want estimated %d", usage.UsedTokens, estimated)
	}
}

func TestEstimateItemsTokensDoesNotTreatInlineImagePayloadAsPlainText(t *testing.T) {
	base64Payload := strings.Repeat("A", 24_000)
	item := llm.ResponseItem{
		Type:   llm.ResponseItemTypeFunctionCallOutput,
		Name:   string(tools.ToolViewImage),
		CallID: "call-1",
		Output: json.RawMessage(`[{"type":"input_image","image_url":"data:image/png;base64,` + base64Payload + `"}]`),
	}

	estimated := estimateItemsTokens([]llm.ResponseItem{item})
	naive := (len(item.Name) + len(item.CallID) + len(item.Output) + 3) / 4
	if estimated <= 0 {
		t.Fatalf("expected multimodal estimate > 0, got %d", estimated)
	}
	if estimated >= naive/4 {
		t.Fatalf("expected multimodal estimate to stay well below plain-text estimate, got estimated=%d naive=%d", estimated, naive)
	}
}

func TestContextUsageDoesNotInflateInlineImagePayloadByBase64Length(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 100, OutputTokens: 0, WindowTokens: 410_000})
	if err := eng.appendMessage("", llm.Message{
		Role:       llm.RoleTool,
		ToolCallID: "call-1",
		Name:       string(tools.ToolViewImage),
		Content:    `[{"type":"input_image","image_url":"data:image/png;base64,` + strings.Repeat("A", 24_000) + `"}]`,
	}); err != nil {
		t.Fatalf("append tool message: %v", err)
	}

	usage := eng.ContextUsage()
	if usage.UsedTokens <= 100 {
		t.Fatalf("expected local estimate to exceed stale usage baseline, got %d", usage.UsedTokens)
	}
	if usage.UsedTokens >= 2_000 {
		t.Fatalf("expected inline image estimate to avoid base64 inflation, got %d", usage.UsedTokens)
	}
}

func TestShouldAutoCompactAccountsForMessagesAppendedAfterLastUsage(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   410_000,
		AutoCompactTokenLimit: 300,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 120, OutputTokens: 0, WindowTokens: 410_000})
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: strings.Repeat("stale-usage-gap-", 120)}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if !eng.shouldAutoCompact() {
		t.Fatalf("expected auto compaction to trigger from appended message growth")
	}
}

func TestShouldAutoCompactUsesPreciseRequestInputTokenCountWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &preciseCompactionClient{inputTokenCount: 960, contextWindow: 1000}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   400_000,
		AutoCompactTokenLimit: 900,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "short"}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if !eng.shouldAutoCompact() {
		t.Fatalf("expected auto compaction to trigger from precise input token count")
	}
}

func TestPreSubmitCompactionTokenLimitUsesSmallerOfWindowHeadroomAndLeadCap(t *testing.T) {
	tests := []struct {
		name     string
		window   int
		limit    int
		leadCap  int
		expected int
	}{
		{
			name:     "window headroom smaller than lead cap",
			window:   200_000,
			limit:    190_000,
			leadCap:  15_000,
			expected: 180_000,
		},
		{
			name:     "lead cap smaller than window headroom",
			window:   400_000,
			limit:    380_000,
			leadCap:  15_000,
			expected: 365_000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := session.Create(dir, "ws", dir)
			if err != nil {
				t.Fatalf("create store: %v", err)
			}

			eng, err := New(store, &fakeClient{}, tools.NewRegistry(), Config{
				Model:                         "gpt-5",
				AutoCompactTokenLimit:         tt.limit,
				ContextWindowTokens:           tt.window,
				PreSubmitCompactionLeadTokens: tt.leadCap,
			})
			if err != nil {
				t.Fatalf("new engine: %v", err)
			}

			if got := eng.preSubmitCompactionTokenLimit(context.Background()); got != tt.expected {
				t.Fatalf("unexpected pre-submit compaction threshold: got %d want %d", got, tt.expected)
			}
		})
	}
}

func TestShouldCompactBeforeUserMessageUsesPromptGrowthBelowPreSubmitBand(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &preciseCompactionClient{inputTokenCount: 960, contextWindow: 1000}
	eng, err := New(store, client, tools.NewRegistry(), Config{
		Model:                         "gpt-5",
		AutoCompactTokenLimit:         950,
		ContextWindowTokens:           1000,
		PreSubmitCompactionLeadTokens: 50,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: strings.Repeat("a", 3400)}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	shouldCompact, err := eng.ShouldCompactBeforeUserMessage(context.Background(), strings.Repeat("b", 400))
	if err != nil {
		t.Fatalf("ShouldCompactBeforeUserMessage: %v", err)
	}
	if !shouldCompact {
		t.Fatal("expected pre-submit compaction when prompt growth would cross the real threshold")
	}
	if client.countCalls == 0 {
		t.Fatal("expected precise request token count to be used for prompt-growth check")
	}
}

func TestShouldAutoCompactRechecksProviderBeforeCompactingOnLargeEstimate(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &preciseCompactionClient{inputTokenCount: 1, contextWindow: 1000}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   400_000,
		AutoCompactTokenLimit: 2,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{
		Role:       llm.RoleTool,
		ToolCallID: "call-1",
		Name:       string(tools.ToolViewImage),
		Content:    `[{"type":"input_image","image_url":"data:image/png;base64,` + strings.Repeat("A", 24_000) + `"}]`,
	}); err != nil {
		t.Fatalf("append tool message: %v", err)
	}

	if eng.shouldAutoCompact() {
		t.Fatalf("expected provider token count to prevent over-eager compaction")
	}
	if client.countCalls != 1 {
		t.Fatalf("expected one precise token count before compact decision, got %d", client.countCalls)
	}
}

func TestShouldAutoCompactPrefersConfiguredThresholdOverResolvedContextWindow(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &preciseCompactionClient{inputTokenCount: 950, contextWindow: 1000}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   400_000,
		AutoCompactTokenLimit: 360_000,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "short"}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if eng.shouldAutoCompact() {
		t.Fatalf("expected auto compaction to honor configured threshold and remain below limit")
	}
	if client.resolveCalls != 0 {
		t.Fatalf("expected configured context window to bypass remote resolver, got resolveCalls=%d", client.resolveCalls)
	}
	eng.mu.Lock()
	defer eng.mu.Unlock()
	if eng.cfg.ContextWindowTokens != 400_000 {
		t.Fatalf("expected configured context window to remain unchanged, got %d", eng.cfg.ContextWindowTokens)
	}
}

func TestShouldAutoCompactAccountsForReservedOutputBudget(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &preciseCompactionClient{inputTokenCount: 850, contextWindow: 400000}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   400_000,
		AutoCompactTokenLimit: 900,
		MaxTokens:             100,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "short"}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if !eng.shouldAutoCompact() {
		t.Fatalf("expected auto compaction when input + reserved output exceeds threshold")
	}
}

func TestShouldAutoCompactSkipsPreciseCountWhenFarBelowThreshold(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &preciseCompactionClient{inputTokenCount: 999999, contextWindow: 400000}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   400_000,
		AutoCompactTokenLimit: 100_000,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "short"}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if eng.shouldAutoCompact() {
		t.Fatalf("expected no compaction when far below configured threshold")
	}
	if client.countCalls != 0 {
		t.Fatalf("expected precise token counting to be skipped when far below threshold, got countCalls=%d", client.countCalls)
	}
}

func TestShouldAutoCompactMemoizesPreciseCountForUnchangedRequest(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &preciseCompactionClient{inputTokenCount: 96000, contextWindow: 400000}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   400_000,
		AutoCompactTokenLimit: 100_000,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 95_000, WindowTokens: 400_000})

	if eng.shouldAutoCompact() {
		t.Fatalf("expected no compaction for precise count below threshold")
	}
	if eng.shouldAutoCompact() {
		t.Fatalf("expected no compaction for repeated unchanged request")
	}
	if client.countCalls != 1 {
		t.Fatalf("expected memoized precise token count across unchanged checks, got countCalls=%d", client.countCalls)
	}
}

func TestCompactionSoonReminderCanBeIssuedAfterReEnablingAutoCompactionAboveReminderBand(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   2_000,
		AutoCompactTokenLimit: 1_000,
		CompactionMode:        "local",
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 890, WindowTokens: 2_000})

	changed, enabled := eng.SetAutoCompactionEnabled(false)
	if !changed || enabled {
		t.Fatalf("expected auto compaction toggle off, changed=%v enabled=%v", changed, enabled)
	}
	if err := eng.maybeAppendCompactionSoonReminder(context.Background(), "step-off"); err != nil {
		t.Fatalf("reminder while disabled: %v", err)
	}

	snap := eng.ChatSnapshot()
	if len(snap.Entries) != 1 {
		t.Fatalf("expected only seed entry while disabled, got %+v", snap.Entries)
	}

	changed, enabled = eng.SetAutoCompactionEnabled(true)
	if !changed || !enabled {
		t.Fatalf("expected auto compaction toggle on, changed=%v enabled=%v", changed, enabled)
	}
	if err := eng.maybeAppendCompactionSoonReminder(context.Background(), "step-on"); err != nil {
		t.Fatalf("reminder after re-enable: %v", err)
	}
	if err := eng.maybeAppendCompactionSoonReminder(context.Background(), "step-on-duplicate"); err != nil {
		t.Fatalf("duplicate reminder check: %v", err)
	}

	snap = eng.ChatSnapshot()
	reminders := 0
	for _, entry := range snap.Entries {
		if entry.Role == "warning" && entry.Text == strings.TrimSpace(prompts.CompactionSoonReminderPrompt) {
			reminders++
		}
	}
	if reminders != 1 {
		t.Fatalf("expected one reminder after re-enable, got %d entries=%+v", reminders, snap.Entries)
	}

	eng.setLastUsage(llm.Usage{InputTokens: 800, WindowTokens: 2_000})
	if err := eng.maybeAppendCompactionSoonReminder(context.Background(), "step-reset"); err != nil {
		t.Fatalf("reset reminder state: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 860, WindowTokens: 2_000})
	if err := eng.maybeAppendCompactionSoonReminder(context.Background(), "step-reissue"); err != nil {
		t.Fatalf("reissue reminder: %v", err)
	}

	snap = eng.ChatSnapshot()
	reminders = 0
	for _, entry := range snap.Entries {
		if entry.Role == "warning" && entry.Text == strings.TrimSpace(prompts.CompactionSoonReminderPrompt) {
			reminders++
		}
	}
	if reminders != 2 {
		t.Fatalf("expected reminder to re-arm after falling below threshold, got %d entries=%+v", reminders, snap.Entries)
	}
}

func TestRunStepLoopSkipsCompactionSoonReminderWhenImmediateAutoCompactionRuns(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{
		responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal}}},
		compactionResponses: []llm.CompactionResponse{{
			OutputItems: []llm.ResponseItem{
				{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "seed"},
				{Type: llm.ResponseItemTypeCompaction, ID: "cmp_1", EncryptedContent: "enc_1"},
			},
			Usage: llm.Usage{InputTokens: 100, WindowTokens: 2_000},
		}},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   2_000,
		AutoCompactTokenLimit: 1_000,
		MaxTokens:             20,
		CompactionMode:        "native",
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 990, WindowTokens: 2_000})

	msg, err := eng.runStepLoop(context.Background(), "step-1")
	if err != nil {
		t.Fatalf("runStepLoop: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("unexpected assistant message: %+v", msg)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected one model request after compaction, got %d", len(client.calls))
	}
	for _, reqMsg := range requestMessages(client.calls[0]) {
		if reqMsg.Role == llm.RoleDeveloper && reqMsg.MessageType == llm.MessageTypeCompactionSoonReminder {
			t.Fatalf("did not expect compaction-soon reminder in request after immediate auto-compaction, messages=%+v", requestMessages(client.calls[0]))
		}
	}

	snap := eng.ChatSnapshot()
	for _, entry := range snap.Entries {
		if entry.Role == "warning" && entry.Text == strings.TrimSpace(prompts.CompactionSoonReminderPrompt) {
			t.Fatalf("did not expect reminder in transcript after immediate auto-compaction, entries=%+v", snap.Entries)
		}
	}
}

func TestManualCompactionRemotePassesSlashCommandArgumentsAsInstructions(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{
		compactionResponses: []llm.CompactionResponse{
			{
				OutputItems: []llm.ResponseItem{
					{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "seed"},
					{Type: llm.ResponseItemTypeCompaction, ID: "cmp_1", EncryptedContent: "enc_1"},
				},
				Usage: llm.Usage{InputTokens: 1000, OutputTokens: 100, WindowTokens: 200000},
			},
		},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	args := "preserve migration caveats"
	if err := eng.CompactContext(context.Background(), args); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if len(client.compactionCalls) != 1 {
		t.Fatalf("expected one remote compact call, got %d", len(client.compactionCalls))
	}
	if got, want := client.compactionCalls[0].Instructions, compactionInstructions(args); got != want {
		t.Fatalf("unexpected compact instructions\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestManualCompactionLocalAppendsSlashCommandArgumentsToPrompt(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{
		responses: []llm.Response{
			{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "summary"}},
		},
	}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5", CompactionMode: "local"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	args := "keep TODO decisions"
	if err := eng.CompactContext(context.Background(), args); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected one local-summary model call, got %d", len(client.calls))
	}
	if len(client.calls[0].Tools) == 0 {
		t.Fatalf("expected tools to remain declared for local compaction cache stability")
	}

	additional := additionalCompactionInstructionsHeader + "\n " + args
	found := false
	for _, item := range client.calls[0].Items {
		if item.Type == llm.ResponseItemTypeMessage && item.Role == llm.RoleDeveloper && item.Content == compactionInstructions(args) && strings.HasSuffix(item.Content, additional) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected local compact prompt to include appended slash command args, got %+v", client.calls[0].Items)
	}
}

func TestManualCompactionAppendsLastVisibleUserMessageCarryover(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{
		compactionResponses: []llm.CompactionResponse{
			{
				OutputItems: []llm.ResponseItem{
					{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, MessageType: llm.MessageTypeCompactionSummary, Content: "condensed summary"},
					{Type: llm.ResponseItemTypeCompaction, ID: "cmp_1", EncryptedContent: "enc_1"},
				},
				Usage: llm.Usage{InputTokens: 1000, OutputTokens: 100, WindowTokens: 200000},
			},
		},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "please keep tests green"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, MessageType: llm.MessageTypeCompactionSummary, Content: "older summary"}); err != nil {
		t.Fatalf("append compaction summary: %v", err)
	}

	if err := eng.CompactContext(context.Background(), ""); err != nil {
		t.Fatalf("compact: %v", err)
	}

	messages := eng.snapshotMessages()
	if len(messages) == 0 {
		t.Fatal("expected messages after manual compaction")
	}
	carryover := messages[len(messages)-1]
	if carryover.Role != llm.RoleDeveloper {
		t.Fatalf("expected developer carryover message, got role=%q", carryover.Role)
	}
	if carryover.MessageType != llm.MessageTypeManualCompactionCarryover {
		t.Fatalf("expected manual compaction carryover message type, got %q", carryover.MessageType)
	}
	if !strings.Contains(carryover.Content, "please keep tests green") {
		t.Fatalf("expected carryover to include last visible user message, got %q", carryover.Content)
	}
	if strings.Contains(carryover.Content, "older summary") {
		t.Fatalf("did not expect prior compaction summary in carryover, got %q", carryover.Content)
	}
}

func TestRemoteCompactionTrimUsesSublinearPreciseTokenCountCalls(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	maxItemsSeen := 0
	client := &fakeCompactionClient{
		inputTokenCountFn: func(req llm.Request) int {
			if len(req.Items) > maxItemsSeen {
				maxItemsSeen = len(req.Items)
			}
			return len(req.Items) * 1000
		},
		compactionResponses: []llm.CompactionResponse{
			{
				OutputItems: []llm.ResponseItem{
					{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "seed"},
					{Type: llm.ResponseItemTypeCompaction, ID: "cmp_1", EncryptedContent: "enc_1"},
				},
				Usage: llm.Usage{InputTokens: 1000, OutputTokens: 100, WindowTokens: 400000},
			},
		},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5", ContextWindowTokens: 400_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	for i := 0; i < 600; i++ {
		if err := eng.appendMessage("", llm.Message{Role: llm.RoleAssistant, Content: "a"}); err != nil {
			t.Fatalf("append assistant message %d: %v", i, err)
		}
	}

	if err := eng.CompactContext(context.Background(), ""); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if maxItemsSeen <= 0 {
		t.Fatalf("expected at least one precise token-count request")
	}
	bound := 2*ceilLog2Int(maxItemsSeen+1) + 14
	if client.countInputTokenCalls > bound {
		t.Fatalf("expected sublinear precise token count calls, got=%d bound=%d n=%d", client.countInputTokenCalls, bound, maxItemsSeen)
	}
}

func TestLocalCompactionCarryoverUsesSublinearPreciseTokenCountCalls(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	maxItemsSeen := 0
	client := &fakeCompactionClient{
		inputTokenCountFn: func(req llm.Request) int {
			if len(req.Items) > maxItemsSeen {
				maxItemsSeen = len(req.Items)
			}
			return len(req.Items) * 1000
		},
		responses: []llm.Response{
			{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "summary"}, Usage: llm.Usage{WindowTokens: 400000}},
		},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:               "gpt-5",
		ContextWindowTokens: 400_000,
		CompactionMode:      "local",
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	for i := 0; i < 512; i++ {
		if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "u"}); err != nil {
			t.Fatalf("append user message %d: %v", i, err)
		}
	}

	if err := eng.CompactContext(context.Background(), ""); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if maxItemsSeen <= 0 {
		t.Fatalf("expected at least one precise token-count request")
	}
	bound := 2*ceilLog2Int(maxItemsSeen+1) + 16
	if client.countInputTokenCalls > bound {
		t.Fatalf("expected sublinear precise token count calls for local carryover, got=%d bound=%d n=%d", client.countInputTokenCalls, bound, maxItemsSeen)
	}
}

func ceilLog2Int(value int) int {
	if value <= 1 {
		return 0
	}
	pow := 0
	current := 1
	for current < value {
		current <<= 1
		pow++
	}
	return pow
}

func TestManualCompactionLocalUsesHistorySinceLastCompactionCheckpoint(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{
		responses: []llm.Response{
			{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "summary"}},
		},
	}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5", CompactionMode: "local"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, Content: "canonical context"}); err != nil {
		t.Fatalf("append canonical context: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "old user request"}); err != nil {
		t.Fatalf("append old user message: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleAssistant, Content: "old assistant response"}); err != nil {
		t.Fatalf("append old assistant message: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, MessageType: llm.MessageTypeCompactionSummary, Content: "old compacted summary"}); err != nil {
		t.Fatalf("append compaction checkpoint: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "new user request"}); err != nil {
		t.Fatalf("append new user message: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleAssistant, Content: "new assistant response"}); err != nil {
		t.Fatalf("append new assistant message: %v", err)
	}

	if err := eng.CompactContext(context.Background(), ""); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected one local-summary model call, got %d", len(client.calls))
	}
	if len(client.calls[0].Tools) == 0 {
		t.Fatalf("expected tools to remain declared for local compaction cache stability")
	}

	foundCanonical := false
	foundCheckpoint := false
	foundNewUser := false
	foundOldUser := false
	foundPrompt := false
	for _, item := range client.calls[0].Items {
		if item.Type != llm.ResponseItemTypeMessage {
			continue
		}
		if item.Role == llm.RoleDeveloper && item.Content == "canonical context" {
			foundCanonical = true
		}
		if item.Role == llm.RoleUser && item.MessageType == llm.MessageTypeCompactionSummary {
			foundCheckpoint = true
		}
		if item.Role == llm.RoleUser && item.Content == "new user request" {
			foundNewUser = true
		}
		if item.Role == llm.RoleUser && item.Content == "old user request" {
			foundOldUser = true
		}
		if item.Role == llm.RoleDeveloper && item.Content == prompts.CompactionPrompt {
			foundPrompt = true
		}
	}

	if !foundCanonical {
		t.Fatalf("expected canonical developer context in local compaction request, got %+v", client.calls[0].Items)
	}
	if !foundCheckpoint {
		t.Fatalf("expected last compaction checkpoint item in local compaction request, got %+v", client.calls[0].Items)
	}
	if !foundNewUser {
		t.Fatalf("expected post-checkpoint history in local compaction request, got %+v", client.calls[0].Items)
	}
	if foundOldUser {
		t.Fatalf("did not expect pre-checkpoint history in local compaction request, got %+v", client.calls[0].Items)
	}
	if !foundPrompt {
		t.Fatalf("expected compaction prompt as developer message, got %+v", client.calls[0].Items)
	}
}

func TestManualCompactionLocalFailsWhenModelAttemptsToolCalls(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{
		responses: []llm.Response{
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: ""},
				ToolCalls: []llm.ToolCall{{ID: "call_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)}},
			},
		},
	}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5", CompactionMode: "local"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	err = eng.CompactContext(context.Background(), "")
	if err == nil {
		t.Fatal("expected local compaction to fail when model attempts tool calls")
	}
	if !strings.Contains(err.Error(), "tool calls") {
		t.Fatalf("expected tool-call error, got %v", err)
	}
}

func TestManualCompactionDisabledWhenModeNone(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5", CompactionMode: "none"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	err = eng.CompactContext(context.Background(), "")
	if err == nil {
		t.Fatal("expected manual compaction to fail when compaction_mode=none")
	}
	if !strings.Contains(err.Error(), "compaction_mode=none") {
		t.Fatalf("expected disabled-compaction error, got %v", err)
	}
	if len(client.compactionCalls) != 0 {
		t.Fatalf("expected no remote compaction call when disabled, got %d", len(client.compactionCalls))
	}
	if len(client.calls) != 0 {
		t.Fatalf("expected no local-summary model call when disabled, got %d", len(client.calls))
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

func TestAutoCompactionRemoteCarriesCanonicalContextWithoutDuplication(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".builder")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("create global dir: %v", err)
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
	if len(client.calls) < 2 {
		t.Fatalf("expected second model call after compaction, got %d calls", len(client.calls))
	}

	post := client.calls[1]
	globalCount := 0
	workspaceCount := 0
	envCount := 0
	for _, item := range post.Items {
		if item.Type != llm.ResponseItemTypeMessage || item.Role != llm.RoleDeveloper {
			continue
		}
		if strings.Contains(item.Content, "source: "+globalPath) {
			globalCount++
		}
		if strings.Contains(item.Content, "source: "+workspacePath) {
			workspaceCount++
		}
		if strings.Contains(item.Content, environmentInjectedHeader) {
			envCount++
		}
	}
	if globalCount != 1 {
		t.Fatalf("expected exactly one global AGENTS context item after compaction, got %d", globalCount)
	}
	if workspaceCount != 1 {
		t.Fatalf("expected exactly one workspace AGENTS context item after compaction, got %d", workspaceCount)
	}
	if envCount != 1 {
		t.Fatalf("expected exactly one environment context item after compaction, got %d", envCount)
	}
}

func TestSanitizeRemoteCompactionOutputAcceptsEncryptedReasoningCheckpoint(t *testing.T) {
	output := []llm.ResponseItem{
		{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "u1"},
		{Type: llm.ResponseItemTypeReasoning, ID: "rs_1", EncryptedContent: "enc_reason"},
	}

	replacement, err := sanitizeRemoteCompactionOutput(output)
	if err != nil {
		t.Fatalf("sanitize remote compaction output: %v", err)
	}

	foundReasoning := false
	for _, item := range replacement {
		if item.Type == llm.ResponseItemTypeReasoning && item.EncryptedContent == "enc_reason" {
			foundReasoning = true
			break
		}
	}
	if !foundReasoning {
		t.Fatalf("expected encrypted reasoning checkpoint in replacement history, got %+v", replacement)
	}
}

func TestRemoteCompactionMissingCheckpointFallsBackToLocal(t *testing.T) {
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
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "local summary"},
				Usage:     llm.Usage{InputTokens: 8000, OutputTokens: 1000, WindowTokens: 200000},
			},
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
				Usage:     llm.Usage{InputTokens: 2000, OutputTokens: 500, WindowTokens: 200000},
			},
		},
		compactionResponses: []llm.CompactionResponse{
			{
				OutputItems: []llm.ResponseItem{
					{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "run tools"},
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
	if len(client.calls) < 3 {
		t.Fatalf("expected first turn + local summary + post-compaction turn, got %d calls", len(client.calls))
	}

	foundLocalSummaryCarryover := false
	for _, req := range client.calls {
		for _, item := range req.Items {
			if item.Type == llm.ResponseItemTypeMessage && item.Role == llm.RoleUser && item.MessageType == llm.MessageTypeCompactionSummary {
				foundLocalSummaryCarryover = true
				break
			}
		}
		if foundLocalSummaryCarryover {
			break
		}
	}
	if !foundLocalSummaryCarryover {
		t.Fatalf("expected local summary carryover item in model requests, got %+v", client.calls)
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
			&llm.ProviderAPIError{ProviderID: "openai", StatusCode: 400, Code: llm.UnifiedErrorCodeContextLengthOverflow, ProviderCode: "context_length_exceeded", Message: "prompt exceeded"},
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

func TestAutoCompactionDoesNotRetryNonOverflow400(t *testing.T) {
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
		},
		compactionErrors: []error{
			&llm.APIStatusError{StatusCode: 400, Body: `{"error":{"type":"invalid_request_error","code":"invalid_tool_arguments","message":"tool arguments must be an object"}}`},
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

	if _, err := eng.SubmitUserMessage(context.Background(), "run tools"); err == nil {
		t.Fatal("expected compaction to fail on non-overflow 400")
	}
	if len(client.compactionCalls) != 1 {
		t.Fatalf("expected one compact call for non-overflow 400, got %d", len(client.compactionCalls))
	}
}

func TestAutoCompactionRetries413ByTrimmingOldestEligibleItems(t *testing.T) {
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
			&llm.ProviderAPIError{ProviderID: "openai", StatusCode: 413, Code: llm.UnifiedErrorCodeContextLengthOverflow, ProviderCode: "context_length_exceeded", Message: "payload too large"},
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
		t.Fatalf("expected two compact calls (retry after 413), got %d", len(client.compactionCalls))
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
			ProviderID:                    "openai",
			SupportsResponsesAPI:          true,
			SupportsResponsesCompact:      true,
			SupportsReasoningEncrypted:    true,
			SupportsServerSideContextEdit: true,
			IsOpenAIFirstParty:            true,
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
			if item.Type == llm.ResponseItemTypeMessage && item.Role == llm.RoleUser && item.MessageType == llm.MessageTypeCompactionSummary {
				t.Fatalf("did not expect local compaction summary fallback, request=%+v", req.Items)
			}
		}
	}
}
