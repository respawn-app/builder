package app

import (
	"context"

	"builder/shared/config"
)

var launchSessionServerDaemon = startLocalRunPromptDaemon

func startSessionServer(ctx context.Context, opts Options, interactor authInteractor) (embeddedServer, error) {
	if remote, ok := tryDialDiscoveredRemoteServer(ctx, opts); ok {
		return remote, nil
	}
	if remote, ok, err := launchSessionServerDaemon(ctx, opts); err == nil && ok {
		cfg, cfgErr := loadSessionServerConfig(opts)
		if cfgErr != nil {
			_ = remote.Close()
			return nil, cfgErr
		}
		return newRemoteAppServer(remote, cfg), nil
	}
	return startEmbeddedServer(ctx, opts, interactor)
}

func tryDialDiscoveredRemoteServer(ctx context.Context, opts Options) (*remoteAppServer, bool) {
	remote, ok := tryDialDiscoveredRemote(ctx, opts)
	if !ok {
		return nil, false
	}
	cfg, err := loadSessionServerConfig(opts)
	if err != nil {
		_ = remote.Close()
		return nil, false
	}
	return newRemoteAppServer(remote, cfg), true
}

func loadSessionServerConfig(opts Options) (config.App, error) {
	workspaceRoot, err := resolveCLIWorkspaceRoot(opts)
	if err != nil {
		return config.App{}, err
	}
	return config.Load(workspaceRoot, config.LoadOptions{
		Model:               opts.Model,
		ProviderOverride:    opts.ProviderOverride,
		ThinkingLevel:       opts.ThinkingLevel,
		Theme:               opts.Theme,
		ModelTimeoutSeconds: opts.ModelTimeoutSeconds,
		ShellTimeoutSeconds: opts.ShellTimeoutSeconds,
		Tools:               opts.Tools,
	})
}
