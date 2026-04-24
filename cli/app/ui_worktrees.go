package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"builder/cli/tui"
	"builder/shared/clientui"
	"builder/shared/serverapi"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"
)

const (
	worktreeOverlayHeaderLines    = 3
	worktreeOverlayFooterLines    = 1
	worktreeOverlayRowLines       = 3
	worktreeOverlayRailGlyph      = "│"
	worktreeCreateRowID           = "__create__"
	worktreeOverlayMaxErrorLines  = 4
	worktreeCreateResolveDebounce = 150 * time.Millisecond
)

type uiWorktreeOverlayPhase string

const (
	uiWorktreeOverlayPhaseList          uiWorktreeOverlayPhase = "list"
	uiWorktreeOverlayPhaseCreate        uiWorktreeOverlayPhase = "create"
	uiWorktreeOverlayPhaseDeleteConfirm uiWorktreeOverlayPhase = "delete_confirm"
)

type uiWorktreeOpenIntent struct {
	OpenCreate          bool
	OpenDelete          bool
	ConfirmDeleteTarget string
	PreferDeleteBranch  bool
}

type uiWorktreeCreateField uint8

const (
	uiWorktreeCreateFieldBranchTarget uiWorktreeCreateField = iota
	uiWorktreeCreateFieldBaseRef
	uiWorktreeCreateFieldActions
)

type uiWorktreeCreateAction uint8

const (
	uiWorktreeCreateActionCreate uiWorktreeCreateAction = iota
	uiWorktreeCreateActionCancel
)

type uiWorktreeCreateDialogState struct {
	baseRef       textinput.Model
	branchTarget  textinput.Model
	focus         uiWorktreeCreateField
	action        uiWorktreeCreateAction
	errorText     string
	submitting    bool
	resolving     bool
	submitPending bool
	resolveToken  uint64
	resolution    serverapi.WorktreeCreateTargetResolution
}

type uiWorktreeDeleteAction uint8

const (
	uiWorktreeDeleteActionCancel uiWorktreeDeleteAction = iota
	uiWorktreeDeleteActionDelete
	uiWorktreeDeleteActionDeleteBranch
)

type uiWorktreeDeleteDialogState struct {
	target             serverapi.WorktreeView
	selectedAction     uiWorktreeDeleteAction
	preferDeleteBranch bool
	errorText          string
	submitting         bool
}

type uiWorktreeOverlayState struct {
	open               bool
	ownsTranscriptMode bool
	loading            bool
	phase              uiWorktreeOverlayPhase
	selection          int
	target             clientui.SessionExecutionTarget
	entries            []serverapi.WorktreeView
	errorText          string
	refreshToken       uint64
	mutationToken      uint64
	switchPending      bool
	selectedID         string
	intent             uiWorktreeOpenIntent
	create             uiWorktreeCreateDialogState
	deleteConfirm      uiWorktreeDeleteDialogState
}

type worktreeListDoneMsg struct {
	token uint64
	resp  serverapi.WorktreeListResponse
	err   error
}

type worktreeCreateDoneMsg struct {
	token uint64
	resp  serverapi.WorktreeCreateResponse
	err   error
}

type worktreeSwitchDoneMsg struct {
	token uint64
	resp  serverapi.WorktreeSwitchResponse
	err   error
}

type worktreeDeleteDoneMsg struct {
	token uint64
	resp  serverapi.WorktreeDeleteResponse
	err   error
}

type worktreeCreateTargetResolveDebounceMsg struct {
	token uint64
}

type worktreeCreateTargetResolveDoneMsg struct {
	token uint64
	query string
	resp  serverapi.WorktreeCreateTargetResolveResponse
	err   error
}

func newWorktreeDialogTextInput(value string) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.SetValue(strings.TrimSpace(value))
	input.Cursor.Style = lipgloss.NewStyle()
	input.TextStyle = lipgloss.NewStyle()
	input.PlaceholderStyle = lipgloss.NewStyle()
	return input
}

func newWorktreeCreateDialog(suggestedBranch string) uiWorktreeCreateDialogState {
	dialog := uiWorktreeCreateDialogState{
		baseRef:      newWorktreeDialogTextInput("HEAD"),
		branchTarget: newWorktreeDialogTextInput(strings.TrimSpace(suggestedBranch)),
		focus:        uiWorktreeCreateFieldBranchTarget,
		action:       uiWorktreeCreateActionCreate,
	}
	dialog.syncFocus()
	return dialog
}

func (d *uiWorktreeCreateDialogState) syncFocus() {
	if d == nil {
		return
	}
	if !d.usesBaseRef() && d.focus == uiWorktreeCreateFieldBaseRef {
		d.focus = uiWorktreeCreateFieldBranchTarget
	}
	d.baseRef.Blur()
	d.branchTarget.Blur()
	switch d.focus {
	case uiWorktreeCreateFieldBaseRef:
		d.baseRef.Focus()
	case uiWorktreeCreateFieldBranchTarget:
		d.branchTarget.Focus()
	}
}

func (d uiWorktreeCreateDialogState) orderedFields() []uiWorktreeCreateField {
	fields := []uiWorktreeCreateField{uiWorktreeCreateFieldBranchTarget}
	if d.usesBaseRef() {
		fields = append(fields, uiWorktreeCreateFieldBaseRef)
	}
	fields = append(fields, uiWorktreeCreateFieldActions)
	return fields
}

func (d uiWorktreeCreateDialogState) usesBaseRef() bool {
	return d.resolution.Kind == serverapi.WorktreeCreateTargetResolutionKindNewBranch
}

func (d *uiWorktreeCreateDialogState) moveFocus(delta int) {
	if d == nil {
		return
	}
	fields := d.orderedFields()
	index := 0
	for idx, field := range fields {
		if field == d.focus {
			index = idx
			break
		}
	}
	index += delta
	if index < 0 {
		index = 0
	}
	if index >= len(fields) {
		index = len(fields) - 1
	}
	d.focus = fields[index]
	d.syncFocus()
	if d.focus == uiWorktreeCreateFieldBranchTarget {
		d.branchTarget.CursorEnd()
	}
	if d.focus == uiWorktreeCreateFieldBaseRef {
		d.baseRef.CursorEnd()
	}
}

func (d *uiWorktreeCreateDialogState) moveAction(delta int) {
	if d == nil {
		return
	}
	index := int(d.action) + delta
	if index < int(uiWorktreeCreateActionCreate) {
		index = int(uiWorktreeCreateActionCreate)
	}
	if index > int(uiWorktreeCreateActionCancel) {
		index = int(uiWorktreeCreateActionCancel)
	}
	d.action = uiWorktreeCreateAction(index)
}

