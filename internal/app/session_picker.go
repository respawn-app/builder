package app

import (
	"fmt"
	"sort"
	"time"

	"builder/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

const sessionPickerWindowSize = 12

type sessionPickerResult struct {
	CreateNew bool
	Session   *session.Summary
	Canceled  bool
}

type sessionPickerModel struct {
	sessions []session.Summary
	cursor   int
	offset   int
	result   sessionPickerResult
}

func newSessionPickerModel(summaries []session.Summary) *sessionPickerModel {
	items := append([]session.Summary(nil), summaries...)
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return &sessionPickerModel{sessions: items}
}

func (m *sessionPickerModel) Init() tea.Cmd {
	return nil
}

func (m *sessionPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch key := msg.(type) {
	case tea.KeyMsg:
		switch key.Type {
		case tea.KeyUp:
			m.moveCursor(-1)
		case tea.KeyDown:
			m.moveCursor(1)
		case tea.KeyRunes:
			if len(key.Runes) == 1 {
				switch key.Runes[0] {
				case 'k':
					m.moveCursor(-1)
				case 'j':
					m.moveCursor(1)
				case 'n':
					m.result = sessionPickerResult{CreateNew: true}
					return m, tea.Quit
				case 'q':
					m.result = sessionPickerResult{Canceled: true}
					return m, tea.Quit
				}
			}
		case tea.KeyEnter:
			if len(m.sessions) == 0 {
				m.result = sessionPickerResult{CreateNew: true}
				return m, tea.Quit
			}
			picked := m.sessions[m.cursor]
			m.result = sessionPickerResult{Session: &picked}
			return m, tea.Quit
		case tea.KeyEsc, tea.KeyCtrlC:
			m.result = sessionPickerResult{Canceled: true}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *sessionPickerModel) View() string {
	out := "Select session (Enter=resume, n=new, q=quit, j/k or arrows=scroll)\n\n"
	if len(m.sessions) == 0 {
		out += "No sessions found. Press n to create a new one.\n"
		return out
	}

	start := m.offset
	end := start + sessionPickerWindowSize
	if end > len(m.sessions) {
		end = len(m.sessions)
	}
	for i := start; i < end; i++ {
		item := m.sessions[i]
		prefix := "  "
		if i == m.cursor {
			prefix = "> "
		}
		out += fmt.Sprintf("%s%s  [%s]\n", prefix, item.SessionID, humanTime(item.UpdatedAt))
	}
	if end < len(m.sessions) {
		out += fmt.Sprintf("\n... %d more sessions ...\n", len(m.sessions)-end)
	}
	return out
}

func (m *sessionPickerModel) moveCursor(delta int) {
	if len(m.sessions) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.sessions) {
		m.cursor = len(m.sessions) - 1
	}

	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+sessionPickerWindowSize {
		m.offset = m.cursor - sessionPickerWindowSize + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func humanTime(ts time.Time) string {
	if ts.IsZero() {
		return "unknown"
	}
	return ts.Local().Format("2006-01-02 15:04:05")
}

func runSessionPicker(summaries []session.Summary) (sessionPickerResult, error) {
	model := newSessionPickerModel(summaries)
	program := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := program.Run()
	if err != nil {
		return sessionPickerResult{}, err
	}
	picked, ok := finalModel.(*sessionPickerModel)
	if !ok {
		return sessionPickerResult{}, fmt.Errorf("unexpected picker model type %T", finalModel)
	}
	return picked.result, nil
}
