package app

import (
	"context"
	"strings"
	"testing"

	sharedclient "builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/serverapi"

	tea "github.com/charmbracelet/bubbletea"
)

type worktreeCommandTestClient struct {
	listResp       serverapi.WorktreeListResponse
	listErr        error
	createResp     serverapi.WorktreeCreateResponse
	createErr      error
	deleteResp     serverapi.WorktreeDeleteResponse
	deleteErr      error
	switchResp     serverapi.WorktreeSwitchResponse
	switchErr      error
	createRequests []serverapi.WorktreeCreateRequest
	deleteRequests []serverapi.WorktreeDeleteRequest
	switchRequests []serverapi.WorktreeSwitchRequest
	leaseFailures  map[string]int
}

func (c *worktreeCommandTestClient) ListWorktrees(context.Context, serverapi.WorktreeListRequest) (serverapi.WorktreeListResponse, error) {
	return c.listResp, c.listErr
}

func (c *worktreeCommandTestClient) CreateWorktree(_ context.Context, req serverapi.WorktreeCreateRequest) (serverapi.WorktreeCreateResponse, error) {
	c.createRequests = append(c.createRequests, req)
	if c.consumeLeaseFailure("create", req.ControllerLeaseID) {
		return serverapi.WorktreeCreateResponse{}, serverapi.ErrInvalidControllerLease
	}
	return c.createResp, c.createErr
}

func (c *worktreeCommandTestClient) SwitchWorktree(_ context.Context, req serverapi.WorktreeSwitchRequest) (serverapi.WorktreeSwitchResponse, error) {
	c.switchRequests = append(c.switchRequests, req)
	if c.consumeLeaseFailure("switch", req.ControllerLeaseID) {
		return serverapi.WorktreeSwitchResponse{}, serverapi.ErrInvalidControllerLease
	}
	return c.switchResp, c.switchErr
}

func (c *worktreeCommandTestClient) DeleteWorktree(_ context.Context, req serverapi.WorktreeDeleteRequest) (serverapi.WorktreeDeleteResponse, error) {
	c.deleteRequests = append(c.deleteRequests, req)
	if c.consumeLeaseFailure("delete", req.ControllerLeaseID) {
		return serverapi.WorktreeDeleteResponse{}, serverapi.ErrInvalidControllerLease
	}
	return c.deleteResp, c.deleteErr
}

func (c *worktreeCommandTestClient) consumeLeaseFailure(kind string, leaseID string) bool {
	if c == nil || c.leaseFailures == nil {
		return false
	}
	key := kind + ":" + strings.TrimSpace(leaseID)
	remaining := c.leaseFailures[key]
	if remaining <= 0 {
		return false
	}
	c.leaseFailures[key] = remaining - 1
	return true
}

func newWorktreeTestRuntimeClient(sessionID string) *sessionRuntimeClient {
	reads := &countingSessionViewClient{view: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: sessionID}}}
	runtimeClient := newUIRuntimeClientWithReads(sessionID, reads, sharedclient.NewLoopbackRuntimeControlClient(nil)).(*sessionRuntimeClient)
	runtimeClient.SetControllerLeaseID("lease-1")
	return runtimeClient
}

func newWorktreeTestModel(t *testing.T, client *worktreeCommandTestClient, opts ...UIOption) *uiModel {
	t.Helper()
	allOpts := []UIOption{WithUIWorktreeClient(client), WithUISessionID("session-1")}
	allOpts = append(allOpts, opts...)
	model := newProjectedTestUIModel(newWorktreeTestRuntimeClient("session-1"), nil, nil, allOpts...)
	if runtimeClient, ok := model.runtimeClient().(*sessionRuntimeClient); ok && strings.TrimSpace(model.sessionName) != "" {
		runtimeClient.storeMainView(clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: model.sessionID, SessionName: model.sessionName}})
	}
	return model
}

func applyWorktreeCmdMessages(t *testing.T, model *uiModel, cmd tea.Cmd) *uiModel {
	t.Helper()
	for _, msg := range collectCmdMessages(t, cmd) {
		switch msg.(type) {
		case worktreeListDoneMsg, worktreeCreateDoneMsg, worktreeSwitchDoneMsg, worktreeDeleteDoneMsg:
			next, nextCmd := model.Update(msg)
			model = next.(*uiModel)
			model = applyWorktreeCmdMessages(t, model, nextCmd)
		}
	}
	return model
}

