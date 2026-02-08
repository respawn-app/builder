package app

import (
	"builder/internal/config"

	tea "github.com/charmbracelet/bubbletea"
)

func runUILoop(wiring *runtimeWiring, active config.Settings, logger *runLogger) (tea.Model, error) {
	program := tea.NewProgram(NewUIModel(
		wiring.engine,
		wiring.eventBridge.Channel(),
		wiring.askBridge.Events(),
		WithUILogger(logger),
		WithUIModelName(active.Model),
		WithUITheme(active.Theme),
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

func extractUIAction(model tea.Model) UIAction {
	if model == nil {
		return UIActionNone
	}
	typed, ok := model.(*uiModel)
	if !ok {
		return UIActionNone
	}
	return typed.Action()
}
