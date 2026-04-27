package app

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"builder/cli/app/commands"
	"builder/cli/tui"
	"builder/server/session"
	"builder/server/tools/askquestion"
	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/serverapi"
	"builder/shared/theme"
	"builder/shared/transcriptdiag"

	tea "github.com/charmbracelet/bubbletea"
)

type submitDoneMsg struct {
	token         uint64
	message       string
	submittedText string
	silentFinal   bool
	err           error
}

func newSubmitDoneMsg(token uint64, message string, submittedText string, err error) submitDoneMsg {
	return submitDoneMsg{
		token:         token,
		message:       message,
		submittedText: submittedText,
		silentFinal:   isNoopFinalText(message),
		err:           err,
	}
}

type preSubmitCompactionCheckDoneMsg struct {
	token         uint64
	text          string
	shouldCompact bool
	err           error
}

type promptHistoryPersistErrMsg struct {
	err error
}

type compactDoneMsg struct {
	err error
}

type activeSubmitState struct {
	token   uint64
	stepID  string
	text    string
	flushed bool
}

type spinnerTickMsg struct {
	token uint64
	at    time.Time
}

type processListRefreshTickMsg struct{}

type openProcessLogsDoneMsg struct {
	err error
}

type clearTransientStatusMsg struct {
	token uint64
}

type startupUpdateNoticeMsg struct {
	version string
}

type nativeResizeReplayMsg struct {
	token uint64
}

type nativeHistoryFlushMsg struct {
	Text       string
	AllowBlank bool
	Sequence   uint64
}

type runtimeEventMsg struct {
	event clientui.Event
}

type runtimeEventBatchMsg struct {
	events []clientui.Event
	carry  *clientui.Event
}

type runtimeConnectionStateChangedMsg struct {
	err error
}

type runtimeLeaseRecoveryWarningMsg struct {
	text       string
	visibility clientui.EntryVisibility
}

type runtimeMainViewRefreshedMsg struct {
	token uint64
	view  clientui.RuntimeMainView
	err   error
}

type runtimeTranscriptRefreshedMsg struct {
	token         uint64
	req           clientui.TranscriptPageRequest
	syncCause     runtimeTranscriptSyncCause
	transcript    clientui.TranscriptPage
	recoveryCause clientui.TranscriptRecoveryCause
	err           error
}

type runtimeTranscriptRetryMsg struct {
	syncCause     runtimeTranscriptSyncCause
	token         uint64
	recoveryCause clientui.TranscriptRecoveryCause
}

type runtimeTranscriptSyncCause string

const (
	runtimeTranscriptSyncCauseBootstrap               runtimeTranscriptSyncCause = "bootstrap"
	runtimeTranscriptSyncCauseCommittedConversation   runtimeTranscriptSyncCause = "committed_conversation_updated"
	runtimeTranscriptSyncCauseCommittedGap            runtimeTranscriptSyncCause = "committed_gap"
	runtimeTranscriptSyncCauseQueuedDrain             runtimeTranscriptSyncCause = "queued_drain"
	runtimeTranscriptSyncCauseDirtyFollowUp           runtimeTranscriptSyncCause = "dirty_follow_up"
	runtimeTranscriptSyncCauseContinuityRecovery      runtimeTranscriptSyncCause = "continuity_recovery"
	runtimeTranscriptSyncCauseManualTranscriptRefresh runtimeTranscriptSyncCause = "manual_transcript_refresh"
)

type detailTranscriptLoadMsg struct{}

type renderDiagnosticMsg struct {
	diagnostic tui.RenderDiagnostic
}

type deferredProjectedTranscriptTail struct {
	rangeStart int
	rangeEnd   int
	revision   int64
	entries    []clientui.ChatEntry
	pending    []string
}

type nativeHistoryReplayPermit uint8

const (
	nativeHistoryReplayPermitNone nativeHistoryReplayPermit = iota
	nativeHistoryReplayPermitContinuityRecovery
	nativeHistoryReplayPermitAuthoritativeHydrate
	nativeHistoryReplayPermitModeRestore
)

type runLoggerDiagnosticMsg struct {
	diagnostic runLoggerDiagnostic
}

type clipboardImagePasteDoneMsg struct {
	Target         uiClipboardPasteTarget
	MainDraftToken uint64
	AskToken       uint64
	Path           string
	Err            error
}

type clipboardTextCopyDoneMsg struct {
	Err error
}

type askEvent struct {
	req              askquestion.Request
	reply            chan askReply
	cancel           func()
	resolvedPromptID string
}

func (e askEvent) promptID() string {
	if strings.TrimSpace(e.resolvedPromptID) != "" {
		return strings.TrimSpace(e.resolvedPromptID)
	}
	return strings.TrimSpace(e.req.ID)
}

func (e askEvent) isResolution() bool {
	return strings.TrimSpace(e.resolvedPromptID) != ""
}

func (e askEvent) cancelPending() {
	if e.cancel != nil {
		e.cancel()
	}
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
	uiStatusNoticeUpdateAvailable
)

type uiStatusNotice struct {
	Text     string
	Kind     uiStatusNoticeKind
	Duration time.Duration
}

type uiStatusNoticeDelivery uint8

