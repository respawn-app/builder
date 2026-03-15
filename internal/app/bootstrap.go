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
	"builder/internal/session"
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
	cfg, err := loadBootstrapConfig(opts, requestedWorkspaceRoot(opts), opts.OpenAIBaseURL, opts.OpenAIBaseURLExplicit)
	if err != nil {
		return appBootstrap{}, err
	}
	if strings.TrimSpace(opts.SessionID) != "" {
		workspaceRoot, openAIBaseURL, useOpenAIBaseURL, err := resolveContinuationLoadParams(cfg.PersistenceRoot, opts)
		if err != nil {
			return appBootstrap{}, err
		}
		cfg, err = loadBootstrapConfig(opts, workspaceRoot, openAIBaseURL, useOpenAIBaseURL)
		if err != nil {
			return appBootstrap{}, err
		}
	}

	_, containerDir, err := config.ResolveWorkspaceContainer(cfg)
	if err != nil {
		return appBootstrap{}, err
	}

	oauthOpts := auth.OpenAIOAuthOptions{
		Issuer:   textutil.FirstNonEmpty(strings.TrimSpace(os.Getenv("BUILDER_OAUTH_ISSUER")), auth.DefaultOpenAIIssuer),
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
	if err := ensureAuthReady(ctx, mgr, oauthOpts, interactor); err != nil {
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

func resolveContinuationLoadParams(persistenceRoot string, opts Options) (string, string, bool, error) {
	store, err := session.OpenByID(persistenceRoot, opts.SessionID)
	if err != nil {
		return "", "", false, err
	}
	meta := store.Meta()

	workspaceRoot := requestedWorkspaceRoot(opts)
	if !opts.WorkspaceRootExplicit && strings.TrimSpace(meta.WorkspaceRoot) != "" {
		workspaceRoot = strings.TrimSpace(meta.WorkspaceRoot)
	}

	if opts.OpenAIBaseURLExplicit {
		return workspaceRoot, strings.TrimSpace(opts.OpenAIBaseURL), true, nil
	}
	if meta.Continuation != nil && strings.TrimSpace(meta.Continuation.OpenAIBaseURL) != "" {
		return workspaceRoot, strings.TrimSpace(meta.Continuation.OpenAIBaseURL), true, nil
	}
	return workspaceRoot, "", false, nil
}
