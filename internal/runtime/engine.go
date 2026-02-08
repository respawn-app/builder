package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	interruptMessage         = "User interrupted you"
	handoffInstruction       = "Context threshold reached. Provide a concise handoff summary with next steps. Do not call tools."
	agentsFileName           = "AGENTS.md"
	agentsGlobalDirName      = ".builder"
	agentsInjectedHeader     = "# AGENTS.md content:"
	agentsInjectedFenceLabel = "md"
)

type Config struct {
	Model         string
	Temperature   float64
	MaxTokens     int
	ThinkingLevel string
	EnabledTools  []tools.ID
	OnEvent       func(Event)
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

	handoffPending bool
	handoffDone    bool
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
	if e.handoffDone {
		e.mu.Unlock()
		return llm.Message{Role: llm.RoleAssistant, Content: "Context threshold reached. Start a new session to continue."}, nil
	}
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

func (e *Engine) runStepLoop(ctx context.Context, stepID string) (llm.Message, error) {
	allowTools := true
	if e.handoffPending {
		allowTools = false
	}

	for {
		req, err := e.buildRequest(stepID, allowTools)
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

		assistantMsg := resp.Assistant
		if len(resp.ToolCalls) > 0 {
			assistantMsg.ToolCalls = append([]llm.ToolCall(nil), resp.ToolCalls...)
		}
		if len(resp.ReasoningItems) > 0 && len(assistantMsg.ReasoningItems) == 0 {
			assistantMsg.ReasoningItems = append([]llm.ReasoningItem(nil), resp.ReasoningItems...)
		}
		if err := e.appendAssistantMessage(stepID, assistantMsg); err != nil {
			return llm.Message{}, err
		}
		if err := e.appendReasoningEntries(stepID, resp.Reasoning); err != nil {
			return llm.Message{}, err
		}

		if resp.Usage.Percent() >= 80 && !e.handoffPending && !e.handoffDone {
			e.handoffPending = true
		}

		if len(resp.ToolCalls) == 0 || !allowTools {
			flushed, err := e.flushPendingUserInjections(stepID)
			if err != nil {
				return llm.Message{}, err
			}
			if flushed > 0 {
				if e.handoffPending {
					allowTools = false
				}
				continue
			}
			if e.handoffPending && !e.handoffDone {
				e.handoffDone = true
			}
			e.emit(Event{Kind: EventAssistantMessage, StepID: stepID, Message: assistantMsg})
			return assistantMsg, nil
		}

		results, err := e.executeToolCalls(ctx, stepID, resp.ToolCalls)
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

		if e.handoffPending {
			handoff := llm.Message{Role: llm.RoleDeveloper, Content: handoffInstruction}
			if err := e.appendMessage(stepID, handoff); err != nil {
				return llm.Message{}, err
			}
			allowTools = false
		}
	}
}

func (e *Engine) buildRequest(_ string, allowTools bool) (llm.Request, error) {
	locked, err := e.ensureLocked()
	if err != nil {
		return llm.Request{}, err
	}

	var requestTools []llm.Tool
	if allowTools {
		defs := e.registry.Definitions()
		if len(defs) > 0 {
			requestTools = make([]llm.Tool, 0, len(defs))
		}
		for _, d := range defs {
			requestTools = append(requestTools, llm.Tool{Name: string(d.ID), Description: d.Description, Schema: d.Schema})
		}
	} else {
		requestTools = []llm.Tool{}
	}

	msgs := e.snapshotMessages()
	msgs = sanitizeMessagesForLLM(msgs)

	req, err := llm.RequestFromLockedContract(locked, prompts.SystemPrompt, msgs, requestTools)
	if err != nil {
		return llm.Request{}, err
	}
	req.SessionID = e.store.Meta().SessionID
	return req, nil
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
		}
	}
	return nil
}

func (e *Engine) snapshotMessages() []llm.Message {
	return e.chat.snapshotMessages()
}

func (e *Engine) ChatSnapshot() ChatSnapshot {
	return e.chat.snapshot()
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

func (e *Engine) emit(evt Event) {
	if e.cfg.OnEvent != nil {
		e.cfg.OnEvent(evt)
	}
}