const (
	uiStatusNoticeReplace uiStatusNoticeDelivery = iota
	uiStatusNoticeQueue
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
	Action                   UIAction
	InitialPrompt            string
	InitialInput             string
	TargetSessionID          string
	ForkUserMessageIndex     int
	ForkTranscriptEntryIndex int
	ParentSessionID          string
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
var nativeResizeReplayNow = time.Now

func WithUILogger(logger uiLogger) UIOption {
	return func(m *uiModel) {
		m.logger = logger
		if logger != nil {
			if configurable, ok := m.engine.(interface{ SetTranscriptDiagnosticLogger(func(string)) }); ok {
				configurable.SetTranscriptDiagnosticLogger(func(line string) {
					logger.Logf("%s", strings.TrimSpace(line))
				})
			}
		}
	}
}

func WithUITranscriptDiagnostics(enabled bool) UIOption {
	return func(m *uiModel) {
		m.transcriptDiagnostics = enabled
		m.updateTranscriptDiagnosticsMode()
	}
}

func WithUIDebug(enabled bool) UIOption {
	return func(m *uiModel) {
		m.debugMode = enabled
		m.updateTranscriptDiagnosticsMode()
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

func WithUIConversationFreshness(freshness session.ConversationFreshness) UIOption {
	return func(m *uiModel) {
		m.conversationFreshness = mapConversationFreshness(freshness)
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
			tui.WithCompactDetail(),
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

func WithUIHasOtherSessions(known bool, available bool) UIOption {
	return func(m *uiModel) {
		m.hasOtherSessionsKnown = known
		m.hasOtherSessions = available
	}
}

func WithUIStartupSubmit(text string) UIOption {
	return func(m *uiModel) {
		m.startupSubmit = text
	}
}

func WithUIInitialInput(text string) UIOption {
	return func(m *uiModel) {
		if text == "" || m.input != "" {
			return
		}
		m.replaceMainInput(text, -1)
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
		if !m.processClientExplicit {
			m.processClient = newUIProcessClient(manager)
		}
	}
}

func WithUIProcessClient(client clientui.ProcessClient) UIOption {
	return func(m *uiModel) {
		m.processClient = client
		m.processClientExplicit = true
	}
}

func WithUIWorktreeClient(client client.WorktreeClient) UIOption {
	return func(m *uiModel) {
		m.worktreeClient = client
	}
}

func WithUITurnQueueHook(hook turnQueueHook) UIOption {
	return func(m *uiModel) {
		m.turnQueueHook = hook
	}
}

func WithUIPromptHistory(history []string) UIOption {
	return func(m *uiModel) {
		m.loadPromptHistory(history)
	}
}

func WithUIStartupUpdateNotice(enabled bool) UIOption {
	return func(m *uiModel) {
		m.startupUpdateNotice = enabled
	}
}

func WithUIClipboardImagePaster(paster uiClipboardImagePaster) UIOption {
	return func(m *uiModel) {
		m.clipboardImagePaster = paster
	}
}

func WithUIClipboardTextCopier(copier uiClipboardTextCopier) UIOption {
	return func(m *uiModel) {
		m.clipboardTextCopier = copier
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
	engine clientui.RuntimeClient
	view   tui.Model

	processClient         clientui.ProcessClient
	processClientExplicit bool
	worktreeClient        client.WorktreeClient

	runtimeEvents               <-chan clientui.Event
	pendingRuntimeEvents        []clientui.Event
	askEvents                   <-chan askEvent
	pathReferenceEvents         <-chan uiPathReferenceSearchEvent
	runtimeConnectionEvents     <-chan runtimeConnectionStateChangedMsg
	runtimeLeaseRecoveryWarning <-chan runtimeLeaseRecoveryWarningMsg

	input                    string
	inputCursor              int // rune index; -1 means "track tail"
	mainInputDraftToken      uint64
	promptHistory            []string
	promptHistorySelection   int
	promptHistoryDraft       string
	promptHistoryDraftCursor int
	busy                     bool
	activity                 uiActivity
	compacting               bool
	reviewerRunning          bool
	reviewerBlocking         bool
	reviewerEnabled          bool
	reviewerMode             string
	autoCompactionEnabled    bool
	conversationFreshness    clientui.ConversationFreshness

	queued               []string
	preSubmitCheckToken  uint64
	pendingPreSubmitText string
	submitToken          uint64
	activeSubmit         activeSubmitState

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
	spinnerClock          frameAnimationClock
	spinnerGeneration     uint64
	spinnerTickToken      uint64
	commandRegistry       *commands.Registry
	hasOtherSessions      bool
	hasOtherSessionsKnown bool
	authSlashCommandName  string
	authSlashCommandErr   string
	authSlashSessionOpen  bool
	slashCommandFilter    string
	slashCommandFilterSet bool
	slashCommandSelection int
	pathReferenceSearch   uiPathReferenceSearch
	pathReference         uiPathReferenceState
	exitAction            UIAction
	theme                 string
	tuiAlternateScreen    config.TUIAlternateScreenPolicy
	altScreenActive       bool

	sawAssistantDelta            bool
	lastCommittedAssistantStepID string
	logger                       uiLogger

	interaction uiInteractionState
	ask         uiAskState

	termWidth       int
	termHeight      int
	windowSizeKnown bool

	initialTranscript []UITranscriptEntry
	startupSubmit     string

	nextSessionInitialPrompt     string
	nextSessionInitialInput      string
	nextSessionID                string
	nextForkUserMessageIndex     int
	nextForkTranscriptEntryIndex int
	nextParentSessionID          string
	sessionName                  string
	sessionID                    string
	processList                  uiProcessListState
	helpVisible                  bool
	reasoningStatusHeader        string
	turnQueueHook                turnQueueHook
	statusConfig                 uiStatusConfig
	statusCollector              uiStatusCollector
	statusRepository             uiStatusRepository
	status                       uiStatusOverlayState
	statusGitBackgroundInFlight  bool
	clipboardImagePaster         uiClipboardImagePaster
	clipboardTextCopier          uiClipboardTextCopier

	transientStatus       string
	transientStatusKind   uiStatusNoticeKind
	transientStatusToken  uint64
	transientStatusQueue  []uiStatusNotice
	startupUpdateNotice   bool
	startupUpdateShown    bool
	debugKeys             bool
	debugMode             bool
	transcriptDiagnostics bool

	transcriptEntries                   []tui.TranscriptEntry
	transcriptBaseOffset                int
	transcriptTotalEntries              int
	transcriptRevision                  int64
	deferredCommittedTail               []deferredProjectedTranscriptTail
	runtimeDisconnected                 bool
	transcriptLiveDirty                 bool
	reasoningLiveDirty                  bool
	detailTranscript                    uiDetailTranscriptWindow
	runtimeMainViewToken                uint64
	runtimeTranscriptToken              uint64
	runtimeTranscriptRetry              uint64
	runtimeTranscriptBusy               bool
	runtimeTranscriptDirty              bool
	runtimeTranscriptDirtyRecoveryCause clientui.TranscriptRecoveryCause
	pendingQueuedDrainAfterHydration    bool
	queuedDrainReadyAfterHydration      bool
	waitRuntimeEventAfterHydration      bool
	nativeFlushedEntryCount             int
	nativeHistoryReplayed               bool
	nativeReplayWidth                   int
	nativeFormatterWidth                int
	nativeProjection                    tui.TranscriptProjection
	nativeProjectionBaseOffset          int
	nativeRenderedProjection            tui.TranscriptProjection
	nativeRenderedBaseOffset            int
	nativeRenderedSnapshot              string
	nativeHistoryReplayPermit           nativeHistoryReplayPermit
	nativeFlushSequence                 uint64
	nativeFlushedSequence               uint64
	nativePendingFlushes                map[uint64]nativeHistoryFlushMsg
	waitRuntimeEventAfterFlushSequence  uint64
	startupCmds                         []tea.Cmd
	nativeLiveRegionLines               int
	nativeLiveRegionPad                 int
	nativeStreamingActive               bool
	nativeStreamingText                 string
	nativeStreamingWidth                int
	nativeStreamingFlushedLineCount     int
	nativeStreamingDividerFlushed       bool
	nativeResizeReplayToken             uint64
	nativeResizeReplayAt                time.Time

	lastEscAt              time.Time
	pendingCSIShiftEnterAt time.Time
	pendingCSIShiftEnter   bool

	rollback  uiRollbackState
	worktrees uiWorktreeOverlayState
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
	TranscriptIndex int
	Text            string
}

func NewProjectedUIModel(runtimeClient clientui.RuntimeClient, runtimeEvents <-chan clientui.Event, askEvents <-chan askEvent, opts ...UIOption) tea.Model {
	m := &uiModel{
		engine:                       runtimeClient,
		view:                         tui.NewModel(tui.WithCompactDetail()),
		activity:                     uiActivityIdle,
		runtimeEvents:                runtimeEvents,
		askEvents:                    askEvents,
		inputCursor:                  -1,
		mainInputDraftToken:          1,
		promptHistorySelection:       -1,
		promptHistoryDraftCursor:     -1,
		commandRegistry:              commands.NewDefaultRegistry(),
		exitAction:                   UIActionNone,
		theme:                        theme.Auto,
		tuiAlternateScreen:           config.TUIAlternateScreenAuto,
		debugKeys:                    envFlagEnabled("BUILDER_DEBUG_KEYS"),
		debugMode:                    envFlagEnabled("BUILDER_DEBUG"),
		transcriptDiagnostics:        envFlagEnabled("BUILDER_TRANSCRIPT_DIAGNOSTICS"),
		nextForkTranscriptEntryIndex: -1,
		reviewerMode:                 "off",
		autoCompactionEnabled:        true,
		conversationFreshness:        clientui.ConversationFreshnessFresh,
		interaction:                  uiInteractionState{Mode: uiInputModeMain},
		ask:                          uiAskState{inputCursor: -1},
		rollback:                     uiRollbackState{phase: uiRollbackPhaseInactive, selectedTranscriptEntry: -1},
		statusRepository:             newMemoryUIStatusRepository(),
		clipboardImagePaster:         newSystemClipboardImagePaster(),
		clipboardTextCopier:          newSystemClipboardTextCopier(),
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.pathReferenceSearch == nil {
		m.pathReferenceSearch = newUIPathReferenceSearch()
		m.pathReferenceEvents = m.pathReferenceSearch.Events()
	}
	m.updateTranscriptDiagnosticsMode()
	m.refreshAutocompleteFromInput()
	if configurable, ok := m.engine.(interface{ SetConnectionStateObserver(func(error)) }); ok {
		runtimeConnectionEvents := make(chan runtimeConnectionStateChangedMsg, 1)
		m.runtimeConnectionEvents = runtimeConnectionEvents
		configurable.SetConnectionStateObserver(func(err error) {
			enqueueRuntimeConnectionStateChange(runtimeConnectionEvents, err)
		})
	}
	if configurable, ok := m.engine.(interface {
		SetLeaseRecoveryWarningObserver(func(string, clientui.EntryVisibility))
	}); ok {
		runtimeLeaseRecoveryWarning := make(chan runtimeLeaseRecoveryWarningMsg, 1)
		m.runtimeLeaseRecoveryWarning = runtimeLeaseRecoveryWarning
		configurable.SetLeaseRecoveryWarningObserver(func(text string, visibility clientui.EntryVisibility) {
			enqueueRuntimeLeaseRecoveryWarning(runtimeLeaseRecoveryWarning, text, visibility)
		})
	}
	status := m.runtimeStatus()
	m.reviewerMode = status.ReviewerFrequency
	m.reviewerEnabled = status.ReviewerEnabled
	m.autoCompactionEnabled = status.AutoCompactionEnabled
	m.fastModeAvailable = status.FastModeAvailable
	m.fastModeEnabled = status.FastModeEnabled
	m.conversationFreshness = status.ConversationFreshness
	m.refreshAuthSlashCommandState()
	if !m.hasRuntimeClient() {
		m.reviewerEnabled = strings.TrimSpace(m.reviewerMode) != "" && strings.TrimSpace(m.reviewerMode) != "off"
	}
	m.refreshProcessEntries()
	var startupNativeHistoryCmd tea.Cmd
	if m.hasRuntimeClient() {
		seedView := m.runtimeMainView().Session
		_ = m.runtimeAdapter().applyProjectedSessionMetadata(seedView)
		_ = m.runtimeAdapter().applyProjectedTranscriptPage(m.runtimeTranscript())
		startupNativeHistoryCmd = m.requestRuntimeBootstrapTranscriptSync()
		m.runtimeTranscriptBusy = false
	} else {
		for _, entry := range m.initialTranscript {
			if strings.TrimSpace(entry.Text) == "" {
				continue
			}
			m.transcriptEntries = append(m.transcriptEntries, tui.TranscriptEntry{Role: entry.Role, Text: entry.Text})
			m.forwardToView(tui.AppendTranscriptMsg{Role: entry.Role, Text: entry.Text})
		}
		m.transcriptBaseOffset = 0
		m.transcriptTotalEntries = len(m.transcriptEntries)
		m.seedPromptHistoryFromTranscriptEntries(m.transcriptEntries)
		m.refreshRollbackCandidates()
		startupNativeHistoryCmd = m.syncNativeHistoryFromTranscript()
	}
	if startupNativeHistoryCmd != nil {
		m.startupCmds = append(m.startupCmds, startupNativeHistoryCmd)
	}
	if gitStartupCmd := m.statusLineGitStartupCmd(); gitStartupCmd != nil {
		m.statusGitBackgroundInFlight = true
		m.startupCmds = append(m.startupCmds, gitStartupCmd)
	}
	if m.pathReferenceSearch != nil && strings.TrimSpace(m.statusConfig.WorkspaceRoot) != "" {
		m.startupCmds = append(m.startupCmds, func() tea.Msg {
			m.pathReferenceSearch.StartPrewarm(strings.TrimSpace(m.statusConfig.WorkspaceRoot))
			return nil
		})
	}
	if m.startupUpdateNotice && m.hasRuntimeClient() {
		m.startupCmds = append(m.startupCmds, m.startupUpdateNoticeCmd(status.Update))
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

func (m *uiModel) handleRuntimeEventBatch(events []clientui.Event) (*uiModel, tea.Cmd) {
	flushSequenceBefore := m.nativeFlushSequence
	wasReviewerRunning := m.reviewerRunning
	m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.runtime_batch", map[string]string{
		"session_id":             strings.TrimSpace(m.sessionID),
		"mode":                   string(m.view.Mode()),
		"event_count":            strconv.Itoa(len(events)),
		"pending_runtime_events": strconv.Itoa(len(m.pendingRuntimeEvents)),
		"wait_after_flush":       strconv.FormatUint(m.waitRuntimeEventAfterFlushSequence, 10),
		"wait_after_hydration":   strconv.FormatBool(m.waitRuntimeEventAfterHydration),
	}))
	result := m.runtimeAdapter().applyProjectedRuntimeEventsBatch(events)
	cmd := result.cmd
	if !wasReviewerRunning && m.reviewerRunning {
		cmd = tea.Batch(cmd, m.ensureSpinnerTicking())
	}
	if wasReviewerRunning && !m.reviewerRunning && !m.shouldAnimateSpinner() {
		m.stopSpinnerTicking()
	}
	if !result.awaitsHydration {
		cmd = sequenceCmds(cmd, m.flushQueuedInputsAfterHydration())
	}
	m.syncViewport()
	if !result.transcriptMutated {
		cmd = sequenceCmds(cmd, m.syncNativeStreamingScrollback())
	}
	if result.awaitsHydration {
		m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.runtime_batch_pause", map[string]string{
			"session_id":             strings.TrimSpace(m.sessionID),
			"mode":                   string(m.view.Mode()),
			"pending_runtime_events": strconv.Itoa(len(m.pendingRuntimeEvents)),
			"native_flush_sequence":  strconv.FormatUint(m.nativeFlushSequence, 10),
		}))
		m.waitRuntimeEventAfterHydration = true
	}
	if m.nativeFlushSequence != flushSequenceBefore {
		m.waitRuntimeEventAfterFlushSequence = m.nativeFlushSequence
		m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.runtime_batch_wait_flush", map[string]string{
			"session_id":             strings.TrimSpace(m.sessionID),
			"mode":                   string(m.view.Mode()),
			"pending_runtime_events": strconv.Itoa(len(m.pendingRuntimeEvents)),
			"native_flush_sequence":  strconv.FormatUint(m.nativeFlushSequence, 10),
		}))
		return m, cmd
	}
	if result.awaitsHydration {
		return m, cmd
	}
	return m, tea.Batch(m.waitRuntimeEventCmd(), cmd)
}

