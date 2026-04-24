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
	"builder/shared/toolspec"
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
		ProviderID:                     "openai",
		SupportsResponsesAPI:           true,
		SupportsResponsesCompact:       true,
		SupportsRequestInputTokenCount: true,
		SupportsPromptCacheKey:         true,
		SupportsNativeWebSearch:        true,
		SupportsReasoningEncrypted:     true,
		SupportsServerSideContextEdit:  true,
		IsOpenAIFirstParty:             true,
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
		ProviderID:                     "openai",
		SupportsResponsesAPI:           true,
		SupportsResponsesCompact:       true,
		SupportsRequestInputTokenCount: true,
		SupportsPromptCacheKey:         true,
		SupportsNativeWebSearch:        true,
		SupportsReasoningEncrypted:     true,
		SupportsServerSideContextEdit:  true,
		IsOpenAIFirstParty:             true,
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
	countErr        error
	countSupported  *bool
	supportErr      error

	countCalls   int
	resolveCalls int
}

func (c *preciseCompactionClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, nil
}

func (c *preciseCompactionClient) CountRequestInputTokens(_ context.Context, _ llm.Request) (int, error) {
	c.countCalls++
	if c.countErr != nil {
		return 0, c.countErr
	}
	if c.inputTokenCount < 0 {
		return 0, nil
	}
	return c.inputTokenCount, nil
}

func (c *preciseCompactionClient) SupportsRequestInputTokenCount(_ context.Context) (bool, error) {
	if c.supportErr != nil {
		return false, c.supportErr
	}
	if c.countSupported != nil {
		return *c.countSupported, nil
	}
	return true, nil
}

func (c *preciseCompactionClient) ResolveModelContextWindow(_ context.Context, _ string) (int, error) {
	c.resolveCalls++
	if c.contextWindow <= 0 {
		return 0, nil
	}
	return c.contextWindow, nil
}

func (c *preciseCompactionClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	supportsExactCount := true
	if c.countSupported != nil {
		supportsExactCount = *c.countSupported
	}
	return llm.ProviderCapabilities{
		ProviderID:                     "openai",
		SupportsResponsesAPI:           true,
		SupportsResponsesCompact:       true,
		SupportsRequestInputTokenCount: supportsExactCount,
		SupportsPromptCacheKey:         true,
		SupportsNativeWebSearch:        true,
		SupportsReasoningEncrypted:     true,
		SupportsServerSideContextEdit:  true,
		IsOpenAIFirstParty:             true,
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
			ProviderID:                     "openai",
			SupportsResponsesAPI:           true,
			SupportsResponsesCompact:       true,
			SupportsRequestInputTokenCount: true,
			SupportsPromptCacheKey:         true,
			SupportsNativeWebSearch:        true,
			SupportsReasoningEncrypted:     true,
			SupportsServerSideContextEdit:  true,
			IsOpenAIFirstParty:             true,
		}, nil
	}
	return f.caps, nil
}

type fakeTool struct {
	name  toolspec.ID
	delay time.Duration
}

func (t fakeTool) Name() toolspec.ID { return t.name }
func (t fakeTool) Call(_ context.Context, c tools.Call) (tools.Result, error) {
	time.Sleep(t.delay)
	out, _ := json.Marshal(map[string]any{"tool": string(t.name)})
	return tools.Result{CallID: c.ID, Name: c.Name, Output: out}, nil
}

type failingTool struct {
	name toolspec.ID
}

func (t failingTool) Name() toolspec.ID { return t.name }
func (t failingTool) Call(_ context.Context, c tools.Call) (tools.Result, error) {
	out, _ := json.Marshal(map[string]any{"error": "failed"})
	return tools.Result{CallID: c.ID, Name: c.Name, Output: out, IsError: true}, nil
}

type blockingTool struct {
	name    toolspec.ID
	started chan struct{}
	release chan struct{}
}

func (t blockingTool) Name() toolspec.ID { return t.name }

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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5",
		Temperature:   1,
		ThinkingLevel: "xhigh",
		EnabledTools:  []toolspec.ID{toolspec.ToolExecCommand},
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
	if len(meta.Locked.EnabledTools) != 1 || meta.Locked.EnabledTools[0] != string(toolspec.ToolExecCommand) {
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5",
		Temperature:   1,
		ThinkingLevel: "high",
		EnabledTools:  []toolspec.ID{toolspec.ToolExecCommand},
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
	firstEngine, err := New(store, firstClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5",
		EnabledTools:  []toolspec.ID{toolspec.ToolExecCommand},
		ToolPreambles: false,
	})
	if err != nil {
		t.Fatalf("new first engine: %v", err)
	}
	if _, err := firstEngine.SubmitUserMessage(context.Background(), "first"); err != nil {
		t.Fatalf("submit first: %v", err)
	}
	if store.Meta().Locked == nil || store.Meta().Locked.ToolPreambles == nil || *store.Meta().Locked.ToolPreambles {
		t.Fatalf("expected first session to lock tool_preambles=false, got %+v", store.Meta().Locked)
	}

	resumedClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "second"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	resumedEngine, err := New(store, resumedClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5",
		EnabledTools:  []toolspec.ID{toolspec.ToolExecCommand},
		ToolPreambles: true,
	})
	if err != nil {
		t.Fatalf("new resumed engine: %v", err)
	}
	if _, err := resumedEngine.SubmitUserMessage(context.Background(), "second"); err != nil {
		t.Fatalf("submit second: %v", err)
	}
	if store.Meta().Locked == nil || store.Meta().Locked.ToolPreambles == nil || *store.Meta().Locked.ToolPreambles {
		t.Fatalf("expected resumed session to preserve locked tool_preambles=false, got %+v", store.Meta().Locked)
	}
}

func TestLockedContextWindowKeepsSystemPromptToolCallEstimateStableAcrossResume(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	firstClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "first"},
		Usage:     llm.Usage{WindowTokens: 272_000},
	}}}
	firstEngine, err := New(store, firstClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:               "gpt-5",
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ContextWindowTokens: 272_000,
	})
	if err != nil {
		t.Fatalf("new first engine: %v", err)
	}
	if _, err := firstEngine.SubmitUserMessage(context.Background(), "first"); err != nil {
		t.Fatalf("submit first: %v", err)
	}
	locked := store.Meta().Locked
	if locked == nil || locked.ContextWindow != 272_000 || locked.ContextPercent != 95 {
		t.Fatalf("expected locked context budget, got %+v", locked)
	}
	if got := firstEngine.estimatedToolCallsForLockedContext(*locked); got != 185 {
		t.Fatalf("estimated tool calls = %d, want 185", got)
	}
	firstPrompt := firstClient.calls[0].SystemPrompt
	if strings.TrimSpace(firstPrompt) == "" {
		t.Fatal("expected non-empty rendered system prompt")
	}
	firstPromptCacheKey := firstClient.calls[0].PromptCacheKey
	if firstPromptCacheKey == "" {
		t.Fatal("expected prompt cache key on first request")
	}

	resumedClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "second"},
		Usage:     llm.Usage{WindowTokens: 400_000},
	}}}
	resumedEngine, err := New(store, resumedClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:               "gpt-5",
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ContextWindowTokens: 400_000,
	})
	if err != nil {
		t.Fatalf("new resumed engine: %v", err)
	}
	if _, err := resumedEngine.SubmitUserMessage(context.Background(), "second"); err != nil {
		t.Fatalf("submit second: %v", err)
	}
	if strings.TrimSpace(resumedClient.calls[0].SystemPrompt) == "" {
		t.Fatal("expected resumed system prompt to stay non-empty")
	}
	if resumedClient.calls[0].PromptCacheKey != firstPromptCacheKey {
		t.Fatalf("expected resumed prompt cache key = %q, got %q", firstPromptCacheKey, resumedClient.calls[0].PromptCacheKey)
	}
	if got := resumedEngine.estimatedToolCallsForLockedContext(*store.Meta().Locked); got != 185 {
		t.Fatalf("resumed estimated tool calls = %d, want 185", got)
	}

	alteredLocked := *store.Meta().Locked
	alteredLocked.ContextWindow = 400_000
	if got := resumedEngine.estimatedToolCallsForLockedContext(alteredLocked); got != 271 {
		t.Fatalf("altered estimated tool calls = %d, want 271", got)
	}
	alteredPrompt, err := resumedEngine.systemPrompt(alteredLocked)
	if err != nil {
		t.Fatalf("altered system prompt: %v", err)
	}
	if alteredPrompt != firstPrompt {
		t.Fatal("expected locked system prompt snapshot to stay stable when locked context budget changes")
	}
}

func TestSystemPromptSnapshotUsesLocalFileAndSurvivesMidSessionFileChanges(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	for _, dir := range []string{filepath.Join(home, agentsGlobalDirName), filepath.Join(workspace, agentsGlobalDirName)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	writeTestFile(t, filepath.Join(home, agentsGlobalDirName, systemPromptFileName), "global system")
	localPath := filepath.Join(workspace, agentsGlobalDirName, systemPromptFileName)
	writeTestFile(t, localPath, "local {{.EstimatedToolCallsForContext}} {{.BuilderRunCommand}}")

	store, err := session.Create(t.TempDir(), "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "first"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                "gpt-5",
		EnabledTools:         []toolspec.ID{toolspec.ToolExecCommand},
		ContextWindowTokens:  272_000,
		TranscriptWorkingDir: workspace,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "first"); err != nil {
		t.Fatalf("submit first: %v", err)
	}
	firstPrompt := client.calls[0].SystemPrompt
	if !strings.Contains(firstPrompt, "local 185 ") || strings.Contains(firstPrompt, "global system") || strings.Contains(firstPrompt, "{{") {
		t.Fatalf("unexpected first system prompt: %q", firstPrompt)
	}
	firstCacheKey := client.calls[0].PromptCacheKey
	if firstCacheKey == "" {
		t.Fatal("expected prompt cache key")
	}
	writeTestFile(t, localPath, "changed local system")
	if err := eng.Close(); err != nil {
		t.Fatalf("close first engine: %v", err)
	}

	reopened, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	reopenedClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "second"},
		Usage:     llm.Usage{WindowTokens: 400000},
	}}}
	reopenedEngine, err := New(reopened, reopenedClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                "gpt-5",
		EnabledTools:         []toolspec.ID{toolspec.ToolExecCommand},
		ContextWindowTokens:  400_000,
		TranscriptWorkingDir: workspace,
	})
	if err != nil {
		t.Fatalf("new reopened engine: %v", err)
	}
	if _, err := reopenedEngine.SubmitUserMessage(context.Background(), "second"); err != nil {
		t.Fatalf("submit second: %v", err)
	}
	if got := reopenedClient.calls[0].SystemPrompt; got != firstPrompt {
		t.Fatalf("system prompt changed after SYSTEM.md edit\ngot: %q\nwant: %q", got, firstPrompt)
	}
	if got := reopenedClient.calls[0].PromptCacheKey; got != firstCacheKey {
		t.Fatalf("prompt cache key changed after SYSTEM.md edit: got %q want %q", got, firstCacheKey)
	}
	if got := reopened.Meta().Locked.SystemPrompt; got != firstPrompt {
		t.Fatalf("locked system prompt mismatch\ngot: %q\nwant: %q", got, firstPrompt)
	}
}

func TestReadSystemPromptTemplateUsesGlobalFileWhenLocalMissing(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, agentsGlobalDirName)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global dir: %v", err)
	}
	writeTestFile(t, filepath.Join(globalDir, systemPromptFileName), "global system")

	template, sourcePath, ok, err := readSystemPromptTemplate(systemPromptSnapshotOptions{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("read system prompt template: %v", err)
	}
	if !ok || template != "global system" {
		t.Fatalf("template = %q ok=%t, want global system true", template, ok)
	}
	if want := filepath.Join(globalDir, systemPromptFileName); sourcePath != want {
		t.Fatalf("source path = %q, want %q", sourcePath, want)
	}
}

