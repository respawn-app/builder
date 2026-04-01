package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"builder/shared/client"
	"builder/shared/config"
	"builder/shared/discovery"
)

func startRunPromptClient(ctx context.Context, opts Options) (client.RunPromptClient, func() error, error) {
	if remote, ok := tryDialDiscoveredRemote(ctx, opts); ok {
		return remote, remote.Close, nil
	}
	server, err := startEmbeddedServer(ctx, opts, newHeadlessAuthInteractor())
	if err != nil {
		return nil, nil, err
	}
	return server.RunPromptClient(), server.Close, nil
}

func tryDialDiscoveredRemote(ctx context.Context, opts Options) (*client.Remote, bool) {
	workspaceRoot, err := resolveCLIWorkspaceRoot(opts)
	if err != nil {
		return nil, false
	}
	cfg, err := config.Load(workspaceRoot, config.LoadOptions{})
	if err != nil {
		return nil, false
	}
	_, containerDir, err := config.ResolveWorkspaceContainer(cfg)
	if err != nil {
		return nil, false
	}
	discoveryPath, err := discovery.PathForContainer(containerDir)
	if err != nil {
		return nil, false
	}
	record, err := discovery.Read(discoveryPath)
	if err != nil {
		return nil, false
	}
	expectedProjectID, err := config.ProjectIDForWorkspaceRoot(cfg.WorkspaceRoot)
	if err != nil {
		return nil, false
	}
	if record.Identity.ProjectID != expectedProjectID {
		return nil, false
	}
	remote, err := client.DialRemote(ctx, record)
	if err != nil {
		return nil, false
	}
	if remote.Identity().ProjectID != expectedProjectID {
		_ = remote.Close()
		return nil, false
	}
	return remote, true
}

func resolveCLIWorkspaceRoot(opts Options) (string, error) {
	trimmed := strings.TrimSpace(opts.WorkspaceRoot)
	if trimmed == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		trimmed = cwd
	}
	return filepath.Abs(trimmed)
}