func (m *uiModel) waitRuntimeEventCmd() tea.Cmd {
	if m == nil {
		return nil
	}
	if m.waitRuntimeEventAfterFlushSequence != 0 || m.waitRuntimeEventAfterHydration {
		m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.wait_runtime_event_blocked", map[string]string{
			"session_id":             strings.TrimSpace(m.sessionID),
			"mode":                   string(m.view.Mode()),
			"pending_runtime_events": strconv.Itoa(len(m.pendingRuntimeEvents)),
			"wait_after_flush":       strconv.FormatUint(m.waitRuntimeEventAfterFlushSequence, 10),
			"wait_after_hydration":   strconv.FormatBool(m.waitRuntimeEventAfterHydration),
		}))
		return nil
	}
	if len(m.pendingRuntimeEvents) == 0 {
		return waitRuntimeEvent(m.runtimeEvents)
	}
	evt := m.pendingRuntimeEvents[0]
	m.pendingRuntimeEvents = append([]clientui.Event(nil), m.pendingRuntimeEvents[1:]...)
	m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.wait_runtime_event_resume_pending", map[string]string{
		"session_id":             strings.TrimSpace(m.sessionID),
		"mode":                   string(m.view.Mode()),
		"kind":                   string(evt.Kind),
		"pending_runtime_events": strconv.Itoa(len(m.pendingRuntimeEvents)),
	}))
	return func() tea.Msg {
		return runtimeEventBatchMsg{events: []clientui.Event{evt}}
	}
}

