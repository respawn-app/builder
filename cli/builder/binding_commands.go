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

	"builder/server/metadata"
	"builder/shared/config"
)

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
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		trimmedPath = "."
	}
	cfg, err := loadBindingCommandConfig(trimmedPath)
	if err != nil {
		return "", err
	}
	store, err := metadata.Open(cfg.PersistenceRoot)
	if err != nil {
		return "", err
	}
	defer func() { _ = store.Close() }()
	_, binding, err := store.ResolveWorkspacePath(ctx, cfg.WorkspaceRoot)
	if err != nil {
		return "", err
	}
	if binding == nil {
		return "", metadata.ErrWorkspaceNotRegistered
	}
	return strings.TrimSpace(binding.ProjectID), nil
}

func attachWorkspace(ctx context.Context, explicitProjectID string, targetPath string) (string, error) {
	targetCfg, err := loadBindingCommandConfig(targetPath)
	if err != nil {
		return "", err
	}
	store, err := metadata.Open(targetCfg.PersistenceRoot)
	if err != nil {
		return "", err
	}
	defer func() { _ = store.Close() }()
	projectID := strings.TrimSpace(explicitProjectID)
	if projectID == "" {
		sourceCfg, err := loadBindingCommandConfig(".")
		if err != nil {
			return "", err
		}
		if sourceCfg.PersistenceRoot != targetCfg.PersistenceRoot {
			return "", errors.New("attach requires source and target workspaces to share the same persistence root")
		}
		_, binding, err := store.ResolveWorkspacePath(ctx, sourceCfg.WorkspaceRoot)
		if err != nil {
			return "", err
		}
		if binding == nil {
			return "", fmt.Errorf("%w: current workspace is not attached to a project; run `builder project` in a workspace that already belongs to the target project or pass --project <project-id>", metadata.ErrWorkspaceNotRegistered)
		}
		projectID = strings.TrimSpace(binding.ProjectID)
	}
	binding, err := store.AttachWorkspaceToProject(ctx, projectID, targetCfg.WorkspaceRoot)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(binding.ProjectID), nil
}

func rebindWorkspace(ctx context.Context, oldPath string, newPath string) (metadata.Binding, error) {
	oldCfg, err := loadBindingCommandConfig(oldPath)
	if err != nil {
		return metadata.Binding{}, err
	}
	newCfg, err := loadBindingCommandConfig(newPath)
	if err != nil {
		return metadata.Binding{}, err
	}
	if oldCfg.PersistenceRoot != newCfg.PersistenceRoot {
		return metadata.Binding{}, errors.New("rebind requires old and new workspaces to share the same persistence root")
	}
	store, err := metadata.Open(newCfg.PersistenceRoot)
	if err != nil {
		return metadata.Binding{}, err
	}
	defer func() { _ = store.Close() }()
	return store.RebindWorkspace(ctx, oldCfg.WorkspaceRoot, newCfg.WorkspaceRoot)
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
