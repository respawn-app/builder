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

	"builder/prompts"
	"builder/server/llm"
	"builder/server/session"
	"builder/server/tools"
	shelltool "builder/server/tools/shell"
	triggerhandofftool "builder/server/tools/triggerhandoff"
	"builder/shared/transcript"
	"builder/shared/transcript/toolcodec"
)

type fakeClient struct {
	mu        sync.Mutex
	responses []llm.Response
	calls     []llm.Request
	caps      llm.ProviderCapabilities
	capsErr   error
}

type hookClient struct {
	mu           sync.Mutex
	response     llm.Response
	calls        []llm.Request
	caps         llm.ProviderCapabilities
	beforeReturn func() error
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

func (c *hookClient) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
	c.mu.Lock()
	c.calls = append(c.calls, req)
	beforeReturn := c.beforeReturn
	response := c.response
	c.mu.Unlock()
	if beforeReturn != nil {
		if err := beforeReturn(); err != nil {
			return llm.Response{}, err
		}
	}
	return response, nil
}

func (c *hookClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(c.caps.ProviderID) != "" {
		return c.caps, nil
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

func TestLastCommittedAssistantFinalAnswerSkipsTrailingReminderEntries(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleAssistant, Phase: llm.MessagePhaseFinal, Content: "final handoff"}); err != nil {
		t.Fatalf("append assistant final: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeCompactionSoonReminder, Content: "heads up"}); err != nil {
		t.Fatalf("append reminder: %v", err)
	}

	if got := eng.LastCommittedAssistantFinalAnswer(); got != "final handoff" {
		t.Fatalf("LastCommittedAssistantFinalAnswer() = %q, want %q", got, "final handoff")
	}
}

func TestLastCommittedAssistantFinalAnswerSkipsTrailingErrorFeedback(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleAssistant, Phase: llm.MessagePhaseFinal, Content: "final handoff"}); err != nil {
		t.Fatalf("append assistant final: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: "phase mismatch"}); err != nil {
		t.Fatalf("append warning: %v", err)
	}

	if got := eng.LastCommittedAssistantFinalAnswer(); got != "final handoff" {
		t.Fatalf("LastCommittedAssistantFinalAnswer() = %q, want %q", got, "final handoff")
	}
}

func TestLastCommittedAssistantFinalAnswerSkipsTrailingHandoffFutureMessage(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleAssistant, Phase: llm.MessagePhaseFinal, Content: "final handoff"}); err != nil {
		t.Fatalf("append assistant final: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHandoffFutureMessage, Content: "resume with tests"}); err != nil {
		t.Fatalf("append handoff future message: %v", err)
	}

	if got := eng.LastCommittedAssistantFinalAnswer(); got != "final handoff" {
		t.Fatalf("LastCommittedAssistantFinalAnswer() = %q, want %q", got, "final handoff")
	}
}

func TestLastCommittedAssistantFinalAnswerDoesNotSkipTrailingUntypedDeveloperMessage(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleAssistant, Phase: llm.MessagePhaseFinal, Content: "final handoff"}); err != nil {
		t.Fatalf("append assistant final: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, Content: "User ran shell command directly:\npwd"}); err != nil {
		t.Fatalf("append developer message: %v", err)
	}

	if got := eng.LastCommittedAssistantFinalAnswer(); got != "" {
		t.Fatalf("LastCommittedAssistantFinalAnswer() = %q, want empty", got)
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

func TestSubmitUserMessageMissingPhaseLegacyClientEmitsAssistantEventOnce(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{
			Role:    llm.RoleAssistant,
			Content: "done",
		},
		Usage: llm.Usage{WindowTokens: 200000},
	}}}
	client.caps = llm.ProviderCapabilities{ProviderID: "anthropic", SupportsResponsesAPI: false, IsOpenAIFirstParty: false}

	var (
		mu     sync.Mutex
		events []Event
	)
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, evt)
		},
	})
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

	mu.Lock()
	defer mu.Unlock()
	assistantEvents := 0
	for _, evt := range events {
		if evt.Kind == EventAssistantMessage && evt.Message.Content == "done" {
			assistantEvents++
		}
	}
	if assistantEvents != 1 {
		t.Fatalf("expected one assistant_message event for missing-phase terminal reply, got %d events=%+v", assistantEvents, events)
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

func TestSubmitUserMessageCommentaryWithoutToolsEmitsRealtimeAssistantEvent(t *testing.T) {
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
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "done",
				Phase:   llm.MessagePhaseFinal,
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
	}}

	var (
		mu     sync.Mutex
		events []Event
	)
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, evt)
		},
	})
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

	mu.Lock()
	defer mu.Unlock()
	assistantContents := make([]string, 0, 2)
	for _, evt := range events {
		if evt.Kind != EventAssistantMessage {
			continue
		}
		assistantContents = append(assistantContents, evt.Message.Content)
	}
	if len(assistantContents) != 2 || assistantContents[0] != "progress update" || assistantContents[1] != "done" {
		t.Fatalf("assistant realtime events = %+v, want [progress update done]", assistantContents)
	}
}

