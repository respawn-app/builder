package app

import (
	"builder/cli/tui"
)

func nativePendingToolEntries(entries []tui.TranscriptEntry) []tui.TranscriptEntry {
	return tui.PendingToolEntries(entries)
}

func renderNativePendingToolSnapshot(entries []tui.TranscriptEntry, theme string, width int, spinnerFrame int) string {
	pending := nativePendingToolEntries(entries)
	if len(pending) == 0 {
		return ""
	}
	return renderNativePendingOngoingSnapshot(pending, theme, width, spinnerFrame)
}

func renderNativePendingOngoingSnapshot(entries []tui.TranscriptEntry, theme string, width int, spinnerFrame int) string {
	if len(entries) == 0 {
		return ""
	}
	frame := pendingToolSpinnerFrame(spinnerFrame)
	return renderStyledNativeProjectionLines(tui.RenderPendingOngoingSnapshotLines(entries, theme, width, frame), theme, width)
}
