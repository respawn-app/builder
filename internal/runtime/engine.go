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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"builder/internal/llm"
	"builder/internal/session"
	"builder/internal/tools"
	"builder/prompts"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
)

const (
	interruptMessage                  = "User interrupted you"
	agentsFileName                    = "AGENTS.md"
	agentsGlobalDirName               = ".builder"
	agentsInjectedHeader              = "# AGENTS.md content:"
	agentsInjectedFenceLabel          = "md"
	environmentInjectedHeader         = "# Info about environment:"
	missingAssistantPhaseWarning      = "You sent a message without specifying a channel/phase. It was treated as commentary. If you finished your work and intended to end your turn, use the final channel explicitly. Otherwise continue and use the commentary channel for progress updates with tool calls."
	garbageAssistantContentWarning    = "Your assistant message appears malformed (contains invalid transport artifacts) and was treated as commentary. Continue working in commentary with proper tool calls, or send a clean final message when done."
	commentaryWithoutToolCallsWarning = "You sent a commentary-channel message without tool calls. This is wrong. If you intend to keep working, include tool calls with commentary updates. If you are done, send a final-channel message with no tool calls."
	finalWithToolCallsIgnoredWarning  = "You included tool calls with your final-channel message. This is wrong, and your tool calls were ignored. If you intended to call the tools, include updates in the commentary channel along with tool calls. Otherwise, do not include tool calls with your final message responses."
	finalWithoutContentWarning        = "You sent a final-channel message with empty content. This is wrong. If you are done, send a non-empty final message. If you intend to keep working, send a commentary-channel message with tool calls."
	reviewerNoopToken                 = "NO_OP"
	reviewerShortCommentaryMaxRunes   = 180
	reviewerMetaBoundaryMessage       = "End of meta information. Transcript begins starting with next message. Below is NOT YOUR conversation, but another agent's transcript.\n-------"
)

var malformedAssistantArtifacts = []string{
	"#+#+#+#+",
	"#+#+#+#+#+",
	"assistant to=functions.shell",
	"assistant to=functions.patch",
	"assistant to=functions.multi_tool_use_parallel",
	"assistant to=multi_tool_use.parallel",
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
	UseNativeCompaction           *bool
	Reviewer                      ReviewerConfig
	OnEvent                       func(Event)
}

type ReviewerConfig struct {
	Frequency      string
	Model          string
	ThinkingLevel  string
	MaxSuggestions int
	Client         llm.Client
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
	cancelCurrent   context.CancelFunc
	busy            bool

	lastUsage llm.Usage

	phaseProtocolResolved bool
	phaseProtocolEnabled  bool

	totalInputTokens       int
	totalCachedInputTokens int

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
		reviewer: cfg.Reviewer.Client,
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
	msg, _, err := e.runStepLoopWithOptions(ctx, stepID, true, true)
	return msg, err
}

