package app

import (
	"builder/cli/app/commands"
	"builder/shared/config"

	tea "github.com/charmbracelet/bubbletea"
)

func runUILoop(wiring *runtimeWiring, active config.Settings, logger *runLogger, commandRegistry *commands.Registry) (tea.Model, error) {
	return runUILoopWithInitialPrompt(wiring, active, logger, commandRegistry, "", "", "", false, active.Model, uiStatusConfig{})
}

func runUILoopWithInitialPrompt(wiring *runtimeWiring, active config.Settings, logger *runLogger, commandRegistry *commands.Registry, initialPrompt string, initialInput string, sessionName string, modelContractLocked bool, configuredModelName string, statusConfig uiStatusConfig) (tea.Model, error) {
	options := mainUIProgramOptions(active)
	runtimeClient := wiring.runtimeClient
	if runtimeClient == nil {
		sessionID := ""
		if wiring.engine != nil {
			sessionID = wiring.engine.SessionID()
		}
		runtimeClient = newUIRuntimeClientWithReads(sessionID, wiring.sessionViews, wiring.runtimeControls)
	}
	runtimeEventStop := make(chan struct{})
	defer close(runtimeEventStop)
	runtimeEvents := wiring.runtimeEvents
	if runtimeEvents == nil && wiring.eventBridge != nil {
		runtimeEvents = projectRuntimeEventChannel(wiring.eventBridge.Channel(), runtimeEventStop)
	}
	askEvents := wiring.askEvents
	if askEvents == nil && wiring.askBridge != nil {
		askEvents = wiring.askBridge.Events()
	}
	sessionID := ""
	if runtimeClient != nil {
		sessionID = runtimeClient.MainView().Session.SessionID
	}

	program := tea.NewProgram(NewProjectedUIModel(
		runtimeClient,
		runtimeEvents,
		askEvents,
		WithUILogger(logger),
		WithUIModelName(active.Model),
		WithUIConfiguredModelName(configuredModelName),
		WithUIThinkingLevel(active.ThinkingLevel),
		WithUIModelContractLocked(modelContractLocked),
		WithUITheme(active.Theme),
		WithUIAlternateScreenPolicy(active.TUIAlternateScreen),
		WithUICommandRegistry(commandRegistry),
		WithUIBackgroundManager(wiring.background),
		WithUIProcessClient(newUIProcessClientWithReads(wiring.background, wiring.processViews, wiring.processControls)),
		WithUIPromptHistory(wiring.promptHistory),
		WithUIStartupSubmit(initialPrompt),
		WithUIInitialInput(initialInput),
		WithUISessionName(sessionName),
		WithUISessionID(sessionID),
		WithUIStatusConfig(statusConfig),
	), options...)

	finalModel, runErr := program.Run()
	if wiring.eventBridge != nil {
		if dropped := wiring.eventBridge.Dropped(); dropped > 0 {
		logger.Logf("runtime.event.drop.total=%d", dropped)
		}
	}
	if runErr != nil {
		logger.Logf("app.exit err=%q", runErr.Error())
		return nil, runErr
	}
	logger.Logf("app.exit ok")
	return finalModel, nil
}

func mainUIProgramOptions(active config.Settings) []tea.ProgramOption {
	options := []tea.ProgramOption{tea.WithFilter(customKeyProgramFilter)}
	return options
}

func extractUITransition(model tea.Model) UITransition {
	if model == nil {
		return UITransition{Action: UIActionNone}
	}
	typed, ok := model.(*uiModel)
	if !ok {
		return UITransition{Action: UIActionNone}
	}
	return typed.Transition()
}