func TestSubmitUserMessageCommentaryWithToolCallsEmitsRealtimeAssistantEventWithoutDuplicateToolCalls(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "working",
				Phase:   llm.MessagePhaseCommentary,
			},
			ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)}},
			Usage:     llm.Usage{WindowTokens: 200000},
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

	var (
		mu     sync.Mutex
		events []Event
	)
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, evt)
		},
	})
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

	mu.Lock()
	defer mu.Unlock()
	assistantContents := make([]string, 0, 2)
	commentaryToolCalls := -1
	for _, evt := range events {
		if evt.Kind != EventAssistantMessage {
			continue
		}
		assistantContents = append(assistantContents, evt.Message.Content)
		if evt.Message.Content == "working" {
			commentaryToolCalls = len(evt.Message.ToolCalls)
		}
	}
	if len(assistantContents) != 2 || assistantContents[0] != "working" || assistantContents[1] != "done" {
		t.Fatalf("assistant realtime events = %+v, want [working done]", assistantContents)
	}
	if commentaryToolCalls != 0 {
		t.Fatalf("expected commentary assistant event to omit tool calls, got %d", commentaryToolCalls)
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
	if reviewerReq.SessionID != reviewerSessionID(store.Meta().SessionID) {
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
	foundToolCallEntry := false
	foundToolResultEntry := false
	for _, message := range requestMessages(reviewerReq)[boundaryIdx+1:] {
		if message.Role != llm.RoleUser {
			t.Fatalf("expected reviewer transcript entries after metadata to be user role messages, got %q", message.Role)
		}
		if strings.Contains(message.Content, "Agent:") {
			foundAgentLabel = true
		}
		if strings.Contains(message.Content, "Tool call:") && strings.Contains(message.Content, "pwd") {
			foundToolCallEntry = true
		}
		if strings.Contains(message.Content, "Tool result:") && strings.Contains(message.Content, "{\"tool\":\"shell\"}") {
			foundToolResultEntry = true
		}
	}
	if !foundAgentLabel {
		t.Fatalf("expected reviewer request to include agent labels, messages=%+v", requestMessages(reviewerReq))
	}
	if !foundToolCallEntry {
		t.Fatalf("expected reviewer request to include tool call transcript entries, messages=%+v", requestMessages(reviewerReq))
	}
	if !foundToolResultEntry {
		t.Fatalf("expected reviewer request to include tool result transcript entries, messages=%+v", requestMessages(reviewerReq))
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

	var (
		eventsMu sync.Mutex
		events   []Event
	)

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
		OnEvent: func(evt Event) {
			eventsMu.Lock()
			defer eventsMu.Unlock()
			events = append(events, evt)
		},
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

	eventsMu.Lock()
	deferredEvents := append([]Event(nil), events...)
	eventsMu.Unlock()
	assistantEventIdx := -1
	reviewerEventIdx := -1
	for idx, evt := range deferredEvents {
		if evt.Kind == EventAssistantMessage && evt.Message.Content == "updated final after review" {
			assistantEventIdx = idx
		}
		if evt.Kind == EventReviewerCompleted && evt.Reviewer != nil && evt.Reviewer.Outcome == "applied" {
			reviewerEventIdx = idx
			if got, want := evt.CommittedEntryCount, len(snapshot.Entries); got != want {
				t.Fatalf("reviewer completed committed count = %d, want %d", got, want)
			}
		}
	}
	if assistantEventIdx < 0 {
		t.Fatalf("expected follow-up assistant event, got %+v", deferredEvents)
	}
	if reviewerEventIdx < 0 {
		t.Fatalf("expected reviewer completed event, got %+v", deferredEvents)
	}
	if assistantEventIdx > reviewerEventIdx {
		t.Fatalf("expected follow-up assistant event before reviewer completion event, got %+v", deferredEvents)
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

func TestReviewerCompletedEventReflectsPersistedReviewerStatusState(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{
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

	var (
		eventsMu                   sync.Mutex
		reviewerCompletedEvent     *Event
		snapshotAtReviewerComplete ChatSnapshot
		eng                        *Engine
	)
	eng, err = New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			if evt.Kind != EventReviewerCompleted || evt.Reviewer == nil || evt.Reviewer.Outcome != "applied" {
				return
			}
			eventsMu.Lock()
			defer eventsMu.Unlock()
			captured := evt
			reviewerCompletedEvent = &captured
			snapshotAtReviewerComplete = eng.ChatSnapshot()
		},
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

	eventsMu.Lock()
	completed := reviewerCompletedEvent
	snapshotAtCompletion := snapshotAtReviewerComplete
	eventsMu.Unlock()
	if completed == nil {
		t.Fatal("expected reviewer completed event")
	}
	if got, want := completed.CommittedEntryCount, len(snapshotAtCompletion.Entries); got != want {
		t.Fatalf("reviewer completed committed count = %d, want %d", got, want)
	}
	if len(snapshotAtCompletion.Entries) < 2 {
		t.Fatalf("expected follow-up assistant and reviewer status in completion snapshot, got %+v", snapshotAtCompletion.Entries)
	}
	assistantEntry := snapshotAtCompletion.Entries[len(snapshotAtCompletion.Entries)-2]
	if assistantEntry.Role != "assistant" || assistantEntry.Text != "updated final after review" {
		t.Fatalf("expected completion snapshot penultimate entry to be follow-up assistant, got %+v", assistantEntry)
	}
	statusEntry := snapshotAtCompletion.Entries[len(snapshotAtCompletion.Entries)-1]
	if statusEntry.Role != "reviewer_status" || statusEntry.Text != "Supervisor ran: 1 suggestion, applied." {
		t.Fatalf("expected completion snapshot to end with reviewer status, got %+v", statusEntry)
	}

	eng.AppendLocalEntry("warning", "later unrelated note")
	finalSnapshot := eng.ChatSnapshot()
	if got, want := len(finalSnapshot.Entries), len(snapshotAtCompletion.Entries)+1; got != want {
		t.Fatalf("expected later note after reviewer completion snapshot, got %d entries want %d", got, want)
	}
	if finalSnapshot.Entries[len(finalSnapshot.Entries)-1].Text != "later unrelated note" {
		t.Fatalf("expected later unrelated note at transcript tail, got %+v", finalSnapshot.Entries[len(finalSnapshot.Entries)-1])
	}
}

func TestRunReviewerFollowUpReturnsCompletionWhenReviewerInstructionAppendFails(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:    "gpt-5",
		Reviewer: ReviewerConfig{Model: "gpt-5"},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendUserMessage("prep-1", "first request"); err != nil {
		t.Fatalf("append first message: %v", err)
	}

	reviewerClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":["Add final verification notes."]}`},
		Usage:     llm.Usage{InputTokens: 10},
	}}}

	eventsPath := filepath.Join(store.Dir(), "events.jsonl")
	info, err := os.Stat(eventsPath)
	if err != nil {
		t.Fatalf("stat events log: %v", err)
	}
	if err := os.Chmod(eventsPath, 0o400); err != nil {
		t.Fatalf("chmod events log readonly: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(eventsPath, info.Mode()) })

	result, err := eng.runReviewerFollowUp(context.Background(), "step-1", llm.Message{Role: llm.RoleAssistant, Phase: llm.MessagePhaseFinal, Content: "original final"}, reviewerClient)
	if err != nil {
		t.Fatalf("run reviewer follow-up: %v", err)
	}
	if result.Message.Content != "original final" {
		t.Fatalf("follow-up result message = %q, want original final", result.Message.Content)
	}
	if result.Completion == nil {
		t.Fatal("expected reviewer completion after follow-up append failure")
	}
	if result.Completion.Outcome != "followup_failed" {
		t.Fatalf("reviewer completion outcome = %q, want followup_failed", result.Completion.Outcome)
	}
	if result.Completion.SuggestionsCount != 1 {
		t.Fatalf("reviewer completion suggestions = %d, want 1", result.Completion.SuggestionsCount)
	}
	if strings.TrimSpace(result.Completion.Error) == "" {
		t.Fatal("expected reviewer completion to include append failure error")
	}
}

func TestRunStepLoopEmitsReviewerCompletionWhenReviewerInstructionAppendFails(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eventsPath := filepath.Join(store.Dir(), "events.jsonl")
	info, err := os.Stat(eventsPath)
	if err != nil {
		t.Fatalf("stat events log: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(eventsPath, info.Mode()) })

	mainClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "original final", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	reviewerClient := &hookClient{
		caps: llm.ProviderCapabilities{ProviderID: "openai-compatible", SupportsResponsesAPI: true},
		response: llm.Response{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":["Add final verification notes."]}`},
			Usage:     llm.Usage{InputTokens: 10, WindowTokens: 200000},
		},
		beforeReturn: func() error {
			return os.Chmod(eventsPath, 0o400)
		},
	}

	var (
		eventsMu sync.Mutex
		events   []Event
	)
	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                 "gpt-5",
		AutoCompactTokenLimit: 1_000_000,
		OnEvent: func(evt Event) {
			eventsMu.Lock()
			defer eventsMu.Unlock()
			events = append(events, evt)
		},
		Reviewer: ReviewerConfig{
			Frequency: "all",
			Model:     "gpt-5",
			Client:    reviewerClient,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "do task"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	msg, err := eng.runStepLoop(context.Background(), "step-1")
	if err != nil {
		t.Fatalf("runStepLoop: %v", err)
	}
	if msg.Content != "original final" {
		t.Fatalf("assistant content = %q, want original final", msg.Content)
	}

	eventsMu.Lock()
	deferredEvents := append([]Event(nil), events...)
	eventsMu.Unlock()
	assistantEventIdx := -1
	reviewerEventIdx := -1
	for idx, evt := range deferredEvents {
		if evt.Kind == EventAssistantMessage && evt.Message.Content == "original final" {
			assistantEventIdx = idx
		}
		if evt.Kind == EventReviewerCompleted && evt.Reviewer != nil {
			reviewerEventIdx = idx
			if evt.Reviewer.Outcome != "followup_failed" {
				t.Fatalf("reviewer completion outcome = %q, want followup_failed", evt.Reviewer.Outcome)
			}
			if evt.Reviewer.SuggestionsCount != 1 {
				t.Fatalf("reviewer completion suggestions = %d, want 1", evt.Reviewer.SuggestionsCount)
			}
			if strings.TrimSpace(evt.Reviewer.Error) == "" {
				t.Fatal("expected reviewer completion error details")
			}
		}
	}
	if assistantEventIdx < 0 {
		t.Fatalf("expected assistant message event, got %+v", deferredEvents)
	}
	if reviewerEventIdx < 0 {
		t.Fatalf("expected reviewer completed event, got %+v", deferredEvents)
	}
	if assistantEventIdx > reviewerEventIdx {
		t.Fatalf("expected assistant event before reviewer completion, got %+v", deferredEvents)
	}

	snapshot := eng.ChatSnapshot()
	if len(snapshot.Entries) < 3 {
		t.Fatalf("expected in-memory transcript entries including reviewer status, got %+v", snapshot.Entries)
	}
	statusEntry := snapshot.Entries[len(snapshot.Entries)-1]
	if statusEntry.Role != "reviewer_status" || !strings.Contains(statusEntry.Text, "follow-up failed") {
		t.Fatalf("expected in-memory reviewer status after append failure, got %+v", statusEntry)
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

func TestRestoreMessagesKeepsStoredToolCallPresentationPayload(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	presentation := toolcodec.EncodeToolCallMeta(transcript.ToolCallMeta{
		ToolName:       string(tools.ToolShell),
		Presentation:   transcript.ToolPresentationShell,
		RenderBehavior: transcript.ToolCallRenderBehaviorShell,
		IsShell:        true,
		Command:        "pwd",
		TimeoutLabel:   "timeout: 5m",
	})
	if _, err := store.AppendEvent("legacy-step", "message", llm.Message{
		Role:    llm.RoleAssistant,
		Content: "working",
		ToolCalls: []llm.ToolCall{{
			ID:           "call_1",
			Name:         string(tools.ToolShell),
			Input:        json.RawMessage(`{"command":"pwd"}`),
			Presentation: presentation,
		}},
	}); err != nil {
		t.Fatalf("append assistant tool call message: %v", err)
	}

	restored, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	snapshot := restored.ChatSnapshot()
	if len(snapshot.Entries) != 2 {
		t.Fatalf("expected assistant and tool call entries, got %+v", snapshot.Entries)
	}
	toolEntry := snapshot.Entries[1]
	if toolEntry.Role != "tool_call" {
		t.Fatalf("expected tool_call entry, got %+v", toolEntry)
	}
	if toolEntry.ToolCall == nil || !toolEntry.ToolCall.IsShell {
		t.Fatalf("expected restored shell tool metadata, got %+v", toolEntry.ToolCall)
	}
	if toolEntry.ToolCall.Command != "pwd" {
		t.Fatalf("expected restored shell command, got %+v", toolEntry.ToolCall)
	}
	if toolEntry.ToolCall.TimeoutLabel != "timeout: 5m" {
		t.Fatalf("expected restored timeout label, got %+v", toolEntry.ToolCall)
	}
}

func TestRestoreMessagesReplaysLegacyReviewerRollbackHistoryReplacement(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	presentation := toolcodec.EncodeToolCallMeta(transcript.ToolCallMeta{
		ToolName:       string(tools.ToolShell),
		Presentation:   transcript.ToolPresentationShell,
		RenderBehavior: transcript.ToolCallRenderBehaviorShell,
		IsShell:        true,
		Command:        "pwd",
	})
	legacyItems := []llm.ResponseItem{
		{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "before"},
		{
			Type:             llm.ResponseItemTypeFunctionCall,
			CallID:           "call_1",
			Name:             string(tools.ToolShell),
			ToolPresentation: presentation,
			Arguments:        json.RawMessage(`{"command":"pwd"}`),
		},
	}
	if _, err := store.AppendEvent("legacy-step", "history_replaced", historyReplacementPayload{
		Engine: "reviewer_rollback",
		Mode:   "manual",
		Items:  legacyItems,
	}); err != nil {
		t.Fatalf("append history replacement: %v", err)
	}

	type restoreResult struct {
		engine *Engine
		err    error
	}
	resultCh := make(chan restoreResult, 1)
	go func() {
		restored, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
		resultCh <- restoreResult{engine: restored, err: err}
	}()
	var restored *Engine
	select {
	case result := <-resultCh:
		restored = result.engine
		if result.err != nil {
			t.Fatalf("restore engine: %v", result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("restore engine timed out; possible store-lock deadlock while replaying reviewer_rollback history replacement")
	}
	items := restored.snapshotItems()
	if len(items) != len(legacyItems) {
		t.Fatalf("expected %d restored items, got %+v", len(legacyItems), items)
	}
	if items[0].Role != llm.RoleUser || items[0].Content != "before" {
		t.Fatalf("unexpected restored first item: %+v", items[0])
	}
	if items[1].Type != llm.ResponseItemTypeFunctionCall || items[1].CallID != "call_1" {
		t.Fatalf("unexpected restored function call item: %+v", items[1])
	}
	if string(items[1].ToolPresentation) != string(presentation) {
		t.Fatalf("expected stored tool presentation preserved, got %+v", items[1])
	}
	snapshot := restored.ChatSnapshot()
	if len(snapshot.Entries) != 2 {
		t.Fatalf("expected restored user and tool call entries, got %+v", snapshot.Entries)
	}
	if snapshot.Entries[1].Role != "tool_call" || snapshot.Entries[1].ToolCall == nil || snapshot.Entries[1].ToolCall.Command != "pwd" {
		t.Fatalf("expected restored tool call transcript entry, got %+v", snapshot.Entries[1])
	}
}

func TestRestoreMessagesFailsOnMalformedHistoryReplacementPayload(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendReplayEvents([]session.ReplayEvent{{
		StepID:  "legacy-step",
		Kind:    "history_replaced",
		Payload: json.RawMessage(`{"engine":"reviewer_rollback","items":"not-an-array"}`),
	}}); err != nil {
		t.Fatalf("append malformed replay event: %v", err)
	}

	if _, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"}); err == nil || !strings.Contains(err.Error(), "decode history_replaced event") {
		t.Fatalf("expected malformed history replacement decode error, got %v", err)
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
	if len(reviewerMessages) != 6 {
		t.Fatalf("expected 6 reviewer transcript messages after filtering, got %d", len(reviewerMessages))
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
	if !strings.Contains(reviewerMessages[3].Content, "Tool call:") || !strings.Contains(reviewerMessages[3].Content, "pwd") {
		t.Fatalf("expected separate tool call transcript entry, message=%q", reviewerMessages[3].Content)
	}
	if strings.Contains(reviewerMessages[3].Content, "(id=") {
		t.Fatalf("did not expect tool call id in reviewer transcript, message=%q", reviewerMessages[3].Content)
	}
	if !strings.Contains(reviewerMessages[4].Content, "Agent:") {
		t.Fatalf("expected assistant final answer entry to use agent label, message=%q", reviewerMessages[4].Content)
	}
	if !strings.Contains(reviewerMessages[5].Content, "Tool result:") || !strings.Contains(reviewerMessages[5].Content, "ok") {
		t.Fatalf("expected separate tool result transcript entry, message=%q", reviewerMessages[5].Content)
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
	if !strings.Contains(reviewerMessages[0].Content, "Tool result:") || !strings.Contains(reviewerMessages[0].Content, "orphan") {
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
	assistantContents := make([]string, 0, 2)
	for _, evt := range events {
		if evt.Kind != EventAssistantMessage {
			continue
		}
		assistantContents = append(assistantContents, evt.Message.Content)
	}
	if len(assistantContents) != 2 || assistantContents[0] != "working" || assistantContents[1] != "foreground done" {
		t.Fatalf("assistant message contents = %+v, want [working foreground done] events=%+v", assistantContents, events)
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
	for i, evt := range events {
		_ = i
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
	assistantContents := make([]string, 0, 1)
	for _, evt := range events {
		if evt.Kind == EventAssistantMessage {
			assistantContents = append(assistantContents, evt.Message.Content)
		}
	}
	if len(assistantContents) != 1 || assistantContents[0] != "working" {
		t.Fatalf("assistant message contents = %+v, want [working] events=%+v", assistantContents, events)
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
	if err := os.Chmod(sessionDir, 0o755); err != nil {
		t.Fatalf("restore session dir permissions: %v", err)
	}

	reopened, openErr := session.Open(sessionDir)
	if openErr != nil {
		t.Fatalf("re-open session store: %v", openErr)
	}
	if !reopened.Meta().InFlightStep {
		t.Fatalf("expected persisted in-flight flag to remain true after clear failure")
	}
	runs, err := reopened.ReadRuns()
	if err != nil {
		t.Fatalf("read durable runs after reopen: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected durable run lifecycle to persist despite clear failure, got %+v", runs)
	}
	if runs[0].Status != session.RunStatusCompleted || runs[0].FinishedAt.IsZero() {
		t.Fatalf("expected terminal durable run after clear failure, got %+v", runs[0])
	}
}

func TestNewNormalizesPersistedInFlightStepOnReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("legacy-step", "message", llm.Message{Role: llm.RoleUser, Content: "hello"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if err := store.MarkInFlight(true); err != nil {
		t.Fatalf("mark in-flight true: %v", err)
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	restored, err := New(reopenedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	if reopenedStore.Meta().InFlightStep {
		t.Fatal("expected reopen path to clear persisted in-flight flag")
	}
	messages := restored.snapshotMessages()
	if len(messages) != 2 {
		t.Fatalf("expected original user message plus interruption marker, got %+v", messages)
	}
	last := messages[len(messages)-1]
	if last.Role != llm.RoleDeveloper || last.MessageType != llm.MessageTypeInterruption || last.Content != interruptMessage {
		t.Fatalf("expected interruption developer message, got %+v", last)
	}
	events, err := reopenedStore.ReadEvents()
	if err != nil {
		t.Fatalf("read reopened events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected persisted interruption event appended on reopen, got %+v", events)
	}
}

func TestReopenCarriesInterruptedAskQuestionToolAttemptIntoNextModelRequest(t *testing.T) {
	testReopenCarriesInterruptedToolAttemptIntoNextModelRequest(t, llm.ToolCall{
		ID:    "call_ask",
		Name:  string(tools.ToolAskQuestion),
		Input: json.RawMessage(`{"question":"Choose scope?","suggestions":["full","fast"],"recommended_option_index":1}`),
		Presentation: toolcodec.EncodeToolCallMeta(transcript.ToolCallMeta{
			ToolName:               string(tools.ToolAskQuestion),
			Presentation:           transcript.ToolPresentationAskQuestion,
			RenderBehavior:         transcript.ToolCallRenderBehaviorAskQuestion,
			Question:               "Choose scope?",
			Suggestions:            []string{"full", "fast"},
			RecommendedOptionIndex: 1,
			Command:                "Choose scope?",
		}),
	})
}

func TestReopenCarriesInterruptedShellToolAttemptIntoNextModelRequest(t *testing.T) {
	testReopenCarriesInterruptedToolAttemptIntoNextModelRequest(t, llm.ToolCall{
		ID:    "call_shell",
		Name:  string(tools.ToolShell),
		Input: json.RawMessage(`{"command":"pwd"}`),
		Presentation: toolcodec.EncodeToolCallMeta(transcript.ToolCallMeta{
			ToolName:       string(tools.ToolShell),
			Presentation:   transcript.ToolPresentationShell,
			RenderBehavior: transcript.ToolCallRenderBehaviorShell,
			IsShell:        true,
			Command:        "pwd",
			TimeoutLabel:   "timeout: 5m",
		}),
	})
}

func TestReopenCarriesInterruptedApprovalBackedPatchToolAttemptIntoNextModelRequest(t *testing.T) {
	testReopenCarriesInterruptedToolAttemptIntoNextModelRequest(t, llm.ToolCall{
		ID:    "call_patch",
		Name:  string(tools.ToolPatch),
		Input: json.RawMessage(`{"patch":"*** Begin Patch\n*** Add File: ../outside.txt\n+hello\n*** End Patch\n"}`),
	})
}

func testReopenCarriesInterruptedToolAttemptIntoNextModelRequest(t *testing.T, call llm.ToolCall) {
	t.Helper()

	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("legacy-step", "message", llm.Message{Role: llm.RoleUser, Content: "do the thing"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("legacy-step", "message", llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}); err != nil {
		t.Fatalf("append assistant tool call message: %v", err)
	}
	if err := store.MarkInFlight(true); err != nil {
		t.Fatalf("mark in-flight true: %v", err)
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "decided anew", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	restored, err := New(reopenedStore, client, tools.NewRegistry(), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	if reopenedStore.Meta().InFlightStep {
		t.Fatal("expected reopen path to clear persisted in-flight flag")
	}

	msg, err := restored.SubmitUserMessage(context.Background(), "continue")
	if err != nil {
		t.Fatalf("submit after reopen: %v", err)
	}
	if msg.Content != "decided anew" {
		t.Fatalf("assistant content = %q, want decided anew", msg.Content)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected one resumed model call, got %d", len(client.calls))
	}

	var (
		foundPriorAttempt    bool
		foundUnexpectedReply bool
	)
	for _, item := range client.calls[0].Items {
		switch {
		case item.Type == llm.ResponseItemTypeFunctionCall && item.CallID == call.ID && item.Name == call.Name:
			foundPriorAttempt = true
		case item.Type == llm.ResponseItemTypeFunctionCallOutput && item.CallID == call.ID:
			foundUnexpectedReply = true
		}
	}
	if !foundPriorAttempt {
		t.Fatalf("expected resumed request to include prior interrupted tool call attempt, items=%+v", client.calls[0].Items)
	}
	if foundUnexpectedReply {
		t.Fatalf("did not expect resumed request to fabricate completed tool output for interrupted call, items=%+v", client.calls[0].Items)
	}

	seenInterruption := false
	for _, reqMsg := range requestMessages(client.calls[0]) {
		if reqMsg.Role == llm.RoleDeveloper && reqMsg.MessageType == llm.MessageTypeInterruption && reqMsg.Content == interruptMessage {
			seenInterruption = true
			break
		}
	}
	if !seenInterruption {
		t.Fatalf("expected resumed request to include interruption marker, messages=%+v", requestMessages(client.calls[0]))
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
		"\nYour model: gpt-5\n",
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
	if !strings.Contains(requestMessages(req)[0].Content, "\nYour model: gpt-5\n") {
		t.Fatalf("expected environment injection to include labeled model identifier, got %+v", requestMessages(req)[0])
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
		if entry.Role != string(transcript.EntryRoleDeveloperFeedback) {
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

func TestEnvironmentContextMessageIncludesLabeledModelIdentifier(t *testing.T) {
	workspace := t.TempDir()
	msg, err := environmentContextMessage(workspace, "gpt-5.3-codex", time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("environmentContextMessage: %v", err)
	}
	if !strings.Contains(msg, "\nYour model: gpt-5.3-codex\n") {
		t.Fatalf("expected environment message to include labeled model identifier, got %q", msg)
	}
	if strings.Contains(msg, "Your model: gpt-5.3-codex high") {
		t.Fatalf("expected environment message to exclude thinking level from model identifier, got %q", msg)
	}
}

func TestEnvironmentContextMessageRejectsEmptyModel(t *testing.T) {
	workspace := t.TempDir()
	if _, err := environmentContextMessage(workspace, "", time.Unix(0, 0).UTC()); err == nil {
		t.Fatal("expected environmentContextMessage to reject empty model")
	} else if !strings.Contains(err.Error(), "requires a model") {
		t.Fatalf("expected empty-model error, got %v", err)
	}
}

func TestNewRejectsEmptyModel(t *testing.T) {
	storeRoot := t.TempDir()
	workspace := t.TempDir()
	store, err := session.Create(storeRoot, "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	_, err = New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{})
	if err == nil {
		t.Fatal("expected New to reject empty model")
	}
	if !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("expected model-required error, got %v", err)
	}
}

func TestSubmitInjectsEnvironmentLineWithLabeledModelIdentifier(t *testing.T) {
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
	if !strings.Contains(envMsg.Content, "\nYour model: gpt-5.3-codex\n") {
		t.Fatalf("expected environment context to contain labeled model identifier, got %q", envMsg.Content)
	}
	if strings.Contains(envMsg.Content, "Your model: gpt-5.3-codex high") {
		t.Fatalf("expected environment context to exclude thinking level from model identifier, got %q", envMsg.Content)
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

func TestDirectUserMessageFlushedEventPrecedesConversationUpdate(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}

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
			if evt.Kind == EventUserMessageFlushed && evt.UserMessage == "say hi" && flushIndex < 0 {
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
			if last.Role == string(llm.RoleUser) && last.Text == "say hi" {
				userConversationIndex = eventIndex
			}
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "say hi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if flushIndex < 0 {
		t.Fatal("expected direct user_message_flushed event")
	}
	if userConversationIndex < 0 {
		t.Fatal("expected conversation_updated event for direct user message")
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

func TestPreSubmitCompactionTokenLimitUsesFixedRunwayReserve(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		runway   int
		expected int
	}{
		{
			name:     "subtracts fixed runway from auto threshold",
			limit:    190_000,
			runway:   35_000,
			expected: 155_000,
		},
		{
			name:     "large windows still use same fixed runway",
			limit:    950_000,
			runway:   35_000,
			expected: 915_000,
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
				ContextWindowTokens:           1_000_000,
				PreSubmitCompactionLeadTokens: tt.runway,
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

func TestCompactionSoonReminderStaysSingleShotAfterReEnablingAutoCompactionAboveReminderBand(t *testing.T) {
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
		if entry.Role == "warning" && entry.Text == prompts.RenderCompactionSoonReminderPrompt(false) {
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
		if entry.Role == "warning" && entry.Text == prompts.RenderCompactionSoonReminderPrompt(false) {
			reminders++
		}
	}
	if reminders != 1 {
		t.Fatalf("expected reminder to remain single-shot after falling below threshold, got %d entries=%+v", reminders, snap.Entries)
	}
}

func TestReopenedSessionRestoresCompactionSoonReminderIssuedState(t *testing.T) {
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
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeCompactionSoonReminder, Content: prompts.RenderCompactionSoonReminderPrompt(false)}); err != nil {
		t.Fatalf("append reminder: %v", err)
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	restored, err := New(reopenedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   2_000,
		AutoCompactTokenLimit: 1_000,
		CompactionMode:        "local",
	})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	restored.setLastUsage(llm.Usage{InputTokens: 890, WindowTokens: 2_000})
	if !restored.compactionSoonReminderIssued {
		t.Fatal("expected reopened session to restore reminder-issued state")
	}
	if !reopenedStore.Meta().CompactionSoonReminderIssued {
		t.Fatal("expected reopened session meta to persist reminder-issued state")
	}
	if err := restored.maybeAppendCompactionSoonReminder(context.Background(), "step-restore"); err != nil {
		t.Fatalf("reminder after reopen: %v", err)
	}
	if reminders := countCompactionSoonReminderWarnings(restored.ChatSnapshot()); reminders != 1 {
		t.Fatalf("expected reopened session to avoid duplicate reminder, got %d entries=%+v", reminders, restored.ChatSnapshot().Entries)
	}
}

func TestForkedSessionBeforeReminderDoesNotCopyReminderIssuedState(t *testing.T) {
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
	if err := eng.persistCompactionSoonReminderIssued(true); err != nil {
		t.Fatalf("persist reminder-issued state: %v", err)
	}

	forkedStore, err := session.ForkAtUserMessage(store, 1, "Parent -> edit")
	if err != nil {
		t.Fatalf("fork session: %v", err)
	}
	if forkedStore.Meta().CompactionSoonReminderIssued {
		t.Fatal("expected fork before reminder to clear reminder-issued state")
	}
	forked, err := New(forkedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   2_000,
		AutoCompactTokenLimit: 1_000,
		CompactionMode:        "local",
	})
	if err != nil {
		t.Fatalf("restore forked engine: %v", err)
	}
	forked.setLastUsage(llm.Usage{InputTokens: 890, WindowTokens: 2_000})
	if forked.compactionSoonReminderIssued {
		t.Fatal("expected forked session before reminder to start with cleared reminder-issued state")
	}
	if err := forked.maybeAppendCompactionSoonReminder(context.Background(), "step-fork"); err != nil {
		t.Fatalf("reminder after fork: %v", err)
	}
	if reminders := countCompactionSoonReminderWarnings(forked.ChatSnapshot()); reminders != 1 {
		t.Fatalf("expected fork before reminder to allow a fresh reminder, got %d entries=%+v", reminders, forked.ChatSnapshot().Entries)
	}
}

func TestForkedSessionAfterReminderPreservesCompactionSoonReminderIssuedState(t *testing.T) {
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
	if err := eng.persistCompactionSoonReminderIssued(true); err != nil {
		t.Fatalf("persist reminder-issued state: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeCompactionSoonReminder, Content: "compact soon"}); err != nil {
		t.Fatalf("append reminder message: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "after reminder"}); err != nil {
		t.Fatalf("append second user message: %v", err)
	}

	forkedStore, err := session.ForkAtUserMessage(store, 2, "Parent -> edit")
	if err != nil {
		t.Fatalf("fork session: %v", err)
	}
	if !forkedStore.Meta().CompactionSoonReminderIssued {
		t.Fatal("expected fork after reminder to preserve reminder-issued state")
	}
}

func TestRealCompactionClearsPersistedCompactionSoonReminderStateAcrossReopenAndFork(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "condensed summary"},
		Usage:     llm.Usage{InputTokens: 200, WindowTokens: 2_000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
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
	if err := eng.maybeAppendCompactionSoonReminder(context.Background(), "step-warning"); err != nil {
		t.Fatalf("append reminder: %v", err)
	}
	if !store.Meta().CompactionSoonReminderIssued {
		t.Fatal("expected reminder-issued state persisted before compaction")
	}

	if err := eng.CompactContext(context.Background(), "compact now"); err != nil {
		t.Fatalf("compact context: %v", err)
	}
	if store.Meta().CompactionSoonReminderIssued {
		t.Fatal("expected real compaction to clear reminder-issued state in session meta")
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	restored, err := New(reopenedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   2_000,
		AutoCompactTokenLimit: 1_000,
		CompactionMode:        "local",
	})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	if restored.compactionSoonReminderIssued {
		t.Fatal("expected reopened compacted session to start with cleared reminder-issued state")
	}
	if reopenedStore.Meta().CompactionSoonReminderIssued {
		t.Fatal("expected reopened compacted session metadata to remain cleared")
	}

	forkedStore, err := session.ForkAtUserMessage(reopenedStore, 1, "Parent -> edit")
	if err != nil {
		t.Fatalf("fork compacted session: %v", err)
	}
	if forkedStore.Meta().CompactionSoonReminderIssued {
		t.Fatal("expected fork of compacted session to inherit cleared reminder-issued state")
	}
	forked, err := New(forkedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   2_000,
		AutoCompactTokenLimit: 1_000,
		CompactionMode:        "local",
	})
	if err != nil {
		t.Fatalf("restore forked engine: %v", err)
	}
	if forked.compactionSoonReminderIssued {
		t.Fatal("expected forked compacted session to start with cleared reminder-issued state")
	}
}

func TestCompactionSoonReminderSkipsPreciseCountingWhenSuppressed(t *testing.T) {
	tests := []struct {
		name           string
		compactionMode string
		disableAuto    bool
	}{
		{name: "auto compaction disabled", compactionMode: "local", disableAuto: true},
		{name: "compaction mode none", compactionMode: "none"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := session.Create(dir, "ws", dir)
			if err != nil {
				t.Fatalf("create store: %v", err)
			}

			client := &preciseCompactionClient{inputTokenCount: 890, contextWindow: 2_000}
			eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
				Model:                 "gpt-5",
				ContextWindowTokens:   2_000,
				AutoCompactTokenLimit: 1_000,
				CompactionMode:        tt.compactionMode,
			})
			if err != nil {
				t.Fatalf("new engine: %v", err)
			}
			if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
				t.Fatalf("append seed message: %v", err)
			}
			eng.setLastUsage(llm.Usage{InputTokens: 890, WindowTokens: 2_000})
			eng.mu.Lock()
			eng.compactionSoonReminderIssued = true
			eng.mu.Unlock()

			if tt.disableAuto {
				changed, enabled := eng.SetAutoCompactionEnabled(false)
				if !changed || enabled {
					t.Fatalf("expected auto compaction toggle off, changed=%v enabled=%v", changed, enabled)
				}
			}

			if err := eng.maybeAppendCompactionSoonReminder(context.Background(), "suppressed"); err != nil {
				t.Fatalf("suppressed reminder check: %v", err)
			}
			if client.countCalls != 0 {
				t.Fatalf("expected suppressed reminder path to skip precise token counting, got %d calls", client.countCalls)
			}
			if got := len(eng.ChatSnapshot().Entries); got != 1 {
				t.Fatalf("expected no reminder entry while suppressed, got %d entries", got)
			}
			eng.mu.Lock()
			issued := eng.compactionSoonReminderIssued
			eng.mu.Unlock()
			if !issued {
				t.Fatal("expected suppressed reminder path to preserve issued state")
			}
		})
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
		if entry.Role == "warning" && entry.Text == prompts.RenderCompactionSoonReminderPrompt(false) {
			t.Fatalf("did not expect reminder in transcript after immediate auto-compaction, entries=%+v", snap.Entries)
		}
	}
}

func TestRunStepLoopInjectsCompactionSoonReminderBeforeFinalAnswerRequest(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{
		responses: []llm.Response{{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{InputTokens: 890, WindowTokens: 2_000},
		}},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
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

	msg, err := eng.runStepLoop(context.Background(), "step-1")
	if err != nil {
		t.Fatalf("runStepLoop: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("unexpected assistant message: %+v", msg)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected exactly one model request, got %d", len(client.calls))
	}
	remindersInRequest := 0
	for _, reqMsg := range requestMessages(client.calls[0]) {
		if reqMsg.Role == llm.RoleDeveloper && reqMsg.MessageType == llm.MessageTypeCompactionSoonReminder {
			remindersInRequest++
		}
	}
	if remindersInRequest != 1 {
		t.Fatalf("expected exactly one reminder in the request that produced the final answer, got %d messages=%+v", remindersInRequest, requestMessages(client.calls[0]))
	}

	snap := eng.ChatSnapshot()
	assistantIdx := -1
	reminderIdx := -1
	reminders := 0
	for idx, entry := range snap.Entries {
		if entry.Role == "assistant" && entry.Text == "done" {
			assistantIdx = idx
		}
		if entry.Role == "warning" && entry.Text == prompts.RenderCompactionSoonReminderPrompt(false) {
			reminders++
			reminderIdx = idx
		}
	}
	if reminders != 1 {
		t.Fatalf("expected exactly one reminder entry, got %d entries=%+v", reminders, snap.Entries)
	}
	if assistantIdx < 0 || reminderIdx != assistantIdx-1 {
		t.Fatalf("expected reminder immediately before final assistant entry, assistantIdx=%d reminderIdx=%d entries=%+v", assistantIdx, reminderIdx, snap.Entries)
	}
}

func TestRunStepLoopAppendsCompactionSoonReminderImmediatelyAfterToolOutputBoundary(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{
		responses: []llm.Response{
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "checking", Phase: llm.MessagePhaseCommentary},
				ToolCalls: []llm.ToolCall{{ID: "call_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)}},
				Usage:     llm.Usage{InputTokens: 100, WindowTokens: 2_000},
			},
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
				Usage:     llm.Usage{InputTokens: 920, WindowTokens: 2_000},
			},
		},
		inputTokenCountFn: func(req llm.Request) int {
			hasToolResult := false
			for _, msg := range requestMessages(req) {
				if msg.Role == llm.RoleTool {
					hasToolResult = true
					break
				}
			}
			if hasToolResult {
				return 890
			}
			return 100
		},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
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

	msg, err := eng.runStepLoop(context.Background(), "step-1")
	if err != nil {
		t.Fatalf("runStepLoop: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("unexpected assistant message: %+v", msg)
	}
	if len(client.calls) != 2 {
		t.Fatalf("expected two model requests, got %d", len(client.calls))
	}
	remindersInSecondRequest := 0
	for _, reqMsg := range requestMessages(client.calls[1]) {
		if reqMsg.Role == llm.RoleDeveloper && reqMsg.MessageType == llm.MessageTypeCompactionSoonReminder {
			remindersInSecondRequest++
		}
	}
	if remindersInSecondRequest != 1 {
		t.Fatalf("expected exactly one reminder in second request, got %d messages=%+v", remindersInSecondRequest, requestMessages(client.calls[1]))
	}

	snap := eng.ChatSnapshot()
	toolIdx := -1
	reminderIdx := -1
	reminders := 0
	for idx, entry := range snap.Entries {
		if strings.HasPrefix(entry.Role, "tool_result") {
			toolIdx = idx
		}
		if entry.Role == "warning" && entry.Text == prompts.RenderCompactionSoonReminderPrompt(false) {
			reminders++
			reminderIdx = idx
		}
	}
	if reminders != 1 {
		t.Fatalf("expected exactly one reminder entry, got %d entries=%+v", reminders, snap.Entries)
	}
	if toolIdx < 0 || reminderIdx != toolIdx+1 {
		t.Fatalf("expected reminder immediately after tool output, toolIdx=%d reminderIdx=%d entries=%+v", toolIdx, reminderIdx, snap.Entries)
	}
}

func TestRunStepLoopDoesNotDuplicateCompactionSoonReminderAfterAutoCompactionIsDisabled(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{
		responses: []llm.Response{
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "checking", Phase: llm.MessagePhaseCommentary},
				ToolCalls: []llm.ToolCall{{ID: "call_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)}},
				Usage:     llm.Usage{InputTokens: 100, WindowTokens: 2_000},
			},
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
				Usage:     llm.Usage{InputTokens: 920, WindowTokens: 2_000},
			},
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "next", Phase: llm.MessagePhaseFinal},
				Usage:     llm.Usage{InputTokens: 930, WindowTokens: 2_000},
			},
		},
		inputTokenCountFn: func(req llm.Request) int {
			hasToolResult := false
			for _, msg := range requestMessages(req) {
				if msg.Role == llm.RoleTool {
					hasToolResult = true
					break
				}
			}
			if hasToolResult {
				return 890
			}
			return 930
		},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
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

	if _, err := eng.runStepLoop(context.Background(), "step-1"); err != nil {
		t.Fatalf("first runStepLoop: %v", err)
	}
	if reminders := countCompactionSoonReminderWarnings(eng.ChatSnapshot()); reminders != 1 {
		t.Fatalf("expected one reminder after first run, got %d entries=%+v", reminders, eng.ChatSnapshot().Entries)
	}

	changed, enabled := eng.SetAutoCompactionEnabled(false)
	if !changed || enabled {
		t.Fatalf("expected auto compaction toggle off, changed=%v enabled=%v", changed, enabled)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "continue"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	msg, err := eng.runStepLoop(context.Background(), "step-2")
	if err != nil {
		t.Fatalf("second runStepLoop: %v", err)
	}
	if msg.Content != "next" {
		t.Fatalf("unexpected second assistant message: %+v", msg)
	}
	if len(client.calls) != 3 {
		t.Fatalf("expected three model requests across both runs, got %d", len(client.calls))
	}

	remindersInThirdRequest := 0
	for _, reqMsg := range requestMessages(client.calls[2]) {
		if reqMsg.Role == llm.RoleDeveloper && reqMsg.MessageType == llm.MessageTypeCompactionSoonReminder {
			remindersInThirdRequest++
		}
	}
	if remindersInThirdRequest != 1 {
		t.Fatalf("expected exactly one historical reminder in request while disabled, got %d messages=%+v", remindersInThirdRequest, requestMessages(client.calls[2]))
	}
	if reminders := countCompactionSoonReminderWarnings(eng.ChatSnapshot()); reminders != 1 {
		t.Fatalf("expected reminder not to duplicate while disabled, got %d entries=%+v", reminders, eng.ChatSnapshot().Entries)
	}
}

func countCompactionSoonReminderWarnings(snapshot ChatSnapshot) int {
	count := 0
	for _, entry := range snapshot.Entries {
		if entry.Role == "warning" && entry.Text == prompts.RenderCompactionSoonReminderPrompt(false) {
			count++
		}
	}
	return count
}

func TestCompactionSoonReminderIncludesTriggerHandoffAdditionWhenConfigured(t *testing.T) {
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
		EnabledTools:          []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 890, WindowTokens: 2_000})

	if err := eng.maybeAppendCompactionSoonReminder(context.Background(), "step-1"); err != nil {
		t.Fatalf("append reminder: %v", err)
	}

	reminderText := prompts.RenderCompactionSoonReminderPrompt(true)
	reminders := 0
	for _, entry := range eng.ChatSnapshot().Entries {
		if entry.Role == "warning" && entry.Text == reminderText {
			reminders++
		}
	}
	if reminders != 1 {
		t.Fatalf("expected enabled reminder text once, got %d entries=%+v", reminders, eng.ChatSnapshot().Entries)
	}
}

func TestTriggerHandoffFailsBeforeReminder(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	_, _, err = eng.TriggerHandoff(context.Background(), "step-1", llm.ToolCall{ID: "call-handoff-1", Name: string(tools.ToolTriggerHandoff)}, "", "")
	if err == nil {
		t.Fatal("expected trigger_handoff to fail before reminder")
	}
	if err.Error() != handoffTooEarlyMessage {
		t.Fatalf("unexpected early handoff error: %v", err)
	}
}

func TestTriggerHandoffFailsWhenAutoCompactionDisabled(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.mu.Lock()
	eng.compactionSoonReminderIssued = true
	eng.mu.Unlock()
	changed, enabled := eng.SetAutoCompactionEnabled(false)
	if !changed || enabled {
		t.Fatalf("expected auto compaction toggle off, changed=%v enabled=%v", changed, enabled)
	}

	_, _, err = eng.TriggerHandoff(context.Background(), "step-1", llm.ToolCall{ID: "call-handoff-1", Name: string(tools.ToolTriggerHandoff)}, "", "")
	if err == nil {
		t.Fatal("expected trigger_handoff to fail while auto compaction is disabled")
	}
	if err.Error() != handoffDisabledByUserMessage {
		t.Fatalf("unexpected disabled handoff error: %v", err)
	}
}

func TestTriggerHandoffSchedulesCompactionAndAppendsFutureMessageWithoutManualCarryover(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{
		responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "summary"}}},
	}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.mu.Lock()
	eng.compactionSoonReminderIssued = true
	eng.mu.Unlock()
	activeCall := llm.ToolCall{ID: "call-handoff-1", Name: string(tools.ToolTriggerHandoff), Input: json.RawMessage(`{"summarizer_prompt":"keep API details","future_agent_message":"resume with tests"}`)}

	summary, futureAdded, err := eng.TriggerHandoff(context.Background(), "step-1", activeCall, "keep API details", "resume with tests")
	if err != nil {
		t.Fatalf("trigger handoff: %v", err)
	}
	if summary == "" || !futureAdded {
		t.Fatalf("unexpected trigger handoff result: summary=%q futureAdded=%v", summary, futureAdded)
	}
	if len(client.calls) != 0 {
		t.Fatalf("expected handoff scheduling to avoid immediate compaction model call, got %d", len(client.calls))
	}
	if err := eng.applyPendingHandoffIfNeeded(context.Background(), "step-1"); err != nil {
		t.Fatalf("apply pending handoff: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected one local-summary model call, got %d", len(client.calls))
	}

	foundPrompt := false
	for _, item := range client.calls[0].Items {
		if item.Type == llm.ResponseItemTypeMessage && item.Role == llm.RoleDeveloper && item.Content == compactionInstructions("keep API details") {
			foundPrompt = true
			break
		}
	}
	if !foundPrompt {
		t.Fatalf("expected handoff to reuse compaction instructions, got %+v", client.calls[0].Items)
	}

	messages := eng.snapshotMessages()
	foundFutureMessage := false
	foundManualCarryover := false
	for _, message := range messages {
		if message.MessageType == llm.MessageTypeHandoffFutureMessage && message.Content == "resume with tests" {
			foundFutureMessage = true
		}
		if message.MessageType == llm.MessageTypeManualCompactionCarryover {
			foundManualCarryover = true
		}
	}
	if !foundFutureMessage {
		t.Fatalf("expected future-agent message in history, got %+v", messages)
	}
	if foundManualCarryover {
		t.Fatalf("did not expect manual compaction carryover for trigger_handoff, got %+v", messages)
	}

	entries := eng.ChatSnapshot().Entries
	foundDeveloperContext := false
	for _, entry := range entries {
		if entry.Role == string(transcript.EntryRoleDeveloperContext) && entry.Text == "resume with tests" {
			foundDeveloperContext = true
		}
		if entry.Role == string(transcript.EntryRoleManualCompactionCarryover) {
			t.Fatalf("did not expect manual carryover transcript entry for trigger_handoff, got %+v", entries)
		}
	}
	if !foundDeveloperContext {
		t.Fatalf("expected future-agent message to be detail-only developer context, got %+v", entries)
	}
}

func TestPendingTriggerHandoffRetriesAfterCompactionFailure(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "condensed summary"},
			Usage:     llm.Usage{InputTokens: 200, WindowTokens: 2_000},
		},
	}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.mu.Lock()
	eng.compactionSoonReminderIssued = true
	eng.mu.Unlock()

	_, _, err = eng.TriggerHandoff(context.Background(), "step-1", llm.ToolCall{ID: "call_handoff_retry", Name: string(tools.ToolTriggerHandoff)}, "keep API details", "resume with tests")
	if err != nil {
		t.Fatalf("trigger handoff: %v", err)
	}
	if eng.pendingHandoffRequest == nil {
		t.Fatal("expected queued handoff before compaction attempt")
	}

	client.responses = nil
	if err := eng.applyPendingHandoffIfNeeded(context.Background(), "step-1"); err == nil {
		t.Fatal("expected first pending handoff attempt to fail when compaction summary response is missing")
	}
	if eng.pendingHandoffRequest == nil {
		t.Fatal("expected failed handoff compaction to leave pending request queued for retry")
	}
	if got, want := eng.pendingHandoffRequest.summarizerPrompt, "keep API details"; got != want {
		t.Fatalf("pending summarizer_prompt after failure = %q, want %q", got, want)
	}
	if got, want := eng.pendingHandoffRequest.futureAgentMessage, "resume with tests"; got != want {
		t.Fatalf("pending future_agent_message after failure = %q, want %q", got, want)
	}

	client.responses = []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "condensed summary"},
		Usage:     llm.Usage{InputTokens: 200, WindowTokens: 2_000},
	}}
	if err := eng.applyPendingHandoffIfNeeded(context.Background(), "step-1"); err != nil {
		t.Fatalf("retry pending handoff: %v", err)
	}
	if eng.pendingHandoffRequest != nil {
		t.Fatalf("expected successful retry to clear pending handoff, got %+v", eng.pendingHandoffRequest)
	}

	messages := eng.snapshotMessages()
	foundFutureMessage := false
	for _, message := range messages {
		if message.MessageType == llm.MessageTypeHandoffFutureMessage && message.Content == "resume with tests" {
			foundFutureMessage = true
			break
		}
	}
	if !foundFutureMessage {
		t.Fatalf("expected successful retry to append future-agent message, got %+v", messages)
	}
}

func TestPendingTriggerHandoffRetriesFutureMessageAfterAppendFailureWithoutRecompaction(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "condensed summary"},
		Usage:     llm.Usage{InputTokens: 200, WindowTokens: 2_000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.mu.Lock()
	eng.compactionSoonReminderIssued = true
	eng.mu.Unlock()

	_, _, err = eng.TriggerHandoff(context.Background(), "step-1", llm.ToolCall{ID: "call_handoff_append_retry", Name: string(tools.ToolTriggerHandoff)}, "keep API details", "resume with tests")
	if err != nil {
		t.Fatalf("trigger handoff: %v", err)
	}

	appendFailures := 0
	eng.beforePersistMessage = func(msg llm.Message) error {
		if msg.MessageType != llm.MessageTypeHandoffFutureMessage || appendFailures > 0 {
			return nil
		}
		appendFailures++
		return errors.New("synthetic future-message append failure")
	}
	if err := eng.applyPendingHandoffIfNeeded(context.Background(), "step-1"); err == nil {
		t.Fatal("expected first pending handoff attempt to fail while appending future-agent message")
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected exactly one compaction summary call after append failure, got %d", len(client.calls))
	}
	if eng.pendingHandoffRequest != nil {
		t.Fatalf("expected compaction-success path to consume original handoff request, got %+v", eng.pendingHandoffRequest)
	}
	if got, want := eng.pendingHandoffFutureMessage, "resume with tests"; got != want {
		t.Fatalf("pending future-agent message after append failure = %q, want %q", got, want)
	}

	eng.beforePersistMessage = nil
	if err := eng.applyPendingHandoffIfNeeded(context.Background(), "step-1"); err != nil {
		t.Fatalf("retry pending future-agent message append: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected retry after future-message append failure not to re-run compaction, got %d compaction calls", len(client.calls))
	}
	if got := eng.pendingHandoffFutureMessage; got != "" {
		t.Fatalf("expected successful retry to clear pending future-agent message, got %q", got)
	}

	messages := eng.snapshotMessages()
	foundFutureMessage := false
	for _, message := range messages {
		if message.MessageType == llm.MessageTypeHandoffFutureMessage && message.Content == "resume with tests" {
			foundFutureMessage = true
			break
		}
	}
	if !foundFutureMessage {
		t.Fatalf("expected successful retry to append future-agent message after append failure, got %+v", messages)
	}
}

func TestReopenedSessionAfterTriggerHandoffFutureMessageAppendFailureRetriesWithoutRecompaction(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "condensed summary"},
		Usage:     llm.Usage{InputTokens: 200, WindowTokens: 2_000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	handoffCall := llm.ToolCall{
		ID:    "call_handoff_reopen_future_retry",
		Name:  string(tools.ToolTriggerHandoff),
		Input: mustJSON(map[string]any{"summarizer_prompt": "keep API details", "future_agent_message": "resume after restart"}),
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleAssistant, Content: "handing off", Phase: llm.MessagePhaseCommentary, ToolCalls: []llm.ToolCall{handoffCall}}); err != nil {
		t.Fatalf("append assistant tool call: %v", err)
	}
	resultOutput := mustJSON(triggerhandofftool.ResultPayload{
		Summary:                 "Handoff scheduled. Context will be compacted before the next model turn and future-agent guidance was saved.",
		FutureAgentMessageAdded: true,
	})
	if err := eng.persistToolCompletion("step-1", tools.Result{CallID: handoffCall.ID, Name: tools.ToolTriggerHandoff, Output: resultOutput}); err != nil {
		t.Fatalf("persist tool completion: %v", err)
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleTool, ToolCallID: handoffCall.ID, Name: string(tools.ToolTriggerHandoff), Content: string(resultOutput)}); err != nil {
		t.Fatalf("append tool result: %v", err)
	}
	eng.queueHandoffRequest("keep API details", "resume after restart")

	eng.beforePersistMessage = func(msg llm.Message) error {
		if msg.MessageType == llm.MessageTypeHandoffFutureMessage {
			return errors.New("synthetic future-message append failure")
		}
		return nil
	}
	if err := eng.applyPendingHandoffIfNeeded(context.Background(), "step-1"); err == nil {
		t.Fatal("expected handoff future-message append to fail")
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected exactly one compaction summary call before reopen, got %d", len(client.calls))
	}
	if eng.pendingHandoffRequest != nil {
		t.Fatalf("expected successful compaction to consume queued handoff request before reopen, got %+v", eng.pendingHandoffRequest)
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	resumedClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "resumed", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{InputTokens: 300, WindowTokens: 2_000},
	}}}
	restored, err := New(reopenedStore, resumedClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	if restored.pendingHandoffRequest != nil {
		t.Fatalf("did not expect restore to requeue handoff after successful compaction, got %+v", restored.pendingHandoffRequest)
	}
	if got, want := restored.pendingHandoffFutureMessage, "resume after restart"; got != want {
		t.Fatalf("pending future-agent message after reopen = %q, want %q", got, want)
	}

	msg, err := restored.SubmitUserMessage(context.Background(), "continue")
	if err != nil {
		t.Fatalf("submit after reopen: %v", err)
	}
	if msg.Content != "resumed" {
		t.Fatalf("assistant content = %q, want resumed", msg.Content)
	}
	if len(resumedClient.calls) != 1 {
		t.Fatalf("expected reopened retry to append future-agent message without re-running compaction, got %d requests", len(resumedClient.calls))
	}
	if got, want := resumedClient.calls[0].SessionID, restored.conversationSessionID(); got != want {
		t.Fatalf("expected reopened request session id to stay on the main conversation after restored handoff compaction, got %q want %q", got, want)
	}
	if got, want := resumedClient.calls[0].PromptCacheKey, restored.conversationPromptCacheKey(); got != want {
		t.Fatalf("expected reopened request prompt cache key to stay rotated after restored handoff compaction, got %q want %q", got, want)
	}
	foundFuture := false
	for _, item := range resumedClient.calls[0].Items {
		if item.Type == llm.ResponseItemTypeMessage && item.MessageType == llm.MessageTypeHandoffFutureMessage && item.Content == "resume after restart" {
			foundFuture = true
			break
		}
	}
	if !foundFuture {
		t.Fatalf("expected reopened request to include retried future-agent message, items=%+v", resumedClient.calls[0].Items)
	}
}

func TestRunStepLoopTriggerHandoffOmitsCallAndOutputFromFollowUpRequestAndKeepsFutureMessage(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{
		responses: []llm.Response{
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "handing off", Phase: llm.MessagePhaseCommentary},
				ToolCalls: []llm.ToolCall{{
					ID:    "call_handoff_1",
					Name:  string(tools.ToolTriggerHandoff),
					Input: json.RawMessage(`{"summarizer_prompt":"keep API details","future_agent_message":"resume with tests"}`),
				}},
				Usage: llm.Usage{InputTokens: 100, WindowTokens: 2_000},
			},
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "condensed summary"},
				Usage:     llm.Usage{InputTokens: 200, WindowTokens: 2_000},
			},
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
				Usage:     llm.Usage{InputTokens: 300, WindowTokens: 2_000},
			},
		},
	}

	var eng *Engine
	registry := tools.NewRegistry(
		fakeTool{name: tools.ToolShell},
		triggerhandofftool.New(func() triggerhandofftool.Controller { return eng }),
	)
	eng, err = New(store, client, registry, Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.mu.Lock()
	eng.compactionSoonReminderIssued = true
	eng.mu.Unlock()

	msg, err := eng.runStepLoop(context.Background(), "step-1")
	if err != nil {
		t.Fatalf("runStepLoop: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("unexpected final assistant message: %+v", msg)
	}
	if len(client.calls) != 3 {
		t.Fatalf("expected tool call, local compaction summary, and follow-up requests, got %d", len(client.calls))
	}
	if got, want := client.calls[2].SessionID, eng.conversationSessionID(); got != want {
		t.Fatalf("expected follow-up request session id to stay on the main conversation after handoff compaction, got %q want %q", got, want)
	}
	if got, want := client.calls[2].PromptCacheKey, eng.conversationPromptCacheKey(); got != want {
		t.Fatalf("expected follow-up request prompt cache key to rotate after handoff compaction, got %q want %q", got, want)
	}

	followUp := client.calls[2]
	foundCall := false
	foundOutput := false
	futureIdx := -1
	for idx, item := range followUp.Items {
		switch {
		case item.Type == llm.ResponseItemTypeFunctionCall && item.CallID == "call_handoff_1":
			foundCall = true
		case item.Type == llm.ResponseItemTypeFunctionCallOutput && item.CallID == "call_handoff_1":
			foundOutput = true
		case item.Type == llm.ResponseItemTypeMessage && item.MessageType == llm.MessageTypeHandoffFutureMessage && item.Content == "resume with tests":
			futureIdx = idx
		}
	}
	if foundCall || foundOutput {
		t.Fatalf("expected follow-up request to omit trigger_handoff call/output items entirely, foundCall=%v foundOutput=%v items=%+v", foundCall, foundOutput, followUp.Items)
	}
	if futureIdx < 0 {
		t.Fatalf("expected future-agent message in follow-up request, items=%+v", followUp.Items)
	}
}

func TestRunStepLoopInjectsReminderBeforeTriggerHandoffAndOmitsCallOutputFromFollowUp(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{
		responses: []llm.Response{
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "handing off", Phase: llm.MessagePhaseCommentary},
				ToolCalls: []llm.ToolCall{{
					ID:    "call_handoff_2",
					Name:  string(tools.ToolTriggerHandoff),
					Input: json.RawMessage(`{"future_agent_message":"resume with tests"}`),
				}},
				Usage: llm.Usage{InputTokens: 100, WindowTokens: 2_000},
			},
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "condensed summary"},
				Usage:     llm.Usage{InputTokens: 200, WindowTokens: 2_000},
			},
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
				Usage:     llm.Usage{InputTokens: 300, WindowTokens: 2_000},
			},
		},
	}

	var eng *Engine
	registry := tools.NewRegistry(
		fakeTool{name: tools.ToolShell},
		triggerhandofftool.New(func() triggerhandofftool.Controller { return eng }),
	)
	eng, err = New(store, client, registry, Config{
		Model:                 "gpt-5",
		CompactionMode:        "local",
		ContextWindowTokens:   2_000,
		AutoCompactTokenLimit: 1_000,
		EnabledTools:          []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 890, WindowTokens: 2_000})

	msg, err := eng.runStepLoop(context.Background(), "step-1")
	if err != nil {
		t.Fatalf("runStepLoop: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("unexpected final assistant message: %+v", msg)
	}
	if len(client.calls) != 3 {
		t.Fatalf("expected trigger request, local compaction summary, and follow-up requests, got %d", len(client.calls))
	}
	if got, want := client.calls[2].SessionID, eng.conversationSessionID(); got != want {
		t.Fatalf("expected follow-up request session id to stay on the main conversation after handoff compaction, got %q want %q", got, want)
	}
	if got, want := client.calls[2].PromptCacheKey, eng.conversationPromptCacheKey(); got != want {
		t.Fatalf("expected follow-up request prompt cache key to rotate after handoff compaction, got %q want %q", got, want)
	}

	remindersInFirstRequest := 0
	for _, reqMsg := range requestMessages(client.calls[0]) {
		if reqMsg.Role == llm.RoleDeveloper && reqMsg.MessageType == llm.MessageTypeCompactionSoonReminder {
			remindersInFirstRequest++
		}
	}
	if remindersInFirstRequest != 1 {
		t.Fatalf("expected exactly one pre-request reminder before trigger_handoff, got %d messages=%+v", remindersInFirstRequest, requestMessages(client.calls[0]))
	}

	followUp := client.calls[2]
	foundCall := false
	foundOutput := false
	futureIdx := -1
	for idx, item := range followUp.Items {
		switch {
		case item.Type == llm.ResponseItemTypeFunctionCall && item.CallID == "call_handoff_2":
			foundCall = true
		case item.Type == llm.ResponseItemTypeFunctionCallOutput && item.CallID == "call_handoff_2":
			foundOutput = true
		case item.Type == llm.ResponseItemTypeMessage && item.MessageType == llm.MessageTypeHandoffFutureMessage && item.Content == "resume with tests":
			futureIdx = idx
		}
	}
	if foundCall || foundOutput {
		t.Fatalf("expected follow-up request to omit trigger_handoff call/output items entirely, foundCall=%v foundOutput=%v items=%+v", foundCall, foundOutput, followUp.Items)
	}
	if futureIdx < 0 {
		t.Fatalf("expected future-agent message in follow-up request, items=%+v", followUp.Items)
	}
}

func TestReopenedSessionAfterTriggerHandoffUsesRotatedRequestSessionAndOmitsLingeringCallOutput(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	firstClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "handing off", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{
				ID:    "call_handoff_restart",
				Name:  string(tools.ToolTriggerHandoff),
				Input: json.RawMessage(`{"future_agent_message":"resume after restart"}`),
			}},
			Usage: llm.Usage{InputTokens: 100, WindowTokens: 2_000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "condensed summary"},
			Usage:     llm.Usage{InputTokens: 200, WindowTokens: 2_000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{InputTokens: 300, WindowTokens: 2_000},
		},
	}}

	var eng *Engine
	registry := tools.NewRegistry(
		fakeTool{name: tools.ToolShell},
		triggerhandofftool.New(func() triggerhandofftool.Controller { return eng }),
	)
	eng, err = New(store, firstClient, registry, Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.mu.Lock()
	eng.compactionSoonReminderIssued = true
	eng.mu.Unlock()

	if _, err := eng.runStepLoop(context.Background(), "step-1"); err != nil {
		t.Fatalf("runStepLoop: %v", err)
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	resumedClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "resumed", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 2_000},
	}}}
	restored, err := New(reopenedStore, resumedClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}

	msg, err := restored.SubmitUserMessage(context.Background(), "continue")
	if err != nil {
		t.Fatalf("submit after reopen: %v", err)
	}
	if msg.Content != "resumed" {
		t.Fatalf("assistant content = %q, want resumed", msg.Content)
	}
	if len(resumedClient.calls) != 1 {
		t.Fatalf("expected one resumed model call, got %d", len(resumedClient.calls))
	}
	if got, want := resumedClient.calls[0].SessionID, restored.conversationSessionID(); got != want {
		t.Fatalf("expected resumed request session id to stay on the main conversation after restore, got %q want %q", got, want)
	}
	if got, want := resumedClient.calls[0].PromptCacheKey, restored.conversationPromptCacheKey(); got != want {
		t.Fatalf("expected resumed request prompt cache key to stay rotated after restore, got %q want %q", got, want)
	}
	for _, item := range resumedClient.calls[0].Items {
		switch {
		case item.Type == llm.ResponseItemTypeFunctionCall && item.CallID == "call_handoff_restart":
			t.Fatalf("did not expect reopened request to include lingering trigger_handoff call item, items=%+v", resumedClient.calls[0].Items)
		case item.Type == llm.ResponseItemTypeFunctionCallOutput && item.CallID == "call_handoff_restart":
			t.Fatalf("did not expect reopened request to include lingering trigger_handoff output item, items=%+v", resumedClient.calls[0].Items)
		}
	}
}