func (m *uiModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.waitRuntimeEventCmd(),
		waitAskEvent(m.askEvents),
		waitPathReferenceSearchEvent(m.pathReferenceEvents),
		tea.SetWindowTitle(m.windowTitle()),
		tea.WindowSize(),
	}
	if m.runtimeConnectionEvents != nil {
		cmds = append(cmds, waitRuntimeConnectionStateChange(m.runtimeConnectionEvents))
	}
	if m.runtimeLeaseRecoveryWarning != nil {
		cmds = append(cmds, waitRuntimeLeaseRecoveryWarning(m.runtimeLeaseRecoveryWarning))
	}
	cmds = append([]tea.Cmd{tea.ClearScreen}, cmds...)
	if startupText := strings.TrimSpace(m.startupSubmit); startupText != "" {
		cmds = append(cmds, m.inputController().startSubmissionWithPromptHistory(startupText))
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
		m.syncViewport()
		if m.nativeHistoryReplayed && previousWidth > 0 && previousWidth != msg.Width {
			committedEntries := committedTranscriptEntriesForApp(m.transcriptEntries)
			if len(committedEntries) == 0 {
				m.resetNativeHistoryState()
				m.nativeHistoryReplayed = true
			} else {
				m.rebaseNativeProjection(committedTranscriptProjectionForApp(m.view, m.transcriptEntries), m.transcriptBaseOffset, len(committedEntries))
			}
		}
		if !m.nativeHistoryReplayed {
			return m, m.syncNativeHistoryFromTranscript()
		}
		if previousWidth > 0 && previousHeight > 0 && previousWidth != msg.Width && m.view.Mode() == tui.ModeOngoing {
			// Only width changes need a native replay: horizontal resize changes the
			// committed scrollback wrapping, while height-only resize affects only the
			// live viewport. After the width has been quiet for the debounce window,
			// clear and replay ongoing history so emitted lines and dividers match.
			m.nativeResizeReplayToken++
			m.nativeResizeReplayAt = nativeResizeReplayNow().Add(nativeResizeReplayDebounce)
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
		if !m.nativeResizeReplayAt.IsZero() {
			remaining := time.Until(m.nativeResizeReplayAt)
			if now := nativeResizeReplayNow(); !now.IsZero() {
				remaining = m.nativeResizeReplayAt.Sub(now)
			}
			if remaining > 0 {
				token := m.nativeResizeReplayToken
				return m, tea.Tick(remaining, func(time.Time) tea.Msg {
					return nativeResizeReplayMsg{token: token}
				})
			}
		}
		m.nativeResizeReplayAt = time.Time{}
		if replay := m.emitCurrentNativeScrollbackState(true); replay != nil {
			return m, replay
		}
		if !m.nativeRenderedProjection.Empty() {
			return m, nil
		}
		return m, tea.ClearScreen
	case runtimeEventMsg:
		return m.handleRuntimeEventBatch([]clientui.Event{msg.event})
	case runtimeEventBatchMsg:
		if msg.carry != nil {
			m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.runtime_batch_carry", map[string]string{
				"session_id":             strings.TrimSpace(m.sessionID),
				"mode":                   string(m.view.Mode()),
				"kind":                   string(msg.carry.Kind),
				"pending_runtime_events": strconv.Itoa(len(m.pendingRuntimeEvents) + 1),
			}))
			m.pendingRuntimeEvents = append([]clientui.Event{*msg.carry}, m.pendingRuntimeEvents...)
		}
		return m.handleRuntimeEventBatch(msg.events)
	case runtimeConnectionStateChangedMsg:
		m.observeRuntimeRequestResult(msg.err)
		m.syncViewport()
		return m, waitRuntimeConnectionStateChange(m.runtimeConnectionEvents)
	case runtimeLeaseRecoveryWarningMsg:
		cmd := m.appendLocalEntryFallbackWithVisibility("warning", msg.text, msg.visibility)
		m.syncViewport()
		return m, sequenceCmds(cmd, waitRuntimeLeaseRecoveryWarning(m.runtimeLeaseRecoveryWarning))
	case runtimeMainViewRefreshedMsg:
		cmd := m.handleRuntimeMainViewRefreshed(msg)
		m.syncViewport()
		return m, cmd
	case runtimeTranscriptRefreshedMsg:
		cmd := m.handleRuntimeTranscriptRefreshed(msg)
		m.syncViewport()
		return m, cmd
	case runtimeTranscriptRetryMsg:
		if msg.token != m.runtimeTranscriptRetry {
			m.syncViewport()
			return m, nil
		}
		cmd := m.startRuntimeTranscriptPageRequest(m.transcriptRequestForCurrentMode(), false, msg.syncCause, msg.recoveryCause)
		m.syncViewport()
		return m, cmd
	case detailTranscriptLoadMsg:
		cmd := m.requestRuntimeTranscriptPage(m.transcriptRequestForCurrentMode())
		m.syncViewport()
		return m, cmd
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
	case uiPathReferenceCorpusReadyMsg:
		m.handlePathReferenceCorpusReady(msg)
		m.syncViewport()
		return m, waitPathReferenceSearchEvent(m.pathReferenceEvents)
	case uiPathReferenceCorpusFailedMsg:
		m.handlePathReferenceCorpusFailed(msg)
		m.syncViewport()
		return m, waitPathReferenceSearchEvent(m.pathReferenceEvents)
	case uiPathReferenceMatchResultMsg:
		m.handlePathReferenceMatchResult(msg)
		m.syncViewport()
		return m, waitPathReferenceSearchEvent(m.pathReferenceEvents)
	case uiPathReferenceLoadingDelayMsg:
		m.handlePathReferenceLoadingDelay(msg)
		m.syncViewport()
		return m, waitPathReferenceSearchEvent(m.pathReferenceEvents)
	case clearTransientStatusMsg:
		if msg.token == m.transientStatusToken {
			return m, m.advanceTransientStatusQueue()
		}
		m.syncViewport()
		return m, nil
	case startupUpdateNoticeMsg:
		if m.startupUpdateShown {
			m.syncViewport()
			return m, nil
		}
		m.startupUpdateShown = true
		cmd := m.enqueueTransientStatusWithDuration("update available: "+strings.TrimSpace(msg.version), uiStatusNoticeUpdateAvailable, updateNoticeDuration)
		m.syncViewport()
		return m, cmd
	case nativeHistoryFlushMsg:
		return m, m.handleNativeHistoryFlush(msg)
	case promptHistoryPersistErrMsg:
		if msg.err == nil {
			return m, nil
		}
		return m, m.setTransientStatusWithKind("prompt history persistence failed: "+msg.err.Error(), uiStatusNoticeError)
	case submitDoneMsg:
		next, cmd := m.inputController().handleSubmitDone(msg)
		next.(*uiModel).syncViewport()
		return next, cmd
	case preSubmitCompactionCheckDoneMsg:
		next, cmd := m.inputController().handlePreSubmitCompactionCheckDone(msg)
		next.(*uiModel).syncViewport()
		return next, cmd
	case compactDoneMsg:
		next, cmd := m.inputController().handleCompactDone(msg)
		next.(*uiModel).syncViewport()
		return next, cmd
	case spinnerTickMsg:
		next, cmd := m.inputController().handleSpinnerTick(msg)
		next.(*uiModel).syncViewport()
		return next, cmd
	case worktreeListDoneMsg:
		if !m.worktrees.isOpen() || msg.token != m.worktrees.refreshToken {
			m.syncViewport()
			return m, nil
		}
		m.worktrees.loading = false
		if msg.err != nil {
			m.worktrees.errorText = formatSubmissionError(msg.err)
			m.syncViewport()
			return m, m.ensureSpinnerTicking()
		}
		m.worktrees.errorText = ""
		m.applyWorktreeListResponse(msg.resp)
		cmd := m.applyWorktreeIntent()
		m.syncViewport()
		return m, tea.Batch(cmd, m.ensureSpinnerTicking())
	case worktreeCreateDoneMsg:
		if msg.token != m.worktrees.mutationToken {
			m.syncViewport()
			return m, nil
		}
		m.worktrees.create.submitting = false
		if msg.err != nil {
			if !m.worktrees.isOpen() {
				status := formatSubmissionError(msg.err)
				m.syncViewport()
				return m, m.setTransientStatusWithKind(status, uiStatusNoticeError)
			}
			m.worktrees.create.errorText = formatSubmissionError(msg.err)
			m.syncViewport()
			return m, m.ensureSpinnerTicking()
		}
		m.applyExecutionTargetChange(msg.resp.Target)
		var overlayCmd tea.Cmd
		if m.worktrees.isOpen() {
			overlayCmd = m.popWorktreeOverlayIfNeeded()
			m.closeWorktreeOverlay()
		}
		status := "Created worktree " + worktreeDisplayName(msg.resp.Worktree)
		if msg.resp.SetupScheduled {
			status += " and started setup"
		}
		feedbackCmd := m.setTransientStatusWithKind(status, uiStatusNoticeSuccess)
		m.syncViewport()
		return m, tea.Batch(overlayCmd, feedbackCmd, m.requestRuntimeMainViewRefresh(), m.ensureSpinnerTicking())
	case worktreeSwitchDoneMsg:
		if msg.token != m.worktrees.mutationToken {
			m.syncViewport()
			return m, nil
		}
		m.worktrees.switchPending = false
		if msg.err != nil {
			if !m.worktrees.isOpen() {
				status := formatSubmissionError(msg.err)
				m.syncViewport()
				return m, m.setTransientStatusWithKind(status, uiStatusNoticeError)
			}
			m.worktrees.errorText = formatSubmissionError(msg.err)
			m.syncViewport()
			return m, m.ensureSpinnerTicking()
		}
		m.applyExecutionTargetChange(msg.resp.Target)
		var overlayCmd tea.Cmd
		if m.worktrees.isOpen() {
			overlayCmd = m.popWorktreeOverlayIfNeeded()
			m.closeWorktreeOverlay()
		}
		status := "Switched to " + worktreeDisplayName(msg.resp.Worktree)
		feedbackCmd := m.setTransientStatusWithKind(status, uiStatusNoticeSuccess)
		m.syncViewport()
		return m, tea.Batch(overlayCmd, feedbackCmd, m.requestRuntimeMainViewRefresh(), m.ensureSpinnerTicking())
	case worktreeDeleteDoneMsg:
		if msg.token != m.worktrees.mutationToken {
			m.syncViewport()
			return m, nil
		}
		m.worktrees.deleteConfirm.submitting = false
		if msg.err != nil {
			if !m.worktrees.isOpen() {
				status := formatSubmissionError(msg.err)
				m.syncViewport()
				return m, m.setTransientStatusWithKind(status, uiStatusNoticeError)
			}
			m.worktrees.deleteConfirm.errorText = formatSubmissionError(msg.err)
			m.syncViewport()
			return m, m.ensureSpinnerTicking()
		}
		m.applyExecutionTargetChange(msg.resp.Target)
		var listCmd tea.Cmd
		if m.worktrees.isOpen() {
			m.closeWorktreeDialog()
			m.worktrees.selectedID = worktreeCreateRowID
			listCmd = m.requestWorktreeListCmd()
		}
		feedbackCmd := m.setTransientStatusWithKind(worktreeDeleteSuccessStatus(msg.resp), uiStatusNoticeSuccess)
		m.syncViewport()
		return m, tea.Batch(feedbackCmd, listCmd, m.requestRuntimeMainViewRefresh(), m.ensureSpinnerTicking())
	case worktreeCreateTargetResolveDebounceMsg:
		if !m.worktrees.isOpen() || m.worktrees.phase != uiWorktreeOverlayPhaseCreate || msg.token != m.worktrees.create.resolveToken {
			m.syncViewport()
			return m, nil
		}
		query := strings.TrimSpace(m.worktrees.create.branchTarget.Value())
		if query == "" {
			m.worktrees.create.resolving = false
			m.worktrees.create.submitPending = false
			m.worktrees.create.resolution = serverapi.WorktreeCreateTargetResolution{}
			m.worktrees.create.errorText = ""
			m.worktrees.create.syncFocus()
			m.syncViewport()
			return m, nil
		}
		m.syncViewport()
		return m, m.worktreeCreateTargetResolveCmd(query, msg.token)
	case worktreeCreateTargetResolveDoneMsg:
		if !m.worktrees.isOpen() || m.worktrees.phase != uiWorktreeOverlayPhaseCreate || msg.token != m.worktrees.create.resolveToken {
			m.syncViewport()
			return m, nil
		}
		if strings.TrimSpace(m.worktrees.create.branchTarget.Value()) != strings.TrimSpace(msg.query) {
			m.syncViewport()
			return m, nil
		}
		m.worktrees.create.resolving = false
		submitPending := m.worktrees.create.submitPending
		m.worktrees.create.submitPending = false
		if msg.err != nil {
			m.worktrees.create.resolution = serverapi.WorktreeCreateTargetResolution{}
			m.worktrees.create.errorText = formatSubmissionError(msg.err)
			m.worktrees.create.syncFocus()
			m.syncViewport()
			return m, nil
		}
		m.worktrees.create.errorText = ""
		m.worktrees.create.resolution = msg.resp.Resolution
		m.worktrees.create.syncFocus()
		m.syncViewport()
		if submitPending {
			req, err := m.worktrees.create.request(msg.resp.Resolution.Kind)
			if err != nil {
				m.worktrees.create.errorText = err.Error()
				m.syncViewport()
				return m, nil
			}
			return m, m.worktreeCreateCmd(req)
		}
		return m, nil
	case processListRefreshTickMsg:
		if !m.processList.isOpen() {
			m.syncViewport()
			return m, nil
		}
		m.refreshProcessEntries()
		m.syncViewport()
		return m, tea.Batch(waitProcessListRefresh(), m.ensureSpinnerTicking())
	case statusRefreshDoneMsg:
		if msg.token != m.status.refreshToken {
			m.syncViewport()
			return m, nil
		}
		m.status.pendingSections = nil
		m.status.sectionWarnings = nil
		m.status.loading = false
		if msg.err != nil {
			m.status.error = msg.err.Error()
			m.syncViewport()
			return m, m.setTransientStatusWithKind(msg.err.Error(), uiStatusNoticeError)
		}
		m.status.error = ""
		m.status.snapshot = msg.snapshot
		m.syncViewport()
		return m, nil
	case statusBaseRefreshDoneMsg:
		if msg.token != m.status.refreshToken {
			m.syncViewport()
			return m, nil
		}
		m.status.error = ""
		snapshot := msg.snapshot
		if statusHasAuthData(m.status.snapshot) {
			snapshot.Auth = m.status.snapshot.Auth
			snapshot.Subscription = m.status.snapshot.Subscription
		}
		if m.status.snapshot.Git.Visible {
			snapshot.Git = m.status.snapshot.Git
		}
		if m.status.snapshot.Skills != nil {
			snapshot.Skills = m.status.snapshot.Skills
		}
		if m.status.snapshot.SkillTokenCounts != nil {
			snapshot.SkillTokenCounts = m.status.snapshot.SkillTokenCounts
		}
		if m.status.snapshot.AgentsPaths != nil {
			snapshot.AgentsPaths = m.status.snapshot.AgentsPaths
		}
		if m.status.snapshot.AgentTokenCounts != nil {
			snapshot.AgentTokenCounts = m.status.snapshot.AgentTokenCounts
		}
		m.status.snapshot = snapshot
		m.finishStatusSectionRefresh(uiStatusSectionBase, msg.snapshot.CollectorWarning)
		m.syncViewport()
		return m, nil
	case statusAuthRefreshDoneMsg:
		if msg.token != m.status.refreshToken {
			m.syncViewport()
			return m, nil
		}
		m.status.snapshot.Auth = msg.result.Auth
		m.status.snapshot.Subscription = msg.result.Subscription
		if m.statusRepository != nil {
			m.statusRepository.StoreAuth(msg.cacheKey, msg.result, time.Now())
		}
		m.finishStatusSectionRefresh(uiStatusSectionAuth, msg.result.Warning)
		m.syncViewport()
		return m, nil
	case statusGitRefreshDoneMsg:
		if msg.background {
			m.statusGitBackgroundInFlight = false
		}
		if msg.token != m.status.refreshToken {
			m.syncViewport()
			return m, nil
		}
		m.status.snapshot.Git = msg.result.Git
		if m.statusRepository != nil {
			m.statusRepository.StoreGit(msg.cacheKey, msg.result, time.Now())
		}
		m.finishStatusSectionRefresh(uiStatusSectionGit, "")
		m.syncViewport()
		return m, nil
	case statusEnvironmentRefreshDoneMsg:
		if msg.token != m.status.refreshToken {
			m.syncViewport()
			return m, nil
		}
		m.status.snapshot.Skills = msg.result.Skills
		m.status.snapshot.SkillTokenCounts = msg.result.SkillTokenCounts
		m.status.snapshot.AgentsPaths = msg.result.AgentsPaths
		m.status.snapshot.AgentTokenCounts = msg.result.AgentTokenCounts
		if m.statusRepository != nil {
			m.statusRepository.StoreEnvironment(msg.cacheKey, msg.result, time.Now())
		}
		m.finishStatusSectionRefresh(uiStatusSectionEnvironment, msg.result.CollectorWarning)
		m.syncViewport()
		return m, nil
	case openProcessLogsDoneMsg:
		m.syncViewport()
		if msg.err != nil {
			return m, m.setTransientStatusWithKind(msg.err.Error(), uiStatusNoticeError)
		}
		return m, nil
	case clipboardImagePasteDoneMsg:
		cmd := m.handleClipboardImagePasteDone(msg)
		m.syncViewport()
		return m, cmd
	case clipboardTextCopyDoneMsg:
		cmd := m.handleClipboardTextCopyDone(msg)
		m.syncViewport()
		return m, cmd
	}

	m.forwardToView(msg)
	m.syncViewport()
	return m, m.maybeRequestDetailTranscriptPage()
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