func (e *Engine) runStepLoopWithOptions(ctx context.Context, stepID string, allowReviewer bool, emitAssistantEvent bool) (llm.Message, bool, error) {
	executedToolCall := false
	patchEditsApplied := false
	phaseProtocolEnabled := e.phaseProtocolEnabledForModel(ctx)
	for {
		if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
			return llm.Message{}, executedToolCall, err
		}

		req, err := e.buildRequest(ctx, stepID, true)
		if err != nil {
			return llm.Message{}, executedToolCall, err
		}

		resp, err := e.generateWithRetry(
			ctx,
			req,
			func(delta string) {
				e.chat.appendOngoingDelta(delta)
				e.emit(Event{Kind: EventAssistantDelta, StepID: stepID, AssistantDelta: delta})
			},
			func() {
				e.chat.clearOngoing()
				e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
				e.emit(Event{Kind: EventAssistantDeltaReset, StepID: stepID})
			},
		)
		if err != nil {
			return llm.Message{}, executedToolCall, err
		}
		e.setLastUsage(resp.Usage)
		e.emit(Event{
			Kind:   EventModelResponse,
			StepID: stepID,
			ModelResponse: &ModelResponseTrace{
				AssistantPhase:   resp.Assistant.Phase,
				AssistantChars:   len(resp.Assistant.Content),
				ToolCallsCount:   len(resp.ToolCalls),
				OutputItemsCount: len(resp.OutputItems),
				OutputItemTypes:  summarizeOutputItemTypes(resp.OutputItems),
			},
		})

		localToolCalls := append([]llm.ToolCall(nil), resp.ToolCalls...)
		hostedToolExecutions := hostedToolExecutionsFromOutputItems(resp.OutputItems)
		if len(localToolCalls) > 0 || len(hostedToolExecutions) > 0 {
			executedToolCall = true
		}
		finalAnswerIncludedToolCalls := false

		assistantMsg := resp.Assistant
		garbageAssistantContent := phaseProtocolEnabled && containsMalformedAssistantContent(assistantMsg.Content)
		if garbageAssistantContent {
			assistantMsg.Phase = llm.MessagePhaseCommentary
		}
		missingAssistantPhase := phaseProtocolEnabled && assistantMsg.Phase == "" && shouldTreatMissingAssistantPhaseAsCommentary(resp)
		if missingAssistantPhase {
			assistantMsg.Phase = llm.MessagePhaseCommentary
		}
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
		if phaseProtocolEnabled && assistantMsg.Phase == llm.MessagePhaseFinal && (len(localToolCalls) > 0 || len(hostedToolExecutions) > 0) {
			finalAnswerIncludedToolCalls = true
			localToolCalls = nil
			hostedToolExecutions = nil
			assistantMsg.ToolCalls = nil
		}
		if err := e.appendAssistantMessage(stepID, assistantMsg); err != nil {
			return llm.Message{}, executedToolCall, err
		}
		if err := e.appendReasoningEntries(stepID, resp.Reasoning); err != nil {
			return llm.Message{}, executedToolCall, err
		}
		if missingAssistantPhase {
			if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: missingAssistantPhaseWarning}); err != nil {
				return llm.Message{}, executedToolCall, err
			}
		}
		if garbageAssistantContent {
			if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: garbageAssistantContentWarning}); err != nil {
				return llm.Message{}, executedToolCall, err
			}
		}
		if finalAnswerIncludedToolCalls {
			if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: finalWithToolCallsIgnoredWarning}); err != nil {
				return llm.Message{}, executedToolCall, err
			}
		}

		for _, hosted := range hostedToolExecutions {
			if err := e.persistToolCompletion(stepID, hosted.Result); err != nil {
				return llm.Message{}, executedToolCall, err
			}
			msg := llm.Message{
				Role:       llm.RoleTool,
				Content:    string(hosted.Result.Output),
				ToolCallID: hosted.Result.CallID,
				Name:       string(hosted.Result.Name),
			}
			if err := e.appendMessage(stepID, msg); err != nil {
				return llm.Message{}, executedToolCall, err
			}
		}

		if len(localToolCalls) == 0 {
			if garbageAssistantContent {
				if _, err := e.flushPendingUserInjections(stepID); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				continue
			}
			if missingAssistantPhase {
				if _, err := e.flushPendingUserInjections(stepID); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				continue
			}
			if phaseProtocolEnabled && assistantMsg.Phase != llm.MessagePhaseFinal {
				if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: commentaryWithoutToolCallsWarning}); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				if _, err := e.flushPendingUserInjections(stepID); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				continue
			}
			if phaseProtocolEnabled && assistantMsg.Phase == llm.MessagePhaseFinal && strings.TrimSpace(assistantMsg.Content) == "" {
				if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: finalWithoutContentWarning}); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				if _, err := e.flushPendingUserInjections(stepID); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				continue
			}
			flushed, err := e.flushPendingUserInjections(stepID)
			if err != nil {
				return llm.Message{}, executedToolCall, err
			}
			if flushed > 0 {
				continue
			}
			if len(hostedToolExecutions) > 0 {
				continue
			}
			resolved := assistantMsg
			if allowReviewer && e.shouldRunReviewerTurn(patchEditsApplied) {
				reviewed, err := e.runReviewerFollowUp(ctx, stepID, assistantMsg)
				if err == nil {
					resolved = reviewed
				}
			}
			if emitAssistantEvent {
				e.emit(Event{Kind: EventAssistantMessage, StepID: stepID, Message: resolved})
			}
			return resolved, executedToolCall, nil
		}

		results, err := e.executeToolCalls(ctx, stepID, localToolCalls)
		if err != nil {
			return llm.Message{}, executedToolCall, err
		}

		for _, r := range results {
			if r.Name == tools.ToolPatch && !r.IsError {
				patchEditsApplied = true
			}
			msg := llm.Message{
				Role:       llm.RoleTool,
				Content:    string(r.Output),
				ToolCallID: r.CallID,
				Name:       string(r.Name),
			}
			if err := e.appendMessage(stepID, msg); err != nil {
				return llm.Message{}, executedToolCall, err
			}
		}

		if _, err := e.flushPendingUserInjections(stepID); err != nil {
			return llm.Message{}, executedToolCall, err
		}
		if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
			return llm.Message{}, executedToolCall, err
		}
	}
}

