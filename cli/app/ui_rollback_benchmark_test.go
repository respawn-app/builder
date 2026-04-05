package app

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func benchmarkRollbackOverlayEntries(count int) []UITranscriptEntry {
	entries := make([]UITranscriptEntry, 0, count*2)
	for index := 0; index < count; index++ {
		entries = append(entries,
			UITranscriptEntry{Role: "user", Text: fmt.Sprintf("u-%04d", index)},
			UITranscriptEntry{Role: "assistant", Text: fmt.Sprintf("a-%04d", index)},
		)
	}
	return entries
}

func benchmarkRollbackOverlayModel(entries []UITranscriptEntry) *uiModel {
	model := newProjectedStaticUIModel(WithUIInitialTranscript(entries))
	next, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	updated, _ := next.(*uiModel)
	if updated == nil {
		return model
	}
	return updated
}

func BenchmarkRollbackOverlayEnter(b *testing.B) {
	entries := benchmarkRollbackOverlayEntries(200)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		model := benchmarkRollbackOverlayModel(entries)
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
		model = next.(*uiModel)
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
		model = next.(*uiModel)
		_ = model.View()
	}
}

func BenchmarkRollbackOverlayMoveSelection(b *testing.B) {
	entries := benchmarkRollbackOverlayEntries(200)
	model := benchmarkRollbackOverlayModel(entries)
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(*uiModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(*uiModel)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
		model = next.(*uiModel)
		_ = model.View()
	}
}
