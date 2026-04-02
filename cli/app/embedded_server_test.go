package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"builder/server/auth"
	serverembedded "builder/server/embedded"
	"builder/server/launch"
	"builder/server/primaryrun"
	"builder/server/projectview"
	"builder/server/registry"
	"builder/server/runtime"
	"builder/server/runtimecontrol"
	"builder/server/sessionlaunch"
	"builder/server/sessionlifecycle"
	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/serverapi"
)

type testEmbeddedServer struct {
	cfg                  config.App
	containerDir         string
	oauthOpts            auth.OpenAIOAuthOptions
	authManager          *auth.Manager
	fastModeState        *runtime.FastModeState
	background           *shelltool.Manager
	backgroundRouter     serverembedded.BackgroundRouter
	runPromptClient      client.RunPromptClient
	projectID            string
	askViewClient        client.AskViewClient
	approvalViewClient   client.ApprovalViewClient
	promptControlClient  client.PromptControlClient
	promptActivityClient client.PromptActivityClient
	projectViewClient    client.ProjectViewClient
	processControlClient client.ProcessControlClient
	processOutputClient  client.ProcessOutputClient
	processViewClient    client.ProcessViewClient
	runtimeControlClient client.RuntimeControlClient
	sessionLaunch        client.SessionLaunchClient
	sessionActivity      client.SessionActivityClient
	sessionLifecycle     client.SessionLifecycleClient
	sessionRuntime       client.SessionRuntimeClient
	sessionViewClient    client.SessionViewClient
	sessionStores        *registry.SessionStoreRegistry
	prepareRuntime       func(plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string) (*runtimeLaunchPlan, error)
	reauthenticate       func(ctx context.Context, interactor authInteractor) error
}

type stubEmbeddedProcessViewClient struct {
	listResp serverapi.ProcessListResponse
	getResp  serverapi.ProcessGetResponse
	err      error
}

type stubEmbeddedProcessControlClient struct {
	inlineResp serverapi.ProcessInlineOutputResponse
	err        error
	killed     []string
}

