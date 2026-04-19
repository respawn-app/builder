package app

func testActiveAsk(m *uiModel) *askEvent {
	if m == nil {
		return nil
	}
	return m.ask.current
}

func testSetActiveAsk(m *uiModel, event *askEvent) {
	if m == nil {
		return
	}
	m.ask.currentToken = nextNonZeroToken(m.ask.currentToken)
	m.ask.current = event
	if event != nil {
		m.setInputMode(uiInputModeAsk)
		return
	}
	m.restorePrimaryInputMode()
}

func testAskFreeform(m *uiModel) bool {
	if m == nil {
		return false
	}
	return m.ask.freeform
}

func testAskCursor(m *uiModel) int {
	if m == nil {
		return 0
	}
	return m.ask.cursor
}

func testAskInput(m *uiModel) string {
	if m == nil {
		return ""
	}
	return m.ask.input
}

func testSetAskInput(m *uiModel, input string) {
	if m == nil {
		return
	}
	m.ask.input = input
}

func testAskInputCursor(m *uiModel) int {
	if m == nil {
		return 0
	}
	return m.ask.inputCursor
}

func testSetAskInputCursor(m *uiModel, cursor int) {
	if m == nil {
		return
	}
	m.ask.inputCursor = cursor
}

func testAskQueue(m *uiModel) []askEvent {
	if m == nil {
		return nil
	}
	return m.ask.queue
}

func testProcessListOpen(m *uiModel) bool {
	if m == nil {
		return false
	}
	return m.processList.open
}

func testProcessListOwnsTranscriptMode(m *uiModel) bool {
	if m == nil {
		return false
	}
	return m.processList.ownsTranscriptMode
}

func testRollbackSelecting(m *uiModel) bool {
	if m == nil {
		return false
	}
	return m.rollback.isSelecting()
}

func testRollbackEditing(m *uiModel) bool {
	if m == nil {
		return false
	}
	return m.rollback.isEditing()
}

func testSetRollbackSelecting(m *uiModel, selection int, selectedTranscriptEntry int) {
	if m == nil {
		return
	}
	m.rollback.phase = uiRollbackPhaseSelection
	m.rollback.selection = selection
	m.rollback.selectedTranscriptEntry = selectedTranscriptEntry
	m.setInputMode(uiInputModeRollbackSelection)
}

func testSetRollbackEditing(m *uiModel, selection int, selectedTranscriptEntry int) {
	if m == nil {
		return
	}
	m.rollback.phase = uiRollbackPhaseEditing
	m.rollback.selection = selection
	m.rollback.selectedTranscriptEntry = selectedTranscriptEntry
	m.setInputMode(uiInputModeRollbackEdit)
}

func testRollbackSelection(m *uiModel) int {
	if m == nil {
		return 0
	}
	return m.rollback.selection
}

func testRollbackCandidates(m *uiModel) []rollbackCandidate {
	if m == nil {
		return nil
	}
	return m.rollback.candidates
}

func testRollbackOwnsTranscriptMode(m *uiModel) bool {
	if m == nil {
		return false
	}
	return m.rollback.ownsTranscriptMode
}

func testStatusOpen(m *uiModel) bool {
	if m == nil {
		return false
	}
	return m.status.open
}

func testStatusOwnsTranscriptMode(m *uiModel) bool {
	if m == nil {
		return false
	}
	return m.status.ownsTranscriptMode
}

func testStatusRefreshToken(m *uiModel) uint64 {
	if m == nil {
		return 0
	}
	return m.status.refreshToken
}