func TestEnsureLockedWithSystemPromptAndTranscriptWorkingDirDoesNotDeadlock(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	systemDir := filepath.Join(workspace, agentsGlobalDirName)
	if err := os.MkdirAll(systemDir, 0o755); err != nil {
		t.Fatalf("mkdir system dir: %v", err)
	}
	writeTestFile(t, filepath.Join(systemDir, systemPromptFileName), "deadlock guard")

	store, err := session.Create(t.TempDir(), "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                "gpt-5",
		EnabledTools:         []toolspec.ID{toolspec.ToolExecCommand},
		TranscriptWorkingDir: workspace,
		ToolPreambles:        false,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	done := make(chan struct {
		locked session.LockedContract
		err    error
	}, 1)
	go func() {
		locked, err := eng.ensureLocked()
		done <- struct {
			locked session.LockedContract
			err    error
		}{locked: locked, err: err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("ensureLocked: %v", got.err)
		}
		if got.locked.SystemPrompt != "deadlock guard" {
			t.Fatalf("system prompt = %q, want deadlock guard", got.locked.SystemPrompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ensureLocked deadlocked while resolving SYSTEM.md from TranscriptWorkingDir")
	}
}

func TestBuildSystemPromptSnapshotForRootDoesNotUseMutexTakingWorkspaceAccessor(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	systemDir := filepath.Join(workspace, agentsGlobalDirName)
	if err := os.MkdirAll(systemDir, 0o755); err != nil {
		t.Fatalf("mkdir system dir: %v", err)
	}
	writeTestFile(t, filepath.Join(systemDir, systemPromptFileName), "locked helper guard")

	store, err := session.Create(t.TempDir(), "ws", t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5",
		EnabledTools:  []toolspec.ID{toolspec.ToolExecCommand},
		ToolPreambles: false,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	done := make(chan struct {
		prompt string
		err    error
	}, 1)
	eng.mu.Lock()
	go func() {
		prompt, err := eng.buildSystemPromptSnapshotForRoot(session.LockedContract{
			Model:          "gpt-5",
			Temperature:    1,
			ContextWindow:  272_000,
			ContextPercent: 95,
			ToolPreambles: func() *bool {
				enabled := false
				return &enabled
			}(),
		}, workspace)
		done <- struct {
			prompt string
			err    error
		}{prompt: prompt, err: err}
	}()
	select {
	case got := <-done:
		eng.mu.Unlock()
		if got.err != nil {
			t.Fatalf("buildSystemPromptSnapshotForRoot: %v", got.err)
		}
		if got.prompt != "locked helper guard" {
			t.Fatalf("prompt = %q, want locked helper guard", got.prompt)
		}
	case <-time.After(2 * time.Second):
		eng.mu.Unlock()
		t.Fatal("buildSystemPromptSnapshotForRoot called a mutex-taking workspace accessor")
	}
}

func TestSystemPromptSnapshotUsesTranscriptWorkingDirForRetargetedSession(t *testing.T) {
	home := t.TempDir()
	canonical := t.TempDir()
	worktree := t.TempDir()
	t.Setenv("HOME", home)
	for _, dir := range []string{filepath.Join(canonical, agentsGlobalDirName), filepath.Join(worktree, agentsGlobalDirName)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	writeTestFile(t, filepath.Join(canonical, agentsGlobalDirName, systemPromptFileName), "canonical system")
	writeTestFile(t, filepath.Join(worktree, agentsGlobalDirName, systemPromptFileName), "worktree system")

	store, err := session.Create(t.TempDir(), "ws", canonical)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                "gpt-5",
		EnabledTools:         []toolspec.ID{toolspec.ToolExecCommand},
		TranscriptWorkingDir: canonical,
		ToolPreambles:        false,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.SetTranscriptWorkingDir(worktree)
	if _, err := eng.SubmitUserMessage(context.Background(), "hello"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if got := client.calls[0].SystemPrompt; got != "worktree system" {
		t.Fatalf("system prompt = %q, want worktree system", got)
	}
}

func TestLegacyLockedSessionBackfillsSystemPromptSnapshotOnce(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	systemDir := filepath.Join(workspace, agentsGlobalDirName)
	if err := os.MkdirAll(systemDir, 0o755); err != nil {
		t.Fatalf("mkdir system dir: %v", err)
	}
	systemPath := filepath.Join(systemDir, systemPromptFileName)
	writeTestFile(t, systemPath, "stale legacy {{.EstimatedToolCallsForContext}}")

	store, err := session.Create(t.TempDir(), "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.MarkModelDispatchLocked(session.LockedContract{
		Model:          "gpt-5",
		Temperature:    1,
		MaxOutputToken: 0,
		ContextWindow:  272_000,
		ContextPercent: 95,
		ToolPreambles: func() *bool {
			enabled := false
			return &enabled
		}(),
	}); err != nil {
		t.Fatalf("mark locked: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                "gpt-5",
		EnabledTools:         []toolspec.ID{toolspec.ToolExecCommand},
		TranscriptWorkingDir: workspace,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if snapshot := store.Meta().Locked.SystemPrompt; snapshot != "" {
		t.Fatalf("system prompt snapshot before first dispatch = %q, want empty", snapshot)
	}
	writeTestFile(t, systemPath, "legacy {{.EstimatedToolCallsForContext}}")
	if _, err := eng.SubmitUserMessage(context.Background(), "hello"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	snapshot := store.Meta().Locked.SystemPrompt
	if snapshot != "legacy 185" {
		t.Fatalf("system prompt snapshot = %q, want legacy 185", snapshot)
	}
	writeTestFile(t, systemPath, "changed legacy")
	if got := client.calls[0].SystemPrompt; got != snapshot {
		t.Fatalf("request used changed system prompt\ngot: %q\nwant: %q", got, snapshot)
	}
}

func TestLegacyLockedSessionBackfillsContextBudgetOnce(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.MarkModelDispatchLocked(session.LockedContract{
		Model:          "gpt-5",
		Temperature:    1,
		MaxOutputToken: 0,
	}); err != nil {
		t.Fatalf("mark locked: %v", err)
	}

	firstEngine, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:               "gpt-5",
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ContextWindowTokens: 272_000,
	})
	if err != nil {
		t.Fatalf("new first engine: %v", err)
	}
	locked := store.Meta().Locked
	if locked == nil || locked.ContextWindow != 272_000 || locked.ContextPercent != 95 {
		t.Fatalf("expected legacy lock backfilled from first resume config, got %+v", locked)
	}
	if got := firstEngine.estimatedToolCallsForLockedContext(*locked); got != 185 {
		t.Fatalf("first estimated tool calls = %d, want 185", got)
	}

	secondEngine, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:               "gpt-5",
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ContextWindowTokens: 400_000,
	})
	if err != nil {
		t.Fatalf("new second engine: %v", err)
	}
	locked = store.Meta().Locked
	if locked == nil || locked.ContextWindow != 272_000 || locked.ContextPercent != 95 {
		t.Fatalf("expected legacy lock backfill to stay pinned, got %+v", locked)
	}
	if got := secondEngine.estimatedToolCallsForLockedContext(*locked); got != 185 {
		t.Fatalf("second estimated tool calls = %d, want 185", got)
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5",
		Temperature:   1,
		ThinkingLevel: "xhigh",
		EnabledTools:  []toolspec.ID{toolspec.ToolExecCommand},
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
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5.4",
		ThinkingLevel: "high",
		EnabledTools:  []toolspec.ID{toolspec.ToolExecCommand},
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5.3-codex",
		Temperature:   1,
		ThinkingLevel: "high",
		EnabledTools:  []toolspec.ID{toolspec.ToolExecCommand},
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
	eng, err := New(store, &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "azure-openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: false}}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), cfg)
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

	restarted, err := New(store, &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), cfg)
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
	engA, err := New(storeA, &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	engB, err := New(storeB, &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), cfg)
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

	restarted, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), cfg)
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
				ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}},
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
	eng, err := New(store, client, tools.NewRegistry(blockingTool{name: toolspec.ToolExecCommand, started: started, release: release}), Config{
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
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), cfg)
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

	restarted, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), cfg)
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
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
			ToolCalls: []llm.ToolCall{{ID: "call_patch_1", Name: string(toolspec.ToolPatch), Custom: true, CustomInput: "*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"}},
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

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolPatch, delay: 50 * time.Millisecond}), Config{
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
			ToolCalls: []llm.ToolCall{{ID: "call_patch_1", Name: string(toolspec.ToolPatch), Custom: true, CustomInput: "*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"}},
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

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolPatch, delay: 50 * time.Millisecond}), Config{
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

	executions := hostedToolExecutionsFromOutputItems([]llm.ResponseItem{item}, tools.DefinitionsFor([]toolspec.ID{toolspec.ToolWebSearch}))
	if len(executions) != 1 {
		t.Fatal("expected hosted web search execution")
	}
	execution := executions[0]
	if execution.Call.Name != string(toolspec.ToolWebSearch) {
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
	if execution.Result.Name != toolspec.ToolWebSearch {
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

	executions := hostedToolExecutionsFromOutputItems([]llm.ResponseItem{item}, tools.DefinitionsFor([]toolspec.ID{toolspec.ToolWebSearch}))
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

	executions := hostedToolExecutionsFromOutputItems([]llm.ResponseItem{item}, tools.DefinitionsFor([]toolspec.ID{toolspec.ToolWebSearch}))
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5",
		WebSearchMode: "native",
		EnabledTools:  []toolspec.ID{toolspec.ToolWebSearch},
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
		if strings.TrimSpace(name) == string(toolspec.ToolWebSearch) {
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
				{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolViewImage}), Config{
		Model:        "gpt-5.3-codex",
		EnabledTools: []toolspec.ID{toolspec.ToolViewImage},
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
		if strings.TrimSpace(tool.Name) == string(toolspec.ToolViewImage) {
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolViewImage}), Config{
		Model:        "gpt-3.5-turbo",
		EnabledTools: []toolspec.ID{toolspec.ToolViewImage},
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
		if strings.TrimSpace(tool.Name) == string(toolspec.ToolViewImage) {
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolViewImage}), Config{
		Model:        "gpt-5.3-codex-spark",
		EnabledTools: []toolspec.ID{toolspec.ToolViewImage},
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
		if strings.TrimSpace(tool.Name) == string(toolspec.ToolViewImage) {
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolViewImage}), Config{
		Model:             "gpt-4.1-2026-01-15",
		ModelCapabilities: session.LockedModelCapabilities{SupportsVisionInputs: true},
		EnabledTools:      []toolspec.ID{toolspec.ToolViewImage},
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
		if strings.TrimSpace(tool.Name) == string(toolspec.ToolViewImage) {
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5.3-codex"})
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                        "gpt-5.4",
		ProviderCapabilitiesOverride: override,
		EnabledTools:                 []toolspec.ID{toolspec.ToolExecCommand},
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
				{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "claude-3"})
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
			ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}},
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

func TestSubmitUserMessageCommentaryWithToolCallsPublishesCommittedEntryStartMetadata(t *testing.T) {
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
			ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}},
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
		eventsMu sync.Mutex
		events   []Event
	)
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			eventsMu.Lock()
			events = append(events, evt)
			eventsMu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "do the task"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	snapshot := eng.ChatSnapshot()
	assistantEntryIndex := -1
	toolCallEntryIndex := -1
	toolResultEntryIndex := -1
	for idx, entry := range snapshot.Entries {
		if assistantEntryIndex < 0 && entry.Role == "assistant" && entry.Text == "working" {
			assistantEntryIndex = idx
		}
		if toolCallEntryIndex < 0 && entry.Role == "tool_call" && entry.ToolCallID == "call_shell_1" {
			toolCallEntryIndex = idx
		}
		if toolResultEntryIndex < 0 && entry.ToolCallID == "call_shell_1" && (entry.Role == "tool_result_ok" || entry.Role == "tool_result_error") {
			toolResultEntryIndex = idx
		}
	}
	if assistantEntryIndex < 0 || toolCallEntryIndex < 0 || toolResultEntryIndex < 0 {
		t.Fatalf("expected authoritative snapshot to contain commentary assistant + tool call/result, snapshot=%+v", snapshot.Entries)
	}

	eventsMu.Lock()
	eventsSnapshot := append([]Event(nil), events...)
	eventsMu.Unlock()
	assistantIdx := -1
	toolStartIdx := -1
	toolCompleteIdx := -1
	for idx, evt := range eventsSnapshot {
		if evt.Kind == EventAssistantMessage && evt.Message.Content == "working" {
			assistantIdx = idx
		}
		if evt.Kind == EventToolCallStarted && evt.ToolCall != nil && evt.ToolCall.ID == "call_shell_1" {
			toolStartIdx = idx
		}
		if evt.Kind == EventToolCallCompleted && evt.ToolResult != nil && evt.ToolResult.CallID == "call_shell_1" {
			toolCompleteIdx = idx
		}
	}
	if assistantIdx < 0 {
		t.Fatalf("expected commentary assistant event, got %+v", eventsSnapshot)
	}
	if toolStartIdx < 0 {
		t.Fatalf("expected tool_call_started event, got %+v", eventsSnapshot)
	}
	if toolCompleteIdx < 0 {
		t.Fatalf("expected tool_call_completed event, got %+v", eventsSnapshot)
	}
	assistantEvt := eventsSnapshot[assistantIdx]
	if !assistantEvt.CommittedEntryStartSet {
		t.Fatalf("expected commentary assistant event committed start set, got %+v", assistantEvt)
	}
	if got, want := assistantEvt.CommittedEntryStart, assistantEntryIndex; got != want {
		t.Fatalf("commentary assistant committed start = %d, want %d", got, want)
	}
	toolStartEvt := eventsSnapshot[toolStartIdx]
	if !toolStartEvt.CommittedEntryStartSet {
		t.Fatalf("expected tool_call_started committed start set, got %+v", toolStartEvt)
	}
	if got, want := toolStartEvt.CommittedEntryStart, toolCallEntryIndex; got != want {
		t.Fatalf("tool_call_started committed start = %d, want %d", got, want)
	}
	toolCompleteEvt := eventsSnapshot[toolCompleteIdx]
	if !toolCompleteEvt.CommittedEntryStartSet {
		t.Fatalf("expected tool_call_completed committed start set, got %+v", toolCompleteEvt)
	}
	if got, want := toolCompleteEvt.CommittedEntryStart, toolResultEntryIndex; got != want {
		t.Fatalf("tool_call_completed committed start = %d, want %d", got, want)
	}
	if toolStartEvt.CommittedEntryCount < toolStartEvt.CommittedEntryStart+1 {
		t.Fatalf("tool_call_started committed count/start inconsistent: %+v", toolStartEvt)
	}
	if toolCompleteEvt.CommittedEntryCount < toolCompleteEvt.CommittedEntryStart+1 {
		t.Fatalf("tool_call_completed committed count/start inconsistent: %+v", toolCompleteEvt)
	}
	if assistantEvt.CommittedEntryCount < assistantEvt.CommittedEntryStart+1 {
		t.Fatalf("assistant committed count/start inconsistent: %+v", assistantEvt)
	}
	if toolStartIdx <= assistantIdx {
		t.Fatalf("expected tool_call_started after commentary assistant event, assistant_idx=%d tool_idx=%d events=%+v", assistantIdx, toolStartIdx, eventsSnapshot)
	}
	if toolCompleteIdx <= toolStartIdx {
		t.Fatalf("expected tool_call_completed after tool_call_started, start_idx=%d complete_idx=%d events=%+v", toolStartIdx, toolCompleteIdx, eventsSnapshot)
	}
	if assistantEvt.CommittedEntryStart >= toolStartEvt.CommittedEntryStart {
		t.Fatalf("expected commentary assistant before tool call in committed order, assistant=%+v tool=%+v", assistantEvt, toolStartEvt)
	}
	if toolStartEvt.CommittedEntryStart >= toolCompleteEvt.CommittedEntryStart {
		t.Fatalf("expected tool call before tool result in committed order, start=%+v complete=%+v", toolStartEvt, toolCompleteEvt)
	}
}

func TestAutoCompactionStatusEventDoesNotPublishCommittedEntryStart(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{
		compactionResponses: []llm.CompactionResponse{{
			OutputItems: []llm.ResponseItem{
				{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "u1"},
				{Type: llm.ResponseItemTypeCompaction, ID: "cmp_1", EncryptedContent: "enc_1"},
			},
			Usage: llm.Usage{InputTokens: 190000, OutputTokens: 1000, WindowTokens: 200000},
		}},
	}

	var (
		eventsMu sync.Mutex
		events   []Event
	)
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			eventsMu.Lock()
			events = append(events, evt)
			eventsMu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 190000, OutputTokens: 0, WindowTokens: 200000})

	if err := eng.autoCompactIfNeeded(context.Background(), "step-1", compactionModeAuto); err != nil {
		t.Fatalf("auto compact failed: %v", err)
	}

	eventsMu.Lock()
	eventsSnapshot := append([]Event(nil), events...)
	eventsMu.Unlock()
	compactionIdx := -1
	localEntryIdx := -1
	for idx, evt := range eventsSnapshot {
		if evt.Kind == EventCompactionCompleted {
			compactionIdx = idx
		}
		if evt.Kind == EventLocalEntryAdded && evt.LocalEntry != nil && evt.LocalEntry.Role == "compaction_notice" {
			localEntryIdx = idx
		}
	}
	if compactionIdx < 0 {
		t.Fatalf("expected compaction completed event, got %+v", eventsSnapshot)
	}
	if localEntryIdx < 0 {
		t.Fatalf("expected compaction notice local entry event, got %+v", eventsSnapshot)
	}
	compactionEvt := eventsSnapshot[compactionIdx]
	if compactionEvt.CommittedEntryStartSet {
		t.Fatalf("expected compaction status event to stay pre-commit, got %+v", compactionEvt)
	}
	localEntryEvt := eventsSnapshot[localEntryIdx]
	if !localEntryEvt.CommittedEntryStartSet {
		t.Fatalf("expected persisted local entry to publish committed start, got %+v", localEntryEvt)
	}
	if localEntryIdx >= compactionIdx {
		t.Fatalf("expected persisted local entry before compaction status, compaction_idx=%d local_entry_idx=%d events=%+v", compactionIdx, localEntryIdx, eventsSnapshot)
	}
}

