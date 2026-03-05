package runtime

import (
	"context"
	"strings"

	"builder/internal/llm"
	"builder/internal/tools"
)

type defaultStepExecutor struct {
	engine   *Engine
	phase    phaseProtocolEnforcer
	reviewer reviewerPipeline
	messages messageLifecycle
	tools    toolExecutor
}

func (s *defaultStepExecutor) RunStepLoopWithOptions(ctx context.Context, stepID string, options stepLoopOptions) (llm.Message, bool, error) {
	e := s.engine
	executedToolCall := false
	patchEditsApplied := false
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

		phaseTurn := s.phase.Apply(ctx, resp, resp.Assistant, localToolCalls, hostedToolExecutions)
		assistantMsg := phaseTurn.Assistant
		localToolCalls = phaseTurn.LocalToolCalls
		hostedToolExecutions = phaseTurn.HostedToolExecutions

		if err := e.appendAssistantMessage(stepID, assistantMsg); err != nil {
			return llm.Message{}, executedToolCall, err
		}
		if err := e.appendReasoningEntries(stepID, resp.Reasoning); err != nil {
			return llm.Message{}, executedToolCall, err
		}
		if phaseTurn.MissingAssistantPhase {
			if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: missingAssistantPhaseWarning}); err != nil {
				return llm.Message{}, executedToolCall, err
			}
		}
		if phaseTurn.GarbageAssistantContent {
			if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: garbageAssistantContentWarning}); err != nil {
				return llm.Message{}, executedToolCall, err
			}
		}
		if phaseTurn.FinalAnswerIncludedToolCalls {
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
			if phaseTurn.GarbageAssistantContent {
				if _, err := s.messages.FlushPendingUserInjections(stepID); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				continue
			}
			if phaseTurn.MissingAssistantPhase {
				if _, err := s.messages.FlushPendingUserInjections(stepID); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				continue
			}
			if phaseTurn.EnforcePhaseProtocol && assistantMsg.Phase != llm.MessagePhaseFinal {
				if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: commentaryWithoutToolCallsWarning}); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				if _, err := s.messages.FlushPendingUserInjections(stepID); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				continue
			}
			if phaseTurn.EnforcePhaseProtocol && assistantMsg.Phase == llm.MessagePhaseFinal && strings.TrimSpace(assistantMsg.Content) == "" {
				if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: finalWithoutContentWarning}); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				if _, err := s.messages.FlushPendingUserInjections(stepID); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
					return llm.Message{}, executedToolCall, err
				}
				continue
			}
			flushed, err := s.messages.FlushPendingUserInjections(stepID)
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
			effectiveReviewerFrequency := options.ReviewerFrequency
			effectiveReviewerClient := options.ReviewerClient
			if options.RefreshReviewerConfigOnResolve {
				effectiveReviewerFrequency, effectiveReviewerClient = e.reviewerTurnConfigSnapshot()
			}
			if s.reviewer.ShouldRunTurn(effectiveReviewerFrequency, effectiveReviewerClient, patchEditsApplied) {
				reviewed, err := s.reviewer.RunFollowUp(ctx, stepID, assistantMsg, effectiveReviewerClient)
				if err == nil {
					resolved = reviewed
				}
			}
			if options.EmitAssistantEvent {
				e.emit(Event{Kind: EventAssistantMessage, StepID: stepID, Message: resolved})
			}
			return resolved, executedToolCall, nil
		}

		results, err := s.tools.ExecuteToolCalls(ctx, stepID, localToolCalls)
		if err != nil {
			return llm.Message{}, executedToolCall, err
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
				return llm.Message{}, executedToolCall, err
			}
		}

		if _, err := s.messages.FlushPendingUserInjections(stepID); err != nil {
			return llm.Message{}, executedToolCall, err
		}
		if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
			return llm.Message{}, executedToolCall, err
		}
	}
}
