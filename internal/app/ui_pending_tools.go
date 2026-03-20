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
	tail := entries[start:]
	include := make(map[int]struct{})
	consumedResults := make(map[int]struct{})
	for idx, entry := range tail {
		if strings.TrimSpace(entry.Role) != "tool_call" {
			continue
		}
		if strings.TrimSpace(ongoingTranscriptText(entry)) == "" {
			continue
		}
		include[idx] = struct{}{}
		resultIdx := nativeFindMatchingToolResultIndex(tail, idx, consumedResults)
		if resultIdx < 0 {
			continue
		}
		include[resultIdx] = struct{}{}
		consumedResults[resultIdx] = struct{}{}
	}
	pending := make([]tui.TranscriptEntry, 0, len(include))
	for idx, entry := range tail {
		if _, ok := include[idx]; !ok {
			continue
		}
		pending = append(pending, entry)
	}
	return pending
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
	rendered := tui.RenderPendingToolSnapshot(pending, theme, width, frame)
	return styleNativeReplayDividers(rendered, theme, width)
}
