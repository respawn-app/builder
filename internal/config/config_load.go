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

	settingsPath, created, err := ensureDefaultSettingsFile()
	if err != nil {
		return App{}, err
	}

	fileConfig, err := readSettingsFile(settingsPath)
	if err != nil {
		return App{}, err
	}

	fileOverlay, err := settingsOverlayFromFile(fileConfig, settingsPath)
	if err != nil {
		return App{}, err
	}
	envOverlay, err := settingsOverlayFromEnv(os.LookupEnv)
	if err != nil {
		return App{}, err
	}
	cliOverlay, err := settingsOverlayFromCLI(opts)
	if err != nil {
		return App{}, err
	}

	settings := defaultSettings()
	sources := defaultSourceMap()
	persistenceRoot := DefaultPersistence
	persistenceSource := "default"

	applySettingsOverlay(&settings, &persistenceRoot, &persistenceSource, sources, fileOverlay, "file")
	applySettingsOverlay(&settings, &persistenceRoot, &persistenceSource, sources, envOverlay, "env")
	applySettingsOverlay(&settings, &persistenceRoot, &persistenceSource, sources, cliOverlay, "cli")
	inheritReviewerModel(&settings)

	if err := validateSettings(settings, sources); err != nil {
		return App{}, err
	}

	absPersistenceRoot, err := preparePersistenceRoot(persistenceRoot)
	if err != nil {
		return App{}, err
	}

	return App{
		AppName:         DefaultAppName,
		WorkspaceRoot:   absWorkspace,
		PersistenceRoot: absPersistenceRoot,
		Settings:        settings,
		Source: SourceReport{
			SettingsPath:         settingsPath,
			CreatedDefaultConfig: created,
			Sources:              withPersistenceSource(sources, persistenceSource),
		},
	}, nil
}