func TestReplaceHistoryPublishesProjectedTranscriptEntriesBeforeCompactionNotice(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	var events []Event
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			events = append(events, evt)
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "before compaction"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}

	replacement := llm.ItemsFromMessages([]llm.Message{
		{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeEnvironment, Content: "environment info"},
		{Role: llm.RoleUser, MessageType: llm.MessageTypeCompactionSummary, Content: "condensed summary"},
	})
	if err := eng.replaceHistory("step-1", "local", compactionModeManual, replacement); err != nil {
		t.Fatalf("replace history: %v", err)
	}
	if err := eng.emitCompactionStatus("step-1", EventCompactionCompleted, compactionModeManual, "local", "", 2, 1, ""); err != nil {
		t.Fatalf("emit compaction status: %v", err)
	}

	var projected []Event
	var notice *Event
	for idx := range events {
		evt := events[idx]
		if evt.Kind != EventLocalEntryAdded || evt.LocalEntry == nil {
			continue
		}
		if evt.LocalEntry.Role == "compaction_notice" {
			notice = &events[idx]
			continue
		}
		projected = append(projected, evt)
	}
	if len(projected) != 2 {
		t.Fatalf("expected 2 projected replacement entry events, got %+v", events)
	}
	if projected[0].LocalEntry.Role != string(transcript.EntryRoleDeveloperContext) || projected[0].LocalEntry.Text != "environment info" {
		t.Fatalf("unexpected first projected event: %+v", projected[0])
	}
	if !projected[0].CommittedEntryStartSet || projected[0].CommittedEntryStart != 1 {
		t.Fatalf("unexpected first projected committed start: %+v", projected[0])
	}
	if projected[1].LocalEntry.Role != string(transcript.EntryRoleCompactionSummary) || projected[1].LocalEntry.Text != "condensed summary" {
		t.Fatalf("unexpected second projected event: %+v", projected[1])
	}
	if !projected[1].CommittedEntryStartSet || projected[1].CommittedEntryStart != 2 {
		t.Fatalf("unexpected second projected committed start: %+v", projected[1])
	}
	if notice == nil {
		t.Fatalf("expected compaction notice event, got %+v", events)
	}
	if !notice.CommittedEntryStartSet || notice.CommittedEntryStart != 3 {
		t.Fatalf("unexpected compaction notice committed start: %+v", *notice)
	}
	conversationUpdatedCount := 0
	for _, evt := range events {
		if evt.Kind != EventConversationUpdated || evt.StepID != "step-1" {
			continue
		}
		conversationUpdatedCount++
	}
	if conversationUpdatedCount != 1 {
		t.Fatalf("expected one compaction conversation update, got %+v", events)
	}
}

