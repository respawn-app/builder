package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/serve"
	serverstartup "builder/server/startup"
	askquestion "builder/server/tools/askquestion"
	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/protocol"
	"builder/shared/serverapi"
	"builder/shared/toolspec"

	"github.com/google/uuid"
	"golang.org/x/net/websocket"
)

func TestStartSessionServerHelperDaemonProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_DAEMON") != "1" {
		return
	}
	workspace := strings.TrimSpace(os.Getenv("GO_HELPER_WORKSPACE_ROOT"))
	if workspace == "" {
		t.Fatal("GO_HELPER_WORKSPACE_ROOT is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
	}, memoryAuthHandler{state: auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "test-key"},
		},
		UpdatedAt: time.Now().UTC(),
	}}, autoOnboarding{})
	if err != nil {
		t.Fatalf("serve.Start: %v", err)
	}
	defer func() { _ = srv.Close() }()
	if err := srv.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve: %v", err)
	}
}

func TestStartSessionServerUsesConfiguredDaemonForInteractiveFlow(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	fakeResponses, hits := newFakeResponsesServer(t, []string{"interactive daemon reply"})
	defer fakeResponses.Close()

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         fakeResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, memoryAuthHandler{state: auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "test-key"},
		},
		UpdatedAt: time.Now().UTC(),
	}}, autoOnboarding{})
	if err != nil {
		t.Fatalf("serve.Start: %v", err)
	}
	defer func() { _ = srv.Close() }()

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(serveCtx)
	}()
	waitForConfiguredRemoteIdentity(t, workspace)

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()
	if _, ok := server.(*remoteAppServer); !ok {
		t.Fatalf("expected remote app server, got %T", server)
	}

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "test remote interactive runtime")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	message, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "hello through interactive daemon")
	if err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}
	if message != "interactive daemon reply" {
		t.Fatalf("assistant message = %q, want %q", message, "interactive daemon reply")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected daemon-backed llm call once, got %d", hits.Load())
	}

	refreshed, err := server.SessionViewClient().GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: plan.SessionID})
	if err != nil {
		t.Fatalf("GetSessionMainView: %v", err)
	}
	if refreshed.MainView.Session.Transcript.CommittedEntryCount == 0 {
		t.Fatalf("expected refreshed transcript metadata, got %+v", refreshed.MainView.Session.Transcript)
	}

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestRemoteInteractiveRuntimeTwoClientsConvergeOnSameSessionAcrossWorkspaces(t *testing.T) {
	fakeResponses, hits := newFakeResponsesServer(t, []string{"shared daemon reply"})
	defer fakeResponses.Close()
	fixture := startRemoteMultiClientRuntimeFixture(t, fakeResponses.URL)

	message, err := fixture.runtimePlanA.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "hello from client A")
	if err != nil {
		t.Fatalf("SubmitUserMessage A: %v", err)
	}
	if message != "shared daemon reply" {
		t.Fatalf("assistant message = %q, want %q", message, "shared daemon reply")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected one daemon-backed llm call, got %d", hits.Load())
	}

	pageA := waitForRemoteTranscriptPage(t, fixture.serverA.SessionViewClient(), fixture.planA.SessionID, func(page clientui.TranscriptPage) bool {
		return transcriptPageContainsAssistantText(page, "shared daemon reply")
	})
	pageB := waitForRemoteTranscriptPage(t, fixture.serverB.SessionViewClient(), fixture.planA.SessionID, func(page clientui.TranscriptPage) bool {
		return transcriptPageContainsAssistantText(page, "shared daemon reply")
	})

	if pageA.Revision != pageB.Revision {
		t.Fatalf("expected clients to converge on same transcript revision, a=%d b=%d", pageA.Revision, pageB.Revision)
	}
	if pageA.TotalEntries != pageB.TotalEntries {
		t.Fatalf("expected clients to converge on same transcript size, a=%d b=%d", pageA.TotalEntries, pageB.TotalEntries)
	}
	if !transcriptPageContainsAssistantText(pageA, "shared daemon reply") || !transcriptPageContainsAssistantText(pageB, "shared daemon reply") {
		t.Fatalf("expected both clients to hydrate assistant reply, pageA=%+v pageB=%+v", pageA, pageB)
	}
}

func TestRemoteReadOnlyClientHydratesCommittedTranscriptAcrossWorkspaces(t *testing.T) {
	fakeResponses, hits := newFakeResponsesServer(t, []string{"reply while client B disconnected", "reply after client B reconnects"})
	defer fakeResponses.Close()
	fixture := startRemoteMultiClientRuntimeFixture(t, fakeResponses.URL)

	firstMessage, err := fixture.runtimePlanA.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "message while client B is disconnected")
	if err != nil {
		t.Fatalf("SubmitUserMessage before reconnect: %v", err)
	}
	if firstMessage != "reply while client B disconnected" {
		t.Fatalf("assistant message before reconnect = %q, want %q", firstMessage, "reply while client B disconnected")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected one daemon-backed llm call before reconnect, got %d", hits.Load())
	}
	pageA1 := waitForRemoteTranscriptPage(t, fixture.serverA.SessionViewClient(), fixture.planA.SessionID, func(page clientui.TranscriptPage) bool {
		return transcriptPageContainsAssistantText(page, "reply while client B disconnected")
	})

	hydratedB := waitForRemoteTranscriptPage(t, fixture.serverB.SessionViewClient(), fixture.planA.SessionID, func(page clientui.TranscriptPage) bool {
		return transcriptPageContainsAssistantText(page, "reply while client B disconnected")
	})
	if !transcriptPageContainsAssistantText(hydratedB, "reply while client B disconnected") {
		t.Fatalf("expected reconnecting client to hydrate missed committed reply, got %+v", hydratedB)
	}
	if hydratedB.Revision != pageA1.Revision || hydratedB.TotalEntries != pageA1.TotalEntries {
		t.Fatalf("expected reconnect hydrate to match authoritative transcript head, hydrated=%+v pageA=%+v", hydratedB, pageA1)
	}

	secondMessage, err := fixture.runtimePlanA.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "message after client B reconnects")
	if err != nil {
		t.Fatalf("SubmitUserMessage after reconnect: %v", err)
	}
	if secondMessage != "reply after client B reconnects" {
		t.Fatalf("assistant message after reconnect = %q, want %q", secondMessage, "reply after client B reconnects")
	}
	if hits.Load() != 2 {
		t.Fatalf("expected two daemon-backed llm calls after reconnect flow, got %d", hits.Load())
	}

	pageA2 := waitForRemoteTranscriptPage(t, fixture.serverA.SessionViewClient(), fixture.planA.SessionID, func(page clientui.TranscriptPage) bool {
		return transcriptPageContainsAssistantText(page, "reply after client B reconnects")
	})
	pageB2 := waitForRemoteTranscriptPage(t, fixture.serverB.SessionViewClient(), fixture.planA.SessionID, func(page clientui.TranscriptPage) bool {
		return transcriptPageContainsAssistantText(page, "reply after client B reconnects")
	})
	if pageA2.Revision != pageB2.Revision || pageA2.TotalEntries != pageB2.TotalEntries {
		t.Fatalf("expected both clients to converge after read-only hydrate, a=%+v b=%+v", pageA2, pageB2)
	}
}

