package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func Load(workspaceRoot string, opts LoadOptions) (App, error) {
	if workspaceRoot == "" {
		return App{}, errors.New("workspace root is required")
	}

	absWorkspace, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return App{}, fmt.Errorf("resolve workspace root: %w", err)
	}

	settingsPath, err := resolveSettingsFilePath()
	if err != nil {
		return App{}, err
	}
	settingsExists, err := settingsFileExists(settingsPath)
	if err != nil {
		return App{}, err
	}

	fileConfig := settingsFile{}
	if settingsExists {
		fileConfig, err = readSettingsFile(settingsPath)
		if err != nil {
			return App{}, err
		}
	}

	state := configRegistry.defaultState()
	sources := configRegistry.defaultSourceMap()

	if err := configRegistry.applyFile(fileConfig, settingsPath, &state, sources); err != nil {
		return App{}, err
	}
	if err := configRegistry.applyEnv(os.LookupEnv, &state, sources); err != nil {
		return App{}, err
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

	return App{
		AppName:         DefaultAppName,
		WorkspaceRoot:   absWorkspace,
		PersistenceRoot: absPersistenceRoot,
		Settings:        state.Settings,
		Source: SourceReport{
			SettingsPath:         settingsPath,
			SettingsFileExists:   settingsExists,
			CreatedDefaultConfig: false,
			Sources:              sources,
		},
	}, nil
}
