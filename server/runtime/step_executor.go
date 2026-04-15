package runtime

import (
	"context"
	"strings"

	"builder/server/llm"
	"builder/server/tools"
	"builder/shared/toolspec"
)

type defaultStepExecutor struct {
	engine   *Engine
	phase    phaseProtocolEnforcer
	reviewer reviewerPipeline
	messages messageLifecycle
	tools    toolExecutor
}

func (s *defaultStepExecutor) RunStepLoopWithOptions(ctx context.Context, stepID string, options stepLoopOptions) (stepLoopResult, error) {
	e := s.engine
	executedToolCall := false
	patchEditsApplied := false
	deferredFinal := llm.Message{}
	deferredFinalCommittedStart := -1
	hasDeferredFinal := false
	for {
		if err := s.prepareModelTurn(ctx, stepID); err != nil {
			return stepLoopResult{}, err
		}

		req, err := e.buildRequest(ctx, stepID, true)
		if err != nil {
			return stepLoopResult{}, err
		}

		resp, err := e.generateWithRetry(
			ctx,
			stepID,
			req,
			func(delta string) {
				e.chat.appendOngoingDelta(delta)
				e.emit(Event{Kind: EventAssistantDelta, StepID: stepID, AssistantDelta: delta})
			},
			func(delta llm.ReasoningSummaryDelta) {
				e.emit(Event{Kind: EventReasoningDelta, StepID: stepID, ReasoningDelta: &delta})
			},
			func() {
				e.clearStreamingAssistantState(stepID)
			},
		)
		if err != nil {
			return stepLoopResult{}, err
		}
		if err := e.recordLastUsage(resp.Usage); err != nil {
			return stepLoopResult{}, err
		}

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
		assistantCommittedStart := -1
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
			if err := e.appendAssistantMessage(stepID, assistantMsg); err != nil {
				return stepLoopResult{}, err
			}
			executableCallIDs := make(map[string]struct{}, len(localToolCalls))
			for _, call := range localToolCalls {
				if callID := strings.TrimSpace(call.ID); callID != "" {
					executableCallIDs[callID] = struct{}{}
				}
			}
			toolCallStarts := map[string]int(nil)
			assistantCommittedStart, toolCallStarts = committedStartsForPersistedAssistantMessage(e, assistantMsg, executableCallIDs)
			e.rememberPendingToolCallStarts(toolCallStarts)
			if liveAssistant, ok := liveCommittedAssistantEventMessage(assistantMsg); ok && options.EmitAssistantEvent {
				e.emit(Event{
					Kind:                       EventAssistantMessage,
					StepID:                     stepID,
					Message:                    liveAssistant,
					CommittedTranscriptChanged: true,
					CommittedEntryStart:        assistantCommittedStart,
					CommittedEntryStartSet:     assistantCommittedStart >= 0,
				})
			}
			if err := e.appendReasoningEntries(stepID, resp.Reasoning); err != nil {
				return stepLoopResult{}, err
			}
			if phaseTurn.MissingAssistantPhase {
				if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: missingAssistantPhaseWarning}); err != nil {
					return stepLoopResult{}, err
				}
			}
			if phaseTurn.FinalAnswerIncludedToolCalls {
				if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: finalWithToolCallsIgnoredWarning}); err != nil {
					return stepLoopResult{}, err
				}
			}
		}

		for _, hosted := range hostedToolExecutions {
			if err := e.persistToolCompletion(stepID, hosted.Result); err != nil {
				return stepLoopResult{}, err
			}
			msg := llm.Message{Role: llm.RoleTool, Content: string(hosted.Result.Output), ToolCallID: hosted.Result.CallID, Name: string(hosted.Result.Name)}
			if err := e.appendMessage(stepID, msg); err != nil {
				return stepLoopResult{}, err
			}
		}

		if len(localToolCalls) == 0 {
			if phaseTurn.MissingAssistantPhase {
				if _, err := s.messages.FlushPendingUserInjections(stepID); err != nil {
					return stepLoopResult{}, err
				}
				continue
			}
			if phaseTurn.EnforcePhaseProtocol && assistantMsg.Phase != llm.MessagePhaseFinal {
				if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: commentaryWithoutToolCallsWarning}); err != nil {
					return stepLoopResult{}, err
				}
				if _, err := s.messages.FlushPendingUserInjections(stepID); err != nil {
					return stepLoopResult{}, err
				}
				continue
			}
			if phaseTurn.EnforcePhaseProtocol && assistantMsg.Phase == llm.MessagePhaseFinal && strings.TrimSpace(assistantMsg.Content) == "" && !noopFinalAnswer {
				if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: finalWithoutContentWarning}); err != nil {
					return stepLoopResult{}, err
				}
				if _, err := s.messages.FlushPendingUserInjections(stepID); err != nil {
					return stepLoopResult{}, err
				}
				continue
			}

			flushed, err := s.messages.FlushPendingUserInjections(stepID)
			if err != nil {
				return stepLoopResult{}, err
			}
			if flushed > 0 {
				if assistantMsg.Phase == llm.MessagePhaseFinal && strings.TrimSpace(assistantMsg.Content) != "" && !noopFinalAnswer {
					deferredFinal = assistantMsg
					deferredFinalCommittedStart = assistantCommittedStart
					hasDeferredFinal = true
				}
				continue
			}
			if len(hostedToolExecutions) > 0 {
				continue
			}

			resolved := assistantMsg
			resolvedNoopFinalAnswer := noopFinalAnswer
			resolvedCommittedStart := assistantCommittedStart
			resolvedCommittedStartSet := assistantCommittedStart >= 0
			var reviewerCompletion *ReviewerStatus
			if hasDeferredFinal {
				resolved = deferredFinal
				resolvedNoopFinalAnswer = isNoopFinalAnswer(resolved)
				resolvedCommittedStart = deferredFinalCommittedStart
				resolvedCommittedStartSet = deferredFinalCommittedStart >= 0
				hasDeferredFinal = false
				deferredFinalCommittedStart = -1
			}
			if resolvedNoopFinalAnswer {
				return stepLoopResult{Message: resolved, ExecutedToolCall: executedToolCall, NoopFinalAnswer: true, AssistantCommittedStart: resolvedCommittedStart, AssistantCommittedStartSet: resolvedCommittedStartSet}, nil
			}

			effectiveReviewerFrequency := options.ReviewerFrequency
			effectiveReviewerClient := options.ReviewerClient
			if options.RefreshReviewerConfigOnResolve {
				effectiveReviewerFrequency, effectiveReviewerClient = e.reviewerTurnConfigSnapshot()
			}
			if s.reviewer.ShouldRunTurn(effectiveReviewerFrequency, effectiveReviewerClient, patchEditsApplied) {
				reviewed, err := s.reviewer.RunFollowUp(ctx, stepID, resolved, resolvedCommittedStart, resolvedCommittedStartSet, effectiveReviewerClient)
				if err == nil {
					resolved = reviewed.Message
					reviewerCompletion = reviewed.Completion
					resolvedCommittedStart = reviewed.AssistantCommittedStart
					resolvedCommittedStartSet = reviewed.AssistantCommittedStartSet
				}
			}
			if options.EmitAssistantEvent {
				e.emit(Event{Kind: EventAssistantMessage, StepID: stepID, Message: resolved, CommittedTranscriptChanged: true, CommittedEntryStart: resolvedCommittedStart, CommittedEntryStartSet: resolvedCommittedStartSet})
			}
			if reviewerCompletion != nil {
				if err := e.appendPersistedLocalEntry(stepID, "reviewer_status", reviewerStatusText(*reviewerCompletion, nil)); err != nil {
					return stepLoopResult{}, err
				}
				e.emit(Event{Kind: EventReviewerCompleted, StepID: stepID, Reviewer: reviewerCompletion})
			}
			return stepLoopResult{Message: resolved, ExecutedToolCall: executedToolCall, AssistantCommittedStart: resolvedCommittedStart, AssistantCommittedStartSet: resolvedCommittedStartSet}, nil
		}

		results, err := s.tools.ExecuteToolCalls(ctx, stepID, localToolCalls)
		if err != nil {
			return stepLoopResult{}, err
		}
		for _, result := range results {
			if result.Name == toolspec.ToolPatch && !result.IsError {
				patchEditsApplied = true
			}
			msg := llm.Message{Role: llm.RoleTool, Content: string(result.Output), ToolCallID: result.CallID, Name: string(result.Name)}
			if err := e.appendMessage(stepID, msg); err != nil {
				return stepLoopResult{}, err
			}
		}
		if _, err := s.messages.FlushPendingUserInjections(stepID); err != nil {
			return stepLoopResult{}, err
		}
	}
}

