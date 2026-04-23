package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	sharedclient "builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/serverapi"

	tea "github.com/charmbracelet/bubbletea"
)

type worktreeCommandTestClient struct {
	listResp        serverapi.WorktreeListResponse
	listErr         error
	resolveResp     serverapi.WorktreeCreateTargetResolveResponse
	resolveErr      error
	createResp      serverapi.WorktreeCreateResponse
	createErr       error
	deleteResp      serverapi.WorktreeDeleteResponse
	deleteErr       error
	switchResp      serverapi.WorktreeSwitchResponse
	switchErr       error
	resolveRequests []serverapi.WorktreeCreateTargetResolveRequest
	createRequests  []serverapi.WorktreeCreateRequest
	deleteRequests  []serverapi.WorktreeDeleteRequest
	switchRequests  []serverapi.WorktreeSwitchRequest
	leaseFailures   map[string]int
}

func (c *worktreeCommandTestClient) ListWorktrees(context.Context, serverapi.WorktreeListRequest) (serverapi.WorktreeListResponse, error) {
	return c.listResp, c.listErr
}

func (c *worktreeCommandTestClient) ResolveWorktreeCreateTarget(_ context.Context, req serverapi.WorktreeCreateTargetResolveRequest) (serverapi.WorktreeCreateTargetResolveResponse, error) {
	c.resolveRequests = append(c.resolveRequests, req)
	if c.resolveErr != nil {
		return serverapi.WorktreeCreateTargetResolveResponse{}, c.resolveErr
	}
	if c.resolveResp.Resolution.Kind != "" {
		return c.resolveResp, nil
	}
	return serverapi.WorktreeCreateTargetResolveResponse{Resolution: serverapi.WorktreeCreateTargetResolution{Input: req.Target, Kind: serverapi.WorktreeCreateTargetResolutionKindNewBranch}}, nil
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
		case worktreeListDoneMsg, worktreeCreateDoneMsg, worktreeSwitchDoneMsg, worktreeDeleteDoneMsg, worktreeCreateTargetResolveDebounceMsg, worktreeCreateTargetResolveDoneMsg:
			next, nextCmd := model.Update(msg)
			model = next.(*uiModel)
			model = applyWorktreeCmdMessages(t, model, nextCmd)
		}
	}
	return model
}

func worktreeStatusLine(model *uiModel) string {
	return stripANSIAndTrimRight(model.renderStatusLine(120, uiThemeStyles("dark")))
}

func assertWorktreeOverlayLocalErrorOnly(t *testing.T, model *uiModel, expectedVisible []string, unexpectedVisible []string) {
	t.Helper()
	status := worktreeStatusLine(model)
	for _, unexpected := range unexpectedVisible {
		if strings.Contains(status, unexpected) {
			t.Fatalf("did not expect status line to contain %q, got %q", unexpected, status)
		}
	}
	plain := stripANSIAndTrimRight(model.View())
	for _, expected := range expectedVisible {
		if !strings.Contains(plain, expected) {
			t.Fatalf("expected overlay to contain %q, got %q", expected, plain)
		}
	}
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
	if strings.Contains(plain, "Open create form") {
		t.Fatalf("did not expect helper copy in create row, got %q", plain)
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
	if !strings.Contains(plain, "New worktree") || !strings.Contains(plain, "Branch or ref") || !strings.Contains(plain, "Base ref") {
		t.Fatalf("expected create dialog render, got %q", plain)
	}
}

func TestWorktreeCreateDialogStartsFocusedOnTargetField(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testMainWorktreeListResponse()}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree create"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if updated.worktrees.create.focus != uiWorktreeCreateFieldBranchTarget {
		t.Fatalf("focus = %v, want branch target", updated.worktrees.create.focus)
	}
}

func TestWorktreeCreateDialogBlankTargetSkipsDisabledBaseRef(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testMainWorktreeListResponse()}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree create"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.worktrees.create.focus != uiWorktreeCreateFieldActions {
		t.Fatalf("focus after down = %v, want actions", updated.worktrees.create.focus)
	}
}