func statusHasAuthData(snapshot uiStatusSnapshot) bool {
	return snapshot.Auth.Visible || snapshot.Subscription.Applicable || strings.TrimSpace(snapshot.Subscription.Summary) != "" || len(snapshot.Subscription.Windows) > 0
}

func (m *uiModel) forwardToView(msg tea.Msg) {
	prevMode := m.view.Mode()
	next, _ := m.view.Update(msg)
	casted, ok := next.(tui.Model)
	if ok {
		m.view = casted
	}
	if prevMode != m.view.Mode() && m.view.Mode() == tui.ModeDetail && m.hasRuntimeClient() {
		m.primeDetailTranscriptFromCurrentTail()
		page := m.detailTranscript.page()
		nextDetail, _ := m.view.Update(tui.SetConversationMsg{
			BaseOffset:   page.Offset,
			TotalEntries: page.TotalEntries,
			Entries:      transcriptEntriesFromPage(page),
			Ongoing:      page.Ongoing,
			OngoingError: page.OngoingError,
		})
		if castedDetail, ok := nextDetail.(tui.Model); ok {
			m.view = castedDetail
		}
	}
}

func (m *uiModel) Action() UIAction {
	return m.exitAction
}

func (m *uiModel) Close() {
	if m == nil || m.pathReferenceSearch == nil {
		return
	}
	m.pathReferenceSearch.Stop()
	m.pathReferenceSearch = nil
	m.pathReferenceEvents = nil
}

