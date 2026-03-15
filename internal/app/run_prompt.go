package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
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

	store, err := openOrCreateSessionNonInteractive(boot.cfg.PersistenceRoot, boot.containerDir, initialSessionID, boot.cfg.WorkspaceRoot)
	if err != nil {
		return RunPromptResult{}, err
	}
	if err := ensureSubagentSessionName(store); err != nil {
		return RunPromptResult{}, err
	}

	active := effectiveSettings(boot.cfg.Settings, store.Meta().Locked)
	if err := store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: active.OpenAIBaseURL}); err != nil {
		return RunPromptResult{}, err
	}
	enabledTools := activeToolIDs(active, store.Meta().Locked)

	logger, err := newRunLogger(store.Dir())
	if err != nil {
		return RunPromptResult{}, err
	}
	defer func() {
		_ = logger.Close()
	}()

	logger.Logf("app.run_prompt.start session_id=%s workspace=%s model=%s", store.Meta().SessionID, boot.cfg.WorkspaceRoot, active.Model)
	logger.Logf("config.settings path=%s created=%t", boot.cfg.Source.SettingsPath, boot.cfg.Source.CreatedDefaultConfig)
	for _, line := range configSourceLines(boot.cfg.Source) {
		logger.Logf("config.source %s", line)
	}

	wiring, err := newRuntimeWiringWithBackground(store, active, enabledTools, boot.cfg.WorkspaceRoot, boot.authManager, logger, boot.background, runtimeWiringOptions{
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
	if boot.backgroundRouter != nil {
		boot.backgroundRouter.SetActiveSession(store.Meta().SessionID, wiring.engine)
	}
	defer func() {
		if boot.backgroundRouter != nil {
			boot.backgroundRouter.ClearActiveSession(store.Meta().SessionID)
		}
		_ = wiring.Close()
	}()

	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	startedAt := time.Now()
	assistant, runErr := wiring.engine.SubmitUserMessage(runCtx, prompt)
	duration := time.Since(startedAt)
	result := RunPromptResult{
		SessionID:   wiring.engine.SessionID(),
		SessionName: wiring.engine.SessionName(),
		Result:      assistant.Content,
		Duration:    duration,
	}
	if dropped := wiring.eventBridge.Dropped(); dropped > 0 {
		logger.Logf("runtime.event.drop.total=%d", dropped)
	}
	if runErr != nil {
		logger.Logf("app.run_prompt.exit err=%q", runErr.Error())
		return result, runErr
	}
	logger.Logf("app.run_prompt.exit ok")
	return result, nil
}

func openOrCreateSessionNonInteractive(persistenceRoot, containerDir, selectedID, workspaceRoot string) (*session.Store, error) {
	if strings.TrimSpace(selectedID) != "" {
		return session.OpenByID(persistenceRoot, selectedID)
	}
	containerName := filepath.Base(containerDir)
	return session.NewLazy(containerDir, containerName, workspaceRoot)
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