func (d uiWorktreeCreateDialogState) request(kind serverapi.WorktreeCreateTargetResolutionKind) (serverapi.WorktreeCreateRequest, error) {
	target := strings.TrimSpace(d.branchTarget.Value())
	if target == "" {
		return serverapi.WorktreeCreateRequest{}, fmt.Errorf("Branch or ref is required")
	}
	if kind == serverapi.WorktreeCreateTargetResolutionKindExistingBranch || kind == serverapi.WorktreeCreateTargetResolutionKindDetachedRef {
		return serverapi.WorktreeCreateRequest{BaseRef: target, CreateBranch: false}, nil
	}
	baseRef := strings.TrimSpace(d.baseRef.Value())
	if baseRef == "" {
		baseRef = "HEAD"
	}
	return serverapi.WorktreeCreateRequest{BaseRef: baseRef, CreateBranch: true, BranchName: target}, nil
}

func (d uiWorktreeDeleteDialogState) availableActions() []uiWorktreeDeleteAction {
	actions := []uiWorktreeDeleteAction{uiWorktreeDeleteActionCancel, uiWorktreeDeleteActionDelete}
	if worktreeDeleteCanAutoDeleteBranch(d.target) || strings.TrimSpace(d.target.BranchName) != "" {
		actions = append(actions, uiWorktreeDeleteActionDeleteBranch)
	}
	return actions
}

func (d *uiWorktreeDeleteDialogState) clampSelection() {
	if d == nil {
		return
	}
	actions := d.availableActions()
	if d.selectedAction != uiWorktreeDeleteActionCancel {
		for _, action := range actions {
			if action == d.selectedAction {
				return
			}
		}
	}
	if d.preferDeleteBranch {
		for _, action := range actions {
			if action == uiWorktreeDeleteActionDeleteBranch {
				d.selectedAction = action
				return
			}
		}
	}
	d.selectedAction = uiWorktreeDeleteActionDelete
	if len(actions) > 0 && actions[0] == uiWorktreeDeleteActionCancel && len(actions) == 1 {
		d.selectedAction = uiWorktreeDeleteActionCancel
	}
}

func (d *uiWorktreeDeleteDialogState) moveSelection(delta int) {
	if d == nil {
		return
	}
	actions := d.availableActions()
	if len(actions) == 0 {
		return
	}
	index := 0
	for idx, action := range actions {
		if action == d.selectedAction {
			index = idx
			break
		}
	}
	index += delta
	if index < 0 {
		index = 0
	}
	if index >= len(actions) {
		index = len(actions) - 1
	}
	d.selectedAction = actions[index]
}

func (s uiWorktreeOverlayState) isOpen() bool {
	return s.open
}

func (s uiWorktreeOverlayState) visibleErrorText() string {
	if !s.open {
		return ""
	}
	switch s.phase {
	case uiWorktreeOverlayPhaseCreate:
		return strings.TrimSpace(s.create.errorText)
	case uiWorktreeOverlayPhaseDeleteConfirm:
		return strings.TrimSpace(s.deleteConfirm.errorText)
	default:
		return strings.TrimSpace(s.errorText)
	}
}

func (m *uiModel) openWorktreeOverlay(intent uiWorktreeOpenIntent) {
	if m == nil {
		return
	}
	m.worktrees.open = true
	m.worktrees.phase = uiWorktreeOverlayPhaseList
	m.worktrees.loading = true
	m.worktrees.errorText = ""
	m.worktrees.intent = intent
	m.worktrees.create = uiWorktreeCreateDialogState{}
	m.worktrees.deleteConfirm = uiWorktreeDeleteDialogState{}
	m.setInputMode(uiInputModeWorktree)
	if len(m.worktrees.entries) == 0 {
		m.worktrees.selection = 0
	}
}

func (m *uiModel) closeWorktreeOverlay() {
	if m == nil {
		return
	}
	if m.worktrees.switchPending {
		return
	}
	m.worktrees = uiWorktreeOverlayState{}
	m.restorePrimaryInputMode()
}

func (m *uiModel) pushWorktreeOverlayIfNeeded() tea.Cmd {
	if m.worktrees.ownsTranscriptMode {
		return nil
	}
	if m.view.Mode() != tui.ModeOngoing {
		return nil
	}
	m.worktrees.ownsTranscriptMode = true
	if transitionCmd := m.transitionTranscriptMode(tui.ModeDetail, true, true); transitionCmd != nil {
		return transitionCmd
	}
	return tea.ClearScreen
}

func (m *uiModel) popWorktreeOverlayIfNeeded() tea.Cmd {
	if !m.worktrees.ownsTranscriptMode {
		return nil
	}
	m.worktrees.ownsTranscriptMode = false
	if m.view.Mode() != tui.ModeDetail {
		return nil
	}
	if transitionCmd := m.transitionTranscriptMode(tui.ModeOngoing, false, true); transitionCmd != nil {
		return transitionCmd
	}
	return tea.ClearScreen
}

func (m *uiModel) requestWorktreeListCmd() tea.Cmd {
	if m == nil {
		return nil
	}
	m.worktrees.refreshToken++
	token := m.worktrees.refreshToken
	m.worktrees.loading = true
	m.worktrees.errorText = ""
	return func() tea.Msg {
		resp, err := m.listWorktreesForCurrentSession()
		return worktreeListDoneMsg{token: token, resp: resp, err: err}
	}
}

func (m *uiModel) openCreateWorktreeDialog() tea.Cmd {
	if m == nil {
		return nil
	}
	m.worktrees.phase = uiWorktreeOverlayPhaseCreate
	m.worktrees.errorText = ""
	m.worktrees.create = newWorktreeCreateDialog(m.suggestedWorktreeBranchFromEntries())
	return m.scheduleWorktreeCreateTargetResolution()
}

func (m *uiModel) openDeleteWorktreeDialog(target serverapi.WorktreeView, preferDeleteBranch bool) {
	if m == nil {
		return
	}
	m.worktrees.phase = uiWorktreeOverlayPhaseDeleteConfirm
	m.worktrees.errorText = ""
	m.worktrees.deleteConfirm = uiWorktreeDeleteDialogState{target: target, preferDeleteBranch: preferDeleteBranch}
	m.worktrees.deleteConfirm.clampSelection()
}

func (m *uiModel) closeWorktreeDialog() {
	if m == nil {
		return
	}
	m.worktrees.phase = uiWorktreeOverlayPhaseList
	m.worktrees.create = uiWorktreeCreateDialogState{}
	m.worktrees.deleteConfirm = uiWorktreeDeleteDialogState{}
	m.worktrees.errorText = ""
}

