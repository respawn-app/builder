package app

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"

	"builder/internal/app/commands"
	"builder/internal/session"
)

func runSessionLifecycle(ctx context.Context, boot appBootstrap, initialSessionID string) error {
	planner := newSessionLaunchPlanner(&boot)
	currentSessionID := strings.TrimSpace(initialSessionID)
	nextSessionInitialPrompt := ""
	nextSessionParentID := ""
	forceNewSession := false
	for {
		plan, err := planner.PlanSession(sessionLaunchRequest{
			Mode:              launchModeInteractive,
			SelectedSessionID: currentSessionID,
			ForceNewSession:   forceNewSession,
			ParentSessionID:   nextSessionParentID,
		})
		if err != nil {
			return err
		}
		forceNewSession = false
		nextSessionParentID = ""
		runtimePlan, err := planner.PrepareRuntime(plan, os.Stderr, "app.start session_id="+plan.Store.Meta().SessionID+" workspace="+plan.WorkspaceRoot+" model="+plan.ActiveSettings.Model, runtimeWiringOptions{FastMode: boot.fastModeState})
		if err != nil {
			return err
		}
		commandRegistry, err := commands.NewDefaultRegistryWithFilePrompts(boot.cfg.WorkspaceRoot, boot.cfg.Source.SettingsPath)
		if err != nil {
			runtimePlan.Close()
			return err
		}

		finalModel, runErr := runUILoopWithInitialPrompt(
			runtimePlan.Wiring,
			plan.ActiveSettings,
			runtimePlan.Logger,
			commandRegistry,
			nextSessionInitialPrompt,
			plan.SessionName,
			plan.ModelContractLocked,
			plan.ConfiguredModelName,
			plan.StatusConfig,
		)
		runtimePlan.Close()
		nextSessionInitialPrompt = ""
		if runErr != nil {
			return runErr
		}

		transition := extractUITransition(finalModel)
		nextSessionID, initialPrompt, parentSessionID, nextForceNewSession, shouldContinue, err := resolveSessionAction(ctx, boot, plan.Store, transition)
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
		if err := ensureAuthReady(ctx, boot.authManager, boot.oauthOpts, boot.cfg.Settings.Theme, boot.cfg.Settings.TUIAlternateScreen, boot.authInteractor); err != nil {
			return "", "", "", false, false, err
		}
		return store.Meta().SessionID, "", "", false, true, nil
	default:
		return "", "", "", false, false, nil
	}
}
