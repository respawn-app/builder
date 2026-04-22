package app

import (
	"context"
	"fmt"
	"strings"

	"builder/cli/tui"
	"builder/shared/clientui"
	"builder/shared/serverapi"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"
)

const (
	worktreeOverlayHeaderLines = 3
	worktreeOverlayFooterLines = 1
	worktreeOverlayRowLines    = 3
	worktreeOverlayRailGlyph   = "│"
	worktreeCreateRowID        = "__create__"
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
	uiWorktreeCreateFieldBaseRef uiWorktreeCreateField = iota
	uiWorktreeCreateFieldBranchMode
	uiWorktreeCreateFieldBranchTarget
	uiWorktreeCreateFieldPath
	uiWorktreeCreateFieldSubmit
	uiWorktreeCreateFieldCancel
)

type uiWorktreeCreateDialogState struct {
	baseRef      textinput.Model
	branchTarget textinput.Model
	path         textinput.Model
	createBranch bool
	focus        uiWorktreeCreateField
	errorText    string
	submitting   bool
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
		path:         newWorktreeDialogTextInput(""),
		createBranch: true,
		focus:        uiWorktreeCreateFieldBaseRef,
	}
	dialog.syncFocus()
	return dialog
}

func (d *uiWorktreeCreateDialogState) syncFocus() {
	if d == nil {
		return
	}
	d.baseRef.Blur()
	d.branchTarget.Blur()
	d.path.Blur()
	switch d.focus {
	case uiWorktreeCreateFieldBaseRef:
		d.baseRef.Focus()
	case uiWorktreeCreateFieldBranchTarget:
		d.branchTarget.Focus()
	case uiWorktreeCreateFieldPath:
		d.path.Focus()
	}
}

func (d uiWorktreeCreateDialogState) orderedFields() []uiWorktreeCreateField {
	fields := []uiWorktreeCreateField{uiWorktreeCreateFieldBranchMode}
	if d.createBranch {
		fields = append(fields, uiWorktreeCreateFieldBaseRef)
	}
	fields = append(fields, uiWorktreeCreateFieldBranchTarget, uiWorktreeCreateFieldPath, uiWorktreeCreateFieldSubmit, uiWorktreeCreateFieldCancel)
	return fields
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
	if d.focus == uiWorktreeCreateFieldBaseRef && !d.createBranch {
		d.moveFocus(delta)
	}
	if d.focus == uiWorktreeCreateFieldBranchTarget {
		d.branchTarget.CursorEnd()
	}
	if d.focus == uiWorktreeCreateFieldPath {
		d.path.CursorEnd()
	}
	if d.focus == uiWorktreeCreateFieldBaseRef {
		d.baseRef.CursorEnd()
	}
}

func (d *uiWorktreeCreateDialogState) toggleBranchMode(next bool) {
	if d == nil {
		return
	}
	if d.createBranch == next {
		return
	}
	d.createBranch = next
	if !d.createBranch && d.focus == uiWorktreeCreateFieldBaseRef {
		d.focus = uiWorktreeCreateFieldBranchTarget
	}
	d.syncFocus()
	d.errorText = ""
	if !d.createBranch && strings.TrimSpace(d.branchTarget.Value()) == "" {
		d.branchTarget.SetValue(strings.TrimSpace(d.baseRef.Value()))
	}
}

