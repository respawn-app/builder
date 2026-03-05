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
	caps      llm.ProviderCapabilities
	capsErr   error
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
		ProviderID: "openai-compatible",
		Code:       llm.UnifiedErrorCodeProviderContract,
		Message:    "no error reducer registered for provider_id \"openai-compatible\"",
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
			Frequency:      "off",
			Model:          "gpt-5",
			ThinkingLevel:  "low",
			MaxSuggestions: 5,
			Client:         &fakeClient{},
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
			Frequency:      "off",
			Model:          "gpt-5",
			ThinkingLevel:  "low",
			MaxSuggestions: 5,
			Client:         nil,
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
			Frequency:      "off",
			Model:          "gpt-5",
			ThinkingLevel:  "low",
			MaxSuggestions: 5,
			Client:         nil,
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
			Frequency:      "off",
			Model:          "gpt-5",
			ThinkingLevel:  "low",
			MaxSuggestions: 5,
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
			Frequency:      "all",
			Model:          "gpt-5",
			ThinkingLevel:  "low",
			MaxSuggestions: 5,
			Client:         reviewerClient,
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

	execution, ok := hostedWebSearchExecution(item)
	if !ok {
		t.Fatal("expected hosted web search execution")
	}
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

	execution, ok := hostedWebSearchExecution(item)
	if !ok {
		t.Fatal("expected hosted web search execution")
	}
	var input map[string]string
	if err := json.Unmarshal(execution.Call.Input, &input); err != nil {
		t.Fatalf("decode hosted input: %v", err)
	}
	if input["query"] != "https://example.com" {
		t.Fatalf("expected url fallback in query, got %+v", input)
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
	for _, reqMsg := range secondReq.Messages {
		if reqMsg.Role == llm.RoleDeveloper && strings.Contains(reqMsg.Content, commentaryWithoutToolCallsWarning) {
			if reqMsg.MessageType != llm.MessageTypeErrorFeedback {
				t.Fatalf("expected commentary warning message type error_feedback, got %+v", reqMsg)
			}
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected commentary warning in next request, got %+v", secondReq.Messages)
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
	for _, reqMsg := range secondReq.Messages {
		if reqMsg.Role == llm.RoleDeveloper && strings.Contains(reqMsg.Content, missingAssistantPhaseWarning) {
			if reqMsg.MessageType != llm.MessageTypeErrorFeedback {
				t.Fatalf("expected missing-phase warning message type error_feedback, got %+v", reqMsg)
			}
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected missing-phase warning in next request, got %+v", secondReq.Messages)
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

func TestSubmitUserMessageGarbageAssistantTokenDowngradesToCommentaryAndContinues(t *testing.T) {
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
	for _, reqMsg := range secondReq.Messages {
		if reqMsg.Role == llm.RoleDeveloper && strings.Contains(reqMsg.Content, garbageAssistantContentWarning) {
			if reqMsg.MessageType != llm.MessageTypeErrorFeedback {
				t.Fatalf("expected garbage-token warning message type error_feedback, got %+v", reqMsg)
			}
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected garbage-token warning in next request, got %+v", secondReq.Messages)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	persistedAsCommentary := false
	persistedToken := false
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var persisted llm.Message
		if err := json.Unmarshal(evt.Payload, &persisted); err != nil {
			t.Fatalf("decode message event: %v", err)
		}
		if persisted.Role == llm.RoleAssistant && strings.Contains(persisted.Content, "working") {
			persistedAsCommentary = persisted.Phase == llm.MessagePhaseCommentary
		}
		if persisted.Role == llm.RoleAssistant && strings.Contains(persisted.Content, "#+#+#+#+") {
			persistedToken = true
		}
	}
	if !persistedAsCommentary {
		t.Fatalf("expected garbage-token assistant message to be persisted as commentary")
	}
	if !persistedToken {
		t.Fatalf("expected garbage token sequence to remain in persisted assistant content")
	}
}

func TestSubmitUserMessageEnvelopeLeakDowngradesToCommentaryAndContinues(t *testing.T) {
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
	for _, reqMsg := range secondReq.Messages {
		if reqMsg.Role == llm.RoleDeveloper && strings.Contains(reqMsg.Content, garbageAssistantContentWarning) {
			if reqMsg.MessageType != llm.MessageTypeErrorFeedback {
				t.Fatalf("expected envelope warning message type error_feedback, got %+v", reqMsg)
			}
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected envelope warning in next request, got %+v", secondReq.Messages)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	persistedEnvelopeAsCommentary := false
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var persisted llm.Message
		if err := json.Unmarshal(evt.Payload, &persisted); err != nil {
			t.Fatalf("decode message event: %v", err)
		}
		if persisted.Role == llm.RoleAssistant && strings.Contains(strings.ToLower(persisted.Content), "assistant to=functions.") {
			persistedEnvelopeAsCommentary = persisted.Phase == llm.MessagePhaseCommentary
		}
	}
	if !persistedEnvelopeAsCommentary {
		t.Fatalf("expected envelope leak assistant message to be persisted as commentary")
	}
}

func TestContainsMalformedAssistantContent_DetectsGarbageToken(t *testing.T) {
	garbageSamples := []string{
		"abc #+#+#+#+#+ xyz",
		"abc #+#+#+#+#+#+ xyz",
	}
	for _, sample := range garbageSamples {
		if !containsMalformedAssistantContent(sample) {
			t.Fatalf("expected malformed content to be detected for %q", sample)
		}
	}
	if containsMalformedAssistantContent("clean content") {
		t.Fatal("did not expect clean content to be flagged malformed")
	}
}

func TestContainsMalformedAssistantContent_DetectsEnvelopeLeak(t *testing.T) {
	leakSamples := []string{
		"assistant to=functions.shell commentary {\"command\":\"pwd\"}",
		"assistant to=functions.patch commentary {\"patch\":\"*** Begin Patch\"}",
		"assistant to=functions.multi_tool_use_parallel commentary {}",
		"assistant to=multi_tool_use.parallel commentary {}",
	}
	for _, sample := range leakSamples {
		if !containsMalformedAssistantContent(sample) {
			t.Fatalf("expected envelope leak to be detected for %q", sample)
		}
	}
	if containsMalformedAssistantContent("normal final answer") {
		t.Fatal("did not expect normal content to be flagged malformed")
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
	for _, reqMsg := range secondReq.Messages {
		if reqMsg.Role == llm.RoleDeveloper && strings.Contains(reqMsg.Content, finalWithoutContentWarning) {
			if reqMsg.MessageType != llm.MessageTypeErrorFeedback {
				t.Fatalf("expected final-without-content warning message type error_feedback, got %+v", reqMsg)
			}
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected final-without-content warning in next request, got %+v", secondReq.Messages)
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
			Frequency:      "edits",
			Model:          "gpt-5",
			ThinkingLevel:  "low",
			MaxSuggestions: 5,
			Client:         reviewerClient,
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
			Frequency:      "all",
			Model:          "gpt-5",
			ThinkingLevel:  "low",
			MaxSuggestions: 5,
			Client:         reviewerClient,
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
			Frequency:      "edits",
			Model:          "gpt-5",
			ThinkingLevel:  "low",
			MaxSuggestions: 5,
			Client:         reviewerClient,
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
			Frequency:      "all",
			Model:          "gpt-5",
			ThinkingLevel:  "low",
			MaxSuggestions: 5,
			Client:         reviewerClient,
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
	for _, message := range req.Messages {
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
	if len(reviewerReq.Messages) == 0 {
		t.Fatalf("expected reviewer request to include transcript entry messages")
	}
	if reviewerReq.Messages[0].Role != llm.RoleDeveloper || reviewerReq.Messages[0].MessageType != llm.MessageTypeAgentsMD || !strings.Contains(reviewerReq.Messages[0].Content, "source: "+globalPath) {
		t.Fatalf("expected reviewer message[0] to be AGENTS meta developer message, got %+v", reviewerReq.Messages[0])
	}
	environmentIdx := -1
	boundaryIdx := -1
	skillsMetaIdx := -1
	for idx, message := range reviewerReq.Messages {
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
		t.Fatalf("expected reviewer metadata to include environment context, got %+v", reviewerReq.Messages)
	}
	if boundaryIdx < 0 {
		t.Fatalf("expected reviewer metadata to include transcript boundary message, got %+v", reviewerReq.Messages)
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
	for _, message := range reviewerReq.Messages[boundaryIdx+1:] {
		if message.Role != llm.RoleUser {
			t.Fatalf("expected reviewer transcript entries after metadata to be user role messages, got %q", message.Role)
		}
		if strings.Contains(message.Content, "Agent:") {
			foundAgentLabel = true
		}
		if strings.Contains(message.Content, "Tool calls:") && strings.Contains(message.Content, "\"command\": \"pwd\"") {
			foundToolCallJSON = true
		}
		if strings.Contains(message.Content, "\"output\"") {
			foundToolOutputField = true
		}
		if strings.Contains(message.Content, "Tool output:") {
			foundSeparateToolOutput = true
		}
	}
	if !foundAgentLabel {
		t.Fatalf("expected reviewer request to include agent labels, messages=%+v", reviewerReq.Messages)
	}
	if !foundToolCallJSON {
		t.Fatalf("expected reviewer request to include tool call json args, messages=%+v", reviewerReq.Messages)
	}
	if !foundToolOutputField {
		t.Fatalf("expected reviewer request to include tool output in tool call payload, messages=%+v", reviewerReq.Messages)
	}
	if foundSeparateToolOutput {
		t.Fatalf("did not expect separate tool output entries when output is paired, messages=%+v", reviewerReq.Messages)
	}
	if len(reviewerReq.Items) != 0 {
		t.Fatalf("expected reviewer request items to be empty when using transcript entry messages, got %d", len(reviewerReq.Items))
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
			Frequency:      "all",
			Model:          "gpt-5",
			ThinkingLevel:  "low",
			MaxSuggestions: 5,
			Client:         reviewerClient,
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
			Frequency:      "all",
			Model:          "gpt-5",
			ThinkingLevel:  "low",
			MaxSuggestions: 5,
			Client:         reviewerClient,
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
			Frequency:      "all",
			Model:          "gpt-5",
			ThinkingLevel:  "low",
			MaxSuggestions: 5,
			Client:         reviewerClient,
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
			Frequency:      "all",
			Model:          "gpt-5",
			ThinkingLevel:  "low",
			MaxSuggestions: 5,
			Client:         reviewerClient,
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
		}
		if entry.Role == "assistant" && strings.Contains(entry.Text, "updated final after review") {
			foundFollowUpAssistant = true
			if followUpIdx < 0 {
				followUpIdx = idx
			}
		}
		if entry.Role == "reviewer_status" && strings.Contains(entry.Text, "applied.") {
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
}

func TestParseReviewerSuggestionsObjectSupportsStructuredPayload(t *testing.T) {
	suggestions := parseReviewerSuggestionsObject(`{"suggestions":["one","two","one"," "]}`, 3)
	if len(suggestions) != 2 || suggestions[0] != "one" || suggestions[1] != "two" {
		t.Fatalf("unexpected suggestions from object payload: %+v", suggestions)
	}

	suggestions = parseReviewerSuggestionsObject(`["a","b"]`, 5)
	if len(suggestions) != 0 {
		t.Fatalf("expected invalid non-object payload to be ignored, got %+v", suggestions)
	}

	suggestions = parseReviewerSuggestionsObject(`not-json`, 5)
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
		{Role: llm.RoleDeveloper, Content: environmentInjectedHeader + "\nOS: darwin"},
	}

	reviewerMessages := buildReviewerTranscriptMessages(messages)
	if len(reviewerMessages) != 3 {
		t.Fatalf("expected 3 reviewer transcript messages after filtering, got %d", len(reviewerMessages))
	}
	if reviewerMessages[0].Role != llm.RoleUser {
		t.Fatalf("expected reviewer transcript messages to use user role, got %q", reviewerMessages[0].Role)
	}
	if strings.Contains(reviewerMessages[0].Content, "I’ll inspect quickly.") {
		t.Fatalf("expected short commentary preamble to be dropped, message=%q", reviewerMessages[0].Content)
	}
	if strings.Contains(reviewerMessages[1].Content, "Running command now.") {
		t.Fatalf("expected short commentary preamble text to be stripped when tool calls exist, message=%q", reviewerMessages[1].Content)
	}
	if !strings.Contains(reviewerMessages[1].Content, "Tool calls:") || !strings.Contains(reviewerMessages[1].Content, "\"command\": \"pwd\"") {
		t.Fatalf("expected tool call arguments in json format, message=%q", reviewerMessages[1].Content)
	}
	if strings.Contains(reviewerMessages[1].Content, "(id=") {
		t.Fatalf("did not expect tool call id in reviewer transcript, message=%q", reviewerMessages[1].Content)
	}
	if !strings.Contains(reviewerMessages[1].Content, "\"output\"") || !strings.Contains(reviewerMessages[1].Content, "\"ok\"") {
		t.Fatalf("expected paired tool output field in tool call payload, message=%q", reviewerMessages[1].Content)
	}
	if !strings.Contains(reviewerMessages[2].Content, "Agent:") {
		t.Fatalf("expected assistant final answer entry to use agent label, message=%q", reviewerMessages[2].Content)
	}
	if strings.Contains(reviewerMessages[2].Content, "Tool output:") {
		t.Fatalf("did not expect separate tool output entry when paired output exists, message=%q", reviewerMessages[2].Content)
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
	got := appendMissingReviewerMetaContext(in, workspace, "gpt-5", "high")
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
	got := appendMissingReviewerMetaContext(in, workspace, "gpt-5", "high")
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
		Content:     agentsInjectedHeader + "\nsource: /tmp/global/AGENTS.md\n\n```md\nglobal\n```",
	}
	existingWorkspaceAgents := llm.Message{
		Role:        llm.RoleDeveloper,
		MessageType: llm.MessageTypeAgentsMD,
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

	got := appendMissingReviewerMetaContext(in, workspace, "gpt-5", "high")
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

	got := appendMissingReviewerMetaContext(in, workspace, "gpt-5", "high")
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
	if len(firstReq.Messages) < 5 {
		t.Fatalf("expected at least 5 messages in first request, got %d", len(firstReq.Messages))
	}
	if firstReq.Messages[0].Role != llm.RoleDeveloper || firstReq.Messages[0].Content != "existing context" {
		t.Fatalf("expected first message to be existing context, got %+v", firstReq.Messages[0])
	}
	if firstReq.Messages[1].Role != llm.RoleDeveloper || !strings.Contains(firstReq.Messages[1].Content, "source: "+globalPath) {
		t.Fatalf("expected second message to be global developer AGENTS injection, got %+v", firstReq.Messages[1])
	}
	if firstReq.Messages[1].MessageType != llm.MessageTypeAgentsMD {
		t.Fatalf("expected global AGENTS message type, got %+v", firstReq.Messages[1])
	}
	if firstReq.Messages[2].Role != llm.RoleDeveloper || !strings.Contains(firstReq.Messages[2].Content, "source: "+workspacePath) {
		t.Fatalf("expected third message to be workspace developer AGENTS injection, got %+v", firstReq.Messages[2])
	}
	if firstReq.Messages[2].MessageType != llm.MessageTypeAgentsMD {
		t.Fatalf("expected workspace AGENTS message type, got %+v", firstReq.Messages[2])
	}
	envMsg := firstReq.Messages[3]
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
	if firstReq.Messages[4].Role != llm.RoleUser || firstReq.Messages[4].Content != "first" {
		t.Fatalf("expected user message after injections, got %+v", firstReq.Messages[4])
	}

	secondReq := client.calls[1]
	injectedCount := 0
	envInjectedCount := 0
	for _, msg := range secondReq.Messages {
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
	if len(req.Messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != llm.RoleDeveloper || !strings.Contains(req.Messages[0].Content, environmentInjectedHeader) {
		t.Fatalf("expected first message to be environment injection, got %+v", req.Messages[0])
	}
	if !strings.Contains(req.Messages[0].Content, "\ngpt-5\n") {
		t.Fatalf("expected environment injection to include model label, got %+v", req.Messages[0])
	}
	if req.Messages[1].Role != llm.RoleUser || req.Messages[1].Content != "first" {
		t.Fatalf("expected user message after environment injection, got %+v", req.Messages[1])
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
	for i, msg := range firstReq.Messages {
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
		t.Fatalf("expected injected skills developer message in first request, messages=%+v", firstReq.Messages)
	}
	if envIdx < 0 {
		t.Fatalf("expected injected environment developer message in first request, messages=%+v", firstReq.Messages)
	}
	if userIdx < 0 {
		t.Fatalf("expected first user message in first request, messages=%+v", firstReq.Messages)
	}
	if !(skillsIdx < envIdx && envIdx < userIdx) {
		t.Fatalf("expected skills -> environment -> user ordering, got skills=%d env=%d user=%d", skillsIdx, envIdx, userIdx)
	}

	secondReq := client.calls[1]
	skillsInjectedCount := 0
	for _, msg := range secondReq.Messages {
		if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeSkills {
			skillsInjectedCount++
		}
	}
	if skillsInjectedCount != 1 {
		t.Fatalf("expected exactly one injected skills message to persist, got %d", skillsInjectedCount)
	}
}

func TestEnvironmentContextMessageIncludesStatusLineModelLabel(t *testing.T) {
	workspace := t.TempDir()
	msg := environmentContextMessage(workspace, "gpt-5.3.codex", "high", time.Unix(0, 0).UTC())
	if !strings.Contains(msg, "\ngpt-5.3.codex high\n") {
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
		Model:                 "gpt-5.3.codex",
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
	if len(req.Messages) < 2 {
		t.Fatalf("expected environment and user messages, got %d", len(req.Messages))
	}
	envMsg := req.Messages[0]
	if envMsg.Role != llm.RoleDeveloper || envMsg.MessageType != llm.MessageTypeEnvironment {
		t.Fatalf("expected first request message to be environment context, got %+v", envMsg)
	}
	if !strings.Contains(envMsg.Content, "\ngpt-5.3.codex high\n") {
		t.Fatalf("expected environment context to contain status model label line, got %q", envMsg.Content)
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
	if err := eng.appendMessage("", llm.Message{Role: llm.RoleUser, Content: prompts.CompactionSummaryPrefix + "\n\nold compacted summary"}); err != nil {
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
		if item.Role == llm.RoleUser && strings.HasPrefix(item.Content, prompts.CompactionSummaryPrefix) {
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
			if item.Type == llm.ResponseItemTypeMessage && item.Role == llm.RoleUser && strings.Contains(item.Content, prompts.CompactionSummaryPrefix) {
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
