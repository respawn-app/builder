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
		runtimePlan, err := planner.PrepareRuntime(plan, os.Stderr, "app.start session_id="+plan.Store.Meta().SessionID+" workspace="+plan.WorkspaceRoot+" model="+plan.ActiveSettings.Model)
		if err != nil {
			return err
		}
		cfg := server.Config()
		commandRegistry, err := commands.NewDefaultRegistryWithFilePrompts(cfg.WorkspaceRoot, cfg.Source.SettingsPath)
		if err != nil {
			runtimePlan.Close()
			return err
		}
		initialInput := sessionLaunchInitialInputFromServer(server, plan.Store, nextSessionInitialInput)

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
		if err := persistSessionDraftToServer(server, plan.Store, finalModel); err != nil {
			return err
		}

		transition := extractUITransition(finalModel)
		resolved, err := resolveSessionAction(ctx, server, interactor, plan.Store, transition)
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

func sessionLaunchInitialInputFromServer(server embeddedServer, store *session.Store, transitionInput string) string {
	sessionID := ""
	if store != nil {
		sessionID = store.Meta().SessionID
	}
	if server == nil || server.SessionLifecycleClient() == nil {
		return sessionLaunchInitialInput(store, transitionInput)
	}
	resp, err := server.SessionLifecycleClient().GetInitialInput(context.Background(), serverapi.SessionInitialInputRequest{
		SessionID:       strings.TrimSpace(sessionID),
		TransitionInput: transitionInput,
	})
	if err != nil {
		return sessionLaunchInitialInput(store, transitionInput)
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

func persistSessionDraftToServer(server embeddedServer, store *session.Store, model any) error {
	if store == nil {
		return nil
	}
	ui, ok := model.(*uiModel)
	if !ok || ui == nil {
		return nil
	}
	if server == nil || server.SessionLifecycleClient() == nil {
		return persistSessionDraft(store, model)
	}
	_, err := server.SessionLifecycleClient().PersistInputDraft(context.Background(), serverapi.SessionPersistInputDraftRequest{SessionID: strings.TrimSpace(store.Meta().SessionID), Input: ui.input})
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

func resolveSessionAction(ctx context.Context, server embeddedServer, interactor authInteractor, store *session.Store, transition UITransition) (resolvedSessionAction, error) {
	if server == nil || server.SessionLifecycleClient() == nil {
		return resolvedSessionAction{}, errors.New("session lifecycle client is required")
	}
	sessionID := ""
	if store != nil {
		sessionID = store.Meta().SessionID
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