func shouldTreatMissingAssistantPhaseAsCommentary(resp llm.Response) bool {
	// Only enforce missing-phase fallback for structured provider responses.
	// Responses API always includes canonical output items; legacy clients that
	// only populate resp.Assistant remain backward-compatible.
	for _, item := range resp.OutputItems {
		if item.Type == llm.ResponseItemTypeMessage && item.Role == llm.RoleAssistant {
			return true
		}
	}
	return false
}

func containsMalformedAssistantContent(content string) bool {
	if strings.TrimSpace(content) == "" {
		return false
	}
	lower := strings.ToLower(content)
	for _, artifact := range malformedAssistantArtifacts {
		if strings.Contains(lower, artifact) {
			return true
		}
	}
	return false
}

func (e *Engine) phaseProtocolEnabledForModel(ctx context.Context) bool {
	// Phase/channel enforcement is an OpenAI Responses protocol feature.
	// Non-OpenAI providers should not be gated by commentary/final phases.
	e.mu.Lock()
	if e.phaseProtocolResolved {
		enabled := e.phaseProtocolEnabled
		e.mu.Unlock()
		return enabled
	}
	e.mu.Unlock()

	enabled := false
	if provider, ok := e.llm.(llm.ProviderCapabilitiesClient); ok {
		if caps, err := provider.ProviderCapabilities(ctx); err == nil {
			enabled = caps.SupportsResponsesAPI && caps.IsOpenAIFirstParty
		}
	}

	e.mu.Lock()
	if !e.phaseProtocolResolved {
		e.phaseProtocolResolved = true
		e.phaseProtocolEnabled = enabled
	}
	result := e.phaseProtocolEnabled
	e.mu.Unlock()
	return result
}

func (e *Engine) shouldRunReviewerTurn(patchEditsApplied bool) bool {
	if e.reviewer == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(e.cfg.Reviewer.Frequency)) {
	case "all":
		return true
	case "edits":
		return patchEditsApplied
	case "off", "":
		return false
	default:
		return false
	}
}