func TestReopenedSessionAfterSuccessfulTriggerHandoffRequeuesPendingHandoff(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	handoffCall := llm.ToolCall{
		ID:    "call_handoff_pending_restore",
		Name:  string(tools.ToolTriggerHandoff),
		Input: mustJSON(map[string]any{"summarizer_prompt": "keep API details", "future_agent_message": "resume after restart"}),
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleAssistant, Content: "handing off", Phase: llm.MessagePhaseCommentary, ToolCalls: []llm.ToolCall{handoffCall}}); err != nil {
		t.Fatalf("append assistant tool call: %v", err)
	}
	resultOutput := mustJSON(triggerhandofftool.ResultPayload{
		Summary:                 "Handoff scheduled. Context will be compacted before the next model turn and future-agent guidance was saved.",
		FutureAgentMessageAdded: true,
	})
	if err := eng.persistToolCompletion("step-1", tools.Result{CallID: handoffCall.ID, Name: tools.ToolTriggerHandoff, Output: resultOutput}); err != nil {
		t.Fatalf("persist tool completion: %v", err)
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleTool, ToolCallID: handoffCall.ID, Name: string(tools.ToolTriggerHandoff), Content: string(resultOutput)}); err != nil {
		t.Fatalf("append tool result: %v", err)
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	resumedClient := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "condensed summary"},
			Usage:     llm.Usage{InputTokens: 200, WindowTokens: 2_000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "resumed", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{InputTokens: 300, WindowTokens: 2_000},
		},
	}}
	restored, err := New(reopenedStore, resumedClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	if restored.pendingHandoffRequest == nil {
		t.Fatal("expected restore to recover pending handoff request")
	}
	if got, want := restored.pendingHandoffRequest.summarizerPrompt, "keep API details"; got != want {
		t.Fatalf("pending summarizer_prompt = %q, want %q", got, want)
	}
	if got, want := restored.pendingHandoffRequest.futureAgentMessage, "resume after restart"; got != want {
		t.Fatalf("pending future_agent_message = %q, want %q", got, want)
	}

	msg, err := restored.SubmitUserMessage(context.Background(), "continue")
	if err != nil {
		t.Fatalf("submit after reopen: %v", err)
	}
	if msg.Content != "resumed" {
		t.Fatalf("assistant content = %q, want resumed", msg.Content)
	}
	if len(resumedClient.calls) != 2 {
		t.Fatalf("expected recovered handoff compaction plus follow-up request, got %d", len(resumedClient.calls))
	}
	first := resumedClient.calls[0]
	foundInstructions := false
	for _, item := range first.Items {
		if item.Type == llm.ResponseItemTypeMessage && item.Role == llm.RoleDeveloper && item.Content == compactionInstructions("keep API details") {
			foundInstructions = true
			break
		}
	}
	if !foundInstructions {
		t.Fatalf("expected restored handoff compaction request to include summarizer prompt, items=%+v", first.Items)
	}
	followUp := resumedClient.calls[1]
	if got, want := followUp.SessionID, restored.conversationSessionID(); got != want {
		t.Fatalf("expected follow-up request session id to stay on the main conversation after restored handoff compaction, got %q want %q", got, want)
	}
	if got, want := followUp.PromptCacheKey, restored.conversationPromptCacheKey(); got != want {
		t.Fatalf("expected follow-up request prompt cache key to rotate after restored handoff compaction, got %q want %q", got, want)
	}
	foundCall := false
	foundOutput := false
	foundFuture := false
	for _, item := range followUp.Items {
		switch {
		case item.Type == llm.ResponseItemTypeFunctionCall && item.CallID == handoffCall.ID:
			foundCall = true
		case item.Type == llm.ResponseItemTypeFunctionCallOutput && item.CallID == handoffCall.ID:
			foundOutput = true
		case item.Type == llm.ResponseItemTypeMessage && item.MessageType == llm.MessageTypeHandoffFutureMessage && item.Content == "resume after restart":
			foundFuture = true
		}
	}
	if foundCall || foundOutput {
		t.Fatalf("expected recovered follow-up request to omit lingering trigger_handoff items, foundCall=%v foundOutput=%v items=%+v", foundCall, foundOutput, followUp.Items)
	}
	if !foundFuture {
		t.Fatalf("expected recovered follow-up request to include future-agent message, items=%+v", followUp.Items)
	}
}

