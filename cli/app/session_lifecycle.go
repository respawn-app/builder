package app

import (
	"context"
	"os"
	"strings"

	"builder/cli/app/commands"
	serverlifecycle "builder/server/lifecycle"
	"builder/server/session"
	"builder/server/sessioncontrol"
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
		runtimePlan, err := planner.PrepareRuntime(plan, os.Stderr, "app.start session_id="+plan.Store.Meta().SessionID+" workspace="+plan.WorkspaceRoot+" model="+plan.ActiveSettings.Model, runtimeWiringOptions{FastMode: server.FastModeState()})
		if err != nil {
			return err
		}
		cfg := server.Config()
		commandRegistry, err := commands.NewDefaultRegistryWithFilePrompts(cfg.WorkspaceRoot, cfg.Source.SettingsPath)
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

type resolvedSessionAction struct {
	NextSessionID   string
	InitialPrompt   string
	InitialInput    string
	ParentSessionID string
	ForceNewSession bool
	ShouldContinue  bool
}

func resolveSessionAction(ctx context.Context, server embeddedServer, interactor authInteractor, store *session.Store, transition UITransition) (resolvedSessionAction, error) {
	controller := sessioncontrol.Controller{
		Config:       server.Config(),
		ContainerDir: server.ContainerDir(),
		AuthManager:  server.AuthManager(),
		Reauth: func(ctx context.Context) error {
			cfg := server.Config()
			return ensureAuthReady(ctx, server.AuthManager(), server.OAuthOptions(), cfg.Settings.Theme, cfg.Settings.TUIAlternateScreen, interactor)
		},
	}
	resolved, err := controller.ResolveTransition(ctx, store, serverlifecycle.Transition{
		Action:               serverlifecycle.Action(transition.Action),
		InitialPrompt:        transition.InitialPrompt,
		InitialInput:         transition.InitialInput,
		TargetSessionID:      transition.TargetSessionID,
		ForkUserMessageIndex: transition.ForkUserMessageIndex,
		ParentSessionID:      transition.ParentSessionID,
	})
	if err != nil {
		return resolvedSessionAction{}, err
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
