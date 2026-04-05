package app

import (
	"context"
	"errors"
	"os"
	"strings"

	"builder/cli/app/commands"
	serverlifecycle "builder/server/lifecycle"
	"builder/server/session"
	"builder/shared/serverapi"
)

func runSessionLifecycle(ctx context.Context, server embeddedServer, interactor authInteractor, initialSessionID string) error {
	planner := newSessionLaunchPlanner(server)
	currentSessionID := strings.TrimSpace(initialSessionID)
	nextSessionInitialPrompt := ""
	nextSessionInitialInput := ""
	nextSessionParentID := ""
	forceNewSession := false
	for {
		plan, err := planner.PlanSession(ctx, sessionLaunchRequest{
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
		runtimePlan, err := planner.PrepareRuntime(ctx, plan, os.Stderr, "app.start session_id="+plan.SessionID+" workspace="+plan.WorkspaceRoot+" model="+plan.ActiveSettings.Model)
		if err != nil {
			return err
		}
		cfg := server.Config()
		commandRegistry, err := commands.NewDefaultRegistryWithFilePrompts(cfg.WorkspaceRoot, cfg.Source.SettingsPath)
		if err != nil {
			runtimePlan.Close()
			return err
		}
		initialInput := sessionLaunchInitialInputFromServer(ctx, server, plan.SessionID, nextSessionInitialInput)

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
		if err := persistSessionDraftToServer(ctx, server, plan.SessionID, finalModel); err != nil {
			return err
		}

		transition := extractUITransition(finalModel)
		resolved, err := resolveSessionAction(ctx, server, interactor, plan.SessionID, transition)
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
	return serverlifecycle.InitialInput(store, transitionInput)
}

func sessionLaunchInitialInputFromServer(ctx context.Context, server embeddedServer, sessionID string, transitionInput string) string {
	if server == nil || server.SessionLifecycleClient() == nil {
		return transitionInput
	}
	resp, err := server.SessionLifecycleClient().GetInitialInput(ctx, serverapi.SessionInitialInputRequest{
		SessionID:       strings.TrimSpace(sessionID),
		TransitionInput: transitionInput,
	})
	if err != nil {
		return transitionInput
	}
	return resp.Input
}

func persistSessionDraft(store *session.Store, model any) error {
	if store == nil {
		return nil
	}
	ui, ok := model.(*uiModel)
	if !ok || ui == nil {
		return nil
	}
	return serverlifecycle.PersistInputDraft(store, ui.input)
}

func persistSessionDraftToServer(ctx context.Context, server embeddedServer, sessionID string, model any) error {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	ui, ok := model.(*uiModel)
	if !ok || ui == nil {
		return nil
	}
	if server == nil || server.SessionLifecycleClient() == nil {
		return nil
	}
	_, err := server.SessionLifecycleClient().PersistInputDraft(ctx, serverapi.SessionPersistInputDraftRequest{SessionID: strings.TrimSpace(sessionID), Input: ui.input})
	return err
}

type resolvedSessionAction struct {
	NextSessionID   string
	InitialPrompt   string
	InitialInput    string
	ParentSessionID string
	ForceNewSession bool
	ShouldContinue  bool
}

func resolveSessionAction(ctx context.Context, server embeddedServer, interactor authInteractor, sessionID string, transition UITransition) (resolvedSessionAction, error) {
	if server == nil || server.SessionLifecycleClient() == nil {
		return resolvedSessionAction{}, errors.New("session lifecycle client is required")
	}
	resolved, err := server.SessionLifecycleClient().ResolveTransition(ctx, serverapi.SessionResolveTransitionRequest{
		SessionID: strings.TrimSpace(sessionID),
		Transition: serverapi.SessionTransition{
			Action:               string(transition.Action),
			InitialPrompt:        transition.InitialPrompt,
			InitialInput:         transition.InitialInput,
			TargetSessionID:      transition.TargetSessionID,
			ForkUserMessageIndex: transition.ForkUserMessageIndex,
			ParentSessionID:      transition.ParentSessionID,
		},
	})
	if err != nil {
		return resolvedSessionAction{}, err
	}
	if resolved.RequiresReauth {
		if err := server.Reauthenticate(ctx, interactor); err != nil {
			return resolvedSessionAction{}, err
		}
	}
	return resolvedSessionAction{
		NextSessionID:   resolved.NextSessionID,
		InitialPrompt:   resolved.InitialPrompt,
		InitialInput:    resolved.InitialInput,
		ParentSessionID: resolved.ParentSessionID,
		ForceNewSession: resolved.ForceNewSession,
		ShouldContinue:  resolved.ShouldContinue,
	}, nil
}