func TestWorktreeCreateDialogNewBranchResolutionEnablesBaseRefFocus(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testMainWorktreeListResponse()}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree create"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)
	updated.worktrees.create.branchTarget.SetValue("feature/new")
	next, _ = updated.Update(worktreeCreateTargetResolveDoneMsg{token: updated.worktrees.create.resolveToken, query: "feature/new", resp: serverapi.WorktreeCreateTargetResolveResponse{Resolution: serverapi.WorktreeCreateTargetResolution{Input: "feature/new", Kind: serverapi.WorktreeCreateTargetResolutionKindNewBranch}}})
	updated = next.(*uiModel)

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.worktrees.create.focus != uiWorktreeCreateFieldBaseRef {
		t.Fatalf("focus after down = %v, want base ref", updated.worktrees.create.focus)
	}
}

func TestWorktreeCreateDialogExistingBranchResolutionSkipsBaseRef(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testMainWorktreeListResponse()}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree create"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)
	updated.worktrees.create.branchTarget.SetValue("main")
	next, _ = updated.Update(worktreeCreateTargetResolveDoneMsg{token: updated.worktrees.create.resolveToken, query: "main", resp: serverapi.WorktreeCreateTargetResolveResponse{Resolution: serverapi.WorktreeCreateTargetResolution{Input: "main", Kind: serverapi.WorktreeCreateTargetResolutionKindExistingBranch}}})
	updated = next.(*uiModel)

	if updated.worktrees.create.usesBaseRef() {
		t.Fatal("did not expect base ref for existing branch")
	}
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "∴ existing branch") || !strings.Contains(plain, "Base ref") {
		t.Fatalf("expected existing-branch badge, got %q", plain)
	}
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.worktrees.create.focus != uiWorktreeCreateFieldActions {
		t.Fatalf("focus after down = %v, want actions", updated.worktrees.create.focus)
	}
}

func TestWorktreeCreateDialogTypingResolvesTargetAndRendersBadge(t *testing.T) {
	client := &worktreeCommandTestClient{
		listResp:    testMainWorktreeListResponse(),
		resolveResp: serverapi.WorktreeCreateTargetResolveResponse{Resolution: serverapi.WorktreeCreateTargetResolution{Input: "main", Kind: serverapi.WorktreeCreateTargetResolutionKindExistingBranch}},
	}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree create"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	for _, r := range []rune("main") {
		next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		updated = applyWorktreeCmdMessages(t, next.(*uiModel), cmd)
	}

	if len(client.resolveRequests) == 0 {
		t.Fatal("expected resolve request after typing target")
	}
	if got := client.resolveRequests[len(client.resolveRequests)-1].Target; got != "main" {
		t.Fatalf("latest resolve target = %q, want main", got)
	}
	if updated.worktrees.create.resolution.Kind != serverapi.WorktreeCreateTargetResolutionKindExistingBranch {
		t.Fatalf("resolution = %q, want existing branch", updated.worktrees.create.resolution.Kind)
	}
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "∴ existing branch") {
		t.Fatalf("expected existing-branch badge after typing, got %q", plain)
	}
}

func TestWorktreeCreateDialogIgnoresStaleTargetResolutionResponses(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testMainWorktreeListResponse()}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree create"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	updated.worktrees.create.branchTarget.SetValue("first")
	_ = updated.scheduleWorktreeCreateTargetResolution()
	firstToken := updated.worktrees.create.resolveToken

	updated.worktrees.create.branchTarget.SetValue("second")
	_ = updated.scheduleWorktreeCreateTargetResolution()
	secondToken := updated.worktrees.create.resolveToken
	if secondToken == firstToken {
		t.Fatalf("expected fresh resolve token, got first=%d second=%d", firstToken, secondToken)
	}

	next, _ = updated.Update(worktreeCreateTargetResolveDoneMsg{
		token: firstToken,
		query: "first",
		resp:  serverapi.WorktreeCreateTargetResolveResponse{Resolution: serverapi.WorktreeCreateTargetResolution{Input: "first", Kind: serverapi.WorktreeCreateTargetResolutionKindExistingBranch}},
	})
	updated = next.(*uiModel)
	if updated.worktrees.create.resolution.Kind != "" {
		t.Fatalf("expected stale response ignored, got %+v", updated.worktrees.create.resolution)
	}
	if updated.worktrees.create.usesBaseRef() {
		t.Fatal("did not expect blank/loading state to enable base ref")
	}

	next, _ = updated.Update(worktreeCreateTargetResolveDoneMsg{
		token: secondToken,
		query: "second",
		resp:  serverapi.WorktreeCreateTargetResolveResponse{Resolution: serverapi.WorktreeCreateTargetResolution{Input: "second", Kind: serverapi.WorktreeCreateTargetResolutionKindDetachedRef}},
	})
	updated = next.(*uiModel)
	if updated.worktrees.create.resolution.Kind != serverapi.WorktreeCreateTargetResolutionKindDetachedRef {
		t.Fatalf("expected latest response applied, got %+v", updated.worktrees.create.resolution)
	}
	if updated.worktrees.create.usesBaseRef() {
		t.Fatal("did not expect base ref after detached-ref resolution")
	}
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "∴ detached ref") || !strings.Contains(plain, "Base ref") {
		t.Fatalf("expected detached-ref render with disabled base ref, got %q", plain)
	}
}