func TestRemoteInteractiveRuntimeAskAnswersRequireControllerLeaseAcrossWorkspaces(t *testing.T) {
	fixture := startRemoteMultiClientRuntimeFixture(t, "")

	askDone := make(chan struct {
		resp askquestion.Response
		err  error
	}, 1)
	go func() {
		resp, err := fixture.daemon.AwaitPromptResponse(context.Background(), fixture.planA.SessionID, askquestion.Request{
			ID:       "ask-race-1",
			Question: "Who answers first?",
		})
		askDone <- struct {
			resp askquestion.Response
			err  error
		}{resp: resp, err: err}
	}()

	askEvtA := waitForRemoteAskEvent(t, fixture.runtimePlanA.Wiring.askEvents)
	if askEvtA.req.ID != "ask-race-1" || askEvtA.req.Question != "Who answers first?" || askEvtA.req.Approval {
		t.Fatalf("unexpected ask event: %+v", askEvtA.req)
	}
	waitForPendingAskResources(t, fixture.serverB.AskViewClient(), fixture.planA.SessionID, 1)

	if err := fixture.serverB.PromptControlClient().AnswerAsk(context.Background(), serverapi.AskAnswerRequest{
		ClientRequestID:   uuid.NewString(),
		SessionID:         fixture.planA.SessionID,
		ControllerLeaseID: "invalid-lease",
		AskID:             "ask-race-1",
		Answer:            "answer from client B",
	}); !errors.Is(err, serverapi.ErrInvalidControllerLease) {
		t.Fatalf("expected invalid controller lease for read-only client, got %v", err)
	}
	if err := fixture.serverA.PromptControlClient().AnswerAsk(context.Background(), serverapi.AskAnswerRequest{
		ClientRequestID:   uuid.NewString(),
		SessionID:         fixture.planA.SessionID,
		ControllerLeaseID: fixture.runtimePlanA.ControllerLeaseID,
		AskID:             "ask-race-1",
		Answer:            "answer from client A",
	}); err != nil {
		t.Fatalf("AnswerAsk controller: %v", err)
	}

	select {
	case result := <-askDone:
		if result.err != nil {
			t.Fatalf("AwaitPromptResponse ask: %v", result.err)
		}
		if result.resp.RequestID != "ask-race-1" || result.resp.Answer != "answer from client A" {
			t.Fatalf("unexpected ask response: %+v", result.resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ask response")
	}
	waitForPendingAskResources(t, fixture.serverA.AskViewClient(), fixture.planA.SessionID, 0)
	waitForPendingAskResources(t, fixture.serverB.AskViewClient(), fixture.planA.SessionID, 0)
}

func TestRemoteInteractiveRuntimeApprovalAnswersRequireControllerLeaseAcrossWorkspaces(t *testing.T) {
	fixture := startRemoteMultiClientRuntimeFixture(t, "")

	approvalDone := make(chan struct {
		resp askquestion.Response
		err  error
	}, 1)
	go func() {
		resp, err := fixture.daemon.AwaitPromptResponse(context.Background(), fixture.planA.SessionID, askquestion.Request{
			ID:              "approval-race-1",
			Question:        "Allow the command?",
			Approval:        true,
			ApprovalOptions: []askquestion.ApprovalOption{{Decision: askquestion.ApprovalDecisionAllowOnce, Label: "Allow once"}, {Decision: askquestion.ApprovalDecisionDeny, Label: "Deny"}},
		})
		approvalDone <- struct {
			resp askquestion.Response
			err  error
		}{resp: resp, err: err}
	}()

	approvalEvtA := waitForRemoteAskEvent(t, fixture.runtimePlanA.Wiring.askEvents)
	if approvalEvtA.req.ID != "approval-race-1" || approvalEvtA.req.Question != "Allow the command?" || !approvalEvtA.req.Approval {
		t.Fatalf("unexpected approval event: %+v", approvalEvtA.req)
	}
	waitForPendingApprovalResources(t, fixture.serverB.ApprovalViewClient(), fixture.planA.SessionID, 1)

	if err := fixture.serverB.PromptControlClient().AnswerApproval(context.Background(), serverapi.ApprovalAnswerRequest{
		ClientRequestID:   uuid.NewString(),
		SessionID:         fixture.planA.SessionID,
		ControllerLeaseID: "invalid-lease",
		ApprovalID:        "approval-race-1",
		Decision:          clientui.ApprovalDecisionDeny,
		Commentary:        "denied by client B",
	}); !errors.Is(err, serverapi.ErrInvalidControllerLease) {
		t.Fatalf("expected invalid controller lease for read-only client, got %v", err)
	}
	if err := fixture.serverA.PromptControlClient().AnswerApproval(context.Background(), serverapi.ApprovalAnswerRequest{
		ClientRequestID:   uuid.NewString(),
		SessionID:         fixture.planA.SessionID,
		ControllerLeaseID: fixture.runtimePlanA.ControllerLeaseID,
		ApprovalID:        "approval-race-1",
		Decision:          clientui.ApprovalDecisionAllowOnce,
		Commentary:        "approved by client A",
	}); err != nil {
		t.Fatalf("AnswerApproval controller: %v", err)
	}

	select {
	case result := <-approvalDone:
		if result.err != nil {
			t.Fatalf("AwaitPromptResponse approval: %v", result.err)
		}
		if result.resp.RequestID != "approval-race-1" || result.resp.Approval == nil {
			t.Fatalf("unexpected approval response: %+v", result.resp)
		}
		if result.resp.Approval.Decision != askquestion.ApprovalDecisionAllowOnce || result.resp.Approval.Commentary != "approved by client A" {
			t.Fatalf("unexpected approval response: %+v", result.resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for approval response")
	}
	waitForPendingApprovalResources(t, fixture.serverA.ApprovalViewClient(), fixture.planA.SessionID, 0)
	waitForPendingApprovalResources(t, fixture.serverB.ApprovalViewClient(), fixture.planA.SessionID, 0)
}

func TestRemoteSessionActivitySlowSubscriberGapHydratesAndResubscribesAcrossWorkspaces(t *testing.T) {
	// One prompt does not emit enough session-activity events to deterministically overflow the
	// server-side subscription buffer plus the client-side relay buffer. Keep this flood size above
	// that combined capacity so the test proves a real remote ErrStreamGap instead of timing luck.
	const floodPromptCount = 320
	replies := make([]string, 0, floodPromptCount+1)
	for i := 0; i < floodPromptCount; i++ {
		replies = append(replies, "flood reply")
	}
	replies = append(replies, "reply after gap recovery")

	fakeResponses, hits := newFakeResponsesServer(t, replies)
	defer fakeResponses.Close()
	fixture := startRemoteMultiClientRuntimeFixture(t, fakeResponses.URL)

	laggingSub, err := fixture.serverB.SessionActivityClient().SubscribeSessionActivity(context.Background(), serverapi.SessionActivitySubscribeRequest{SessionID: fixture.planA.SessionID})
	if err != nil {
		t.Fatalf("SubscribeSessionActivity lagging client: %v", err)
	}
	defer func() { _ = laggingSub.Close() }()

	for i := 0; i < floodPromptCount; i++ {
		message, err := fixture.runtimePlanA.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "flood the lagging subscriber")
		if err != nil {
			t.Fatalf("SubmitUserMessage flood %d: %v", i, err)
		}
		if message != "flood reply" {
			t.Fatalf("assistant message flood %d = %q, want %q", i, message, "flood reply")
		}
	}
	if hits.Load() != floodPromptCount {
		t.Fatalf("expected %d daemon-backed llm calls during flood, got %d", floodPromptCount, hits.Load())
	}

	gapErr := waitForSessionActivityGap(t, laggingSub)
	if !errors.Is(gapErr, serverapi.ErrStreamGap) {
		t.Fatalf("expected remote slow subscriber to fail with stream gap, got %v", gapErr)
	}

	pageA := waitForRemoteTranscriptPage(t, fixture.serverA.SessionViewClient(), fixture.planA.SessionID, func(page clientui.TranscriptPage) bool {
		return page.TotalEntries >= floodPromptCount*2
	})
	pageB := waitForRemoteTranscriptPage(t, fixture.serverB.SessionViewClient(), fixture.planA.SessionID, func(page clientui.TranscriptPage) bool {
		return page.Revision == pageA.Revision && page.TotalEntries == pageA.TotalEntries
	})
	if pageA.Revision != pageB.Revision || pageA.TotalEntries != pageB.TotalEntries {
		t.Fatalf("expected authoritative transcript hydrate to converge after stream gap, a=%+v b=%+v", pageA, pageB)
	}

	recoveredSub, err := fixture.serverB.SessionActivityClient().SubscribeSessionActivity(context.Background(), serverapi.SessionActivitySubscribeRequest{SessionID: fixture.planA.SessionID})
	if err != nil {
		t.Fatalf("SubscribeSessionActivity recovered client: %v", err)
	}
	defer func() { _ = recoveredSub.Close() }()

	message, err := fixture.runtimePlanA.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "message after lagging subscriber recovers")
	if err != nil {
		t.Fatalf("SubmitUserMessage after gap recovery: %v", err)
	}
	if message != "reply after gap recovery" {
		t.Fatalf("assistant message after gap recovery = %q, want %q", message, "reply after gap recovery")
	}
	if hits.Load() != floodPromptCount+1 {
		t.Fatalf("expected %d daemon-backed llm calls after recovery message, got %d", floodPromptCount+1, hits.Load())
	}

	assistantEvt := waitForSessionActivitySubscriptionEvent(t, recoveredSub, "assistant message after gap recovery", func(evt clientui.Event) bool {
		if evt.Kind != clientui.EventAssistantMessage {
			return false
		}
		for _, entry := range evt.TranscriptEntries {
			if entry.Role == "assistant" && entry.Text == "reply after gap recovery" {
				return true
			}
		}
		return false
	})
	if assistantEvt.StepID == "" {
		t.Fatalf("expected assistant event step id after gap recovery, got %+v", assistantEvt)
	}

	pageA2 := waitForRemoteTranscriptPage(t, fixture.serverA.SessionViewClient(), fixture.planA.SessionID, func(page clientui.TranscriptPage) bool {
		return transcriptPageContainsAssistantText(page, "reply after gap recovery")
	})
	pageB2 := waitForRemoteTranscriptPage(t, fixture.serverB.SessionViewClient(), fixture.planA.SessionID, func(page clientui.TranscriptPage) bool {
		return transcriptPageContainsAssistantText(page, "reply after gap recovery")
	})
	if pageA2.Revision != pageB2.Revision || pageA2.TotalEntries != pageB2.TotalEntries {
		t.Fatalf("expected both clients to converge after gap recovery, a=%+v b=%+v", pageA2, pageB2)
	}
}

type remoteMultiClientRuntimeFixture struct {
	daemon       *serve.Server
	workspaceA   string
	workspaceB   string
	serverA      embeddedServer
	serverB      embeddedServer
	planA        sessionLaunchPlan
	planB        sessionLaunchPlan
	runtimePlanA *runtimeLaunchPlan
	runtimePlanB *runtimeLaunchPlan
}

type promptAnswerResult struct {
	client string
	err    error
}

func startRemoteMultiClientRuntimeFixture(t *testing.T, openAIBaseURL string) *remoteMultiClientRuntimeFixture {
	t.Helper()

	fixture := &remoteMultiClientRuntimeFixture{
		workspaceA: t.TempDir(),
		workspaceB: t.TempDir(),
	}
	t.Setenv("HOME", t.TempDir())
	registerAppWorkspace(t, fixture.workspaceA)
	registerAppWorkspace(t, fixture.workspaceB)

	req := serverstartup.Request{
		WorkspaceRoot:         fixture.workspaceA,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
	}
	if strings.TrimSpace(openAIBaseURL) != "" {
		req.OpenAIBaseURL = openAIBaseURL
		req.OpenAIBaseURLExplicit = true
	}

	srv, err := serve.Start(context.Background(), req, memoryAuthHandler{state: auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "test-key"},
		},
		UpdatedAt: time.Now().UTC(),
	}}, autoOnboarding{})
	if err != nil {
		t.Fatalf("serve.Start: %v", err)
	}
	fixture.daemon = srv

	serveCtx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(serveCtx)
	}()
	waitForConfiguredRemoteIdentity(t, fixture.workspaceA)

	t.Cleanup(func() {
		if fixture.runtimePlanB != nil {
			fixture.runtimePlanB.Close()
		}
		if fixture.runtimePlanA != nil {
			fixture.runtimePlanA.Close()
		}
		if fixture.serverB != nil {
			_ = fixture.serverB.Close()
		}
		if fixture.serverA != nil {
			_ = fixture.serverA.Close()
		}
		cancel()
		if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) && serveErr != nil {
			t.Errorf("Serve error = %v, want context canceled", serveErr)
		}
		_ = srv.Close()
	})

	serverA, err := startSessionServer(context.Background(), Options{WorkspaceRoot: fixture.workspaceA, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer workspace A: %v", err)
	}
	fixture.serverA = serverA
	if _, ok := fixture.serverA.(*remoteAppServer); !ok {
		t.Fatalf("expected remote app server for workspace A, got %T", fixture.serverA)
	}

	cfgB, err := loadSessionServerConfig(Options{WorkspaceRoot: fixture.workspaceB, WorkspaceRootExplicit: true})
	if err != nil {
		t.Fatalf("loadSessionServerConfig workspace B: %v", err)
	}
	remoteB, err := client.DialRemoteURLForProject(context.Background(), config.ServerRPCURL(cfgB), fixture.serverA.ProjectID())
	if err != nil {
		t.Fatalf("DialRemote workspace B: %v", err)
	}
	fixture.serverB = newRemoteAppServer(remoteB, cfgB)

	if got, want := fixture.serverA.ProjectID(), fixture.serverB.ProjectID(); got != want {
		t.Fatalf("project id mismatch across clients: a=%q b=%q", got, want)
	}
	if fixture.serverA.Config().WorkspaceRoot == fixture.serverB.Config().WorkspaceRoot {
		t.Fatalf("expected distinct workspace roots across clients, both=%q", fixture.serverA.Config().WorkspaceRoot)
	}

	plannerA := newSessionLaunchPlanner(fixture.serverA)
	fixture.planA, err = plannerA.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession A: %v", err)
	}
	fixture.runtimePlanA, err = plannerA.PrepareRuntime(context.Background(), fixture.planA, io.Discard, "test remote multi-client runtime A")
	if err != nil {
		t.Fatalf("PrepareRuntime A: %v", err)
	}

	plannerB := newSessionLaunchPlanner(fixture.serverB)
	fixture.planB, err = plannerB.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, SelectedSessionID: fixture.planA.SessionID})
	if err != nil {
		t.Fatalf("PlanSession B: %v", err)
	}
	if fixture.planB.SessionID != fixture.planA.SessionID {
		t.Fatalf("expected second client to attach same session, a=%q b=%q", fixture.planA.SessionID, fixture.planB.SessionID)
	}

	return fixture
}

