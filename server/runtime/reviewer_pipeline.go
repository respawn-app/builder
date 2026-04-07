package runtime

import (
	"context"
	"fmt"
	"strings"

	"builder/server/llm"
)

type defaultReviewerPipeline struct {
	engine     *Engine
	stepRunner stepLoopRunner
}

func (r *defaultReviewerPipeline) ShouldRunTurn(frequency string, reviewerClient llm.Client, patchEditsApplied bool) bool {
	if reviewerClient == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(frequency)) {
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

func (r *defaultReviewerPipeline) RunFollowUp(ctx context.Context, stepID string, original llm.Message, reviewerClient llm.Client) (llm.Message, error) {
	e := r.engine
	baselineItems := e.snapshotItems()
	e.emit(Event{Kind: EventReviewerStarted, StepID: stepID})
	reviewerResult, err := r.RunSuggestions(ctx, stepID, reviewerClient)
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
	if e.cfg.Reviewer.VerboseOutput {
		_ = e.appendPersistedLocalEntryWithOngoingText(
			stepID,
			"reviewer_suggestions",
			reviewerSuggestionsText(suggestions),
			reviewerSuggestionsText(suggestions),
		)
	}

	instruction := formatReviewerDeveloperInstruction(suggestions)
	if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeReviewerFeedback, Content: instruction}); err != nil {
		return original, err
	}
	if r.stepRunner == nil {
		status := ReviewerStatus{
			Outcome:          "followup_failed",
			SuggestionsCount: len(suggestions),
			Error:            "reviewer step runner is not configured",
		}
		e.emit(Event{Kind: EventReviewerCompleted, StepID: stepID, Reviewer: &status})
		_ = e.appendPersistedLocalEntry(stepID, "reviewer_status", reviewerStatusText(status, nil))
		return original, nil
	}

	followUp, followUpExecutedToolCall, noopFinalAnswer, err := r.stepRunner.RunStepLoopWithOptions(ctx, stepID, stepLoopOptions{
		ReviewerFrequency:              "off",
		ReviewerClient:                 nil,
		EmitAssistantEvent:             false,
		RefreshReviewerConfigOnResolve: false,
	})
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
	if noopFinalAnswer || isNoopFinalAnswer(followUp) {
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

func (r *defaultReviewerPipeline) RunSuggestions(ctx context.Context, stepID string, reviewerClient llm.Client) (reviewerSuggestionsResult, error) {
	e := r.engine
	if reviewerClient == nil {
		return reviewerSuggestionsResult{}, nil
	}
	req, err := e.buildReviewerRequest(ctx, reviewerClient)
	if err != nil {
		return reviewerSuggestionsResult{}, fmt.Errorf("build reviewer request: %w", err)
	}
	resp, err := e.generateWithRetryClient(ctx, stepID, reviewerClient, req, nil, nil, nil)
	if err != nil {
		return reviewerSuggestionsResult{}, err
	}
	cachePct, hasCachePct := resp.Usage.CacheHitPercent()
	return reviewerSuggestionsResult{
		Suggestions:           parseReviewerSuggestionsObject(resp.Assistant.Content),
		CacheHitPercent:       cachePct,
		HasCacheHitPercentage: hasCachePct,
	}, nil
}