func TestWorktreeCreateDialogRenderStates(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testMainWorktreeListResponse()}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree create"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	blank := stripANSIAndTrimRight(updated.View())
	if strings.Contains(blank, "✔︎ new branch") || strings.Contains(blank, "∴ existing branch") || strings.Contains(blank, "∴ detached ref") {
		t.Fatalf("did not expect badge for blank target, got %q", blank)
	}
	if !strings.Contains(blank, "Base ref") {
		t.Fatalf("expected base ref visible for blank target state, got %q", blank)
	}

	updated.worktrees.create.branchTarget.SetValue("feature/new")
	next, _ = updated.Update(worktreeCreateTargetResolveDoneMsg{token: updated.worktrees.create.resolveToken, query: "feature/new", resp: serverapi.WorktreeCreateTargetResolveResponse{Resolution: serverapi.WorktreeCreateTargetResolution{Input: "feature/new", Kind: serverapi.WorktreeCreateTargetResolutionKindNewBranch}}})
	updated = next.(*uiModel)
	newBranch := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(newBranch, "✔︎ new branch") || !strings.Contains(newBranch, "Base ref") {
		t.Fatalf("expected new-branch badge with base ref, got %q", newBranch)
	}

	updated.worktrees.create.branchTarget.SetValue("main")
	next, _ = updated.Update(worktreeCreateTargetResolveDoneMsg{token: updated.worktrees.create.resolveToken, query: "main", resp: serverapi.WorktreeCreateTargetResolveResponse{Resolution: serverapi.WorktreeCreateTargetResolution{Input: "main", Kind: serverapi.WorktreeCreateTargetResolutionKindExistingBranch}}})
	updated = next.(*uiModel)
	existing := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(existing, "∴ existing branch") || !strings.Contains(existing, "Base ref") {
		t.Fatalf("expected existing-branch badge with disabled base ref, got %q", existing)
	}

	updated.worktrees.create.branchTarget.SetValue("HEAD~1")
	next, _ = updated.Update(worktreeCreateTargetResolveDoneMsg{token: updated.worktrees.create.resolveToken, query: "HEAD~1", resp: serverapi.WorktreeCreateTargetResolveResponse{Resolution: serverapi.WorktreeCreateTargetResolution{Input: "HEAD~1", Kind: serverapi.WorktreeCreateTargetResolutionKindDetachedRef}}})
	updated = next.(*uiModel)
	detached := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(detached, "∴ detached ref") || !strings.Contains(detached, "Base ref") {
		t.Fatalf("expected detached-ref badge with disabled base ref, got %q", detached)
	}
}

func TestWorktreeCreateDialogResolveErrorRendersLocally(t *testing.T) {
	client := &worktreeCommandTestClient{
		listResp:   testMainWorktreeListResponse(),
		resolveErr: errors.New("resolve failed: bad repo state"),
	}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree create"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	updated = applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if updated.worktrees.create.resolution.Kind != "" {
		t.Fatalf("expected empty resolution after resolve error, got %+v", updated.worktrees.create.resolution)
	}
	assertWorktreeOverlayLocalErrorOnly(t, updated, []string{"resolve failed: bad repo state"}, []string{"resolve failed: bad repo state"})
}

