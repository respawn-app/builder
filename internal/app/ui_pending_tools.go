package app

import (
	"builder/internal/tui"
)

func nativePendingToolEntries(entries []tui.TranscriptEntry) []tui.TranscriptEntry {
	return tui.PendingToolEntries(entries)
}

func renderNativePendingToolSnapshot(entries []tui.TranscriptEntry, theme string, width int, spinnerFrame int) string {
	pending := nativePendingToolEntries(entries)
	if len(pending) == 0 {
		return ""
	}
	frame := ""
	if len(pendingToolSpinner.Frames) > 0 {
		index := spinnerFrame % len(pendingToolSpinner.Frames)
		if index < 0 {
			index = 0
		}
		frame = pendingToolSpinner.Frames[index]
	}
	return renderStyledNativeProjectionLines(tui.RenderPendingToolSnapshotLines(pending, theme, width, frame), theme, width)
}
