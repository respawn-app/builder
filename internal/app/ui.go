package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"builder/internal/app/commands"
	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/tools/askquestion"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

type submitDoneMsg struct {
	message string
	err     error
}

type spinnerTickMsg struct{}

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

type uiLogger interface {
	Logf(format string, args ...any)
}

type UIOption func(*uiModel)

type UIAction string

type UITranscriptEntry struct {
	Role string
	Text string
}

const (
	UIActionNone       UIAction = "none"
	UIActionExit       UIAction = "exit"
	UIActionNewSession UIAction = "new_session"
	UIActionLogout     UIAction = "logout"
)

func WithUILogger(logger uiLogger) UIOption {
	return func(m *uiModel) {
		m.logger = logger
	}
}

func WithUIModelName(model string) UIOption {
	return func(m *uiModel) {
		m.modelName = strings.TrimSpace(model)
	}
}

func WithUITheme(theme string) UIOption {
	return func(m *uiModel) {
		m.theme = strings.TrimSpace(theme)
		m.view = tui.NewModel(tui.WithTheme(theme))
	}
}

func WithUIInitialTranscript(entries []UITranscriptEntry) UIOption {
	return func(m *uiModel) {
		m.initialTranscript = append([]UITranscriptEntry(nil), entries...)
	}
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

	modelName       string
	spinnerFrame    int
	commandRegistry *commands.Registry
	exitAction      UIAction
	theme           string

	sawAssistantDelta bool
	logger            uiLogger

	activeAsk   *askEvent
	askQueue    []askEvent
	askCursor   int
	askFreeform bool
	askInput    string

	termWidth  int
	termHeight int

	initialTranscript []UITranscriptEntry
}

func NewUIModel(engine *runtime.Engine, runtimeEvents <-chan runtime.Event, askEvents <-chan askEvent, opts ...UIOption) tea.Model {
	m := &uiModel{
		engine:          engine,
		view:            tui.NewModel(),
		status:          "idle",
		runtimeEvents:   runtimeEvents,
		askEvents:       askEvents,
		commandRegistry: commands.NewDefaultRegistry(),
		exitAction:      UIActionNone,
		theme:           "dark",
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.engine != nil {
		m.syncConversationFromEngine()
	} else {
		for _, entry := range m.initialTranscript {
			if strings.TrimSpace(entry.Text) == "" {
				continue
			}
			m.forwardToView(tui.AppendTranscriptMsg{Role: entry.Role, Text: entry.Text})
		}
	}
	m.syncViewport()
	return m
}

func (m *uiModel) Init() tea.Cmd {
	return tea.Batch(waitRuntimeEvent(m.runtimeEvents), waitAskEvent(m.askEvents))
}

func (m *uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		m.syncViewport()
		return m, nil
	case runtimeEventMsg:
		m.handleRuntimeEvent(msg.event)
		m.syncViewport()
		return m, waitRuntimeEvent(m.runtimeEvents)
	case askEventMsg:
		if m.activeAsk == nil {
			m.setActiveAsk(msg.event)
			m.status = "question"
		} else {
			m.askQueue = append(m.askQueue, msg.event)
		}
		m.syncViewport()
		return m, waitAskEvent(m.askEvents)
	case tea.KeyMsg:
		if m.activeAsk != nil {
			next, cmd := m.handleAskKey(msg)
			next.(*uiModel).syncViewport()
			return next, cmd
		}
		next, cmd := m.handleMainKey(msg)
		next.(*uiModel).syncViewport()
		return next, cmd
	case submitDoneMsg:
		m.busy = false
		m.spinnerFrame = 0
		if msg.err != nil {
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
				return m, m.startSubmission(next)
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
			return m, m.startSubmission(next)
		}
		m.syncViewport()
		return m, nil
	case spinnerTickMsg:
		if !m.busy {
			return m, nil
		}
		m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
		m.syncViewport()
		return m, tickSpinner()
	}

	m.forwardToView(msg)
	m.syncViewport()
	return m, nil
}

