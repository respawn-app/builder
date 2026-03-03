package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

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

func BenchmarkDetailSelectionFocusStep(b *testing.B) {
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
		entryIndex := i % len(entries)
		next, _ = model.Update(SetSelectedTranscriptEntryMsg{EntryIndex: entryIndex, Active: true, RefreshDetailSnapshot: false})
		model = next.(Model)
		next, _ = model.Update(FocusTranscriptEntryMsg{EntryIndex: entryIndex, Center: true})
		model = next.(Model)
		_ = model.View()
	}
}

func BenchmarkDetailSelectionFocusStepWithRefresh(b *testing.B) {
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
		entryIndex := i % len(entries)
		next, _ = model.Update(SetSelectedTranscriptEntryMsg{EntryIndex: entryIndex, Active: true, RefreshDetailSnapshot: true})
		model = next.(Model)
		next, _ = model.Update(FocusTranscriptEntryMsg{EntryIndex: entryIndex, Center: true})
		model = next.(Model)
		_ = model.View()
	}
}