func TestWorktreeCreateDialogTypingAfterResolveErrorClearsErrorAndShowsBadge(t *testing.T) {
	client := &worktreeCommandTestClient{
		listResp:   testMainWorktreeListResponse(),
		resolveErr: errors.New("resolve failed: bad repo state"),
	}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree create"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	updated = applyWorktreeCmdMessages(t, next.(*uiModel), cmd)
	if !strings.Contains(stripANSIAndTrimRight(updated.View()), "resolve failed: bad repo state") {
		t.Fatalf("expected resolve error visible, got %q", stripANSIAndTrimRight(updated.View()))
	}

	client.resolveErr = nil
	client.resolveResp = serverapi.WorktreeCreateTargetResolveResponse{Resolution: serverapi.WorktreeCreateTargetResolution{Input: "main", Kind: serverapi.WorktreeCreateTargetResolutionKindExistingBranch}}
	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	updated = applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if updated.worktrees.create.errorText != "" {
		t.Fatalf("expected resolve error cleared after new input, got %q", updated.worktrees.create.errorText)
	}
	if updated.worktrees.create.resolution.Kind != serverapi.WorktreeCreateTargetResolutionKindExistingBranch {
		t.Fatalf("resolution = %q, want existing branch", updated.worktrees.create.resolution.Kind)
	}
	plain := stripANSIAndTrimRight(updated.View())
	if strings.Contains(plain, "resolve failed: bad repo state") {
		t.Fatalf("did not expect stale resolve error after recovery, got %q", plain)
	}
	if !strings.Contains(plain, "∴ existing branch") {
		t.Fatalf("expected existing-branch badge after recovery, got %q", plain)
	}
}

func TestWorktreeCreateDialogLayoutStaysStableAcrossResolutionStates(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testMainWorktreeListResponse()}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree create"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	blank := strings.Split(stripANSIAndTrimRight(updated.View()), "\n")
	blankCount := len(blank)
	if strings.Contains(strings.Join(blank, "\n"), "Resolving target") {
		t.Fatalf("did not expect loading copy in blank state, got %q", strings.Join(blank, "\n"))
	}

	updated.worktrees.create.branchTarget.SetValue("feature/x")
	updated.worktrees.create.resolving = true
	loadingPlain := stripANSIAndTrimRight(updated.View())
	if strings.Contains(loadingPlain, "Resolving target") {
		t.Fatalf("did not expect loading copy while resolving, got %q", loadingPlain)
	}
	if got := len(strings.Split(loadingPlain, "\n")); got != blankCount {
		t.Fatalf("loading line count = %d, want %d", got, blankCount)
	}
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.worktrees.create.focus != uiWorktreeCreateFieldActions {
		t.Fatalf("loading focus after down = %v, want actions", updated.worktrees.create.focus)
	}

	updated.worktrees.create.focus = uiWorktreeCreateFieldBranchTarget
	updated.worktrees.create.syncFocus()
	next, _ = updated.Update(worktreeCreateTargetResolveDoneMsg{token: updated.worktrees.create.resolveToken, query: "feature/x", resp: serverapi.WorktreeCreateTargetResolveResponse{Resolution: serverapi.WorktreeCreateTargetResolution{Input: "feature/x", Kind: serverapi.WorktreeCreateTargetResolutionKindExistingBranch}}})
	updated = next.(*uiModel)
	existingPlain := stripANSIAndTrimRight(updated.View())
	if got := len(strings.Split(existingPlain, "\n")); got != blankCount {
		t.Fatalf("existing line count = %d, want %d", got, blankCount)
	}
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.worktrees.create.focus != uiWorktreeCreateFieldActions {
		t.Fatalf("existing focus after down = %v, want actions", updated.worktrees.create.focus)
	}

	updated.worktrees.create.focus = uiWorktreeCreateFieldBranchTarget
	updated.worktrees.create.branchTarget.SetValue("HEAD~1")
	updated.worktrees.create.syncFocus()
	next, _ = updated.Update(worktreeCreateTargetResolveDoneMsg{token: updated.worktrees.create.resolveToken, query: "HEAD~1", resp: serverapi.WorktreeCreateTargetResolveResponse{Resolution: serverapi.WorktreeCreateTargetResolution{Input: "HEAD~1", Kind: serverapi.WorktreeCreateTargetResolutionKindDetachedRef}}})
	updated = next.(*uiModel)
	detachedPlain := stripANSIAndTrimRight(updated.View())
	if got := len(strings.Split(detachedPlain, "\n")); got != blankCount {
		t.Fatalf("detached line count = %d, want %d", got, blankCount)
	}
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.worktrees.create.focus != uiWorktreeCreateFieldActions {
		t.Fatalf("detached focus after down = %v, want actions", updated.worktrees.create.focus)
	}

	updated.worktrees.create.focus = uiWorktreeCreateFieldBranchTarget
	updated.worktrees.create.branchTarget.SetValue("feature/new")
	updated.worktrees.create.syncFocus()
	next, _ = updated.Update(worktreeCreateTargetResolveDoneMsg{token: updated.worktrees.create.resolveToken, query: "feature/new", resp: serverapi.WorktreeCreateTargetResolveResponse{Resolution: serverapi.WorktreeCreateTargetResolution{Input: "feature/new", Kind: serverapi.WorktreeCreateTargetResolutionKindNewBranch}}})
	updated = next.(*uiModel)
	newPlain := stripANSIAndTrimRight(updated.View())
	if got := len(strings.Split(newPlain, "\n")); got != blankCount {
		t.Fatalf("new branch line count = %d, want %d", got, blankCount)
	}
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.worktrees.create.focus != uiWorktreeCreateFieldBaseRef {
		t.Fatalf("new-branch focus after down = %v, want base ref", updated.worktrees.create.focus)
	}
}

