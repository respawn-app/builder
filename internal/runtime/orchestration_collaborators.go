package runtime

import (
	"context"

	"builder/internal/llm"
	"builder/internal/tools"
)

type stepLoopOptions struct {
	ReviewerFrequency              string
	ReviewerClient                 llm.Client
	EmitAssistantEvent             bool
	RefreshReviewerConfigOnResolve bool
}

type stepExecutor interface {
	RunStepLoopWithOptions(ctx context.Context, stepID string, options stepLoopOptions) (llm.Message, bool, bool, error)
}

type stepLoopRunner interface {
	RunStepLoopWithOptions(ctx context.Context, stepID string, options stepLoopOptions) (llm.Message, bool, bool, error)
}

type toolExecutor interface {
	ExecuteToolCalls(ctx context.Context, stepID string, calls []llm.ToolCall) ([]tools.Result, error)
}

type messageLifecycle interface {
	RestoreMessages() error
	InjectAgentsIfNeeded(stepID string) error
	FlushPendingUserInjections(stepID string) (int, error)
}

type reviewerPipeline interface {
	ShouldRunTurn(frequency string, reviewerClient llm.Client, patchEditsApplied bool) bool
	RunFollowUp(ctx context.Context, stepID string, original llm.Message, reviewerClient llm.Client) (llm.Message, error)
	RunSuggestions(ctx context.Context, reviewerClient llm.Client) (reviewerSuggestionsResult, error)
}

type phaseProtocolTurn struct {
	Assistant                    llm.Message
	LocalToolCalls               []llm.ToolCall
	HostedToolExecutions         []hostedToolExecution
	EnforcePhaseProtocol         bool
	MissingAssistantPhase        bool
	GarbageAssistantContent      bool
	FinalAnswerIncludedToolCalls bool
}

type phaseProtocolEnforcer interface {
	EnabledForModel(ctx context.Context) bool
	Apply(ctx context.Context, resp llm.Response, assistant llm.Message, localToolCalls []llm.ToolCall, hostedToolExecutions []hostedToolExecution) phaseProtocolTurn
}

func (e *Engine) ensureOrchestrationCollaborators() {
	e.collaboratorsOnce.Do(func() {
		if e.phaseProtocol == nil {
			e.phaseProtocol = &defaultPhaseProtocol{engine: e}
		}
		if e.messageFlow == nil {
			e.messageFlow = &defaultMessageLifecycle{engine: e}
		}
		if e.toolFlow == nil {
			e.toolFlow = &defaultToolExecutor{engine: e}
		}
		if e.reviewerFlow == nil {
			e.reviewerFlow = &defaultReviewerPipeline{engine: e}
		}
		if e.stepFlow == nil {
			e.stepFlow = &defaultStepExecutor{
				engine:   e,
				phase:    e.phaseProtocol,
				reviewer: e.reviewerFlow,
				messages: e.messageFlow,
				tools:    e.toolFlow,
			}
		}
		if reviewer, ok := e.reviewerFlow.(*defaultReviewerPipeline); ok && reviewer.stepRunner == nil {
			reviewer.stepRunner = e.stepFlow
		}
	})
}