func waitForPromptAnswerResult(t *testing.T, results <-chan promptAnswerResult) promptAnswerResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for prompt answer result")
		return promptAnswerResult{}
	}
}

func requireExactlyOnePromptWinner(t *testing.T, first promptAnswerResult, second promptAnswerResult) (promptAnswerResult, promptAnswerResult) {
	t.Helper()
	if first.err == nil && second.err != nil {
		return first, second
	}
	if second.err == nil && first.err != nil {
		return second, first
	}
	t.Fatalf("expected exactly one prompt answer winner, got first=%+v second=%+v", first, second)
	return promptAnswerResult{}, promptAnswerResult{}
}

func isTerminalPromptAnswerError(err error) bool {
	return errors.Is(err, serverapi.ErrPromptNotFound) || errors.Is(err, serverapi.ErrPromptAlreadyResolved)
}

func TestShouldBypassRemoteStartupForInteractiveOnboardingOnFirstRun(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	bypass, err := shouldBypassRemoteStartupForInteractiveOnboarding(Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, &stubAuthInteractor{})
	if err != nil {
		t.Fatalf("shouldBypassRemoteStartupForInteractiveOnboarding: %v", err)
	}
	if !bypass {
		t.Fatal("expected first-run interactive startup to bypass remote onboarding paths")
	}
}

