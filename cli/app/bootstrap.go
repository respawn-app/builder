package app

import (
	"context"
	"errors"

	serverstartup "builder/server/startup"
	"builder/shared/config"
)

func startEmbeddedServer(ctx context.Context, opts Options, interactor authInteractor) (*embeddedAppServer, error) {
	if interactor == nil {
		return nil, errors.New("auth interactor is required")
	}
	server, err := serverstartup.Start(ctx, serverstartup.Request{
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
	if err != nil {
		return nil, err
	}
	return newEmbeddedAppServer(server), nil
}

type frontendOnboardingHandler struct {
	inner authInteractor
}

func (h frontendOnboardingHandler) EnsureOnboardingReady(ctx context.Context, req serverstartup.OnboardingRequest) (config.App, error) {
	cfg, _, err := ensureOnboardingReady(ctx, req.Config, req.AuthManager, h.inner, req.ReloadConfig)
	if err != nil {
		return config.App{}, err
	}
	return cfg, nil
}
