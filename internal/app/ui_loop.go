package app

import (
	"builder/internal/app/commands"
	"builder/internal/config"

	tea "github.com/charmbracelet/bubbletea"
)

func runUILoop(wiring *runtimeWiring, active config.Settings, logger *runLogger, commandRegistry *commands.Registry) (tea.Model, error) {
	return runUILoopWithInitialPrompt(wiring, active, logger, commandRegistry, "")
}

func runUILoopWithInitialPrompt(wiring *runtimeWiring, active config.Settings, logger *runLogger, commandRegistry *commands.Registry, initialPrompt string) (tea.Model, error) {
	program := tea.NewProgram(NewUIModel(
		wiring.engine,
		wiring.eventBridge.Channel(),
		wiring.askBridge.Events(),
		WithUILogger(logger),
		WithUIModelName(active.Model),
		WithUITheme(active.Theme),
		WithUICommandRegistry(commandRegistry),
		WithUIStartupSubmit(initialPrompt),
	), tea.WithAltScreen())

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
