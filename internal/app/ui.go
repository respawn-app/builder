package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/tools"
	"builder/internal/tools/askquestion"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

type submitDoneMsg struct {
	message string
	err     error
}

type runtimeEventMsg struct {
	event runtime.Event
}

type askEvent struct {
	req   askquestion.Request
	reply chan askReply
}

type askReply struct {
	answer string
	err    error
}

type askEventMsg struct {
	event askEvent
}

type askBridge struct {
	ch chan askEvent
}

func newAskBridge() *askBridge {
	return &askBridge{ch: make(chan askEvent, 64)}
}

func (b *askBridge) Events() <-chan askEvent {
	return b.ch
}

func (b *askBridge) Handle(req askquestion.Request) (string, error) {
	e := askEvent{req: req, reply: make(chan askReply, 1)}
	b.ch <- e
	resp := <-e.reply
	return resp.answer, resp.err
}

type uiModel struct {
	engine *runtime.Engine
	view   tui.Model

	runtimeEvents <-chan runtime.Event
	askEvents     <-chan askEvent

	input  string
	busy   bool
	status string

	queued []string

	sawAssistantDelta bool

	activeAsk   *askEvent
	askQueue    []askEvent
	askCursor   int
	askFreeform bool
	askInput    string
}

func NewUIModel(engine *runtime.Engine, runtimeEvents <-chan runtime.Event, askEvents <-chan askEvent) tea.Model {
	return &uiModel{
		engine:        engine,
		view:          tui.NewModel(),
		status:        "idle",
		runtimeEvents: runtimeEvents,
		askEvents:     askEvents,
	}
}

func (m *uiModel) Init() tea.Cmd {
	return tea.Batch(waitRuntimeEvent(m.runtimeEvents), waitAskEvent(m.askEvents))
}

func (m *uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case runtimeEventMsg:
		m.handleRuntimeEvent(msg.event)
		return m, waitRuntimeEvent(m.runtimeEvents)
	case askEventMsg:
		if m.activeAsk == nil {
			m.setActiveAsk(msg.event)
			m.status = "question"
		} else {
			m.askQueue = append(m.askQueue, msg.event)
		}
		return m, waitAskEvent(m.askEvents)
	case tea.KeyMsg:
		if m.activeAsk != nil {
			return m.handleAskKey(msg)
		}
		return m.handleMainKey(msg)
	case submitDoneMsg:
		m.busy = false
		if msg.err != nil {
			m.status = "error"
			m.forwardToView(tui.SetOngoingErrorMsg{Err: msg.err})
			if len(m.queued) > 0 {
				next := m.popQueued()
				return m, m.startSubmission(next)
			}
			return m, nil
		}
		m.status = "idle"
		m.forwardToView(tui.ClearOngoingErrorMsg{})
		if !m.sawAssistantDelta && msg.message != "" {
			m.forwardToView(tui.StreamAssistantMsg{Delta: msg.message})
		}
		m.forwardToView(tui.CommitAssistantMsg{})
		m.sawAssistantDelta = false
		if len(m.queued) > 0 {
			next := m.popQueued()
			return m, m.startSubmission(next)
		}
		return m, nil
	}

	m.forwardToView(msg)
	return m, nil
}

func (m *uiModel) View() string {
	header := fmt.Sprintf("builder [%s] q=%d - Tab:toggle Ctrl+C:interrupt/quit Ctrl+Enter:queue\n", m.status, len(m.queued))
	mainInput := "\n\ninput> " + m.input
	if m.activeAsk != nil {
		return header + m.view.View() + "\n\n" + m.renderAskPrompt()
	}
	return header + m.view.View() + mainInput
}

