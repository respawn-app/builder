package app

import (
	"strings"

	"builder/internal/tui"
)

func nativePendingToolEntries(entries []tui.TranscriptEntry) []tui.TranscriptEntry {
	if len(entries) == 0 {
		return nil
	}
	start := nativeCommittedPrefixEnd(entries)
	if start >= len(entries) {
		return nil
	}
	pending := make([]tui.TranscriptEntry, 0, len(entries)-start)
	for _, entry := range entries[start:] {
		if strings.TrimSpace(entry.Role) != "tool_call" {
			continue
		}
		if strings.TrimSpace(ongoingTranscriptText(entry)) == "" {
			continue
		}
		pending = append(pending, entry)
	}
	return pending
}

func renderNativePendingToolSnapshot(entries []tui.TranscriptEntry, theme string, width int) string {
	pending := nativePendingToolEntries(entries)
	if len(pending) == 0 {
		return ""
	}
	rendered := renderNativeCommittedSnapshot(pending, theme, width)
	return styleNativeReplayDividers(rendered, theme, width)
}
