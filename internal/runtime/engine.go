package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"builder/internal/llm"
	"builder/internal/session"
	"builder/internal/tools"
	"builder/prompts"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
)

const (
	interruptMessage          = "User interrupted you"
	agentsFileName            = "AGENTS.md"
	agentsGlobalDirName       = ".builder"
	agentsInjectedHeader      = "# AGENTS.md content:"
	agentsInjectedFenceLabel  = "md"
	environmentInjectedHeader = "# Info about environment:"
	commentaryWithoutToolCallsWarning = "You sent a commentary-phase message without tool calls. This is wrong. If you intend to keep working, include tool calls with commentary updates. If you are done, send a final_answer phase message with no tool calls."
	finalWithToolCallsIgnoredWarning  = "You included tool calls with your final answer message. This is wrong, and your tool calls were ignored. If you intended to call the tools, include updates in the \"commentary\" channel along with tool calls. Otherwise, do not include tool calls with your final message responses"
)

type Config struct {
	Model                         string
	Temperature                   float64
	MaxTokens                     int
	ThinkingLevel                 string
	WebSearchMode                 string
	EnabledTools                  []tools.ID
	AutoCompactTokenLimit         int
	ContextWindowTokens           int
	EffectiveContextWindowPercent int
	LocalCompactionCarryoverLimit int
	UseNativeCompaction           *bool
	OnEvent                       func(Event)
}

type ContextUsage struct {
	UsedTokens   int
	WindowTokens int
}

type Engine struct {
	mu sync.Mutex

	store    *session.Store
	llm      llm.Client
	registry *tools.Registry
	cfg      Config

	chat   *chatStore
	locked *session.LockedContract

	pendingInjected []string
	cancelCurrent   context.CancelFunc
	busy            bool

	lastUsage llm.Usage

	compactionCount int
}

func New(store *session.Store, client llm.Client, registry *tools.Registry, cfg Config) (*Engine, error) {
	if store == nil || client == nil || registry == nil {
		return nil, errors.New("store, llm client, and tool registry are required")
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-5"
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 1
	}
	if cfg.MaxTokens < 0 {
		cfg.MaxTokens = 0
	}
	if cfg.EffectiveContextWindowPercent <= 0 || cfg.EffectiveContextWindowPercent > 100 {
		cfg.EffectiveContextWindowPercent = 95
	}
	if cfg.LocalCompactionCarryoverLimit <= 0 {
		cfg.LocalCompactionCarryoverLimit = 20_000
	}
	if cfg.UseNativeCompaction == nil {
		useNative := true
		cfg.UseNativeCompaction = &useNative
	}
	if cfg.ContextWindowTokens <= 0 {
		if meta, ok := llm.LookupModelMetadata(cfg.Model); ok && meta.ContextWindowTokens > 0 {
			cfg.ContextWindowTokens = meta.ContextWindowTokens
		}
	}

	eng := &Engine{
		store:    store,
		llm:      client,
		registry: registry,
		cfg:      cfg,
		chat:     newChatStore(),
	}

	meta := store.Meta()
	if meta.Locked != nil {
		copyLocked := *meta.Locked
		eng.locked = &copyLocked
	}

	if err := eng.restoreMessages(); err != nil {
		return nil, err
	}
	if meta.InFlightStep {
		if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, Content: interruptMessage}); err != nil {
			return nil, err
		}
		if err := store.MarkInFlight(false); err != nil {
			return nil, err
		}
	}

	return eng, nil
}

func (e *Engine) QueueUserMessage(text string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pendingInjected = append(e.pendingInjected, text)
}