func TestForkedSessionAfterTriggerHandoffRequeuesPendingHandoff(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	handoffCall := llm.ToolCall{
		ID:    "call_handoff_fork_restore",
		Name:  string(tools.ToolTriggerHandoff),
		Input: mustJSON(map[string]any{"future_agent_message": "resume after fork"}),
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleAssistant, Content: "handing off", Phase: llm.MessagePhaseCommentary, ToolCalls: []llm.ToolCall{handoffCall}}); err != nil {
		t.Fatalf("append assistant tool call: %v", err)
	}
	resultOutput := mustJSON(triggerhandofftool.ResultPayload{
		Summary:                 "Handoff scheduled. Context will be compacted before the next model turn and future-agent guidance was saved.",
		FutureAgentMessageAdded: true,
	})
	if err := eng.persistToolCompletion("step-1", tools.Result{CallID: handoffCall.ID, Name: tools.ToolTriggerHandoff, Output: resultOutput}); err != nil {
		t.Fatalf("persist tool completion: %v", err)
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleTool, ToolCallID: handoffCall.ID, Name: string(tools.ToolTriggerHandoff), Content: string(resultOutput)}); err != nil {
		t.Fatalf("append tool result: %v", err)
	}
	if err := eng.appendMessage("step-2", llm.Message{Role: llm.RoleUser, Content: "edit anchor"}); err != nil {
		t.Fatalf("append second user message: %v", err)
	}

	forkedStore, err := session.ForkAtUserMessage(store, 2, "Parent -> edit")
	if err != nil {
		t.Fatalf("fork session: %v", err)
	}
	forked, err := New(forkedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("restore forked engine: %v", err)
	}
	if forked.pendingHandoffRequest == nil {
		t.Fatal("expected forked session to recover pending handoff request")
	}
	if got, want := forked.pendingHandoffRequest.futureAgentMessage, "resume after fork"; got != want {
		t.Fatalf("forked pending future_agent_message = %q, want %q", got, want)
	}
}

