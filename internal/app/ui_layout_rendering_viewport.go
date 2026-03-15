package app

import "builder/internal/tui"

func (l uiViewLayout) effectiveWidth() int {
	m := l.model
	if m.termWidth > 0 {
		return m.termWidth
	}
	return 120
}

func (l uiViewLayout) effectiveHeight() int {
	m := l.model
	if m.termHeight > 0 {
		return m.termHeight
	}
	return 32
}

func (l uiViewLayout) calcChatLines() int {
	height := l.effectiveHeight()
	if l.model.view.Mode() == tui.ModeDetail {
		chat := height - 1
		if chat < 1 {
			return 1
		}
		return chat
	}

	inputLines := l.inputPanelLineCount(l.effectiveWidth(), height)
	queuedLines := l.queuedPaneLineCount()
	pickerLines := 0
	if l.model.slashCommandPicker().visible {
		pickerLines = slashCommandPickerLines
	}
	chat := height - inputLines - queuedLines - pickerLines - 1
	if chat < 1 {
		return 1
	}
	return chat
}

func (l uiViewLayout) syncViewport() {
	width := l.effectiveWidth()
	l.syncNativeLiveRegionState()
	l.model.nativeReplayWidth = width
	l.model.forwardToView(tui.SetViewportSizeMsg{
		Lines: l.calcChatLines(),
		Width: width,
	})
}

func (l uiViewLayout) shouldRenderSoftCursor() bool {
	m := l.model
	return !m.isInputLocked() && m.activeAsk == nil && !m.rollbackMode
}