func (e *Engine) DiscardQueuedUserMessagesMatching(text string) int {
	needle := strings.TrimSpace(text)
	if needle == "" {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	filtered := e.pendingInjected[:0]
	removed := 0
	for _, pending := range e.pendingInjected {
		if strings.TrimSpace(pending) == needle {
			removed++
			continue
		}
		filtered = append(filtered, pending)
	}
	e.pendingInjected = filtered
	return removed
}

func (e *Engine) Interrupt() error {
	e.mu.Lock()
	cancel := e.cancelCurrent
	busy := e.busy
	e.mu.Unlock()

	if !busy || cancel == nil {
		return nil
	}
	cancel()

	if err := e.appendMessage("", llm.Message{Role: llm.RoleDeveloper, Content: interruptMessage}); err != nil {
		return err
	}
	if err := e.store.MarkInFlight(false); err != nil {
		return err
	}
	return nil
}

func (e *Engine) SubmitUserMessage(ctx context.Context, text string) (assistant llm.Message, err error) {
	if text == "" {
		return llm.Message{}, errors.New("empty message")
	}

	e.mu.Lock()
	if e.busy {
		e.mu.Unlock()
		return llm.Message{}, errors.New("agent is busy")
	}
	e.busy = true
	stepCtx, cancel := context.WithCancel(ctx)
	e.cancelCurrent = cancel
	e.mu.Unlock()
	stepID := ""
	defer func() {
		e.mu.Lock()
		e.busy = false
		e.cancelCurrent = nil
		e.mu.Unlock()
		if clearErr := e.store.MarkInFlight(false); clearErr != nil {
			wrapped := fmt.Errorf("mark in-flight false: %w", clearErr)
			e.emit(Event{Kind: EventInFlightClearFailed, StepID: stepID, Error: wrapped.Error()})
			err = errors.Join(err, wrapped)
		}
	}()

	if err = e.store.MarkInFlight(true); err != nil {
		return llm.Message{}, err
	}

	stepID = uuid.NewString()

	if err = e.injectAgentsIfNeeded(stepID); err != nil {
		return llm.Message{}, err
	}
	if err = e.appendUserMessage(stepID, text); err != nil {
		return llm.Message{}, err
	}

	assistant, err = e.runStepLoop(stepCtx, stepID)
	if err != nil {
		return llm.Message{}, err
	}
	return assistant, nil
}

func (e *Engine) SubmitUserShellCommand(ctx context.Context, command string) (result tools.Result, err error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return tools.Result{}, errors.New("empty command")
	}

	e.mu.Lock()
	if e.busy {
		e.mu.Unlock()
		return tools.Result{}, errors.New("agent is busy")
	}
	e.busy = true
	stepCtx, cancel := context.WithCancel(ctx)
	e.cancelCurrent = cancel
	e.mu.Unlock()
	stepID := ""
	defer func() {
		e.mu.Lock()
		e.busy = false
		e.cancelCurrent = nil
		e.mu.Unlock()
		if clearErr := e.store.MarkInFlight(false); clearErr != nil {
			wrapped := fmt.Errorf("mark in-flight false: %w", clearErr)
			e.emit(Event{Kind: EventInFlightClearFailed, StepID: stepID, Error: wrapped.Error()})
			err = errors.Join(err, wrapped)
		}
	}()

	if err = e.store.MarkInFlight(true); err != nil {
		return tools.Result{}, err
	}

	stepID = uuid.NewString()

	if err = e.injectAgentsIfNeeded(stepID); err != nil {
		return tools.Result{}, err
	}
	if err = e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, Content: fmt.Sprintf("User ran shell command directly:\n%s", command)}); err != nil {
		return tools.Result{}, err
	}

	call := llm.ToolCall{
		ID:   uuid.NewString(),
		Name: string(tools.ToolShell),
		Input: mustJSON(map[string]any{
			"command":        command,
			"user_initiated": true,
		}),
	}
	if err = e.appendAssistantMessage(stepID, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}); err != nil {
		return tools.Result{}, err
	}

	e.emit(Event{Kind: EventToolCallStarted, StepID: stepID, ToolCall: &call})
	h, ok := e.registry.Get(tools.ToolShell)
	if !ok {
		result = tools.Result{CallID: call.ID, Name: tools.ToolShell, IsError: true, Output: mustJSON(map[string]any{"error": "unknown tool"})}
		if err = e.persistToolCompletion(stepID, result); err != nil {
			return result, fmt.Errorf("persist tool completion (call_id=%s tool=%s): %w", call.ID, result.Name, err)
		}
		e.emit(Event{Kind: EventToolCallCompleted, StepID: stepID, ToolResult: &result})
		if appendErr := e.appendMessage(stepID, llm.Message{Role: llm.RoleTool, Content: string(result.Output), ToolCallID: result.CallID, Name: string(result.Name)}); appendErr != nil {
			return result, appendErr
		}
		return result, errors.New("unknown tool")
	}

	result, err = h.Call(stepCtx, tools.Call{ID: call.ID, Name: tools.ToolShell, Input: call.Input, StepID: stepID})
	if result.Name == "" {
		result.Name = tools.ToolShell
	}
	if result.CallID == "" {
		result.CallID = call.ID
	}
	if err != nil {
		result = tools.Result{CallID: call.ID, Name: tools.ToolShell, IsError: true, Output: mustJSON(map[string]any{"error": err.Error()})}
	}
	if persistErr := e.persistToolCompletion(stepID, result); persistErr != nil {
		return result, errors.Join(err, fmt.Errorf("persist tool completion (call_id=%s tool=%s): %w", call.ID, result.Name, persistErr))
	}
	e.emit(Event{Kind: EventToolCallCompleted, StepID: stepID, ToolResult: &result})
	if appendErr := e.appendMessage(stepID, llm.Message{Role: llm.RoleTool, Content: string(result.Output), ToolCallID: result.CallID, Name: string(result.Name)}); appendErr != nil {
		return result, errors.Join(err, appendErr)
	}

	return result, err
}