func (m *uiModel) Transition() UITransition {
	return UITransition{
		Action:                   m.exitAction,
		InitialPrompt:            m.nextSessionInitialPrompt,
		InitialInput:             m.nextSessionInitialInput,
		TargetSessionID:          strings.TrimSpace(m.nextSessionID),
		ForkUserMessageIndex:     m.nextForkUserMessageIndex,
		ForkTranscriptEntryIndex: m.nextForkTranscriptEntryIndex,
		ParentSessionID:          strings.TrimSpace(m.nextParentSessionID),
	}
}

func (m *uiModel) windowTitle() string {
	return sessionTitle(m.sessionName)
}

func (m *uiModel) logf(format string, args ...any) {
	if m.logger != nil {
		m.logger.Logf(format, args...)
	}
}

func (m *uiModel) logTranscriptDiag(line string) {
	if m == nil || !m.transcriptDiagnosticsEnabled() {
		return
	}
	m.logf("%s", strings.TrimSpace(line))
}

func (m *uiModel) transcriptDiagnosticsEnabled() bool {
	if m == nil {
		return false
	}
	return m.transcriptDiagnostics || transcriptdiag.EnabledForProcess(m.debugMode)
}

func (m *uiModel) updateTranscriptDiagnosticsMode() {
	if m == nil {
		return
	}
	if configurable, ok := m.engine.(interface{ SetTranscriptDiagnosticsEnabled(bool) }); ok {
		configurable.SetTranscriptDiagnosticsEnabled(m.transcriptDiagnosticsEnabled())
	}
}

