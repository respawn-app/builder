package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"builder/internal/llm"
	"builder/internal/session"
	"builder/internal/tools"
	"github.com/google/uuid"
)

const (
	interruptMessage                  = "User interrupted you"
	agentsFileName                    = "AGENTS.md"
	agentsGlobalDirName               = ".builder"
	agentsInjectedHeader              = "# Project context and authoritative instructions from the ./AGENTS.md file:"
	agentsInjectedFenceLabel          = "md"
	environmentInjectedHeader         = "# Info about environment:"
	missingAssistantPhaseWarning      = "You sent a message without specifying a channel/phase. It was treated as commentary. If you finished your work and intended to end your turn, use the final channel explicitly. Otherwise continue and use the commentary channel for progress updates with tool calls."
	garbageAssistantContentWarning    = "Your assistant message appears malformed (contains invalid transport artifacts) and was treated as commentary. Continue working in commentary with proper tool calls, or send a clean final message when done."
	commentaryWithoutToolCallsWarning = "You sent a commentary-channel message without tool calls. This is wrong. If you intend to keep working, include tool calls with commentary updates. If you are done, send a final-channel message with no tool calls."
	finalWithToolCallsIgnoredWarning  = "You included tool calls with your final-channel message. This is wrong, and your tool calls were ignored. If you intended to call the tools, include updates in the commentary channel along with tool calls. Otherwise, do not include tool calls with your final message responses."
	finalWithoutContentWarning        = "You sent a final-channel message with empty content. This is wrong. If you are done, send a non-empty final message. If you intend to keep working, send a commentary-channel message with tool calls."
	reviewerNoopToken                 = "NO_OP"
	reviewerMetaBoundaryMessage       = "End of meta information. Transcript begins starting with next message. Below is NOT YOUR conversation, but another agent's transcript.\n-------"
)

var supportedThinkingLevels = map[string]struct{}{
	"low":    {},
	"medium": {},
	"high":   {},
	"xhigh":  {},
}

func NormalizeThinkingLevel(level string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(level))
	if normalized == "" {
		return "", false
	}
	_, ok := supportedThinkingLevels[normalized]
	return normalized, ok
}

func NormalizeReviewerFrequency(frequency string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(frequency)) {
	case "off":
		return "off", true
	case "all":
		return "all", true
	case "edits":
		return "edits", true
	default:
		return "", false
	}
}

func NormalizeCompactionMode(mode string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "native":
		return "native", true
	case "local":
		return "local", true
	case "none":
		return "none", true
	default:
		return "", false
	}
}

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
	CompactionMode                string
	AutoCompactionEnabled         *bool
	Reviewer                      ReviewerConfig
	HeadlessMode                  bool
	ToolPreambles                 bool
	OnEvent                       func(Event)
}

type ReviewerConfig struct {
	Frequency      string
	Model          string
	ThinkingLevel  string
	MaxSuggestions int
	Client         llm.Client
	ClientFactory  func() (llm.Client, error)
}

type ContextUsage struct {
	UsedTokens            int
	WindowTokens          int
	CacheHitPercent       int
	HasCacheHitPercentage bool
}