func (s *testEmbeddedServer) Close() error       { return nil }
func (s *testEmbeddedServer) Config() config.App { return s.cfg }
func (s *testEmbeddedServer) ProjectID() string {
	if strings.TrimSpace(s.projectID) != "" {
		return s.projectID
	}
	projectID, _ := config.ProjectIDForWorkspaceRoot(s.cfg.WorkspaceRoot)
	return projectID
}
func (s *testEmbeddedServer) ProjectViewClient() client.ProjectViewClient {
	if s.projectViewClient != nil {
		return s.projectViewClient
	}
	service, err := projectview.NewService(s.ProjectID(), s.cfg.WorkspaceRoot, s.containerDir)
	if err != nil {
		return nil
	}
	return client.NewLoopbackProjectViewClient(service)
}
func (s *testEmbeddedServer) AskViewClient() client.AskViewClient { return s.askViewClient }
func (s *testEmbeddedServer) ApprovalViewClient() client.ApprovalViewClient {
	return s.approvalViewClient
}
func (s *testEmbeddedServer) PromptControlClient() client.PromptControlClient {
	return s.promptControlClient
}
func (s *testEmbeddedServer) PromptActivityClient() client.PromptActivityClient {
	return s.promptActivityClient
}
func (s *testEmbeddedServer) ContainerDir() string                  { return s.containerDir }
func (s *testEmbeddedServer) OAuthOptions() auth.OpenAIOAuthOptions { return s.oauthOpts }
func (s *testEmbeddedServer) AuthManager() *auth.Manager            { return s.authManager }
func (s *testEmbeddedServer) FastModeState() *runtime.FastModeState { return s.fastModeState }
func (s *testEmbeddedServer) Background() *shelltool.Manager        { return s.background }
func (s *testEmbeddedServer) BackgroundRouter() serverembedded.BackgroundRouter {
	return s.backgroundRouter
}
func (s *testEmbeddedServer) RunPromptClient() client.RunPromptClient { return s.runPromptClient }
func (s *testEmbeddedServer) ProcessControlClient() client.ProcessControlClient {
	return s.processControlClient
}
func (s *testEmbeddedServer) ProcessOutputClient() client.ProcessOutputClient {
	return s.processOutputClient
}
func (s *testEmbeddedServer) ProcessViewClient() client.ProcessViewClient {
	return s.processViewClient
}
func (s *testEmbeddedServer) RuntimeControlClient() client.RuntimeControlClient {
	if s.runtimeControlClient != nil {
		return s.runtimeControlClient
	}
	registry := registry.NewRuntimeRegistry()
	return client.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(registry, registry))
}
func (s *testEmbeddedServer) sessionStoreRegistry() *registry.SessionStoreRegistry {
	if s.sessionStores == nil {
		s.sessionStores = registry.NewSessionStoreRegistry()
	}
	return s.sessionStores
}
func (s *testEmbeddedServer) SessionLaunchClient() client.SessionLaunchClient {
	if s.sessionLaunch != nil {
		return s.sessionLaunch
	}
	service := sessionlaunch.NewService(launch.Planner{Config: s.cfg, ContainerDir: s.containerDir}, s.sessionStoreRegistry())
	return client.NewLoopbackSessionLaunchClient(service)
}
func (s *testEmbeddedServer) SessionActivityClient() client.SessionActivityClient {
	return s.sessionActivity
}
func (s *testEmbeddedServer) SessionLifecycleClient() client.SessionLifecycleClient {
	if s.sessionLifecycle != nil {
		return s.sessionLifecycle
	}
	return client.NewLoopbackSessionLifecycleClient(sessionlifecycle.NewService(s.cfg.PersistenceRoot, s.sessionStoreRegistry(), s.authManager))
}
func (s *testEmbeddedServer) SessionRuntimeClient() client.SessionRuntimeClient {
	return s.sessionRuntime
}
func (s *testEmbeddedServer) SessionViewClient() client.SessionViewClient {
	return s.sessionViewClient
}
func (s *testEmbeddedServer) PrepareRuntime(plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string) (*runtimeLaunchPlan, error) {
	if s.prepareRuntime != nil {
		return s.prepareRuntime(plan, diagnosticWriter, startLogLine)
	}
	return nil, errors.New("test embedded server prepare runtime not configured")
}
func (s *testEmbeddedServer) Reauthenticate(ctx context.Context, interactor authInteractor) error {
	if s.reauthenticate != nil {
		return s.reauthenticate(ctx, interactor)
	}
	return ensureAuthReady(ctx, s.authManager, s.oauthOpts, s.cfg.Settings.Theme, s.cfg.Settings.TUIAlternateScreen, interactor)
}

func (s *stubEmbeddedProcessViewClient) ListProcesses(context.Context, serverapi.ProcessListRequest) (serverapi.ProcessListResponse, error) {
	if s.err != nil {
		return serverapi.ProcessListResponse{}, s.err
	}
	return s.listResp, nil
}

func (s *stubEmbeddedProcessViewClient) GetProcess(context.Context, serverapi.ProcessGetRequest) (serverapi.ProcessGetResponse, error) {
	if s.err != nil {
		return serverapi.ProcessGetResponse{}, s.err
	}
	return s.getResp, nil
}

func (s *stubEmbeddedProcessControlClient) KillProcess(_ context.Context, req serverapi.ProcessKillRequest) (serverapi.ProcessKillResponse, error) {
	if s.err != nil {
		return serverapi.ProcessKillResponse{}, s.err
	}
	s.killed = append(s.killed, req.ProcessID)
	return serverapi.ProcessKillResponse{}, nil
}

func (s *stubEmbeddedProcessControlClient) GetInlineOutput(context.Context, serverapi.ProcessInlineOutputRequest) (serverapi.ProcessInlineOutputResponse, error) {
	if s.err != nil {
		return serverapi.ProcessInlineOutputResponse{}, s.err
	}
	return s.inlineResp, nil
}