func (d uiWorktreeCreateDialogState) request() (serverapi.WorktreeCreateRequest, error) {
	branchTarget := strings.TrimSpace(d.branchTarget.Value())
	rootPath := strings.TrimSpace(d.path.Value())
	if d.createBranch {
		if branchTarget == "" {
			return serverapi.WorktreeCreateRequest{}, fmt.Errorf("Branch name is required")
		}
		baseRef := strings.TrimSpace(d.baseRef.Value())
		if baseRef == "" {
			baseRef = "HEAD"
		}
		return serverapi.WorktreeCreateRequest{BaseRef: baseRef, CreateBranch: true, BranchName: branchTarget, RootPath: rootPath}, nil
	}
	if branchTarget == "" {
		return serverapi.WorktreeCreateRequest{}, fmt.Errorf("Existing branch/ref is required")
	}
	return serverapi.WorktreeCreateRequest{BaseRef: branchTarget, CreateBranch: false, RootPath: rootPath}, nil
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

func (m *uiModel) openCreateWorktreeDialog() {
	if m == nil {
		return
	}
	m.worktrees.phase = uiWorktreeOverlayPhaseCreate
	m.worktrees.errorText = ""
	m.worktrees.create = newWorktreeCreateDialog(m.suggestedWorktreeBranchFromEntries())
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
		m.openCreateWorktreeDialog()
		return nil
	}
	if !intent.OpenDelete {
		return nil
	}
	target, err := resolveWorktreeDeletionTargetFromEntries(m.worktrees.entries, intent.ConfirmDeleteTarget)
	if err != nil {
		m.worktrees.errorText = formatSubmissionError(err)
		return m.setTransientStatusWithKind(m.worktrees.errorText, uiStatusNoticeError)
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
		matches := make([]serverapi.WorktreeView, 0, 4)
		for _, item := range entries {
			if matcher(item, trimmedToken) {
				matches = append(matches, item)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			names := make([]string, 0, len(matches))
			for _, item := range matches {
				names = append(names, worktreeDisplayName(item))
			}
			return serverapi.WorktreeView{}, fmt.Errorf("worktree %q is ambiguous: %s", trimmedToken, strings.Join(names, ", "))
		}
	}
	return serverapi.WorktreeView{}, fmt.Errorf("worktree %q not found", trimmedToken)
}

func (m *uiModel) suggestedWorktreeBranchFromEntries() string {
	if m == nil {
		return "worktree"
	}
	if sessionBranch := sanitizeWorktreeBranchSuggestion(m.suggestedWorktreeSessionName()); sessionBranch != "" {
		return sessionBranch
	}
	for _, item := range m.worktrees.entries {
		if item.IsCurrent {
			if branch := strings.TrimSpace(item.BranchName); branch != "" {
				return branch
			}
		}
	}
	for _, item := range m.worktrees.entries {
		if item.IsMain {
			if branch := strings.TrimSpace(item.BranchName); branch != "" {
				return branch
			}
		}
	}
	return "worktree"
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
		resp, err := runWorktreeMutation(m, func(leaseID string) (serverapi.WorktreeCreateResponse, error) {
			req.ClientRequestID = uuid.NewString()
			req.SessionID = m.sessionID
			req.ControllerLeaseID = leaseID
			return m.worktreeClient.CreateWorktree(context.Background(), req)
		})
		return worktreeCreateDoneMsg{token: token, resp: resp, err: err}
	}
}