func (e *Engine) runStepLoop(ctx context.Context, stepID string) (llm.Message, error) {
	for {
		if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
			return llm.Message{}, err
		}

		req, err := e.buildRequest(ctx, stepID, true)
		if err != nil {
			return llm.Message{}, err
		}

		resp, err := e.generateWithRetry(
			ctx,
			req,
			func(delta string) {
				e.chat.appendOngoingDelta(delta)
				e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
				e.emit(Event{Kind: EventAssistantDelta, StepID: stepID, AssistantDelta: delta})
			},
			func() {
				e.chat.clearOngoing()
				e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
				e.emit(Event{Kind: EventAssistantDeltaReset, StepID: stepID})
			},
		)
		if err != nil {
			return llm.Message{}, err
		}
		e.setLastUsage(resp.Usage)

		localToolCalls := append([]llm.ToolCall(nil), resp.ToolCalls...)
		hostedToolExecutions := hostedToolExecutionsFromOutputItems(resp.OutputItems)
		finalAnswerIncludedToolCalls := false

		assistantMsg := resp.Assistant
		if len(localToolCalls) > 0 {
			assistantMsg.ToolCalls = append([]llm.ToolCall(nil), localToolCalls...)
		}
		if len(hostedToolExecutions) > 0 {
			for _, hosted := range hostedToolExecutions {
				assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, hosted.Call)
			}
		}
		if len(resp.ReasoningItems) > 0 && len(assistantMsg.ReasoningItems) == 0 {
			assistantMsg.ReasoningItems = append([]llm.ReasoningItem(nil), resp.ReasoningItems...)
		}
		if assistantMsg.Phase == llm.MessagePhaseFinal && (len(localToolCalls) > 0 || len(hostedToolExecutions) > 0) {
			finalAnswerIncludedToolCalls = true
			localToolCalls = nil
			hostedToolExecutions = nil
			assistantMsg.ToolCalls = nil
		}
		if err := e.appendAssistantMessage(stepID, assistantMsg); err != nil {
			return llm.Message{}, err
		}
		if err := e.appendReasoningEntries(stepID, resp.Reasoning); err != nil {
			return llm.Message{}, err
		}
		if finalAnswerIncludedToolCalls {
			if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, Content: finalWithToolCallsIgnoredWarning}); err != nil {
				return llm.Message{}, err
			}
		}

		for _, hosted := range hostedToolExecutions {
			if err := e.persistToolCompletion(stepID, hosted.Result); err != nil {
				return llm.Message{}, err
			}
			msg := llm.Message{
				Role:       llm.RoleTool,
				Content:    string(hosted.Result.Output),
				ToolCallID: hosted.Result.CallID,
				Name:       string(hosted.Result.Name),
			}
			if err := e.appendMessage(stepID, msg); err != nil {
				return llm.Message{}, err
			}
		}

		if len(localToolCalls) == 0 {
			if assistantMsg.Phase == llm.MessagePhaseCommentary {
				if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, Content: commentaryWithoutToolCallsWarning}); err != nil {
					return llm.Message{}, err
				}
				if _, err := e.flushPendingUserInjections(stepID); err != nil {
					return llm.Message{}, err
				}
				if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
					return llm.Message{}, err
				}
				continue
			}
			flushed, err := e.flushPendingUserInjections(stepID)
			if err != nil {
				return llm.Message{}, err
			}
			if flushed > 0 {
				continue
			}
			if len(hostedToolExecutions) > 0 {
				continue
			}
			e.emit(Event{Kind: EventAssistantMessage, StepID: stepID, Message: assistantMsg})
			return assistantMsg, nil
		}

		results, err := e.executeToolCalls(ctx, stepID, localToolCalls)
		if err != nil {
			return llm.Message{}, err
		}

		for _, r := range results {
			msg := llm.Message{
				Role:       llm.RoleTool,
				Content:    string(r.Output),
				ToolCallID: r.CallID,
				Name:       string(r.Name),
			}
			if err := e.appendMessage(stepID, msg); err != nil {
				return llm.Message{}, err
			}
		}

		if _, err := e.flushPendingUserInjections(stepID); err != nil {
			return llm.Message{}, err
		}
		if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
			return llm.Message{}, err
		}
	}
}

