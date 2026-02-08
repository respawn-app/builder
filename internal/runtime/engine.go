package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"builder/internal/llm"
	"builder/internal/session"
	"builder/internal/tools"
	"builder/prompts"
	"github.com/google/uuid"
)

const (
	interruptMessage         = "User interrupted you"
	handoffInstruction       = "Context threshold reached. Provide a concise handoff summary with next steps. Do not call tools."
	agentsFileName           = "AGENTS.md"
	agentsInjectedHeader     = "# AGENTS.md auto-injection"
	agentsInjectedFenceLabel = "md"
)

type Config struct {
	Model       string
	Temperature float64
	MaxTokens   int
}

type Engine struct {
	mu sync.Mutex

	store    *session.Store
	llm      llm.Client
	registry *tools.Registry
	cfg      Config

	messages []llm.Message
	locked   *session.LockedContract

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

func (e *Engine) SubmitUserMessage(ctx context.Context, text string) (llm.Message, error) {
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
	defer func() {
		e.mu.Lock()
		e.busy = false
		e.cancelCurrent = nil
		e.mu.Unlock()
		_ = e.store.MarkInFlight(false)
	}()

	if err := e.store.MarkInFlight(true); err != nil {
		return llm.Message{}, err
	}

	stepID := uuid.NewString()

	if err := e.appendUserMessage(stepID, text); err != nil {
		return llm.Message{}, err
	}

	if err := e.injectAgentsIfNeeded(stepID); err != nil {
		return llm.Message{}, err
	}

	assistant, err := e.runStepLoop(stepCtx, stepID)
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

		resp, err := e.generateWithRetry(ctx, req)
		if err != nil {
			return llm.Message{}, err
		}

		assistantMsg := resp.Assistant
		if len(resp.ToolCalls) > 0 {
			assistantMsg.ToolCalls = append([]llm.ToolCall(nil), resp.ToolCalls...)
		}
		if err := e.appendAssistantMessage(stepID, assistantMsg); err != nil {
			return llm.Message{}, err
		}

		if resp.Usage.Percent() >= 80 && !e.handoffPending && !e.handoffDone {
			e.handoffPending = true
		}

		if len(resp.ToolCalls) == 0 || !allowTools {
			if e.handoffPending && !e.handoffDone {
				e.handoffDone = true
			}
			return resp.Assistant, nil
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
				Name:       r.Name,
			}
			if err := e.appendMessage(stepID, msg); err != nil {
				return llm.Message{}, err
			}
		}

		if err := e.flushPendingUserInjections(stepID); err != nil {
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
			requestTools = append(requestTools, llm.Tool{Name: d.Name, Description: d.Description, Schema: d.Schema})
		}
	} else {
		requestTools = []llm.Tool{}
	}

	msgs := e.snapshotMessages()

	return llm.RequestFromLockedContract(locked, msgs, requestTools)
}

func (e *Engine) ensureLocked() (session.LockedContract, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.locked != nil {
		return *e.locked, nil
	}

	toolsJSON, err := json.Marshal(e.registry.Definitions())
	if err != nil {
		return session.LockedContract{}, err
	}
	lock := session.LockedContract{
		Model:          e.cfg.Model,
		Temperature:    e.cfg.Temperature,
		MaxOutputToken: e.cfg.MaxTokens,
		ToolsJSON:      toolsJSON,
		SystemPrompt:   prompts.SystemPrompt,
	}
	if err := e.store.MarkModelDispatchLocked(lock); err != nil {
		return session.LockedContract{}, err
	}
	e.locked = &lock
	return lock, nil
}

func (e *Engine) generateWithRetry(ctx context.Context, req llm.Request) (llm.Response, error) {
	delays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	var lastErr error
	for i := 0; i <= len(delays); i++ {
		resp, err := e.llm.Generate(ctx, req)
		if err == nil {
			return resp, nil
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
	errCh := make(chan error, len(calls))
	wg := sync.WaitGroup{}

	for i := range calls {
		call := calls[i]
		if call.ID == "" {
			call.ID = uuid.NewString()
		}
		idx := i
		wg.Add(1)
		go func(tc llm.ToolCall) {
			defer wg.Done()
			h, ok := e.registry.Get(tc.Name)
			if !ok {
				results[idx] = tools.Result{CallID: tc.ID, Name: tc.Name, IsError: true, Output: mustJSON(map[string]any{"error": "unknown tool"})}
				_ = e.persistToolCompletion(stepID, results[idx])
				return
			}
			res, err := h.Call(ctx, tools.Call{ID: tc.ID, Name: tc.Name, Input: tc.Input, StepID: stepID})
			if err != nil {
				errCh <- err
				res = tools.Result{CallID: tc.ID, Name: tc.Name, IsError: true, Output: mustJSON(map[string]any{"error": err.Error()})}
			}
			results[idx] = res
			_ = e.persistToolCompletion(stepID, res)
		}(call)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

func (e *Engine) persistToolCompletion(stepID string, r tools.Result) error {
	_, err := e.store.AppendEvent(stepID, "tool_completed", map[string]any{
		"call_id":  r.CallID,
		"name":     r.Name,
		"is_error": r.IsError,
		"output":   json.RawMessage(r.Output),
	})
	return err
}

func (e *Engine) appendUserMessage(stepID, text string) error {
	msg := llm.Message{Role: llm.RoleUser, Content: text}
	return e.appendMessage(stepID, msg)
}

func (e *Engine) appendAssistantMessage(stepID string, msg llm.Message) error {
	return e.appendMessage(stepID, msg)
}

func (e *Engine) appendMessage(stepID string, msg llm.Message) error {
	e.mu.Lock()
	e.messages = append(e.messages, msg)
	e.mu.Unlock()
	_, err := e.store.AppendEvent(stepID, "message", msg)
	return err
}

func (e *Engine) flushPendingUserInjections(stepID string) error {
	e.mu.Lock()
	pending := append([]string(nil), e.pendingInjected...)
	e.pendingInjected = nil
	e.mu.Unlock()

	for _, m := range pending {
		if err := e.appendUserMessage(stepID, m); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) injectAgentsIfNeeded(stepID string) error {
	meta := e.store.Meta()
	if meta.AgentsInjected {
		return nil
	}
	path := filepath.Join(meta.WorkspaceRoot, agentsFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return e.store.MarkAgentsInjected()
		}
		return fmt.Errorf("read AGENTS.md: %w", err)
	}

	injected := fmt.Sprintf("%s\nsource: %s\n\n```%s\n%s\n```", agentsInjectedHeader, path, agentsInjectedFenceLabel, string(data))
	if err := e.appendUserMessage(stepID, injected); err != nil {
		return err
	}
	return e.store.MarkAgentsInjected()
}

func (e *Engine) restoreMessages() error {
	events, err := e.store.ReadEvents()
	if err != nil {
		return err
	}
	msgs := []llm.Message{}
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var msg llm.Message
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			return fmt.Errorf("decode message event: %w", err)
		}
		msgs = append(msgs, msg)
	}
	e.mu.Lock()
	e.messages = msgs
	e.mu.Unlock()
	return nil
}

func (e *Engine) snapshotMessages() []llm.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	msgs := make([]llm.Message, len(e.messages))
	copy(msgs, e.messages)
	return msgs
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