func testMainWorktreeListResponse() serverapi.WorktreeListResponse {
	return serverapi.WorktreeListResponse{
		Target: clientui.SessionExecutionTarget{
			WorkspaceID:      "workspace-1",
			WorkspaceRoot:    "/repo",
			EffectiveWorkdir: "/repo",
		},
		Worktrees: []serverapi.WorktreeView{{
			WorktreeID:    "wt-main",
			DisplayName:   "main",
			CanonicalRoot: "/repo",
			BranchName:    "main",
			IsMain:        true,
			IsCurrent:     true,
		}},
	}
}

func testLinkedWorktreeListResponse() serverapi.WorktreeListResponse {
	return serverapi.WorktreeListResponse{
		Target: clientui.SessionExecutionTarget{
			WorkspaceID:      "workspace-1",
			WorkspaceRoot:    "/repo",
			WorktreeID:       "wt-feature",
			WorktreeRoot:     "/wt/feature-a",
			EffectiveWorkdir: "/wt/feature-a/pkg",
		},
		Worktrees: []serverapi.WorktreeView{
			{
				WorktreeID:    "wt-main",
				DisplayName:   "main",
				CanonicalRoot: "/repo",
				BranchName:    "main",
				IsMain:        true,
			},
			{
				WorktreeID:      "wt-feature",
				DisplayName:     "feature-a",
				CanonicalRoot:   "/wt/feature-a",
				BranchName:      "feature/a",
				IsCurrent:       true,
				BuilderManaged:  true,
				CreatedBranch:   true,
				OriginSessionID: "session-1",
			},
		},
	}
}

func TestWorktreeCommandOpensOverlayAndRendersPage(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testLinkedWorktreeListResponse()}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if !updated.worktrees.isOpen() {
		t.Fatal("expected worktree overlay open")
	}
	if updated.inputMode() != uiInputModeWorktree {
		t.Fatalf("input mode = %q, want %q", updated.inputMode(), uiInputModeWorktree)
	}
	if updated.view.Mode() != "detail" {
		t.Fatalf("view mode = %q, want detail", updated.view.Mode())
	}
	if len(updated.transcriptEntries) != 0 {
		t.Fatalf("did not expect transcript feedback, got %d entries", len(updated.transcriptEntries))
	}
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "Worktrees") || !strings.Contains(plain, "Create worktree") || !strings.Contains(plain, "/wt/feature-a") {
		t.Fatalf("expected worktree page render, got %q", plain)
	}
}

func TestWorktreeCreateCommandOpensCreateDialog(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testMainWorktreeListResponse()}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree create"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if !updated.worktrees.isOpen() {
		t.Fatal("expected worktree overlay open")
	}
	if updated.worktrees.phase != uiWorktreeOverlayPhaseCreate {
		t.Fatalf("phase = %q, want %q", updated.worktrees.phase, uiWorktreeOverlayPhaseCreate)
	}
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "Create worktree") || !strings.Contains(plain, "Branch mode") {
		t.Fatalf("expected create dialog render, got %q", plain)
	}
}

func TestWorktreeDeleteCommandOpensDeleteDialogInOverlay(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testLinkedWorktreeListResponse()}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree delete"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if updated.worktrees.phase != uiWorktreeOverlayPhaseDeleteConfirm {
		t.Fatalf("phase = %q, want %q", updated.worktrees.phase, uiWorktreeOverlayPhaseDeleteConfirm)
	}
	if updated.worktrees.deleteConfirm.target.WorktreeID != "wt-feature" {
		t.Fatalf("delete target = %+v", updated.worktrees.deleteConfirm.target)
	}
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "Delete worktree") || !strings.Contains(plain, "feature-a") {
		t.Fatalf("expected delete dialog render, got %q", plain)
	}
}

func TestWorktreeSwitchCommandRemainsDirectShortcut(t *testing.T) {
	client := &worktreeCommandTestClient{
		listResp: testLinkedWorktreeListResponse(),
		switchResp: serverapi.WorktreeSwitchResponse{
			Target: clientui.SessionExecutionTarget{EffectiveWorkdir: "/repo"},
			Worktree: serverapi.WorktreeView{WorktreeID: "wt-main", DisplayName: "main", CanonicalRoot: "/repo", IsMain: true},
		},
	}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree switch main"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if updated.worktrees.isOpen() {
		t.Fatal("did not expect overlay for direct switch")
	}
	if len(client.switchRequests) != 1 || client.switchRequests[0].WorktreeID != "wt-main" {
		t.Fatalf("unexpected switch requests: %+v", client.switchRequests)
	}
	plain := stripANSIAndTrimRight(updated.view.OngoingSnapshot())
	if !strings.Contains(plain, "Switched to main") {
		t.Fatalf("expected switch note in transcript, got %q", plain)
	}
}