func TestReopenedSessionAfterTriggerHandoffDoesNotRequeueWhenAnyCompactionAlreadyHappened(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	handoffCall := llm.ToolCall{
		ID:    "call_handoff_satisfied_restore",
		Name:  string(tools.ToolTriggerHandoff),
		Input: mustJSON(map[string]any{"future_agent_message": "resume after manual compact"}),
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleAssistant, Content: "handing off", Phase: llm.MessagePhaseCommentary, ToolCalls: []llm.ToolCall{handoffCall}}); err != nil {
		t.Fatalf("append assistant tool call: %v", err)
	}
	resultOutput := mustJSON(triggerhandofftool.ResultPayload{
		Summary:                 "Handoff scheduled. Context will be compacted before the next model turn and future-agent guidance was saved.",
		FutureAgentMessageAdded: true,
	})
	if err := eng.persistToolCompletion("step-1", tools.Result{CallID: handoffCall.ID, Name: tools.ToolTriggerHandoff, Output: resultOutput}); err != nil {
		t.Fatalf("persist tool completion: %v", err)
	}
	if err := eng.replaceHistory("step-1", "local", compactionModeManual, llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeCompactionSummary, Content: "summary"}})); err != nil {
		t.Fatalf("replace history: %v", err)
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	resumedClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "resumed", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{InputTokens: 300, WindowTokens: 2_000},
	}}}
	restored, err := New(reopenedStore, resumedClient, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	if restored.pendingHandoffRequest != nil {
		t.Fatalf("did not expect restore to requeue handoff after later compaction, got %+v", restored.pendingHandoffRequest)
	}

	msg, err := restored.SubmitUserMessage(context.Background(), "continue")
	if err != nil {
		t.Fatalf("submit after reopen: %v", err)
	}
	if msg.Content != "resumed" {
		t.Fatalf("assistant content = %q, want resumed", msg.Content)
	}
	if len(resumedClient.calls) != 1 {
		t.Fatalf("expected compaction-satisfied session to resume with a single request, got %d", len(resumedClient.calls))
	}
	if got, want := resumedClient.calls[0].SessionID, restored.conversationSessionID(); got != want {
		t.Fatalf("expected resumed request session id to stay on the main conversation after restored compaction, got %q want %q", got, want)
	}
	if got, want := resumedClient.calls[0].PromptCacheKey, restored.conversationPromptCacheKey(); got != want {
		t.Fatalf("expected resumed request prompt cache key to stay rotated after restored compaction, got %q want %q", got, want)
	}
	for _, item := range resumedClient.calls[0].Items {
		switch {
		case item.Type == llm.ResponseItemTypeFunctionCall && item.CallID == handoffCall.ID:
			t.Fatalf("did not expect reopened request to include lingering trigger_handoff call item, items=%+v", resumedClient.calls[0].Items)
		case item.Type == llm.ResponseItemTypeFunctionCallOutput && item.CallID == handoffCall.ID:
			t.Fatalf("did not expect reopened request to include lingering trigger_handoff output item, items=%+v", resumedClient.calls[0].Items)
		}
	}
}

