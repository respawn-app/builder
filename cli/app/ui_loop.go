package app

import (
	"builder/cli/app/commands"
	"builder/shared/config"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func runUILoop(wiring *runtimeWiring, active config.Settings, logger *runLogger, commandRegistry *commands.Registry) (tea.Model, error) {
	return runUILoopWithInitialPrompt(wiring, active, logger, commandRegistry, "", "", "", false, active.Model, uiStatusConfig{}, false)
}

func runUILoopWithInitialPrompt(wiring *runtimeWiring, active config.Settings, logger *runLogger, commandRegistry *commands.Registry, initialPrompt string, initialInput string, sessionName string, modelContractLocked bool, configuredModelName string, statusConfig uiStatusConfig, startupUpdateNotice bool) (tea.Model, error) {
	terminalCursor := newUITerminalCursorState()
	options := mainUIProgramOptions(active, terminalCursor)
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
		runtimeEvents = projectRuntimeEventChannel(wiring.eventBridge.Channel(), wiring.eventBridge.GapChannel(), runtimeEventStop)
	}
	askEvents := wiring.askEvents
	if askEvents == nil && wiring.askBridge != nil {
		askEvents = wiring.askBridge.Events()
	}
	sessionID := ""
	if runtimeClient != nil {
		sessionID = runtimeClient.MainView().Session.SessionID
	}

	model := NewProjectedUIModel(
		runtimeClient,
		runtimeEvents,
		askEvents,
		WithUILogger(logger),
		WithUIModelName(active.Model),
		WithUIConfiguredModelName(configuredModelName),
		WithUIThinkingLevel(active.ThinkingLevel),
		WithUIModelContractLocked(modelContractLocked),
		WithUITheme(active.Theme),
		WithUIDebug(active.Debug),
		WithUIAlternateScreenPolicy(active.TUIAlternateScreen),
		WithUICommandRegistry(commandRegistry),
		WithUIHasOtherSessions(wiring.hasOtherSessionsKnown, wiring.hasOtherSessions),
		WithUIBackgroundManager(wiring.background),
		WithUITurnQueueHook(wiring.turnQueueHook),
		WithUIProcessClient(newUIProcessClientWithReads(wiring.background, wiring.processViews, wiring.processControls)),
		WithUIWorktreeClient(wiring.worktrees),
		WithUIPromptHistory(wiring.promptHistory),
		WithUIStartupSubmit(initialPrompt),
		WithUIInitialInput(initialInput),
		WithUISessionName(sessionName),
		WithUISessionID(sessionID),
		WithUIStatusConfig(statusConfig),
		WithUIStartupUpdateNotice(startupUpdateNotice),
		WithUITerminalCursorState(terminalCursor),
	)
	if closable, ok := model.(interface{ Close() }); ok {
		defer closable.Close()
	}
	program := tea.NewProgram(model, options...)

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

func mainUIProgramOptions(active config.Settings, terminalCursor *uiTerminalCursorState) []tea.ProgramOption {
	return mainUIProgramOptionsWithOutput(active, terminalCursor, os.Stdout)
}

func mainUIProgramOptionsWithOutput(active config.Settings, terminalCursor *uiTerminalCursorState, output io.Writer) []tea.ProgramOption {
	options := []tea.ProgramOption{tea.WithFilter(terminalCursorProgramFilter(terminalCursor))}
	if terminalCursor != nil {
		options = append(options, tea.WithOutput(mainUIProgramOutputWriter(terminalCursor, output)))
	}
	return options
}

func mainUIProgramOutputWriter(terminalCursor *uiTerminalCursorState, output io.Writer) io.Writer {
	return newUITerminalCursorWriter(output, terminalCursor)
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