func (m *uiModel) scheduleWorktreeCreateTargetResolution() tea.Cmd {
	if m == nil || !m.worktrees.isOpen() || m.worktrees.phase != uiWorktreeOverlayPhaseCreate {
		return nil
	}
	dialog := &m.worktrees.create
	query := strings.TrimSpace(dialog.branchTarget.Value())
	dialog.errorText = ""
	dialog.resolveToken++
	token := dialog.resolveToken
	dialog.resolution = serverapi.WorktreeCreateTargetResolution{}
	dialog.resolving = query != ""
	dialog.submitPending = false
	dialog.syncFocus()
	if query == "" {
		return nil
	}
	return tea.Tick(worktreeCreateResolveDebounce, func(time.Time) tea.Msg {
		return worktreeCreateTargetResolveDebounceMsg{token: token}
	})
}

func (m *uiModel) worktreeCreateTargetResolveCmd(query string, token uint64) tea.Cmd {
	if m == nil || m.worktreeClient == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := m.worktreeResolveContext()
		defer cancel()
		resp, err := m.worktreeClient.ResolveWorktreeCreateTarget(ctx, serverapi.WorktreeCreateTargetResolveRequest{SessionID: m.sessionID, Target: query})
		return worktreeCreateTargetResolveDoneMsg{token: token, query: query, resp: resp, err: err}
	}
}

func (m *uiModel) worktreeResolveContext() (context.Context, context.CancelFunc) {
	if m != nil {
		if client, ok := m.runtimeClient().(*sessionRuntimeClient); ok && client != nil {
			return client.controlContext()
		}
	}
	return context.WithTimeout(context.Background(), uiRuntimeControlTimeout)
}

func (m *uiModel) worktreeRowCount() int {
	if m == nil {
		return 1
	}
	return len(m.worktrees.entries) + 1
}

func (m *uiModel) clampWorktreeSelection() {
	if m == nil {
		return
	}
	rowCount := m.worktreeRowCount()
	if rowCount <= 0 {
		m.worktrees.selection = 0
		return
	}
	if m.worktrees.selection < 0 {
		m.worktrees.selection = 0
	}
	if m.worktrees.selection >= rowCount {
		m.worktrees.selection = rowCount - 1
	}
}

func (m *uiModel) moveWorktreeSelection(delta int) {
	if m == nil {
		return
	}
	m.worktrees.selection += delta
	m.clampWorktreeSelection()
}

func (m *uiModel) moveWorktreeSelectionPage(deltaPages int) {
	rows := m.worktreeRowsPerPage()
	m.moveWorktreeSelection(rows * deltaPages)
}

func (m *uiModel) worktreeRowsPerPage() int {
	available := m.termHeight - 1 - worktreeOverlayHeaderLines - worktreeOverlayFooterLines
	if available < worktreeOverlayRowLines {
		return 1
	}
	rows := available / worktreeOverlayRowLines
	if rows < 1 {
		return 1
	}
	return rows
}

func (m *uiModel) selectFirstWorktreeRow() {
	if m == nil {
		return
	}
	m.worktrees.selection = 0
}

func (m *uiModel) selectLastWorktreeRow() {
	if m == nil {
		return
	}
	m.worktrees.selection = max(0, m.worktreeRowCount()-1)
}

func (m *uiModel) selectedWorktreeRow() (serverapi.WorktreeView, bool) {
	if m == nil || m.worktrees.selection <= 0 {
		return serverapi.WorktreeView{}, false
	}
	index := m.worktrees.selection - 1
	if index < 0 || index >= len(m.worktrees.entries) {
		return serverapi.WorktreeView{}, false
	}
	return m.worktrees.entries[index], true
}

func (m *uiModel) selectedWorktreeID() string {
	if item, ok := m.selectedWorktreeRow(); ok {
		return strings.TrimSpace(item.WorktreeID)
	}
	return worktreeCreateRowID
}

func (m *uiModel) recordWorktreeSelection() {
	if m == nil {
		return
	}
	m.worktrees.selectedID = m.selectedWorktreeID()
}

func (m *uiModel) restoreWorktreeSelection() {
	if m == nil {
		return
	}
	selectedID := strings.TrimSpace(m.worktrees.selectedID)
	if selectedID == "" || selectedID == worktreeCreateRowID {
		m.worktrees.selection = 0
		return
	}
	for idx, item := range m.worktrees.entries {
		if strings.TrimSpace(item.WorktreeID) == selectedID {
			m.worktrees.selection = idx + 1
			return
		}
	}
	if len(m.worktrees.entries) == 0 {
		m.worktrees.selection = 0
		return
	}
	m.clampWorktreeSelection()
}

func (m *uiModel) applyWorktreeListResponse(resp serverapi.WorktreeListResponse) {
	if m == nil {
		return
	}
	m.recordWorktreeSelection()
	m.worktrees.target = resp.Target
	m.worktrees.entries = append([]serverapi.WorktreeView(nil), resp.Worktrees...)
	m.restoreWorktreeSelection()
	m.clampWorktreeSelection()
	if m.worktrees.phase == uiWorktreeOverlayPhaseDeleteConfirm {
		targetID := strings.TrimSpace(m.worktrees.deleteConfirm.target.WorktreeID)
		if targetID == "" {
			m.closeWorktreeDialog()
			return
		}
		for _, item := range m.worktrees.entries {
			if strings.TrimSpace(item.WorktreeID) == targetID {
				m.worktrees.deleteConfirm.target = item
				m.worktrees.deleteConfirm.clampSelection()
				return
			}
		}
		m.closeWorktreeDialog()
	}
}

func (m *uiModel) applyWorktreeIntent() tea.Cmd {
	if m == nil {
		return nil
	}
	intent := m.worktrees.intent
	m.worktrees.intent = uiWorktreeOpenIntent{}
	if intent.OpenCreate {
		return m.openCreateWorktreeDialog()
	}
	if !intent.OpenDelete {
		return nil
	}
	target, err := resolveWorktreeDeletionTargetFromEntries(m.worktrees.entries, intent.ConfirmDeleteTarget)
	if err != nil {
		m.worktrees.errorText = formatSubmissionError(err)
		return nil
	}
	m.recordWorktreeSelection()
	for idx, item := range m.worktrees.entries {
		if strings.TrimSpace(item.WorktreeID) == strings.TrimSpace(target.WorktreeID) {
			m.worktrees.selection = idx + 1
			break
		}
	}
	m.openDeleteWorktreeDialog(target, intent.PreferDeleteBranch)
	return nil
}

func resolveWorktreeDeletionTargetFromEntries(entries []serverapi.WorktreeView, token string) (serverapi.WorktreeView, error) {
	trimmedToken := strings.TrimSpace(token)
	if trimmedToken != "" {
		return resolveWorktreeTokenFromEntries(entries, trimmedToken)
	}
	for _, item := range entries {
		if item.IsCurrent {
			if item.IsMain {
				return serverapi.WorktreeView{}, fmt.Errorf("main workspace is not deletable; choose another worktree")
			}
			return item, nil
		}
	}
	return serverapi.WorktreeView{}, serverapi.ErrWorktreeNotFound
}