func (e *Engine) runReviewerFollowUp(ctx context.Context, stepID string, original llm.Message) (llm.Message, error) {
	baselineItems := e.snapshotItems()
	e.emit(Event{Kind: EventReviewerStarted, StepID: stepID})
	reviewerResult, err := e.runReviewerSuggestions(ctx)
	if err != nil {
		status := ReviewerStatus{
			Outcome: "failed",
			Error:   strings.TrimSpace(err.Error()),
		}
		e.emit(Event{Kind: EventReviewerCompleted, StepID: stepID, Reviewer: &status})
		_ = e.appendPersistedLocalEntry(stepID, "reviewer_status", reviewerStatusText(status, nil))
		return original, nil
	}
	suggestions := reviewerResult.Suggestions
	if len(suggestions) == 0 {
		status := ReviewerStatus{Outcome: "no_suggestions"}
		e.emit(Event{Kind: EventReviewerCompleted, StepID: stepID, Reviewer: &status})
		_ = e.appendPersistedLocalEntry(stepID, "reviewer_status", reviewerStatusText(status, nil))
		return original, nil
	}
	_ = e.appendPersistedLocalEntry(stepID, "reviewer_status", reviewerSuggestionsText(suggestions))

	instruction := formatReviewerDeveloperInstruction(suggestions)
	if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeReviewerFeedback, Content: instruction}); err != nil {
		return original, err
	}

	followUp, followUpExecutedToolCall, err := e.runStepLoopWithOptions(ctx, stepID, false, false)
	if err != nil {
		status := ReviewerStatus{
			Outcome:               "followup_failed",
			SuggestionsCount:      len(suggestions),
			CacheHitPercent:       reviewerResult.CacheHitPercent,
			HasCacheHitPercentage: reviewerResult.HasCacheHitPercentage,
			Error:                 strings.TrimSpace(err.Error()),
		}
		e.emit(Event{Kind: EventReviewerCompleted, StepID: stepID, Reviewer: &status})
		_ = e.appendPersistedLocalEntry(stepID, "reviewer_status", reviewerStatusText(status, nil))
		return original, nil
	}
	if strings.TrimSpace(followUp.Content) == reviewerNoopToken {
		if !followUpExecutedToolCall {
			_ = e.replaceHistory(stepID, "reviewer_rollback", compactionModeManual, baselineItems)
		}
		status := ReviewerStatus{
			Outcome:               "noop",
			SuggestionsCount:      len(suggestions),
			CacheHitPercent:       reviewerResult.CacheHitPercent,
			HasCacheHitPercentage: reviewerResult.HasCacheHitPercentage,
		}
		e.emit(Event{Kind: EventReviewerCompleted, StepID: stepID, Reviewer: &status})
		_ = e.appendPersistedLocalEntry(stepID, "reviewer_status", reviewerStatusText(status, nil))
		return original, nil
	}
	status := ReviewerStatus{
		Outcome:               "applied",
		SuggestionsCount:      len(suggestions),
		CacheHitPercent:       reviewerResult.CacheHitPercent,
		HasCacheHitPercentage: reviewerResult.HasCacheHitPercentage,
	}
	e.emit(Event{Kind: EventReviewerCompleted, StepID: stepID, Reviewer: &status})
	_ = e.appendPersistedLocalEntry(stepID, "reviewer_status", reviewerStatusText(status, nil))
	return followUp, nil
}

type reviewerSuggestionsResult struct {
	Suggestions           []string
	CacheHitPercent       int
	HasCacheHitPercentage bool
}

func (e *Engine) runReviewerSuggestions(ctx context.Context) (reviewerSuggestionsResult, error) {
	if e.reviewer == nil {
		return reviewerSuggestionsResult{}, nil
	}

	schema := mustJSON(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"suggestions": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
			},
		},
		"required": []string{"suggestions"},
	})

	messages := sanitizeMessagesForLLM(e.snapshotMessages())
	reviewerMessages := buildReviewerRequestMessages(messages, e.store.Meta().WorkspaceRoot)
	req := llm.Request{
		Model:           e.cfg.Reviewer.Model,
		Temperature:     1,
		MaxTokens:       0,
		ReasoningEffort: e.cfg.Reviewer.ThinkingLevel,
		SystemPrompt:    prompts.ReviewerSystemPrompt,
		SessionID:       reviewerSessionID(e.store.Meta().SessionID),
		Messages:        reviewerMessages,
		Items:           []llm.ResponseItem{},
		Tools:           []llm.Tool{},
		StructuredOutput: &llm.StructuredOutput{
			Name:   "reviewer_suggestions",
			Schema: schema,
			Strict: true,
		},
	}
	if err := req.Validate(); err != nil {
		return reviewerSuggestionsResult{}, err
	}
	resp, err := e.generateWithRetryClient(ctx, e.reviewer, req, nil, nil)
	if err != nil {
		return reviewerSuggestionsResult{}, err
	}
	cachePct, hasCachePct := resp.Usage.CacheHitPercent()
	return reviewerSuggestionsResult{
		Suggestions:           parseReviewerSuggestionsObject(resp.Assistant.Content, e.cfg.Reviewer.MaxSuggestions),
		CacheHitPercent:       cachePct,
		HasCacheHitPercentage: hasCachePct,
	}, nil
}

