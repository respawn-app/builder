package app

import (
	"context"
	"io"
	"strings"
	"time"

	"builder/server/launch"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/config"
)

type Options struct {
	WorkspaceRoot         string
	WorkspaceRootExplicit bool
	SessionID             string
	Model                 string
	ProviderOverride      string
	ThinkingLevel         string
	Theme                 string
	ModelTimeoutSeconds   int
	ShellTimeoutSeconds   int
	Tools                 string
	OpenAIBaseURL         string
	OpenAIBaseURLExplicit bool
}

func Run(ctx context.Context, opts Options) error {
	interactor := newInteractiveAuthInteractor()
	server, err := startSessionServer(ctx, opts, interactor)
	if err != nil {
		return err
	}
	defer func() { _ = server.Close() }()
	return runSessionLifecycle(ctx, server, interactor, strings.TrimSpace(opts.SessionID))
}

func RunPrompt(ctx context.Context, opts Options, prompt string, timeout time.Duration, progress io.Writer) (RunPromptResult, error) {
	runClient, closeFn, err := startRunPromptClient(ctx, opts)
	if err != nil {
		return RunPromptResult{}, err
	}
	defer func() {
		if closeFn != nil {
			_ = closeFn()
		}
	}()
	return runPrompt(ctx, runClient, opts, strings.TrimSpace(opts.SessionID), prompt, timeout, progress)
}
func effectiveSettings(base config.Settings, locked *session.LockedContract) config.Settings {
	return launch.EffectiveSettings(base, locked)
}

func activeToolIDs(settings config.Settings, source config.SourceReport, locked *session.LockedContract) []tools.ID {
	return launch.ActiveToolIDs(settings, source, locked)
}

func dedupeSortToolIDs(ids []tools.ID) []tools.ID {
	return launch.DedupeSortToolIDs(ids)
}
