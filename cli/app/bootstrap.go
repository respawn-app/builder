package app

import (
	"context"
	"errors"

	serverembedded "builder/server/embedded"
	serverstartup "builder/server/startup"
	"builder/shared/config"
)

func startEmbeddedServer(ctx context.Context, opts Options, interactor authInteractor) (*serverembedded.Server, error) {
	if interactor == nil {
		return nil, errors.New("auth interactor is required")
	}
	return serverstartup.Start(ctx, serverstartup.Request{
		WorkspaceRoot:         opts.WorkspaceRoot,
		WorkspaceRootExplicit: opts.WorkspaceRootExplicit,
		SessionID:             opts.SessionID,
		Model:                 opts.Model,
		ProviderOverride:      opts.ProviderOverride,
		ThinkingLevel:         opts.ThinkingLevel,
		Theme:                 opts.Theme,
		ModelTimeoutSeconds:   opts.ModelTimeoutSeconds,
		ShellTimeoutSeconds:   opts.ShellTimeoutSeconds,
		Tools:                 opts.Tools,
		OpenAIBaseURL:         opts.OpenAIBaseURL,
		OpenAIBaseURLExplicit: opts.OpenAIBaseURLExplicit,
	}, interactor, frontendOnboardingHandler{inner: interactor})
}

type frontendOnboardingHandler struct {
	inner authInteractor
}

func (h frontendOnboardingHandler) EnsureOnboardingReady(ctx context.Context, req serverembedded.OnboardingRequest) (config.App, error) {
	cfg, _, err := ensureOnboardingReady(ctx, req.Config, req.AuthManager, h.inner, req.ReloadConfig)
	if err != nil {
		return config.App{}, err
	}
	return cfg, nil
}