func resolveWorktreeTokenFromEntries(entries []serverapi.WorktreeView, token string) (serverapi.WorktreeView, error) {
	trimmedToken := strings.TrimSpace(token)
	if trimmedToken == "" {
		return serverapi.WorktreeView{}, serverapi.ErrWorktreeNotFound
	}
	matchers := []func(serverapi.WorktreeView, string) bool{
		func(item serverapi.WorktreeView, token string) bool {
			return strings.TrimSpace(item.WorktreeID) == token
		},
		func(item serverapi.WorktreeView, token string) bool {
			return strings.TrimSpace(item.CanonicalRoot) == token
		},
		func(item serverapi.WorktreeView, token string) bool {
			return strings.TrimSpace(item.DisplayName) == token
		},
		func(item serverapi.WorktreeView, token string) bool {
			return strings.TrimSpace(item.BranchName) == token || (token == "main" && item.IsMain)
		},
	}
	for _, matcher := range matchers {
		uniqueMatches := make(map[string]serverapi.WorktreeView, len(entries))
		orderedKeys := make([]string, 0, len(entries))
		for _, item := range entries {
			if !matcher(item, trimmedToken) {
				continue
			}
			key := resolvedWorktreeTokenMatchKey(item)
			if _, ok := uniqueMatches[key]; ok {
				continue
			}
			uniqueMatches[key] = item
			orderedKeys = append(orderedKeys, key)
		}
		if len(orderedKeys) == 1 {
			return uniqueMatches[orderedKeys[0]], nil
		}
		if len(orderedKeys) > 1 {
			names := make([]string, 0, len(orderedKeys))
			for _, key := range orderedKeys {
				names = append(names, worktreeDisplayName(uniqueMatches[key]))
			}
			return serverapi.WorktreeView{}, fmt.Errorf("worktree %q is ambiguous: %s", trimmedToken, strings.Join(names, ", "))
		}
	}
	return serverapi.WorktreeView{}, fmt.Errorf("worktree %q not found", trimmedToken)
}

func resolvedWorktreeTokenMatchKey(item serverapi.WorktreeView) string {
	if trimmed := strings.TrimSpace(item.WorktreeID); trimmed != "" {
		return "id:" + trimmed
	}
	if trimmed := strings.TrimSpace(item.CanonicalRoot); trimmed != "" {
		return "root:" + trimmed
	}
	return "name:" + worktreeDisplayName(item)
}

func (m *uiModel) suggestedWorktreeBranchFromEntries() string {
	if m == nil {
		return ""
	}
	if sessionBranch := sanitizeWorktreeBranchSuggestion(m.suggestedWorktreeSessionName()); sessionBranch != "" {
		return sessionBranch
	}
	return ""
}

func (m *uiModel) worktreeCreateCmd(req serverapi.WorktreeCreateRequest) tea.Cmd {
	if m == nil {
		return nil
	}
	m.worktrees.mutationToken++
	token := m.worktrees.mutationToken
	m.worktrees.create.errorText = ""
	m.worktrees.create.submitting = true
	return func() tea.Msg {
		resp, err := runWorktreeMutation(m, func(ctx context.Context, leaseID string) (serverapi.WorktreeCreateResponse, error) {
			req.ClientRequestID = uuid.NewString()
			req.SessionID = m.sessionID
			req.ControllerLeaseID = leaseID
			return m.worktreeClient.CreateWorktree(ctx, req)
		})
		return worktreeCreateDoneMsg{token: token, resp: resp, err: err}
	}
}

func (m *uiModel) worktreeSwitchCmd(target serverapi.WorktreeView) tea.Cmd {
	if m == nil {
		return nil
	}
	m.worktrees.mutationToken++
	m.worktrees.switchPending = true
	token := m.worktrees.mutationToken
	m.worktrees.errorText = ""
	return func() tea.Msg {
		resp, err := runWorktreeMutation(m, func(ctx context.Context, leaseID string) (serverapi.WorktreeSwitchResponse, error) {
			return m.worktreeClient.SwitchWorktree(ctx, serverapi.WorktreeSwitchRequest{
				ClientRequestID:   uuid.NewString(),
				SessionID:         m.sessionID,
				ControllerLeaseID: leaseID,
				WorktreeID:        target.WorktreeID,
			})
		})
		return worktreeSwitchDoneMsg{token: token, resp: resp, err: err}
	}
}

func (m *uiModel) worktreeDeleteCmd(target serverapi.WorktreeView, deleteBranch bool) tea.Cmd {
	if m == nil {
		return nil
	}
	m.worktrees.mutationToken++
	token := m.worktrees.mutationToken
	m.worktrees.deleteConfirm.errorText = ""
	m.worktrees.deleteConfirm.submitting = true
	return func() tea.Msg {
		resp, err := runWorktreeMutation(m, func(ctx context.Context, leaseID string) (serverapi.WorktreeDeleteResponse, error) {
			return m.worktreeClient.DeleteWorktree(ctx, serverapi.WorktreeDeleteRequest{
				ClientRequestID:   uuid.NewString(),
				SessionID:         m.sessionID,
				ControllerLeaseID: leaseID,
				WorktreeID:        target.WorktreeID,
				DeleteBranch:      deleteBranch,
			})
		})
		return worktreeDeleteDoneMsg{token: token, resp: resp, err: err}
	}
}

func (c uiInputController) startWorktreeOverlayCmd(intent uiWorktreeOpenIntent) tea.Cmd {
	m := c.model
	m.openWorktreeOverlay(intent)
	refreshCmd := m.requestWorktreeListCmd()
	spinnerCmd := m.ensureSpinnerTicking()
	if overlayCmd := m.pushWorktreeOverlayIfNeeded(); overlayCmd != nil {
		return tea.Batch(overlayCmd, refreshCmd, spinnerCmd)
	}
	return tea.Batch(refreshCmd, spinnerCmd)
}

func (c uiInputController) stopWorktreeOverlayCmd() tea.Cmd {
	m := c.model
	if m.worktrees.switchPending {
		return nil
	}
	overlayCmd := m.popWorktreeOverlayIfNeeded()
	m.closeWorktreeOverlay()
	spinnerCmd := m.ensureSpinnerTicking()
	if overlayCmd != nil {
		return tea.Batch(overlayCmd, spinnerCmd)
	}
	return spinnerCmd
}

