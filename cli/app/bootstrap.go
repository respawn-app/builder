package app

import (
	"context"
	"errors"

	"builder/server/auth"
	serverbootstrap "builder/server/bootstrap"
	"builder/server/runtime"
	shelltool "builder/server/tools/shell"
	"builder/shared/config"
)

type appBootstrap struct {
	cfg              config.App
	containerDir     string
	oauthOpts        auth.OpenAIOAuthOptions
	authManager      *auth.Manager
	authInteractor   authInteractor
	fastModeState    *runtime.FastModeState
	background       *shelltool.Manager
	backgroundRouter *backgroundEventRouter
}

func bootstrapApp(ctx context.Context, opts Options, interactor authInteractor) (appBootstrap, error) {
	if interactor == nil {
		return appBootstrap{}, errors.New("auth interactor is required")
	}
	request := buildBootstrapRequest(opts, resolveEnvLookup(interactor))
	resolved, err := serverbootstrap.ResolveConfig(request)
	if err != nil {
		return appBootstrap{}, err
	}
	cfg := resolved.Config
	store := interactor.WrapStore(auth.NewFileStore(config.GlobalAuthConfigPath(cfg)))
	authSupport, err := serverbootstrap.BuildAuthSupport(store, request.LookupEnv, request.Now)
	if err != nil {
		return appBootstrap{}, err
	}
	if err := ensureAuthReady(ctx, authSupport.AuthManager, authSupport.OAuthOptions, cfg.Settings.Theme, cfg.Settings.TUIAlternateScreen, interactor); err != nil {
		return appBootstrap{}, err
	}
	if cfg, _, err = ensureOnboardingReady(ctx, cfg, authSupport.AuthManager, interactor, func() (config.App, error) {
		refreshed, err := serverbootstrap.ResolveConfig(request)
		if err != nil {
			return config.App{}, err
		}
		return refreshed.Config, nil
	}); err != nil {
		return appBootstrap{}, err
	}
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(cfg)
	if err != nil {
		return appBootstrap{}, err
	}
	return appBootstrap{
		cfg:            cfg,
		containerDir:   resolved.ContainerDir,
		oauthOpts:      authSupport.OAuthOptions,
		authManager:    authSupport.AuthManager,
		authInteractor: interactor,
		fastModeState:  runtimeSupport.FastModeState,
		background:     runtimeSupport.Background,
		backgroundRouter: &backgroundEventRouter{
			inner:       runtimeSupport.BackgroundRouter,
			background:  runtimeSupport.Background,
			outputLimit: cfg.Settings.ShellOutputMaxChars,
			outputMode:  shelltool.NormalizeBackgroundOutputMode(string(cfg.Settings.BGShellsOutput)),
		},
	}, nil
}

func buildBootstrapRequest(opts Options, lookupEnv func(string) string) serverbootstrap.Request {
	return serverbootstrap.Request{
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