func TestEmbeddedAppServerPrepareRuntimeRegistersRuntimeForSessionViews(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "sk-test")

	server, err := startEmbeddedServer(context.Background(), Options{WorkspaceRoot: workspace}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("start embedded server: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(plan, io.Discard, "test prepare runtime")
	if err != nil {
		t.Fatalf("prepare runtime: %v", err)
	}
	defer runtimePlan.Close()
	if err := runtimePlan.Wiring.runtimeControls.SetThinkingLevel(context.Background(), serverapi.RuntimeSetThinkingLevelRequest{SessionID: plan.SessionID, Level: "high"}); err != nil {
		t.Fatalf("set thinking level: %v", err)
	}

	resp, err := server.SessionViewClient().GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: plan.SessionID})
	if err != nil {
		t.Fatalf("get session main view while runtime attached: %v", err)
	}
	if resp.MainView.Session.SessionID != plan.SessionID {
		t.Fatalf("session id = %q, want %q", resp.MainView.Session.SessionID, plan.SessionID)
	}
	if resp.MainView.Status.ThinkingLevel != "high" {
		t.Fatalf("thinking level = %q, want high", resp.MainView.Status.ThinkingLevel)
	}
}

func TestEmbeddedAppServerPrepareRuntimeWiresProcessReadsForUIHydration(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "sk-test")

	server, err := startEmbeddedServer(context.Background(), Options{WorkspaceRoot: workspace}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("start embedded server: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(plan, io.Discard, "test prepare runtime process reads")
	if err != nil {
		t.Fatalf("prepare runtime: %v", err)
	}
	defer runtimePlan.Close()
	if runtimePlan.Wiring.processViews == nil {
		t.Fatal("expected PrepareRuntime to wire process view client")
	}

	manager := server.inner.Background()
	if manager == nil {
		t.Fatal("expected server background manager")
	}
	manager.SetMinimumExecToBgTime(250 * time.Millisecond)
	res, err := manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "printf 'local\n'; sleep 1"},
		DisplayCommand: "local-process",
		OwnerSessionID: plan.SessionID,
		OwnerRunID:     "local-run",
		OwnerStepID:    "local-step",
		Workdir:        workspace,
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start background process: %v", err)
	}
	if !res.Backgrounded {
		t.Fatal("expected backgrounded local process")
	}

	runtimePlan.Wiring.processViews = &stubEmbeddedProcessViewClient{listResp: serverapi.ProcessListResponse{Processes: []clientui.BackgroundProcess{{
		ID:             "remote-proc",
		OwnerSessionID: plan.SessionID,
		OwnerRunID:     "remote-run",
		OwnerStepID:    "remote-step",
		Command:        "remote-process",
	}}}}

	processClient := newUIProcessClientWithReads(nil, runtimePlan.Wiring.processViews, runtimePlan.Wiring.processControls)
	got := processClient.ListProcesses()
	if len(got) != 1 || got[0].ID != "remote-proc" || got[0].OwnerRunID != "remote-run" || got[0].OwnerStepID != "remote-step" {
		t.Fatalf("expected shared process reads to win over local manager snapshot, got %+v", got)
	}
}

func TestEmbeddedAppServerPrepareRuntimeExposesPendingAsksAndApprovals(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "sk-test")

	server, err := startEmbeddedServer(context.Background(), Options{WorkspaceRoot: workspace}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("start embedded server: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(plan, io.Discard, "test prepare runtime pending prompts")
	if err != nil {
		t.Fatalf("prepare runtime: %v", err)
	}
	defer runtimePlan.Close()
	if runtimePlan.Wiring.askViews == nil || runtimePlan.Wiring.approvalViews == nil || runtimePlan.Wiring.promptControl == nil {
		t.Fatal("expected PrepareRuntime to wire shared prompt clients")
	}
}

func waitForPendingAskResources(t *testing.T, client client.AskViewClient, sessionID string, want int) []clientui.PendingAsk {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.ListPendingAsksBySession(context.Background(), serverapi.AskListPendingBySessionRequest{SessionID: sessionID})
		if err != nil {
			t.Fatalf("ListPendingAsksBySession: %v", err)
		}
		if len(resp.Asks) == want {
			return resp.Asks
		}
		time.Sleep(10 * time.Millisecond)
	}
	resp, err := client.ListPendingAsksBySession(context.Background(), serverapi.AskListPendingBySessionRequest{SessionID: sessionID})
	if err != nil {
		t.Fatalf("ListPendingAsksBySession final: %v", err)
	}
	t.Fatalf("timed out waiting for %d pending asks, got %+v", want, resp.Asks)
	return nil
}

