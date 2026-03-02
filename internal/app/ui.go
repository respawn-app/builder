package app

import (
	"strings"
	"time"

	"builder/internal/app/commands"
	"builder/internal/config"
	"builder/internal/runtime"
	"builder/internal/tools/askquestion"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

type submitDoneMsg struct {
	message string
	err     error
}

type compactDoneMsg struct {
	err error
}

type spinnerTickMsg struct{}

type clearTransientStatusMsg struct {
	token uint64
}

type nativeHistoryFlushMsg struct {
	Text string
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

type uiLogger interface {
	Logf(format string, args ...any)
}

type UIOption func(*uiModel)

type UIAction string

type UITranscriptEntry struct {
	Role string
	Text string
}

type UITransition struct {
	Action               UIAction
	InitialPrompt        string
	TargetSessionID      string
	ForkUserMessageIndex int
	ParentSessionID      string
}

const (
	UIActionNone         UIAction = "none"
	UIActionExit         UIAction = "exit"
	UIActionNewSession   UIAction = "new_session"
	UIActionResume       UIAction = "resume"
	UIActionLogout       UIAction = "logout"
	UIActionForkRollback UIAction = "fork_rollback"
	UIActionOpenSession  UIAction = "open_session"
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

func WithUIAlternateScreenPolicy(policy config.TUIAlternateScreenPolicy) UIOption {
	return func(m *uiModel) {
		m.tuiAlternateScreen = policy
		m.altScreenActive = policy == config.TUIAlternateScreenAlways
	}
}

func WithUIScrollMode(mode config.TUIScrollMode) UIOption {
	return func(m *uiModel) {
		m.tuiScrollMode = mode
	}
}

func WithUIInitialTranscript(entries []UITranscriptEntry) UIOption {
	return func(m *uiModel) {
		m.initialTranscript = append([]UITranscriptEntry(nil), entries...)
	}
}

func WithUICommandRegistry(registry *commands.Registry) UIOption {
	return func(m *uiModel) {
		if registry == nil {
			return
		}
		m.commandRegistry = registry
	}
}

func WithUIStartupSubmit(text string) UIOption {
	return func(m *uiModel) {
		m.startupSubmit = text
	}
}

func WithUISessionName(name string) UIOption {
	return func(m *uiModel) {
		m.sessionName = strings.TrimSpace(name)
	}
}

func WithUISessionID(sessionID string) UIOption {
	return func(m *uiModel) {
		m.sessionID = strings.TrimSpace(sessionID)
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

	input            string
	inputCursor      int // rune index; -1 means "track tail"
	busy             bool
	activity         uiActivity
	compacting       bool
	reviewerRunning  bool
	reviewerBlocking bool

	queued []string

	pendingInjected   []string
	lockedInjectText  string
	inputSubmitLocked bool

	modelName             string
	spinnerFrame          int
	commandRegistry       *commands.Registry
	slashCommandFilter    string
	slashCommandFilterSet bool
	slashCommandSelection int
	exitAction            UIAction
	theme                 string
	tuiAlternateScreen    config.TUIAlternateScreenPolicy
	tuiScrollMode         config.TUIScrollMode
	altScreenActive       bool

	sawAssistantDelta bool
	logger            uiLogger

	activeAsk       *askEvent
	askQueue        []askEvent
	askCursor       int
	askFreeform     bool
	askFreeformMode askFreeformMode
	askInput        string

	termWidth  int
	termHeight int
	windowSizeKnown bool

	initialTranscript []UITranscriptEntry
	startupSubmit     string

	nextSessionInitialPrompt string
	nextSessionID            string
	nextForkUserMessageIndex int
	nextParentSessionID      string
	sessionName              string
	sessionID                string

	transientStatus      string
	transientStatusToken uint64

	transcriptEntries       []tui.TranscriptEntry
	nativeFlushedEntryCount int
	nativeHistoryReplayed   bool
	nativeReplayWidth       int
	nativeFormatter         tui.Model
	nativeFormatterReady    bool
	nativeFormatterWidth    int
	nativeFormatterSnapshot string
	nativeFormatterEntries  []tui.TranscriptEntry
	startupCmds             []tea.Cmd
	nativeLiveRegionLines   int
	nativeLiveRegionPad     int

	lastEscAt time.Time

	rollbackMode                     bool
	rollbackEditing                  bool
	rollbackCandidates               []rollbackCandidate
	rollbackSelection                int
	rollbackSelectedUserMessageIndex int
	rollbackRestoreOngoingScroll     int
	rollbackRestoreScrollActive      bool
}

func (m *uiModel) isInputLocked() bool {
	return m.inputSubmitLocked || m.reviewerBlocking
}

func (m *uiModel) clearReviewerState() {
	m.reviewerRunning = false
	m.reviewerBlocking = false
}

type rollbackCandidate struct {
	TranscriptIndex  int
	UserMessageIndex int
	Text             string
}

func NewUIModel(engine *runtime.Engine, runtimeEvents <-chan runtime.Event, askEvents <-chan askEvent, opts ...UIOption) tea.Model {
	m := &uiModel{
		engine:             engine,
		view:               tui.NewModel(),
		activity:           uiActivityIdle,
		runtimeEvents:      runtimeEvents,
		askEvents:          askEvents,
		inputCursor:        -1,
		commandRegistry:    commands.NewDefaultRegistry(),
		exitAction:         UIActionNone,
		theme:              "dark",
		tuiAlternateScreen: config.TUIAlternateScreenAuto,
		tuiScrollMode:      config.TUIScrollModeAlt,
	}
	for _, opt := range opts {
		opt(m)
	}
	var startupNativeHistoryCmd tea.Cmd
	if m.engine != nil {
		startupNativeHistoryCmd = m.runtimeAdapter().syncConversationFromEngine()
	} else {
		for _, entry := range m.initialTranscript {
			if strings.TrimSpace(entry.Text) == "" {
				continue
			}
			m.transcriptEntries = append(m.transcriptEntries, tui.TranscriptEntry{Role: entry.Role, Text: entry.Text})
			m.forwardToView(tui.AppendTranscriptMsg{Role: entry.Role, Text: entry.Text})
		}
		m.refreshRollbackCandidates()
		startupNativeHistoryCmd = m.syncNativeHistoryFromTranscript()
	}
	if startupNativeHistoryCmd != nil {
		m.startupCmds = append(m.startupCmds, startupNativeHistoryCmd)
	}
	m.syncViewport()
	return m
}

func (m *uiModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		waitRuntimeEvent(m.runtimeEvents),
		waitAskEvent(m.askEvents),
		tea.SetWindowTitle(m.windowTitle()),
		tea.WindowSize(),
	}
	if m.shouldClearOnInit() {
		cmds = append([]tea.Cmd{tea.ClearScreen}, cmds...)
	}
	if strings.TrimSpace(m.startupSubmit) != "" {
		cmds = append(cmds, m.inputController().startSubmission(m.startupSubmit))
	}
	if len(m.startupCmds) > 0 {
		cmds = append(cmds, m.startupCmds...)
		m.startupCmds = nil
	}
	return tea.Batch(cmds...)
}

func (m *uiModel) shouldClearOnInit() bool {
	return !m.usesNativeScrollback()
}

func (m *uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := normalizeKeyMsg(msg); ok {
		if m.activeAsk != nil {
			next, cmd := m.askController().handleKey(keyMsg)
			next.(*uiModel).syncViewport()
			return next, cmd
		}
		next, cmd := m.inputController().handleKey(keyMsg)
		next.(*uiModel).syncViewport()
		return next, cmd
	}
	if _, isKey := msg.(tea.KeyMsg); isKey {
		m.lastEscAt = time.Time{}
		m.syncViewport()
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		previousWidth := m.termWidth
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		m.windowSizeKnown = true
		if m.usesNativeScrollback() && m.nativeFormatterReady && previousWidth > 0 && previousWidth != msg.Width {
			m.rebaseNativeFormatterSnapshot()
		}
		m.syncViewport()
		if m.usesNativeScrollback() && !m.nativeHistoryReplayed {
			return m, m.syncNativeHistoryFromTranscript()
		}
		return m, nil
	case runtimeEventMsg:
		historyCmd := m.runtimeAdapter().handleRuntimeEvent(msg.event)
		m.syncViewport()
		return m, tea.Batch(waitRuntimeEvent(m.runtimeEvents), historyCmd)
	case askEventMsg:
		m.askController().acceptEvent(msg.event)
		m.syncViewport()
		return m, waitAskEvent(m.askEvents)
	case clearTransientStatusMsg:
		if msg.token == m.transientStatusToken {
			m.transientStatus = ""
		}
		m.syncViewport()
		return m, nil
	case nativeHistoryFlushMsg:
		if strings.TrimSpace(msg.Text) == "" {
			return m, nil
		}
		return m, tea.Printf("%s", msg.Text)
	case submitDoneMsg:
		next, cmd := m.inputController().handleSubmitDone(msg)
		next.(*uiModel).syncViewport()
		return next, cmd
	case compactDoneMsg:
		next, cmd := m.inputController().handleCompactDone(msg)
		next.(*uiModel).syncViewport()
		return next, cmd
	case spinnerTickMsg:
		next, cmd := m.inputController().handleSpinnerTick()
		next.(*uiModel).syncViewport()
		return next, cmd
	}

	m.forwardToView(msg)
	m.syncViewport()
	return m, nil
}

func (m *uiModel) forwardToView(msg tea.Msg) {
	next, _ := m.view.Update(msg)
	casted, ok := next.(tui.Model)
	if ok {
		m.view = casted
	}
}

func (m *uiModel) Action() UIAction {
	return m.exitAction
}

func (m *uiModel) Transition() UITransition {
	return UITransition{
		Action:               m.exitAction,
		InitialPrompt:        m.nextSessionInitialPrompt,
		TargetSessionID:      strings.TrimSpace(m.nextSessionID),
		ForkUserMessageIndex: m.nextForkUserMessageIndex,
		ParentSessionID:      strings.TrimSpace(m.nextParentSessionID),
	}
}

func (m *uiModel) windowTitle() string {
	if strings.TrimSpace(m.sessionName) == "" {
		return "builder"
	}
	return m.sessionName
}

func (m *uiModel) logf(format string, args ...any) {
	if m.logger != nil {
		m.logger.Logf(format, args...)
	}
}

func (m *uiModel) inputController() uiInputController {
	return uiInputController{model: m}
}

func (m *uiModel) askController() uiAskController {
	return uiAskController{model: m}
}

func (m *uiModel) runtimeAdapter() uiRuntimeAdapter {
	return uiRuntimeAdapter{model: m}
}

func (m *uiModel) layout() uiViewLayout {
	return uiViewLayout{model: m}
}
