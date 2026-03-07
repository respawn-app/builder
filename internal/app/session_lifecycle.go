package app

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"builder/internal/app/commands"
	"builder/internal/config"
	"builder/internal/session"
)

func runSessionLifecycle(ctx context.Context, boot appBootstrap, initialSessionID string) error {
	currentSessionID := strings.TrimSpace(initialSessionID)
	nextSessionInitialPrompt := ""
	nextSessionParentID := ""
	forceNewSession := false
	for {
		store, err := openOrCreateSession(
			boot.containerDir,
			currentSessionID,
			boot.cfg.WorkspaceRoot,
			boot.cfg.Settings.Theme,
			boot.cfg.Settings.TUIAlternateScreen,
			forceNewSession,
			nextSessionParentID,
		)
		if err != nil {
			return err
		}
		forceNewSession = false
		nextSessionParentID = ""

		active := effectiveSettings(boot.cfg.Settings, store.Meta().Locked)
		enabledTools := activeToolIDs(active, store.Meta().Locked)

		logger, err := newRunLogger(store.Dir())
		if err != nil {
			return err
		}
		logger.Logf("app.start session_id=%s workspace=%s model=%s", store.Meta().SessionID, boot.cfg.WorkspaceRoot, active.Model)
		if active.TUIScrollMode == config.TUIScrollModeNative && active.TUIAlternateScreen == config.TUIAlternateScreenAlways {
			logger.Logf("ui.scroll_mode.native overrides tui_alternate_screen=always for main UI startup (normal buffer required for replay)")
		}
		logger.Logf("config.settings path=%s created=%t", boot.cfg.Source.SettingsPath, boot.cfg.Source.CreatedDefaultConfig)
		for _, line := range configSourceLines(boot.cfg.Source) {
			logger.Logf("config.source %s", line)
		}

		wiring, err := newRuntimeWiring(store, active, enabledTools, boot.cfg.WorkspaceRoot, boot.authManager, logger, runtimeWiringOptions{})
		if err != nil {
			_ = logger.Close()
			return err
		}
		commandRegistry, err := commands.NewDefaultRegistryWithFilePrompts(boot.cfg.WorkspaceRoot, boot.cfg.Source.SettingsPath)
		if err != nil {
			_ = wiring.Close()
			_ = logger.Close()
			return err
		}

		finalModel, runErr := runUILoopWithInitialPrompt(
			wiring,
			active,
			logger,
			commandRegistry,
			nextSessionInitialPrompt,
			store.Meta().Name,
			store.Meta().Locked != nil,
		)
		_ = wiring.Close()
		nextSessionInitialPrompt = ""
		_ = logger.Close()
		if runErr != nil {
			return runErr
		}

		transition := extractUITransition(finalModel)
		nextSessionID, initialPrompt, parentSessionID, nextForceNewSession, shouldContinue, err := resolveSessionAction(ctx, boot, store, transition)
		if err != nil {
			return err
		}
		if !shouldContinue {
			return nil
		}
		currentSessionID = nextSessionID
		nextSessionInitialPrompt = initialPrompt
		nextSessionParentID = parentSessionID
		forceNewSession = nextForceNewSession
	}
}

func resolveSessionAction(ctx context.Context, boot appBootstrap, store *session.Store, transition UITransition) (string, string, string, bool, bool, error) {
	switch transition.Action {
	case UIActionNewSession:
		return "", transition.InitialPrompt, transition.ParentSessionID, true, true, nil
	case UIActionResume:
		return "", "", "", false, true, nil
	case UIActionOpenSession:
		return strings.TrimSpace(transition.TargetSessionID), "", "", false, true, nil
	case UIActionForkRollback:
		if store == nil {
			return "", "", "", false, false, errors.New("current store is required for rollback fork")
		}
		if transition.ForkUserMessageIndex <= 0 {
			return "", "", "", false, false, errors.New("rollback fork user message index must be > 0")
		}
		parentMeta := store.Meta()
		baseName := strings.TrimSpace(parentMeta.Name)
		if baseName == "" {
			baseName = parentMeta.SessionID
		}
		forkName := strings.TrimSpace(baseName + " → edit u" + strconv.Itoa(transition.ForkUserMessageIndex))
		forkedStore, err := session.ForkAtUserMessage(store, transition.ForkUserMessageIndex, forkName)
		if err != nil {
			return "", "", "", false, false, err
		}
		return forkedStore.Meta().SessionID, transition.InitialPrompt, "", false, true, nil
	case UIActionLogout:
		if _, err := boot.authManager.ClearMethod(ctx, true); err != nil {
			return "", "", "", false, false, err
		}
		if err := ensureAuthReady(ctx, boot.authManager, boot.oauthOpts); err != nil {
			return "", "", "", false, false, err
		}
		return store.Meta().SessionID, "", "", false, true, nil
	default:
		return "", "", "", false, false, nil
	}
}

func openOrCreateSession(
	containerDir,
	selectedID,
	workspaceRoot,
	theme string,
	alternateScreenPolicy config.TUIAlternateScreenPolicy,
	forceNew bool,
	parentSessionID string,
) (*session.Store, error) {
	if strings.TrimSpace(selectedID) != "" {
		return session.Open(filepath.Join(containerDir, selectedID))
	}
	if forceNew {
		containerName := filepath.Base(containerDir)
		created, err := session.NewLazy(containerDir, containerName, workspaceRoot)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(parentSessionID) != "" {
			if err := created.SetParentSessionID(parentSessionID); err != nil {
				return nil, err
			}
		}
		return created, nil
	}

	summaries, err := session.ListSessions(containerDir)
	if err != nil {
		return nil, err
	}
	if len(summaries) == 0 {
		containerName := filepath.Base(containerDir)
		return session.NewLazy(containerDir, containerName, workspaceRoot)
	}

	picked, err := runSessionPicker(summaries, theme, alternateScreenPolicy)
	if err != nil {
		return nil, err
	}
	if picked.Canceled {
		return nil, errors.New("startup canceled by user")
	}
	if picked.CreateNew {
		containerName := filepath.Base(containerDir)
		return session.NewLazy(containerDir, containerName, workspaceRoot)
	}
	if picked.Session == nil {
		return nil, errors.New("no session selected")
	}
	return session.Open(picked.Session.Path)
}