func TestWorktreeCreateDialogLeavesBranchNameBlankWithoutSessionNameSuggestion(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testMainWorktreeListResponse()}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree create"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if got := updated.worktrees.create.branchTarget.Value(); got != "" {
		t.Fatalf("branch target default = %q, want empty", got)
	}
}

func TestWorktreeCreateDialogBlankBranchNameValidationDoesNotSendRequest(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testMainWorktreeListResponse()}
	m := newWorktreeTestModel(t, client)
	m.input = "/worktree create"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)
	updated.worktrees.create.baseRef.SetValue("HEAD")
	updated.worktrees.create.branchTarget.SetValue("")
	updated.worktrees.create.focus = uiWorktreeCreateFieldActions
	updated.worktrees.create.action = uiWorktreeCreateActionCreate
	updated.worktrees.create.syncFocus()

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if len(client.createRequests) != 0 {
		t.Fatalf("expected no create requests, got %+v", client.createRequests)
	}
	if updated.worktrees.create.errorText != "Branch or ref is required" {
		t.Fatalf("error text = %q, want branch validation", updated.worktrees.create.errorText)
	}
	if strings.TrimSpace(updated.transientStatus) != "" {
		t.Fatalf("expected no status-line error mirror, got %q", updated.transientStatus)
	}
	if !updated.worktrees.isOpen() || updated.worktrees.phase != uiWorktreeOverlayPhaseCreate {
		t.Fatalf("expected create dialog to remain open, open=%t phase=%q", updated.worktrees.isOpen(), updated.worktrees.phase)
	}
}

func TestWorktreeCreateDialogMutationErrorRendersOnceLocallyAndClamps(t *testing.T) {
	client := &worktreeCommandTestClient{
		listResp:  testMainWorktreeListResponse(),
		createErr: errors.New("git worktree add -b main /tmp/main HEAD\nline two\nline three\nline four\nline five"),
	}
	m := newWorktreeTestModel(t, client)
	m.input = "/wt create"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)
	updated.worktrees.create.baseRef.SetValue("HEAD")
	updated.worktrees.create.branchTarget.SetValue("feature/branch")
	updated.worktrees.create.focus = uiWorktreeCreateFieldActions
	updated.worktrees.create.action = uiWorktreeCreateActionCreate
	updated.worktrees.create.syncFocus()

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if strings.TrimSpace(updated.transientStatus) != "" {
		t.Fatalf("expected no status-line error mirror, got %q", updated.transientStatus)
	}
	plain := stripANSIAndTrimRight(updated.View())
	if count := strings.Count(plain, "git worktree add -b main /tmp/main HEAD"); count != 1 {
		t.Fatalf("expected one overlay error rendering, count=%d view=%q", count, plain)
	}
	status := worktreeStatusLine(updated)
	if strings.Contains(status, "git worktree add -b main /tmp/main HEAD") {
		t.Fatalf("did not expect status line to mirror create error, got %q", status)
	}
	for _, expected := range []string{"line two", "line three", "line four"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("expected wrapped error line %q in view %q", expected, plain)
		}
	}
	if strings.Contains(plain, "line five") {
		t.Fatalf("expected error block clamped before fifth line, got %q", plain)
	}
}

