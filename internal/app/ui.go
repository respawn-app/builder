package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

type submitDoneMsg struct {
	message llm.Message
	err     error
}

type uiModel struct {
	engine *runtime.Engine
	view   tui.Model

	input  string
	busy   bool
	status string
}

func NewUIModel(engine *runtime.Engine) tea.Model {
	return &uiModel{
		engine: engine,
		view:   tui.NewModel(),
		status: "idle",
	}
}

func (m *uiModel) Init() tea.Cmd {
	return nil
}

func (m *uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			if m.busy {
				_ = m.engine.Interrupt()
				m.busy = false
				m.status = "interrupted"
				return m, nil
			}
			return m, tea.Quit
		case tea.KeyTab:
			m.forwardToView(tui.ToggleModeMsg{})
			return m, nil
		case tea.KeyEnter:
			if m.busy {
				return m, nil
			}
			text := strings.TrimSpace(m.input)
			if text == "" {
				return m, nil
			}
			m.input = ""
			m.busy = true
			m.status = "running"
			m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: text})
			return m, m.submitCmd(text)
		case tea.KeyBackspace:
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
			return m, nil
		case tea.KeyUp:
			m.forwardToView(tea.KeyMsg{Type: tea.KeyUp})
			return m, nil
		case tea.KeyDown:
			m.forwardToView(tea.KeyMsg{Type: tea.KeyDown})
			return m, nil
		default:
			if msg.Type == tea.KeyRunes {
				m.input += string(msg.Runes)
			}
			return m, nil
		}
	case submitDoneMsg:
		m.busy = false
		if msg.err != nil {
			m.status = "error"
			m.forwardToView(tui.SetOngoingErrorMsg{Err: msg.err})
			return m, nil
		}
		m.status = "idle"
		m.forwardToView(tui.ClearOngoingErrorMsg{})
		m.forwardToView(tui.StreamAssistantMsg{Delta: msg.message.Content})
		m.forwardToView(tui.CommitAssistantMsg{})
		return m, nil
	}

	m.forwardToView(msg)
	return m, nil
}

func (m *uiModel) View() string {
	header := fmt.Sprintf("builder [%s] - Tab:toggle Ctrl+C:interrupt/quit\n", m.status)
	input := "\n\ninput> " + m.input
	return header + m.view.View() + input
}

func (m *uiModel) submitCmd(text string) tea.Cmd {
	return func() tea.Msg {
		msg, err := m.engine.SubmitUserMessage(context.Background(), text)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return submitDoneMsg{err: errors.New("interrupted")}
			}
			return submitDoneMsg{err: err}
		}
		return submitDoneMsg{message: msg}
	}
}

func (m *uiModel) forwardToView(msg tea.Msg) {
	next, _ := m.view.Update(msg)
	casted, ok := next.(tui.Model)
	if ok {
		m.view = casted
	}
}