func (m *uiModel) handleMainKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keyString := strings.ToLower(msg.String())
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
		text := strings.TrimSpace(m.input)
		if text == "" {
			if !m.busy && len(m.queued) > 0 {
				next := m.popQueued()
				return m, m.startSubmission(next)
			}
			return m, nil
		}
		if m.busy {
			m.engine.QueueUserMessage(text)
			m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: text})
			m.input = ""
			m.status = "injected"
			return m, nil
		}
		m.input = ""
		return m, m.startSubmission(text)
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
		if keyString == "ctrl+enter" || keyString == "ctrl+j" {
			text := strings.TrimSpace(m.input)
			if text == "" {
				return m, nil
			}
			m.queued = append(m.queued, text)
			m.input = ""
			if !m.busy {
				next := m.popQueued()
				return m, m.startSubmission(next)
			}
			m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: text})
			m.status = "queued"
			return m, nil
		}
		if msg.Type == tea.KeyRunes {
			m.input += string(msg.Runes)
		}
		return m, nil
	}
}

func (m *uiModel) handleAskKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.activeAsk == nil {
		return m, nil
	}
	req := m.activeAsk.req

	switch msg.Type {
	case tea.KeyCtrlC:
		hasNext := m.answerAsk("", errors.New("interrupted"))
		if m.busy {
			_ = m.engine.Interrupt()
			m.busy = false
		}
		if hasNext {
			m.status = "question"
		} else {
			m.status = "interrupted"
		}
		return m, nil
	case tea.KeyEsc:
		hasNext := m.answerAsk("", errors.New("question canceled"))
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
			hasNext := m.answerAsk(answer, nil)
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
		hasNext := m.answerAsk(req.Suggestions[m.askCursor], nil)
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
	default:
		if m.askFreeform && msg.Type == tea.KeyRunes {
			m.askInput += string(msg.Runes)
			return m, nil
		}
		return m, nil
	}
}

func (m *uiModel) renderAskPrompt() string {
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

func (m *uiModel) startSubmission(text string) tea.Cmd {
	m.busy = true
	m.status = "running"
	m.sawAssistantDelta = false
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: text})
	return m.submitCmd(text)
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
		return submitDoneMsg{message: msg.Content}
	}
}

func (m *uiModel) forwardToView(msg tea.Msg) {
	next, _ := m.view.Update(msg)
	casted, ok := next.(tui.Model)
	if ok {
		m.view = casted
	}
}

func (m *uiModel) handleRuntimeEvent(evt runtime.Event) {
	switch evt.Kind {
	case runtime.EventAssistantDelta:
		if evt.AssistantDelta != "" {
			m.sawAssistantDelta = true
			m.forwardToView(tui.StreamAssistantMsg{Delta: evt.AssistantDelta})
		}
	case runtime.EventAssistantDeltaReset:
		m.sawAssistantDelta = false
		m.forwardToView(tui.ClearOngoingAssistantMsg{})
	case runtime.EventToolCallStarted:
		if evt.ToolCall != nil {
			m.forwardToView(tui.AppendTranscriptMsg{Role: "tool_call", Text: formatToolCall(*evt.ToolCall)})
		}
	case runtime.EventToolCallCompleted:
		if evt.ToolResult != nil {
			m.forwardToView(tui.AppendTranscriptMsg{Role: "tool_result", Text: formatToolResult(*evt.ToolResult)})
		}
	}
}

func formatToolCall(call llm.ToolCall) string {
	return fmt.Sprintf("id=%s name=%s\ninput:\n%s", call.ID, call.Name, string(call.Input))
}

func formatToolResult(result tools.Result) string {
	return fmt.Sprintf("id=%s name=%s error=%t\noutput:\n%s", result.CallID, result.Name, result.IsError, string(result.Output))
}

func (m *uiModel) popQueued() string {
	if len(m.queued) == 0 {
		return ""
	}
	next := m.queued[0]
	m.queued = m.queued[1:]
	return next
}

func (m *uiModel) answerAsk(answer string, err error) bool {
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
	m.setActiveAsk(next)
	return true
}

func (m *uiModel) setActiveAsk(evt askEvent) {
	current := evt
	m.activeAsk = &current
	m.askCursor = 0
	m.askInput = ""
	m.askFreeform = len(current.req.Suggestions) == 0
}

func waitRuntimeEvent(ch <-chan runtime.Event) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-ch
		if !ok {
			return nil
		}
		return runtimeEventMsg{event: evt}
	}
}

func waitAskEvent(ch <-chan askEvent) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-ch
		if !ok {
			return nil
		}
		return askEventMsg{event: evt}
	}
}
