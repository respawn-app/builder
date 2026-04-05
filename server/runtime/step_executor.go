package runtime

import (
	"context"
	"strings"

	"builder/server/llm"
	"builder/server/tools"
)

type defaultStepExecutor struct {
	engine   *Engine
	phase    phaseProtocolEnforcer
	reviewer reviewerPipeline
	messages messageLifecycle
	tools    toolExecutor
}

func (s *defaultStepExecutor) RunStepLoopWithOptions(ctx context.Context, stepID string, options stepLoopOptions) (llm.Message, bool, bool, error) {
	e := s.engine
	executedToolCall := false
	patchEditsApplied := false
	deferredFinal := llm.Message{}
	hasDeferredFinal := false
	for {
		if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
			return llm.Message{}, executedToolCall, false, err
		}

		req, err := e.buildRequest(ctx, stepID, true)
		if err != nil {
			return llm.Message{}, executedToolCall, false, err
		}

		resp, err := e.generateWithRetry(
			ctx,
			req,
			func(delta string) {
				e.chat.appendOngoingDelta(delta)
				e.emit(Event{Kind: EventAssistantDelta, StepID: stepID, AssistantDelta: delta})
			},
			func(delta llm.ReasoningSummaryDelta) {
				e.emit(Event{Kind: EventReasoningDelta, StepID: stepID, ReasoningDelta: &delta})
			},
			func() {
				e.chat.clearOngoing()
				e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
				e.emit(Event{Kind: EventAssistantDeltaReset, StepID: stepID})
				e.emit(Event{Kind: EventReasoningDeltaReset, StepID: stepID})
			},
		)
		if err != nil {
			return llm.Message{}, executedToolCall, false, err
		}
		e.setLastUsage(resp.Usage)

		localToolCalls := append([]llm.ToolCall(nil), resp.ToolCalls...)
		hostedToolExecutions := hostedToolExecutionsFromOutputItems(resp.OutputItems, tools.DefinitionsFor(e.cfg.EnabledTools))
		if len(localToolCalls) > 0 || len(hostedToolExecutions) > 0 {
			executedToolCall = true
		}

		phaseTurn := s.phase.Apply(ctx, resp, resp.Assistant, localToolCalls, hostedToolExecutions)
		assistantMsg := phaseTurn.Assistant
		localToolCalls = phaseTurn.LocalToolCalls
		hostedToolExecutions = phaseTurn.HostedToolExecutions
		noopFinalAnswer := isNoopFinalAnswer(assistantMsg)
		if noopFinalAnswer {
			e.clearStreamingAssistantState(stepID)
		}
		if !noopFinalAnswer {
			e.emit(Event{
				Kind:   EventModelResponse,
				StepID: stepID,
				ModelResponse: &ModelResponseTrace{
					AssistantPhase:   assistantMsg.Phase,
					AssistantChars:   len(assistantMsg.Content),
					ToolCallsCount:   len(resp.ToolCalls),
					OutputItemsCount: len(resp.OutputItems),
					OutputItemTypes:  summarizeOutputItemTypes(resp.OutputItems),
				},
			})
		}

		if !noopFinalAnswer {
			if err := e.appendAssistantMessage(stepID, assistantMsg); err != nil {
				return llm.Message{}, executedToolCall, false, err
			}
			if liveAssistant, ok := liveCommittedAssistantEventMessage(assistantMsg); ok && options.EmitAssistantEvent {
				e.emit(Event{Kind: EventAssistantMessage, StepID: stepID, Message: liveAssistant})
			}
			if err := e.appendReasoningEntries(stepID, resp.Reasoning); err != nil {
				return llm.Message{}, executedToolCall, false, err
			}
			if phaseTurn.MissingAssistantPhase {
				if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: missingAssistantPhaseWarning}); err != nil {
					return llm.Message{}, executedToolCall, false, err
				}
			}
			if phaseTurn.FinalAnswerIncludedToolCalls {
				if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: finalWithToolCallsIgnoredWarning}); err != nil {
					return llm.Message{}, executedToolCall, false, err
				}
			}
		}

		for _, hosted := range hostedToolExecutions {
			if err := e.persistToolCompletion(stepID, hosted.Result); err != nil {
				return llm.Message{}, executedToolCall, false, err
			}
			msg := llm.Message{
				Role:       llm.RoleTool,
				Content:    string(hosted.Result.Output),
				ToolCallID: hosted.Result.CallID,
				Name:       string(hosted.Result.Name),
			}
			if err := e.appendMessage(stepID, msg); err != nil {
				return llm.Message{}, executedToolCall, false, err
			}
		}

		if len(localToolCalls) == 0 {
			if phaseTurn.MissingAssistantPhase {
				if _, err := s.messages.FlushPendingUserInjections(stepID); err != nil {
					return llm.Message{}, executedToolCall, false, err
				}
				if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
					return llm.Message{}, executedToolCall, false, err
				}
				if err := e.maybeAppendCompactionSoonReminder(ctx, stepID); err != nil {
					return llm.Message{}, executedToolCall, false, err
				}
				continue
			}
			if phaseTurn.EnforcePhaseProtocol && assistantMsg.Phase != llm.MessagePhaseFinal {
				if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: commentaryWithoutToolCallsWarning}); err != nil {
					return llm.Message{}, executedToolCall, false, err
				}
				if _, err := s.messages.FlushPendingUserInjections(stepID); err != nil {
					return llm.Message{}, executedToolCall, false, err
				}
				if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
					return llm.Message{}, executedToolCall, false, err
				}
				if err := e.maybeAppendCompactionSoonReminder(ctx, stepID); err != nil {
					return llm.Message{}, executedToolCall, false, err
				}
				continue
			}
			if phaseTurn.EnforcePhaseProtocol && assistantMsg.Phase == llm.MessagePhaseFinal && strings.TrimSpace(assistantMsg.Content) == "" && !noopFinalAnswer {
				if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: finalWithoutContentWarning}); err != nil {
					return llm.Message{}, executedToolCall, false, err
				}
				if _, err := s.messages.FlushPendingUserInjections(stepID); err != nil {
					return llm.Message{}, executedToolCall, false, err
				}
				if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
					return llm.Message{}, executedToolCall, false, err
				}
				if err := e.maybeAppendCompactionSoonReminder(ctx, stepID); err != nil {
					return llm.Message{}, executedToolCall, false, err
				}
				continue
			}
			flushed, err := s.messages.FlushPendingUserInjections(stepID)
			if err != nil {
				return llm.Message{}, executedToolCall, false, err
			}
			if flushed > 0 {
				if assistantMsg.Phase == llm.MessagePhaseFinal && strings.TrimSpace(assistantMsg.Content) != "" && !noopFinalAnswer {
					deferredFinal = assistantMsg
					hasDeferredFinal = true
				}
				if err := e.maybeAppendCompactionSoonReminder(ctx, stepID); err != nil {
					return llm.Message{}, executedToolCall, false, err
				}
				continue
			}
			if len(hostedToolExecutions) > 0 {
				if err := e.maybeAppendCompactionSoonReminder(ctx, stepID); err != nil {
					return llm.Message{}, executedToolCall, false, err
				}
				continue
			}
			resolved := assistantMsg
			resolvedNoopFinalAnswer := noopFinalAnswer
			if hasDeferredFinal {
				resolved = deferredFinal
				resolvedNoopFinalAnswer = isNoopFinalAnswer(resolved)
				hasDeferredFinal = false
			}
			if resolvedNoopFinalAnswer {
				return resolved, executedToolCall, true, nil
			}
			effectiveReviewerFrequency := options.ReviewerFrequency
			effectiveReviewerClient := options.ReviewerClient
			if options.RefreshReviewerConfigOnResolve {
				effectiveReviewerFrequency, effectiveReviewerClient = e.reviewerTurnConfigSnapshot()
			}
			if s.reviewer.ShouldRunTurn(effectiveReviewerFrequency, effectiveReviewerClient, patchEditsApplied) {
				reviewed, err := s.reviewer.RunFollowUp(ctx, stepID, resolved, effectiveReviewerClient)
				if err == nil {
					resolved = reviewed
				}
			}
			if err := e.maybeAppendCompactionSoonReminder(ctx, stepID); err != nil {
				return llm.Message{}, executedToolCall, false, err
			}
			if options.EmitAssistantEvent {
				e.emit(Event{Kind: EventAssistantMessage, StepID: stepID, Message: resolved})
			}
			return resolved, executedToolCall, false, nil
		}

		results, err := s.tools.ExecuteToolCalls(ctx, stepID, localToolCalls)
		if err != nil {
			return llm.Message{}, executedToolCall, false, err
		}

		for _, result := range results {
			if result.Name == tools.ToolPatch && !result.IsError {
				patchEditsApplied = true
			}
			msg := llm.Message{
				Role:       llm.RoleTool,
				Content:    string(result.Output),
				ToolCallID: result.CallID,
				Name:       string(result.Name),
			}
			if err := e.appendMessage(stepID, msg); err != nil {
				return llm.Message{}, executedToolCall, false, err
			}
		}

		if _, err := s.messages.FlushPendingUserInjections(stepID); err != nil {
			return llm.Message{}, executedToolCall, false, err
		}
		if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
			return llm.Message{}, executedToolCall, false, err
		}
		if err := e.maybeAppendCompactionSoonReminder(ctx, stepID); err != nil {
			return llm.Message{}, executedToolCall, false, err
		}
	}
}

func liveCommittedAssistantEventMessage(msg llm.Message) (llm.Message, bool) {
	if msg.Phase != llm.MessagePhaseCommentary {
		return llm.Message{}, false
	}
	if strings.TrimSpace(msg.Content) == "" {
		return llm.Message{}, false
	}
	return llm.Message{
		Role:    llm.RoleAssistant,
		Content: msg.Content,
		Phase:   msg.Phase,
	}, true
}
