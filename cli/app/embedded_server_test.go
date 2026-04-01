package app

import (
	"context"
	"errors"
	"io"
	"testing"

	"builder/server/auth"
	serverembedded "builder/server/embedded"
	"builder/server/launch"
	serverlifecycle "builder/server/lifecycle"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/sessioncontrol"
	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/config"
	"builder/shared/serverapi"
)

type testEmbeddedServer struct {
	cfg               config.App
	containerDir      string
	oauthOpts         auth.OpenAIOAuthOptions
	authManager       *auth.Manager
	fastModeState     *runtime.FastModeState
	background        *shelltool.Manager
	backgroundRouter  serverembedded.BackgroundRouter
	runPromptClient   client.RunPromptClient
	processViewClient client.ProcessViewClient
	sessionViewClient client.SessionViewClient
	planSession       func(req sessionLaunchRequest, pick sessionPickerRunner) (sessionLaunchPlan, error)
	prepareRuntime    func(plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string) (*runtimeLaunchPlan, error)
	resolveAction     func(ctx context.Context, interactor authInteractor, store *session.Store, transition UITransition) (resolvedSessionAction, error)
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
