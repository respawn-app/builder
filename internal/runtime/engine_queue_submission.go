package runtime

import (
	"context"

	"builder/internal/llm"
)

// SubmitQueuedUserMessages starts a fresh step from already-queued injected user
// messages or background notices. This is used when a non-turn busy operation
// (for example manual compaction) completes while queued steering is waiting.
func (e *Engine) SubmitQueuedUserMessages(ctx context.Context) (assistant llm.Message, err error) {
	e.ensureOrchestrationCollaborators()
	err = e.stepLifecycle.Run(ctx, exclusiveStepOptions{EmitRunState: true}, func(stepCtx context.Context, stepID string) error {
		if err := e.injectAgentsIfNeeded(stepID); err != nil {
			return err
		}
		if err := e.injectHeadlessModeTransitionPromptIfNeeded(stepID); err != nil {
			return err
		}
		flushed, err := e.flushPendingUserInjections(stepID)
		if err != nil {
			return err
		}
		if flushed == 0 {
			return nil
		}
		msg, runErr := e.runStepLoop(stepCtx, stepID)
		assistant = msg
		return runErr
	})
	return assistant, err
}

func (e *Engine) HasQueuedUserWork() bool {
	e.ensureOrchestrationCollaborators()
	e.mu.Lock()
	hasInjected := len(e.pendingInjected) > 0
	e.mu.Unlock()
	if hasInjected {
		return true
	}
	if e.backgroundFlow != nil && e.backgroundFlow.HasPendingNotices() {
		return true
	}
	return false
}