func TestSubmitUserMessageDoesNotRetainPendingToolStartForHostedExecutions(t *testing.T) {
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
			ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}},
			OutputItems: []llm.ResponseItem{{
				Type: llm.ResponseItemTypeOther,
				Raw:  json.RawMessage(`{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"builder cli"}}`),
			}},
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:        "gpt-5",
		EnabledTools: []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolWebSearch},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "do the task"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if got := len(eng.pendingToolCallStarts); got != 0 {
		t.Fatalf("expected pending tool call starts drained after submit, got %+v", eng.pendingToolCallStarts)
	}
	if _, ok := eng.pendingToolCallStarts["ws_1"]; ok {
		t.Fatalf("did not expect hosted tool call id retained in pending starts: %+v", eng.pendingToolCallStarts)
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
				{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
	}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	noopFinalCount := 0
	for _, persisted := range eng.snapshotMessages() {
		if persisted.Role == llm.RoleAssistant && persisted.Phase == llm.MessagePhaseFinal {
			finalAssistantContents = append(finalAssistantContents, persisted.Content)
		}
		if isNoopFinalAnswer(persisted) {
			noopFinalCount++
		}
	}
	if noopFinalCount != 1 {
		t.Fatalf("noop final count = %d, want 1; messages=%+v", noopFinalCount, eng.snapshotMessages())
	}
	if len(finalAssistantContents) != 1 || finalAssistantContents[0] != reviewerNoopToken {
		t.Fatalf("expected hidden persisted noop final assistant message, got %q", finalAssistantContents)
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
			ToolCalls: []llm.ToolCall{{ID: "call_patch_1", Name: string(toolspec.ToolPatch), Custom: true, CustomInput: "*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"}},
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

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolPatch}), Config{
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
				{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
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

	var (
		eventsMu sync.Mutex
		events   []Event
	)
	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	if requestMessages(reviewerReq)[0].Role != llm.RoleDeveloper || requestMessages(reviewerReq)[0].MessageType != llm.MessageTypeEnvironment {
		t.Fatalf("expected reviewer message[0] to be environment meta developer message, got %+v", requestMessages(reviewerReq)[0])
	}
	agentsIdx := -1
	environmentIdx := -1
	boundaryIdx := -1
	skillsMetaIdx := -1
	for idx, message := range requestMessages(reviewerReq) {
		if message.Role == llm.RoleDeveloper && message.MessageType == llm.MessageTypeAgentsMD && strings.Contains(message.Content, "source: "+globalPath) {
			agentsIdx = idx
		}
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
	if agentsIdx < 0 {
		t.Fatalf("expected reviewer metadata to include AGENTS context, got %+v", requestMessages(reviewerReq))
	}
	if environmentIdx >= boundaryIdx {
		t.Fatalf("expected environment metadata before boundary, env=%d boundary=%d", environmentIdx, boundaryIdx)
	}
	if agentsIdx <= environmentIdx {
		t.Fatalf("expected AGENTS metadata after environment, agents=%d env=%d", agentsIdx, environmentIdx)
	}
	if skillsMetaIdx >= 0 && (skillsMetaIdx <= environmentIdx || skillsMetaIdx >= agentsIdx) {
		t.Fatalf("expected skills metadata between environment and AGENTS when present, skills=%d env=%d agents=%d", skillsMetaIdx, environmentIdx, agentsIdx)
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
		if strings.Contains(message.Content, "Tool result:") && strings.Contains(message.Content, "{\"tool\":\"exec_command\"}") {
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
		if entry.Role == "reviewer_status" && strings.Contains(entry.Text, "Supervisor ran") {
			foundReviewerStatus = true
		}
	}
	if !foundReviewerStatus {
		t.Fatalf("expected reviewer status entry in snapshot, got %+v", snapshot.Entries)
	}

	eventsMu.Lock()
	recordedEvents := append([]Event(nil), events...)
	eventsMu.Unlock()
	originalFinalEventIdx := -1
	reviewerSuggestionsEventIdx := -1
	reviewerStatusEventIdx := -1
	for idx, evt := range recordedEvents {
		if evt.Kind == EventAssistantMessage && evt.Message.Content == "original final" {
			originalFinalEventIdx = idx
		}
		if evt.Kind == EventLocalEntryAdded && evt.LocalEntry != nil && evt.LocalEntry.Role == "reviewer_suggestions" {
			reviewerSuggestionsEventIdx = idx
		}
		if evt.Kind == EventLocalEntryAdded && evt.LocalEntry != nil && evt.LocalEntry.Role == "reviewer_status" {
			reviewerStatusEventIdx = idx
		}
	}
	if originalFinalEventIdx < 0 {
		t.Fatalf("expected original final assistant event before reviewer events, got %+v", recordedEvents)
	}
	if reviewerSuggestionsEventIdx < 0 {
		t.Fatalf("expected reviewer suggestions local entry event, got %+v", recordedEvents)
	}
	if reviewerStatusEventIdx < 0 {
		t.Fatalf("expected reviewer status local entry event, got %+v", recordedEvents)
	}
	if originalFinalEventIdx > reviewerSuggestionsEventIdx || reviewerSuggestionsEventIdx > reviewerStatusEventIdx {
		t.Fatalf("expected original final -> reviewer suggestions -> reviewer status event order, got %+v", recordedEvents)
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
				{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
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

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
				{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
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

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
				{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
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

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
				{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
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

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	originalFinalEventIdx := -1
	reviewerSuggestionsEventIdx := -1
	assistantEventIdx := -1
	reviewerStatusIdx := -1
	reviewerEventIdx := -1
	for idx, evt := range deferredEvents {
		if evt.Kind == EventAssistantMessage && evt.Message.Content == "original final" {
			originalFinalEventIdx = idx
		}
		if evt.Kind == EventLocalEntryAdded && evt.LocalEntry != nil && evt.LocalEntry.Role == "reviewer_suggestions" {
			reviewerSuggestionsEventIdx = idx
		}
		if evt.Kind == EventAssistantMessage && evt.Message.Content == "updated final after review" {
			assistantEventIdx = idx
		}
		if evt.Kind == EventLocalEntryAdded && evt.LocalEntry != nil && evt.LocalEntry.Role == "reviewer_status" && strings.Contains(evt.LocalEntry.Text, "applied") {
			reviewerStatusIdx = idx
		}
		if evt.Kind == EventReviewerCompleted && evt.Reviewer != nil && evt.Reviewer.Outcome == "applied" {
			reviewerEventIdx = idx
			if evt.CommittedTranscriptChanged {
				t.Fatalf("expected reviewer completion to avoid committed transcript advancement, got %+v", evt)
			}
		}
	}
	if assistantEventIdx < 0 {
		t.Fatalf("expected follow-up assistant event, got %+v", deferredEvents)
	}
	if originalFinalEventIdx < 0 {
		t.Fatalf("expected original final assistant event before reviewer follow-up, got %+v", deferredEvents)
	}
	if reviewerSuggestionsEventIdx < 0 {
		t.Fatalf("expected reviewer suggestions local entry event before reviewer follow-up, got %+v", deferredEvents)
	}
	if reviewerStatusIdx < 0 {
		t.Fatalf("expected reviewer status local entry event, got %+v", deferredEvents)
	}
	if reviewerEventIdx < 0 {
		t.Fatalf("expected reviewer completed event, got %+v", deferredEvents)
	}
	if originalFinalEventIdx > reviewerSuggestionsEventIdx || reviewerSuggestionsEventIdx > assistantEventIdx || assistantEventIdx > reviewerStatusIdx || reviewerStatusIdx > reviewerEventIdx {
		t.Fatalf("expected original final -> reviewer suggestions -> updated final -> reviewer_status -> reviewer_completed event order, got %+v", deferredEvents)
	}

	restored, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

func TestReviewerCompletedEventReflectsPersistedReviewerStatusStateWithoutTranscriptAdvance(t *testing.T) {
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
		assistantEvent             *Event
		reviewerCompletedEvent     *Event
		snapshotAtReviewerComplete ChatSnapshot
		eng                        *Engine
	)
	eng, err = New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			if evt.Kind == EventAssistantMessage && evt.Message.Content == "updated final after review" {
				eventsMu.Lock()
				captured := evt
				assistantEvent = &captured
				eventsMu.Unlock()
				return
			}
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
	assistant := assistantEvent
	completed := reviewerCompletedEvent
	snapshotAtCompletion := snapshotAtReviewerComplete
	eventsMu.Unlock()
	if assistant == nil {
		t.Fatal("expected follow-up assistant event")
	}
	if completed == nil {
		t.Fatal("expected reviewer completed event")
	}
	if completed.CommittedTranscriptChanged {
		t.Fatalf("expected reviewer completed event to avoid committed transcript advancement, got %+v", *completed)
	}
	if len(snapshotAtCompletion.Entries) < 2 {
		t.Fatalf("expected follow-up assistant and reviewer status in completion snapshot, got %+v", snapshotAtCompletion.Entries)
	}
	assistantEntry := snapshotAtCompletion.Entries[len(snapshotAtCompletion.Entries)-2]
	if assistantEntry.Role != "assistant" || assistantEntry.Text != "updated final after review" {
		t.Fatalf("expected completion snapshot penultimate entry to be follow-up assistant, got %+v", assistantEntry)
	}
	if !assistant.CommittedEntryStartSet {
		t.Fatalf("expected follow-up assistant event committed start metadata, got %+v", *assistant)
	}
	if got, want := assistant.CommittedEntryStart, len(snapshotAtCompletion.Entries)-2; got != want {
		t.Fatalf("follow-up assistant committed start = %d, want %d; snapshot=%+v", got, want, snapshotAtCompletion.Entries)
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

func TestAppendPersistedLocalEntryEmitsRealtimeLocalEntryEvent(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	var events []Event
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			events = append(events, evt)
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if err := eng.appendPersistedLocalEntryWithOngoingText("step-1", "reviewer_suggestions", "Supervisor suggested:\n1. Add verification notes.", "Supervisor made 1 suggestion."); err != nil {
		t.Fatalf("append persisted local entry: %v", err)
	}
	if got := len(events); got != 1 {
		t.Fatalf("event count = %d, want 1", got)
	}
	if got := events[0].Kind; got != EventLocalEntryAdded {
		t.Fatalf("first event kind = %q, want %q", got, EventLocalEntryAdded)
	}
	if events[0].LocalEntry == nil {
		t.Fatal("expected local entry payload on realtime local entry event")
	}
	if got := events[0].LocalEntry.Role; got != "reviewer_suggestions" {
		t.Fatalf("local entry role = %q, want reviewer_suggestions", got)
	}
	if got := events[0].LocalEntry.OngoingText; got != "Supervisor made 1 suggestion." {
		t.Fatalf("local entry ongoing text = %q, want supervisor summary", got)
	}
}

func TestRunReviewerFollowUpReturnsCompletionWhenReviewerInstructionAppendFails(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:    "gpt-5",
		Reviewer: ReviewerConfig{Model: "gpt-5"},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendUserMessage("prep-1", "first request"); err != nil {
		t.Fatalf("append first message: %v", err)
	}

	reviewerClient := &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai-compatible", SupportsResponsesAPI: true}, responses: []llm.Response{{
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

	result, err := eng.runReviewerFollowUp(context.Background(), "step-1", llm.Message{Role: llm.RoleAssistant, Phase: llm.MessagePhaseFinal, Content: "original final"}, -1, false, reviewerClient)
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

func TestRunStepLoopFailsWhenReviewerStatusPersistenceFailsAfterReviewerInstructionAppendFailure(t *testing.T) {
	reviewerInstructionErr := errors.New("injected reviewer instruction persistence failure")
	localEntryErr := errors.New("injected reviewer status persistence failure")
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	mainClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "original final", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	reviewerClient := &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai-compatible", SupportsResponsesAPI: true}, responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":["Add final verification notes."]}`},
		Usage:     llm.Usage{InputTokens: 10, WindowTokens: 200000},
	}}}

	var (
		eventsMu sync.Mutex
		events   []Event
	)
	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng.beforePersistMessage = func(msg llm.Message) error {
		if msg.MessageType == llm.MessageTypeReviewerFeedback {
			return reviewerInstructionErr
		}
		return nil
	}
	eng.beforePersistLocalEntry = func(entry storedLocalEntry) error {
		if entry.Role == "reviewer_status" {
			return localEntryErr
		}
		return nil
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "do task"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	_, err = eng.runStepLoop(context.Background(), "step-1")
	if err == nil {
		t.Fatal("expected runStepLoop to fail when reviewer status persistence fails")
	}

	eventsMu.Lock()
	deferredEvents := append([]Event(nil), events...)
	eventsMu.Unlock()
	assistantEventIdx := -1
	for idx, evt := range deferredEvents {
		if evt.Kind == EventAssistantMessage && evt.Message.Content == "original final" {
			assistantEventIdx = idx
		}
		if evt.Kind == EventReviewerCompleted {
			t.Fatalf("did not expect reviewer completed event after reviewer status persistence failure, got %+v", deferredEvents)
		}
	}
	if assistantEventIdx < 0 {
		t.Fatalf("expected assistant message event, got %+v", deferredEvents)
	}
	if !errors.Is(err, localEntryErr) {
		t.Fatalf("expected injected reviewer status failure, got %v", err)
	}

	snapshot := eng.ChatSnapshot()
	if len(snapshot.Entries) != 2 {
		t.Fatalf("expected append failure to leave transcript at persisted assistant entries only, got %+v", snapshot.Entries)
	}
	for _, entry := range snapshot.Entries {
		if entry.Role == "reviewer_status" {
			t.Fatalf("did not expect in-memory reviewer status after append failure, got %+v", snapshot.Entries)
		}
	}
}

func TestSubmitUserMessageFailsWhenReviewerStatusPersistenceFailsAfterAssistantEvent(t *testing.T) {
	localEntryErr := errors.New("injected reviewer status persistence failure")
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
		eventsMu sync.Mutex
		events   []Event
	)
	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng.beforePersistLocalEntry = func(entry storedLocalEntry) error {
		if entry.Role == "reviewer_status" {
			return localEntryErr
		}
		return nil
	}

	_, err = eng.SubmitUserMessage(context.Background(), "do task")
	if err == nil {
		t.Fatal("expected submit to fail when reviewer status persistence fails")
	}

	eventsMu.Lock()
	deferredEvents := append([]Event(nil), events...)
	eventsMu.Unlock()
	for _, evt := range deferredEvents {
		if evt.Kind == EventReviewerCompleted {
			t.Fatalf("did not expect reviewer completed event after reviewer status persistence failure, got %+v", deferredEvents)
		}
	}
	if !errors.Is(err, localEntryErr) {
		t.Fatalf("expected injected reviewer status failure, got %v", err)
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

	restored, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

func TestAppendPersistedLocalEntryRecordDoesNotMutateChatOnAppendFailure(t *testing.T) {
	localEntryErr := errors.New("injected local entry persistence failure")
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.beforePersistLocalEntry = func(entry storedLocalEntry) error {
		return localEntryErr
	}

	err = eng.appendPersistedLocalEntryRecord("step-1", storedLocalEntry{
		Visibility: transcript.EntryVisibilityAll,
		Role:       "reviewer_status",
		Text:       "Supervisor ran, applied 1 suggestion.",
	})
	if !errors.Is(err, localEntryErr) {
		t.Fatalf("expected injected local entry failure, got %v", err)
	}
	if snapshot := eng.ChatSnapshot(); len(snapshot.Entries) != 0 {
		t.Fatalf("expected no in-memory local entries after append failure, got %+v", snapshot.Entries)
	}
}

func TestAppendLocalEntryWithOngoingTextSkipsBlankEntries(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	var events []Event
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:   "gpt-5",
		OnEvent: func(evt Event) { events = append(events, evt) },
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	eng.AppendLocalEntryWithOngoingText("user", "   ", "ignored")
	if len(events) != 0 {
		t.Fatalf("expected blank local entry to emit no events, got %+v", events)
	}
	if snapshot := eng.ChatSnapshot(); len(snapshot.Entries) != 0 {
		t.Fatalf("expected blank local entry to skip chat append, got %+v", snapshot.Entries)
	}
}

func TestRestoreMessagesKeepsStoredToolCallPresentationPayload(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	presentation := toolcodec.EncodeToolCallMeta(transcript.ToolCallMeta{
		ToolName:       string(toolspec.ToolExecCommand),
		Presentation:   transcript.ToolPresentationShell,
		RenderBehavior: transcript.ToolCallRenderBehaviorShell,
		IsShell:        true,
		Command:        "pwd",
		TimeoutLabel:   "",
	})
	if _, err := store.AppendEvent("legacy-step", "message", llm.Message{
		Role:    llm.RoleAssistant,
		Content: "working",
		ToolCalls: []llm.ToolCall{{
			ID:           "call_1",
			Name:         string(toolspec.ToolExecCommand),
			Input:        json.RawMessage(`{"command":"pwd"}`),
			Presentation: presentation,
		}},
	}); err != nil {
		t.Fatalf("append assistant tool call message: %v", err)
	}

	restored, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
	if toolEntry.ToolCall.TimeoutLabel != "" {
		t.Fatalf("expected restored timeout label, got %+v", toolEntry.ToolCall)
	}
}

func TestRestoreMessagesIgnoresLegacyReviewerRollbackHistoryReplacement(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	presentation := toolcodec.EncodeToolCallMeta(transcript.ToolCallMeta{
		ToolName:       string(toolspec.ToolExecCommand),
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
			Name:             string(toolspec.ToolExecCommand),
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
		restored, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
		t.Fatal("restore engine timed out while ignoring legacy reviewer_rollback history replacement")
	}
	items := restored.snapshotItems()
	if len(items) != 0 {
		t.Fatalf("expected legacy reviewer rollback replacement to be ignored, got %+v", items)
	}
	snapshot := restored.ChatSnapshot()
	if len(snapshot.Entries) != 0 {
		t.Fatalf("expected ignored legacy reviewer rollback to produce no transcript entries, got %+v", snapshot.Entries)
	}
}

func TestRestoreMessagesFailsOnMalformedHistoryReplacementPayload(t *testing.T) {
	t.Run("non-legacy payload still fails", func(t *testing.T) {
		dir := t.TempDir()
		store, err := session.Create(dir, "ws", dir)
		if err != nil {
			t.Fatalf("create store: %v", err)
		}
		if _, err := store.AppendReplayEvents([]session.ReplayEvent{{
			StepID:  "legacy-step",
			Kind:    "history_replaced",
			Payload: json.RawMessage(`{"engine":"local","items":"not-an-array"}`),
		}}); err != nil {
			t.Fatalf("append malformed replay event: %v", err)
		}

		if _, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"}); err == nil || !strings.Contains(err.Error(), "decode history_replaced event") {
			t.Fatalf("expected malformed history replacement decode error, got %v", err)
		}
	})

	t.Run("legacy reviewer rollback payload is ignored", func(t *testing.T) {
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

		if _, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"}); err != nil {
			t.Fatalf("expected malformed legacy reviewer rollback payload to be ignored, got %v", err)
		}
	})
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

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

	restored, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
	suggestions := parseReviewerSuggestionsObject(`{"suggestions":["one"," two ","one"," ","NO_OP","no_op"]}`)
	if len(suggestions) != 3 || suggestions[0] != "one" || suggestions[1] != "two" || suggestions[2] != "one" {
		t.Fatalf("unexpected suggestions from object payload: %+v", suggestions)
	}

	suggestions = parseReviewerSuggestionsObject(`[" ","NO_OP"]`)
	if len(suggestions) != 0 {
		t.Fatalf("expected invalid non-object payload to be ignored, got %+v", suggestions)
	}

	suggestions = parseReviewerSuggestionsObject("")
	if len(suggestions) != 0 {
		t.Fatalf("expected empty payload to be ignored, got %+v", suggestions)
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
		{Role: llm.RoleAssistant, Content: "Running command now.", Phase: llm.MessagePhaseCommentary, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "exec_command", Input: json.RawMessage(`{"command":"pwd"}`)}}},
		{Role: llm.RoleAssistant, Content: "assistant response", Phase: llm.MessagePhaseFinal},
		{Role: llm.RoleTool, Name: "exec_command", ToolCallID: "call_1", Content: "{\"output\":\"ok\"}"},
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
		{Role: llm.RoleTool, Name: "exec_command", ToolCallID: "orphan_call", Content: "{\"output\":\"orphan\"}"},
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
	if !strings.Contains(reviewerMessages[0].Content, "Developer context:") {
		t.Fatalf("expected developer-context label in reviewer message, got %q", reviewerMessages[0].Content)
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
	if got[0].Role != llm.RoleDeveloper || got[0].MessageType != llm.MessageTypeEnvironment || !strings.Contains(got[0].Content, environmentInjectedHeader) {
		t.Fatalf("expected prepended environment developer message first, got %+v", got[0])
	}
	if got[1].Role != llm.RoleDeveloper || got[1].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(got[1].Content, "source: "+globalPath) {
		t.Fatalf("expected global AGENTS developer message after environment, got %+v", got[1])
	}
	if got[2].Role != llm.RoleDeveloper || got[2].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(got[2].Content, "source: "+workspacePath) {
		t.Fatalf("expected workspace AGENTS developer message last in base context, got %+v", got[2])
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
	if got[0].MessageType != llm.MessageTypeEnvironment {
		t.Fatalf("expected environment metadata first, got %+v", got[0])
	}
	if got[1].MessageType != llm.MessageTypeSkills {
		t.Fatalf("expected skills metadata after environment, got %+v", got[1])
	}
	if got[2].MessageType != llm.MessageTypeAgentsMD || got[3].MessageType != llm.MessageTypeAgentsMD {
		t.Fatalf("expected AGENTS metadata after environment+skills, got %+v %+v", got[2], got[3])
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
	if got[0].MessageType != llm.MessageTypeEnvironment {
		t.Fatalf("expected environment metadata first when already present, got %+v", got[0])
	}
	if got[1].MessageType != llm.MessageTypeSkills {
		t.Fatalf("expected skills metadata after environment, got %+v", got[1])
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
	if got[0].MessageType != llm.MessageTypeEnvironment {
		t.Fatalf("expected environment first, got %+v", got[0])
	}
	if got[1].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(got[1].Content, "source: "+globalPath) {
		t.Fatalf("expected global AGENTS after environment, got %+v", got[1])
	}
	if got[2].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(got[2].Content, "source: "+workspacePath) {
		t.Fatalf("expected missing workspace AGENTS to be backfilled last in base context, got %+v", got[2])
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
	if got[0].MessageType != llm.MessageTypeEnvironment || !strings.Contains(got[0].Content, environmentInjectedHeader) {
		t.Fatalf("expected live environment metadata first, got %+v", got[0])
	}
	if got[1].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(got[1].Content, "source: "+globalPath) {
		t.Fatalf("expected live global AGENTS after environment, got %+v", got[1])
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
				Name:  string(toolspec.ToolExecCommand),
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
	time.Sleep(50 * time.Millisecond)
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
			ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}},
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
	eng, err := New(store, client, tools.NewRegistry(blockingTool{name: toolspec.ToolExecCommand, started: started, release: release}), Config{
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
	time.Sleep(50 * time.Millisecond)
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
			ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}},
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
	eng, err := New(store, mainClient, tools.NewRegistry(blockingTool{name: toolspec.ToolExecCommand, started: started, release: release}), Config{
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
	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	snapshot := eng.ChatSnapshot()

	mu.Lock()
	defer mu.Unlock()
	assistantMessages := 0
	flushedQueuedUser := false
	assistantCommittedStart := -1
	assistantCommittedStartSet := false
	for i, evt := range events {
		_ = i
		if evt.Kind == EventAssistantMessage {
			assistantMessages++
			if evt.Message.Content != "foreground done" {
				t.Fatalf("assistant message content = %q, want foreground done", evt.Message.Content)
			}
			assistantCommittedStart = evt.CommittedEntryStart
			assistantCommittedStartSet = evt.CommittedEntryStartSet
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
	if !assistantCommittedStartSet {
		t.Fatalf("expected deferred final assistant event committed start metadata, got %+v", events)
	}
	if assistantCommittedStart < 0 || assistantCommittedStart >= len(snapshot.Entries) {
		t.Fatalf("deferred final assistant committed start = %d, snapshot=%+v", assistantCommittedStart, snapshot.Entries)
	}
	assistantEntry := snapshot.Entries[assistantCommittedStart]
	if assistantEntry.Role != "assistant" || assistantEntry.Text != "foreground done" {
		t.Fatalf("expected deferred final assistant event to point at committed assistant row, got %+v", assistantEntry)
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
	eng, err := New(store, mainClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	snapshot := eng.ChatSnapshot()

	mu.Lock()
	defer mu.Unlock()
	assistantMessages := 0
	assistantCommittedStart := -1
	assistantCommittedStartSet := false
	for _, evt := range events {
		if evt.Kind != EventAssistantMessage {
			continue
		}
		assistantMessages++
		if evt.Message.Content != "foreground done" {
			t.Fatalf("assistant message content = %q, want foreground done", evt.Message.Content)
		}
		assistantCommittedStart = evt.CommittedEntryStart
		assistantCommittedStartSet = evt.CommittedEntryStartSet
	}
	if assistantMessages != 1 {
		t.Fatalf("expected one assistant_message event for deferred final, got %d events=%+v", assistantMessages, events)
	}
	if !assistantCommittedStartSet {
		t.Fatalf("expected deferred final assistant event committed start metadata, got %+v", events)
	}
	if assistantCommittedStart < 0 || assistantCommittedStart >= len(snapshot.Entries) {
		t.Fatalf("deferred final assistant committed start = %d, snapshot=%+v", assistantCommittedStart, snapshot.Entries)
	}
	assistantEntry := snapshot.Entries[assistantCommittedStart]
	if assistantEntry.Role != "assistant" || assistantEntry.Text != "foreground done" {
		t.Fatalf("expected deferred final assistant event to point at committed assistant row, got %+v", assistantEntry)
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
			ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}},
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
	eng, err := New(store, client, tools.NewRegistry(blockingTool{name: toolspec.ToolExecCommand, started: started, release: release}), Config{
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
	time.Sleep(50 * time.Millisecond)
	client.mu.Lock()
	callCountAfterReturn := len(client.calls)
	client.mu.Unlock()
	if callCountAfterReturn != 2 {
		t.Fatalf("did not expect a later batched continuation after turn completion, got %d calls", callCountAfterReturn)
	}

	finalAssistantContents := make([]string, 0)
	foundBackgroundNotice := false
	noopFinalCount := 0
	for _, persisted := range eng.snapshotMessages() {
		if persisted.Role == llm.RoleAssistant && persisted.Phase == llm.MessagePhaseFinal {
			finalAssistantContents = append(finalAssistantContents, persisted.Content)
		}
		if persisted.Role == llm.RoleDeveloper && persisted.MessageType == llm.MessageTypeBackgroundNotice && strings.Contains(persisted.Content, "Background shell 1000 completed.") {
			foundBackgroundNotice = true
		}
		if isNoopFinalAnswer(persisted) {
			noopFinalCount++
		}
	}
	if !foundBackgroundNotice {
		t.Fatalf("expected persisted background notice, got %+v", eng.snapshotMessages())
	}
	if noopFinalCount != 1 {
		t.Fatalf("noop final count = %d, want 1; messages=%+v", noopFinalCount, eng.snapshotMessages())
	}
	if len(finalAssistantContents) != 1 || finalAssistantContents[0] != reviewerNoopToken {
		t.Fatalf("expected hidden persisted noop final assistant message, got %q", finalAssistantContents)
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
			ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	started := make(chan struct{})
	release := make(chan struct{})
	eng, err := New(store, client, tools.NewRegistry(blockingTool{name: toolspec.ToolExecCommand, started: started, release: release}), Config{Model: "gpt-5"})
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

	time.Sleep(50 * time.Millisecond)
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
				Name:  string(toolspec.ToolExecCommand),
				Input: json.RawMessage(`{"cmd":"sleep 0.3; echo done","shell":"/bin/sh","login":false,"yield_time_ms":250}`),
			}},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "wait for it", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{
				ID:    "call_poll_1",
				Name:  string(toolspec.ToolWriteStdin),
				Input: json.RawMessage(`{"session_id":1000,"yield_time_ms":800}`),
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
	time.Sleep(50 * time.Millisecond)

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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	restored, err := New(reopenedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
		Name:  string(toolspec.ToolAskQuestion),
		Input: json.RawMessage(`{"question":"Choose scope?","suggestions":["full","fast"],"recommended_option_index":1}`),
		Presentation: toolcodec.EncodeToolCallMeta(transcript.ToolCallMeta{
			ToolName:               string(toolspec.ToolAskQuestion),
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
		Name:  string(toolspec.ToolExecCommand),
		Input: json.RawMessage(`{"command":"pwd"}`),
		Presentation: toolcodec.EncodeToolCallMeta(transcript.ToolCallMeta{
			ToolName:       string(toolspec.ToolExecCommand),
			Presentation:   transcript.ToolPresentationShell,
			RenderBehavior: transcript.ToolCallRenderBehaviorShell,
			IsShell:        true,
			Command:        "pwd",
			TimeoutLabel:   "",
		}),
	})
}

func TestReopenCarriesInterruptedApprovalBackedPatchToolAttemptIntoNextModelRequest(t *testing.T) {
	testReopenCarriesInterruptedToolAttemptIntoNextModelRequest(t, llm.ToolCall{
		ID:          "call_patch",
		Name:        string(toolspec.ToolPatch),
		Custom:      true,
		CustomInput: "*** Begin Patch\n*** Add File: ../outside.txt\n+hello\n*** End Patch\n",
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
		case item.Type == llm.ResponseItemTypeCustomToolCall && item.CallID == call.ID && item.Name == call.Name:
			foundPriorAttempt = true
		case item.Type == llm.ResponseItemTypeFunctionCallOutput && item.CallID == call.ID:
			foundUnexpectedReply = true
		case item.Type == llm.ResponseItemTypeCustomToolOutput && item.CallID == call.ID:
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	result, err := eng.SubmitUserShellCommand(context.Background(), "pwd")
	if err != nil {
		t.Fatalf("submit user shell command: %v", err)
	}
	if result.Name != toolspec.ToolExecCommand {
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
			if len(msg.ToolCalls) == 1 && msg.ToolCalls[0].Name == string(toolspec.ToolExecCommand) {
				foundAssistantToolCall = true
			}
		case llm.RoleTool:
			if msg.Name == string(toolspec.ToolExecCommand) && strings.TrimSpace(msg.Content) != "" {
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
	if result.Name != toolspec.ToolExecCommand || !result.IsError {
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
		if msg.Name != string(toolspec.ToolExecCommand) {
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
				{ID: "a", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{}`)},
				{ID: "b", Name: string(toolspec.ToolPatch), Input: json.RawMessage(`{}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	eng, err := New(store, client, tools.NewRegistry(
		fakeTool{name: toolspec.ToolExecCommand, delay: 40 * time.Millisecond},
		fakeTool{name: toolspec.ToolPatch, delay: 1 * time.Millisecond},
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
				{ID: "a", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{}`)},
				{ID: "b", Name: string(toolspec.ToolPatch), Input: json.RawMessage(`{}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	slow := blockingTool{name: toolspec.ToolExecCommand, started: make(chan struct{}), release: make(chan struct{})}
	toolCompleted := make(chan tools.Result, 4)
	eng, err := New(store, client, tools.NewRegistry(
		slow,
		fakeTool{name: toolspec.ToolPatch, delay: 1 * time.Millisecond},
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
				{ID: "a", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
			},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
			callName: string(toolspec.ToolExecCommand),
		},
		{
			name:     "registered tool handler",
			registry: tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}),
			callName: string(toolspec.ToolExecCommand),
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
		Name:  string(toolspec.ToolWebSearch),
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

func TestCriticalExactRecountsAfterToolCompletionBeforeToolMessageAppend(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{inputTokenCountFn: func(req llm.Request) int {
		for _, item := range req.Items {
			if item.Type == llm.ResponseItemTypeFunctionCallOutput && item.CallID == "call-1" {
				return 200
			}
		}
		return 100
	}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 400_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	call := llm.ToolCall{ID: "call-1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}
	if err := eng.appendAssistantMessage("step", llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}); err != nil {
		t.Fatalf("append assistant tool call: %v", err)
	}
	if precise, ok := eng.currentInputTokensPrecisely(context.Background()); !ok || precise != 100 {
		t.Fatalf("initial exact count = (%d, %v), want (100, true)", precise, ok)
	}
	if client.countInputTokenCalls != 1 {
		t.Fatalf("count calls=%d, want 1", client.countInputTokenCalls)
	}
	results, err := eng.executeToolCalls(context.Background(), "step", []llm.ToolCall{call})
	if err != nil {
		t.Fatalf("execute tool calls: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one tool result, got %d", len(results))
	}
	req, err := eng.buildRequest(context.Background(), "", true)
	if err != nil {
		t.Fatalf("build request after tool completion: %v", err)
	}
	foundOutput := false
	for _, item := range req.Items {
		if item.Type == llm.ResponseItemTypeFunctionCallOutput && item.CallID == call.ID {
			foundOutput = true
			break
		}
	}
	if !foundOutput {
		t.Fatalf("expected synthesized function_call_output before tool message append, items=%+v", req.Items)
	}
	if precise, ok := eng.currentInputTokensPreciselyIfCritical(context.Background(), 1_000); !ok || precise != 200 {
		t.Fatalf("critical exact recount = (%d, %v), want (200, true)", precise, ok)
	}
	if client.countInputTokenCalls != 2 {
		t.Fatalf("expected critical recount after tool completion, got %d count calls", client.countInputTokenCalls)
	}
}

func TestCustomToolResultPersistsAsCustomToolCallOutput(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	patchInput := "*** Begin Patch\n*** Add File: a.txt\n+hi\n*** End Patch\n"
	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "patching", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{
				ID:          "call_patch",
				Name:        string(toolspec.ToolPatch),
				Custom:      true,
				CustomInput: patchInput,
				Input:       json.RawMessage(`{}`),
			}},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolPatch}), Config{Model: "gpt-5", EnabledTools: []toolspec.ID{toolspec.ToolPatch}})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	msg, err := eng.SubmitUserMessage(context.Background(), "apply patch")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("unexpected final message: %+v", msg)
	}
	if len(client.calls) < 2 {
		t.Fatalf("expected follow-up request after tool result, got %d", len(client.calls))
	}

	foundCustomCall := false
	foundCustomOutput := false
	foundFunctionOutput := false
	for _, item := range client.calls[1].Items {
		switch {
		case item.Type == llm.ResponseItemTypeCustomToolCall && item.CallID == "call_patch":
			foundCustomCall = true
		case item.Type == llm.ResponseItemTypeCustomToolOutput && item.CallID == "call_patch":
			foundCustomOutput = true
		case item.Type == llm.ResponseItemTypeFunctionCallOutput && item.CallID == "call_patch":
			foundFunctionOutput = true
		}
	}
	if !foundCustomCall || !foundCustomOutput || foundFunctionOutput {
		t.Fatalf("expected custom call/output pair only, foundCustomCall=%v foundCustomOutput=%v foundFunctionOutput=%v items=%+v", foundCustomCall, foundCustomOutput, foundFunctionOutput, client.calls[1].Items)
	}
}

func TestRequestToolsExposePatchAsCustomToolOnlyForFirstPartyResponsesProvider(t *testing.T) {
	tests := []struct {
		name       string
		caps       llm.ProviderCapabilities
		wantCustom bool
	}{
		{
			name:       "first party OpenAI",
			caps:       llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true},
			wantCustom: true,
		},
		{
			name:       "OpenAI compatible fallback",
			caps:       llm.ProviderCapabilities{ProviderID: "openai-compatible", SupportsResponsesAPI: true, IsOpenAIFirstParty: false},
			wantCustom: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := session.Create(dir, "ws", dir)
			if err != nil {
				t.Fatalf("create store: %v", err)
			}
			client := &fakeClient{caps: tt.caps}
			eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolPatch}), Config{Model: "gpt-5", EnabledTools: []toolspec.ID{toolspec.ToolPatch}})
			if err != nil {
				t.Fatalf("new engine: %v", err)
			}
			if _, err := eng.ensureLocked(); err != nil {
				t.Fatalf("ensureLocked: %v", err)
			}

			requestTools := eng.requestTools(context.Background())
			if len(requestTools) != 1 {
				t.Fatalf("request tools = %+v, want one patch tool", requestTools)
			}
			gotCustom := requestTools[0].Custom != nil
			if gotCustom != tt.wantCustom {
				t.Fatalf("patch custom tool = %v, want %v; tool=%+v", gotCustom, tt.wantCustom, requestTools[0])
			}
			if !tt.wantCustom && len(requestTools[0].Schema) == 0 {
				t.Fatalf("expected function-tool schema fallback for unsupported custom tools, got %+v", requestTools[0])
			}
		})
	}
}

func TestRequestToolsUseActiveProviderCapsForCustomPatchTool(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.MarkModelDispatchLocked(session.LockedContract{
		Model:        "gpt-5",
		EnabledTools: []string{string(toolspec.ToolPatch)},
		ProviderContract: llm.LockedProviderCapabilitiesFromContract(llm.ProviderCapabilities{
			ProviderID:           "openai",
			SupportsResponsesAPI: true,
			IsOpenAIFirstParty:   true,
		}),
	}); err != nil {
		t.Fatalf("mark locked: %v", err)
	}
	activeCaps := llm.ProviderCapabilities{ProviderID: "openai-compatible", SupportsResponsesAPI: true, IsOpenAIFirstParty: false}
	client := &fakeClient{caps: activeCaps}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolPatch}), Config{
		Model:                        "gpt-5",
		EnabledTools:                 []toolspec.ID{toolspec.ToolPatch},
		ProviderCapabilitiesOverride: &activeCaps,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	requestTools := eng.requestTools(context.Background())
	if len(requestTools) != 1 {
		t.Fatalf("request tools = %+v, want one patch tool", requestTools)
	}
	if requestTools[0].Custom != nil {
		t.Fatalf("expected active compatible provider to use schema patch tool despite stale locked OpenAI caps, got %+v", requestTools[0])
	}
	if len(requestTools[0].Schema) == 0 {
		t.Fatalf("expected function-tool schema fallback for active compatible provider, got %+v", requestTools[0])
	}
}

func TestFailedCustomToolResultPersistsAsCustomToolCallOutput(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "patching", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{
				ID:          "call_patch",
				Name:        string(toolspec.ToolPatch),
				Custom:      true,
				CustomInput: "*** Begin Patch\n*** Add File: a.txt\n+hi\n*** End Patch\n",
				Input:       json.RawMessage(`{}`),
			}},
			Usage: llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	eng, err := New(store, client, tools.NewRegistry(failingTool{name: toolspec.ToolPatch}), Config{Model: "gpt-5", EnabledTools: []toolspec.ID{toolspec.ToolPatch}})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.SubmitUserMessage(context.Background(), "apply patch"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(client.calls) < 2 {
		t.Fatalf("expected follow-up request after tool result, got %d", len(client.calls))
	}

	foundCustomOutput := false
	foundFunctionOutput := false
	for _, item := range client.calls[1].Items {
		switch {
		case item.Type == llm.ResponseItemTypeCustomToolOutput && item.CallID == "call_patch":
			foundCustomOutput = true
		case item.Type == llm.ResponseItemTypeFunctionCallOutput && item.CallID == "call_patch":
			foundFunctionOutput = true
		}
	}
	if !foundCustomOutput || foundFunctionOutput {
		t.Fatalf("expected failed custom output only, foundCustomOutput=%v foundFunctionOutput=%v items=%+v", foundCustomOutput, foundFunctionOutput, client.calls[1].Items)
	}
}

func TestRestoreMessagesPreservesRecoveredMultiToolProviderOrder(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	call1 := llm.ToolCall{ID: "call-1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}
	call2 := llm.ToolCall{ID: "call-2", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"ls"}`)}
	if _, err := store.AppendEvent("step", "message", llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call1, call2}}); err != nil {
		t.Fatalf("append assistant tool calls: %v", err)
	}
	if _, err := store.AppendEvent("step", "tool_completed", map[string]any{"call_id": call1.ID, "name": string(toolspec.ToolExecCommand), "is_error": false, "output": json.RawMessage(`{"output":"/tmp"}`)}); err != nil {
		t.Fatalf("append first tool completion: %v", err)
	}
	if _, err := store.AppendEvent("step", "tool_completed", map[string]any{"call_id": call2.ID, "name": string(toolspec.ToolExecCommand), "is_error": false, "output": json.RawMessage(`{"output":"a.txt"}`)}); err != nil {
		t.Fatalf("append second tool completion: %v", err)
	}
	restored, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	items := restored.snapshotItems()
	if len(items) != 4 {
		t.Fatalf("expected 4 restored items, got %d (%+v)", len(items), items)
	}
	if items[0].Type != llm.ResponseItemTypeFunctionCall || items[0].CallID != call1.ID {
		t.Fatalf("unexpected restored item[0]: %+v", items[0])
	}
	if items[1].Type != llm.ResponseItemTypeFunctionCall || items[1].CallID != call2.ID {
		t.Fatalf("unexpected restored item[1]: %+v", items[1])
	}
	if items[2].Type != llm.ResponseItemTypeFunctionCallOutput || items[2].CallID != call1.ID {
		t.Fatalf("unexpected restored item[2]: %+v", items[2])
	}
	if items[3].Type != llm.ResponseItemTypeFunctionCallOutput || items[3].CallID != call2.ID {
		t.Fatalf("unexpected restored item[3]: %+v", items[3])
	}
}

func TestRestoreMessagesPreservesRecoveredMultiToolExactTokenParity(t *testing.T) {
	dir := t.TempDir()
	liveStore, err := session.Create(filepath.Join(dir, "live"), "ws", dir)
	if err != nil {
		t.Fatalf("create live store: %v", err)
	}
	restoredStore, err := session.Create(filepath.Join(dir, "restored"), "ws", dir)
	if err != nil {
		t.Fatalf("create restored store: %v", err)
	}
	countForRequest := func(req llm.Request) int {
		count := 0
		for i, item := range req.Items {
			switch item.Type {
			case llm.ResponseItemTypeFunctionCall:
				count += 100 + (i * 7)
			case llm.ResponseItemTypeFunctionCallOutput:
				count += 1_000 + (i * 11)
			default:
				count += 10 + i
			}
		}
		return count
	}
	client := &fakeCompactionClient{inputTokenCountFn: countForRequest}
	live, err := New(liveStore, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 400_000})
	if err != nil {
		t.Fatalf("new live engine: %v", err)
	}
	call1 := llm.ToolCall{ID: "call-1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}
	call2 := llm.ToolCall{ID: "call-2", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"ls"}`)}
	if err := live.appendAssistantMessage("step", llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call1, call2}}); err != nil {
		t.Fatalf("append live assistant tool calls: %v", err)
	}
	if _, err := live.executeToolCalls(context.Background(), "step", []llm.ToolCall{call1, call2}); err != nil {
		t.Fatalf("execute live tool calls: %v", err)
	}
	liveReq, err := live.buildRequest(context.Background(), "", true)
	if err != nil {
		t.Fatalf("build live request: %v", err)
	}
	liveCount, ok := live.requestInputTokensPrecisely(context.Background(), liveReq)
	if !ok {
		t.Fatal("expected live precise token count")
	}
	if _, err := restoredStore.AppendEvent("step", "message", llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call1, call2}}); err != nil {
		t.Fatalf("append restored assistant tool calls: %v", err)
	}
	if _, err := restoredStore.AppendEvent("step", "tool_completed", map[string]any{"call_id": call1.ID, "name": string(toolspec.ToolExecCommand), "is_error": false, "output": json.RawMessage(`{"tool":"exec_command"}`)}); err != nil {
		t.Fatalf("append restored tool completion 1: %v", err)
	}
	if _, err := restoredStore.AppendEvent("step", "tool_completed", map[string]any{"call_id": call2.ID, "name": string(toolspec.ToolExecCommand), "is_error": false, "output": json.RawMessage(`{"tool":"exec_command"}`)}); err != nil {
		t.Fatalf("append restored tool completion 2: %v", err)
	}
	restored, err := New(restoredStore, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 400_000})
	if err != nil {
		t.Fatalf("new restored engine: %v", err)
	}
	restoredReq, err := restored.buildRequest(context.Background(), "", true)
	if err != nil {
		t.Fatalf("build restored request: %v", err)
	}
	restoredCount, ok := restored.requestInputTokensPrecisely(context.Background(), restoredReq)
	if !ok {
		t.Fatal("expected restored precise token count")
	}
	liveItemsJSON, err := json.Marshal(liveReq.Items)
	if err != nil {
		t.Fatalf("marshal live request items: %v", err)
	}
	restoredItemsJSON, err := json.Marshal(restoredReq.Items)
	if err != nil {
		t.Fatalf("marshal restored request items: %v", err)
	}
	if string(liveItemsJSON) != string(restoredItemsJSON) {
		t.Fatalf("request items mismatch\nlive=%s\nrestored=%s", liveItemsJSON, restoredItemsJSON)
	}
	if liveCount != restoredCount {
		t.Fatalf("precise token count mismatch: live=%d restored=%d", liveCount, restoredCount)
	}
}

func TestStreamingRetryResetsAttemptDeltas(t *testing.T) {
	withGenerateRetryDelays(t, []time.Duration{time.Millisecond})

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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, fakeReasoningStreamClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, fakeAsyncLateDeltaClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, fakeNoopStreamClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err = New(store, fakeSimpleStreamClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err = New(store, fakeSimpleStreamClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
			eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
	envMsg := requestMessages(firstReq)[1]
	if envMsg.Role != llm.RoleDeveloper || !strings.Contains(envMsg.Content, environmentInjectedHeader) {
		t.Fatalf("expected second message to be environment developer injection, got %+v", envMsg)
	}
	if envMsg.MessageType != llm.MessageTypeEnvironment {
		t.Fatalf("expected environment message type, got %+v", envMsg)
	}
	if requestMessages(firstReq)[2].Role != llm.RoleDeveloper || !strings.Contains(requestMessages(firstReq)[2].Content, "source: "+globalPath) {
		t.Fatalf("expected third message to be global developer AGENTS injection, got %+v", requestMessages(firstReq)[2])
	}
	if requestMessages(firstReq)[2].MessageType != llm.MessageTypeAgentsMD {
		t.Fatalf("expected global AGENTS message type, got %+v", requestMessages(firstReq)[2])
	}
	if requestMessages(firstReq)[3].Role != llm.RoleDeveloper || !strings.Contains(requestMessages(firstReq)[3].Content, "source: "+workspacePath) {
		t.Fatalf("expected fourth message to be workspace developer AGENTS injection, got %+v", requestMessages(firstReq)[3])
	}
	if requestMessages(firstReq)[3].MessageType != llm.MessageTypeAgentsMD {
		t.Fatalf("expected workspace AGENTS message type, got %+v", requestMessages(firstReq)[3])
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
	if !(envIdx < skillsIdx && skillsIdx < userIdx) {
		t.Fatalf("expected environment -> skills -> user ordering, got env=%d skills=%d user=%d", envIdx, skillsIdx, userIdx)
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
	for _, msg := range requestMessages(client.calls[0]) {
		if strings.Contains(msg.Content, "Skipped skill \"broken-skill\"") {
			t.Fatalf("expected broken skill warning to stay out of model request, got %+v", requestMessages(client.calls[0]))
		}
	}

	snapshot := eng.ChatSnapshot()
	foundWarning := false
	for _, entry := range snapshot.Entries {
		if entry.Role != "warning" || entry.Visibility != transcript.EntryVisibilityAll {
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

func TestEnvironmentContextMessageUsesWorkspaceRootForCWD(t *testing.T) {
	workspace := t.TempDir()
	msg, err := environmentContextMessage(workspace, "gpt-5.3-codex", time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("environmentContextMessage: %v", err)
	}
	if !strings.Contains(msg, "\nCWD: "+workspace+"\n") {
		t.Fatalf("expected environment message cwd to use workspace root %q, got %q", workspace, msg)
	}
}

func TestEnvironmentContextMessageFallsBackToProcessCWDWhenWorkspaceRootMissing(t *testing.T) {
	processCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	msg, err := environmentContextMessage("", "gpt-5.3-codex", time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("environmentContextMessage: %v", err)
	}
	if !strings.Contains(msg, "\nCWD: "+processCWD+"\n") {
		t.Fatalf("expected environment message cwd to fall back to process cwd %q, got %q", processCWD, msg)
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

	_, err = New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{})
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	if !strings.Contains(envMsg.Content, "\nCWD: "+workspace+"\n") {
		t.Fatalf("expected environment context cwd to use session workspace root %q, got %q", workspace, envMsg.Content)
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

func TestManualCompactionReinjectsHeadlessEnterOnlyWhileHeadlessRemainsActive(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "condensed summary"},
		Usage:     llm.Usage{InputTokens: 200, WindowTokens: 2_000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", CompactionMode: "local"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHeadlessMode, Content: "headless mode instructions"}); err != nil {
		t.Fatalf("append headless mode: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "continue"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	if err := eng.CompactContext(context.Background(), ""); err != nil {
		t.Fatalf("compact: %v", err)
	}

	messages := eng.snapshotMessages()
	headlessCount := 0
	exitCount := 0
	for _, message := range messages {
		switch message.MessageType {
		case llm.MessageTypeHeadlessMode:
			headlessCount++
		case llm.MessageTypeHeadlessModeExit:
			exitCount++
		}
	}
	if headlessCount != 1 {
		t.Fatalf("expected exactly one reinjected headless enter after compaction, got %d messages=%+v", headlessCount, messages)
	}
	if exitCount != 0 {
		t.Fatalf("did not expect headless exit after compaction while still headless, got %d messages=%+v", exitCount, messages)
	}
}

func TestManualCompactionDoesNotReinjectHeadlessEnterAfterExit(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "condensed summary"},
		Usage:     llm.Usage{InputTokens: 200, WindowTokens: 2_000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", CompactionMode: "local"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHeadlessMode, Content: "headless mode instructions"}); err != nil {
		t.Fatalf("append headless mode: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHeadlessModeExit, Content: "interactive mode instructions"}); err != nil {
		t.Fatalf("append headless exit: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "continue"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	if err := eng.CompactContext(context.Background(), ""); err != nil {
		t.Fatalf("compact: %v", err)
	}

	messages := eng.snapshotMessages()
	for _, message := range messages {
		if message.MessageType == llm.MessageTypeHeadlessMode {
			t.Fatalf("did not expect headless enter reinjection after exit, got messages=%+v", messages)
		}
		if message.MessageType == llm.MessageTypeHeadlessModeExit {
			t.Fatalf("did not expect historical headless exit to survive compaction, got messages=%+v", messages)
		}
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
	interactiveEngine, err := New(store, interactiveClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
	headlessEngine, err := New(store, headlessClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", HeadlessMode: true})
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
	headlessEngine, err := New(store, headlessClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", HeadlessMode: true})
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
	interactiveEngine, err := New(store, interactiveClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

func TestQueuedUserMessageFlushDoesNotEmitConversationUpdatedForInjectedMessage(t *testing.T) {
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
		eng        *Engine
		events     []Event
		eventIndex int
		flushIndex = -1
	)
	eng, err = New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			events = append(events, evt)
			eventIndex++
			if evt.Kind == EventUserMessageFlushed && evt.UserMessage == "steer now" && flushIndex < 0 {
				flushIndex = eventIndex
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
	if got := committedConversationUpdatedCountAfterLastUserFlush(events); got != 0 {
		t.Fatalf("committed conversation_updated count after injected user flush = %d, want 0; events=%+v", got, events)
	}
}

func TestDirectUserMessageFlushDoesNotEmitConversationUpdated(t *testing.T) {
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
		eng        *Engine
		events     []Event
		eventIndex int
		flushIndex = -1
	)
	eng, err = New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model: "gpt-5",
		OnEvent: func(evt Event) {
			events = append(events, evt)
			eventIndex++
			if evt.Kind == EventUserMessageFlushed && evt.UserMessage == "say hi" && flushIndex < 0 {
				flushIndex = eventIndex
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
	if got := committedConversationUpdatedCountAfterLastUserFlush(events); got != 0 {
		t.Fatalf("committed conversation_updated count after direct user flush = %d, want 0; events=%+v", got, events)
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 400_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 1234, OutputTokens: 66, WindowTokens: 399_000})

	usage := eng.ContextUsage()
	if usage.UsedTokens != 1234 {
		t.Fatalf("used tokens=%d, want 1234", usage.UsedTokens)
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
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
	want := 100 + estimated
	if usage.UsedTokens != want {
		t.Fatalf("used tokens=%d, want baseline+delta %d", usage.UsedTokens, want)
	}
}

func TestContextUsageAddsOnlyPostCheckpointEstimateDelta(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: strings.Repeat("seed-", 100)}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	checkpointEstimate := estimateItemsTokens(eng.snapshotItems())
	eng.setLastUsage(llm.Usage{InputTokens: 900, OutputTokens: 120, WindowTokens: 410_000})
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: strings.Repeat("delta-", 40)}); err != nil {
		t.Fatalf("append delta message: %v", err)
	}

	currentEstimate := estimateItemsTokens(eng.snapshotItems())
	deltaEstimate := currentEstimate - checkpointEstimate
	if deltaEstimate <= 0 {
		t.Fatalf("expected positive estimated delta, got checkpoint=%d current=%d", checkpointEstimate, currentEstimate)
	}

	usage := eng.ContextUsage()
	want := 900 + deltaEstimate
	if usage.UsedTokens != want {
		t.Fatalf("used tokens=%d, want baseline+delta %d", usage.UsedTokens, want)
	}
}

func TestReopenedSessionRestoresUsageCheckpointDeltaAccounting(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: strings.Repeat("seed-", 100)}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	checkpointEstimate := estimateItemsTokens(eng.snapshotItems())
	if err := eng.recordLastUsage(llm.Usage{InputTokens: 900, OutputTokens: 120, WindowTokens: 410_000, CachedInputTokens: 45, HasCachedInputTokens: true}); err != nil {
		t.Fatalf("record last usage: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: strings.Repeat("delta-", 40)}); err != nil {
		t.Fatalf("append delta message: %v", err)
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	restored, err := New(reopenedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}

	currentEstimate := estimateItemsTokens(restored.snapshotItems())
	deltaEstimate := currentEstimate - checkpointEstimate
	if deltaEstimate <= 0 {
		t.Fatalf("expected positive estimated delta after reopen, got checkpoint=%d current=%d", checkpointEstimate, currentEstimate)
	}
	usage := restored.ContextUsage()
	want := 900 + deltaEstimate
	if usage.UsedTokens != want {
		t.Fatalf("used tokens after reopen=%d, want baseline+delta %d", usage.UsedTokens, want)
	}
	if !usage.HasCacheHitPercentage || usage.CacheHitPercent != 5 {
		t.Fatalf("cache hit metadata after reopen=%+v, want 5%%", usage)
	}
}

func TestHistoryReplacementResetsDiagnosticDedupe(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendPersistedDiagnosticEntry("step-1", preciseTokenCountFailureDiagnostic, "error", "first fallback"); err != nil {
		t.Fatalf("append first diagnostic: %v", err)
	}
	if err := eng.replaceHistory("step-compact", "local", compactionModeManual, llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleAssistant, Content: "summary"}})); err != nil {
		t.Fatalf("replace history: %v", err)
	}
	if err := eng.appendPersistedDiagnosticEntry("step-2", preciseTokenCountFailureDiagnostic, "error", "second fallback"); err != nil {
		t.Fatalf("append second diagnostic: %v", err)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	count := 0
	for _, evt := range events {
		if evt.Kind != "local_entry" {
			continue
		}
		var entry storedLocalEntry
		if err := json.Unmarshal(evt.Payload, &entry); err != nil {
			t.Fatalf("decode local entry: %v", err)
		}
		if entry.DiagnosticKey == preciseTokenCountFailureDiagnostic {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("diagnostic entry count=%d, want 2", count)
	}
}

func TestReopenedSessionHistoryReplacementResetsDiagnosticDedupe(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendPersistedDiagnosticEntry("step-1", preciseTokenCountFailureDiagnostic, "error", "first fallback"); err != nil {
		t.Fatalf("append first diagnostic: %v", err)
	}
	if err := eng.replaceHistory("step-compact", "local", compactionModeManual, llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleAssistant, Content: "summary"}})); err != nil {
		t.Fatalf("replace history: %v", err)
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	restored, err := New(reopenedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	if err := restored.appendPersistedDiagnosticEntry("step-2", preciseTokenCountFailureDiagnostic, "error", "second fallback"); err != nil {
		t.Fatalf("append second diagnostic after reopen: %v", err)
	}

	events, err := reopenedStore.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	count := 0
	for _, evt := range events {
		if evt.Kind != "local_entry" {
			continue
		}
		var entry storedLocalEntry
		if err := json.Unmarshal(evt.Payload, &entry); err != nil {
			t.Fatalf("decode local entry: %v", err)
		}
		if entry.DiagnosticKey == preciseTokenCountFailureDiagnostic {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("diagnostic entry count after reopen=%d, want 2", count)
	}
}

func TestEstimateItemsTokensDoesNotTreatInlineImagePayloadAsPlainText(t *testing.T) {
	base64Payload := strings.Repeat("A", 24_000)
	item := llm.ResponseItem{
		Type:   llm.ResponseItemTypeFunctionCallOutput,
		Name:   string(toolspec.ToolViewImage),
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 100, OutputTokens: 0, WindowTokens: 410_000})
	if err := eng.appendMessage("", llm.Message{
		Role:       llm.RoleTool,
		ToolCallID: "call-1",
		Name:       string(toolspec.ToolViewImage),
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

func TestShouldCompactBeforeUserMessageFallsBackWhenExactCountUnsupported(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	supported := false
	client := &preciseCompactionClient{inputTokenCount: 960, contextWindow: 1000, countSupported: &supported}
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
		t.Fatal("expected fallback estimator to trigger pre-submit compaction when exact counting is unsupported")
	}
	if client.countCalls != 0 {
		t.Fatalf("count calls=%d, want 0 when exact counting is unsupported", client.countCalls)
	}
}

func TestShouldCompactBeforeUserMessageSkipsExactCountWhenProviderOverrideDisablesIt(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &preciseCompactionClient{inputTokenCount: 960, contextWindow: 1000}
	eng, err := New(store, client, tools.NewRegistry(), Config{
		Model:                 "gpt-5",
		AutoCompactTokenLimit: 950,
		ContextWindowTokens:   1000,
		ProviderCapabilitiesOverride: &llm.ProviderCapabilities{
			ProviderID:                     "openai",
			SupportsResponsesAPI:           true,
			SupportsResponsesCompact:       true,
			SupportsRequestInputTokenCount: false,
			SupportsPromptCacheKey:         true,
			SupportsNativeWebSearch:        true,
			SupportsReasoningEncrypted:     true,
			SupportsServerSideContextEdit:  true,
			IsOpenAIFirstParty:             true,
		},
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
		t.Fatal("expected fallback estimator to trigger pre-submit compaction when provider override disables exact counting")
	}
	if client.countCalls != 0 {
		t.Fatalf("count calls=%d, want 0 when provider override disables exact counting", client.countCalls)
	}
}

func TestShouldCompactBeforeUserMessageSkipsExactCountWhenLockedContractDisablesIt(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.MarkModelDispatchLocked(session.LockedContract{
		Model: "gpt-5",
		ProviderContract: session.LockedProviderCapabilities{
			ProviderID:                        "openai",
			SupportsRequestInputTokenCount:    false,
			HasSupportsRequestInputTokenCount: true,
		},
	}); err != nil {
		t.Fatalf("MarkModelDispatchLocked: %v", err)
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
		t.Fatal("expected fallback estimator to trigger pre-submit compaction when locked contract disables exact counting")
	}
	if client.countCalls != 0 {
		t.Fatalf("count calls=%d, want 0 when locked contract disables exact counting", client.countCalls)
	}
}

func TestShouldAutoCompactRechecksProviderBeforeCompactingOnLargeEstimate(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &preciseCompactionClient{inputTokenCount: 1, contextWindow: 1000}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
		Name:       string(toolspec.ToolViewImage),
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	restored, err := New(reopenedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	forked, err := New(forkedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

func TestForkedSessionDoesNotCopyPersistedUsageState(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	if err := eng.recordLastUsage(llm.Usage{InputTokens: 900, WindowTokens: 410_000}); err != nil {
		t.Fatalf("record last usage: %v", err)
	}
	if store.Meta().UsageState == nil {
		t.Fatal("expected parent session to persist usage state")
	}

	forkedStore, err := session.ForkAtUserMessage(store, 1, "Parent -> edit")
	if err != nil {
		t.Fatalf("fork session: %v", err)
	}
	if forkedStore.Meta().UsageState != nil {
		t.Fatalf("expected forked session usage state cleared, got %+v", forkedStore.Meta().UsageState)
	}
}

func TestForkedSessionAfterReminderPreservesCompactionSoonReminderIssuedState(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	restored, err := New(reopenedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	forked, err := New(forkedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

func TestLegacyReviewerRollbackHistoryReplacementIsIgnoredAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 410_000})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	if err := eng.recordLastUsage(llm.Usage{InputTokens: 900, WindowTokens: 410_000}); err != nil {
		t.Fatalf("record last usage: %v", err)
	}
	if store.Meta().UsageState == nil {
		t.Fatal("expected usage state persisted before rollback")
	}
	if _, err := store.AppendEvent("step-rollback", "history_replaced", historyReplacementPayload{Engine: "reviewer_rollback", Items: llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleUser, Content: "rolled back"}})}); err != nil {
		t.Fatalf("append legacy reviewer rollback history replacement: %v", err)
	}
	if store.Meta().UsageState == nil {
		t.Fatal("expected ignored legacy reviewer rollback to leave persisted usage state intact")
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	if reopenedStore.Meta().UsageState == nil {
		t.Fatal("expected reopened session to keep usage state intact after ignored legacy reviewer rollback")
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
			eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   20_000,
		AutoCompactTokenLimit: 10_000,
		MaxTokens:             20,
		CompactionMode:        "native",
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 9_990, WindowTokens: 20_000})

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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
				ToolCalls: []llm.ToolCall{{ID: "call_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}},
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
				ToolCalls: []llm.ToolCall{{ID: "call_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}},
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   2_000,
		AutoCompactTokenLimit: 1_000,
		CompactionMode:        "local",
		EnabledTools:          []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
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

func TestCompactionSoonReminderRechecksPreciselyAfterTranscriptMutation(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &preciseCompactionClient{inputTokenCount: 840, contextWindow: 2_000}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                 "gpt-5",
		ContextWindowTokens:   2_000,
		AutoCompactTokenLimit: 1_000,
		CompactionMode:        "local",
		EnabledTools:          []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 860, WindowTokens: 2_000})

	if err := eng.maybeAppendCompactionSoonReminder(context.Background(), "step-1"); err != nil {
		t.Fatalf("reminder below exact threshold: %v", err)
	}
	if client.countCalls != 1 {
		t.Fatalf("expected first reminder probe to count precisely once, got %d", client.countCalls)
	}
	if eng.handoffToolEnabled() {
		t.Fatal("did not expect handoff tool to become enabled below the exact reminder threshold")
	}

	client.inputTokenCount = 860
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleAssistant, Content: "mutation"}); err != nil {
		t.Fatalf("append mutation: %v", err)
	}
	if err := eng.maybeAppendCompactionSoonReminder(context.Background(), "step-2"); err != nil {
		t.Fatalf("reminder above exact threshold after mutation: %v", err)
	}
	if client.countCalls != 2 {
		t.Fatalf("expected transcript mutation to force a fresh precise reminder check, got %d calls", client.countCalls)
	}
	if !eng.handoffToolEnabled() {
		t.Fatal("expected reminder to enable trigger_handoff after exact recount")
	}
	reminderText := prompts.RenderCompactionSoonReminderPrompt(true)
	reminders := 0
	for _, entry := range eng.ChatSnapshot().Entries {
		if entry.Role == "warning" && entry.Text == reminderText {
			reminders++
		}
	}
	if reminders != 1 {
		t.Fatalf("expected one reminder after exact recount, got %d entries=%+v", reminders, eng.ChatSnapshot().Entries)
	}
}

func TestTriggerHandoffFailsBeforeReminder(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	_, _, err = eng.TriggerHandoff(context.Background(), "step-1", llm.ToolCall{ID: "call-handoff-1", Name: string(toolspec.ToolTriggerHandoff)}, "", "")
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
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

	_, _, err = eng.TriggerHandoff(context.Background(), "step-1", llm.ToolCall{ID: "call-handoff-1", Name: string(toolspec.ToolTriggerHandoff)}, "", "")
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
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
	activeCall := llm.ToolCall{ID: "call-handoff-1", Name: string(toolspec.ToolTriggerHandoff), Input: json.RawMessage(`{"summarizer_prompt":"keep API details","future_agent_message":"resume with tests"}`)}

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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
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

	_, _, err = eng.TriggerHandoff(context.Background(), "step-1", llm.ToolCall{ID: "call_handoff_retry", Name: string(toolspec.ToolTriggerHandoff)}, "keep API details", "resume with tests")
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
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

	_, _, err = eng.TriggerHandoff(context.Background(), "step-1", llm.ToolCall{ID: "call_handoff_append_retry", Name: string(toolspec.ToolTriggerHandoff)}, "keep API details", "resume with tests")
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	handoffCall := llm.ToolCall{
		ID:    "call_handoff_reopen_future_retry",
		Name:  string(toolspec.ToolTriggerHandoff),
		Input: mustJSON(map[string]any{"summarizer_prompt": "keep API details", "future_agent_message": "resume after restart"}),
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleAssistant, Content: "handing off", Phase: llm.MessagePhaseCommentary, ToolCalls: []llm.ToolCall{handoffCall}}); err != nil {
		t.Fatalf("append assistant tool call: %v", err)
	}
	resultOutput := mustJSON(triggerhandofftool.ResultPayload{
		Summary:                 "Handoff scheduled. Context will be compacted before the next model turn and future-agent guidance was saved.",
		FutureAgentMessageAdded: true,
	})
	if err := eng.persistToolCompletion("step-1", tools.Result{CallID: handoffCall.ID, Name: toolspec.ToolTriggerHandoff, Output: resultOutput}); err != nil {
		t.Fatalf("persist tool completion: %v", err)
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleTool, ToolCallID: handoffCall.ID, Name: string(toolspec.ToolTriggerHandoff), Content: string(resultOutput)}); err != nil {
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
	restored, err := New(reopenedStore, resumedClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
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
					Name:  string(toolspec.ToolTriggerHandoff),
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
		fakeTool{name: toolspec.ToolExecCommand},
		triggerhandofftool.New(func() triggerhandofftool.Controller { return eng }),
	)
	eng, err = New(store, client, registry, Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
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
					Name:  string(toolspec.ToolTriggerHandoff),
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
		fakeTool{name: toolspec.ToolExecCommand},
		triggerhandofftool.New(func() triggerhandofftool.Controller { return eng }),
	)
	eng, err = New(store, client, registry, Config{
		Model:                 "gpt-5",
		CompactionMode:        "local",
		ContextWindowTokens:   20_000,
		AutoCompactTokenLimit: 10_000,
		EnabledTools:          []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 8_900, WindowTokens: 20_000})

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
				Name:  string(toolspec.ToolTriggerHandoff),
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
		fakeTool{name: toolspec.ToolExecCommand},
		triggerhandofftool.New(func() triggerhandofftool.Controller { return eng }),
	)
	eng, err = New(store, firstClient, registry, Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	// Match real startup semantics: the initial runtime session has already injected
	// AGENTS/environment context before any reopen-and-resume path is exercised.
	// Without this seed, the first post-reopen SubmitUserMessage legitimately performs
	// that one-time injection and can trigger an extra compaction turn under this
	// tiny test window, which makes the test fail for the wrong reason.
	if err := eng.injectAgentsIfNeeded("seed-meta"); err != nil {
		t.Fatalf("inject agents: %v", err)
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
	restored, err := New(reopenedStore, resumedClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	handoffCall := llm.ToolCall{
		ID:    "call_handoff_pending_restore",
		Name:  string(toolspec.ToolTriggerHandoff),
		Input: mustJSON(map[string]any{"summarizer_prompt": "keep API details", "future_agent_message": "resume after restart"}),
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleAssistant, Content: "handing off", Phase: llm.MessagePhaseCommentary, ToolCalls: []llm.ToolCall{handoffCall}}); err != nil {
		t.Fatalf("append assistant tool call: %v", err)
	}
	resultOutput := mustJSON(triggerhandofftool.ResultPayload{
		Summary:                 "Handoff scheduled. Context will be compacted before the next model turn and future-agent guidance was saved.",
		FutureAgentMessageAdded: true,
	})
	if err := eng.persistToolCompletion("step-1", tools.Result{CallID: handoffCall.ID, Name: toolspec.ToolTriggerHandoff, Output: resultOutput}); err != nil {
		t.Fatalf("persist tool completion: %v", err)
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleTool, ToolCallID: handoffCall.ID, Name: string(toolspec.ToolTriggerHandoff), Content: string(resultOutput)}); err != nil {
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
	restored, err := New(reopenedStore, resumedClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	handoffCall := llm.ToolCall{
		ID:    "call_handoff_fork_restore",
		Name:  string(toolspec.ToolTriggerHandoff),
		Input: mustJSON(map[string]any{"future_agent_message": "resume after fork"}),
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleAssistant, Content: "handing off", Phase: llm.MessagePhaseCommentary, ToolCalls: []llm.ToolCall{handoffCall}}); err != nil {
		t.Fatalf("append assistant tool call: %v", err)
	}
	resultOutput := mustJSON(triggerhandofftool.ResultPayload{
		Summary:                 "Handoff scheduled. Context will be compacted before the next model turn and future-agent guidance was saved.",
		FutureAgentMessageAdded: true,
	})
	if err := eng.persistToolCompletion("step-1", tools.Result{CallID: handoffCall.ID, Name: toolspec.ToolTriggerHandoff, Output: resultOutput}); err != nil {
		t.Fatalf("persist tool completion: %v", err)
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleTool, ToolCallID: handoffCall.ID, Name: string(toolspec.ToolTriggerHandoff), Content: string(resultOutput)}); err != nil {
		t.Fatalf("append tool result: %v", err)
	}
	if err := eng.appendMessage("step-2", llm.Message{Role: llm.RoleUser, Content: "edit anchor"}); err != nil {
		t.Fatalf("append second user message: %v", err)
	}

	forkedStore, err := session.ForkAtUserMessage(store, 2, "Parent -> edit")
	if err != nil {
		t.Fatalf("fork session: %v", err)
	}
	forked, err := New(forkedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	handoffCall := llm.ToolCall{
		ID:    "call_handoff_satisfied_restore",
		Name:  string(toolspec.ToolTriggerHandoff),
		Input: mustJSON(map[string]any{"future_agent_message": "resume after manual compact"}),
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleAssistant, Content: "handing off", Phase: llm.MessagePhaseCommentary, ToolCalls: []llm.ToolCall{handoffCall}}); err != nil {
		t.Fatalf("append assistant tool call: %v", err)
	}
	resultOutput := mustJSON(triggerhandofftool.ResultPayload{
		Summary:                 "Handoff scheduled. Context will be compacted before the next model turn and future-agent guidance was saved.",
		FutureAgentMessageAdded: true,
	})
	if err := eng.persistToolCompletion("step-1", tools.Result{CallID: handoffCall.ID, Name: toolspec.ToolTriggerHandoff, Output: resultOutput}); err != nil {
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
	restored, err := New(reopenedStore, resumedClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
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

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	handoffCall := llm.ToolCall{
		ID:    "call_handoff_failed_restore",
		Name:  string(toolspec.ToolTriggerHandoff),
		Input: mustJSON(map[string]any{"future_agent_message": "should not resume"}),
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleAssistant, Content: "attempting handoff", Phase: llm.MessagePhaseCommentary, ToolCalls: []llm.ToolCall{handoffCall}}); err != nil {
		t.Fatalf("append assistant tool call: %v", err)
	}
	failedOutput := mustJSON(map[string]any{"error": handoffDisabledByUserMessage})
	if err := eng.persistToolCompletion("step-1", tools.Result{CallID: handoffCall.ID, Name: toolspec.ToolTriggerHandoff, IsError: true, Output: failedOutput}); err != nil {
		t.Fatalf("persist failed tool completion: %v", err)
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	restored, err := New(reopenedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	if restored.pendingHandoffRequest != nil {
		t.Fatalf("did not expect failed trigger_handoff completion to requeue handoff, got %+v", restored.pendingHandoffRequest)
	}
}

func TestReopenedSessionAfterLegacyReviewerRollbackStillRequeuesPendingTriggerHandoff(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	handoffCall := llm.ToolCall{
		ID:    "call_handoff_rollback_restore",
		Name:  string(toolspec.ToolTriggerHandoff),
		Input: mustJSON(map[string]any{"future_agent_message": "resume after rollback"}),
	}
	if err := eng.appendMessage("step-1", llm.Message{Role: llm.RoleAssistant, Content: "handing off", Phase: llm.MessagePhaseCommentary, ToolCalls: []llm.ToolCall{handoffCall}}); err != nil {
		t.Fatalf("append assistant tool call: %v", err)
	}
	resultOutput := mustJSON(triggerhandofftool.ResultPayload{
		Summary:                 "Handoff scheduled. Context will be compacted before the next model turn and future-agent guidance was saved.",
		FutureAgentMessageAdded: true,
	})
	if err := eng.persistToolCompletion("step-1", tools.Result{CallID: handoffCall.ID, Name: toolspec.ToolTriggerHandoff, Output: resultOutput}); err != nil {
		t.Fatalf("persist tool completion: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "history_replaced", historyReplacementPayload{Engine: "reviewer_rollback", Items: llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleUser, Content: "rolled back"}})}); err != nil {
		t.Fatalf("append legacy reviewer rollback history replacement: %v", err)
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	restored, err := New(reopenedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
	})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}
	if restored.pendingHandoffRequest == nil {
		t.Fatal("expected ignored legacy reviewer rollback to preserve pending handoff recovery")
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:          "gpt-5",
		CompactionMode: "local",
		EnabledTools:   []toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolTriggerHandoff},
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

	_, _, err = eng.TriggerHandoff(context.Background(), "step-1", llm.ToolCall{ID: "call_handoff_manual_clear", Name: string(toolspec.ToolTriggerHandoff)}, "", "resume after manual compact")
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", CompactionMode: "local"})
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", CompactionMode: "local"})
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeCompactionSummary, Content: "older summary"}); err != nil {
		t.Fatalf("append compaction summary: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "please keep tests green"}); err != nil {
		t.Fatalf("append user message: %v", err)
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

func TestManualLocalCompactionRebuildsCanonicalContextOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, agentsGlobalDirName)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global agents dir: %v", err)
	}
	globalPath := filepath.Join(globalDir, agentsFileName)
	if err := os.WriteFile(globalPath, []byte("global instructions"), 0o644); err != nil {
		t.Fatalf("write global AGENTS.md: %v", err)
	}

	workspace := t.TempDir()
	workspacePath := filepath.Join(workspace, agentsFileName)
	if err := os.WriteFile(workspacePath, []byte("workspace instructions"), 0o644); err != nil {
		t.Fatalf("write workspace AGENTS.md: %v", err)
	}
	writeTestSkill(t, filepath.Join(workspace, ".builder", "skills", "workspace-skill"), "workspace-skill", "from workspace")

	store, err := session.Create(t.TempDir(), "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeCompactionClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "condensed summary"},
		Usage:     llm.Usage{InputTokens: 1000, OutputTokens: 100, WindowTokens: 200000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", CompactionMode: "local"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "please keep tests green"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	if err := eng.CompactContext(context.Background(), ""); err != nil {
		t.Fatalf("compact: %v", err)
	}

	messages := eng.snapshotMessages()
	if len(messages) < 6 {
		t.Fatalf("expected canonical post-compaction messages, got %+v", messages)
	}
	if messages[0].MessageType != llm.MessageTypeCompactionSummary {
		t.Fatalf("expected compaction summary first, got %+v", messages[0])
	}
	if messages[1].MessageType != llm.MessageTypeEnvironment {
		t.Fatalf("expected environment second, got %+v", messages[1])
	}
	if messages[2].MessageType != llm.MessageTypeSkills {
		t.Fatalf("expected skills third, got %+v", messages[2])
	}
	if messages[3].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(messages[3].Content, "source: "+globalPath) {
		t.Fatalf("expected global AGENTS after skills, got %+v", messages[3])
	}
	if messages[4].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(messages[4].Content, "source: "+workspacePath) {
		t.Fatalf("expected workspace AGENTS after global AGENTS, got %+v", messages[4])
	}
	if messages[5].MessageType != llm.MessageTypeManualCompactionCarryover || !strings.Contains(messages[5].Content, "please keep tests green") {
		t.Fatalf("expected manual carryover after reinjected base context, got %+v", messages[5])
	}
}

func TestHandoffCompactionAppendsFutureMessageBeforeHeadlessReentry(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeCompactionClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "condensed summary"},
		Usage:     llm.Usage{InputTokens: 1000, OutputTokens: 100, WindowTokens: 200000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", CompactionMode: "local"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHeadlessMode, Content: "headless mode instructions"}); err != nil {
		t.Fatalf("append headless enter: %v", err)
	}
	eng.queueHandoffRequest("", "resume with tests")

	if err := eng.applyPendingHandoffIfNeeded(context.Background(), "step-1"); err != nil {
		t.Fatalf("apply pending handoff: %v", err)
	}

	messages := eng.snapshotMessages()
	futureIdx := -1
	headlessIdx := -1
	for idx, message := range messages {
		switch message.MessageType {
		case llm.MessageTypeHandoffFutureMessage:
			futureIdx = idx
		case llm.MessageTypeHeadlessMode:
			if idx > 0 {
				headlessIdx = idx
			}
		}
	}
	if futureIdx < 0 {
		t.Fatalf("expected future-agent message after handoff compaction, got %+v", messages)
	}
	if headlessIdx < 0 {
		t.Fatalf("expected headless enter reinjection after handoff compaction, got %+v", messages)
	}
	if futureIdx >= headlessIdx {
		t.Fatalf("expected future-agent message before headless reinjection, future=%d headless=%d messages=%+v", futureIdx, headlessIdx, messages)
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", CompactionMode: "local"})
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
	summaryCount := 0
	carryoverIndex := -1
	for i, entry := range entries {
		switch entry.Role {
		case "compaction_summary":
			summaryIndex = i
			summaryCount++
		case "manual_compaction_carryover":
			carryoverIndex = i
		}
	}
	if summaryIndex < 0 || carryoverIndex < 0 {
		t.Fatalf("expected summary and carryover entries, got %+v", entries)
	}
	if summaryCount != 1 {
		t.Fatalf("expected exactly one compaction summary entry, got %d entries=%+v", summaryCount, entries)
	}
	if summaryIndex >= carryoverIndex {
		t.Fatalf("expected compaction summary before manual carryover, got %+v", entries)
	}
}

func TestManualLocalCompactionOmitsCarryoverWithoutNewUserMessageSincePreviousCompaction(t *testing.T) {
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", CompactionMode: "local"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "older user message"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeCompactionSummary, Content: "previous compaction summary"}); err != nil {
		t.Fatalf("append previous compaction summary: %v", err)
	}

	if err := eng.CompactContext(context.Background(), ""); err != nil {
		t.Fatalf("compact: %v", err)
	}

	for _, message := range eng.snapshotMessages() {
		if message.MessageType == llm.MessageTypeManualCompactionCarryover {
			t.Fatalf("did not expect manual carryover message when no user message followed prior compaction, got %+v", eng.snapshotMessages())
		}
	}
	for _, entry := range eng.ChatSnapshot().Entries {
		if entry.Role == string(transcript.EntryRoleManualCompactionCarryover) {
			t.Fatalf("did not expect manual carryover transcript entry when no user message followed prior compaction, got %+v", eng.ChatSnapshot().Entries)
		}
	}
}

func TestReopenedManualCompactionKeepsCarryoverAsSingleDetailTranscriptEntry(t *testing.T) {
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", CompactionMode: "local"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "please keep tests green"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if err := eng.CompactContext(context.Background(), ""); err != nil {
		t.Fatalf("compact: %v", err)
	}

	reopenedStore, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	restored, err := New(reopenedStore, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", CompactionMode: "local"})
	if err != nil {
		t.Fatalf("restore engine: %v", err)
	}

	messages := restored.snapshotMessages()
	carryoverMessages := 0
	for _, message := range messages {
		if message.MessageType != llm.MessageTypeManualCompactionCarryover {
			continue
		}
		carryoverMessages++
		if !strings.Contains(message.Content, "please keep tests green") {
			t.Fatalf("expected reopened model carryover to preserve last user text, got %q", message.Content)
		}
	}
	if carryoverMessages != 1 {
		t.Fatalf("manual compaction carryover message count = %d, want 1; messages=%+v", carryoverMessages, messages)
	}

	entries := restored.ChatSnapshot().Entries
	carryoverEntries := 0
	for _, entry := range entries {
		if entry.Role != string(transcript.EntryRoleManualCompactionCarryover) {
			continue
		}
		carryoverEntries++
		if !strings.Contains(entry.Text, "please keep tests green") {
			t.Fatalf("expected reopened transcript carryover to preserve last user text, got %q", entry.Text)
		}
		if entry.Visibility != transcript.EntryVisibilityDetailOnly {
			t.Fatalf("expected reopened transcript carryover to stay detail-only, got %+v", entry)
		}
	}
	if carryoverEntries != 1 {
		t.Fatalf("manual compaction carryover transcript entry count = %d, want 1; entries=%+v", carryoverEntries, entries)
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", ContextWindowTokens: 400_000})
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", CompactionMode: "local"})
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

	if foundCanonical {
		t.Fatalf("did not expect pre-compaction developer context in local compaction request, got %+v", client.calls[0].Items)
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
				ToolCalls: []llm.ToolCall{{ID: "call_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}},
			},
		},
	}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", CompactionMode: "local"})
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
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5", CompactionMode: "none"})
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 190000, OutputTokens: 0, WindowTokens: 200000})

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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 190000, OutputTokens: 0, WindowTokens: 200000})

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

func TestAutoCompactionFailsWhenCompactionNoticePersistenceFails(t *testing.T) {
	localEntryErr := errors.New("injected compaction notice persistence failure")
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model: "gpt-5",
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.beforePersistLocalEntry = func(entry storedLocalEntry) error {
		if entry.Role == "compaction_notice" {
			return localEntryErr
		}
		return nil
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}
	eng.setLastUsage(llm.Usage{InputTokens: 190000, OutputTokens: 0, WindowTokens: 200000})

	err = eng.autoCompactIfNeeded(context.Background(), "step-1", compactionModeAuto)
	if err == nil {
		t.Fatal("expected auto compaction to fail when notice persistence fails")
	}
	if !errors.Is(err, localEntryErr) {
		t.Fatalf("expected injected compaction notice failure, got %v", err)
	}
}

func TestEmitCompactionStatusStillPublishesTerminalEventWhenNoticePersistenceFails(t *testing.T) {
	localEntryErr := errors.New("injected compaction notice persistence failure")
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	var events []Event
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:   "gpt-5",
		OnEvent: func(evt Event) { events = append(events, evt) },
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.beforePersistLocalEntry = func(entry storedLocalEntry) error {
		if entry.Role == "compaction_notice" {
			return localEntryErr
		}
		return nil
	}

	err = eng.emitCompactionStatus("step-1", EventCompactionCompleted, compactionModeAuto, "remote", "openai", 4, 2, "")
	if !errors.Is(err, localEntryErr) {
		t.Fatalf("emitCompactionStatus error = %v, want %v", err, localEntryErr)
	}
	terminalEvents := 0
	for _, evt := range events {
		if evt.Kind == EventLocalEntryAdded {
			t.Fatalf("did not expect persisted local entry event after notice persistence failure, got %+v", events)
		}
		if evt.Kind == EventCompactionCompleted {
			terminalEvents++
		}
	}
	if terminalEvents != 1 {
		t.Fatalf("expected one compaction completed event despite notice persistence failure, got %+v", events)
	}
}

func TestEmitCompactionStatusStillPublishesFailureEventWhenErrorPersistenceFails(t *testing.T) {
	localEntryErr := errors.New("injected compaction error persistence failure")
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	var events []Event
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:   "gpt-5",
		OnEvent: func(evt Event) { events = append(events, evt) },
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.beforePersistLocalEntry = func(entry storedLocalEntry) error {
		if entry.Role == "error" {
			return localEntryErr
		}
		return nil
	}

	err = eng.emitCompactionStatus("step-1", EventCompactionFailed, compactionModeAuto, "remote", "openai", 0, 2, "quota exceeded")
	if !errors.Is(err, localEntryErr) {
		t.Fatalf("emitCompactionStatus error = %v, want %v", err, localEntryErr)
	}
	terminalEvents := 0
	for _, evt := range events {
		if evt.Kind == EventLocalEntryAdded {
			t.Fatalf("did not expect persisted local entry event after error persistence failure, got %+v", events)
		}
		if evt.Kind == EventCompactionFailed {
			terminalEvents++
		}
	}
	if terminalEvents != 1 {
		t.Fatalf("expected one compaction failed event despite error persistence failure, got %+v", events)
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
					{ID: "call_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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

func TestAutoCompactionRemoteDropsPreCompactionDeveloperContext(t *testing.T) {
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
					{ID: "call_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
		t.Fatalf("expected remote compaction to reinject exactly one current global AGENTS context, got %d", globalCount)
	}
	if workspaceCount != 1 {
		t.Fatalf("expected remote compaction to reinject exactly one current workspace AGENTS context, got %d", workspaceCount)
	}
	if envCount != 1 {
		t.Fatalf("expected remote compaction to reinject exactly one current environment context, got %d", envCount)
	}
}

func TestManualRemoteCompactionRebuildsCanonicalPrefixOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, agentsGlobalDirName)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global agents dir: %v", err)
	}
	globalPath := filepath.Join(globalDir, agentsFileName)
	if err := os.WriteFile(globalPath, []byte("global instructions"), 0o644); err != nil {
		t.Fatalf("write global AGENTS.md: %v", err)
	}

	workspace := t.TempDir()
	workspacePath := filepath.Join(workspace, agentsFileName)
	if err := os.WriteFile(workspacePath, []byte("workspace instructions"), 0o644); err != nil {
		t.Fatalf("write workspace AGENTS.md: %v", err)
	}
	writeTestSkill(t, filepath.Join(workspace, ".builder", "skills", "workspace-skill"), "workspace-skill", "from workspace")

	store, err := session.Create(t.TempDir(), "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeCompactionClient{compactionResponses: []llm.CompactionResponse{{
		OutputItems: []llm.ResponseItem{
			{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, MessageType: llm.MessageTypeCompactionSummary, Content: "remote summary"},
			{Type: llm.ResponseItemTypeCompaction, ID: "cmp_1", EncryptedContent: "enc_1"},
		},
		Usage: llm.Usage{InputTokens: 1000, OutputTokens: 100, WindowTokens: 200000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHeadlessMode, Content: "headless mode instructions"}); err != nil {
		t.Fatalf("append headless mode: %v", err)
	}
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}

	if _, err := eng.compactNow(context.Background(), "step-1", compactionModeManual, "", false); err != nil {
		t.Fatalf("compactNow: %v", err)
	}

	items := eng.snapshotItems()
	if len(items) < 7 {
		t.Fatalf("expected canonical remote compaction prefix, got %+v", items)
	}
	if items[0].MessageType != llm.MessageTypeCompactionSummary || items[0].Content != "remote summary" {
		t.Fatalf("expected provider summary first, got %+v", items[0])
	}
	if items[1].Type != llm.ResponseItemTypeCompaction || items[1].EncryptedContent != "enc_1" {
		t.Fatalf("expected provider checkpoint second, got %+v", items[1])
	}
	if items[2].MessageType != llm.MessageTypeEnvironment {
		t.Fatalf("expected environment after provider output, got %+v", items[2])
	}
	if items[3].MessageType != llm.MessageTypeSkills {
		t.Fatalf("expected skills after environment, got %+v", items[3])
	}
	if items[4].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(items[4].Content, "source: "+globalPath) {
		t.Fatalf("expected global AGENTS after skills, got %+v", items[4])
	}
	if items[5].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(items[5].Content, "source: "+workspacePath) {
		t.Fatalf("expected workspace AGENTS after global AGENTS, got %+v", items[5])
	}
	if items[6].MessageType != llm.MessageTypeHeadlessMode {
		t.Fatalf("expected headless reinjection after canonical base context, got %+v", items[6])
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
					{ID: "call_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
					{ID: "call_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5.3-codex"})
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
					{ID: "call_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5.3-codex"})
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
					{ID: "call_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5.3-codex"})
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
					{ID: "call_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)},
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

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{Model: "gpt-5"})
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