func (m *uiModel) worktreeSwitchCmd(target serverapi.WorktreeView) tea.Cmd {
	if m == nil {
		return nil
	}
	m.worktrees.mutationToken++
	token := m.worktrees.mutationToken
	m.worktrees.errorText = ""
	return func() tea.Msg {
		resp, err := runWorktreeMutation(m, func(leaseID string) (serverapi.WorktreeSwitchResponse, error) {
			return m.worktreeClient.SwitchWorktree(context.Background(), serverapi.WorktreeSwitchRequest{
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
		resp, err := runWorktreeMutation(m, func(leaseID string) (serverapi.WorktreeDeleteResponse, error) {
			return m.worktreeClient.DeleteWorktree(context.Background(), serverapi.WorktreeDeleteRequest{
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
		m.openCreateWorktreeDialog()
		return m, nil
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
			m.openCreateWorktreeDialog()
			return m, nil
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
		if dialog.focus == uiWorktreeCreateFieldBranchMode {
			dialog.toggleBranchMode(true)
			return m, nil
		}
		if dialog.focus == uiWorktreeCreateFieldCancel {
			dialog.focus = uiWorktreeCreateFieldSubmit
			dialog.syncFocus()
			return m, nil
		}
	case "right", "l":
		if dialog.focus == uiWorktreeCreateFieldBranchMode {
			dialog.toggleBranchMode(false)
			return m, nil
		}
		if dialog.focus == uiWorktreeCreateFieldSubmit {
			dialog.focus = uiWorktreeCreateFieldCancel
			dialog.syncFocus()
			return m, nil
		}
	case "enter":
		switch dialog.focus {
		case uiWorktreeCreateFieldSubmit:
			req, err := dialog.request()
			if err != nil {
				dialog.errorText = err.Error()
				return m, c.showErrorStatus(dialog.errorText)
			}
			return m, m.worktreeCreateCmd(req)
		case uiWorktreeCreateFieldCancel:
			m.closeWorktreeDialog()
			return m, nil
		case uiWorktreeCreateFieldBranchMode:
			dialog.toggleBranchMode(!dialog.createBranch)
			return m, nil
		default:
			dialog.moveFocus(1)
			return m, nil
		}
	}
	var cmd tea.Cmd
	switch dialog.focus {
	case uiWorktreeCreateFieldBaseRef:
		dialog.baseRef, cmd = dialog.baseRef.Update(msg)
	case uiWorktreeCreateFieldBranchTarget:
		dialog.branchTarget, cmd = dialog.branchTarget.Update(msg)
	case uiWorktreeCreateFieldPath:
		dialog.path, cmd = dialog.path.Update(msg)
	default:
		return m, nil
	}
	dialog.errorText = ""
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
			content = append(content, lipgloss.NewStyle().Foreground(statusRedColor()).Bold(true).Render(truncateQueuedMessageLine(m.worktrees.errorText, width)))
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
	line := lipgloss.NewStyle().Foreground(p.primary)
	if selected {
		line = line.Bold(true)
	}
	rail := " "
	sep := ""
	if selected {
		rail = worktreeOverlayRailGlyph
		sep = worktreeOverlayRailGlyph
	}
	parts1 := []string{line.Render(rail), line.Render(" "), line.Render("Create worktree")}
	parts2 := []string{line.Render(rail), style.meta.Render(" " + truncateQueuedMessageLine("Open create form", max(1, width-2)))}
	return []string{
		worktreeOverlayPadLine(parts1, width, line),
		worktreeOverlayPadLine(parts2, width, line),
		worktreeOverlayPadLine([]string{line.Render(sep)}, width, line),
	}
}

func renderWorktreeEntry(item serverapi.WorktreeView, selected bool, width int, theme string, style uiStyles) []string {
	p := uiPalette(theme)
	line := lipgloss.NewStyle().Foreground(p.foreground)
	if selected {
		line = line.Background(p.modeBg)
	}
	titleStyle := line.Copy().Bold(true)
	railStyle := line.Copy().Foreground(p.primary).Bold(true)
	metaStyle := line.Copy().Foreground(p.muted).Faint(true)
	rail := " "
	sep := ""
	if selected {
		rail = worktreeOverlayRailGlyph
		sep = worktreeOverlayRailGlyph
	}
	title := truncateQueuedMessageLine(worktreeDisplayName(item), max(1, width-2))
	badges := renderWorktreeBadges(item, theme)
	line1 := worktreeOverlayComposeTitleLine(railStyle.Render(rail), title, titleStyle, badges, width, line)
	path := style.meta.Render(truncateQueuedMessageLine(strings.TrimSpace(item.CanonicalRoot), max(1, width-2)))
	line2 := worktreeOverlayPadLine([]string{railStyle.Render(rail), metaStyle.Render(" "), path}, width, line)
	return []string{
		line1,
		line2,
		worktreeOverlayPadLine([]string{railStyle.Render(sep)}, width, line),
	}
}

func renderWorktreeBadges(item serverapi.WorktreeView, theme string) []string {
	p := uiPalette(theme)
	badges := make([]string, 0, 4)
	badge := func(text string, fg lipgloss.TerminalColor) string {
		return lipgloss.NewStyle().Foreground(fg).Bold(true).Render("[" + text + "]")
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
	badgeText := ""
	if len(badges) > 0 {
		badgeText = strings.Join(badges, " ")
		badgeWidth = lipgloss.Width(badgeText) + 1
	}
	maxTitleWidth := available - badgeWidth
	if maxTitleWidth < 1 {
		maxTitleWidth = available
		badgeText = ""
	}
	parts := []string{rail, fill.Render(" "), titleStyle.Render(truncateQueuedMessageLine(title, maxTitleWidth))}
	if badgeText != "" {
		parts = append(parts, fill.Render(" "), badgeText)
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
	header := []string{
		style.brand.Render(truncateQueuedMessageLine("Worktrees", width)),
		style.meta.Render(truncateQueuedMessageLine("Create worktree", width)),
		"",
	}
	lines := append([]string{}, header...)
	lines = append(lines, l.renderWorktreeCreateField(width, style, "Base ref", "Used when creating a new branch; defaults to HEAD.", dialog.baseRef.Value(), dialog.baseRef.Position(), dialog.focus == uiWorktreeCreateFieldBaseRef, dialog.createBranch)...)
	lines = append(lines, "")
	lines = append(lines, l.renderWorktreeBranchMode(width, style, dialog)...)
	lines = append(lines, "")
	branchLabel := "Branch name"
	branchHelp := "Name for the new branch."
	if !dialog.createBranch {
		branchLabel = "Existing branch/ref"
		branchHelp = "Branch or ref to reuse for the new worktree."
	}
	lines = append(lines, l.renderWorktreeCreateField(width, style, branchLabel, branchHelp, dialog.branchTarget.Value(), dialog.branchTarget.Position(), dialog.focus == uiWorktreeCreateFieldBranchTarget, true)...)
	lines = append(lines, "")
	lines = append(lines, l.renderWorktreeCreateField(width, style, "Path", "Optional. Leave blank for Builder default path.", dialog.path.Value(), dialog.path.Position(), dialog.focus == uiWorktreeCreateFieldPath, true)...)
	lines = append(lines, "")
	lines = append(lines, renderWorktreeDialogButtons(width, style, dialog.focus == uiWorktreeCreateFieldSubmit, dialog.focus == uiWorktreeCreateFieldCancel, "Create", "Cancel"))
	if dialog.submitting {
		lines = append(lines, "", style.meta.Render(pendingToolSpinnerFrame(m.spinnerFrame)+" Creating worktree..."))
	}
	if trimmed := strings.TrimSpace(dialog.errorText); trimmed != "" {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(statusRedColor()).Bold(true).Render(truncateQueuedMessageLine(trimmed, width)))
	}
	lines = append(lines, "", style.meta.Render(truncateQueuedMessageLine("Esc back | Tab/Shift+Tab move | Left/Right toggle mode or buttons | Enter activate", width)))
	return l.renderWorktreeDialogLines(lines, width, height, style)
}

func (l uiViewLayout) renderWorktreeCreateField(width int, style uiStyles, label string, helper string, value string, cursor int, focused bool, enabled bool) []string {
	labelStyle := style.brand
	if !focused {
		labelStyle = style.chat
	}
	if !enabled {
		labelStyle = style.meta
	}
	lineStyle := style.input
	borderStyle := l.inputBorderStyle()
	if !focused {
		borderStyle = style.meta
	}
	if !enabled {
		lineStyle = style.inputDisabled
		borderStyle = style.meta
	}
	spec := uiEditableInputRenderSpec{Prefix: "› ", Text: value, CursorIndex: cursor, RenderCursor: focused && enabled}
	contentWidth := max(1, width)
	lines := []string{labelStyle.Render(truncateQueuedMessageLine(label, contentWidth))}
	if strings.TrimSpace(helper) != "" {
		lines = append(lines, style.meta.Render(truncateQueuedMessageLine(helper, contentWidth)))
	}
	lines = append(lines, renderFramedEditableInputLines(contentWidth, 1, spec, lineStyle, borderStyle)...)
	return lines
}

func (l uiViewLayout) renderWorktreeBranchMode(width int, style uiStyles, dialog uiWorktreeCreateDialogState) []string {
	labelStyle := style.chat
	if dialog.focus == uiWorktreeCreateFieldBranchMode {
		labelStyle = style.brand
	}
	newStyle := style.chat
	existingStyle := style.meta
	if dialog.createBranch {
		newStyle = style.brand
	} else {
		existingStyle = style.brand
	}
	line := newStyle.Render("(●) new branch") + style.meta.Render("   ") + existingStyle.Render("("+map[bool]string{true: " ", false: "●"}[dialog.createBranch]+") existing ref")
	if !dialog.createBranch {
		line = style.meta.Render("( ) new branch") + style.meta.Render("   ") + style.brand.Render("(●) existing ref")
	}
	if dialog.createBranch {
		line = style.brand.Render("(●) new branch") + style.meta.Render("   ") + style.meta.Render("( ) existing ref")
	}
	return []string{
		labelStyle.Render("Branch mode"),
		style.meta.Render(truncateQueuedMessageLine("Choose whether Builder creates a branch or reuses an existing ref.", width)),
		padANSIRight(line, width),
	}
}

func renderWorktreeDialogButtons(width int, style uiStyles, createSelected bool, cancelSelected bool, createLabel string, cancelLabel string) string {
	button := func(label string, selected bool) string {
		buttonStyle := style.meta
		if selected {
			buttonStyle = style.brand
		}
		return buttonStyle.Render("[ " + label + " ]")
	}
	line := button(createLabel, createSelected) + "  " + button(cancelLabel, cancelSelected)
	return padANSIRight(line, width)
}

func (l uiViewLayout) renderWorktreeDeleteDialog(width, height int, style uiStyles) []string {
	m := l.model
	dialog := m.worktrees.deleteConfirm
	lines := []string{
		style.brand.Render(truncateQueuedMessageLine("Worktrees", width)),
		style.meta.Render(truncateQueuedMessageLine("Delete worktree", width)),
		"",
		style.chat.Render(truncateQueuedMessageLine("Delete "+worktreeDisplayName(dialog.target)+"?", width)),
		style.meta.Render(truncateQueuedMessageLine(strings.TrimSpace(dialog.target.CanonicalRoot), width)),
		"",
	}
	body := worktreeDeleteBodyLines(dialog.target)
	for _, line := range body {
		lines = append(lines, style.meta.Render(truncateQueuedMessageLine(line, width)))
	}
	lines = append(lines, "", renderWorktreeDeleteButtons(width, style, dialog))
	if dialog.submitting {
		lines = append(lines, "", style.meta.Render(pendingToolSpinnerFrame(m.spinnerFrame)+" Deleting worktree..."))
	}
	if trimmed := strings.TrimSpace(dialog.errorText); trimmed != "" {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(statusRedColor()).Bold(true).Render(truncateQueuedMessageLine(trimmed, width)))
	}
	lines = append(lines, "", style.meta.Render(truncateQueuedMessageLine("Esc back | Left/Right choose action | Enter confirm", width)))
	return l.renderWorktreeDialogLines(lines, width, height, style)
}

func worktreeDeleteBodyLines(target serverapi.WorktreeView) []string {
	lines := []string{}
	if target.Detached {
		lines = append(lines, "Branch cleanup target: detached")
	} else if branch := strings.TrimSpace(target.BranchName); branch != "" {
		lines = append(lines, "Branch cleanup target: "+branch)
	}
	if worktreeDeleteCanAutoDeleteBranch(target) {
		lines = append(lines, "Builder can safely attempt branch cleanup for this worktree.")
	} else if strings.TrimSpace(target.BranchName) != "" {
		lines = append(lines, "Branch cleanup needs explicit confirmation for this worktree.")
	} else {
		lines = append(lines, "This removes the filesystem worktree.")
	}
	return lines
}

func renderWorktreeDeleteButtons(width int, style uiStyles, dialog uiWorktreeDeleteDialogState) string {
	actions := dialog.availableActions()
	parts := make([]string, 0, len(actions))
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
		buttonStyle := style.meta
		if action == dialog.selectedAction {
			buttonStyle = style.brand
		}
		parts = append(parts, buttonStyle.Render("[ "+label+" ]"))
	}
	return padANSIRight(strings.Join(parts, "  "), width)
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