func (c uiInputController) handleWorktreeOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := c.model
	if m.worktrees.phase == uiWorktreeOverlayPhaseCreate {
		return c.handleWorktreeCreateDialogKey(msg)
	}
	if m.worktrees.phase == uiWorktreeOverlayPhaseDeleteConfirm {
		return c.handleWorktreeDeleteDialogKey(msg)
	}
	switch strings.ToLower(msg.String()) {
	case "ctrl+c":
		if m.busy {
			_ = m.interruptRuntime()
			m.preSubmitCheckToken++
			c.releaseLockedInjectedInput(true)
			c.restorePendingInjectedIntoInput()
			c.restoreQueuedMessagesIntoInput()
			m.pendingPreSubmitText = ""
			m.busy = false
			m.activity = uiActivityInterrupted
			m.clearReviewerState()
			return m, nil
		}
		m.exitAction = UIActionExit
		if overlayCmd := m.popWorktreeOverlayIfNeeded(); overlayCmd != nil {
			m.closeWorktreeOverlay()
			return m, tea.Sequence(overlayCmd, tea.Quit)
		}
		return m, tea.Quit
	case "esc", "q":
		return m, c.stopWorktreeOverlayCmd()
	case "up", "k":
		m.moveWorktreeSelection(-1)
		return m, nil
	case "down", "j":
		m.moveWorktreeSelection(1)
		return m, nil
	case "pgup":
		m.moveWorktreeSelectionPage(-1)
		return m, nil
	case "pgdown":
		m.moveWorktreeSelectionPage(1)
		return m, nil
	case "home":
		m.selectFirstWorktreeRow()
		return m, nil
	case "end":
		m.selectLastWorktreeRow()
		return m, nil
	case "r":
		return m, tea.Batch(m.requestWorktreeListCmd(), m.ensureSpinnerTicking())
	case "c", "n":
		return m, m.openCreateWorktreeDialog()
	case "d":
		target, ok := m.selectedWorktreeRow()
		if !ok {
			return m, c.showErrorStatus("Select a worktree to delete")
		}
		if target.IsMain {
			return m, c.showErrorStatus("Main workspace is not deletable")
		}
		m.openDeleteWorktreeDialog(target, false)
		return m, nil
	case "x":
		target, ok := m.selectedWorktreeRow()
		if !ok {
			return m, c.showErrorStatus("Select a worktree to delete")
		}
		if target.IsMain {
			return m, c.showErrorStatus("Main workspace is not deletable")
		}
		m.openDeleteWorktreeDialog(target, true)
		return m, nil
	case "enter":
		if m.worktrees.selection == 0 {
			return m, m.openCreateWorktreeDialog()
		}
		target, ok := m.selectedWorktreeRow()
		if !ok {
			return m, nil
		}
		if target.IsCurrent {
			return m, c.showTransientStatus("Already current worktree")
		}
		return m, m.worktreeSwitchCmd(target)
	default:
		return m, nil
	}
}

func (c uiInputController) handleWorktreeCreateDialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := c.model
	dialog := &m.worktrees.create
	if dialog.submitting {
		return m, nil
	}
	switch strings.ToLower(msg.String()) {
	case "esc":
		m.closeWorktreeDialog()
		return m, nil
	case "tab", "down":
		dialog.moveFocus(1)
		return m, nil
	case "shift+tab", "up":
		dialog.moveFocus(-1)
		return m, nil
	case "left", "h":
		if dialog.focus == uiWorktreeCreateFieldActions {
			dialog.moveAction(-1)
			return m, nil
		}
	case "right", "l":
		if dialog.focus == uiWorktreeCreateFieldActions {
			dialog.moveAction(1)
			return m, nil
		}
	case "enter":
		switch dialog.focus {
		case uiWorktreeCreateFieldActions:
			if dialog.action == uiWorktreeCreateActionCancel {
				m.closeWorktreeDialog()
				return m, nil
			}
			query := strings.TrimSpace(dialog.branchTarget.Value())
			if query == "" {
				dialog.errorText = "Branch or ref is required"
				return m, nil
			}
			dialog.errorText = ""
			dialog.resolution = serverapi.WorktreeCreateTargetResolution{}
			dialog.resolving = true
			dialog.submitPending = true
			dialog.resolveToken++
			dialog.syncFocus()
			return m, m.worktreeCreateTargetResolveCmd(query, dialog.resolveToken)
		default:
			dialog.moveFocus(1)
			return m, nil
		}
	}
	var cmd tea.Cmd
	var resolveCmd tea.Cmd
	switch dialog.focus {
	case uiWorktreeCreateFieldBaseRef:
		dialog.baseRef, cmd = dialog.baseRef.Update(msg)
	case uiWorktreeCreateFieldBranchTarget:
		before := dialog.branchTarget.Value()
		dialog.branchTarget, cmd = dialog.branchTarget.Update(msg)
		if dialog.branchTarget.Value() != before {
			resolveCmd = m.scheduleWorktreeCreateTargetResolution()
		}
	default:
		return m, nil
	}
	if resolveCmd != nil {
		return m, tea.Batch(cmd, resolveCmd)
	}
	if dialog.focus == uiWorktreeCreateFieldBaseRef {
		dialog.errorText = ""
	}
	return m, cmd
}

func (c uiInputController) handleWorktreeDeleteDialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := c.model
	dialog := &m.worktrees.deleteConfirm
	if dialog.submitting {
		return m, nil
	}
	switch strings.ToLower(msg.String()) {
	case "esc":
		m.closeWorktreeDialog()
		return m, nil
	case "tab", "right", "l":
		dialog.moveSelection(1)
		return m, nil
	case "shift+tab", "left", "h":
		dialog.moveSelection(-1)
		return m, nil
	case "enter":
		switch dialog.selectedAction {
		case uiWorktreeDeleteActionCancel:
			m.closeWorktreeDialog()
			return m, nil
		case uiWorktreeDeleteActionDelete:
			return m, m.worktreeDeleteCmd(dialog.target, false)
		case uiWorktreeDeleteActionDeleteBranch:
			return m, m.worktreeDeleteCmd(dialog.target, true)
		}
	}
	return m, nil
}

func (l uiViewLayout) renderWorktreeOverlay(width, height int, style uiStyles) []string {
	m := l.model
	if m.worktrees.phase == uiWorktreeOverlayPhaseCreate {
		return l.renderWorktreeCreateDialog(width, height, style)
	}
	if m.worktrees.phase == uiWorktreeOverlayPhaseDeleteConfirm {
		return l.renderWorktreeDeleteDialog(width, height, style)
	}
	return l.renderWorktreeList(width, height, style)
}