func (m *uiModel) inputController() uiInputController {
	return uiInputController{model: m}
}

func worktreeDeleteSuccessStatus(resp serverapi.WorktreeDeleteResponse) string {
	status := "Deleted worktree " + worktreeDisplayName(resp.Worktree)
	if cleanup := strings.TrimSpace(resp.BranchCleanupMessage); cleanup != "" {
		status += ". " + cleanup
	}
	return status
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
	return m.sendTransientStatus(message, kind, transientStatusDuration, uiStatusNoticeReplace)
}

func (m *uiModel) enqueueTransientStatus(message string, kind uiStatusNoticeKind) tea.Cmd {
	return m.sendTransientStatus(message, kind, transientStatusDuration, uiStatusNoticeQueue)
}

func (m *uiModel) enqueueTransientStatusWithDuration(message string, kind uiStatusNoticeKind, duration time.Duration) tea.Cmd {
	return m.sendTransientStatus(message, kind, duration, uiStatusNoticeQueue)
}

func (m *uiModel) sendTransientStatus(message string, kind uiStatusNoticeKind, duration time.Duration, delivery uiStatusNoticeDelivery) tea.Cmd {
	if strings.TrimSpace(message) == "" {
		return nil
	}
	notice := uiStatusNotice{Text: strings.TrimSpace(message), Kind: kind, Duration: duration}
	if delivery == uiStatusNoticeQueue && strings.TrimSpace(m.transientStatus) != "" {
		if m.transientStatus == notice.Text && m.transientStatusKind == notice.Kind {
			return nil
		}
		if len(m.transientStatusQueue) > 0 {
			last := m.transientStatusQueue[len(m.transientStatusQueue)-1]
			if last == notice {
				return nil
			}
		}
		m.transientStatusQueue = append(m.transientStatusQueue, notice)
		return nil
	}
	return m.showTransientStatusNotice(notice)
}

