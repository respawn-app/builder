package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func Load(workspaceRoot string, opts LoadOptions) (App, error) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return App{}, errors.New("workspace root is required")
	}
	return load(workspaceRoot, true, opts)
}

func LoadGlobal(opts LoadOptions) (App, error) {
	return load("", false, opts)
}

func load(workspaceRoot string, includeWorkspaceLayer bool, opts LoadOptions) (App, error) {
	absWorkspace := ""
	if strings.TrimSpace(workspaceRoot) != "" {
		resolved, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return App{}, fmt.Errorf("resolve workspace root: %w", err)
		}
		absWorkspace = resolved
	} else if includeWorkspaceLayer {
		return App{}, errors.New("workspace root is required")
	}

	homeSettingsPath, err := resolveSettingsFilePath()
	if err != nil {
		return App{}, err
	}
	homeSettingsExists, err := settingsFileExists(homeSettingsPath)
	if err != nil {
		return App{}, err
	}

	homeFileConfig := settingsFile{}
	if homeSettingsExists {
		homeFileConfig, err = readSettingsFile(homeSettingsPath)
		if err != nil {
			return App{}, err
		}
	}
	workspaceSettingsPath := ""
	workspaceSettingsExists := false
	workspaceFileConfig := settingsFile{}
	if includeWorkspaceLayer {
		workspaceSettingsPath, err = resolveWorkspaceSettingsFilePath(absWorkspace)
		if err != nil {
			return App{}, err
		}
		workspaceSettingsExists, err = settingsFileExists(workspaceSettingsPath)
		if err != nil {
			return App{}, err
		}
		if workspaceSettingsExists {
			workspaceFileConfig, err = readSettingsFile(workspaceSettingsPath)
			if err != nil {
				return App{}, err
			}
		}
	}

	state := configRegistry.defaultState()
	sources := configRegistry.defaultSourceMap()

	if err := configRegistry.applyFile(homeFileConfig, homeSettingsPath, &state, sources); err != nil {
		return App{}, err
	}
	if err := configRegistry.applyEnv(os.LookupEnv, &state, sources); err != nil {
		return App{}, err
	}
	if includeWorkspaceLayer {
		if err := configRegistry.applyFile(workspaceFileConfig, workspaceSettingsPath, &state, sources); err != nil {
			return App{}, err
		}
	}
	if err := configRegistry.applyCLI(opts, &state, sources); err != nil {
		return App{}, err
	}
	inheritReviewerDefaults(&state.Settings)

	if err := validateSettings(state.Settings, sources); err != nil {
		return App{}, err
	}

	absPersistenceRoot, err := preparePersistenceRoot(state.PersistenceRoot)
	if err != nil {
		return App{}, err
	}
	if _, _, err := EnsureManagedRGConfigFile(); err != nil {
		return App{}, err
	}
	absWorktreeBaseDir, err := prepareWorktreeBaseDir(absPersistenceRoot, state.Settings.Worktrees.BaseDir)
	if err != nil {
		return App{}, err
	}
	state.Settings.Worktrees.BaseDir = absWorktreeBaseDir

	settingsPath := homeSettingsPath
	if workspaceSettingsExists {
		settingsPath = workspaceSettingsPath
	}
	settingsExists := homeSettingsExists || workspaceSettingsExists
	return App{
		AppName:         DefaultAppName,
		WorkspaceRoot:   absWorkspace,
		PersistenceRoot: absPersistenceRoot,
		Settings:        state.Settings,
		Source: SourceReport{
			SettingsPath:                  settingsPath,
			SettingsFileExists:            settingsExists,
			CreatedDefaultConfig:          false,
			HomeSettingsPath:              homeSettingsPath,
			HomeSettingsFileExists:        homeSettingsExists,
			WorkspaceSettingsPath:         workspaceSettingsPath,
			WorkspaceSettingsFileExists:   workspaceSettingsExists,
			WorkspaceSettingsLayerEnabled: includeWorkspaceLayer,
			Sources:                       sources,
		},
	}, nil
}
