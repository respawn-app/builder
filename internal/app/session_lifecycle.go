package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"builder/internal/app/commands"
	"builder/internal/session"
)

func runSessionLifecycle(ctx context.Context, boot appBootstrap, initialSessionID string) error {
	currentSessionID := strings.TrimSpace(initialSessionID)
	for {
		store, err := openOrCreateSession(boot.containerDir, currentSessionID, boot.cfg.WorkspaceRoot, boot.cfg.Settings.Theme)
		if err != nil {
			return err
		}

		active := effectiveSettings(boot.cfg.Settings, store.Meta().Locked)
		enabledTools := activeToolIDs(active, store.Meta().Locked)

		logger, err := newRunLogger(store.Dir())
		if err != nil {
			return err
		}
		logger.Logf("app.start session_id=%s workspace=%s model=%s", store.Meta().SessionID, boot.cfg.WorkspaceRoot, active.Model)
		logger.Logf("config.settings path=%s created=%t", boot.cfg.Source.SettingsPath, boot.cfg.Source.CreatedDefaultConfig)
		for _, line := range configSourceLines(boot.cfg.Source) {
			logger.Logf("config.source %s", line)
		}

		wiring, err := newRuntimeWiring(store, active, enabledTools, boot.cfg.WorkspaceRoot, boot.authManager, logger)
		if err != nil {
			_ = logger.Close()
			return err
		}

		commandRegistry, err := commands.NewDefaultRegistryWithFilePrompts(boot.cfg.WorkspaceRoot, boot.cfg.Source.SettingsPath)
		if err != nil {
			_ = logger.Close()
			return err
		}

		finalModel, runErr := runUILoop(wiring, active, logger, commandRegistry)
		_ = logger.Close()
		if runErr != nil {
			return runErr
		}

		nextSessionID, shouldContinue, err := resolveSessionAction(ctx, boot, store, extractUIAction(finalModel))
		if err != nil {
			return err
		}
		if !shouldContinue {
			return nil
		}
		currentSessionID = nextSessionID
	}
}

func resolveSessionAction(ctx context.Context, boot appBootstrap, store *session.Store, action UIAction) (string, bool, error) {
	switch action {
	case UIActionNewSession:
		newStore, err := session.Create(boot.containerDir, filepath.Base(boot.containerDir), boot.cfg.WorkspaceRoot)
		if err != nil {
			return "", false, err
		}
		return newStore.Meta().SessionID, true, nil
	case UIActionLogout:
		if _, err := boot.authManager.ClearMethod(ctx, true); err != nil {
			return "", false, err
		}
		if err := ensureAuthReady(ctx, boot.authManager, boot.oauthOpts); err != nil {
			return "", false, err
		}
		return store.Meta().SessionID, true, nil
	default:
		return "", false, nil
	}
}

func openOrCreateSession(containerDir, selectedID, workspaceRoot, theme string) (*session.Store, error) {
	if strings.TrimSpace(selectedID) != "" {
		return session.Open(filepath.Join(containerDir, selectedID))
	}

	summaries, err := session.ListSessions(containerDir)
	if err != nil {
		return nil, err
	}
	if len(summaries) == 0 {
		containerName := filepath.Base(containerDir)
		return session.Create(containerDir, containerName, workspaceRoot)
	}

	picked, err := runSessionPicker(summaries, theme)
	if err != nil {
		return nil, err
	}
	if picked.Canceled {
		return nil, errors.New("startup canceled by user")
	}
	if picked.CreateNew {
		containerName := filepath.Base(containerDir)
		return session.Create(containerDir, containerName, workspaceRoot)
	}
	if picked.Session == nil {
		return nil, errors.New("no session selected")
	}
	return session.Open(picked.Session.Path)
}
