package app

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type uiAskController struct {
	model *uiModel
}

func (c uiAskController) acceptEvent(evt askEvent) {
	m := c.model
	if m.activeAsk == nil {
		c.setActiveAsk(evt)
		m.status = "question"
		return
	}
	m.askQueue = append(m.askQueue, evt)
}

func (c uiAskController) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := c.model
	if m.activeAsk == nil {
		return m, nil
	}
	req := m.activeAsk.req

	switch msg.Type {
	case tea.KeyCtrlC:
		hasNext := c.answer("", errors.New("interrupted"))
		if m.busy {
			if m.engine != nil {
				_ = m.engine.Interrupt()
			}
			m.busy = false
		}
		if hasNext {
			m.status = "question"
		} else {
			m.status = "interrupted"
		}
		return m, nil
	case tea.KeyEsc:
		hasNext := c.answer("", errors.New("question canceled"))
		if hasNext {
			m.status = "question"
		} else {
			m.status = "idle"
		}
		return m, nil
	case tea.KeyTab:
		m.askFreeform = true
		return m, nil
	case tea.KeyEnter:
		if m.askFreeform {
			answer := strings.TrimSpace(m.askInput)
			hasNext := c.answer(answer, nil)
			if hasNext {
				m.status = "question"
			} else {
				m.status = "running"
			}
			return m, nil
		}
		if len(req.Suggestions) == 0 {
			m.askFreeform = true
			return m, nil
		}
		if m.askCursor >= len(req.Suggestions) {
			m.askFreeform = true
			m.askInput = ""
			return m, nil
		}
		hasNext := c.answer(req.Suggestions[m.askCursor], nil)
		if hasNext {
			m.status = "question"
		} else {
			m.status = "running"
		}
		return m, nil
	case tea.KeyUp:
		if !m.askFreeform && m.askCursor > 0 {
			m.askCursor--
		}
		return m, nil
	case tea.KeyDown:
		if !m.askFreeform {
			max := len(req.Suggestions)
			if m.askCursor < max {
				m.askCursor++
			}
		}
		return m, nil
	case tea.KeyBackspace:
		if m.askFreeform && len(m.askInput) > 0 {
			m.askInput = m.askInput[:len(m.askInput)-1]
		}
		return m, nil
	case tea.KeySpace:
		if m.askFreeform {
			m.askInput += " "
		}
		return m, nil
	default:
		if m.askFreeform && msg.Type == tea.KeyRunes {
			m.askInput += string(msg.Runes)
			return m, nil
		}
		return m, nil
	}
}

func (c uiAskController) renderPrompt() string {
	m := c.model
	if m.activeAsk == nil {
		return ""
	}
	req := m.activeAsk.req
	lines := []string{fmt.Sprintf("question> %s", req.Question)}
	if len(req.Suggestions) > 0 && !m.askFreeform {
		for i, s := range req.Suggestions {
			prefix := "  "
			if i == m.askCursor {
				prefix = "> "
			}
			lines = append(lines, fmt.Sprintf("%s%d. %s", prefix, i+1, s))
		}
		prefix := "  "
		if m.askCursor == len(req.Suggestions) {
			prefix = "> "
		}
		lines = append(lines, fmt.Sprintf("%s%d. none of the above", prefix, len(req.Suggestions)+1))
		lines = append(lines, "Tab to switch to freeform")
		lines = append(lines, "Enter to submit")
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "freeform> "+m.askInput)
	lines = append(lines, "Enter to submit")
	return strings.Join(lines, "\n")
}

func (c uiAskController) answer(answer string, err error) bool {
	m := c.model
	if m.activeAsk == nil {
		return false
	}
	m.activeAsk.reply <- askReply{answer: answer, err: err}
	if len(m.askQueue) == 0 {
		m.activeAsk = nil
		m.askCursor = 0
		m.askInput = ""
		m.askFreeform = false
		return false
	}
	next := m.askQueue[0]
	m.askQueue = m.askQueue[1:]
	c.setActiveAsk(next)
	return true
}

func (c uiAskController) setActiveAsk(evt askEvent) {
	m := c.model
	current := evt
	m.activeAsk = &current
	m.askCursor = 0
	m.askInput = ""
	m.askFreeform = len(current.req.Suggestions) == 0
}

func (m *uiModel) renderAskPrompt() string {
	return m.askController().renderPrompt()
}

func (m *uiModel) answerAsk(answer string, err error) bool {
	return m.askController().answer(answer, err)
}

func (m *uiModel) setActiveAsk(evt askEvent) {
	m.askController().setActiveAsk(evt)
}
