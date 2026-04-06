package runtime

import (
	"context"

	"builder/server/llm"
	"builder/server/tools"
)

type exclusiveStepOptions struct {
	EmitRunState        bool
	PersistRunLifecycle bool
}

type exclusiveStepLifecycle interface {
	Run(ctx context.Context, options exclusiveStepOptions, fn func(stepCtx context.Context, stepID string) error) error
	Interrupt() error
	IsBusy() bool
	Snapshot() *RunSnapshot
}

type backgroundNoticeScheduler interface {
	HandleBackgroundShellUpdate(evt BackgroundShellEvent, queueNotice bool)
	QueueDeveloperNotice(msg llm.Message)
	DrainPendingNotices() []llm.Message
	HasPendingNotices() bool
	ConsumePendingBackgroundNotice(sessionID string) bool
	ScheduleIfIdle()
}

type contextCompactor interface {
	CompactContext(ctx context.Context, args string) error
	CompactContextForPreSubmit(ctx context.Context) error
	AutoCompactIfNeeded(ctx context.Context, stepID string, mode compactionMode) error
	ShouldCompactBeforeUserMessage(ctx context.Context, text string) (bool, error)
}

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
	RunSuggestions(ctx context.Context, stepID string, reviewerClient llm.Client) (reviewerSuggestionsResult, error)
}

type phaseProtocolTurn struct {
	Assistant                    llm.Message
	LocalToolCalls               []llm.ToolCall
	HostedToolExecutions         []hostedToolExecution
	EnforcePhaseProtocol         bool
	MissingAssistantPhase        bool
	FinalAnswerIncludedToolCalls bool
}

type phaseProtocolEnforcer interface {
	EnabledForModel(ctx context.Context) bool
	Apply(ctx context.Context, resp llm.Response, assistant llm.Message, localToolCalls []llm.ToolCall, hostedToolExecutions []hostedToolExecution) phaseProtocolTurn
}

func (e *Engine) ensureOrchestrationCollaborators() {
	e.collaboratorsOnce.Do(func() {
		if e.stepLifecycle == nil {
			e.stepLifecycle = &defaultExclusiveStepLifecycle{engine: e}
		}
		if e.backgroundFlow == nil {
			e.backgroundFlow = &defaultBackgroundNoticeScheduler{engine: e, steps: e.stepLifecycle}
		}
		if lifecycle, ok := e.stepLifecycle.(*defaultExclusiveStepLifecycle); ok && lifecycle.background == nil {
			lifecycle.background = e.backgroundFlow
		}
		if e.phaseProtocol == nil {
			e.phaseProtocol = &defaultPhaseProtocol{engine: e}
		}
		if e.messageFlow == nil {
			e.messageFlow = &defaultMessageLifecycle{engine: e, background: e.backgroundFlow}
		}
		if e.toolFlow == nil {
			e.toolFlow = &defaultToolExecutor{engine: e}
		}
		if e.compactionFlow == nil {
			e.compactionFlow = &defaultContextCompactor{engine: e, steps: e.stepLifecycle}
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