func (e *Engine) buildRequest(ctx context.Context, _ string, allowTools bool) (llm.Request, error) {
	locked, err := e.ensureLocked()
	if err != nil {
		return llm.Request{}, err
	}

	var requestTools []llm.Tool
	if allowTools {
		requestTools = e.requestTools()
	} else {
		requestTools = []llm.Tool{}
	}

	msgs := e.snapshotMessages()
	msgs = sanitizeMessagesForLLM(msgs)
	items := e.snapshotItems()
	items = sanitizeItemsForLLM(items)

	req, err := llm.RequestFromLockedContractWithItems(locked, prompts.SystemPrompt, msgs, items, requestTools)
	if err != nil {
		return llm.Request{}, err
	}
	if allowTools {
		nativeWebSearch, nativeErr := e.enableNativeWebSearch(ctx)
		if nativeErr != nil {
			return llm.Request{}, nativeErr
		}
		req.EnableNativeWebSearch = nativeWebSearch
	}
	req.SessionID = e.store.Meta().SessionID
	return req, nil
}

func (e *Engine) enableNativeWebSearch(ctx context.Context) (bool, error) {
	if !hasEnabledTool(e.cfg.EnabledTools, tools.ToolWebSearch) {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(e.cfg.WebSearchMode), "native") {
		return false, nil
	}
	provider, ok := e.llm.(llm.ProviderCapabilitiesClient)
	if !ok {
		return false, nil
	}
	caps, err := provider.ProviderCapabilities(ctx)
	if err != nil {
		return false, fmt.Errorf("resolve provider capabilities for native web search: %w", err)
	}
	return caps.SupportsNativeWebSearch, nil
}

func hasEnabledTool(ids []tools.ID, toolID tools.ID) bool {
	for _, id := range ids {
		if id == toolID {
			return true
		}
	}
	return false
}

type hostedToolExecution struct {
	Call   llm.ToolCall
	Result tools.Result
}

func hostedToolExecutionsFromOutputItems(items []llm.ResponseItem) []hostedToolExecution {
	out := make([]hostedToolExecution, 0, len(items))
	for _, item := range items {
		execution, ok := hostedWebSearchExecution(item)
		if !ok {
			continue
		}
		out = append(out, execution)
	}
	return out
}

