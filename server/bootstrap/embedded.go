package bootstrap

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"builder/server/auth"
	"builder/server/launch"
	"builder/server/runtime"
	"builder/server/runtimewire"
	"builder/server/storagemigration"
	shelltool "builder/server/tools/shell"
	"builder/server/tools/shell/postprocess"
	"builder/shared/config"
	"builder/shared/textutil"
)

type Request struct {
	WorkspaceRoot         string
	WorkspaceRootExplicit bool
	SessionID             string
	OpenAIBaseURL         string
	OpenAIBaseURLExplicit bool
	LoadOptions           config.LoadOptions
	LookupEnv             func(string) string
	Now                   func() time.Time
}

type ConfigPlan struct {
	Config       config.App
	ContainerDir string
}

type AuthSupport struct {
	OAuthOptions auth.OpenAIOAuthOptions
	AuthManager  *auth.Manager
}

type RuntimeSupport struct {
	FastModeState    *runtime.FastModeState
	Background       *shelltool.Manager
	BackgroundRouter *runtimewire.BackgroundEventRouter
}

func ResolveConfig(req Request) (ConfigPlan, error) {
	now := req.Now
	if now == nil {
		now = time.Now
	}
	bootstrapPlan := launch.BootstrapPlan{
		WorkspaceRoot:    requestedWorkspaceRoot(req.WorkspaceRoot),
		OpenAIBaseURL:    strings.TrimSpace(req.OpenAIBaseURL),
		UseOpenAIBaseURL: req.OpenAIBaseURLExplicit,
	}
	cfg, err := loadConfig(req.LoadOptions, bootstrapPlan.WorkspaceRoot, bootstrapPlan.OpenAIBaseURL, bootstrapPlan.UseOpenAIBaseURL)
	if err != nil {
		return ConfigPlan{}, err
	}
	if err := storagemigration.EnsureProjectV1(context.Background(), cfg.PersistenceRoot, now); err != nil {
		return ConfigPlan{}, err
	}
	bootstrapPlan, err = launch.ResolveBootstrapPlan(cfg.PersistenceRoot, launch.BootstrapRequest{
		WorkspaceRoot:         requestedWorkspaceRoot(req.WorkspaceRoot),
		WorkspaceRootExplicit: req.WorkspaceRootExplicit,
		SessionID:             strings.TrimSpace(req.SessionID),
		OpenAIBaseURL:         strings.TrimSpace(req.OpenAIBaseURL),
		OpenAIBaseURLExplicit: req.OpenAIBaseURLExplicit,
	})
	if err != nil {
		return ConfigPlan{}, err
	}
	cfg, err = loadConfig(req.LoadOptions, bootstrapPlan.WorkspaceRoot, bootstrapPlan.OpenAIBaseURL, bootstrapPlan.UseOpenAIBaseURL)
	if err != nil {
		return ConfigPlan{}, err
	}
	_, containerDir, err := config.ResolveWorkspaceContainer(cfg)
	if err != nil {
		return ConfigPlan{}, err
	}
	return ConfigPlan{Config: cfg, ContainerDir: containerDir}, nil
}

func BuildAuthSupport(store auth.Store, lookupEnv func(string) string, now func() time.Time) (AuthSupport, error) {
	if store == nil {
		return AuthSupport{}, errors.New("auth store is required")
	}
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}
	if now == nil {
		now = time.Now
	}
	oauthOpts := auth.OpenAIOAuthOptions{
		Issuer:   auth.DefaultOpenAIIssuer,
		ClientID: textutil.FirstNonEmpty(strings.TrimSpace(lookupEnv("BUILDER_OAUTH_CLIENT_ID")), auth.DefaultOpenAIClientID),
	}
	return AuthSupport{
		OAuthOptions: oauthOpts,
		AuthManager: auth.NewManager(
			store,
			auth.NewOpenAIOAuthRefresher(oauthOpts, now, 5*time.Minute),
			now,
		),
	}, nil
}

func BuildRuntimeSupport(cfg config.App) (RuntimeSupport, error) {
	background, err := shelltool.NewManager(
		shelltool.WithMinimumExecToBgTime(time.Duration(cfg.Settings.MinimumExecToBgSeconds)*time.Second),
		shelltool.WithPostprocessor(postprocess.NewRunner(postprocess.Settings{
			Mode:     cfg.Settings.Shell.PostprocessingMode,
			HookPath: cfg.Settings.Shell.PostprocessHook,
		})),
	)
	if err != nil {
		return RuntimeSupport{}, err
	}
	return RuntimeSupport{
		FastModeState:    runtime.NewFastModeState(cfg.Settings.PriorityRequestMode),
		Background:       background,
		BackgroundRouter: runtimewire.NewBackgroundEventRouter(background, cfg.Settings.ShellOutputMaxChars, shelltool.NormalizeBackgroundOutputMode(string(cfg.Settings.BGShellsOutput))),
	}, nil
}

func loadConfig(loadOpts config.LoadOptions, workspaceRoot, openAIBaseURL string, useOpenAIBaseURL bool) (config.App, error) {
	if useOpenAIBaseURL {
		loadOpts.OpenAIBaseURL = openAIBaseURL
	} else {
		loadOpts.OpenAIBaseURL = ""
	}
	return config.Load(workspaceRoot, loadOpts)
}

func requestedWorkspaceRoot(workspaceRoot string) string {
	if strings.TrimSpace(workspaceRoot) == "" {
		return "."
	}
	return workspaceRoot
}