type Engine struct {
	mu sync.Mutex

	store    *session.Store
	llm      llm.Client
	reviewer llm.Client
	registry *tools.Registry
	cfg      Config

	chat   *chatStore
	locked *session.LockedContract

	pendingInjected []string
	pendingNotices  []llm.Message
	cancelCurrent   context.CancelFunc
	busy            bool
	noticeScheduled bool

	lastUsage llm.Usage

	phaseProtocolResolved bool
	phaseProtocolEnabled  bool

	reviewerResumeFrequency string

	totalInputTokens       int
	totalCachedInputTokens int

	compactionCount int

	compactionTokenCountCacheKey   string
	compactionTokenCountCacheValue int
	collaboratorsOnce              sync.Once

	phaseProtocol phaseProtocolEnforcer
	reviewerFlow  reviewerPipeline
	messageFlow   messageLifecycle
	stepFlow      stepExecutor
	toolFlow      toolExecutor
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
	if normalized, ok := NormalizeCompactionMode(cfg.CompactionMode); ok {
		cfg.CompactionMode = normalized
	} else {
		cfg.CompactionMode = "native"
	}
	if cfg.AutoCompactionEnabled == nil {
		enabled := true
		cfg.AutoCompactionEnabled = &enabled
	}
	if cfg.ContextWindowTokens <= 0 {
		if meta, ok := llm.LookupModelMetadata(cfg.Model); ok && meta.ContextWindowTokens > 0 {
			cfg.ContextWindowTokens = meta.ContextWindowTokens
		}
	}

	eng := &Engine{
		store:    store,
		llm:      client,
		reviewer: cfg.Reviewer.Client,
		registry: registry,
		cfg:      cfg,
		chat:     newChatStore(),
	}
	eng.ensureOrchestrationCollaborators()

	reviewerFrequency, ok := NormalizeReviewerFrequency(eng.cfg.Reviewer.Frequency)
	if !ok {
		reviewerFrequency = "off"
	}
	eng.cfg.Reviewer.Frequency = reviewerFrequency
	eng.reviewerResumeFrequency = reviewerFrequency
	if eng.reviewerResumeFrequency == "off" {
		eng.reviewerResumeFrequency = "edits"
	}
	if reviewerFrequency != "off" {
		if err := eng.initReviewerClient(); err != nil {
			return nil, err
		}
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
		if err := eng.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeInterruption, Content: interruptMessage}); err != nil {
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

	if err := e.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeInterruption, Content: interruptMessage}); err != nil {
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
	e.emit(Event{Kind: EventRunStateChanged, RunState: &RunState{Busy: true}})
	stepID := ""
	defer func() {
		e.mu.Lock()
		e.busy = false
		e.cancelCurrent = nil
		hasQueuedNotices := len(e.pendingNotices) > 0 && !e.noticeScheduled
		if hasQueuedNotices {
			e.noticeScheduled = true
		}
		e.mu.Unlock()
		e.emit(Event{Kind: EventRunStateChanged, StepID: stepID, RunState: &RunState{Busy: false}})
		if hasQueuedNotices {
			go e.processQueuedNotices(context.Background())
		}
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
	if err = e.injectHeadlessModeTransitionPromptIfNeeded(stepID); err != nil {
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
	e.emit(Event{Kind: EventRunStateChanged, RunState: &RunState{Busy: true}})
	stepID := ""
	defer func() {
		e.mu.Lock()
		e.busy = false
		e.cancelCurrent = nil
		hasQueuedNotices := len(e.pendingNotices) > 0 && !e.noticeScheduled
		if hasQueuedNotices {
			e.noticeScheduled = true
		}
		e.mu.Unlock()
		e.emit(Event{Kind: EventRunStateChanged, StepID: stepID, RunState: &RunState{Busy: false}})
		if hasQueuedNotices {
			go e.processQueuedNotices(context.Background())
		}
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
	if _, ok := e.registry.Get(tools.ToolShell); !ok {
		e.emit(Event{Kind: EventToolCallStarted, StepID: stepID, ToolCall: copiedToolCall(call)})
		result = tools.Result{CallID: call.ID, Name: tools.ToolShell, IsError: true, Output: mustJSON(map[string]any{"error": "unknown tool"})}
		if err = e.persistToolCompletion(stepID, result); err != nil {
			return result, fmt.Errorf("persist tool completion (call_id=%s tool=%s): %w", call.ID, result.Name, err)
		}
		e.emit(Event{Kind: EventToolCallCompleted, StepID: stepID, ToolResult: copiedToolResult(result)})
		if appendErr := e.appendMessage(stepID, llm.Message{Role: llm.RoleTool, Content: string(result.Output), ToolCallID: result.CallID, Name: string(result.Name)}); appendErr != nil {
			return result, appendErr
		}
		return result, errors.New("unknown tool")
	}

	results, execErr := e.executeToolCalls(stepCtx, stepID, []llm.ToolCall{call})
	if len(results) == 0 {
		return tools.Result{}, errors.New("shell tool execution returned no result")
	}
	result = results[0]
	err = execErr
	if appendErr := e.appendMessage(stepID, llm.Message{Role: llm.RoleTool, Content: string(result.Output), ToolCallID: result.CallID, Name: string(result.Name)}); appendErr != nil {
		return result, errors.Join(err, appendErr)
	}

	return result, err
}

func (e *Engine) runStepLoop(ctx context.Context, stepID string) (llm.Message, error) {
	reviewerFrequency := e.ReviewerFrequency()
	reviewerClient := e.reviewerClientSnapshot()
	msg, _, err := e.runStepLoopWithOptions(ctx, stepID, reviewerFrequency, reviewerClient, true, true)
	return msg, err
}

// runStepLoopWithOptions executes a single assistant/tool loop.
// reviewerFrequency/reviewerClient are used as the baseline reviewer policy for
// this run. When refreshReviewerConfigOnResolve is true, the final assistant
// resolution re-reads current runtime reviewer config so busy-time toggles (for
// example from /supervisor) affect the currently running step at completion.
func (e *Engine) runStepLoopWithOptions(ctx context.Context, stepID string, reviewerFrequency string, reviewerClient llm.Client, emitAssistantEvent bool, refreshReviewerConfigOnResolve bool) (llm.Message, bool, error) {
	e.ensureOrchestrationCollaborators()
	return e.stepFlow.RunStepLoopWithOptions(ctx, stepID, stepLoopOptions{
		ReviewerFrequency:              reviewerFrequency,
		ReviewerClient:                 reviewerClient,
		EmitAssistantEvent:             emitAssistantEvent,
		RefreshReviewerConfigOnResolve: refreshReviewerConfigOnResolve,
	})
}

func (e *Engine) phaseProtocolEnabledForModel(ctx context.Context) bool {
	e.ensureOrchestrationCollaborators()
	return e.phaseProtocol.EnabledForModel(ctx)
}

func (e *Engine) shouldRunReviewerTurnForFrequency(frequency string, reviewerClient llm.Client, patchEditsApplied bool) bool {
	e.ensureOrchestrationCollaborators()
	return e.reviewerFlow.ShouldRunTurn(frequency, reviewerClient, patchEditsApplied)
}

func (e *Engine) runReviewerFollowUp(ctx context.Context, stepID string, original llm.Message, reviewerClient llm.Client) (llm.Message, error) {
	e.ensureOrchestrationCollaborators()
	return e.reviewerFlow.RunFollowUp(ctx, stepID, original, reviewerClient)
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
		EnabledTools:   toToolNames(e.cfg.EnabledTools),
		ToolPreambles: func() *bool {
			enabled := !e.cfg.HeadlessMode && e.cfg.ToolPreambles
			return &enabled
		}(),
	}
	if err := e.store.MarkModelDispatchLocked(lock); err != nil {
		return session.LockedContract{}, err
	}
	e.locked = &lock
	return lock, nil
}

func (e *Engine) generateWithRetry(ctx context.Context, req llm.Request, onDelta func(string), onReasoningDelta func(llm.ReasoningSummaryDelta), onAttemptReset func()) (llm.Response, error) {
	return e.generateWithRetryClient(ctx, e.llm, req, onDelta, onReasoningDelta, onAttemptReset)
}

func (e *Engine) generateWithRetryClient(ctx context.Context, client llm.Client, req llm.Request, onDelta func(string), onReasoningDelta func(llm.ReasoningSummaryDelta), onAttemptReset func()) (llm.Response, error) {
	delays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	var lastErr error
	for i := 0; i <= len(delays); i++ {
		var (
			resp                    llm.Response
			err                     error
			attemptEmitted          bool
			reasoningEmitted        bool
			attemptOnDelta          func(string)
			attemptOnReasoningDelta func(llm.ReasoningSummaryDelta)
			attemptDone             atomic.Bool
		)
		if onDelta != nil {
			attemptOnDelta = func(delta string) {
				if attemptDone.Load() {
					return
				}
				if delta == "" {
					return
				}
				attemptEmitted = true
				onDelta(delta)
			}
		}
		if onReasoningDelta != nil {
			attemptOnReasoningDelta = func(delta llm.ReasoningSummaryDelta) {
				if attemptDone.Load() {
					return
				}
				if strings.TrimSpace(delta.Text) == "" {
					return
				}
				reasoningEmitted = true
				onReasoningDelta(delta)
			}
		}
		if streamingClient, ok := client.(llm.StreamEventsClient); ok {
			resp, err = streamingClient.GenerateStreamWithEvents(ctx, req, llm.StreamCallbacks{
				OnAssistantDelta:        attemptOnDelta,
				OnReasoningSummaryDelta: attemptOnReasoningDelta,
			})
		} else if streamingClient, ok := client.(llm.StreamClient); ok {
			resp, err = streamingClient.GenerateStream(ctx, req, attemptOnDelta)
		} else {
			resp, err = client.Generate(ctx, req)
			if err == nil && attemptOnDelta != nil && resp.Assistant.Content != "" {
				attemptOnDelta(resp.Assistant.Content)
			}
		}
		attemptDone.Store(true)
		if err == nil {
			return resp, nil
		}
		if llm.IsNonRetriableModelError(err) {
			return llm.Response{}, err
		}
		if (attemptEmitted || reasoningEmitted) && onAttemptReset != nil {
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
	e.ensureOrchestrationCollaborators()
	return e.toolFlow.ExecuteToolCalls(ctx, stepID, calls)
}