func (l uiViewLayout) renderWorktreeList(width, height int, style uiStyles) []string {
	m := l.model
	if height < 1 {
		return []string{padRight("", width)}
	}
	header := []string{
		style.brand.Render(truncateQueuedMessageLine("Worktrees", width)),
		style.meta.Render(truncateQueuedMessageLine(worktreeOverlaySummary(m.worktrees.target), width)),
		"",
	}
	remainingHeight := height - len(header) - worktreeOverlayFooterLines
	if remainingHeight < 0 {
		remainingHeight = 0
	}
	content := make([]string, 0, remainingHeight)
	if remainingHeight > 0 {
		switch {
		case m.worktrees.loading:
			content = append(content, style.meta.Render(pendingToolSpinnerFrame(m.spinnerFrame)+" Loading worktrees..."))
		case strings.TrimSpace(m.worktrees.errorText) != "":
			content = append(content, renderWorktreeErrorLines(m.worktrees.errorText, width, lipgloss.NewStyle().Foreground(statusRedColor()).Bold(true), worktreeOverlayMaxErrorLines)...)
		case len(m.worktrees.entries) == 0:
			content = append(content, style.meta.Render("No worktrees."))
		default:
			rows := make([]string, 0, (len(m.worktrees.entries)+1)*worktreeOverlayRowLines)
			rows = append(rows, renderWorktreeCreateRow(m.worktrees.selection == 0, width, m.theme, style)...)
			for idx, item := range m.worktrees.entries {
				rows = append(rows, renderWorktreeEntry(item, idx+1 == m.worktrees.selection, width, m.theme, style)...)
			}
			start := worktreeOverlayStartRow(m.worktrees.selection, m.worktreeRowCount(), remainingHeight)
			end := start + remainingHeight
			if end > len(rows) {
				end = len(rows)
			}
			content = append(content, rows[start:end]...)
		}
		for len(content) < remainingHeight {
			content = append(content, "")
		}
	}
	footer := []string{style.meta.Render(truncateQueuedMessageLine("Esc/q close | Enter switch | c create | d delete | x delete+branch | PgUp/PgDn/Home/End move | r refresh", width))}
	lines := append(append(header, content...), footer...)
	return l.renderChatContentLines(lines, nil, width, style)
}

func worktreeOverlaySummary(target clientui.SessionExecutionTarget) string {
	current := strings.TrimSpace(target.EffectiveWorkdir)
	if current == "" {
		current = strings.TrimSpace(target.WorkspaceRoot)
	}
	if current == "" {
		current = "unknown"
	}
	return "Current workdir: " + current
}

func renderWorktreeCreateRow(selected bool, width int, theme string, style uiStyles) []string {
	p := uiPalette(theme)
	line := lipgloss.NewStyle().Foreground(p.foreground)
	if selected {
		line = line.Background(p.modeBg)
	}
	titleStyle := line.Foreground(p.primary).Bold(true)
	railStyle := line.Foreground(p.primary).Bold(true)
	rail := " "
	sep := ""
	if selected {
		rail = worktreeOverlayRailGlyph
		sep = worktreeOverlayRailGlyph
	}
	parts1 := []string{railStyle.Render(rail), line.Render(" "), titleStyle.Render("Create worktree")}
	parts2 := []string{railStyle.Render(rail), line.Render(" ")}
	return []string{
		worktreeOverlayPadLine(parts1, width, line),
		worktreeOverlayPadLine(parts2, width, line),
		worktreeOverlayPadLine([]string{railStyle.Render(sep)}, width, line),
	}
}

func renderWorktreeEntry(item serverapi.WorktreeView, selected bool, width int, theme string, style uiStyles) []string {
	p := uiPalette(theme)
	line := lipgloss.NewStyle().Foreground(p.foreground)
	if selected {
		line = line.Background(p.modeBg)
	}
	titleStyle := line.Bold(true)
	railStyle := line.Foreground(p.primary).Bold(true)
	metaStyle := line.Foreground(p.muted).Faint(true)
	rail := " "
	sep := ""
	if selected {
		rail = worktreeOverlayRailGlyph
		sep = worktreeOverlayRailGlyph
	}
	title := truncateQueuedMessageLine(worktreeDisplayName(item), max(1, width-2))
	badges := renderWorktreeBadges(item, selected, theme)
	line1 := worktreeOverlayComposeTitleLine(railStyle.Render(rail), title, titleStyle, badges, width, line)
	path := metaStyle.Render(truncateQueuedMessageLine(strings.TrimSpace(item.CanonicalRoot), max(1, width-2)))
	line2 := worktreeOverlayPadLine([]string{railStyle.Render(rail), line.Render(" "), path}, width, line)
	return []string{
		line1,
		line2,
		worktreeOverlayPadLine([]string{railStyle.Render(sep)}, width, line),
	}
}

func renderWorktreeBadges(item serverapi.WorktreeView, selected bool, theme string) []string {
	p := uiPalette(theme)
	badges := make([]string, 0, 4)
	base := lipgloss.NewStyle()
	if selected {
		base = base.Background(p.modeBg)
	}
	badge := func(text string, fg lipgloss.TerminalColor) string {
		return base.Foreground(fg).Bold(true).Render("[" + text + "]")
	}
	if item.IsCurrent {
		badges = append(badges, badge("current", p.secondary))
	}
	if item.IsMain {
		badges = append(badges, badge("main", p.primary))
	}
	if item.Detached {
		badges = append(badges, badge("detached", statusAmberColor()))
	} else if branch := strings.TrimSpace(item.BranchName); branch != "" {
		badges = append(badges, badge("branch:"+branch, p.foreground))
	}
	if !item.BuilderManaged && !item.IsMain {
		badges = append(badges, badge("external", p.muted))
	}
	return badges
}

func worktreeOverlayComposeTitleLine(rail string, title string, titleStyle lipgloss.Style, badges []string, width int, fill lipgloss.Style) string {
	prefix := rail + " "
	available := max(1, width-lipgloss.Width(prefix))
	badgeWidth := 0
	hasBadges := len(badges) > 0
	if len(badges) > 0 {
		badgeWidth = 1
		for index, badge := range badges {
			badgeWidth += lipgloss.Width(badge)
			if index > 0 {
				badgeWidth += 1
			}
		}
	}
	maxTitleWidth := available - badgeWidth
	if maxTitleWidth < 1 {
		maxTitleWidth = available
		hasBadges = false
	}
	parts := []string{rail, fill.Render(" "), titleStyle.Render(truncateQueuedMessageLine(title, maxTitleWidth))}
	if hasBadges {
		parts = append(parts, fill.Render(" "))
		for index, badge := range badges {
			if index > 0 {
				parts = append(parts, fill.Render(" "))
			}
			parts = append(parts, badge)
		}
	}
	return worktreeOverlayPadLine(parts, width, fill)
}

func worktreeOverlayPadLine(parts []string, width int, fill lipgloss.Style) string {
	line := strings.Join(parts, "")
	remaining := width - lipgloss.Width(line)
	if remaining <= 0 {
		return line
	}
	return line + fill.Render(strings.Repeat(" ", remaining))
}

func worktreeOverlayStartRow(selection, rowCount, contentHeight int) int {
	if selection < 0 || rowCount <= 0 || contentHeight <= 0 {
		return 0
	}
	visibleRows := contentHeight / worktreeOverlayRowLines
	if visibleRows < 1 {
		visibleRows = 1
	}
	startRow := 0
	if selection >= visibleRows {
		startRow = selection - visibleRows + 1
	}
	if startRow >= rowCount {
		startRow = rowCount - 1
	}
	if startRow < 0 {
		startRow = 0
	}
	return startRow * worktreeOverlayRowLines
}