func TestWorktreeOverlayListErrorRendersLocallyWithoutStatusLineMirror(t *testing.T) {
	client := &worktreeCommandTestClient{listErr: errors.New("load failed\nline two\nline three\nline four\nline five")}
	m := newWorktreeTestModel(t, client)
	m.input = "/wt"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if updated.worktrees.errorText == "" {
		t.Fatal("expected overlay error text")
	}
	assertWorktreeOverlayLocalErrorOnly(t, updated, []string{"load failed", "line two", "line three", "line four"}, []string{"load failed"})
	if plain := stripANSIAndTrimRight(updated.View()); strings.Contains(plain, "line five") {
		t.Fatalf("expected list error clamped before fifth line, got %q", plain)
	}
}

func TestWorktreeDeleteTargetResolutionErrorRendersLocallyWithoutStatusLineMirror(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testMainWorktreeListResponse()}
	m := newWorktreeTestModel(t, client)
	m.input = "/wt delete missing"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if updated.worktrees.phase != uiWorktreeOverlayPhaseList {
		t.Fatalf("phase = %q, want list", updated.worktrees.phase)
	}
	if !strings.Contains(updated.worktrees.errorText, `worktree "missing" not found`) {
		t.Fatalf("unexpected overlay error %q", updated.worktrees.errorText)
	}
	if strings.TrimSpace(updated.transientStatus) != "" {
		t.Fatalf("expected no status mutation, got %q", updated.transientStatus)
	}
	assertWorktreeOverlayLocalErrorOnly(t, updated, []string{`worktree "missing" not found`}, []string{`worktree "missing" not found`})
}

func TestWorktreeOverlayCreateErrorSuppressesPreexistingStatusNotice(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testMainWorktreeListResponse(), createErr: errors.New("create failed")}
	m := newWorktreeTestModel(t, client)
	m.transientStatus = "old success notice"
	m.transientStatusKind = uiStatusNoticeSuccess
	m.input = "/wt create"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)
	updated.worktrees.create.baseRef.SetValue("HEAD")
	updated.worktrees.create.branchTarget.SetValue("feature/branch")
	updated.worktrees.create.focus = uiWorktreeCreateFieldActions
	updated.worktrees.create.action = uiWorktreeCreateActionCreate
	updated.worktrees.create.syncFocus()

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	status := worktreeStatusLine(updated)
	if strings.Contains(status, "old success notice") || strings.Contains(status, "create failed") {
		t.Fatalf("expected status line suppression while overlay error visible, got %q", status)
	}
	if updated.transientStatus != "old success notice" {
		t.Fatalf("expected transient status state preserved, got %q", updated.transientStatus)
	}
	assertWorktreeOverlayLocalErrorOnly(t, updated, []string{"create failed"}, []string{"create failed", "old success notice"})
}

func TestWorktreeOverlaySwitchErrorRendersLocallyWithoutStatusLineMirror(t *testing.T) {
	resp := testMainWorktreeListResponse()
	resp.Worktrees = append(resp.Worktrees, serverapi.WorktreeView{WorktreeID: "wt-feature", DisplayName: "feature-a", CanonicalRoot: "/wt/feature-a", BranchName: "feature/a"})
	client := &worktreeCommandTestClient{listResp: resp, switchErr: errors.New("switch failed\nline two")}
	m := newWorktreeTestModel(t, client)
	m.input = "/wt"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if !updated.worktrees.isOpen() || updated.worktrees.phase != uiWorktreeOverlayPhaseList {
		t.Fatalf("expected overlay list to remain open, open=%t phase=%q", updated.worktrees.isOpen(), updated.worktrees.phase)
	}
	assertWorktreeOverlayLocalErrorOnly(t, updated, []string{"switch failed", "line two"}, []string{"switch failed"})
}

