package app

type uiInputMode string

const (
	uiInputModeMain              uiInputMode = "main"
	uiInputModeAsk               uiInputMode = "ask"
	uiInputModeRollbackSelection uiInputMode = "rollback_selection"
	uiInputModeRollbackEdit      uiInputMode = "rollback_edit"
	uiInputModeProcessList       uiInputMode = "process_list"
)

type uiInputModeState struct {
	Mode           uiInputMode
	InputLocked    bool
	Busy           bool
	ShowsMainInput bool
	ShowsAskInput  bool
}

func (m *uiModel) inputMode() uiInputMode {
	switch {
	case m == nil:
		return uiInputModeMain
	case m.activeAsk != nil:
		return uiInputModeAsk
	case m.psVisible:
		return uiInputModeProcessList
	case m.rollbackMode:
		return uiInputModeRollbackSelection
	case m.rollbackEditing:
		return uiInputModeRollbackEdit
	default:
		return uiInputModeMain
	}
}

func (m *uiModel) inputModeState() uiInputModeState {
	mode := m.inputMode()
	return uiInputModeState{
		Mode:           mode,
		InputLocked:    m != nil && m.isInputLocked(),
		Busy:           m != nil && m.busy,
		ShowsMainInput: mode.showsMainInput(),
		ShowsAskInput:  mode.showsAskInput(),
	}
}

func (mode uiInputMode) showsMainInput() bool {
	return mode == uiInputModeMain || mode == uiInputModeRollbackEdit
}

func (mode uiInputMode) showsAskInput() bool {
	return mode == uiInputModeAsk
}

func (mode uiInputMode) suppressesMainInput() bool {
	return !mode.showsMainInput() && !mode.showsAskInput()
}
