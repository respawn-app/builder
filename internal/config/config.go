package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

const (
	DefaultAppName       = "builder"
	DefaultPersistence   = "./agents/builder"
	workspaceIndexName   = "workspaces.json"
	globalAuthConfigName = "auth.json"
)

type App struct {
	AppName         string
	WorkspaceRoot   string
	PersistenceRoot string
}

func Load(workspaceRoot string) (App, error) {
	if workspaceRoot == "" {
		return App{}, errors.New("workspace root is required")
	}

	absWorkspace, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return App{}, fmt.Errorf("resolve workspace root: %w", err)
	}

	root := os.Getenv("BUILDER_PERSISTENCE_ROOT")
	if root == "" {
		root = DefaultPersistence
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return App{}, fmt.Errorf("resolve persistence root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return App{}, fmt.Errorf("create persistence root: %w", err)
	}

	return App{
		AppName:         DefaultAppName,
		WorkspaceRoot:   absWorkspace,
		PersistenceRoot: absRoot,
	}, nil
}

type workspaceIndex struct {
	Entries map[string]string `json:"entries"`
}

func ResolveWorkspaceContainer(cfg App) (string, string, error) {
	idxPath := filepath.Join(cfg.PersistenceRoot, workspaceIndexName)
	idx, err := loadWorkspaceIndex(idxPath)
	if err != nil {
		return "", "", err
	}

	if name, ok := idx.Entries[cfg.WorkspaceRoot]; ok {
		return name, filepath.Join(cfg.PersistenceRoot, name), nil
	}

	base := filepath.Base(cfg.WorkspaceRoot)
	container := fmt.Sprintf("%s-%s", base, uuid.NewString())
	idx.Entries[cfg.WorkspaceRoot] = container
	if err := saveWorkspaceIndexAtomic(idxPath, idx); err != nil {
		return "", "", err
	}

	containerDir := filepath.Join(cfg.PersistenceRoot, container)
	if err := os.MkdirAll(containerDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create workspace container: %w", err)
	}

	return container, containerDir, nil
}

func GlobalAuthConfigPath(cfg App) string {
	return filepath.Join(cfg.PersistenceRoot, globalAuthConfigName)
}

func loadWorkspaceIndex(path string) (workspaceIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return workspaceIndex{Entries: map[string]string{}}, nil
		}
		return workspaceIndex{}, fmt.Errorf("read workspace index: %w", err)
	}

	var idx workspaceIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return workspaceIndex{}, fmt.Errorf("parse workspace index: %w", err)
	}
	if idx.Entries == nil {
		idx.Entries = map[string]string{}
	}
	return idx, nil
}

func saveWorkspaceIndexAtomic(path string, idx workspaceIndex) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workspace index: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write workspace index tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace workspace index: %w", err)
	}
	return nil
}
