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

	store, err := openOrCreateSessionNonInteractive(boot.containerDir, initialSessionID, boot.cfg.WorkspaceRoot)
	if err != nil {
		return RunPromptResult{}, err
	}
	if err := ensureSubagentSessionName(store); err != nil {
		return RunPromptResult{}, err
	}

	active := effectiveSettings(boot.cfg.Settings, store.Meta().Locked)
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

	wiring, err := newRuntimeWiring(store, active, enabledTools, boot.cfg.WorkspaceRoot, boot.authManager, logger, runtimeWiringOptions{
		AskHandler: runPromptAskHandler,
		OnEvent: func(evt runtime.Event) {
			writeRunProgressEvent(progress, evt)
		},
	})
	if err != nil {
		return RunPromptResult{}, err
	}

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

func openOrCreateSessionNonInteractive(containerDir, selectedID, workspaceRoot string) (*session.Store, error) {
	if strings.TrimSpace(selectedID) != "" {
		return session.Open(filepath.Join(containerDir, selectedID))
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

func runPromptAskHandler(req askquestion.Request) (string, error) {
	return "", fmt.Errorf("ask_question is not supported in run mode: %s", strings.TrimSpace(req.Question))
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
