package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"builder/shared/client"
	"builder/shared/config"
	"builder/shared/serverapi"
)

const bindingCommandRPCTimeout = 5 * time.Second

func projectSubcommand(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("builder project", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	remaining := fs.Args()
	if len(remaining) > 1 {
		fmt.Fprintln(stderr, "project accepts at most one path argument")
		return 2
	}
	path := "."
	if len(remaining) == 1 {
		path = remaining[0]
	}
	projectID, err := projectIDForPath(context.Background(), path)
	if err != nil {
		fmt.Fprintln(stderr, formatProjectLookupCommandError(path, err))
		return 1
	}
	_, _ = fmt.Fprintln(stdout, projectID)
	return 0
}

func attachSubcommand(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("builder attach", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := fs.String("project", "", "explicit project id override")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	remaining := fs.Args()
	if len(remaining) > 1 {
		fmt.Fprintln(stderr, "attach accepts at most one path argument; use --project for explicit project ids")
		return 2
	}
	targetPath := "."
	if len(remaining) == 1 {
		targetPath = remaining[0]
	}
	boundProjectID, err := attachWorkspace(context.Background(), *projectID, targetPath)
	if err != nil {
		fmt.Fprintln(stderr, formatAttachWorkspaceCommandError(targetPath, *projectID, err))
		return 1
	}
	_, _ = fmt.Fprintln(stdout, boundProjectID)
	return 0
}

func rebindSubcommand(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("builder rebind", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	remaining := fs.Args()
	if len(remaining) != 2 {
		fmt.Fprintln(stderr, "rebind requires <old-path> and <new-path>")
		return 2
	}
	binding, err := rebindWorkspace(context.Background(), remaining[0], remaining[1])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, binding.WorkspaceID)
	return 0
}

func projectIDForPath(ctx context.Context, path string) (string, error) {
	cfg, remote, err := openBindingCommandRemote(ctx, path)
	if err != nil {
		return "", err
	}
	defer func() { _ = remote.Close() }()
	binding, err := resolveWorkspaceBinding(ctx, remote, cfg.WorkspaceRoot)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(binding.ProjectID), nil
}

func attachWorkspace(ctx context.Context, explicitProjectID string, targetPath string) (string, error) {
	targetCfg, remote, err := openBindingCommandRemote(ctx, targetPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = remote.Close() }()
	projectID := strings.TrimSpace(explicitProjectID)
	if projectID == "" {
		sourceCfg, err := loadBindingCommandConfig(".")
		if err != nil {
			return "", err
		}
		if config.ServerRPCURL(sourceCfg) != config.ServerRPCURL(targetCfg) {
			return "", errors.New("attach requires source and target workspaces to share the same configured server")
		}
		sourceBinding, err := resolveWorkspaceBinding(ctx, remote, sourceCfg.WorkspaceRoot)
		if err != nil {
			return "", fmt.Errorf("%w: current workspace is not attached to a project; run `builder project` in a workspace that already belongs to the target project or pass --project <project-id>", err)
		}
		projectID = strings.TrimSpace(sourceBinding.ProjectID)
	}
	resp, err := remote.AttachWorkspaceToProject(ctx, serverapi.ProjectAttachWorkspaceRequest{ProjectID: projectID, WorkspaceRoot: targetCfg.WorkspaceRoot})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Binding.ProjectID), nil
}

func rebindWorkspace(ctx context.Context, oldPath string, newPath string) (serverapi.ProjectBinding, error) {
	oldCfg, err := loadBindingCommandConfig(oldPath)
	if err != nil {
		return serverapi.ProjectBinding{}, err
	}
	newCfg, err := loadBindingCommandConfig(newPath)
	if err != nil {
		return serverapi.ProjectBinding{}, err
	}
	if config.ServerRPCURL(oldCfg) != config.ServerRPCURL(newCfg) {
		return serverapi.ProjectBinding{}, errors.New("rebind requires old and new workspaces to share the same configured server")
	}
	ctx, cancel := context.WithTimeout(ctx, bindingCommandRPCTimeout)
	defer cancel()
	remote, err := client.DialRemoteURL(ctx, config.ServerRPCURL(newCfg))
	if err != nil {
		return serverapi.ProjectBinding{}, err
	}
	defer func() { _ = remote.Close() }()
	resp, err := remote.RebindWorkspace(ctx, serverapi.ProjectRebindWorkspaceRequest{OldWorkspaceRoot: oldCfg.WorkspaceRoot, NewWorkspaceRoot: newCfg.WorkspaceRoot})
	if err != nil {
		return serverapi.ProjectBinding{}, err
	}
	return resp.Binding, nil
}

func openBindingCommandRemote(ctx context.Context, path string) (config.App, *client.Remote, error) {
	cfg, err := loadBindingCommandConfig(path)
	if err != nil {
		return config.App{}, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, bindingCommandRPCTimeout)
	defer cancel()
	remote, err := client.DialRemoteURL(ctx, config.ServerRPCURL(cfg))
	if err != nil {
		return config.App{}, nil, err
	}
	return cfg, remote, nil
}

func resolveWorkspaceBinding(ctx context.Context, projectViews client.ProjectViewClient, workspaceRoot string) (serverapi.ProjectBinding, error) {
	resp, err := projectViews.ResolveProjectPath(ctx, serverapi.ProjectResolvePathRequest{Path: workspaceRoot})
	if err != nil {
		return serverapi.ProjectBinding{}, err
	}
	if resp.Binding == nil {
		return serverapi.ProjectBinding{}, errWorkspaceNotRegistered
	}
	return *resp.Binding, nil
}

func loadBindingCommandConfig(path string) (config.App, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		trimmedPath = "."
	}
	absPath, err := filepath.Abs(trimmedPath)
	if err != nil {
		return config.App{}, err
	}
	if info, statErr := os.Stat(absPath); statErr == nil && !info.IsDir() {
		absPath = filepath.Dir(absPath)
	}
	return config.Load(absPath, config.LoadOptions{})
}

var errWorkspaceNotRegistered = serverapi.ErrWorkspaceNotRegistered