func TestWorktreeOverlayDeleteErrorRendersLocallyWithoutStatusLineMirror(t *testing.T) {
	client := &worktreeCommandTestClient{listResp: testLinkedWorktreeListResponse(), deleteErr: errors.New("delete failed\nline two")}
	m := newWorktreeTestModel(t, client)
	m.input = "/wt delete"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)
	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if updated.worktrees.phase != uiWorktreeOverlayPhaseDeleteConfirm {
		t.Fatalf("expected delete dialog to remain open, phase=%q", updated.worktrees.phase)
	}
	assertWorktreeOverlayLocalErrorOnly(t, updated, []string{"delete failed", "line two"}, []string{"delete failed"})
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
			Target:   clientui.SessionExecutionTarget{EffectiveWorkdir: "/repo"},
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
			Target:   clientui.SessionExecutionTarget{EffectiveWorkdir: "/wt/feature-a"},
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
			Target:        clientui.SessionExecutionTarget{EffectiveWorkdir: "/wt/feature-branch"},
			Worktree:      serverapi.WorktreeView{WorktreeID: "wt-new", DisplayName: "feature-branch", CanonicalRoot: "/wt/feature-branch", BranchName: "feature/branch"},
			CreatedBranch: true,
		},
	}
	m := newWorktreeTestModel(t, client)
	m.input = "/wt create"
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	updated.worktrees.create.baseRef.SetValue("HEAD")
	updated.worktrees.create.branchTarget.SetValue("feature/branch")
	updated.worktrees.create.focus = uiWorktreeCreateFieldActions
	updated.worktrees.create.action = uiWorktreeCreateActionCreate
	updated.worktrees.create.syncFocus()

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if updated.worktrees.isOpen() {
		t.Fatal("expected overlay closed after create")
	}
	if len(client.createRequests) != 1 {
		t.Fatalf("create requests = %d, want 1", len(client.createRequests))
	}
	if len(client.resolveRequests) == 0 || client.resolveRequests[len(client.resolveRequests)-1].Target != "feature/branch" {
		t.Fatalf("expected resolve request for branch target, got %+v", client.resolveRequests)
	}
	if got := client.createRequests[0]; got.BaseRef != "HEAD" || !got.CreateBranch || got.BranchName != "feature/branch" || got.RootPath != "" {
		t.Fatalf("unexpected create request: %+v", got)
	}
}

func TestWorktreeCreateDialogDetachedRefResolutionCreatesWithoutBranch(t *testing.T) {
	client := &worktreeCommandTestClient{
		listResp:    testMainWorktreeListResponse(),
		resolveResp: serverapi.WorktreeCreateTargetResolveResponse{Resolution: serverapi.WorktreeCreateTargetResolution{Input: "HEAD~1", Kind: serverapi.WorktreeCreateTargetResolutionKindDetachedRef}},
		createResp:  serverapi.WorktreeCreateResponse{Target: clientui.SessionExecutionTarget{EffectiveWorkdir: "/wt/head-1"}, Worktree: serverapi.WorktreeView{WorktreeID: "wt-detached", DisplayName: "head-1", CanonicalRoot: "/wt/head-1", Detached: true}},
	}
	m := newWorktreeTestModel(t, client)
	m.input = "/wt create"
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	updated.worktrees.create.branchTarget.SetValue("HEAD~1")
	updated.worktrees.create.focus = uiWorktreeCreateFieldActions
	updated.worktrees.create.action = uiWorktreeCreateActionCreate
	updated.worktrees.create.syncFocus()

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = applyWorktreeCmdMessages(t, next.(*uiModel), cmd)

	if len(client.createRequests) != 1 {
		t.Fatalf("create requests = %d, want 1", len(client.createRequests))
	}
	if got := client.createRequests[0]; got.CreateBranch || got.BranchName != "" || got.BaseRef != "HEAD~1" {
		t.Fatalf("unexpected detached create request: %+v", got)
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
