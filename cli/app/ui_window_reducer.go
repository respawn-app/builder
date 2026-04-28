package app

import (
	"time"

	"builder/cli/tui"

	tea "github.com/charmbracelet/bubbletea"
)

type uiWindowFeatureReducer struct {
	model *uiModel
}

func (m *uiModel) windowReducer() uiWindowFeatureReducer {
	return uiWindowFeatureReducer{model: m}
}

func (r uiWindowFeatureReducer) Update(msg tea.Msg) uiFeatureUpdateResult {
	m := r.model
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		previousWidth := m.termWidth
		previousHeight := m.termHeight
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		m.windowSizeKnown = true
		m.syncViewport()
		if m.nativeHistoryReplayed && previousWidth > 0 && previousWidth != msg.Width {
			committedEntries := committedTranscriptEntriesForApp(m.transcriptEntries)
			if len(committedEntries) == 0 {
				m.resetNativeHistoryState()
				m.nativeHistoryReplayed = true
			} else {
				m.rebaseNativeProjection(m.nativeCommittedProjection(committedEntries), m.transcriptBaseOffset, len(committedEntries))
			}
		}
		if !m.nativeHistoryReplayed {
			return handledUIFeatureUpdate(m, m.syncNativeHistoryFromTranscript())
		}
		if previousWidth > 0 && previousHeight > 0 && previousWidth != msg.Width && m.view.Mode() == tui.ModeOngoing {
			// Only width changes need a native replay: horizontal resize changes the
			// committed scrollback wrapping, while height-only resize affects only the
			// live viewport. After the width has been quiet for the debounce window,
			// clear and replay ongoing history so emitted lines and dividers match.
			m.nativeResizeReplayToken++
			m.nativeResizeReplayAt = nativeResizeReplayNow().Add(nativeResizeReplayDebounce)
			token := m.nativeResizeReplayToken
			return handledUIFeatureUpdate(m, tea.Tick(nativeResizeReplayDebounce, func(time.Time) tea.Msg {
				return nativeResizeReplayMsg{token: token}
			}))
		}
		return handledUIFeatureUpdate(m, nil)
	case nativeResizeReplayMsg:
		if msg.token != m.nativeResizeReplayToken || m.view.Mode() != tui.ModeOngoing {
			return handledUIFeatureUpdate(m, nil)
		}
		if !m.nativeResizeReplayAt.IsZero() {
			remaining := time.Until(m.nativeResizeReplayAt)
			if now := nativeResizeReplayNow(); !now.IsZero() {
				remaining = m.nativeResizeReplayAt.Sub(now)
			}
			if remaining > 0 {
				token := m.nativeResizeReplayToken
				return handledUIFeatureUpdate(m, tea.Tick(remaining, func(time.Time) tea.Msg {
					return nativeResizeReplayMsg{token: token}
				}))
			}
		}
		m.nativeResizeReplayAt = time.Time{}
		if replay := m.emitCurrentNativeScrollbackState(true); replay != nil {
			return handledUIFeatureUpdate(m, replay)
		}
		if !m.nativeRenderedProjection.Empty() {
			return handledUIFeatureUpdate(m, nil)
		}
		return handledUIFeatureUpdate(m, tea.ClearScreen)
	}
	return uiFeatureUpdateResult{}
}