func TestShouldBypassRemoteStartupForInteractiveOnboardingSkipsWhenConfigExists(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	if _, _, err := config.WriteDefaultSettingsFile(); err != nil {
		t.Fatalf("WriteDefaultSettingsFile: %v", err)
	}

	bypass, err := shouldBypassRemoteStartupForInteractiveOnboarding(Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, &stubAuthInteractor{})
	if err != nil {
		t.Fatalf("shouldBypassRemoteStartupForInteractiveOnboarding: %v", err)
	}
	if bypass {
		t.Fatal("expected configured interactive startup to keep remote onboarding paths enabled")
	}
}

func TestStartSessionServerBypassesRemoteAndDaemonOnFirstInteractiveRun(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	originalDial := dialInteractiveRemoteSessionServer
	originalLaunch := launchSessionServerDaemon
	originalEmbedded := startInteractiveEmbeddedSessionServer
	defer func() {
		dialInteractiveRemoteSessionServer = originalDial
		launchSessionServerDaemon = originalLaunch
		startInteractiveEmbeddedSessionServer = originalEmbedded
	}()

	remoteCalled := false
	daemonCalled := false
	embeddedCalled := false
	startInteractiveEmbeddedSessionServer = func(_ context.Context, _ Options, _ authInteractor) (*embeddedAppServer, error) {
		embeddedCalled = true
		return &embeddedAppServer{}, nil
	}
	dialInteractiveRemoteSessionServer = func(context.Context, Options, authInteractor) (*remoteAppServer, bool, error) {
		remoteCalled = true
		return nil, false, nil
	}
	launchSessionServerDaemon = func(context.Context, Options) (*client.Remote, func() error, bool, error) {
		daemonCalled = true
		return nil, nil, false, nil
	}

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, &stubAuthInteractor{})
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()
	if !embeddedCalled {
		t.Fatal("expected embedded startup path to be used")
	}
	if remoteCalled {
		t.Fatal("expected remote startup path to be skipped on first interactive run")
	}
	if daemonCalled {
		t.Fatal("expected daemon launch path to be skipped on first interactive run")
	}
	if _, ok := server.(*embeddedAppServer); !ok {
		t.Fatalf("expected embedded app server, got %T", server)
	}
}

func TestStartSessionServerUnregisteredWorkspaceStartsRegistrationCapableServer(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configureAppTestServerPort(t)

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractorWithEnvKey("test-key"))
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()

	if got := server.ProjectID(); got != "" {
		t.Fatalf("project id = %q, want empty for unregistered workspace", got)
	}
	resolved, err := server.ProjectViewClient().ResolveProjectPath(context.Background(), serverapi.ProjectResolvePathRequest{Path: workspace})
	if err != nil {
		t.Fatalf("ResolveProjectPath: %v", err)
	}
	if resolved.Binding != nil {
		t.Fatalf("expected unknown workspace resolution, got %+v", resolved.Binding)
	}
}

func TestStartEmbeddedServerUnknownWorkspaceCreateProjectFlowCanPlanSession(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store := auth.NewFileStore(config.GlobalAuthConfigPath(cfg))
	if err := store.Save(context.Background(), auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "test-key"},
		},
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save auth state: %v", err)
	}

	originalPicker := runProjectBindingPickerFlow
	originalPrompt := runProjectNamePromptFlow
	t.Cleanup(func() {
		runProjectBindingPickerFlow = originalPicker
		runProjectNamePromptFlow = originalPrompt
	})
	runProjectBindingPickerFlow = func(projects []clientui.ProjectSummary, theme string, policy config.TUIAlternateScreenPolicy) (projectBindingPickerResult, error) {
		if len(projects) != 0 {
			t.Fatalf("expected no existing projects, got %+v", projects)
		}
		return projectBindingPickerResult{CreateNew: true}, nil
	}
	runProjectNamePromptFlow = func(defaultName string, theme string, policy config.TUIAlternateScreenPolicy) (string, error) {
		if want := filepath.Base(workspace); defaultName != want {
			t.Fatalf("default project name = %q, want %q", defaultName, want)
		}
		return "Created From Startup", nil
	}

	t.Log("starting embedded server")
	server, err := startEmbeddedServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startEmbeddedServer: %v", err)
	}
	defer func() { _ = server.Close() }()

	t.Log("binding unknown workspace")
	bound, err := ensureInteractiveProjectBinding(context.Background(), server)
	if err != nil {
		t.Fatalf("ensureInteractiveProjectBinding: %v", err)
	}
	if got := bound.ProjectID(); got == "" {
		t.Fatal("expected bound project id after create-project flow")
	}

	t.Log("planning interactive session")
	planner := newSessionLaunchPlanner(bound)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("EvalSymlinks workspace: %v", err)
	}
	if plan.WorkspaceRoot != canonicalWorkspace {
		t.Fatalf("plan workspace root = %q, want %q", plan.WorkspaceRoot, canonicalWorkspace)
	}
	resolved, err := bound.ProjectViewClient().ResolveProjectPath(context.Background(), serverapi.ProjectResolvePathRequest{Path: workspace})
	if err != nil {
		t.Fatalf("ResolveProjectPath: %v", err)
	}
	t.Log("resolved created binding")
	if resolved.Binding == nil || resolved.Binding.ProjectName != "Created From Startup" {
		t.Fatalf("expected created binding metadata, got %+v", resolved.Binding)
	}
}

func TestStartSessionServerRejectsIncompatibleDiscoveredDaemonAndFallsBack(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	fakeResponses, hits := newFakeResponsesServer(t, []string{"embedded fallback reply"})
	defer fakeResponses.Close()

	cleanup := publishConfiguredRemoteForWorkspace(t, workspace, protocol.CapabilityFlags{
		JSONRPCWebSocket: true,
		ProjectAttach:    true,
		SessionAttach:    true,
		RunPrompt:        true,
		SessionActivity:  true,
		ProcessOutput:    true,
	})
	defer cleanup()

	server, err := startSessionServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         fakeResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, newHeadlessAuthInteractorWithEnvKey("test-key"))
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()
	if _, ok := server.(*remoteAppServer); ok {
		t.Fatal("expected incompatible configured daemon to be rejected")
	}

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "test embedded fallback runtime")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	message, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "hello through embedded fallback")
	if err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}
	if message != "embedded fallback reply" {
		t.Fatalf("assistant message = %q, want %q", message, "embedded fallback reply")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected embedded fallback llm call once, got %d", hits.Load())
	}
}

func TestStartSessionServerRejectsDiscoveredDaemonWithoutProcessOutputCapability(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	fakeResponses, hits := newFakeResponsesServer(t, []string{"embedded fallback reply"})
	defer fakeResponses.Close()

	cleanup := publishConfiguredRemoteForWorkspace(t, workspace, protocol.CapabilityFlags{
		JSONRPCWebSocket: true,
		ProjectAttach:    true,
		SessionAttach:    true,
		SessionPlan:      true,
		SessionLifecycle: true,
		SessionRuntime:   true,
		RuntimeControl:   true,
		PromptControl:    true,
		PromptActivity:   true,
		SessionActivity:  true,
	})
	defer cleanup()

	server, err := startSessionServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         fakeResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, newHeadlessAuthInteractorWithEnvKey("test-key"))
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()
	if _, ok := server.(*remoteAppServer); ok {
		t.Fatal("expected configured daemon without process capability to be rejected")
	}

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "test embedded fallback runtime")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	message, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "hello after capability fallback")
	if err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}
	if message != "embedded fallback reply" {
		t.Fatalf("assistant message = %q, want %q", message, "embedded fallback reply")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected embedded fallback llm call once, got %d", hits.Load())
	}
}

