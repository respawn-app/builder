package app

import (
	"fmt"
	"os"
	"strings"
	"time"

	"builder/internal/app/commands"
	"builder/internal/config"
	"builder/internal/runtime"
	"builder/internal/tools/askquestion"
	shelltool "builder/internal/tools/shell"
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

type processListRefreshTickMsg struct{}

type openProcessLogsDoneMsg struct {
	err error
}

type clearTransientStatusMsg struct {
	token uint64
}

type nativeResizeReplayMsg struct {
	token uint64
}

type nativeHistoryFlushMsg struct {
	Text       string
	AllowBlank bool
}

type runtimeEventMsg struct {
	event runtime.Event
}

type renderDiagnosticMsg struct {
	diagnostic tui.RenderDiagnostic
}

type runLoggerDiagnosticMsg struct {
	diagnostic runLoggerDiagnostic
}

type askEvent struct {
	req   askquestion.Request
	reply chan askReply
}

type askReply struct {
	response askquestion.Response
	err      error
}

type askEventMsg struct {
	event askEvent
}

type askBridge struct {
	ch chan askEvent
}

type uiStatusNoticeKind uint8

const (
	uiStatusNoticeNeutral uiStatusNoticeKind = iota
	uiStatusNoticeSuccess
	uiStatusNoticeError
)

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

var nativeResizeReplayDebounce = time.Second

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

func WithUIConfiguredModelName(model string) UIOption {
	return func(m *uiModel) {
		m.configuredModelName = strings.TrimSpace(model)
	}
}

func WithUIThinkingLevel(thinkingLevel string) UIOption {
	return func(m *uiModel) {
		m.thinkingLevel = strings.TrimSpace(thinkingLevel)
	}
}

func WithUIFastModeAvailable(available bool) UIOption {
	return func(m *uiModel) {
		m.fastModeAvailable = available
	}
}

func WithUIFastModeEnabled(enabled bool) UIOption {
	return func(m *uiModel) {
		m.fastModeEnabled = enabled
	}
}

func WithUIModelContractLocked(locked bool) UIOption {
	return func(m *uiModel) {
		m.modelContractLocked = locked
	}
}

func WithUITheme(theme string) UIOption {
	return func(m *uiModel) {
		m.theme = strings.TrimSpace(theme)
		m.view = tui.NewModel(
			tui.WithTheme(theme),
			tui.WithRenderDiagnosticHandler(m.handleRenderDiagnostic),
		)
	}
}

func WithUIAlternateScreenPolicy(policy config.TUIAlternateScreenPolicy) UIOption {
	return func(m *uiModel) {
		m.tuiAlternateScreen = policy
		m.altScreenActive = policy == config.TUIAlternateScreenAlways
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

func WithUIBackgroundManager(manager *shelltool.Manager) UIOption {
	return func(m *uiModel) {
		m.backgroundManager = manager
	}
}

func newAskBridge() *askBridge {
	return &askBridge{ch: make(chan askEvent, 64)}
}

func (b *askBridge) Events() <-chan askEvent {
	return b.ch
}

func (b *askBridge) Handle(req askquestion.Request) (askquestion.Response, error) {
	e := askEvent{req: req, reply: make(chan askReply, 1)}
	b.ch <- e
	resp := <-e.reply
	return resp.response, resp.err
}

type uiModel struct {
	engine *runtime.Engine
	view   tui.Model

	backgroundManager *shelltool.Manager

	runtimeEvents <-chan runtime.Event
	askEvents     <-chan askEvent

	input                 string
	inputCursor           int // rune index; -1 means "track tail"
	busy                  bool
	activity              uiActivity
	compacting            bool
	reviewerRunning       bool
	reviewerBlocking      bool
	reviewerEnabled       bool
	reviewerMode          string
	autoCompactionEnabled bool

	queued []string

	pendingInjected   []string
	lockedInjectText  string
	inputSubmitLocked bool

	modelName             string
	configuredModelName   string
	thinkingLevel         string
	fastModeAvailable     bool
	fastModeEnabled       bool
	modelContractLocked   bool
	spinnerFrame          int
	commandRegistry       *commands.Registry
	slashCommandFilter    string
	slashCommandFilterSet bool
	slashCommandSelection int
	exitAction            UIAction
	theme                 string
	tuiAlternateScreen    config.TUIAlternateScreenPolicy
	altScreenActive       bool

	sawAssistantDelta bool
	logger            uiLogger

	activeAsk       *askEvent
	askQueue        []askEvent
	askCursor       int
	askFreeform     bool
	askFreeformMode askFreeformMode
	askInput        string

	termWidth       int
	termHeight      int
	windowSizeKnown bool

	initialTranscript []UITranscriptEntry
	startupSubmit     string

	nextSessionInitialPrompt string
	nextSessionID            string
	nextForkUserMessageIndex int
	nextParentSessionID      string
	sessionName              string
	sessionID                string
	psVisible                bool
	psOverlayPushed          bool
	psSelection              int
	psEntries                []shelltool.Snapshot
	helpVisible              bool
	reasoningStatusHeader    string

	transientStatus      string
	transientStatusKind  uiStatusNoticeKind
	transientStatusToken uint64
	debugKeys            bool

	transcriptEntries       []tui.TranscriptEntry
	nativeFlushedEntryCount int
	nativeHistoryReplayed   bool
	nativeReplayWidth       int
	nativeFormatter         tui.Model
	nativeFormatterReady    bool
	nativeFormatterWidth    int
	nativeFormatterSnapshot string
	nativeRenderedSnapshot  string
	nativeFormatterEntries  []tui.TranscriptEntry
	startupCmds             []tea.Cmd
	nativeLiveRegionLines   int
	nativeLiveRegionPad     int
	nativeStreamingActive   bool
	nativeResizeReplayToken uint64

	lastEscAt              time.Time
	pendingCSIShiftEnterAt time.Time
	pendingCSIShiftEnter   bool

	rollbackMode                     bool
	rollbackEditing                  bool
	rollbackOverlayPushed            bool
	rollbackCandidates               []rollbackCandidate
	rollbackSelection                int
	rollbackSelectedUserMessageIndex int
	rollbackRestoreOngoingScroll     int
	rollbackRestoreScrollActive      bool
}

func (m *uiModel) isInputLocked() bool {
	return m.inputSubmitLocked
}

func (m *uiModel) clearReviewerState() {
	m.reviewerRunning = false
	m.reviewerBlocking = false
}

func (m *uiModel) invalidateNativeResizeReplay() {
	m.nativeResizeReplayToken++
}

type rollbackCandidate struct {
	TranscriptIndex  int
	UserMessageIndex int
	Text             string
}

func NewUIModel(engine *runtime.Engine, runtimeEvents <-chan runtime.Event, askEvents <-chan askEvent, opts ...UIOption) tea.Model {
	m := &uiModel{
		engine:                engine,
		view:                  tui.NewModel(),
		activity:              uiActivityIdle,
		runtimeEvents:         runtimeEvents,
		askEvents:             askEvents,
		inputCursor:           -1,
		commandRegistry:       commands.NewDefaultRegistry(),
		exitAction:            UIActionNone,
		theme:                 "dark",
		tuiAlternateScreen:    config.TUIAlternateScreenAuto,
		debugKeys:             envFlagEnabled("BUILDER_DEBUG_KEYS"),
		reviewerMode:          "off",
		autoCompactionEnabled: true,
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.engine != nil {
		m.reviewerMode = m.engine.ReviewerFrequency()
		m.reviewerEnabled = m.engine.ReviewerEnabled()
		m.autoCompactionEnabled = m.engine.AutoCompactionEnabled()
		m.fastModeAvailable = m.engine.FastModeAvailable()
		m.fastModeEnabled = m.engine.FastModeEnabled()
	} else {
		m.reviewerEnabled = strings.TrimSpace(m.reviewerMode) != "" && strings.TrimSpace(m.reviewerMode) != "off"
	}
	m.refreshProcessEntries()
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

func (m *uiModel) handleRenderDiagnostic(diag tui.RenderDiagnostic) {
	m.startupCmds = append(m.startupCmds, func() tea.Msg {
		return renderDiagnosticMsg{diagnostic: diag}
	})
}

func (m *uiModel) handleRunLoggerDiagnostic(diag runLoggerDiagnostic) {
	m.startupCmds = append(m.startupCmds, func() tea.Msg {
		return runLoggerDiagnosticMsg{diagnostic: diag}
	})
}

func (m *uiModel) applyRenderDiagnostic(diag tui.RenderDiagnostic) tea.Cmd {
	message := strings.TrimSpace(diag.Message)
	if message == "" {
		return nil
	}
	severity := strings.TrimSpace(string(diag.Severity))
	if severity == "" {
		severity = string(tui.RenderDiagnosticSeverityWarn)
	}
	m.logf("render.diagnostic severity=%s component=%s message=%q", severity, strings.TrimSpace(diag.Component), message)
	if diag.Err != nil {
		m.logf("render.diagnostic.err component=%s err=%q", strings.TrimSpace(diag.Component), diag.Err.Error())
	}
	kind := uiStatusNoticeNeutral
	switch diag.Severity {
	case tui.RenderDiagnosticSeverityError, tui.RenderDiagnosticSeverityFatal:
		kind = uiStatusNoticeError
	default:
		kind = uiStatusNoticeNeutral
	}
	return m.setTransientStatusWithKind(message, kind)
}

func (m *uiModel) applyRunLoggerDiagnostic(diag runLoggerDiagnostic) tea.Cmd {
	message := strings.TrimSpace(diag.Message)
	if message == "" {
		message = "run logger diagnostic"
	}
	m.logf("run_logger.diagnostic kind=%s message=%q", strings.TrimSpace(diag.Kind), message)
	if diag.Err != nil {
		m.logf("run_logger.diagnostic.err kind=%s err=%q", strings.TrimSpace(diag.Kind), diag.Err.Error())
	}
	return m.setTransientStatusWithKind(message, uiStatusNoticeError)
}

func (m *uiModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		waitRuntimeEvent(m.runtimeEvents),
		waitAskEvent(m.askEvents),
		tea.SetWindowTitle(m.windowTitle()),
		tea.WindowSize(),
	}
	cmds = append([]tea.Cmd{tea.ClearScreen}, cmds...)
	if strings.TrimSpace(m.startupSubmit) != "" {
		cmds = append(cmds, m.inputController().startSubmission(m.startupSubmit))
	}
	if len(m.startupCmds) > 0 {
		cmds = append(cmds, m.startupCmds...)
		m.startupCmds = nil
	}
	return tea.Batch(cmds...)
}

func (m *uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok, source := normalizeKeyMsgWithSource(msg); ok {
		if m.debugKeys {
			m.setDebugKeyTransientStatus(msg, keyMsg, source)
		}
		if m.helpVisible {
			m.helpVisible = false
			if isHelpKey(keyMsg, m) && m.canShowHelp() {
				m.lastEscAt = time.Time{}
				m.syncViewport()
				return m, nil
			}
		}
		if isHelpKey(keyMsg, m) && m.canShowHelp() {
			m.lastEscAt = time.Time{}
			m.toggleHelp()
			m.syncViewport()
			return m, nil
		}
		switch m.inputModeState().Mode {
		case uiInputModeAsk:
			next, cmd := m.askController().handleKey(keyMsg)
			next.(*uiModel).syncViewport()
			return next, cmd
		default:
			next, cmd := m.inputController().handleKey(keyMsg)
			next.(*uiModel).syncViewport()
			return next, cmd
		}
	}
	if _, isKey := msg.(tea.KeyMsg); isKey {
		if m.helpVisible {
			m.helpVisible = false
		}
		m.lastEscAt = time.Time{}
		m.syncViewport()
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		previousWidth := m.termWidth
		previousHeight := m.termHeight
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		m.windowSizeKnown = true
		if m.nativeFormatterReady && previousWidth > 0 && previousWidth != msg.Width {
			m.rebaseNativeFormatterSnapshot()
		}
		m.syncViewport()
		if !m.nativeHistoryReplayed {
			return m, m.syncNativeHistoryFromTranscript()
		}
		if previousWidth > 0 && previousHeight > 0 && (previousWidth != msg.Width || previousHeight != msg.Height) && m.view.Mode() == tui.ModeOngoing {
			m.nativeResizeReplayToken++
			token := m.nativeResizeReplayToken
			return m, tea.Tick(nativeResizeReplayDebounce, func(time.Time) tea.Msg {
				return nativeResizeReplayMsg{token: token}
			})
		}
		return m, nil
	case nativeResizeReplayMsg:
		if msg.token != m.nativeResizeReplayToken || m.view.Mode() != tui.ModeOngoing {
			return m, nil
		}
		if replay := m.emitCurrentNativeScrollbackState(true); replay != nil {
			return m, replay
		}
		return m, tea.ClearScreen
	case runtimeEventMsg:
		historyCmd := m.runtimeAdapter().handleRuntimeEvent(msg.event)
		m.syncViewport()
		return m, tea.Batch(waitRuntimeEvent(m.runtimeEvents), historyCmd)
	case renderDiagnosticMsg:
		cmd := m.applyRenderDiagnostic(msg.diagnostic)
		m.syncViewport()
		return m, cmd
	case runLoggerDiagnosticMsg:
		cmd := m.applyRunLoggerDiagnostic(msg.diagnostic)
		m.syncViewport()
		return m, cmd
	case askEventMsg:
		m.askController().acceptEvent(msg.event)
		m.syncViewport()
		return m, waitAskEvent(m.askEvents)
	case clearTransientStatusMsg:
		if msg.token == m.transientStatusToken {
			m.transientStatus = ""
			m.transientStatusKind = uiStatusNoticeNeutral
		}
		m.syncViewport()
		return m, nil
	case nativeHistoryFlushMsg:
		if !msg.AllowBlank && strings.TrimSpace(msg.Text) == "" {
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
	case processListRefreshTickMsg:
		if !m.psVisible {
			m.syncViewport()
			return m, nil
		}
		m.refreshProcessEntries()
		m.syncViewport()
		return m, waitProcessListRefresh()
	case openProcessLogsDoneMsg:
		m.syncViewport()
		if msg.err != nil {
			return m, m.setTransientStatusWithKind(msg.err.Error(), uiStatusNoticeError)
		}
		return m, nil
	}

	m.forwardToView(msg)
	m.syncViewport()
	return m, nil
}

func (m *uiModel) setDebugKeyTransientStatus(raw tea.Msg, normalized tea.KeyMsg, source string) {
	rawString := ""
	if stringer, ok := raw.(fmt.Stringer); ok {
		rawString = stringer.String()
	}
	m.transientStatusToken++
	m.transientStatus = fmt.Sprintf("key src=%s raw=%q norm=%q type=%d", source, rawString, normalized.String(), normalized.Type)
	m.transientStatusKind = uiStatusNoticeNeutral
}

func envFlagEnabled(name string) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return false
	}
	switch strings.ToLower(value) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
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

func (m *uiModel) setTransientStatus(message string) tea.Cmd {
	return m.setTransientStatusWithKind(message, uiStatusNoticeNeutral)
}

func (m *uiModel) setTransientStatusWithKind(message string, kind uiStatusNoticeKind) tea.Cmd {
	m.transientStatusToken++
	token := m.transientStatusToken
	m.transientStatus = strings.TrimSpace(message)
	m.transientStatusKind = kind
	return tea.Tick(transientStatusDuration, func(time.Time) tea.Msg {
		return clearTransientStatusMsg{token: token}
	})
}

func (m *uiModel) layout() uiViewLayout {
	return uiViewLayout{model: m}
}
