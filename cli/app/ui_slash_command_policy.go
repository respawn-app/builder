package app

const slashCommandEditModeError = "Slash commands are unavailable while editing a message"

func (m *uiModel) slashCommandDisabledReason() string {
	if m == nil {
		return ""
	}
	switch m.inputMode() {
	case uiInputModeRollbackSelection, uiInputModeRollbackEdit:
		return slashCommandEditModeError
	default:
		return ""
	}
}

func (m *uiModel) slashCommandInputBlocked(text string) (string, bool) {
	if m == nil {
		return "", false
	}
	parsed := parseSlashCommandInput(text)
	if !parsed.active {
		return "", false
	}
	reason := m.slashCommandDisabledReason()
	if reason == "" {
		return "", false
	}
	return reason, true
}