func TestStartSessionServerRejectsDiscoveredDaemonWithoutTranscriptPagingCapability(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	fakeResponses, hits := newFakeResponsesServer(t, []string{"embedded fallback reply"})
	defer fakeResponses.Close()

	cleanup := publishConfiguredRemoteForWorkspace(t, workspace, protocol.CapabilityFlags{
		JSONRPCWebSocket: true,
		ProjectAttach:    true,
		SessionAttach:    true,
		SessionPlan:      true,
		SessionLifecycle: true,
		SessionRuntime:   true,
		RuntimeControl:   true,
		PromptControl:    true,
		PromptActivity:   true,
		SessionActivity:  true,
		ProcessOutput:    true,
	})
	defer cleanup()

	server, err := startSessionServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         fakeResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, newHeadlessAuthInteractorWithEnvKey("test-key"))
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()
	if _, ok := server.(*remoteAppServer); ok {
		t.Fatal("expected configured daemon without transcript paging capability to be rejected")
	}

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "test embedded fallback runtime")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	message, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "hello after transcript paging fallback")
	if err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}
	if message != "embedded fallback reply" {
		t.Fatalf("assistant message = %q, want %q", message, "embedded fallback reply")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected embedded fallback llm call once, got %d", hits.Load())
	}
}

func TestRemoteSessionStatusUsesLocalOAuthAuthState(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
	}, memoryAuthHandler{state: auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "test-key"},
		},
		UpdatedAt: time.Now().UTC(),
	}}, autoOnboarding{})
	if err != nil {
		t.Fatalf("serve.Start: %v", err)
	}
	defer func() { _ = srv.Close() }()

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(serveCtx)
	}()
	defer func() {
		cancel()
		if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
			t.Fatalf("Serve error = %v, want context canceled", serveErr)
		}
	}()
	waitForConfiguredRemoteIdentity(t, workspace)

	loadCfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store := auth.NewFileStore(config.GlobalAuthConfigPath(loadCfg))
	if err := store.Save(context.Background(), auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type: auth.MethodOAuth,
			OAuth: &auth.OAuthMethod{
				AccessToken: "access-token",
				AccountID:   "acct-123",
				Email:       "user@example.com",
			},
		},
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save auth state: %v", err)
	}

	originalFetcher := statusUsagePayloadFetcher
	defer func() { statusUsagePayloadFetcher = originalFetcher }()
	statusUsagePayloadFetcher = func(_ context.Context, baseURL string, state auth.State) (statusUsagePayload, error) {
		if baseURL != statusUsageBaseURL {
			t.Fatalf("base URL = %q", baseURL)
		}
		if state.Method.OAuth == nil || state.Method.OAuth.Email != "user@example.com" || state.Method.OAuth.AccountID != "acct-123" {
			t.Fatalf("unexpected auth state: %+v", state.Method.OAuth)
		}
		return statusUsagePayload{PlanType: "pro"}, nil
	}

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()
	if _, ok := server.(*remoteAppServer); !ok {
		t.Fatalf("expected remote app server, got %T", server)
	}

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	if plan.StatusConfig.AuthManager == nil {
		t.Fatal("expected status auth manager for remote session")
	}

	collector := defaultUIStatusCollector{}
	snapshot, err := collector.Collect(context.Background(), uiStatusRequest{
		WorkspaceRoot:   plan.StatusConfig.WorkspaceRoot,
		PersistenceRoot: plan.StatusConfig.PersistenceRoot,
		Settings:        plan.StatusConfig.Settings,
		Source:          plan.StatusConfig.Source,
		AuthManager:     plan.StatusConfig.AuthManager,
		AuthStatePath:   plan.StatusConfig.AuthStatePath,
		OwnsServer:      plan.StatusConfig.OwnsServer,
	})
	if err != nil {
		t.Fatalf("collect status: %v", err)
	}
	if got := snapshot.Auth.Summary; got != "user@example.com" {
		t.Fatalf("auth summary = %q", got)
	}
	if got := snapshot.Subscription.Summary; got != "Pro subscription" {
		t.Fatalf("subscription summary = %q", got)
	}
}