func TestReopenedSessionAfterFailedTriggerHandoffDoesNotRequeuePendingHandoff(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	handoffCall := llm.ToolCall{
		ID:    "call_handoff_failed_restore",
		Name:  string(tools.ToolTriggerHandoff),
		Input: mustJSON(map[string]any{"future_agent_message": "should not resume"}),
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleAssistant, Content: "attempting handoff", Phase: llm.MessagePhaseCommentary, ToolCalls: []llm.ToolCall{handoffCall}}); err != nil {
		t.Fatalf("append assistant tool call: %v", err)
	}
	failedOutput := mustJSON(map[string]any{"error": handoffDisabledByUserMessage})
	if err := eng.persistToolCompletion("step-1", tools.Result{CallID: handoffCall.ID, Name: tools.ToolTriggerHandoff, IsError: true, Output: failedOutput}); err != nil {
		t.Fatalf("persist failed tool completion: %v", err)
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	restored, err := New(reopenedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	if restored.pendingHandoffRequest != nil {
		t.Fatalf("did not expect failed trigger_handoff completion to requeue handoff, got %+v", restored.pendingHandoffRequest)
	}
}

func TestReopenedSessionAfterReviewerRollbackStillRequeuesPendingTriggerHandoff(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	handoffCall := llm.ToolCall{
		ID:    "call_handoff_rollback_restore",
		Name:  string(tools.ToolTriggerHandoff),
		Input: mustJSON(map[string]any{"future_agent_message": "resume after rollback"}),
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleAssistant, Content: "handing off", Phase: llm.MessagePhaseCommentary, ToolCalls: []llm.ToolCall{handoffCall}}); err != nil {
		t.Fatalf("append assistant tool call: %v", err)
	}
	resultOutput := mustJSON(triggerhandofftool.ResultPayload{
		Summary:                 "Handoff scheduled. Context will be compacted before the next model turn and future-agent guidance was saved.",
		FutureAgentMessageAdded: true,
	})
	if err := eng.persistToolCompletion("step-1", tools.Result{CallID: handoffCall.ID, Name: tools.ToolTriggerHandoff, Output: resultOutput}); err != nil {
		t.Fatalf("persist tool completion: %v", err)
	}
	if err := eng.replaceHistory("step-1", "reviewer_rollback", compactionModeManual, llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleUser, Content: "rolled back"}})); err != nil {
		t.Fatalf("append reviewer rollback history replacement: %v", err)
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	restored, err := New(reopenedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	if restored.pendingHandoffRequest == nil {
		t.Fatal("expected reviewer rollback to preserve pending handoff recovery")
	}
	if got, want := restored.pendingHandoffRequest.futureAgentMessage, "resume after rollback"; got != want {
		t.Fatalf("pending future_agent_message = %q, want %q", got, want)
	}
}

func TestManualCompactionClearsQueuedTriggerHandoff(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "condensed summary"},
			Usage:     llm.Usage{InputTokens: 200, WindowTokens: 2_000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{InputTokens: 300, WindowTokens: 2_000},
		},
	}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []tools.ID{tools.ToolShell, tools.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.mu.Lock()
	eng.compactionSoonReminderIssued = true
	eng.mu.Unlock()

	_, _, err = eng.TriggerHandoff(context.Background(), "step-1", llm.ToolCall{ID: "call_handoff_manual_clear", Name: string(tools.ToolTriggerHandoff)}, "", "resume after manual compact")
	if err != nil {
		t.Fatalf("trigger handoff: %v", err)
	}
	if eng.pendingHandoffRequest == nil {
		t.Fatal("expected queued handoff before manual compaction")
	}
	if err := eng.CompactContext(context.Background(), "manual compact now"); err != nil {
		t.Fatalf("manual compact: %v", err)
	}
	if eng.pendingHandoffRequest != nil {
		t.Fatalf("expected manual compaction to clear queued handoff, got %+v", eng.pendingHandoffRequest)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "continue")
	if err != nil {
		t.Fatalf("submit after manual compaction: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("assistant content = %q, want done", msg.Content)
	}
	if len(client.calls) != 2 {
		t.Fatalf("expected manual compaction plus a single follow-up request, got %d", len(client.calls))
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

func TestManualCompactionLocalSendsPromptAsDeveloperMessage(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{
		responses: []llm.Response{{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "summary"},
		}},
	}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5", CompactionMode: "local"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	if err := eng.CompactContext(context.Background(), ""); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected one local-summary model call, got %d", len(client.calls))
	}

	found := false
	for _, item := range client.calls[0].Items {
		if item.Type == llm.ResponseItemTypeMessage && item.Role == llm.RoleDeveloper && item.Content == prompts.CompactionPrompt {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected local compaction prompt as developer message, got %+v", client.calls[0].Items)
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
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeCompactionSummary, Content: "older summary"}); err != nil {
		t.Fatalf("append compaction summary: %v", err)
	}

	if err := eng.CompactContext(context.Background(), ""); err != nil {
		t.Fatalf("compact: %v", err)
	}

	messages := eng.snapshotMessages()
	if len(messages) == 0 {
		t.Fatal("expected messages after manual compaction")
	}
	carryoverIndex := -1
	var carryover llm.Message
	for i, message := range messages {
		switch message.MessageType {
		case llm.MessageTypeManualCompactionCarryover:
			carryoverIndex = i
			carryover = message
		}
	}
	if carryoverIndex < 0 {
		t.Fatalf("expected manual compaction carryover in message history, got %+v", messages)
	}
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

func TestManualLocalCompactionPlacesSummaryBeforeCarryoverInTranscript(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{
		responses: []llm.Response{{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "condensed summary"},
			Usage:     llm.Usage{InputTokens: 1000, OutputTokens: 100, WindowTokens: 200000},
		}},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5", CompactionMode: "local"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "please keep tests green"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	if err := eng.CompactContext(context.Background(), ""); err != nil {
		t.Fatalf("compact: %v", err)
	}

	entries := eng.ChatSnapshot().Entries
	if len(entries) < 3 {
		t.Fatalf("expected transcript entries after compaction, got %+v", entries)
	}

	summaryIndex := -1
	carryoverIndex := -1
	for i, entry := range entries {
		switch entry.Role {
		case "compaction_summary":
			summaryIndex = i
		case "manual_compaction_carryover":
			carryoverIndex = i
		}
	}
	if summaryIndex < 0 || carryoverIndex < 0 {
		t.Fatalf("expected summary and carryover entries, got %+v", entries)
	}
	if summaryIndex >= carryoverIndex {
		t.Fatalf("expected compaction summary before manual carryover, got %+v", entries)
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
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeCompactionSummary, Content: "old compacted summary"}); err != nil {
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
		if item.Role == llm.RoleDeveloper && item.MessageType == llm.MessageTypeCompactionSummary {
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
			if item.Type == llm.ResponseItemTypeMessage && item.Role == llm.RoleDeveloper && item.MessageType == llm.MessageTypeCompactionSummary {
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
			if item.Type == llm.ResponseItemTypeMessage && item.MessageType == llm.MessageTypeCompactionSummary {
				t.Fatalf("did not expect local compaction summary fallback, request=%+v", req.Items)
			}
		}
	}
}