func (m *uiModel) showTransientStatusNotice(notice uiStatusNotice) tea.Cmd {
	m.transientStatusToken++
	token := m.transientStatusToken
	m.transientStatus = strings.TrimSpace(notice.Text)
	m.transientStatusKind = notice.Kind
	return scheduleTransientStatusClear(notice.Duration, token)
}

func (m *uiModel) advanceTransientStatusQueue() tea.Cmd {
	m.transientStatus = ""
	m.transientStatusKind = uiStatusNoticeNeutral
	if len(m.transientStatusQueue) == 0 {
		m.syncViewport()
		return nil
	}
	next := m.transientStatusQueue[0]
	m.transientStatusQueue = append([]uiStatusNotice(nil), m.transientStatusQueue[1:]...)
	cmd := m.showTransientStatusNotice(next)
	m.syncViewport()
	return cmd
}

func (m *uiModel) startupUpdateNoticeCmd(status clientui.UpdateStatus) tea.Cmd {
	if status.Available && strings.TrimSpace(status.LatestVersion) != "" {
		return func() tea.Msg {
			return startupUpdateNoticeMsg{version: status.LatestVersion}
		}
	}
	if status.Checked {
		return nil
	}
	client := m.runtimeClient()
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		refreshed, err := client.RefreshMainView()
		if err != nil || !refreshed.Status.Update.Available || strings.TrimSpace(refreshed.Status.Update.LatestVersion) == "" {
			return nil
		}
		return startupUpdateNoticeMsg{version: refreshed.Status.Update.LatestVersion}
	}
}

func batchCmds(cmds ...tea.Cmd) tea.Cmd {
	filtered := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			filtered = append(filtered, cmd)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return tea.Batch(filtered...)
}

func (m *uiModel) layout() uiViewLayout {
	return uiViewLayout{model: m}
}