func TestStartSessionServerOwnsLaunchedDaemonCloser(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	called := false
	originalLaunch := launchSessionServerDaemon
	t.Cleanup(func() { launchSessionServerDaemon = originalLaunch })
	launchSessionServerDaemon = func(context.Context, Options) (*client.Remote, func() error, bool, error) {
		return &client.Remote{}, func() error {
			called = true
			return nil
		}, true, nil
	}

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	if _, ok := server.(*remoteAppServer); !ok {
		t.Fatalf("expected remote app server, got %T", server)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !called {
		t.Fatal("expected launched daemon closer to be invoked")
	}
}

func TestStartSessionServerLaunchedDaemonCloseStopsProcess(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("helper daemon process signal probe is unix-only")
	}
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	t.Setenv("GO_WANT_HELPER_DAEMON", "1")
	t.Setenv("GO_HELPER_WORKSPACE_ROOT", workspace)

	originalExecPath := resolveDaemonExecutablePath
	originalServeArgs := buildServeArgsFunc
	t.Cleanup(func() {
		resolveDaemonExecutablePath = originalExecPath
		buildServeArgsFunc = originalServeArgs
	})
	resolveDaemonExecutablePath = func() (string, bool) {
		path, err := os.Executable()
		if err != nil {
			t.Fatalf("os.Executable: %v", err)
		}
		return path, true
	}
	buildServeArgsFunc = func(string, Options) []string {
		return []string{"-test.run=^TestStartSessionServerHelperDaemonProcess$"}
	}

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	remote, ok := server.(*remoteAppServer)
	if !ok {
		t.Fatalf("expected remote app server, got %T", server)
	}
	if remote.identity.PID == 0 {
		t.Fatal("expected launched daemon pid")
	}
	identity := waitForConfiguredRemoteIdentity(t, workspace)
	if identity.PID != remote.identity.PID {
		t.Fatalf("connected pid = %d, remote pid = %d", identity.PID, remote.identity.PID)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitForPIDExit(t, remote.identity.PID)
}

func TestStartSessionServerUsesInvocationOverridesWhenAttachingToDiscoveredDaemon(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	defaultResponses, defaultHits := newFakeResponsesServer(t, []string{"interactive daemon default"})
	defer defaultResponses.Close()
	overrideResponses, overrideHits := newFakeResponsesServer(t, []string{"interactive daemon override"})
	defer overrideResponses.Close()

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         defaultResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, memoryAuthHandler{state: auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "test-key"},
		},
		UpdatedAt: time.Now().UTC(),
	}}, autoOnboarding{})
	if err != nil {
		t.Fatalf("serve.Start: %v", err)
	}
	defer func() { _ = srv.Close() }()

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(serveCtx)
	}()
	waitForConfiguredRemoteIdentity(t, workspace)

	server, err := startSessionServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         overrideResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "test remote interactive runtime override")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	message, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "hello through interactive override")
	if err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}
	if message != "interactive daemon override" {
		t.Fatalf("assistant message = %q, want %q", message, "interactive daemon override")
	}
	if overrideHits.Load() != 1 {
		t.Fatalf("expected override llm call once, got %d", overrideHits.Load())
	}
	if defaultHits.Load() != 0 {
		t.Fatalf("expected daemon default llm endpoint unused, got %d", defaultHits.Load())
	}

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestStartSessionServerPreservesExplicitCLIToolsWithCLIModelOverride(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5.4",
	}, memoryAuthHandler{state: auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "test-key"},
		},
		UpdatedAt: time.Now().UTC(),
	}}, autoOnboarding{})
	if err != nil {
		t.Fatalf("serve.Start: %v", err)
	}
	defer func() { _ = srv.Close() }()

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(serveCtx)
	}()
	waitForConfiguredRemoteIdentity(t, workspace)

	server, err := startSessionServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5.3-codex",
		Tools:                 "shell",
	}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	if plan.ActiveSettings.Model != "gpt-5.3-codex" {
		t.Fatalf("model = %q, want gpt-5.3-codex", plan.ActiveSettings.Model)
	}
	if len(plan.EnabledTools) != 1 || plan.EnabledTools[0] != toolspec.ToolShell {
		t.Fatalf("enabled tools = %+v, want only shell", plan.EnabledTools)
	}

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestStartSessionServerUsesConfiguredDaemonForPromptRoundTrip(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
	}, memoryAuthHandler{state: auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "test-key"},
		},
		UpdatedAt: time.Now().UTC(),
	}}, autoOnboarding{})
	if err != nil {
		t.Fatalf("serve.Start: %v", err)
	}
	defer func() { _ = srv.Close() }()

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(serveCtx)
	}()
	waitForConfiguredRemoteIdentity(t, workspace)

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "test remote prompt round trip")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	askDone := make(chan struct {
		resp askquestion.Response
		err  error
	}, 1)
	go func() {
		resp, err := srv.AwaitPromptResponse(context.Background(), plan.SessionID, askquestion.Request{
			ID:                     "ask-1",
			Question:               "Pick one",
			Suggestions:            []string{"one", "two"},
			RecommendedOptionIndex: 2,
		})
		askDone <- struct {
			resp askquestion.Response
			err  error
		}{resp: resp, err: err}
	}()
	waitForPendingAskResources(t, server.AskViewClient(), plan.SessionID, 1)
	askEvt := waitForRemoteAskEvent(t, runtimePlan.Wiring.askEvents)
	if askEvt.req.ID != "ask-1" || askEvt.req.Question != "Pick one" {
		t.Fatalf("unexpected ask event: %+v", askEvt.req)
	}
	askEvt.reply <- askReply{response: askquestion.Response{RequestID: askEvt.req.ID, SelectedOptionNumber: 2}}
	select {
	case result := <-askDone:
		if result.err != nil {
			t.Fatalf("AwaitPromptResponse ask: %v", result.err)
		}
		if result.resp.RequestID != "ask-1" || result.resp.SelectedOptionNumber != 2 {
			t.Fatalf("unexpected ask response: %+v", result.resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ask response")
	}
	waitForPendingAskResources(t, server.AskViewClient(), plan.SessionID, 0)

	approvalDone := make(chan struct {
		resp askquestion.Response
		err  error
	}, 1)
	go func() {
		resp, err := srv.AwaitPromptResponse(context.Background(), plan.SessionID, askquestion.Request{
			ID:              "approval-1",
			Question:        "Approve it?",
			Approval:        true,
			ApprovalOptions: []askquestion.ApprovalOption{{Decision: askquestion.ApprovalDecisionAllowOnce, Label: "Allow once"}, {Decision: askquestion.ApprovalDecisionDeny, Label: "Deny"}},
		})
		approvalDone <- struct {
			resp askquestion.Response
			err  error
		}{resp: resp, err: err}
	}()
	waitForPendingApprovalResources(t, server.ApprovalViewClient(), plan.SessionID, 1)
	approvalEvt := waitForRemoteAskEvent(t, runtimePlan.Wiring.askEvents)
	if !approvalEvt.req.Approval || approvalEvt.req.ID != "approval-1" {
		t.Fatalf("unexpected approval event: %+v", approvalEvt.req)
	}
	approvalEvt.reply <- askReply{response: askquestion.Response{RequestID: approvalEvt.req.ID, Approval: &askquestion.ApprovalPayload{Decision: askquestion.ApprovalDecisionAllowOnce, Commentary: "trusted"}}}
	select {
	case result := <-approvalDone:
		if result.err != nil {
			t.Fatalf("AwaitPromptResponse approval: %v", result.err)
		}
		if result.resp.RequestID != "approval-1" || result.resp.Approval == nil || result.resp.Approval.Decision != askquestion.ApprovalDecisionAllowOnce || result.resp.Approval.Commentary != "trusted" {
			t.Fatalf("unexpected approval response: %+v", result.resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for approval response")
	}
	waitForPendingApprovalResources(t, server.ApprovalViewClient(), plan.SessionID, 0)

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestStartSessionServerUsesConfiguredDaemonForSessionLifecycleDraftPersistence(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
	}, memoryAuthHandler{state: auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "test-key"},
		},
		UpdatedAt: time.Now().UTC(),
	}}, autoOnboarding{})
	if err != nil {
		t.Fatalf("serve.Start: %v", err)
	}
	defer func() { _ = srv.Close() }()

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(serveCtx)
	}()
	waitForConfiguredRemoteIdentity(t, workspace)

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "session lifecycle draft persistence")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()
	if _, err := server.SessionLifecycleClient().PersistInputDraft(context.Background(), serverapi.SessionPersistInputDraftRequest{ClientRequestID: uuid.NewString(), SessionID: plan.SessionID, ControllerLeaseID: runtimePlan.ControllerLeaseID, Input: "saved draft"}); err != nil {
		t.Fatalf("PersistInputDraft: %v", err)
	}
	if got := sessionLaunchInitialInputFromServer(context.Background(), server, plan.SessionID, "transition draft"); got != "saved draft" {
		t.Fatalf("sessionLaunchInitialInputFromServer = %q, want saved draft", got)
	}
	resolved, err := server.SessionLifecycleClient().ResolveTransition(context.Background(), serverapi.SessionResolveTransitionRequest{
		ClientRequestID:   uuid.NewString(),
		SessionID:         plan.SessionID,
		ControllerLeaseID: runtimePlan.ControllerLeaseID,
		Transition: serverapi.SessionTransition{
			Action:          "open_session",
			TargetSessionID: plan.SessionID,
			InitialInput:    "transition draft",
		},
	})
	if err != nil {
		t.Fatalf("ResolveTransition: %v", err)
	}
	if !resolved.ShouldContinue || resolved.NextSessionID != plan.SessionID || resolved.InitialInput != "transition draft" {
		t.Fatalf("unexpected resolved transition: %+v", resolved)
	}

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestStartSessionServerListsPendingPromptSnapshotOverRemoteReads(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
	}, memoryAuthHandler{state: auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "test-key"},
		},
		UpdatedAt: time.Now().UTC(),
	}}, autoOnboarding{})
	if err != nil {
		t.Fatalf("serve.Start: %v", err)
	}
	defer func() { _ = srv.Close() }()

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(serveCtx)
	}()
	waitForConfiguredRemoteIdentity(t, workspace)

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()
	if _, ok := server.(*remoteAppServer); !ok {
		t.Fatalf("expected remote app server, got %T", server)
	}

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "test remote prompt snapshot reads")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	askDone := make(chan error, 1)
	go func() {
		_, err := srv.AwaitPromptResponse(context.Background(), plan.SessionID, askquestion.Request{ID: "ask-remote-1", Question: "Ask?"})
		askDone <- err
	}()
	approvalDone := make(chan error, 1)
	go func() {
		_, err := srv.AwaitPromptResponse(context.Background(), plan.SessionID, askquestion.Request{
			ID:              "approval-remote-1",
			Question:        "Approve?",
			Approval:        true,
			ApprovalOptions: []askquestion.ApprovalOption{{Decision: askquestion.ApprovalDecisionAllowOnce, Label: "Allow once"}},
		})
		approvalDone <- err
	}()

	waitForPendingAskResources(t, server.AskViewClient(), plan.SessionID, 1)
	waitForPendingApprovalResources(t, server.ApprovalViewClient(), plan.SessionID, 1)
	ids, err := listPendingPromptIDs(context.Background(), plan.SessionID, server.AskViewClient(), server.ApprovalViewClient())
	if err != nil {
		t.Fatalf("listPendingPromptIDs: %v", err)
	}
	if _, ok := ids["ask-remote-1"]; !ok {
		t.Fatalf("pending prompt snapshot missing ask id: %+v", ids)
	}
	if _, ok := ids["approval-remote-1"]; !ok {
		t.Fatalf("pending prompt snapshot missing approval id: %+v", ids)
	}

	first := waitForRemoteAskEvent(t, runtimePlan.Wiring.askEvents)
	second := waitForRemoteAskEvent(t, runtimePlan.Wiring.askEvents)
	for _, evt := range []askEvent{first, second} {
		switch evt.req.ID {
		case "ask-remote-1":
			evt.reply <- askReply{response: askquestion.Response{RequestID: evt.req.ID, Answer: "done"}}
		case "approval-remote-1":
			evt.reply <- askReply{response: askquestion.Response{RequestID: evt.req.ID, Approval: &askquestion.ApprovalPayload{Decision: askquestion.ApprovalDecisionAllowOnce}}}
		default:
			t.Fatalf("unexpected prompt event id %q", evt.req.ID)
		}
	}

	select {
	case err := <-askDone:
		if err != nil {
			t.Fatalf("AwaitPromptResponse ask: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for remote ask response")
	}
	select {
	case err := <-approvalDone:
		if err != nil {
			t.Fatalf("AwaitPromptResponse approval: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for remote approval response")
	}

	ids, err = listPendingPromptIDs(context.Background(), plan.SessionID, server.AskViewClient(), server.ApprovalViewClient())
	if err != nil {
		t.Fatalf("listPendingPromptIDs after resolution: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected no pending prompt ids after resolution, got %+v", ids)
	}

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestStartSessionServerUsesConfiguredDaemonForProcessFlows(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
	}, memoryAuthHandler{state: auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "test-key"},
		},
		UpdatedAt: time.Now().UTC(),
	}}, autoOnboarding{})
	if err != nil {
		t.Fatalf("serve.Start: %v", err)
	}
	defer func() { _ = srv.Close() }()
	srv.Background().SetMinimumExecToBgTime(time.Millisecond)

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(serveCtx)
	}()
	waitForConfiguredRemoteIdentity(t, workspace)

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}

	result, err := srv.Background().Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"/bin/sh", "-lc", "printf 'daemon process output\n'; sleep 5"},
		DisplayCommand: "printf 'daemon process output'; sleep 5",
		Workdir:        workspace,
		YieldTime:      time.Millisecond,
		OwnerSessionID: plan.SessionID,
	})
	if err != nil {
		t.Fatalf("Background().Start: %v", err)
	}
	if !result.Backgrounded {
		t.Fatal("expected backgrounded process")
	}

	proc := waitForRemoteProcess(t, server.ProcessViewClient(), plan.SessionID, result.SessionID)
	if proc.OwnerSessionID != plan.SessionID {
		t.Fatalf("unexpected process owner: %+v", proc)
	}

	getResp, err := server.ProcessViewClient().GetProcess(context.Background(), serverapi.ProcessGetRequest{ProcessID: result.SessionID})
	if err != nil {
		t.Fatalf("GetProcess: %v", err)
	}
	if getResp.Process == nil || getResp.Process.ID != result.SessionID {
		t.Fatalf("unexpected get process response: %+v", getResp.Process)
	}

	outputSub, err := server.ProcessOutputClient().SubscribeProcessOutput(context.Background(), serverapi.ProcessOutputSubscribeRequest{ProcessID: result.SessionID, OffsetBytes: 0})
	if err != nil {
		t.Fatalf("SubscribeProcessOutput: %v", err)
	}
	defer func() { _ = outputSub.Close() }()
	chunk, err := outputSub.Next(context.Background())
	if err != nil {
		t.Fatalf("ProcessOutput Next: %v", err)
	}
	if !strings.Contains(chunk.Text, "daemon process output") {
		t.Fatalf("unexpected process output chunk: %+v", chunk)
	}
	inlineResp := waitForRemoteInlineOutput(t, server.ProcessControlClient(), result.SessionID)
	if !strings.Contains(inlineResp.Output, "daemon process output") {
		t.Fatalf("unexpected inline output: %q", inlineResp.Output)
	}

	if _, err := server.ProcessControlClient().KillProcess(context.Background(), serverapi.ProcessKillRequest{ClientRequestID: uuid.NewString(), ProcessID: result.SessionID}); err != nil {
		t.Fatalf("KillProcess: %v", err)
	}
	waitForRemoteProcessExit(t, server.ProcessViewClient(), result.SessionID)

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestInteractiveSessionServerWorkflowParity(t *testing.T) {
	t.Run("embedded", func(t *testing.T) {
		home := t.TempDir()
		workspace := t.TempDir()
		t.Setenv("HOME", home)
		registerAppWorkspace(t, workspace)
		fakeResponses, _ := newFakeResponsesServer(t, []string{"parity reply"})
		defer fakeResponses.Close()
		server, err := startEmbeddedServer(context.Background(), Options{
			WorkspaceRoot:         workspace,
			WorkspaceRootExplicit: true,
			Model:                 "gpt-5",
			OpenAIBaseURL:         fakeResponses.URL,
			OpenAIBaseURLExplicit: true,
		}, newHeadlessAuthInteractorWithEnvKey("test-key"))
		if err != nil {
			t.Fatalf("startEmbeddedServer: %v", err)
		}
		defer func() { _ = server.Close() }()
		runInteractiveWorkflowScenario(t, server, "parity reply")
	})

	t.Run("daemon", func(t *testing.T) {
		home := t.TempDir()
		workspace := t.TempDir()
		t.Setenv("HOME", home)
		registerAppWorkspace(t, workspace)
		fakeResponses, _ := newFakeResponsesServer(t, []string{"parity reply"})
		defer fakeResponses.Close()

		srv, err := serve.Start(context.Background(), serverstartup.Request{
			WorkspaceRoot:         workspace,
			WorkspaceRootExplicit: true,
			Model:                 "gpt-5",
			OpenAIBaseURL:         fakeResponses.URL,
			OpenAIBaseURLExplicit: true,
		}, memoryAuthHandler{state: auth.State{
			Scope:     auth.ScopeGlobal,
			Method:    auth.Method{Type: auth.MethodAPIKey, APIKey: &auth.APIKeyMethod{Key: "test-key"}},
			UpdatedAt: time.Now().UTC(),
		}}, autoOnboarding{})
		if err != nil {
			t.Fatalf("serve.Start: %v", err)
		}
		defer func() { _ = srv.Close() }()

		serveCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		errCh := make(chan error, 1)
		go func() {
			errCh <- srv.Serve(serveCtx)
		}()
		waitForConfiguredRemoteIdentity(t, workspace)

		server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
		if err != nil {
			t.Fatalf("startSessionServer: %v", err)
		}
		defer func() { _ = server.Close() }()
		runInteractiveWorkflowScenario(t, server, "parity reply")

		cancel()
		if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
			t.Fatalf("Serve error = %v, want context canceled", serveErr)
		}
	})
}