func waitForPendingApprovalResources(t *testing.T, client client.ApprovalViewClient, sessionID string, want int) []clientui.PendingApproval {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.ListPendingApprovalsBySession(context.Background(), serverapi.ApprovalListPendingBySessionRequest{SessionID: sessionID})
		if err != nil {
			t.Fatalf("ListPendingApprovalsBySession: %v", err)
		}
		if len(resp.Approvals) == want {
			return resp.Approvals
		}
		time.Sleep(10 * time.Millisecond)
	}
	resp, err := client.ListPendingApprovalsBySession(context.Background(), serverapi.ApprovalListPendingBySessionRequest{SessionID: sessionID})
	if err != nil {
		t.Fatalf("ListPendingApprovalsBySession final: %v", err)
	}
	t.Fatalf("timed out waiting for %d pending approvals, got %+v", want, resp.Approvals)
	return nil
}

func TestEmbeddedAppServerPrepareRuntimeWiresSessionActivityForSharedClients(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "sk-test")

	server, err := startEmbeddedServer(context.Background(), Options{WorkspaceRoot: workspace}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("start embedded server: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(plan, io.Discard, "test prepare runtime session activity")
	if err != nil {
		t.Fatalf("prepare runtime: %v", err)
	}
	defer runtimePlan.Close()

	reads := server.SessionViewClient()
	if reads == nil {
		t.Fatal("expected session view client")
	}
	hydrated, err := reads.GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: plan.SessionID})
	if err != nil {
		t.Fatalf("GetSessionMainView: %v", err)
	}
	if hydrated.MainView.Session.SessionID != plan.SessionID {
		t.Fatalf("unexpected hydrated session: %+v", hydrated.MainView.Session)
	}

	activity := server.inner.SessionActivityClient()
	if activity == nil {
		t.Fatal("expected session activity client")
	}
	first, err := activity.SubscribeSessionActivity(context.Background(), serverapi.SessionActivitySubscribeRequest{SessionID: plan.SessionID})
	if err != nil {
		t.Fatalf("SubscribeSessionActivity first: %v", err)
	}
	defer func() { _ = first.Close() }()
	second, err := activity.SubscribeSessionActivity(context.Background(), serverapi.SessionActivitySubscribeRequest{SessionID: plan.SessionID})
	if err != nil {
		t.Fatalf("SubscribeSessionActivity second: %v", err)
	}
	defer func() { _ = second.Close() }()

	runtimePlan.Wiring.runtimeClient.AppendLocalEntry("user", "hello from client one")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	firstEvt, err := first.Next(ctx)
	if err != nil {
		t.Fatalf("first.Next: %v", err)
	}
	secondEvt, err := second.Next(ctx)
	if err != nil {
		t.Fatalf("second.Next: %v", err)
	}
	if firstEvt.Kind != clientui.EventConversationUpdated || secondEvt.Kind != clientui.EventConversationUpdated {
		t.Fatalf("unexpected activity events: first=%+v second=%+v", firstEvt, secondEvt)
	}

	refreshed, err := reads.GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: plan.SessionID})
	if err != nil {
		t.Fatalf("GetSessionMainView refreshed: %v", err)
	}
	if len(refreshed.MainView.Session.Chat.Entries) == 0 {
		t.Fatalf("expected hydrated chat entries after activity: %+v", refreshed.MainView.Session.Chat)
	}
	last := refreshed.MainView.Session.Chat.Entries[len(refreshed.MainView.Session.Chat.Entries)-1]
	if last.Text != "hello from client one" {
		t.Fatalf("unexpected hydrated entry: %+v", last)
	}
}

