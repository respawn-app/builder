package app

import (
	"builder/internal/tools/askquestion"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type uiAskController struct {
	model *uiModel
}

type askPromptLineKind int

const (
	askPromptLineKindQuestion askPromptLineKind = iota
	askPromptLineKindOption
	askPromptLineKindHint
	askPromptLineKindInput
)

type askPromptLine struct {
	Text     string
	Kind     askPromptLineKind
	Selected bool
}

type askFreeformMode int

const (
	askFreeformModeGeneric askFreeformMode = iota
	askFreeformModeApprovalAllowCommentary
	askFreeformModeApprovalDenyCommentary
)

const approvalCommentaryOptionText = "Deny, and add commentary"

func (c uiAskController) acceptEvent(evt askEvent) {
	m := c.model
	if m.activeAsk == nil {
		c.setActiveAsk(evt)
		m.activity = uiActivityQuestion
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
			m.activity = uiActivityQuestion
		} else {
			m.activity = uiActivityInterrupted
		}
		return m, nil
	case tea.KeyEsc:
		hasNext := c.answer("", errors.New("question canceled"))
		if hasNext {
			m.activity = uiActivityQuestion
		} else {
			m.activity = uiActivityIdle
		}
		return m, nil
	case tea.KeyTab:
		m.askFreeform = true
		if req.Approval {
			m.askFreeformMode = askFreeformModeApprovalAllowCommentary
			m.askInput = ""
		}
		return m, nil
	case tea.KeyEnter:
		if m.askFreeform {
			answer := strings.TrimSpace(m.askInput)
			if req.Approval && m.askFreeformMode == askFreeformModeApprovalAllowCommentary {
				if answer != "" && m.engine != nil {
					m.engine.QueueUserMessage(answer)
					m.pendingInjected = append(m.pendingInjected, answer)
				}
				answer = approvalAllowWithCommentaryAnswerPrefix + answer
			}
			hasNext := c.answer(answer, nil)
			if hasNext {
				m.activity = uiActivityQuestion
			} else {
				m.activity = uiActivityRunning
			}
			return m, nil
		}
		optionCount := askOptionCount(req)
		if optionCount == 0 {
			m.askFreeform = true
			return m, nil
		}
		if askHasApprovalCommentaryOption(req) && m.askCursor == len(req.Suggestions) {
			m.askFreeform = true
			m.askFreeformMode = askFreeformModeApprovalDenyCommentary
			m.askInput = ""
			return m, nil
		}
		if m.askCursor < 0 || m.askCursor >= len(req.Suggestions) {
			m.askFreeform = true
			m.askInput = ""
			return m, nil
		}
		hasNext := c.answer(req.Suggestions[m.askCursor], nil)
		if hasNext {
			m.activity = uiActivityQuestion
		} else {
			m.activity = uiActivityRunning
		}
		return m, nil
	case tea.KeyUp:
		if !m.askFreeform && m.askCursor > 0 {
			m.askCursor--
		}
		return m, nil
	case tea.KeyDown:
		if !m.askFreeform {
			maxIdx := askOptionCount(req) - 1
			if maxIdx >= 0 && m.askCursor < maxIdx {
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
			filtered, _ := stripMouseSGRRunes(msg.Runes)
			if len(filtered) == 0 {
				return m, nil
			}
			m.askInput += string(filtered)
			return m, nil
		}
		return m, nil
	}
}

func (c uiAskController) renderPrompt() string {
	lines := c.renderPromptLines()
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, line.Text)
	}
	return strings.Join(out, "\n")
}

func (c uiAskController) renderPromptLines() []askPromptLine {
	m := c.model
	if m.activeAsk == nil {
		return nil
	}
	req := m.activeAsk.req
	if isApprovalDenyCommentaryPrompt(req, m.askFreeform, m.askFreeformMode) {
		return []askPromptLine{
			{Text: "Your comment:", Kind: askPromptLineKindHint},
			{Text: m.askInput, Kind: askPromptLineKindInput},
		}
	}
	lines := []askPromptLine{{Text: strings.TrimSpace(req.Question), Kind: askPromptLineKindQuestion}}
	if askOptionCount(req) > 0 && !m.askFreeform {
		for i, s := range req.Suggestions {
			selected := i == m.askCursor
			prefix := "  "
			if selected {
				prefix = "✓ "
			}
			lines = append(lines, askPromptLine{Text: fmt.Sprintf("%s%d. %s", prefix, i+1, s), Kind: askPromptLineKindOption, Selected: selected})
		}
		if askHasApprovalCommentaryOption(req) {
			selected := m.askCursor == len(req.Suggestions)
			prefix := "  "
			if selected {
				prefix = "✓ "
			}
			lines = append(lines, askPromptLine{Text: fmt.Sprintf("%s%d. %s", prefix, len(req.Suggestions)+1, approvalCommentaryOptionText), Kind: askPromptLineKindOption, Selected: selected})
		}
		hint := "Tab to switch to freeform • Enter to submit"
		if req.Approval {
			hint = "Tab to allow and add commentary • Enter to submit"
		}
		lines = append(lines, askPromptLine{Text: hint, Kind: askPromptLineKindHint})
		return lines
	}

	inputLine := m.askInput
	if req.Approval {
		if m.askFreeformMode == askFreeformModeApprovalAllowCommentary {
			inputLine = "Allow commentary: " + m.askInput
		} else {
			inputLine = "Deny commentary: " + m.askInput
		}
	}
	lines = append(lines, askPromptLine{Text: inputLine, Kind: askPromptLineKindInput})
	hint := "Tab to switch to freeform • Enter to submit"
	if req.Approval {
		hint = "Tab to allow and add commentary • Enter to submit"
	}
	lines = append(lines, askPromptLine{Text: hint, Kind: askPromptLineKindHint})
	return lines
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
		m.askFreeformMode = askFreeformModeGeneric
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
	m.askFreeform = askOptionCount(current.req) == 0
	m.askFreeformMode = askFreeformModeGeneric
}

func askHasApprovalCommentaryOption(req askquestion.Request) bool {
	return req.Approval && len(req.Suggestions) > 0
}

func askOptionCount(req askquestion.Request) int {
	count := len(req.Suggestions)
	if askHasApprovalCommentaryOption(req) {
		count++
	}
	return count
}

func isApprovalDenyCommentaryPrompt(req askquestion.Request, freeform bool, mode askFreeformMode) bool {
	if !freeform || mode != askFreeformModeApprovalDenyCommentary {
		return false
	}
	return req.Approval
}

func (m *uiModel) renderAskPrompt() string {
	return m.askController().renderPrompt()
}

func (m *uiModel) renderAskPromptLines() []askPromptLine {
	return m.askController().renderPromptLines()
}

func (m *uiModel) answerAsk(answer string, err error) bool {
	return m.askController().answer(answer, err)
}

func (m *uiModel) setActiveAsk(evt askEvent) {
	m.askController().setActiveAsk(evt)
}