func waitForConfiguredRemoteIdentity(t *testing.T, workspace string) protocol.ServerIdentity {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	opts := Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}
	for time.Now().Before(deadline) {
		remote, ok := tryDialConfiguredRemote(context.Background(), opts, nil)
		if ok {
			identity := remote.Identity()
			_ = remote.Close()
			return identity
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("configured daemon did not become reachable for workspace %s", workspace)
	return protocol.ServerIdentity{}
}

func waitForPIDExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still running", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForRemoteAskEvent(t *testing.T, events <-chan askEvent) askEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				t.Fatal("ask event channel closed")
			}
			if evt.isResolution() {
				continue
			}
			return evt
		case <-deadline:
			t.Fatal("timed out waiting for ask event")
			return askEvent{}
		}
	}
}

func waitForRemoteRuntimeEvent(t *testing.T, events <-chan clientui.Event, description string, predicate func(clientui.Event) bool) clientui.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				t.Fatalf("runtime event channel closed while waiting for %s", description)
			}
			if predicate == nil || predicate(evt) {
				return evt
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", description)
			return clientui.Event{}
		}
	}
}

func waitForSessionActivitySubscriptionEvent(t *testing.T, sub serverapi.SessionActivitySubscription, description string, predicate func(clientui.Event) bool) clientui.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		evt, err := sub.Next(ctx)
		if err != nil {
			t.Fatalf("session activity subscription failed while waiting for %s: %v", description, err)
		}
		if predicate == nil || predicate(evt) {
			return evt
		}
	}
}

