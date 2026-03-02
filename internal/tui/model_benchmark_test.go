package tui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func benchmarkDetailEntries(count int) []TranscriptEntry {
	entries := make([]TranscriptEntry, 0, count)
	for index := 0; index < count; index++ {
		entries = append(entries, TranscriptEntry{Role: "user", Text: fmt.Sprintf("request %d", index)})
		entries = append(entries, TranscriptEntry{Role: "assistant", Text: fmt.Sprintf("response %d\n\n```yaml\nroot:\n  idx: %d\n```", index, index)})
	}
	return entries
}

func BenchmarkToggleModeFirstDetailSnapshot(b *testing.B) {
	entries := benchmarkDetailEntries(600)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		model := NewModel(WithTheme("dark"))
		next, _ := model.Update(SetViewportSizeMsg{Lines: 40, Width: 120})
		model = next.(Model)
		next, _ = model.Update(SetConversationMsg{Entries: entries})
		model = next.(Model)
		b.StartTimer()
		next, _ = model.Update(ToggleModeMsg{})
		model = next.(Model)
		_ = model.View()
	}
}

func BenchmarkToggleModeReopenDetailSnapshot(b *testing.B) {
	entries := benchmarkDetailEntries(600)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		model := NewModel(WithTheme("dark"))
		next, _ := model.Update(SetViewportSizeMsg{Lines: 40, Width: 120})
		model = next.(Model)
		next, _ = model.Update(SetConversationMsg{Entries: entries})
		model = next.(Model)
		next, _ = model.Update(ToggleModeMsg{})
		model = next.(Model)
		next, _ = model.Update(ToggleModeMsg{})
		model = next.(Model)
		b.StartTimer()
		next, _ = model.Update(ToggleModeMsg{})
		model = next.(Model)
		_ = model.View()
	}
}

func BenchmarkDetailScrollStep(b *testing.B) {
	entries := benchmarkDetailEntries(600)
	model := NewModel(WithTheme("dark"))
	next, _ := model.Update(SetViewportSizeMsg{Lines: 40, Width: 120})
	model = next.(Model)
	next, _ = model.Update(SetConversationMsg{Entries: entries})
	model = next.(Model)
	next, _ = model.Update(ToggleModeMsg{})
	model = next.(Model)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
		model = next.(Model)
		_ = model.View()
	}
}
