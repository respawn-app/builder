package app

import (
	"context"
	"os"
	"strings"
	"time"

	"builder/internal/auth"
	"builder/internal/config"
)

type appBootstrap struct {
	cfg          config.App
	containerDir string
	oauthOpts    auth.OpenAIOAuthOptions
	authManager  *auth.Manager
}

func bootstrapApp(ctx context.Context, opts Options) (appBootstrap, error) {
	cfg, err := config.Load(opts.WorkspaceRoot, config.LoadOptions{
		Model:               opts.Model,
		ThinkingLevel:       opts.ThinkingLevel,
		Theme:               opts.Theme,
		ModelTimeoutSeconds: opts.ModelTimeoutSeconds,
		ShellTimeoutSeconds: opts.ShellTimeoutSeconds,
		Tools:               opts.Tools,
	})
	if err != nil {
		return appBootstrap{}, err
	}

	_, containerDir, err := config.ResolveWorkspaceContainer(cfg)
	if err != nil {
		return appBootstrap{}, err
	}

	oauthOpts := auth.OpenAIOAuthOptions{
		Issuer:   firstNonEmpty(strings.TrimSpace(os.Getenv("BUILDER_OAUTH_ISSUER")), auth.DefaultOpenAIIssuer),
		ClientID: firstNonEmpty(strings.TrimSpace(os.Getenv("BUILDER_OAUTH_CLIENT_ID")), auth.DefaultOpenAIClientID),
	}

	mgr := auth.NewManager(
		auth.NewFileStore(config.GlobalAuthConfigPath(cfg)),
		auth.NewOpenAIOAuthRefresher(oauthOpts, time.Now, 5*time.Minute),
		time.Now,
	)
	if err := ensureAuthReady(ctx, mgr, oauthOpts); err != nil {
		return appBootstrap{}, err
	}

	return appBootstrap{
		cfg:          cfg,
		containerDir: containerDir,
		oauthOpts:    oauthOpts,
		authManager:  mgr,
	}, nil
}