func (m *uiModel) View() string {
	style := uiThemeStyles(m.theme)
	width := m.effectiveWidth()
	height := m.effectiveHeight()
	if width <= 0 || height <= 0 {
		return ""
	}

	inputLines := m.renderInputLines(width, style)
	statusLine := m.renderStatusLine(width, style)
	statusLines := 1
	chatLines := height - len(inputLines) - statusLines
	if chatLines < 1 {
		chatLines = 1
	}
	chatPanel := m.renderChatPanel(width, chatLines, style)
	allLines := make([]string, 0, height)
	allLines = append(allLines, chatPanel...)
	allLines = append(allLines, inputLines...)
	allLines = append(allLines, statusLine)
	for len(allLines) < height {
		allLines = append(allLines, padRight("", width))
	}
	if len(allLines) > height {
		allLines = allLines[len(allLines)-height:]
	}
	rendered := strings.Join(allLines, "\n")
	if visible, row, col := m.inputCursorPosition(width, height, chatLines); visible {
		return rendered + ansiShowCursor + fmt.Sprintf("\x1b[%d;%dH", row, col)
	}
	return rendered + ansiHideCursor
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
		m.exitAction = UIActionExit
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
		if m.busy {
			return m, nil
		}
		m.input = ""
		return m, m.startSubmission(text)
	case tea.KeyBackspace:
		if m.busy {
			return m, nil
		}
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	case tea.KeySpace:
		if m.busy {
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
		if keyString == "ctrl+enter" || keyString == "ctrl+j" {
			if m.busy {
				return m, nil
			}
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
			m.status = "queued"
			return m, nil
		}
		if msg.Type == tea.KeyRunes {
			if m.busy {
				return m, nil
			}
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
	m.logf("step.start user_chars=%d", len(text))
	if m.engine == nil {
		m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: text})
	}
	m.syncViewport()
	return tea.Batch(m.submitCmd(text), tickSpinner())
}

