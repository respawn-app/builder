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
	Text        string
	Kind        askPromptLineKind
	Selected    bool
	Recommended bool
	MutedSuffix string
	Disabled    bool
	InputPrefix string
	InputText   string
	InputCursor int
	ShowsCursor bool
}

type askFreeformMode int

const (
	askFreeformModeGeneric askFreeformMode = iota
	askFreeformModeApprovalCommentary
)

const askFreeformSelectionOptionText = "Freeform answer"

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
	if msg.Type != tea.KeyEnter && msg.Type != keyTypeShiftEnterCSI {
		m.inputController().clearPendingCSIShiftEnter()
	}
	req := m.activeAsk.req

	switch msg.Type {
	case tea.KeyCtrlC:
		hasNext := c.answer(askquestion.Response{}, errors.New("interrupted"))
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
		hasNext := c.answer(askquestion.Response{}, errors.New("question canceled"))
		if hasNext {
			m.activity = uiActivityQuestion
		} else {
			m.activity = uiActivityIdle
		}
		return m, nil
	case tea.KeyTab:
		if m.askFreeform {
			if !askSupportsDraftRoundTrip(req) {
				return m, nil
			}
			m.askFreeform = false
			return m, nil
		}
		m.askFreeform = true
		if approvalSupportsCommentary(req) {
			m.askFreeformMode = askFreeformModeApprovalCommentary
			m.clearAskInput()
		}
		return m, nil
	case tea.KeyEnter:
		m.inputController().normalizePendingCSIShiftEnterOnEnter()
		if m.askFreeform {
			commentary := strings.TrimSpace(m.askInput)
			if askRequiresFreeformSelectionCommentary(req, m.askCursor) && commentary == "" {
				return m, c.showFreeformSelectionCommentaryRequiredError()
			}
			resp := askquestion.Response{Answer: commentary, FreeformAnswer: commentary}
			if optionNumber, ok := selectedAskOptionNumber(req, m.askCursor); ok {
				resp.SelectedOptionNumber = optionNumber
			}
			if req.Approval {
				if m.askFreeformMode == askFreeformModeApprovalCommentary {
					decision, ok := selectedApprovalDecision(req, m.askCursor)
					if !ok {
						return m, nil
					}
					if commentary != "" && m.engine != nil {
						m.engine.QueueUserMessage(commentary)
						m.pendingInjected = append(m.pendingInjected, commentary)
					}
					resp = askquestion.Response{Approval: &askquestion.ApprovalPayload{Decision: decision, Commentary: commentary}}
				}
			}
			hasNext := c.answer(resp, nil)
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
		if askHasFreeformSelectionOption(req) && m.askCursor == len(askVisibleOptions(req)) {
			commentary := strings.TrimSpace(m.askInput)
			if commentary == "" {
				m.askFreeform = true
				return m, nil
			}
			hasNext := c.answer(askquestion.Response{Answer: commentary, FreeformAnswer: commentary}, nil)
			if hasNext {
				m.activity = uiActivityQuestion
			} else {
				m.activity = uiActivityRunning
			}
			return m, nil
		}
		visibleOptions := askVisibleOptions(req)
		if m.askCursor < 0 || m.askCursor >= len(visibleOptions) {
			m.askFreeform = true
			m.clearAskInput()
			return m, nil
		}
		resp := askquestion.Response{SelectedOptionNumber: m.askCursor + 1}
		if commentary := strings.TrimSpace(m.askInput); askSupportsDraftRoundTrip(req) && commentary != "" {
			resp.FreeformAnswer = commentary
		}
		if req.Approval && m.askCursor < len(req.ApprovalOptions) {
			resp = askquestion.Response{Approval: &askquestion.ApprovalPayload{Decision: req.ApprovalOptions[m.askCursor].Decision}}
		}
		hasNext := c.answer(resp, nil)
		if hasNext {
			m.activity = uiActivityQuestion
		} else {
			m.activity = uiActivityRunning
		}
		return m, nil
	case tea.KeyUp:
		if m.askFreeform {
			m.moveAskCursorUpLine()
			return m, nil
		}
		if m.askCursor > 0 {
			m.askCursor--
		}
		return m, nil
	case tea.KeyDown:
		if m.askFreeform {
			m.moveAskCursorDownLine()
			return m, nil
		}
		maxIdx := askOptionCount(req) - 1
		if maxIdx >= 0 && m.askCursor < maxIdx {
			m.askCursor++
		}
		return m, nil
	case tea.KeyCtrlJ, keyTypeShiftEnterCSI:
		if !m.askFreeform {
			return m, nil
		}
		m.insertAskInputRunes([]rune{'\n'})
		if msg.Type == keyTypeShiftEnterCSI {
			m.inputController().markPendingCSIShiftEnter()
		}
		return m, nil
	case tea.KeyBackspace:
		if m.askFreeform {
			m.backspaceAskInput()
		}
		return m, nil
	case tea.KeySpace:
		if m.askFreeform {
			m.insertAskInputRunes([]rune{' '})
		}
		return m, nil
	case tea.KeyLeft:
		if !m.askFreeform {
			return m, nil
		}
		if msg.Alt {
			m.moveAskCursorWordLeft()
			return m, nil
		}
		m.moveAskCursorLeft()
		return m, nil
	case tea.KeyRight:
		if !m.askFreeform {
			return m, nil
		}
		if msg.Alt {
			m.moveAskCursorWordRight()
			return m, nil
		}
		m.moveAskCursorRight()
		return m, nil
	case tea.KeyHome, tea.KeyCtrlA:
		if m.askFreeform {
			m.moveAskCursorStart()
		}
		return m, nil
	case tea.KeyEnd, tea.KeyCtrlE, tea.KeyCtrlEnd:
		if m.askFreeform {
			m.moveAskCursorEnd()
		}
		return m, nil
	case tea.KeyCtrlLeft:
		if m.askFreeform {
			m.moveAskCursorWordLeft()
		}
		return m, nil
	case tea.KeyCtrlRight:
		if m.askFreeform {
			m.moveAskCursorWordRight()
		}
		return m, nil
	default:
		if isDeleteCurrentLineKey(msg) {
			if m.askFreeform {
				m.deleteCurrentAskInputLine()
			}
			return m, nil
		}
		if isShiftEnterKey(msg) {
			if !m.askFreeform {
				return m, nil
			}
			m.insertAskInputRunes([]rune{'\n'})
			return m, nil
		}
		if m.askFreeform && msg.Type == tea.KeyRunes {
			m.insertAskInputRunes(msg.Runes)
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
	if isApprovalCommentaryPrompt(req, m.askFreeform, m.askFreeformMode) {
		return []askPromptLine{
			{Text: approvalCommentaryLabel(req, m.askCursor), Kind: askPromptLineKindHint},
			{Kind: askPromptLineKindInput, InputPrefix: m.askInputPrefix(), InputText: m.askInput, InputCursor: m.askInputCursor, ShowsCursor: true},
		}
	}
	lines := []askPromptLine{{Text: strings.TrimSpace(req.Question), Kind: askPromptLineKindQuestion}}
	if askOptionCount(req) > 0 && !m.askFreeform {
		visibleOptions := askVisibleOptions(req)
		for i, s := range visibleOptions {
			selected := i == m.askCursor
			recommended := askOptionIsRecommended(req, i)
			marker := "  "
			if selected {
				marker = "✔︎ "
			} else if recommended {
				marker = "★ "
			}
			text := fmt.Sprintf("%s%d. %s", marker, i+1, s)
			mutedSuffix := ""
			if recommended {
				mutedSuffix = " • recommended"
				text += mutedSuffix
			}
			lines = append(lines, askPromptLine{Text: text, Kind: askPromptLineKindOption, Selected: selected, Recommended: recommended, MutedSuffix: mutedSuffix})
		}
		if askHasFreeformSelectionOption(req) {
			idx := len(visibleOptions) + 1
			selected := m.askCursor == len(visibleOptions)
			prefix := "  "
			if selected {
				prefix = "✔︎ "
			}
			lines = append(lines, askPromptLine{Text: fmt.Sprintf("%s%d. %s", prefix, idx, askFreeformSelectionOptionText), Kind: askPromptLineKindOption, Selected: selected})
		}
		if askSupportsDraftRoundTrip(req) && askHasPendingFreeformDraft(m.askInput) {
			lines = append(lines, askPromptLine{Kind: askPromptLineKindInput, Disabled: true, InputPrefix: m.askInputPrefix(), InputText: m.askInput, InputCursor: m.askInputCursor, ShowsCursor: false})
			return lines
		}
		hint := "Tab to add commentary • Enter to submit"
		if approvalSupportsCommentary(req) {
			hint = "Tab to add commentary • Enter to submit"
		}
		lines = append(lines, askPromptLine{Text: hint, Kind: askPromptLineKindHint})
		return lines
	}

	inputLabel := ""
	if isApprovalCommentaryPrompt(req, m.askFreeform, m.askFreeformMode) {
		inputLabel = approvalCommentaryLabel(req, m.askCursor)
	}
	if inputLabel != "" {
		lines = append(lines, askPromptLine{Text: inputLabel, Kind: askPromptLineKindHint})
	}
	lines = append(lines, askPromptLine{Kind: askPromptLineKindInput, InputPrefix: m.askInputPrefix(), InputText: m.askInput, InputCursor: m.askInputCursor, ShowsCursor: true})
	hint := "Enter to submit"
	if askSupportsDraftRoundTrip(req) {
		hint = "Tab to return to picker • Enter to submit"
	}
	lines = append(lines, askPromptLine{Text: hint, Kind: askPromptLineKindHint})
	return lines
}

func (c uiAskController) answer(resp askquestion.Response, err error) bool {
	m := c.model
	if m.activeAsk == nil {
		return false
	}
	if resp.RequestID == "" {
		resp.RequestID = m.activeAsk.req.ID
	}
	m.activeAsk.reply <- askReply{response: resp, err: err}
	if len(m.askQueue) == 0 {
		m.activeAsk = nil
		m.askCursor = 0
		m.clearAskInput()
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
	m.clearAskInput()
	m.askFreeform = askOptionCount(current.req) == 0
	m.askFreeformMode = askFreeformModeGeneric
}

func (m *uiModel) askInputPrefix() string {
	return "› "
}

func askVisibleOptions(req askquestion.Request) []string {
	if req.Approval && len(req.ApprovalOptions) > 0 {
		out := make([]string, 0, len(req.ApprovalOptions))
		for _, option := range req.ApprovalOptions {
			out = append(out, option.Label)
		}
		return out
	}
	return req.Suggestions
}

func approvalOptionIndex(req askquestion.Request, decision askquestion.ApprovalDecision) int {
	for i, option := range req.ApprovalOptions {
		if option.Decision == decision {
			return i
		}
	}
	return -1
}

func approvalSupportsCommentary(req askquestion.Request) bool {
	if !req.Approval {
		return false
	}
	return len(askVisibleOptions(req)) > 0
}

func askHasFreeformSelectionOption(req askquestion.Request) bool {
	if req.Approval {
		return false
	}
	return len(askVisibleOptions(req)) > 0
}

func askOptionCount(req askquestion.Request) int {
	count := len(askVisibleOptions(req))
	if askHasFreeformSelectionOption(req) {
		count++
	}
	return count
}

func isApprovalCommentaryPrompt(req askquestion.Request, freeform bool, mode askFreeformMode) bool {
	if !freeform || mode != askFreeformModeApprovalCommentary {
		return false
	}
	return req.Approval
}

func selectedApprovalDecision(req askquestion.Request, cursor int) (askquestion.ApprovalDecision, bool) {
	if !req.Approval || cursor < 0 || cursor >= len(req.ApprovalOptions) {
		return "", false
	}
	return req.ApprovalOptions[cursor].Decision, true
}

func approvalCommentaryLabel(req askquestion.Request, cursor int) string {
	if !req.Approval || cursor < 0 || cursor >= len(req.ApprovalOptions) {
		return "Commentary:"
	}
	return fmt.Sprintf("Commentary for %s:", req.ApprovalOptions[cursor].Label)
}

func selectedAskOptionNumber(req askquestion.Request, cursor int) (int, bool) {
	if req.Approval {
		return 0, false
	}
	visibleOptions := askVisibleOptions(req)
	if cursor < 0 || cursor >= len(visibleOptions) {
		return 0, false
	}
	return cursor + 1, true
}

func askOptionIsRecommended(req askquestion.Request, index int) bool {
	if req.Approval {
		return false
	}
	return req.RecommendedOptionIndex == index+1
}

func askRequiresFreeformSelectionCommentary(req askquestion.Request, cursor int) bool {
	if !askHasFreeformSelectionOption(req) {
		return false
	}
	return cursor == len(askVisibleOptions(req))
}

func askHasPendingFreeformDraft(input string) bool {
	return strings.TrimSpace(input) != ""
}

func askSupportsDraftRoundTrip(req askquestion.Request) bool {
	return !req.Approval && len(askVisibleOptions(req)) > 0
}

func (c uiAskController) showFreeformSelectionCommentaryRequiredError() tea.Cmd {
	return sequenceCmds(
		c.model.setTransientStatusWithKind("Write your response before submitting the freeform option", uiStatusNoticeError),
		ringBellCmd(),
	)
}

func (m *uiModel) renderAskPrompt() string {
	return m.askController().renderPrompt()
}

func (m *uiModel) renderAskPromptLines() []askPromptLine {
	return m.askController().renderPromptLines()
}

func (m *uiModel) answerAsk(answer string, err error) bool {
	return m.askController().answer(askquestion.Response{Answer: answer}, err)
}

func (m *uiModel) setActiveAsk(evt askEvent) {
	m.askController().setActiveAsk(evt)
}