func waitForSessionActivityGap(t *testing.T, sub serverapi.SessionActivitySubscription) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, err := sub.Next(ctx)
		if err != nil {
			return err
		}
	}
}

func waitForRemoteTranscriptPage(t *testing.T, views client.SessionViewClient, sessionID string, predicate func(clientui.TranscriptPage) bool) clientui.TranscriptPage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := views.GetSessionTranscriptPage(context.Background(), serverapi.SessionTranscriptPageRequest{SessionID: sessionID})
		if err != nil {
			t.Fatalf("GetSessionTranscriptPage: %v", err)
		}
		if predicate == nil || predicate(resp.Transcript) {
			return resp.Transcript
		}
		time.Sleep(10 * time.Millisecond)
	}
	resp, err := views.GetSessionTranscriptPage(context.Background(), serverapi.SessionTranscriptPageRequest{SessionID: sessionID})
	if err != nil {
		t.Fatalf("GetSessionTranscriptPage final: %v", err)
	}
	t.Fatalf("timed out waiting for transcript page match for session %s: %+v", sessionID, resp.Transcript)
	return clientui.TranscriptPage{}
}

func waitForRemoteProjectSessions(t *testing.T, views client.ProjectViewClient, projectID string, predicate func([]clientui.SessionSummary) bool) []clientui.SessionSummary {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := views.ListSessionsByProject(context.Background(), serverapi.SessionListByProjectRequest{ProjectID: projectID})
		if err != nil {
			t.Fatalf("ListSessionsByProject: %v", err)
		}
		if predicate == nil || predicate(resp.Sessions) {
			return resp.Sessions
		}
		time.Sleep(10 * time.Millisecond)
	}
	resp, err := views.ListSessionsByProject(context.Background(), serverapi.SessionListByProjectRequest{ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListSessionsByProject final: %v", err)
	}
	t.Fatalf("timed out waiting for project session list match for project %s: %+v", projectID, resp.Sessions)
	return nil
}

func transcriptPageContainsAssistantText(page clientui.TranscriptPage, want string) bool {
	for _, entry := range page.Entries {
		if entry.Role == "assistant" && entry.Text == want {
			return true
		}
	}
	return false
}

func sessionSummariesContainID(summaries []clientui.SessionSummary, sessionID string) bool {
	for _, summary := range summaries {
		if summary.SessionID == sessionID {
			return true
		}
	}
	return false
}

func waitForRemoteProcess(t *testing.T, views client.ProcessViewClient, sessionID string, processID string) clientui.BackgroundProcess {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := views.ListProcesses(context.Background(), serverapi.ProcessListRequest{OwnerSessionID: sessionID})
		if err != nil {
			t.Fatalf("ListProcesses: %v", err)
		}
		for _, proc := range resp.Processes {
			if proc.ID == processID {
				return proc
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for process %s in session %s", processID, sessionID)
	return clientui.BackgroundProcess{}
}

func waitForRemoteProcessExit(t *testing.T, views client.ProcessViewClient, processID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := views.GetProcess(context.Background(), serverapi.ProcessGetRequest{ProcessID: processID})
		if err != nil {
			t.Fatalf("GetProcess: %v", err)
		}
		if resp.Process != nil && !resp.Process.Running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for process %s to exit", processID)
}

func waitForRemoteInlineOutput(t *testing.T, controls client.ProcessControlClient, processID string) serverapi.ProcessInlineOutputResponse {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := controls.GetInlineOutput(context.Background(), serverapi.ProcessInlineOutputRequest{ProcessID: processID, MaxChars: 1024})
		if err != nil {
			t.Fatalf("GetInlineOutput: %v", err)
		}
		if strings.TrimSpace(resp.Output) != "" {
			return resp
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for inline output from %s", processID)
	return serverapi.ProcessInlineOutputResponse{}
}

func runInteractiveWorkflowScenario(t *testing.T, server embeddedServer, wantReply string) {
	t.Helper()
	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "workflow parity")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	message, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "hello parity")
	if err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}
	if message != wantReply {
		t.Fatalf("assistant message = %q, want %q", message, wantReply)
	}
	if _, err := server.SessionLifecycleClient().PersistInputDraft(context.Background(), serverapi.SessionPersistInputDraftRequest{ClientRequestID: uuid.NewString(), SessionID: plan.SessionID, ControllerLeaseID: runtimePlan.ControllerLeaseID, Input: "workflow draft"}); err != nil {
		t.Fatalf("PersistInputDraft: %v", err)
	}
	if got := sessionLaunchInitialInputFromServer(context.Background(), server, plan.SessionID, "transition draft"); got != "workflow draft" {
		t.Fatalf("sessionLaunchInitialInputFromServer = %q, want workflow draft", got)
	}
	refreshed, err := server.SessionViewClient().GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: plan.SessionID})
	if err != nil {
		t.Fatalf("GetSessionMainView: %v", err)
	}
	if refreshed.MainView.Session.Transcript.CommittedEntryCount == 0 {
		t.Fatalf("expected transcript metadata, got %+v", refreshed.MainView.Session.Transcript)
	}
}

func newHeadlessAuthInteractorWithEnvKey(key string) authInteractor {
	return &headlessAuthInteractor{lookupEnv: func(env string) string {
		if env == "OPENAI_API_KEY" {
			return key
		}
		return ""
	}}
}

func publishConfiguredRemoteForWorkspace(t *testing.T, workspace string, caps protocol.CapabilityFlags) func() {
	t.Helper()
	identity := protocol.ServerIdentity{
		ProtocolVersion: protocol.Version,
		ServerID:        "stale-daemon",
		PID:             222,
		Capabilities:    caps,
	}
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		var req protocol.Request
		if err := websocket.JSON.Receive(ws, &req); err != nil {
			return
		}
		if req.Method != protocol.MethodHandshake {
			_ = websocket.JSON.Send(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidRequest, "handshake required"))
			return
		}
		if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, protocol.HandshakeResponse{Identity: identity})); err != nil {
			return
		}
		for {
			if err := websocket.JSON.Receive(ws, &req); err != nil {
				return
			}
			_ = websocket.JSON.Send(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeMethodNotFound, "method not found"))
		}
	}))
	host, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		server.Close()
		t.Fatalf("SplitHostPort: %v", err)
	}
	t.Setenv("BUILDER_SERVER_HOST", host)
	t.Setenv("BUILDER_SERVER_PORT", port)
	return func() {
		server.Close()
	}
}