func parseReviewerSuggestionsObject(content string, maxSuggestions int) []string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}

	var payload struct {
		Suggestions []string `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return nil
	}
	out := payload.Suggestions

	if maxSuggestions <= 0 {
		maxSuggestions = 5
	}
	normalized := make([]string, 0, min(maxSuggestions, len(out)))
	seen := map[string]bool{}
	for _, suggestion := range out {
		text := strings.TrimSpace(suggestion)
		if text == "" {
			continue
		}
		if seen[text] {
			continue
		}
		seen[text] = true
		normalized = append(normalized, text)
		if len(normalized) >= maxSuggestions {
			break
		}
	}
	return normalized
}

func buildReviewerRequestMessages(messages []llm.Message, workspaceRoot string) []llm.Message {
	metaMessages, transcriptSource := splitReviewerMetaMessages(messages)
	metaMessages = appendMissingReviewerMetaContext(metaMessages, workspaceRoot)
	out := make([]llm.Message, 0, len(metaMessages)+2+len(transcriptSource))
	out = append(out, metaMessages...)
	out = append(out, llm.Message{Role: llm.RoleDeveloper, Content: reviewerMetaBoundaryMessage})
	out = append(out, buildReviewerTranscriptMessages(transcriptSource)...)
	return out
}

func splitReviewerMetaMessages(messages []llm.Message) ([]llm.Message, []llm.Message) {
	meta := make([]llm.Message, 0, 3)
	transcript := make([]llm.Message, 0, len(messages))
	seenEnvironment := false
	seenAgentContent := map[string]bool{}
	for _, message := range messages {
		if message.Role == llm.RoleDeveloper && message.MessageType == llm.MessageTypeAgentsMD {
			normalized := strings.TrimSpace(message.Content)
			if normalized == "" || seenAgentContent[normalized] {
				continue
			}
			seenAgentContent[normalized] = true
			meta = append(meta, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeAgentsMD, Content: message.Content})
			continue
		}
		if message.Role == llm.RoleDeveloper && message.MessageType == llm.MessageTypeEnvironment {
			if seenEnvironment {
				continue
			}
			seenEnvironment = true
			meta = append(meta, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeEnvironment, Content: message.Content})
			continue
		}
		transcript = append(transcript, message)
	}
	return meta, transcript
}

func buildReviewerTranscriptMessages(messages []llm.Message) []llm.Message {
	toolOutputsByCallID := collectReviewerToolOutputs(messages)
	toolCallIDs := collectReviewerToolCallIDs(messages)
	out := make([]llm.Message, 0, len(messages)+1)
	for _, message := range messages {
		if message.Role == llm.RoleTool {
			callID := strings.TrimSpace(message.ToolCallID)
			if callID != "" && toolCallIDs[callID] {
				continue
			}
		}
		if message.Role == llm.RoleTool && strings.TrimSpace(message.ToolCallID) == "" {
			continue
		}
		if !shouldIncludeReviewerMessage(message) {
			continue
		}
		out = append(out, llm.Message{Role: llm.RoleUser, Content: formatReviewerTranscriptEntry(message, toolOutputsByCallID)})
	}
	if len(out) == 0 {
		out = append(out, llm.Message{Role: llm.RoleUser, Content: "No reviewable transcript entries were available for this turn."})
	}
	return out
}

func collectReviewerToolOutputs(messages []llm.Message) map[string]string {
	out := make(map[string]string)
	for _, message := range messages {
		if message.Role != llm.RoleTool {
			continue
		}
		callID := strings.TrimSpace(message.ToolCallID)
		if callID == "" {
			continue
		}
		if _, exists := out[callID]; exists {
			continue
		}
		out[callID] = compactReviewerToolOutput(message.Content)
	}
	return out
}

func collectReviewerToolCallIDs(messages []llm.Message) map[string]bool {
	out := make(map[string]bool)
	for _, message := range messages {
		if message.Role != llm.RoleAssistant || len(message.ToolCalls) == 0 {
			continue
		}
		for _, call := range message.ToolCalls {
			callID := strings.TrimSpace(call.ID)
			if callID == "" {
				continue
			}
			out[callID] = true
		}
	}
	return out
}

func shouldIncludeReviewerMessage(message llm.Message) bool {
	if isShortAssistantCommentaryPreamble(message) && len(message.ToolCalls) == 0 {
		return false
	}
	if message.Role == llm.RoleDeveloper {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			return false
		}
		if message.MessageType == llm.MessageTypeEnvironment {
			return false
		}
		if message.MessageType == llm.MessageTypeErrorFeedback || message.MessageType == llm.MessageTypeInterruption {
			return false
		}
		// Backward compatibility for persisted transcripts created before message_type.
		if strings.Contains(content, environmentInjectedHeader) {
			return false
		}
		if content == commentaryWithoutToolCallsWarning || content == finalWithToolCallsIgnoredWarning || content == missingAssistantPhaseWarning || content == garbageAssistantContentWarning || content == interruptMessage {
			return false
		}
	}
	if strings.TrimSpace(message.Content) == "" && len(message.ToolCalls) == 0 {
		return false
	}
	return true
}

func formatReviewerTranscriptEntry(message llm.Message, toolOutputsByCallID map[string]string) string {
	b := strings.Builder{}
	b.WriteString(reviewerMessageLabel(message))
	content := reviewerTranscriptContent(message)
	if content != "" {
		b.WriteString("\n")
		if message.Role == llm.RoleTool {
			b.WriteString("Tool output:\n")
			b.WriteString(compactReviewerToolOutput(content))
		} else {
			b.WriteString(content)
		}
		b.WriteString("\n")
	}
	if len(message.ToolCalls) > 0 {
		b.WriteString("\nTool calls:\n")
		for i, call := range message.ToolCalls {
			b.WriteString(strconv.Itoa(i + 1))
			b.WriteString(". ")
			b.WriteString(strings.TrimSpace(call.Name))
			b.WriteString("\n")
			output := ""
			if callID := strings.TrimSpace(call.ID); callID != "" {
				output = strings.TrimSpace(toolOutputsByCallID[callID])
			}
			b.WriteString(formatReviewerToolCallPayload(call.Input, output))
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func formatReviewerToolCallPayload(input json.RawMessage, output string) string {
	payload := map[string]any{
		"input": reviewerJSONValueFromRaw(input),
	}
	if strings.TrimSpace(output) != "" {
		payload["output"] = reviewerJSONValueFromString(output)
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

func reviewerJSONValueFromRaw(raw json.RawMessage) any {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return map[string]any{}
	}
	if !json.Valid([]byte(trimmed)) {
		return trimmed
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return trimmed
	}
	return decoded
}

func reviewerJSONValueFromString(content string) any {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	if !json.Valid([]byte(trimmed)) {
		return trimmed
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return trimmed
	}
	return decoded
}

func reviewerTranscriptContent(message llm.Message) string {
	if isShortAssistantCommentaryPreamble(message) {
		return ""
	}
	return strings.TrimSpace(message.Content)
}

func isShortAssistantCommentaryPreamble(message llm.Message) bool {
	if message.Role != llm.RoleAssistant || message.Phase != llm.MessagePhaseCommentary {
		return false
	}
	content := strings.TrimSpace(message.Content)
	if content == "" {
		return false
	}
	if strings.Contains(content, "\n") {
		return false
	}
	return utf8.RuneCountInString(content) <= reviewerShortCommentaryMaxRunes
}

func reviewerMessageLabel(message llm.Message) string {
	switch message.Role {
	case llm.RoleAssistant:
		return "Agent:"
	case llm.RoleUser:
		return "User:"
	case llm.RoleTool:
		return "Tool:"
	case llm.RoleDeveloper:
		return "Developer:"
	case llm.RoleSystem:
		return "System:"
	default:
		role := strings.TrimSpace(string(message.Role))
		if role == "" {
			role = "unknown"
		}
		return fmt.Sprintf("%s:", titleCaseASCII(role))
	}
}

func titleCaseASCII(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "Unknown"
	}
	runes := []rune(trimmed)
	if len(runes) == 1 {
		return strings.ToUpper(trimmed)
	}
	return strings.ToUpper(string(runes[0])) + string(runes[1:])
}

func compactReviewerToolOutput(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "{}"
	}
	if !json.Valid([]byte(trimmed)) {
		return trimmed
	}
	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return trimmed
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return trimmed
	}
	return string(encoded)
}

func prettyReviewerJSON(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "{}"
	}
	if !json.Valid([]byte(trimmed)) {
		return trimmed
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, []byte(trimmed), "", "  "); err != nil {
		return trimmed
	}
	return formatted.String()
}

func formatReviewerDeveloperInstruction(suggestions []string) string {
	b := strings.Builder{}
	b.WriteString("Supervisor agent gave you suggestions:\n")
	for idx, suggestion := range suggestions {
		b.WriteString(strconv.Itoa(idx + 1))
		b.WriteString(". ")
		b.WriteString(suggestion)
		b.WriteString("\n")
	}
	b.WriteString("\nIf no suggestions are applicable and you don't want to say anything to the user (not the supervisor!), respond with exactly ")
	b.WriteString(reviewerNoopToken)
	b.WriteString(" and no additional text. Otherwise, address the suggestions now. The supervisor can't hear you, your response will be to the user.")
	return b.String()
}

func reviewerStatusText(status ReviewerStatus, suggestions []string) string {
	statusText := ""
	switch strings.TrimSpace(status.Outcome) {
	case "failed":
		if strings.TrimSpace(status.Error) == "" {
			statusText = "Supervisor ran: failed to generate suggestions."
			break
		}
		statusText = fmt.Sprintf("Supervisor ran: failed to generate suggestions: %s", status.Error)
	case "no_suggestions":
		statusText = "Supervisor ran: no suggestions."
	case "followup_failed":
		if strings.TrimSpace(status.Error) == "" {
			statusText = fmt.Sprintf("Supervisor ran: %s, but follow-up failed.", reviewerSuggestionCountLabel(status.SuggestionsCount))
			break
		}
		statusText = fmt.Sprintf("Supervisor ran: %s, but follow-up failed: %s", reviewerSuggestionCountLabel(status.SuggestionsCount), status.Error)
	case "noop":
		statusText = fmt.Sprintf("Supervisor ran: %s, no changes applied.", reviewerSuggestionCountLabel(status.SuggestionsCount))
	case "applied":
		statusText = fmt.Sprintf("Supervisor ran: %s, applied.", reviewerSuggestionCountLabel(status.SuggestionsCount))
	default:
		statusText = "Supervisor ran."
	}
	if len(suggestions) == 0 {
		if status.HasCacheHitPercentage {
			return statusText + "\n\n" + fmt.Sprintf("%d%% cache hit", status.CacheHitPercent)
		}
		return statusText
	}
	b := strings.Builder{}
	b.WriteString(statusText)
	b.WriteString("\n\n")
	b.WriteString("Supervisor suggestions:\n")
	for idx, suggestion := range suggestions {
		b.WriteString(strconv.Itoa(idx + 1))
		b.WriteString(". ")
		b.WriteString(strings.TrimSpace(suggestion))
		if idx < len(suggestions)-1 {
			b.WriteString("\n")
		}
	}
	if status.HasCacheHitPercentage {
		b.WriteString("\n\n")
		b.WriteString(fmt.Sprintf("%d%% cache hit", status.CacheHitPercent))
	}
	return b.String()
}

func reviewerSuggestionsText(suggestions []string) string {
	if len(suggestions) == 0 {
		return ""
	}
	b := strings.Builder{}
	b.WriteString(fmt.Sprintf("Supervisor ran: %s.", reviewerSuggestionCountLabel(len(suggestions))))
	b.WriteString("\n\n")
	b.WriteString("Supervisor suggestions:\n")
	for idx, suggestion := range suggestions {
		b.WriteString(strconv.Itoa(idx + 1))
		b.WriteString(". ")
		b.WriteString(strings.TrimSpace(suggestion))
		if idx < len(suggestions)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func reviewerSuggestionCountLabel(count int) string {
	if count <= 1 {
		return "1 suggestion"
	}
	return fmt.Sprintf("%d suggestions", count)
}

func reviewerSessionID(sessionID string) string {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return ""
	}
	return trimmed + "-review"
}

func appendMissingReviewerMetaContext(messages []llm.Message, workspaceRoot string) []llm.Message {
	haveEnvironment := false
	haveAgents := false
	for _, msg := range messages {
		if msg.Role != llm.RoleDeveloper {
			continue
		}
		if msg.MessageType == llm.MessageTypeAgentsMD {
			haveAgents = true
		}
		if msg.MessageType == llm.MessageTypeEnvironment {
			haveEnvironment = true
		}
	}
	if haveAgents && haveEnvironment {
		return messages
	}
	paths, err := agentsInjectionPaths(workspaceRoot)
	if err != nil {
		paths = nil
	}
	prefixed := make([]llm.Message, 0, len(paths)+1+len(messages))
	if !haveAgents {
		for _, path := range paths {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				if errors.Is(readErr, os.ErrNotExist) {
					continue
				}
				continue
			}
			injected := fmt.Sprintf("%s\nsource: %s\n\n```%s\n%s\n```", agentsInjectedHeader, path, agentsInjectedFenceLabel, string(data))
			prefixed = append(prefixed, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeAgentsMD, Content: injected})
		}
	}
	if !haveEnvironment {
		prefixed = append(prefixed, llm.Message{
			Role:        llm.RoleDeveloper,
			MessageType: llm.MessageTypeEnvironment,
			Content:     environmentContextMessage(workspaceRoot, time.Now()),
		})
	}
	if len(prefixed) == 0 {
		return messages
	}
	prefixed = append(prefixed, messages...)
	return prefixed
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

func summarizeOutputItemTypes(items []llm.ResponseItem) []string {
	if len(items) == 0 {
		return nil
	}
	counts := make(map[string]int, len(items))
	order := make([]string, 0, len(items))
	for _, item := range items {
		t := strings.TrimSpace(string(item.Type))
		if t == "" {
			t = "unknown"
		}
		if _, ok := counts[t]; !ok {
			order = append(order, t)
		}
		counts[t]++
	}
	out := make([]string, 0, len(order))
	for _, t := range order {
		out = append(out, fmt.Sprintf("%s:%d", t, counts[t]))
	}
	return out
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
	return e.generateWithRetryClient(ctx, e.llm, req, onDelta, onAttemptReset)
}

func (e *Engine) generateWithRetryClient(ctx context.Context, client llm.Client, req llm.Request, onDelta func(string), onAttemptReset func()) (llm.Response, error) {
	delays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	var lastErr error
	for i := 0; i <= len(delays); i++ {
		var (
			resp           llm.Response
			err            error
			attemptEmitted bool
			attemptOnDelta func(string)
			attemptDone    atomic.Bool
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
		if streamingClient, ok := client.(llm.StreamClient); ok {
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
		if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeAgentsMD, Content: injected}); err != nil {
			return err
		}
	}
	environment := environmentContextMessage(meta.WorkspaceRoot, time.Now())
	if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeEnvironment, Content: environment}); err != nil {
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
			if strings.TrimSpace(payload.Engine) == "reviewer_rollback" {
				e.chat.restoreMessagesFromItems(payload.Items)
			} else {
				e.chat.replaceHistory(payload.Items)
				e.compactionCount++
			}
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
	cacheHitPercent, hasCacheHitPercentage := e.cacheHitSnapshot()
	if used < 0 {
		used = 0
	}
	if window < 0 {
		window = 0
	}
	return ContextUsage{
		UsedTokens:            used,
		WindowTokens:          window,
		CacheHitPercent:       cacheHitPercent,
		HasCacheHitPercentage: hasCacheHitPercentage,
	}
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

func (e *Engine) SetSessionName(name string) error {
	return e.store.SetName(name)
}

func (e *Engine) SessionName() string {
	return strings.TrimSpace(e.store.Meta().Name)
}

func (e *Engine) SessionID() string {
	return strings.TrimSpace(e.store.Meta().SessionID)
}

func (e *Engine) ParentSessionID() string {
	return strings.TrimSpace(e.store.Meta().ParentSessionID)
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
	if usage.HasCachedInputTokens && usage.InputTokens > 0 {
		cachedTokens := usage.CachedInputTokens
		if cachedTokens < 0 {
			cachedTokens = 0
		}
		if cachedTokens > usage.InputTokens {
			cachedTokens = usage.InputTokens
		}
		e.totalInputTokens += usage.InputTokens
		e.totalCachedInputTokens += cachedTokens
	}
	e.mu.Unlock()
}

func (e *Engine) cacheHitSnapshot() (int, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.totalInputTokens <= 0 {
		return 0, false
	}
	cachedTokens := e.totalCachedInputTokens
	if cachedTokens < 0 {
		cachedTokens = 0
	}
	if cachedTokens > e.totalInputTokens {
		cachedTokens = e.totalInputTokens
	}
	pct := (cachedTokens * 100) / e.totalInputTokens
	if pct < 0 {
		return 0, false
	}
	if pct > 100 {
		return 100, true
	}
	return pct, true
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
