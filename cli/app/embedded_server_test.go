package app

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"builder/server/auth"
	serverembedded "builder/server/embedded"
	"builder/server/launch"
	serverlifecycle "builder/server/lifecycle"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/sessioncontrol"
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
	processControlClient client.ProcessControlClient
	processViewClient    client.ProcessViewClient
	sessionViewClient    client.SessionViewClient
	planSession          func(req sessionLaunchRequest, pick sessionPickerRunner) (sessionLaunchPlan, error)
	prepareRuntime       func(plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string) (*runtimeLaunchPlan, error)
	resolveAction        func(ctx context.Context, interactor authInteractor, store *session.Store, transition UITransition) (resolvedSessionAction, error)
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

func (s *testEmbeddedServer) Close() error                          { return nil }
func (s *testEmbeddedServer) Config() config.App                    { return s.cfg }
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
func (s *testEmbeddedServer) ProcessViewClient() client.ProcessViewClient {
	return s.processViewClient
}
func (s *testEmbeddedServer) SessionViewClient() client.SessionViewClient {
	return s.sessionViewClient
}
func (s *testEmbeddedServer) PlanSession(req sessionLaunchRequest, pick sessionPickerRunner) (sessionLaunchPlan, error) {
	if s.planSession != nil {
		return s.planSession(req, pick)
	}
	controller := sessioncontrol.Controller{
		Config:       s.cfg,
		ContainerDir: s.containerDir,
		AuthManager:  s.authManager,
		PickSession: func(summaries []session.Summary, theme string, alternateScreenPolicy config.TUIAlternateScreenPolicy) (launch.SessionSelection, error) {
			runPicker := pick
			if runPicker == nil {
				runPicker = func(summaries []session.Summary, theme string, alternateScreenPolicy config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
					return runSessionPicker(summaries, theme, alternateScreenPolicy)
				}
			}
			picked, err := runPicker(summaries, theme, alternateScreenPolicy)
			if err != nil {
				return launch.SessionSelection{}, err
			}
			return launch.SessionSelection{Session: picked.Session, CreateNew: picked.CreateNew, Canceled: picked.Canceled}, nil
		},
	}
	serverPlan, err := controller.PlanSession(launch.SessionRequest{
		Mode:              launch.Mode(req.Mode),
		SelectedSessionID: req.SelectedSessionID,
		ForceNewSession:   req.ForceNewSession,
		ParentSessionID:   req.ParentSessionID,
	})
	if err != nil {
		return sessionLaunchPlan{}, err
	}
	return sessionLaunchPlan{
		Mode:                req.Mode,
		Store:               serverPlan.Store,
		ActiveSettings:      serverPlan.ActiveSettings,
		EnabledTools:        serverPlan.EnabledTools,
		ConfiguredModelName: serverPlan.ConfiguredModelName,
		SessionName:         serverPlan.SessionName,
		ModelContractLocked: serverPlan.ModelContractLocked,
		StatusConfig: uiStatusConfig{
			WorkspaceRoot:   s.cfg.WorkspaceRoot,
			PersistenceRoot: s.cfg.PersistenceRoot,
			Settings:        serverPlan.ActiveSettings,
			Source:          serverPlan.Source,
			AuthManager:     s.authManager,
			AuthStatePath:   config.GlobalAuthConfigPath(s.cfg),
		},
		WorkspaceRoot: serverPlan.WorkspaceRoot,
		Source:        serverPlan.Source,
	}, nil
}
func (s *testEmbeddedServer) PrepareRuntime(plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string) (*runtimeLaunchPlan, error) {
	if s.prepareRuntime != nil {
		return s.prepareRuntime(plan, diagnosticWriter, startLogLine)
	}
	return nil, errors.New("test embedded server prepare runtime not configured")
}
func (s *testEmbeddedServer) ResolveTransition(ctx context.Context, interactor authInteractor, store *session.Store, transition UITransition) (resolvedSessionAction, error) {
	if s.resolveAction != nil {
		return s.resolveAction(ctx, interactor, store, transition)
	}
	controller := sessioncontrol.Controller{
		Config:       s.cfg,
		ContainerDir: s.containerDir,
		AuthManager:  s.authManager,
		Reauth: func(ctx context.Context) error {
			return ensureAuthReady(ctx, s.authManager, s.oauthOpts, s.cfg.Settings.Theme, s.cfg.Settings.TUIAlternateScreen, interactor)
		},
	}
	resolved, err := controller.ResolveTransition(ctx, store, serverlifecycle.Transition{
		Action:               serverlifecycle.Action(transition.Action),
		InitialPrompt:        transition.InitialPrompt,
		InitialInput:         transition.InitialInput,
		TargetSessionID:      transition.TargetSessionID,
		ForkUserMessageIndex: transition.ForkUserMessageIndex,
		ParentSessionID:      transition.ParentSessionID,
	})
	if err != nil {
		return resolvedSessionAction{}, err
	}
	return resolvedSessionAction{
		NextSessionID:   resolved.NextSessionID,
		InitialPrompt:   resolved.InitialPrompt,
		InitialInput:    resolved.InitialInput,
		ParentSessionID: resolved.ParentSessionID,
		ForceNewSession: resolved.ForceNewSession,
		ShouldContinue:  resolved.ShouldContinue,
	}, nil
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
	plan, err := planner.PlanSession(sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(plan, io.Discard, "test prepare runtime")
	if err != nil {
		t.Fatalf("prepare runtime: %v", err)
	}
	defer runtimePlan.Close()
	if err := runtimePlan.Wiring.engine.SetThinkingLevel("high"); err != nil {
		t.Fatalf("set thinking level: %v", err)
	}

	resp, err := server.SessionViewClient().GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: plan.Store.Meta().SessionID})
	if err != nil {
		t.Fatalf("get session main view while runtime attached: %v", err)
	}
	if resp.MainView.Session.SessionID != plan.Store.Meta().SessionID {
		t.Fatalf("session id = %q, want %q", resp.MainView.Session.SessionID, plan.Store.Meta().SessionID)
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
	plan, err := planner.PlanSession(sessionLaunchRequest{Mode: launchModeInteractive})
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

	manager := runtimePlan.Wiring.background
	if manager == nil {
		t.Fatal("expected background manager")
	}
	manager.SetMinimumExecToBgTime(250 * time.Millisecond)
	res, err := manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "printf 'local\n'; sleep 1"},
		DisplayCommand: "local-process",
		OwnerSessionID: plan.Store.Meta().SessionID,
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
		OwnerSessionID: plan.Store.Meta().SessionID,
		OwnerRunID:     "remote-run",
		OwnerStepID:    "remote-step",
		Command:        "remote-process",
	}}}}

	processClient := newUIProcessClientWithReads(runtimePlan.Wiring.background, runtimePlan.Wiring.processViews, runtimePlan.Wiring.processControls)
	got := processClient.ListProcesses()
	if len(got) != 1 || got[0].ID != "remote-proc" || got[0].OwnerRunID != "remote-run" || got[0].OwnerStepID != "remote-step" {
		t.Fatalf("expected shared process reads to win over local manager snapshot, got %+v", got)
	}
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
	plan, err := planner.PlanSession(sessionLaunchRequest{Mode: launchModeInteractive})
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
	hydrated, err := reads.GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: plan.Store.Meta().SessionID})
	if err != nil {
		t.Fatalf("GetSessionMainView: %v", err)
	}
	if hydrated.MainView.Session.SessionID != plan.Store.Meta().SessionID {
		t.Fatalf("unexpected hydrated session: %+v", hydrated.MainView.Session)
	}

	activity := server.inner.SessionActivityClient()
	if activity == nil {
		t.Fatal("expected session activity client")
	}
	first, err := activity.SubscribeSessionActivity(context.Background(), serverapi.SessionActivitySubscribeRequest{SessionID: plan.Store.Meta().SessionID})
	if err != nil {
		t.Fatalf("SubscribeSessionActivity first: %v", err)
	}
	defer func() { _ = first.Close() }()
	second, err := activity.SubscribeSessionActivity(context.Background(), serverapi.SessionActivitySubscribeRequest{SessionID: plan.Store.Meta().SessionID})
	if err != nil {
		t.Fatalf("SubscribeSessionActivity second: %v", err)
	}
	defer func() { _ = second.Close() }()

	runtimePlan.Wiring.engine.AppendLocalEntry("user", "hello from client one")

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

	refreshed, err := reads.GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: plan.Store.Meta().SessionID})
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
	plan, err := planner.PlanSession(sessionLaunchRequest{Mode: launchModeInteractive})
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
	processClient := newUIProcessClientWithReads(runtimePlan.Wiring.background, runtimePlan.Wiring.processViews, runtimePlan.Wiring.processControls)

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