func TestWorktreeOverlayEnterSwitchesSelectedItemAndCloses(t *testing.T) {
	resp := testMainWorktreeListResponse()
	resp.Worktrees = append(resp.Worktrees, serverapi.WorktreeView{WorktreeID: "wt-feature", DisplayName: "feature-a", CanonicalRoot: "/wt/feature-a", BranchName: "feature/a"})
	client := &worktreeCommandTestClient{
		listResp: resp,
		switchResp: serverapi.WorktreeSwitchResponse{
			Target: clientui.SessionExecutionTarget{EffectiveWorkdir: "/wt/feature-a"},
			Worktree: serverapi.WorktreeView{WorktreeID: "wt-feature", DisplayName: "feature-a", CanonicalRoot: "/wt/feature-a", BranchName: "feature/a"},
		},
	}
	m := newWorktreeTestModel(t, client)
	m.input = "/wt"
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = applyWorktreeCmdMessages(t, next.(*uiModel), cmd)
	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if updated.worktrees.isOpen() {
		t.Fatal("expected overlay closed after switch")
	}
	if len(client.switchRequests) != 1 || client.switchRequests[0].WorktreeID != "wt-feature" {
		t.Fatalf("unexpected switch request: %+v", client.switchRequests)
	}
}

func TestWorktreeCreateDialogSubmitsAndClosesOnSuccess(t *testing.T) {
	client := &worktreeCommandTestClient{
		listResp: testMainWorktreeListResponse(),
		createResp: serverapi.WorktreeCreateResponse{
			Target: clientui.SessionExecutionTarget{EffectiveWorkdir: "/wt/feature-branch"},
			Worktree: serverapi.WorktreeView{WorktreeID: "wt-new", DisplayName: "feature-branch", CanonicalRoot: "/wt/feature-branch", BranchName: "feature/branch"},
			CreatedBranch: true,
		},
	}
	m := newWorktreeTestModel(t, client)
	m.input = "/wt create"
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	updated.worktrees.create.baseRef.SetValue("HEAD")
	updated.worktrees.create.branchTarget.SetValue("feature/branch")
	updated.worktrees.create.path.SetValue("/wt/feature-branch")
	updated.worktrees.create.focus = uiWorktreeCreateFieldSubmit
	updated.worktrees.create.syncFocus()

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if updated.worktrees.isOpen() {
		t.Fatal("expected overlay closed after create")
	}
	if len(client.createRequests) != 1 {
		t.Fatalf("create requests = %d, want 1", len(client.createRequests))
	}
	if got := client.createRequests[0]; got.BaseRef != "HEAD" || !got.CreateBranch || got.BranchName != "feature/branch" || got.RootPath != "/wt/feature-branch" {
		t.Fatalf("unexpected create request: %+v", got)
	}
}

func TestWorktreeDeleteDialogStaysOpenAfterSuccess(t *testing.T) {
	client := &worktreeCommandTestClient{
		listResp:   testLinkedWorktreeListResponse(),
		deleteResp: serverapi.WorktreeDeleteResponse{Target: clientui.SessionExecutionTarget{EffectiveWorkdir: "/repo"}, Worktree: serverapi.WorktreeView{WorktreeID: "wt-feature", DisplayName: "feature-a", CanonicalRoot: "/wt/feature-a"}},
	}
	m := newWorktreeTestModel(t, client)
	m.input = "/wt delete"
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)
	client.listResp = testMainWorktreeListResponse()

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if !updated.worktrees.isOpen() {
		t.Fatal("expected overlay to stay open after delete")
	}
	if updated.worktrees.phase != uiWorktreeOverlayPhaseList {
		t.Fatalf("phase = %q, want list", updated.worktrees.phase)
	}
	if len(client.deleteRequests) != 1 || client.deleteRequests[0].DeleteBranch {
		t.Fatalf("unexpected delete request: %+v", client.deleteRequests)
	}
}

func TestWorktreeDeleteBranchHotkeyPrefersBranchDeleteAction(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testLinkedWorktreeListResponse()}
	m := newWorktreeTestModel(t, client)
	m.input = "/wt"
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	updated = next.(*uiModel)

	if updated.worktrees.phase != uiWorktreeOverlayPhaseDeleteConfirm {
		t.Fatalf("phase = %q, want delete_confirm", updated.worktrees.phase)
	}
	if updated.worktrees.deleteConfirm.selectedAction != uiWorktreeDeleteActionDeleteBranch {
		t.Fatalf("selected action = %v, want delete branch", updated.worktrees.deleteConfirm.selectedAction)
	}
}
