package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"builder/internal/auth"
	"builder/internal/config"
	"builder/internal/runtime"
	"builder/internal/shared/textutil"
	shelltool "builder/internal/tools/shell"
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
	bootstrapPlan := bootstrapLaunchPlan{
		WorkspaceRoot:    requestedWorkspaceRoot(opts),
		OpenAIBaseURL:    strings.TrimSpace(opts.OpenAIBaseURL),
		UseOpenAIBaseURL: opts.OpenAIBaseURLExplicit,
	}
	cfg, err := loadBootstrapConfig(opts, bootstrapPlan.WorkspaceRoot, bootstrapPlan.OpenAIBaseURL, bootstrapPlan.UseOpenAIBaseURL)
	if err != nil {
		return appBootstrap{}, err
	}
	planner := newBootstrapLaunchPlanner(cfg.PersistenceRoot)
	bootstrapPlan, err = planner.PlanBootstrap(opts)
	if err != nil {
		return appBootstrap{}, err
	}
	cfg, err = loadBootstrapConfig(opts, bootstrapPlan.WorkspaceRoot, bootstrapPlan.OpenAIBaseURL, bootstrapPlan.UseOpenAIBaseURL)
	if err != nil {
		return appBootstrap{}, err
	}

	_, containerDir, err := config.ResolveWorkspaceContainer(cfg)
	if err != nil {
		return appBootstrap{}, err
	}

	oauthOpts := auth.OpenAIOAuthOptions{
		Issuer:   auth.DefaultOpenAIIssuer,
		ClientID: textutil.FirstNonEmpty(strings.TrimSpace(os.Getenv("BUILDER_OAUTH_CLIENT_ID")), auth.DefaultOpenAIClientID),
	}
	if interactor == nil {
		return appBootstrap{}, errors.New("auth interactor is required")
	}
	store := interactor.WrapStore(auth.NewFileStore(config.GlobalAuthConfigPath(cfg)))

	mgr := auth.NewManager(
		store,
		auth.NewOpenAIOAuthRefresher(oauthOpts, time.Now, 5*time.Minute),
		time.Now,
	)
	if err := ensureAuthReady(ctx, mgr, oauthOpts, cfg.Settings.Theme, cfg.Settings.TUIAlternateScreen, interactor); err != nil {
		return appBootstrap{}, err
	}
	if cfg, _, err = ensureOnboardingReady(ctx, cfg, mgr, interactor, func() (config.App, error) {
		refreshPlanner := newBootstrapLaunchPlanner(cfg.PersistenceRoot)
		refreshedPlan, err := refreshPlanner.PlanBootstrap(opts)
		if err != nil {
			return config.App{}, err
		}
		return loadBootstrapConfig(opts, refreshedPlan.WorkspaceRoot, refreshedPlan.OpenAIBaseURL, refreshedPlan.UseOpenAIBaseURL)
	}); err != nil {
		return appBootstrap{}, err
	}

	background, err := shelltool.NewManager(
		shelltool.WithMinimumExecToBgTime(time.Duration(cfg.Settings.MinimumExecToBgSeconds) * time.Second),
	)
	if err != nil {
		return appBootstrap{}, err
	}

	return appBootstrap{
		cfg:            cfg,
		containerDir:   containerDir,
		oauthOpts:      oauthOpts,
		authManager:    mgr,
		authInteractor: interactor,
		fastModeState:  runtime.NewFastModeState(cfg.Settings.PriorityRequestMode),
		background:     background,
		backgroundRouter: newBackgroundEventRouter(
			background,
			cfg.Settings.ShellOutputMaxChars,
			shelltool.NormalizeBackgroundOutputMode(string(cfg.Settings.BGShellsOutput)),
		),
	}, nil
}

func loadBootstrapConfig(opts Options, workspaceRoot, openAIBaseURL string, useOpenAIBaseURL bool) (config.App, error) {
	loadOpts := config.LoadOptions{
		Model:               opts.Model,
		ProviderOverride:    opts.ProviderOverride,
		ThinkingLevel:       opts.ThinkingLevel,
		Theme:               opts.Theme,
		ModelTimeoutSeconds: opts.ModelTimeoutSeconds,
		ShellTimeoutSeconds: opts.ShellTimeoutSeconds,
		Tools:               opts.Tools,
	}
	if useOpenAIBaseURL {
		loadOpts.OpenAIBaseURL = openAIBaseURL
	}
	return config.Load(workspaceRoot, loadOpts)
}

func requestedWorkspaceRoot(opts Options) string {
	workspaceRoot := strings.TrimSpace(opts.WorkspaceRoot)
	if workspaceRoot == "" {
		return "."
	}
	return workspaceRoot
}