func (l uiViewLayout) renderWorktreeCreateDialog(width, height int, style uiStyles) []string {
	m := l.model
	dialog := m.worktrees.create
	body := make([]string, 0, 32)
	focusedStart := 0
	focusedEnd := -1
	addSection := func(field uiWorktreeCreateField, focusable bool, lines []string) {
		if len(lines) == 0 {
			return
		}
		if len(body) > 0 {
			separator := ""
			if focusable && dialog.focus == field {
				separator = lipgloss.NewStyle().Background(uiPalette(m.theme).modeBg).Render(strings.Repeat(" ", width))
			}
			body = append(body, separator)
		}
		start := len(body)
		body = append(body, lines...)
		if focusable && dialog.focus == field {
			focusedStart = start
			focusedEnd = len(body) - 1
		}
	}
	addSection(uiWorktreeCreateFieldBranchTarget, false, []string{
		style.brand.Render(truncateQueuedMessageLine("New worktree", width)),
	})
	addSection(uiWorktreeCreateFieldBranchTarget, true, l.renderWorktreeCreateTargetField(width, dialog))
	addSection(uiWorktreeCreateFieldBaseRef, false, l.renderWorktreeCreateField(width, style, "Base ref", "Used when creating a new branch.", dialog.baseRef.Value(), dialog.baseRef.Position(), dialog.focus == uiWorktreeCreateFieldBaseRef, dialog.usesBaseRef()))
	addSection(uiWorktreeCreateFieldActions, true, renderWorktreeCreateActionGroup(width, m.theme, dialog, dialog.focus == uiWorktreeCreateFieldActions))
	footer := make([]string, 0, 3)
	if dialog.submitting {
		footer = append(footer, style.meta.Render(truncateQueuedMessageLine(pendingToolSpinnerFrame(m.spinnerFrame)+" Creating worktree...", width)))
	}
	if trimmed := strings.TrimSpace(dialog.errorText); trimmed != "" {
		footer = append(footer, renderWorktreeErrorLines(trimmed, width, lipgloss.NewStyle().Foreground(statusRedColor()).Bold(true), worktreeOverlayMaxErrorLines)...)
	}
	footer = append(footer, style.meta.Render(truncateQueuedMessageLine("Esc back | Up/Down move | Left/Right change option | Enter activate", width)))
	if len(footer) > height {
		footer = footer[len(footer)-height:]
	}
	bodyHeight := height - len(footer)
	if bodyHeight < 0 {
		bodyHeight = 0
	}
	if focusedEnd < focusedStart {
		focusedStart = 0
		focusedEnd = 0
	}
	visibleStart := worktreeDialogVisibleStart(len(body), bodyHeight, focusedStart, focusedEnd)
	visibleEnd := visibleStart + bodyHeight
	if visibleEnd > len(body) {
		visibleEnd = len(body)
	}
	lines := append([]string{}, body[visibleStart:visibleEnd]...)
	for len(lines) < bodyHeight {
		lines = append(lines, padRight("", width))
	}
	lines = append(lines, footer...)
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, padANSIRight(line, width))
	}
	return out
}

func (l uiViewLayout) renderWorktreeCreateTargetField(width int, dialog uiWorktreeCreateDialogState) []string {
	p := uiPalette(l.model.theme)
	rowStyle := lipgloss.NewStyle()
	if dialog.focus == uiWorktreeCreateFieldBranchTarget {
		rowStyle = rowStyle.Background(p.modeBg)
	}
	labelStyle := rowStyle.Foreground(p.primary).Bold(true)
	if dialog.focus != uiWorktreeCreateFieldBranchTarget {
		labelStyle = rowStyle.Foreground(p.foreground)
	}
	badgeStyle := rowStyle.Foreground(p.muted).Faint(true)
	badgeText := ""
	switch {
	case strings.TrimSpace(dialog.branchTarget.Value()) == "":
		badgeText = ""
	case dialog.resolving:
		badgeText = ""
	case dialog.resolution.Kind == serverapi.WorktreeCreateTargetResolutionKindNewBranch:
		badgeStyle = rowStyle.Foreground(p.secondary).Bold(true)
		badgeText = "✔︎ new branch"
	case dialog.resolution.Kind == serverapi.WorktreeCreateTargetResolutionKindExistingBranch:
		badgeStyle = rowStyle.Foreground(statusAmberColor()).Bold(true)
		badgeText = "∴ existing branch"
	case dialog.resolution.Kind == serverapi.WorktreeCreateTargetResolutionKindDetachedRef:
		badgeStyle = rowStyle.Foreground(statusAmberColor()).Bold(true)
		badgeText = "∴ detached ref"
	}
	lineStyle := rowStyle.Foreground(p.foreground)
	borderStyle := rowStyle.Foreground(p.muted).Faint(true)
	spec := uiEditableInputRenderSpec{Prefix: "› ", Text: dialog.branchTarget.Value(), CursorIndex: dialog.branchTarget.Position(), RenderCursor: dialog.focus == uiWorktreeCreateFieldBranchTarget}
	lines := []string{labelStyle.Render(padANSIRight("Branch or ref", width))}
	lines = append(lines, badgeStyle.Render(padANSIRight(truncateQueuedMessageLine(badgeText, width), width)))
	lines = append(lines, renderFramedEditableInputLines(max(1, width), 1, spec, lineStyle, borderStyle)...)
	return lines
}

func (l uiViewLayout) renderWorktreeCreateField(width int, style uiStyles, label string, helper string, value string, cursor int, focused bool, enabled bool) []string {
	p := uiPalette(l.model.theme)
	rowStyle := lipgloss.NewStyle()
	if focused {
		rowStyle = rowStyle.Background(p.modeBg)
	}
	labelStyle := rowStyle.Foreground(p.primary).Bold(true)
	if !focused {
		labelStyle = rowStyle.Foreground(p.foreground)
	}
	if !enabled {
		labelStyle = rowStyle.Foreground(p.muted).Faint(true)
	}
	lineStyle := rowStyle.Foreground(p.foreground)
	borderStyle := rowStyle.Foreground(p.muted).Faint(true)
	if !enabled {
		lineStyle = rowStyle.Foreground(p.muted).Faint(true)
	}
	spec := uiEditableInputRenderSpec{Prefix: "› ", Text: value, CursorIndex: cursor, RenderCursor: focused && enabled}
	contentWidth := max(1, width)
	lines := []string{labelStyle.Render(padANSIRight(truncateQueuedMessageLine(label, contentWidth), contentWidth))}
	if strings.TrimSpace(helper) != "" {
		helperStyle := rowStyle.Foreground(p.muted).Faint(true)
		lines = append(lines, helperStyle.Render(padANSIRight(truncateQueuedMessageLine(helper, contentWidth), contentWidth)))
	}
	lines = append(lines, renderFramedEditableInputLines(contentWidth, 1, spec, lineStyle, borderStyle)...)
	return lines
}

