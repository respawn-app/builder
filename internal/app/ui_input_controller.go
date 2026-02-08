package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"builder/internal/app/commands"
	"builder/internal/llm"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

type uiInputController struct {
	model *uiModel
}

var spinnerFrames = []string{"|", "/", "-", "\\"}
var spinnerTickInterval = 360 * time.Millisecond

func (c uiInputController) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := c.model
	keyString := strings.ToLower(msg.String())
	if keyString == "tab" || keyString == "ctrl+enter" || keyString == "ctrl+j" {
		text := strings.TrimSpace(m.input)
		if text == "" {
			return m, nil
		}
		if m.busy {
			if m.inputSubmitLocked {
				return m, nil
			}
			if m.engine != nil {
				m.engine.QueueUserMessage(text)
			}
			m.pendingInjected = append(m.pendingInjected, text)
			m.input = ""
			m.status = "queued"
			return m, nil
		}
		m.queued = append(m.queued, text)
		m.input = ""
		if !m.busy {
			next := m.popQueued()
			return m, c.startSubmission(next)
		}
		m.status = "queued"
		return m, nil
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		if m.busy {
			if m.engine != nil {
				_ = m.engine.Interrupt()
			}
			m.busy = false
			m.status = "interrupted"
			return m, nil
		}
		m.exitAction = UIActionExit
		return m, tea.Quit
	case tea.KeyShiftTab:
		m.forwardToView(tui.ToggleModeMsg{})
		return m, nil
	case tea.KeyEnter:
		text := strings.TrimSpace(m.input)
		if text == "" {
			if !m.busy && len(m.queued) > 0 {
				next := m.popQueued()
				return m, c.startSubmission(next)
			}
			return m, nil
		}
		if m.busy {
			if m.engine != nil {
				m.engine.QueueUserMessage(text)
			}
			m.pendingInjected = append(m.pendingInjected, text)
			m.lockedInjectText = text
			m.inputSubmitLocked = true
			m.status = "queued"
			return m, nil
		}
		if commandResult := m.commandRegistry.Execute(text); commandResult.Handled {
			m.input = ""
			if commandResult.Text != "" {
				if m.engine != nil {
					m.engine.AppendLocalEntry("system", commandResult.Text)
				} else {
					m.forwardToView(tui.AppendTranscriptMsg{Role: "system", Text: commandResult.Text})
				}
			}
			switch commandResult.Action {
			case commands.ActionExit:
				m.exitAction = UIActionExit
				return m, tea.Quit
			case commands.ActionNew:
				m.exitAction = UIActionNewSession
				return m, tea.Quit
			case commands.ActionLogout:
				m.exitAction = UIActionLogout
				return m, tea.Quit
			}
			return m, nil
		}
		m.input = ""
		return m, c.startSubmission(text)
	case tea.KeyBackspace:
		if m.inputSubmitLocked {
			return m, nil
		}
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	case tea.KeySpace:
		if m.inputSubmitLocked {
			return m, nil
		}
		m.input += " "
		return m, nil
	case tea.KeyUp:
		m.forwardToView(tea.KeyMsg{Type: tea.KeyUp})
		return m, nil
	case tea.KeyDown:
		m.forwardToView(tea.KeyMsg{Type: tea.KeyDown})
		return m, nil
	default:
		if msg.Type == tea.KeyRunes {
			if m.inputSubmitLocked {
				return m, nil
			}
			m.input += string(msg.Runes)
		}
		return m, nil
	}
}

func (c uiInputController) startSubmission(text string) tea.Cmd {
	m := c.model
	m.busy = true
	m.status = "running"
	m.sawAssistantDelta = false
	m.logf("step.start user_chars=%d", len(text))
	if m.engine == nil {
		m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: text})
	}
	m.syncViewport()
	return tea.Batch(c.submitCmd(text), tickSpinner())
}

func (c uiInputController) submitCmd(text string) tea.Cmd {
	m := c.model
	return func() tea.Msg {
		if m.engine == nil {
			return submitDoneMsg{err: errors.New("runtime engine is not configured")}
		}
		msg, err := m.engine.SubmitUserMessage(context.Background(), text)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return submitDoneMsg{err: errors.New("interrupted")}
			}
			return submitDoneMsg{err: err}
		}
		return submitDoneMsg{message: msg.Content}
	}
}

func (c uiInputController) handleSubmitDone(msg submitDoneMsg) (tea.Model, tea.Cmd) {
	m := c.model
	m.busy = false
	m.spinnerFrame = 0
	if msg.err != nil {
		c.unlockInputAfterSubmissionError()
		detailErr := formatSubmissionError(msg.err)
		m.status = "error"
		if m.engine != nil {
			m.engine.SetOngoingError(detailErr)
			m.engine.AppendLocalEntry("error", detailErr)
		} else {
			m.forwardToView(tui.SetOngoingErrorMsg{Err: errors.New(detailErr)})
			m.forwardToView(tui.AppendTranscriptMsg{Role: "error", Text: detailErr})
		}
		m.logf("step.error err=%q", detailErr)
		if len(m.queued) > 0 {
			next := m.popQueued()
			return m, c.startSubmission(next)
		}
		m.syncViewport()
		return m, nil
	}

	m.status = "idle"
	if m.engine != nil {
		m.engine.ClearOngoingError()
	} else {
		m.forwardToView(tui.ClearOngoingErrorMsg{})
		if !m.sawAssistantDelta && msg.message != "" {
			m.forwardToView(tui.StreamAssistantMsg{Delta: msg.message})
		}
		m.forwardToView(tui.CommitAssistantMsg{})
	}
	m.logf("step.done assistant_chars=%d", len(msg.message))
	m.sawAssistantDelta = false
	if len(m.queued) > 0 {
		next := m.popQueued()
		return m, c.startSubmission(next)
	}
	m.syncViewport()
	return m, nil
}

func (c uiInputController) unlockInputAfterSubmissionError() {
	m := c.model
	if !m.inputSubmitLocked {
		return
	}
	locked := strings.TrimSpace(m.lockedInjectText)
	if locked != "" {
		for i, pending := range m.pendingInjected {
			if strings.TrimSpace(pending) != locked {
				continue
			}
			m.pendingInjected = append(m.pendingInjected[:i], m.pendingInjected[i+1:]...)
			break
		}
	}
	m.inputSubmitLocked = false
	m.lockedInjectText = ""
}

func (c uiInputController) handleSpinnerTick() (tea.Model, tea.Cmd) {
	m := c.model
	if !m.busy {
		return m, nil
	}
	m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
	m.syncViewport()
	return m, tickSpinner()
}

func (m *uiModel) popQueued() string {
	if len(m.queued) == 0 {
		return ""
	}
	next := m.queued[0]
	m.queued = m.queued[1:]
	return next
}

func formatSubmissionError(err error) string {
	if err == nil {
		return ""
	}
	var statusErr *llm.APIStatusError
	if errors.As(err, &statusErr) {
		body := statusErr.Body
		if strings.TrimSpace(body) == "" {
			body = "<empty error body>"
		}
		return fmt.Sprintf("openai status %d\nresponse body:\n%s", statusErr.StatusCode, body)
	}
	return err.Error()
}

func tickSpinner() tea.Cmd {
	return tea.Tick(spinnerTickInterval, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}
