package tui

import "fmt"

func benchmarkDetailEntries(count int) []TranscriptEntry {
	entries := make([]TranscriptEntry, 0, count)
	for index := 0; index < count; index++ {
		entries = append(entries, TranscriptEntry{Role: "user", Text: fmt.Sprintf("request %d", index)})
		entries = append(entries, TranscriptEntry{Role: "assistant", Text: fmt.Sprintf("response %d\n\n```yaml\nroot:\n  idx: %d\n```", index, index)})
	}
	return entries
}
