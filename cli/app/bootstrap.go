package app

import (
	"context"
	"errors"

	"builder/server/auth"
	serverembedded "builder/server/embedded"
	"builder/shared/config"
)

func startEmbeddedServer(ctx context.Context, opts Options, interactor authInteractor) (*serverembedded.Server, error) {
	if interactor == nil {
		return nil, errors.New("auth interactor is required")
	}
	return serverembedded.Start(ctx, buildBootstrapRequest(opts, resolveEnvLookup(interactor)), serverembedded.StartHooks{
		Auth:       frontendAuthHandler{inner: interactor},
		Onboarding: frontendOnboardingHandler{inner: interactor},
	})
}

func buildBootstrapRequest(opts Options, lookupEnv func(string) string) serverembedded.Request {
	return serverembedded.Request{
		WorkspaceRoot:         opts.WorkspaceRoot,
		WorkspaceRootExplicit: opts.WorkspaceRootExplicit,
		SessionID:             opts.SessionID,
		OpenAIBaseURL:         opts.OpenAIBaseURL,
		OpenAIBaseURLExplicit: opts.OpenAIBaseURLExplicit,
		LookupEnv:             lookupEnv,
		LoadOptions: config.LoadOptions{
			Model:               opts.Model,
			ProviderOverride:    opts.ProviderOverride,
			ThinkingLevel:       opts.ThinkingLevel,
			Theme:               opts.Theme,
			ModelTimeoutSeconds: opts.ModelTimeoutSeconds,
			ShellTimeoutSeconds: opts.ShellTimeoutSeconds,
			Tools:               opts.Tools,
		},
	}
}

type frontendAuthHandler struct {
	inner authInteractor
}

func (h frontendAuthHandler) WrapStore(base auth.Store) auth.Store {
	return h.inner.WrapStore(base)
}

func (h frontendAuthHandler) NeedsInteraction(req authInteraction) bool {
	return h.inner.NeedsInteraction(req)
}

func (h frontendAuthHandler) Interact(ctx context.Context, req authInteraction) error {
	return h.inner.Interact(ctx, req)
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