func TestEmbeddedAppServerPrepareRuntimeWiresProcessControlForUIActions(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "sk-test")

	server, err := startEmbeddedServer(context.Background(), Options{WorkspaceRoot: workspace}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("start embedded server: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(plan, io.Discard, "test prepare runtime process control")
	if err != nil {
		t.Fatalf("prepare runtime: %v", err)
	}
	defer runtimePlan.Close()
	if runtimePlan.Wiring.processControls == nil {
		t.Fatal("expected PrepareRuntime to wire process control client")
	}

	controls := &stubEmbeddedProcessControlClient{inlineResp: serverapi.ProcessInlineOutputResponse{Output: "remote preview", LogPath: "/tmp/remote.log"}}
	runtimePlan.Wiring.processControls = controls
	processClient := newUIProcessClientWithReads(nil, runtimePlan.Wiring.processViews, runtimePlan.Wiring.processControls)

	preview, logPath, err := processClient.InlineOutput("proc-1", 12_000)
	if err != nil {
		t.Fatalf("InlineOutput: %v", err)
	}
	if preview != "remote preview" || logPath != "/tmp/remote.log" {
		t.Fatalf("unexpected inline output payload preview=%q logPath=%q", preview, logPath)
	}
	if err := processClient.KillProcess("proc-1"); err != nil {
		t.Fatalf("KillProcess: %v", err)
	}
	if len(controls.killed) != 1 || controls.killed[0] != "proc-1" {
		t.Fatalf("expected shared process control client to handle kill, got %+v", controls.killed)
	}
}

func TestEmbeddedAppServerPrepareRuntimeWiresProcessOutputClient(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "sk-test")

	server, err := startEmbeddedServer(context.Background(), Options{WorkspaceRoot: workspace}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("start embedded server: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(plan, io.Discard, "test prepare runtime process output")
	if err != nil {
		t.Fatalf("prepare runtime: %v", err)
	}
	defer runtimePlan.Close()
	if runtimePlan.Wiring.processOutput == nil {
		t.Fatal("expected PrepareRuntime to wire process output client")
	}
}

func TestEmbeddedAppServerPrepareRuntimeUsesPrimaryRunGuardedRuntimeClient(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "sk-test")

	server, err := startEmbeddedServer(context.Background(), Options{WorkspaceRoot: workspace}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("start embedded server: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(plan, io.Discard, "test prepare runtime primary run gate")
	if err != nil {
		t.Fatalf("prepare runtime: %v", err)
	}
	defer runtimePlan.Close()
	if runtimePlan.Wiring.runtimeClient == nil {
		t.Fatal("expected PrepareRuntime to wire guarded runtime client")
	}

	lease, err := server.inner.AcquirePrimaryRun(plan.SessionID)
	if err != nil {
		t.Fatalf("AcquirePrimaryRun: %v", err)
	}
	defer lease.Release()
	if _, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "hello"); !errors.Is(err, primaryrun.ErrActivePrimaryRun) {
		t.Fatalf("SubmitUserMessage error = %v, want active primary run", err)
	}
}

func TestEmbeddedAppServerPrepareRuntimeRejectsConcurrentPrimarySubmitWhileRunInFlight(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	var requests atomic.Int32
	responseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got == "" {
			t.Fatal("expected authorization header")
		}
		index := int(requests.Add(1))
		switch index {
		case 1:
			close(firstStarted)
			<-firstRelease
		case 2:
		default:
			t.Fatalf("unexpected responses request index %d", index)
		}
		reply := map[int]string{1: "first reply", 2: "second reply"}[index]
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":11,\"output_tokens\":7,\"total_tokens\":18},\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}]}}\n\n", reply)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer responseServer.Close()

	server, err := startEmbeddedServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         responseServer.URL,
		OpenAIBaseURLExplicit: true,
	}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("start embedded server: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(plan, io.Discard, "test prepare runtime in-flight primary run gate")
	if err != nil {
		t.Fatalf("prepare runtime: %v", err)
	}
	defer runtimePlan.Close()

	type submitResult struct {
		message string
		err     error
	}
	firstDone := make(chan submitResult, 1)
	go func() {
		message, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "first prompt")
		firstDone <- submitResult{message: message, err: err}
	}()

	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first submit to start")
	}

	if _, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "second prompt"); !errors.Is(err, primaryrun.ErrActivePrimaryRun) {
		t.Fatalf("second SubmitUserMessage error = %v, want active primary run", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("responses request count during rejected concurrent submit = %d, want 1", got)
	}

	close(firstRelease)
	select {
	case result := <-firstDone:
		if result.err != nil {
			t.Fatalf("first SubmitUserMessage error: %v", result.err)
		}
		if result.message != "first reply" {
			t.Fatalf("first SubmitUserMessage message = %q, want first reply", result.message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first submit to finish")
	}

	message, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "third prompt")
	if err != nil {
		t.Fatalf("third SubmitUserMessage error: %v", err)
	}
	if message != "second reply" {
		t.Fatalf("third SubmitUserMessage message = %q, want second reply", message)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("responses request count after third submit = %d, want 2", got)
	}
}