func (s *defaultStepExecutor) prepareModelTurn(ctx context.Context, stepID string) error {
	e := s.engine
	if err := e.applyPendingHandoffIfNeeded(ctx, stepID); err != nil {
		return err
	}
	if err := e.autoCompactIfNeeded(ctx, stepID, compactionModeAuto); err != nil {
		return err
	}
	return e.maybeAppendCompactionSoonReminder(ctx, stepID)
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

func committedStartsForPersistedAssistantMessage(e *Engine, msg llm.Message, executableCallIDs map[string]struct{}) (int, map[string]int) {
	if e == nil {
		return -1, nil
	}
	persisted := normalizeMessageForTranscript(msg, e.store.Meta().WorkspaceRoot)
	entries := VisibleChatEntriesFromMessage(persisted)
	if len(entries) == 0 {
		return -1, nil
	}
	start := e.CommittedTranscriptEntryCount() - len(entries)
	if start < 0 {
		return -1, nil
	}
	toolCallStarts := make(map[string]int)
	for idx, entry := range entries {
		if strings.TrimSpace(entry.Role) != "tool_call" {
			continue
		}
		callID := strings.TrimSpace(entry.ToolCallID)
		if callID == "" {
			continue
		}
		if _, ok := executableCallIDs[callID]; !ok {
			continue
		}
		toolCallStarts[callID] = start + idx
	}
	return start, toolCallStarts
}