func (l uiViewLayout) renderWorktreeDeleteDialog(width, height int, style uiStyles) []string {
	m := l.model
	dialog := m.worktrees.deleteConfirm
	lines := []string{
		style.brand.Render(truncateQueuedMessageLine("Delete "+worktreeDisplayName(dialog.target)+"?", width)),
		"",
	}
	body := worktreeDeletePreviewLines(dialog)
	for _, line := range body {
		lineStyle := style.chat
		if line.kind == worktreeDeletePreviewLineKindHeader {
			lineStyle = lineStyle.Bold(true)
		}
		lines = append(lines, lineStyle.Render(truncateQueuedMessageLine(line.text, width)))
	}
	lines = append(lines, "", renderWorktreeDeleteButtons(width, l.model.theme, dialog))
	if dialog.submitting {
		lines = append(lines, "", style.meta.Render(pendingToolSpinnerFrame(m.spinnerFrame)+" Deleting worktree..."))
	}
	if trimmed := strings.TrimSpace(dialog.errorText); trimmed != "" {
		lines = append(lines, "")
		lines = append(lines, renderWorktreeErrorLines(trimmed, width, lipgloss.NewStyle().Foreground(statusRedColor()).Bold(true), worktreeOverlayMaxErrorLines)...)
	}
	return l.renderWorktreeDialogLines(lines, width, height, style)
}

func renderWorktreeErrorLines(text string, width int, lineStyle lipgloss.Style, maxLines int) []string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || width < 1 || maxLines < 1 {
		return nil
	}
	wrapped := make([]string, 0, maxLines)
	for _, line := range splitPlainLines(strings.TrimRight(trimmed, "\n")) {
		parts := wrapLine(line, width)
		if len(parts) == 0 {
			parts = []string{""}
		}
		wrapped = append(wrapped, parts...)
	}
	if len(wrapped) > maxLines {
		wrapped = append([]string(nil), wrapped[:maxLines]...)
		wrapped[len(wrapped)-1] = appendOverflowEllipsis(wrapped[len(wrapped)-1], width)
	}
	out := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		out = append(out, lineStyle.Render(padANSIRight(line, width)))
	}
	return out
}

func appendOverflowEllipsis(line string, width int) string {
	if width < 1 {
		return ""
	}
	if width == 1 {
		return "…"
	}
	trimmed := truncateQueuedMessageLine(line, width-1)
	if strings.HasSuffix(trimmed, "…") {
		trimmed = strings.TrimSuffix(trimmed, "…")
	}
	return trimmed + "…"
}

type worktreeDeletePreviewLineKind uint8

const (
	worktreeDeletePreviewLineKindHeader worktreeDeletePreviewLineKind = iota
	worktreeDeletePreviewLineKindBullet
)

type worktreeDeletePreviewLine struct {
	kind worktreeDeletePreviewLineKind
	text string
}

func worktreeDeletePreviewLines(dialog uiWorktreeDeleteDialogState) []worktreeDeletePreviewLine {
	target := dialog.target
	items := make([]worktreeDeletePreviewLine, 0, 4)
	if worktreeDeleteWillDeleteBranch(dialog) {
		items = append(items, worktreeDeletePreviewLine{kind: worktreeDeletePreviewLineKindBullet, text: "• Local branch " + strings.TrimSpace(target.BranchName)})
	}
	if root := strings.TrimSpace(target.CanonicalRoot); root != "" {
		items = append(items, worktreeDeletePreviewLine{kind: worktreeDeletePreviewLineKindBullet, text: "• Workspace folder at " + root})
	}
	items = append(items, worktreeDeletePreviewLine{kind: worktreeDeletePreviewLineKindBullet, text: "• Git worktree " + worktreeDisplayName(target)})
	if len(items) == 0 {
		return nil
	}
	return append([]worktreeDeletePreviewLine{{kind: worktreeDeletePreviewLineKindHeader, text: "Will delete:"}}, items...)
}

func worktreeDeleteWillDeleteBranch(dialog uiWorktreeDeleteDialogState) bool {
	if strings.TrimSpace(dialog.target.BranchName) == "" {
		return false
	}
	return dialog.selectedAction == uiWorktreeDeleteActionDeleteBranch
}

func renderWorktreeDeleteButtons(width int, theme string, dialog uiWorktreeDeleteDialogState) string {
	actions := dialog.availableActions()
	options := make([]uiChoiceOption, 0, len(actions))
	selectedIndex := 0
	for _, action := range actions {
		label := ""
		switch action {
		case uiWorktreeDeleteActionCancel:
			label = "Cancel"
		case uiWorktreeDeleteActionDelete:
			label = "Delete"
		case uiWorktreeDeleteActionDeleteBranch:
			label = "Delete + Branch"
		}
		if action == dialog.selectedAction {
			selectedIndex = len(options)
		}
		options = append(options, uiChoiceOption{Label: label})
	}
	return renderUIChoiceGroupLine(width, theme, uiChoiceGroupKindButton, options, selectedIndex)
}

func renderWorktreeCreateActionGroup(width int, theme string, dialog uiWorktreeCreateDialogState, focused bool) []string {
	p := uiPalette(theme)
	rowStyle := lipgloss.NewStyle()
	if focused {
		rowStyle = rowStyle.Background(p.modeBg)
	}
	selectedStyle := rowStyle.Foreground(p.primary).Bold(true)
	defaultStyle := rowStyle.Foreground(p.muted).Faint(true)
	return []string{renderUIChoiceGroupLineStyled(width, uiChoiceGroupKindButton, []uiChoiceOption{{Label: "Create"}, {Label: "Cancel"}}, int(dialog.action), selectedStyle, defaultStyle)}
}

func worktreeDialogVisibleStart(totalLines int, viewportHeight int, focusedStart int, focusedEnd int) int {
	if viewportHeight <= 0 || totalLines <= viewportHeight {
		return 0
	}
	if focusedStart < 0 {
		focusedStart = 0
	}
	if focusedEnd < focusedStart {
		focusedEnd = focusedStart
	}
	start := focusedStart
	maxStart := totalLines - viewportHeight
	if focusedEnd-focusedStart+1 >= viewportHeight {
		if start > maxStart {
			start = maxStart
		}
		if start < 0 {
			start = 0
		}
		return start
	}
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	return start
}

func (l uiViewLayout) renderWorktreeDialogLines(lines []string, width int, height int, style uiStyles) []string {
	if height < 1 {
		return []string{padRight("", width)}
	}
	if len(lines) < height {
		for len(lines) < height {
			lines = append(lines, "")
		}
	} else if len(lines) > height {
		lines = lines[:height]
	}
	return l.renderChatContentLines(lines, nil, width, style)
}
