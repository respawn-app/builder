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
	nextSessionInitialInput := ""
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
		initialInput := sessionLaunchInitialInput(plan.Store, nextSessionInitialInput)

		finalModel, runErr := runUILoopWithInitialPrompt(
			runtimePlan.Wiring,
			plan.ActiveSettings,
			runtimePlan.Logger,
			commandRegistry,
			nextSessionInitialPrompt,
			initialInput,
			plan.SessionName,
			plan.ModelContractLocked,
			plan.ConfiguredModelName,
			plan.StatusConfig,
		)
		runtimePlan.Close()
		nextSessionInitialPrompt = ""
		nextSessionInitialInput = ""
		if runErr != nil {
			return runErr
		}
		if err := persistSessionDraft(plan.Store, finalModel); err != nil {
			return err
		}

		transition := extractUITransition(finalModel)
		resolved, err := resolveSessionAction(ctx, boot, plan.Store, transition)
		if err != nil {
			return err
		}
		if !resolved.ShouldContinue {
			return nil
		}
		currentSessionID = resolved.NextSessionID
		nextSessionInitialPrompt = resolved.InitialPrompt
		nextSessionInitialInput = resolved.InitialInput
		nextSessionParentID = resolved.ParentSessionID
		forceNewSession = resolved.ForceNewSession
	}
}

func sessionLaunchInitialInput(store *session.Store, transitionInput string) string {
	if store == nil {
		return transitionInput
	}
	meta := store.Meta()
	if meta.InputDraft != "" {
		return meta.InputDraft
	}
	return transitionInput
}

func persistSessionDraft(store *session.Store, model any) error {
	if store == nil {
		return nil
	}
	ui, ok := model.(*uiModel)
	if !ok || ui == nil {
		return nil
	}
	return store.SetInputDraft(ui.input)
}

type resolvedSessionAction struct {
	NextSessionID   string
	InitialPrompt   string
	InitialInput    string
	ParentSessionID string
	ForceNewSession bool
	ShouldContinue  bool
}

func resolveSessionAction(ctx context.Context, boot appBootstrap, store *session.Store, transition UITransition) (resolvedSessionAction, error) {
	switch transition.Action {
	case UIActionNewSession:
		return resolvedSessionAction{
			InitialPrompt:   transition.InitialPrompt,
			ParentSessionID: transition.ParentSessionID,
			ForceNewSession: true,
			ShouldContinue:  true,
		}, nil
	case UIActionResume:
		return resolvedSessionAction{ShouldContinue: true}, nil
	case UIActionOpenSession:
		return resolvedSessionAction{
			NextSessionID:  strings.TrimSpace(transition.TargetSessionID),
			InitialInput:   transition.InitialInput,
			ShouldContinue: true,
		}, nil
	case UIActionForkRollback:
		if store == nil {
			return resolvedSessionAction{}, errors.New("current store is required for rollback fork")
		}
		if transition.ForkUserMessageIndex <= 0 {
			return resolvedSessionAction{}, errors.New("rollback fork user message index must be > 0")
		}
		parentMeta := store.Meta()
		baseName := strings.TrimSpace(parentMeta.Name)
		if baseName == "" {
			baseName = parentMeta.SessionID
		}
		forkName := strings.TrimSpace(baseName + " → edit u" + strconv.Itoa(transition.ForkUserMessageIndex))
		forkedStore, err := session.ForkAtUserMessage(store, transition.ForkUserMessageIndex, forkName)
		if err != nil {
			return resolvedSessionAction{}, err
		}
		return resolvedSessionAction{
			NextSessionID:  forkedStore.Meta().SessionID,
			InitialPrompt:  transition.InitialPrompt,
			ShouldContinue: true,
		}, nil
	case UIActionLogout:
		if _, err := boot.authManager.ClearMethod(ctx, true); err != nil {
			return resolvedSessionAction{}, err
		}
		if err := ensureAuthReady(ctx, boot.authManager, boot.oauthOpts, boot.cfg.Settings.Theme, boot.cfg.Settings.TUIAlternateScreen, boot.authInteractor); err != nil {
			return resolvedSessionAction{}, err
		}
		sessionID := ""
		if store != nil {
			sessionID = store.Meta().SessionID
		}
		return resolvedSessionAction{NextSessionID: sessionID, ShouldContinue: true}, nil
	default:
		return resolvedSessionAction{}, nil
	}
}
