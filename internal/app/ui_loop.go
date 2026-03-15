package app

import (
	"builder/internal/app/commands"
	"builder/internal/config"

	tea "github.com/charmbracelet/bubbletea"
)

func runUILoop(wiring *runtimeWiring, active config.Settings, logger *runLogger, commandRegistry *commands.Registry) (tea.Model, error) {
	return runUILoopWithInitialPrompt(wiring, active, logger, commandRegistry, "", "", false)
}

func runUILoopWithInitialPrompt(wiring *runtimeWiring, active config.Settings, logger *runLogger, commandRegistry *commands.Registry, initialPrompt string, sessionName string, modelContractLocked bool) (tea.Model, error) {
	options := mainUIProgramOptions(active)

	program := tea.NewProgram(NewUIModel(
		wiring.engine,
		wiring.eventBridge.Channel(),
		wiring.askBridge.Events(),
		WithUILogger(logger),
		WithUIModelName(active.Model),
		WithUIThinkingLevel(active.ThinkingLevel),
		WithUIModelContractLocked(modelContractLocked),
		WithUITheme(active.Theme),
		WithUIAlternateScreenPolicy(active.TUIAlternateScreen),
		WithUIScrollMode(active.TUIScrollMode),
		WithUICommandRegistry(commandRegistry),
		WithUIBackgroundManager(wiring.background),
		WithUIStartupSubmit(initialPrompt),
		WithUISessionName(sessionName),
		WithUISessionID(wiring.engine.SessionID()),
	), options...)

	finalModel, runErr := program.Run()
	if dropped := wiring.eventBridge.Dropped(); dropped > 0 {
		logger.Logf("runtime.event.drop.total=%d", dropped)
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
	if shouldStartMainUIInAltScreen(active.TUIAlternateScreen) && active.TUIScrollMode != config.TUIScrollModeNative {
		options = append(options, tea.WithAltScreen())
	}
	if active.TUIScrollMode == config.TUIScrollModeAlt {
		options = append(options, tea.WithMouseCellMotion())
	}
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
