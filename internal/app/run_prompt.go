package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"builder/internal/runtime"
	"builder/internal/session"
	"builder/internal/tools/askquestion"
)

const subagentSessionSuffix = "subagent"

type RunPromptResult struct {
	SessionID   string
	SessionName string
	Result      string
	Duration    time.Duration
}

func runPrompt(ctx context.Context, boot appBootstrap, initialSessionID, prompt string, timeout time.Duration, progress io.Writer) (RunPromptResult, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return RunPromptResult{}, errors.New("prompt is required")
	}

	planner := newSessionLaunchPlanner(&boot)
	plan, err := planner.PlanSession(sessionLaunchRequest{
		Mode:              launchModeHeadless,
		SelectedSessionID: initialSessionID,
	})
	if err != nil {
		return RunPromptResult{}, err
	}
	runtimePlan, err := planner.PrepareRuntime(plan, progress, "app.run_prompt.start session_id="+plan.Store.Meta().SessionID+" workspace="+plan.WorkspaceRoot+" model="+plan.ActiveSettings.Model, runtimeWiringOptions{
		AskHandler: runPromptAskHandler,
		Headless:   true,
		FastMode:   boot.fastModeState,
		OnEvent: func(evt runtime.Event) {
			writeRunProgressEvent(progress, evt)
		},
	})
	if err != nil {
		return RunPromptResult{}, err
	}
	defer func() {
		runtimePlan.Close()
	}()

	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	startedAt := time.Now()
	assistant, runErr := runtimePlan.Wiring.engine.SubmitUserMessage(runCtx, prompt)
	duration := time.Since(startedAt)
	result := RunPromptResult{
		SessionID:   runtimePlan.Wiring.engine.SessionID(),
		SessionName: runtimePlan.Wiring.engine.SessionName(),
		Result:      assistant.Content,
		Duration:    duration,
	}
	if dropped := runtimePlan.Wiring.eventBridge.Dropped(); dropped > 0 {
		runtimePlan.Logger.Logf("runtime.event.drop.total=%d", dropped)
	}
	if runErr != nil {
		runtimePlan.Logger.Logf("app.run_prompt.exit err=%q", runErr.Error())
		return result, runErr
	}
	runtimePlan.Logger.Logf("app.run_prompt.exit ok")
	return result, nil
}

func ensureSubagentSessionName(store *session.Store) error {
	if store == nil {
		return errors.New("session store is required")
	}
	meta := store.Meta()
	if strings.TrimSpace(meta.Name) != "" {
		return nil
	}
	name := strings.TrimSpace(meta.SessionID + " " + subagentSessionSuffix)
	if name == "" {
		return nil
	}
	return store.SetName(name)
}

func runPromptAskHandler(req askquestion.Request) (askquestion.Response, error) {
	return askquestion.Response{}, errors.New("You can't ask questions in headless/background mode. If the question is critical and materially affects the task, ask it by ending your turn after trying to do as much work as possible beforehand. Otherwise, follow best practice and mention the ambiguity in your final answer.")
}

func writeRunProgressEvent(w io.Writer, evt runtime.Event) {
	if w == nil {
		return
	}
	switch evt.Kind {
	case runtime.EventToolCallStarted, runtime.EventToolCallCompleted, runtime.EventReviewerCompleted, runtime.EventCompactionStarted, runtime.EventCompactionCompleted, runtime.EventCompactionFailed, runtime.EventInFlightClearFailed:
		_, _ = fmt.Fprintln(w, formatRuntimeEvent(evt))
	}
}
