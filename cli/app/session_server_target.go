package app

import (
	"context"

	"builder/server/auth"
	"builder/shared/config"
)

var launchSessionServerDaemon = startLocalRunPromptDaemon
var startInteractiveEmbeddedSessionServer = startEmbeddedServer
var dialInteractiveRemoteSessionServer = tryDialDiscoveredRemoteServer

func remoteAuthHooks(interactor authInteractor) (func(string) string, func(auth.Store) auth.Store) {
	if interactor == nil {
		return nil, nil
	}
	return interactor.LookupEnv, interactor.WrapStore
}

func startSessionServer(ctx context.Context, opts Options, interactor authInteractor) (embeddedServer, error) {
	bypassRemote, err := shouldBypassRemoteStartupForInteractiveOnboarding(opts, interactor)
	if err != nil {
		return nil, err
	}
	if bypassRemote {
		return startInteractiveEmbeddedSessionServer(ctx, opts, interactor)
	}
	if remote, ok, err := dialInteractiveRemoteSessionServer(ctx, opts, interactor); err != nil {
		return nil, err
	} else if ok {
		return remote, nil
	}
	lookupEnv, wrapStore := remoteAuthHooks(interactor)
	if remote, closeFn, ok, err := launchSessionServerDaemon(ctx, opts); err == nil && ok {
		cfg, cfgErr := loadSessionServerConfig(opts)
		if cfgErr != nil {
			if closeFn != nil {
				_ = closeFn()
			} else {
				_ = remote.Close()
			}
			return nil, cfgErr
		}
		return newRemoteAppServerWithAuth(remote, cfg, closeFn, lookupEnv, wrapStore), nil
	}
	return startInteractiveEmbeddedSessionServer(ctx, opts, interactor)
}

func shouldBypassRemoteStartupForInteractiveOnboarding(opts Options, interactor authInteractor) (bool, error) {
	if interactor == nil || !interactor.Interactive() {
		return false, nil
	}
	cfg, err := loadSessionServerConfig(opts)
	if err != nil {
		return false, err
	}
	return !cfg.Source.SettingsFileExists, nil
}

func tryDialDiscoveredRemoteServer(ctx context.Context, opts Options, interactor authInteractor) (*remoteAppServer, bool, error) {
	remote, ok := tryDialDiscoveredRemote(ctx, opts, discoveredRemoteSupportsInteractiveSession)
	if !ok {
		return nil, false, nil
	}
	cfg, err := loadSessionServerConfig(opts)
	if err != nil {
		_ = remote.Close()
		return nil, false, err
	}
	lookupEnv, wrapStore := remoteAuthHooks(interactor)
	return newRemoteAppServerWithAuth(remote, cfg, nil, lookupEnv, wrapStore), true, nil
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
		OpenAIBaseURL:       opts.OpenAIBaseURL,
	})
}