func (m *uiModel) submitCmd(text string) tea.Cmd {
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

func (m *uiModel) forwardToView(msg tea.Msg) {
	next, _ := m.view.Update(msg)
	casted, ok := next.(tui.Model)
	if ok {
		m.view = casted
	}
}

func (m *uiModel) handleRuntimeEvent(evt runtime.Event) {
	switch evt.Kind {
	case runtime.EventConversationUpdated:
		m.syncConversationFromEngine()
	case runtime.EventAssistantDelta:
		m.sawAssistantDelta = evt.AssistantDelta != ""
	case runtime.EventAssistantDeltaReset:
		m.sawAssistantDelta = false
	}
}

func (m *uiModel) syncConversationFromEngine() {
	if m.engine == nil {
		return
	}
	snapshot := m.engine.ChatSnapshot()
	entries := make([]tui.TranscriptEntry, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		entries = append(entries, tui.TranscriptEntry{
			Role: entry.Role,
			Text: entry.Text,
		})
	}
	m.forwardToView(tui.SetConversationMsg{
		Entries:      entries,
		Ongoing:      snapshot.Ongoing,
		OngoingError: snapshot.OngoingError,
	})
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

func (m *uiModel) Action() UIAction {
	return m.exitAction
}

func (m *uiModel) logf(format string, args ...any) {
	if m.logger != nil {
		m.logger.Logf(format, args...)
	}
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

var spinnerFrames = []string{"|", "/", "-", "\\"}
var spinnerTickInterval = 360 * time.Millisecond

const (
	ansiShowCursor = "\x1b[?25h"
	ansiHideCursor = "\x1b[?25l"
)

func tickSpinner() tea.Cmd {
	return tea.Tick(spinnerTickInterval, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func (m *uiModel) renderStatusLine(width int, style uiStyles) string {
	spin := renderStatusDot(m.theme, m.busy, m.spinnerFrame)
	segments := []string{
		spin,
		style.meta.Render(string(m.view.Mode())),
		style.meta.Render(firstNonEmpty(m.modelName, "gpt-5")),
	}
	line := strings.Join(segments, " | ")
	return padRight(line, width)
}

func renderStatusDot(theme string, busy bool, frame int) string {
	if !busy {
		green := lipgloss.CompleteAdaptiveColor{
			Light: lipgloss.CompleteColor{ANSI: "2", ANSI256: "34", TrueColor: "#22863A"},
			Dark:  lipgloss.CompleteColor{ANSI: "2", ANSI256: "114", TrueColor: "#98C379"},
		}
		return lipgloss.NewStyle().Foreground(green).Render("●")
	}
	if frame%2 == 1 {
		return " "
	}
	muted := uiPalette(theme).muted
	return lipgloss.NewStyle().Foreground(muted).Render("●")
}

func (m *uiModel) renderChatPanel(width, height int, style uiStyles) []string {
	if width < 1 {
		return []string{padRight("", width)}
	}
	contentWidth := width
	rawLines := splitPlainLines(m.view.View())
	contentLines := append([]string(nil), rawLines...)
	if len(contentLines) < height {
		for len(contentLines) < height {
			contentLines = append(contentLines, "")
		}
	} else if len(contentLines) > height {
		contentLines = contentLines[:height]
	}
	out := make([]string, 0, height)
	for _, line := range contentLines {
		if line == tui.TranscriptDivider {
			out = append(out, style.meta.Render(strings.Repeat("─", contentWidth)))
			continue
		}
		out = append(out, style.chat.Render(padANSIRight(line, contentWidth)))
	}
	return out
}

func (m *uiModel) renderInputLines(width int, style uiStyles) []string {
	if width < 1 {
		return []string{padRight("", width)}
	}
	contentWidth := width
	var raw []string
	if m.activeAsk != nil {
		raw = splitPlainLines(m.renderAskPrompt())
	} else {
		text := m.input
		prefix := "› "
		if m.busy {
			text = "input locked while agent is running"
			prefix = "⨯ "
		}
		raw = splitPlainLines(prefix + text)
	}
	wrapped := make([]string, 0, len(raw))
	for _, line := range raw {
		wrapped = append(wrapped, wrapLine(line, contentWidth)...)
	}
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	maxContentLines := m.effectiveHeight() - 4
	if maxContentLines < 1 {
		maxContentLines = 1
	}
	if len(wrapped) > maxContentLines {
		wrapped = wrapped[len(wrapped)-maxContentLines:]
	}

	borderColor := uiPalette(m.theme).primary
	if m.busy {
		borderColor = uiPalette(m.theme).muted
	}
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	top := borderStyle.Render(strings.Repeat("─", width))
	bottom := borderStyle.Render(strings.Repeat("─", width))

	out := make([]string, 0, len(wrapped)+2)
	out = append(out, top)
	lineStyle := style.input
	if m.busy {
		lineStyle = style.inputDisabled
	}
	for _, line := range wrapped {
		out = append(out, lineStyle.Render(padRight(line, contentWidth)))
	}
	out = append(out, bottom)
	return out
}

func (m *uiModel) effectiveWidth() int {
	if m.termWidth > 0 {
		return m.termWidth
	}
	return 120
}

func (m *uiModel) effectiveHeight() int {
	if m.termHeight > 0 {
		return m.termHeight
	}
	return 32
}

func (m *uiModel) calcChatLines() int {
	width := m.effectiveWidth()
	height := m.effectiveHeight()
	contentWidth := width
	if contentWidth < 1 {
		contentWidth = 1
	}

	inputContentLines := 1
	if m.activeAsk != nil {
		lines := splitPlainLines(m.renderAskPrompt())
		inputContentLines = 0
		for _, line := range lines {
			inputContentLines += len(wrapLine(line, contentWidth))
		}
	} else {
		text := m.input
		if m.busy {
			text = "input locked while agent is running"
		}
		wrapped := wrapLine("› "+text, contentWidth)
		inputContentLines = len(wrapped)
	}
	if inputContentLines < 1 {
		inputContentLines = 1
	}
	maxContentLines := height - 4
	if maxContentLines < 1 {
		maxContentLines = 1
	}
	if inputContentLines > maxContentLines {
		inputContentLines = maxContentLines
	}
	inputLines := inputContentLines + 2
	chat := height - inputLines - 1
	if chat < 1 {
		return 1
	}
	return chat
}

func (m *uiModel) syncViewport() {
	m.forwardToView(tui.SetViewportSizeMsg{
		Lines: m.calcChatLines(),
		Width: m.effectiveWidth(),
	})
}

func (m *uiModel) inputCursorPosition(width, height, chatLines int) (bool, int, int) {
	if m.busy || m.activeAsk != nil || width < 1 || height < 1 {
		return false, 0, 0
	}
	line := "› " + m.input
	wrapped := wrapLine(line, width)
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	maxContentLines := m.effectiveHeight() - 4
	if maxContentLines < 1 {
		maxContentLines = 1
	}
	if len(wrapped) > maxContentLines {
		wrapped = wrapped[len(wrapped)-maxContentLines:]
	}

	cursorLine := len(wrapped) - 1
	row := chatLines + 2 + cursorLine
	if row < 1 {
		row = 1
	} else if row > height {
		row = height
	}

	col := runewidth.StringWidth(wrapped[cursorLine]) + 1
	if col < 1 {
		col = 1
	} else if col > width {
		col = width
	}
	return true, row, col
}

func splitPlainLines(v string) []string {
	if strings.TrimSpace(v) == "" {
		return []string{""}
	}
	return strings.Split(v, "\n")
}

func wrapLine(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	if runewidth.StringWidth(line) <= width {
		return []string{line}
	}
	parts := make([]string, 0, 4)
	remaining := []rune(line)
	for len(remaining) > 0 {
		w := 0
		cut := 0
		for i, r := range remaining {
			rw := runewidth.RuneWidth(r)
			if w+rw > width {
				break
			}
			w += rw
			cut = i + 1
		}
		if cut == 0 {
			cut = 1
		}
		parts = append(parts, string(remaining[:cut]))
		remaining = remaining[cut:]
	}
	return parts
}

func padRight(line string, width int) string {
	if width <= 0 {
		return ""
	}
	current := runewidth.StringWidth(line)
	if current == width {
		return line
	}
	if current > width {
		return line
	}
	return line + strings.Repeat(" ", width-current)
}

func padANSIRight(line string, width int) string {
	if width <= 0 {
		return ""
	}
	current := lipgloss.Width(line)
	if current >= width {
		return line
	}
	return line + strings.Repeat(" ", width-current)
}

type uiStyles struct {
	brand         lipgloss.Style
	modeChip      lipgloss.Style
	stateChip     lipgloss.Style
	panel         lipgloss.Style
	chat          lipgloss.Style
	input         lipgloss.Style
	inputDisabled lipgloss.Style
	meta          lipgloss.Style
	ask           lipgloss.Style
}

func uiThemeStyles(theme string) uiStyles {
	p := uiPalette(theme)
	return uiStyles{
		brand: lipgloss.NewStyle().Foreground(p.primary).Bold(true),
		modeChip: lipgloss.NewStyle().
			Foreground(p.modeText).
			Background(p.modeBg).
			Padding(0, 1).
			Bold(true),
		stateChip: lipgloss.NewStyle().
			Foreground(p.stateText).
			Background(p.stateBg).
			Padding(0, 1),
		panel: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(p.border).
			Padding(0, 1),
		chat: lipgloss.NewStyle().
			Foreground(p.foreground),
		input: lipgloss.NewStyle().
			Foreground(p.foreground),
		inputDisabled: lipgloss.NewStyle().
			Foreground(p.muted).
			Faint(true),
		meta: lipgloss.NewStyle().Foreground(p.muted).Faint(true),
		ask: lipgloss.NewStyle().
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(p.secondary).
			Foreground(p.foreground).
			Padding(0, 1),
	}
}

type uiColors struct {
	primary    lipgloss.TerminalColor
	secondary  lipgloss.TerminalColor
	foreground lipgloss.TerminalColor
	muted      lipgloss.TerminalColor
	border     lipgloss.TerminalColor
	modeBg     lipgloss.TerminalColor
	modeText   lipgloss.TerminalColor
	stateBg    lipgloss.TerminalColor
	stateText  lipgloss.TerminalColor
	chatBg     lipgloss.TerminalColor
	inputBg    lipgloss.TerminalColor
}

func uiPalette(theme string) uiColors {
	theme = strings.ToLower(strings.TrimSpace(theme))
	if theme == "light" {
		return uiColors{
			// ANSI colors follow terminal defaults; hex values are Atom One Light fallback.
			primary:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "4", ANSI256: "33", TrueColor: "#4078F2"}, Dark: lipgloss.CompleteColor{ANSI: "4", ANSI256: "33", TrueColor: "#61AFEF"}},
			secondary:  lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "5", ANSI256: "134", TrueColor: "#A626A4"}, Dark: lipgloss.CompleteColor{ANSI: "5", ANSI256: "176", TrueColor: "#C678DD"}},
			foreground: lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#383A42"}, Dark: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#ABB2BF"}},
			muted:      lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "8", ANSI256: "245", TrueColor: "#A0A1A7"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "243", TrueColor: "#5C6370"}},
			border:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "250", TrueColor: "#D0D0D0"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "240", TrueColor: "#3D434F"}},
			modeBg:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#EAEAEB"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "238", TrueColor: "#353B45"}},
			modeText:   lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#383A42"}, Dark: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#ABB2BF"}},
			stateBg:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "6", ANSI256: "37", TrueColor: "#EAF2FF"}, Dark: lipgloss.CompleteColor{ANSI: "6", ANSI256: "31", TrueColor: "#28374F"}},
			stateText:  lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "4", ANSI256: "33", TrueColor: "#4078F2"}, Dark: lipgloss.CompleteColor{ANSI: "4", ANSI256: "75", TrueColor: "#61AFEF"}},
			chatBg:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "255", TrueColor: "#F8F8F8"}, Dark: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#1E222A"}},
			inputBg:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "254", TrueColor: "#F2F3F5"}, Dark: lipgloss.CompleteColor{ANSI: "0", ANSI256: "236", TrueColor: "#2A2F37"}},
		}
	}
	return uiColors{
		primary:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "4", ANSI256: "33", TrueColor: "#4078F2"}, Dark: lipgloss.CompleteColor{ANSI: "4", ANSI256: "75", TrueColor: "#61AFEF"}},
		secondary:  lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "5", ANSI256: "134", TrueColor: "#A626A4"}, Dark: lipgloss.CompleteColor{ANSI: "5", ANSI256: "176", TrueColor: "#C678DD"}},
		foreground: lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#383A42"}, Dark: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#ABB2BF"}},
		muted:      lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "8", ANSI256: "245", TrueColor: "#A0A1A7"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "243", TrueColor: "#5C6370"}},
		border:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "250", TrueColor: "#D0D0D0"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "240", TrueColor: "#3D434F"}},
		modeBg:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#EAEAEB"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "238", TrueColor: "#353B45"}},
		modeText:   lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#383A42"}, Dark: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#ABB2BF"}},
		stateBg:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "6", ANSI256: "37", TrueColor: "#EAF2FF"}, Dark: lipgloss.CompleteColor{ANSI: "6", ANSI256: "31", TrueColor: "#28374F"}},
		stateText:  lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "4", ANSI256: "33", TrueColor: "#4078F2"}, Dark: lipgloss.CompleteColor{ANSI: "4", ANSI256: "75", TrueColor: "#61AFEF"}},
		chatBg:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "255", TrueColor: "#F8F8F8"}, Dark: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#1E222A"}},
		inputBg:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "254", TrueColor: "#F2F3F5"}, Dark: lipgloss.CompleteColor{ANSI: "0", ANSI256: "236", TrueColor: "#2A2F37"}},
	}
}