func hostedWebSearchExecution(item llm.ResponseItem) (hostedToolExecution, bool) {
	raw := item.Raw
	if len(raw) == 0 || !json.Valid(raw) {
		return hostedToolExecution{}, false
	}
	var payload struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		Status string `json:"status"`
		Action struct {
			Type    string `json:"type"`
			Query   string `json:"query"`
			URL     string `json:"url"`
			Pattern string `json:"pattern"`
		} `json:"action"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return hostedToolExecution{}, false
	}
	if strings.TrimSpace(payload.Type) != "web_search_call" {
		return hostedToolExecution{}, false
	}
	callID := strings.TrimSpace(payload.ID)
	if callID == "" {
		callID = strings.TrimSpace(item.ID)
	}
	if callID == "" {
		callID = strings.TrimSpace(item.CallID)
	}
	if callID == "" {
		return hostedToolExecution{}, false
	}
	input := map[string]string{}
	actionType := strings.TrimSpace(payload.Action.Type)
	if actionType != "" {
		input["action"] = actionType
	}
	query := strings.TrimSpace(payload.Action.Query)
	if url := strings.TrimSpace(payload.Action.URL); url != "" {
		if query == "" {
			query = url
		}
		input["url"] = url
	}
	if pattern := strings.TrimSpace(payload.Action.Pattern); pattern != "" {
		if query == "" {
			query = pattern
		}
		input["pattern"] = pattern
	}
	if query == "" {
		if actionType != "" {
			query = actionType
		} else {
			query = "web search"
		}
	}
	input["query"] = query
	inputRaw, err := json.Marshal(input)
	if err != nil {
		return hostedToolExecution{}, false
	}
	output := append(json.RawMessage(nil), raw...)
	if !json.Valid(output) {
		output = mustJSON(map[string]any{"raw": string(raw)})
	}
	isError := strings.EqualFold(strings.TrimSpace(payload.Status), "failed")
	return hostedToolExecution{
		Call: llm.ToolCall{
			ID:    callID,
			Name:  string(tools.ToolWebSearch),
			Input: inputRaw,
		},
		Result: tools.Result{
			CallID:  callID,
			Name:    tools.ToolWebSearch,
			Output:  output,
			IsError: isError,
		},
	}, true
}

func (e *Engine) requestTools() []llm.Tool {
	defs := e.registry.Definitions()
	if len(defs) == 0 {
		return nil
	}
	out := make([]llm.Tool, 0, len(defs))
	for _, d := range defs {
		out = append(out, llm.Tool{Name: string(d.ID), Description: d.Description, Schema: d.Schema})
	}
	return out
}

func sanitizeMessagesForLLM(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return messages
	}
	cleaned := make([]llm.Message, len(messages))
	for i, msg := range messages {
		cleaned[i] = msg
		content := xansi.Strip(msg.Content)
		if msg.Role == llm.RoleTool {
			content = normalizeToolMessageForLLM(content)
		}
		cleaned[i].Content = content
	}
	return cleaned
}

func sanitizeItemsForLLM(items []llm.ResponseItem) []llm.ResponseItem {
	if len(items) == 0 {
		return items
	}
	cleaned := llm.CloneResponseItems(items)
	for i := range cleaned {
		if cleaned[i].Type == llm.ResponseItemTypeMessage {
			cleaned[i].Content = xansi.Strip(cleaned[i].Content)
		}
		if cleaned[i].Type == llm.ResponseItemTypeFunctionCallOutput && len(cleaned[i].Output) > 0 {
			normalized := normalizeToolMessageForLLM(string(cleaned[i].Output))
			if json.Valid([]byte(normalized)) {
				cleaned[i].Output = json.RawMessage(normalized)
			} else {
				quoted, _ := json.Marshal(normalized)
				cleaned[i].Output = quoted
			}
		}
	}
	return cleaned
}

func normalizeToolMessageForLLM(content string) string {
	var payload any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return content
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return content
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

func (e *Engine) ensureLocked() (session.LockedContract, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.locked != nil {
		return *e.locked, nil
	}

	lock := session.LockedContract{
		Model:          e.cfg.Model,
		Temperature:    e.cfg.Temperature,
		MaxOutputToken: e.cfg.MaxTokens,
		ThinkingLevel:  e.cfg.ThinkingLevel,
		EnabledTools:   toToolNames(e.cfg.EnabledTools),
	}
	if err := e.store.MarkModelDispatchLocked(lock); err != nil {
		return session.LockedContract{}, err
	}
	e.locked = &lock
	return lock, nil
}

func (e *Engine) generateWithRetry(ctx context.Context, req llm.Request, onDelta func(string), onAttemptReset func()) (llm.Response, error) {
	delays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	var lastErr error
	for i := 0; i <= len(delays); i++ {
		var (
			resp           llm.Response
			err            error
			attemptEmitted bool
			attemptOnDelta func(string)
		)
		if onDelta != nil {
			attemptOnDelta = func(delta string) {
				if delta == "" {
					return
				}
				attemptEmitted = true
				onDelta(delta)
			}
		}
		if streamingClient, ok := e.llm.(llm.StreamClient); ok {
			resp, err = streamingClient.GenerateStream(ctx, req, attemptOnDelta)
		} else {
			resp, err = e.llm.Generate(ctx, req)
			if err == nil && attemptOnDelta != nil && resp.Assistant.Content != "" {
				attemptOnDelta(resp.Assistant.Content)
			}
		}
		if err == nil {
			return resp, nil
		}
		if llm.IsNonRetriableModelError(err) {
			return llm.Response{}, err
		}
		if attemptEmitted && onAttemptReset != nil {
			onAttemptReset()
		}
		lastErr = err
		if i == len(delays) {
			break
		}
		select {
		case <-ctx.Done():
			return llm.Response{}, ctx.Err()
		case <-time.After(delays[i]):
		}
	}
	return llm.Response{}, fmt.Errorf("model generation failed after retries: %w", lastErr)
}

func (e *Engine) executeToolCalls(ctx context.Context, stepID string, calls []llm.ToolCall) ([]tools.Result, error) {
	results := make([]tools.Result, len(calls))
	callErrs := make([]error, len(calls))
	wg := sync.WaitGroup{}

	for i := range calls {
		call := calls[i]
		if call.ID == "" {
			call.ID = uuid.NewString()
		}
		e.emit(Event{Kind: EventToolCallStarted, StepID: stepID, ToolCall: &call})
		idx := i
		wg.Add(1)
		go func(tc llm.ToolCall) {
			defer wg.Done()
			var callErr error

			toolID, ok := tools.ParseID(tc.Name)
			if !ok {
				results[idx] = tools.Result{CallID: tc.ID, Name: tools.ID(tc.Name), IsError: true, Output: mustJSON(map[string]any{"error": "unknown tool"})}
				if err := e.persistToolCompletion(stepID, results[idx]); err != nil {
					callErrs[idx] = fmt.Errorf("persist tool completion (call_id=%s tool=%s): %w", tc.ID, results[idx].Name, err)
				} else {
					e.emit(Event{Kind: EventToolCallCompleted, StepID: stepID, ToolResult: &results[idx]})
				}
				return
			}
			h, ok := e.registry.Get(toolID)
			if !ok {
				results[idx] = tools.Result{CallID: tc.ID, Name: toolID, IsError: true, Output: mustJSON(map[string]any{"error": "unknown tool"})}
				if err := e.persistToolCompletion(stepID, results[idx]); err != nil {
					callErrs[idx] = fmt.Errorf("persist tool completion (call_id=%s tool=%s): %w", tc.ID, results[idx].Name, err)
				} else {
					e.emit(Event{Kind: EventToolCallCompleted, StepID: stepID, ToolResult: &results[idx]})
				}
				return
			}
			res, err := h.Call(ctx, tools.Call{ID: tc.ID, Name: toolID, Input: tc.Input, StepID: stepID})
			if err != nil {
				callErr = err
				res = tools.Result{CallID: tc.ID, Name: toolID, IsError: true, Output: mustJSON(map[string]any{"error": err.Error()})}
			}
			if res.Name == "" {
				res.Name = toolID
			}
			results[idx] = res
			if err := e.persistToolCompletion(stepID, res); err != nil {
				persistErr := fmt.Errorf("persist tool completion (call_id=%s tool=%s): %w", tc.ID, res.Name, err)
				callErrs[idx] = errors.Join(callErr, persistErr)
				return
			}
			e.emit(Event{Kind: EventToolCallCompleted, StepID: stepID, ToolResult: &res})
			callErrs[idx] = callErr
		}(call)
	}

	wg.Wait()
	var joined error
	for _, err := range callErrs {
		joined = errors.Join(joined, err)
	}
	if joined != nil {
		return results, joined
	}
	return results, nil
}

func (e *Engine) persistToolCompletion(stepID string, r tools.Result) error {
	_, err := e.store.AppendEvent(stepID, "tool_completed", map[string]any{
		"call_id":  r.CallID,
		"name":     string(r.Name),
		"is_error": r.IsError,
		"output":   json.RawMessage(r.Output),
	})
	if err == nil {
		e.chat.recordToolCompletion(r)
		e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
	}
	return err
}

func (e *Engine) appendUserMessage(stepID, text string) error {
	msg := llm.Message{Role: llm.RoleUser, Content: text}
	return e.appendMessage(stepID, msg)
}

func (e *Engine) appendAssistantMessage(stepID string, msg llm.Message) error {
	return e.appendMessage(stepID, msg)
}

func (e *Engine) appendReasoningEntries(stepID string, entries []llm.ReasoningEntry) error {
	for _, entry := range entries {
		if err := e.appendPersistedLocalEntry(stepID, entry.Role, entry.Text); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) appendPersistedLocalEntry(stepID, role, text string) error {
	role = strings.TrimSpace(role)
	if role == "" || strings.TrimSpace(text) == "" {
		return nil
	}
	e.chat.appendLocalEntry(role, text)
	_, err := e.store.AppendEvent(stepID, "local_entry", storedLocalEntry{
		Role: role,
		Text: text,
	})
	if err == nil {
		e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
	}
	return err
}

func (e *Engine) appendMessage(stepID string, msg llm.Message) error {
	e.chat.appendMessage(msg)
	_, err := e.store.AppendEvent(stepID, "message", msg)
	if err == nil {
		e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
	}
	return err
}

func (e *Engine) flushPendingUserInjections(stepID string) (int, error) {
	e.mu.Lock()
	pending := append([]string(nil), e.pendingInjected...)
	e.pendingInjected = nil
	e.mu.Unlock()
	flushed := 0

	for _, m := range pending {
		if err := e.appendUserMessage(stepID, m); err != nil {
			return flushed, err
		}
		flushed++
		e.emit(Event{Kind: EventUserMessageFlushed, StepID: stepID, UserMessage: m})
	}
	return flushed, nil
}

func (e *Engine) injectAgentsIfNeeded(stepID string) error {
	meta := e.store.Meta()
	if meta.AgentsInjected {
		return nil
	}
	paths, err := agentsInjectionPaths(meta.WorkspaceRoot)
	if err != nil {
		return err
	}

	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("read AGENTS.md: %w", readErr)
		}
		injected := fmt.Sprintf("%s\nsource: %s\n\n```%s\n%s\n```", agentsInjectedHeader, path, agentsInjectedFenceLabel, string(data))
		if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, Content: injected}); err != nil {
			return err
		}
	}
	environment := environmentContextMessage(meta.WorkspaceRoot, time.Now())
	if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, Content: environment}); err != nil {
		return err
	}

	return e.store.MarkAgentsInjected()
}

func agentsInjectionPaths(workspaceRoot string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}

	paths := make([]string, 0, 2)
	seen := map[string]bool{}
	addPath := func(path string) {
		cleaned := filepath.Clean(path)
		if cleaned == "" || seen[cleaned] {
			return
		}
		seen[cleaned] = true
		paths = append(paths, cleaned)
	}

	addPath(filepath.Join(home, agentsGlobalDirName, agentsFileName))
	addPath(filepath.Join(workspaceRoot, agentsFileName))
	return paths, nil
}

func environmentContextMessage(workspaceRoot string, now time.Time) string {
	cwd, err := os.Getwd()
	if err != nil || strings.TrimSpace(cwd) == "" {
		cwd = strings.TrimSpace(workspaceRoot)
	}
	if strings.TrimSpace(cwd) == "" {
		cwd = "unknown"
	}

	shell := shellEnvironmentName()
	if strings.TrimSpace(shell) == "" {
		shell = "unknown"
	}

	osName := strings.TrimSpace(goruntime.GOOS)
	if osName == "" {
		osName = "unknown"
	}

	cpuArch := strings.TrimSpace(goruntime.GOARCH)
	if strings.TrimSpace(cpuArch) == "" {
		cpuArch = "unknown"
	}

	tzName, tzOffset := now.Zone()
	tzName = strings.TrimSpace(tzName)
	if tzName == "" {
		tzName = strings.TrimSpace(now.Location().String())
	}
	if tzName == "" {
		tzName = "unknown"
	}

	return strings.Join([]string{
		environmentInjectedHeader,
		fmt.Sprintf("OS: %s", osName),
		fmt.Sprintf("Current TZ: %s (UTC%s)", tzName, formatUTCOffset(tzOffset)),
		fmt.Sprintf("Date/time: %s", now.Format(time.RFC3339)),
		fmt.Sprintf("Shell: %s", shell),
		fmt.Sprintf("CWD: %s", cwd),
		fmt.Sprintf("CPU arch: %s", cpuArch),
	}, "\n")
}

func shellEnvironmentName() string {
	for _, key := range []string{"SHELL", "COMSPEC"} {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			continue
		}
		base := filepath.Base(value)
		if base == "" || base == "." || base == string(filepath.Separator) {
			return value
		}
		return base
	}
	return ""
}

func formatUTCOffset(offsetSeconds int) string {
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	hours := offsetSeconds / 3600
	minutes := (offsetSeconds % 3600) / 60
	return fmt.Sprintf("%s%02d:%02d", sign, hours, minutes)
}

func (e *Engine) restoreMessages() error {
	events, err := e.store.ReadEvents()
	if err != nil {
		return err
	}
	for _, evt := range events {
		switch evt.Kind {
		case "message":
			var msg llm.Message
			if err := json.Unmarshal(evt.Payload, &msg); err != nil {
				return fmt.Errorf("decode message event: %w", err)
			}
			e.chat.appendMessage(msg)
		case "tool_completed":
			if err := e.chat.restoreToolCompletionPayload(evt.Payload); err != nil {
				return err
			}
		case "local_entry":
			var entry storedLocalEntry
			if err := json.Unmarshal(evt.Payload, &entry); err != nil {
				return fmt.Errorf("decode local_entry event: %w", err)
			}
			e.chat.appendLocalEntry(entry.Role, entry.Text)
		case "history_replaced":
			var payload historyReplacementPayload
			if err := json.Unmarshal(evt.Payload, &payload); err != nil {
				return fmt.Errorf("decode history_replaced event: %w", err)
			}
			e.chat.replaceHistory(payload.Items)
			e.compactionCount++
		}
	}
	return nil
}

func (e *Engine) snapshotMessages() []llm.Message {
	return e.chat.snapshotMessages()
}

func (e *Engine) snapshotItems() []llm.ResponseItem {
	return e.chat.snapshotItems()
}

func (e *Engine) ChatSnapshot() ChatSnapshot {
	return e.chat.snapshot()
}

func (e *Engine) ContextUsage() ContextUsage {
	window := e.contextWindowTokens()
	used := e.currentTokenUsage()
	if used < 0 {
		used = 0
	}
	if window < 0 {
		window = 0
	}
	return ContextUsage{UsedTokens: used, WindowTokens: window}
}

func (e *Engine) AppendLocalEntry(role, text string) {
	e.chat.appendLocalEntry(role, text)
	e.emit(Event{Kind: EventConversationUpdated, StepID: ""})
}

func (e *Engine) SetOngoingError(text string) {
	e.chat.setOngoingError(text)
	e.emit(Event{Kind: EventConversationUpdated, StepID: ""})
}

func (e *Engine) ClearOngoingError() {
	e.chat.clearOngoingError()
	e.emit(Event{Kind: EventConversationUpdated, StepID: ""})
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

type storedLocalEntry struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type historyReplacementPayload struct {
	Engine string             `json:"engine"`
	Mode   string             `json:"mode"`
	Items  []llm.ResponseItem `json:"items"`
}

func toToolNames(ids []tools.ID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		out = append(out, string(id))
	}
	return out
}

func (e *Engine) lastUsageSnapshot() llm.Usage {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastUsage
}

func (e *Engine) setLastUsage(usage llm.Usage) {
	e.mu.Lock()
	e.lastUsage = usage
	e.mu.Unlock()
}

func (e *Engine) emit(evt Event) {
	if e.cfg.OnEvent != nil {
		e.cfg.OnEvent(evt)
	}
}

func (e *Engine) nextCompactionCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.compactionCount++
	return e.compactionCount
}
