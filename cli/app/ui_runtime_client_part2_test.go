package app

import (
	"builder/server/llm"
	"builder/server/registry"
	"builder/server/runtime"
	"builder/server/runtimecontrol"
	"builder/server/runtimeview"
	"builder/server/session"
	"builder/server/tools"
	sharedclient "builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/serverapi"
	"context"
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRuntimeClientMainViewDoesNotRefreshCachedSnapshotBehindUIBack(t *testing.T) {
	reads := &countingSessionViewClient{view: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1"}}}
	controls := sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(registry.NewRuntimeRegistry(), nil))
	runtimeClient := newUIRuntimeClientWithReads("session-1", reads, controls).(*sessionRuntimeClient)
	runtimeClient.storeMainView(clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1"}})
	notified := make(chan error, 1)
	runtimeClient.SetConnectionStateObserver(func(err error) {
		notified <- err
	})

	_ = runtimeClient.MainView()

	if got := reads.count.Load(); got != 0 {
		t.Fatalf("main view read count = %d, want 0", got)
	}
	select {
	case err := <-notified:
		t.Fatalf("did not expect synchronous main-view refresh notification, got %v", err)
	default:
	}
}

type leaseRetryRuntimeControlClient struct {
	mu             sync.Mutex
	firstSubmitErr error
	appendErr      error
	submitLeaseID  []string
	goalLeaseID    []string
	localEntries   []serverapi.RuntimeAppendLocalEntryRequest
	showGoalResp   serverapi.RuntimeGoalShowResponse
	setGoalResp    serverapi.RuntimeGoalShowResponse
	pauseGoalResp  serverapi.RuntimeGoalShowResponse
	resumeGoalResp serverapi.RuntimeGoalShowResponse
	clearGoalResp  serverapi.RuntimeGoalShowResponse
}

func (c *leaseRetryRuntimeControlClient) submitLeaseIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.submitLeaseID...)
}

func (c *leaseRetryRuntimeControlClient) goalLeaseIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.goalLeaseID...)
}

func (c *leaseRetryRuntimeControlClient) resetGoalLeaseIDs() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.goalLeaseID = nil
}

func (c *leaseRetryRuntimeControlClient) appendedLocalEntries() []serverapi.RuntimeAppendLocalEntryRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]serverapi.RuntimeAppendLocalEntryRequest(nil), c.localEntries...)
}

func (c *leaseRetryRuntimeControlClient) SetSessionName(context.Context, serverapi.RuntimeSetSessionNameRequest) error {
	return nil
}

func (c *leaseRetryRuntimeControlClient) SetThinkingLevel(context.Context, serverapi.RuntimeSetThinkingLevelRequest) error {
	return nil
}

func (c *leaseRetryRuntimeControlClient) SetFastModeEnabled(context.Context, serverapi.RuntimeSetFastModeEnabledRequest) (serverapi.RuntimeSetFastModeEnabledResponse, error) {
	return serverapi.RuntimeSetFastModeEnabledResponse{}, nil
}

func (c *leaseRetryRuntimeControlClient) SetReviewerEnabled(context.Context, serverapi.RuntimeSetReviewerEnabledRequest) (serverapi.RuntimeSetReviewerEnabledResponse, error) {
	return serverapi.RuntimeSetReviewerEnabledResponse{}, nil
}

func (c *leaseRetryRuntimeControlClient) SetAutoCompactionEnabled(context.Context, serverapi.RuntimeSetAutoCompactionEnabledRequest) (serverapi.RuntimeSetAutoCompactionEnabledResponse, error) {
	return serverapi.RuntimeSetAutoCompactionEnabledResponse{}, nil
}

func (c *leaseRetryRuntimeControlClient) AppendLocalEntry(_ context.Context, req serverapi.RuntimeAppendLocalEntryRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.localEntries = append(c.localEntries, req)
	return c.appendErr
}

func (c *leaseRetryRuntimeControlClient) ShouldCompactBeforeUserMessage(context.Context, serverapi.RuntimeShouldCompactBeforeUserMessageRequest) (serverapi.RuntimeShouldCompactBeforeUserMessageResponse, error) {
	return serverapi.RuntimeShouldCompactBeforeUserMessageResponse{}, nil
}

func (c *leaseRetryRuntimeControlClient) SubmitUserMessage(_ context.Context, req serverapi.RuntimeSubmitUserMessageRequest) (serverapi.RuntimeSubmitUserMessageResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.submitLeaseID = append(c.submitLeaseID, req.ControllerLeaseID)
	switch req.ControllerLeaseID {
	case "lease-old":
		if c.firstSubmitErr != nil {
			return serverapi.RuntimeSubmitUserMessageResponse{}, c.firstSubmitErr
		}
		return serverapi.RuntimeSubmitUserMessageResponse{}, serverapi.ErrInvalidControllerLease
	case "lease-new":
		return serverapi.RuntimeSubmitUserMessageResponse{Message: "recovered"}, nil
	default:
		return serverapi.RuntimeSubmitUserMessageResponse{}, errors.New("unexpected controller lease")
	}
}

func (c *leaseRetryRuntimeControlClient) SubmitUserShellCommand(context.Context, serverapi.RuntimeSubmitUserShellCommandRequest) error {
	return nil
}

func (c *leaseRetryRuntimeControlClient) CompactContext(context.Context, serverapi.RuntimeCompactContextRequest) error {
	return nil
}

func (c *leaseRetryRuntimeControlClient) CompactContextForPreSubmit(context.Context, serverapi.RuntimeCompactContextForPreSubmitRequest) error {
	return nil
}

func (c *leaseRetryRuntimeControlClient) HasQueuedUserWork(context.Context, serverapi.RuntimeHasQueuedUserWorkRequest) (serverapi.RuntimeHasQueuedUserWorkResponse, error) {
	return serverapi.RuntimeHasQueuedUserWorkResponse{}, nil
}

func (c *leaseRetryRuntimeControlClient) SubmitQueuedUserMessages(context.Context, serverapi.RuntimeSubmitQueuedUserMessagesRequest) (serverapi.RuntimeSubmitQueuedUserMessagesResponse, error) {
	return serverapi.RuntimeSubmitQueuedUserMessagesResponse{}, nil
}

func (c *leaseRetryRuntimeControlClient) Interrupt(context.Context, serverapi.RuntimeInterruptRequest) error {
	return nil
}

func (c *leaseRetryRuntimeControlClient) QueueUserMessage(context.Context, serverapi.RuntimeQueueUserMessageRequest) error {
	return nil
}

func (c *leaseRetryRuntimeControlClient) DiscardQueuedUserMessagesMatching(context.Context, serverapi.RuntimeDiscardQueuedUserMessagesMatchingRequest) (serverapi.RuntimeDiscardQueuedUserMessagesMatchingResponse, error) {
	return serverapi.RuntimeDiscardQueuedUserMessagesMatchingResponse{}, nil
}

func (c *leaseRetryRuntimeControlClient) RecordPromptHistory(context.Context, serverapi.RuntimeRecordPromptHistoryRequest) error {
	return nil
}

func (c *leaseRetryRuntimeControlClient) ShowGoal(context.Context, serverapi.RuntimeGoalShowRequest) (serverapi.RuntimeGoalShowResponse, error) {
	return c.showGoalResp, nil
}

func (c *leaseRetryRuntimeControlClient) SetGoal(_ context.Context, req serverapi.RuntimeGoalSetRequest) (serverapi.RuntimeGoalShowResponse, error) {
	return c.goalWriteResponse(req.ControllerLeaseID, c.setGoalResp)
}

func (c *leaseRetryRuntimeControlClient) PauseGoal(_ context.Context, req serverapi.RuntimeGoalStatusRequest) (serverapi.RuntimeGoalShowResponse, error) {
	return c.goalWriteResponse(req.ControllerLeaseID, c.pauseGoalResp)
}

func (c *leaseRetryRuntimeControlClient) ResumeGoal(_ context.Context, req serverapi.RuntimeGoalStatusRequest) (serverapi.RuntimeGoalShowResponse, error) {
	return c.goalWriteResponse(req.ControllerLeaseID, c.resumeGoalResp)
}

func (c *leaseRetryRuntimeControlClient) CompleteGoal(context.Context, serverapi.RuntimeGoalStatusRequest) (serverapi.RuntimeGoalShowResponse, error) {
	return serverapi.RuntimeGoalShowResponse{}, nil
}

func (c *leaseRetryRuntimeControlClient) ClearGoal(_ context.Context, req serverapi.RuntimeGoalClearRequest) (serverapi.RuntimeGoalShowResponse, error) {
	return c.goalWriteResponse(req.ControllerLeaseID, c.clearGoalResp)
}

func (c *leaseRetryRuntimeControlClient) goalWriteResponse(leaseID string, resp serverapi.RuntimeGoalShowResponse) (serverapi.RuntimeGoalShowResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.goalLeaseID = append(c.goalLeaseID, leaseID)
	if leaseID == "lease-old" {
		return serverapi.RuntimeGoalShowResponse{}, serverapi.ErrInvalidControllerLease
	}
	return resp, nil
}

func TestRuntimeClientGoalMethodsPatchCachedMainView(t *testing.T) {
	showGoal := &serverapi.RuntimeGoal{ID: "goal-show", Objective: "show goal", Status: "paused", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	setGoal := &serverapi.RuntimeGoal{ID: "goal-set", Objective: "set goal", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	pauseGoal := &serverapi.RuntimeGoal{ID: "goal-pause", Objective: "pause goal", Status: "paused", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	resumeGoal := &serverapi.RuntimeGoal{ID: "goal-resume", Objective: "resume goal", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	controls := &leaseRetryRuntimeControlClient{
		showGoalResp:   serverapi.RuntimeGoalShowResponse{Goal: showGoal},
		setGoalResp:    serverapi.RuntimeGoalShowResponse{Goal: setGoal},
		pauseGoalResp:  serverapi.RuntimeGoalShowResponse{Goal: pauseGoal},
		resumeGoalResp: serverapi.RuntimeGoalShowResponse{Goal: resumeGoal},
		clearGoalResp:  serverapi.RuntimeGoalShowResponse{},
	}
	runtimeClient := newUIRuntimeClientWithReads("session-1", &countingSessionViewClient{}, controls).(*sessionRuntimeClient)
	leaseManager := newControllerLeaseManager("lease-old")
	leaseManager.SetRecoverFunc(func(context.Context) (string, error) { return "lease-new", nil })
	runtimeClient.SetControllerLeaseManager(leaseManager)

	goal, err := runtimeClient.ShowGoal()
	if err != nil {
		t.Fatalf("ShowGoal: %v", err)
	}
	assertRuntimeClientGoalCached(t, runtimeClient, goal, runtimeGoalFromAPI(showGoal))
	assertRuntimeGoalConversionDropsAPITimestamps(t, goal, showGoal)

	for _, tt := range []struct {
		name string
		call func() (*clientui.RuntimeGoal, error)
		want *serverapi.RuntimeGoal
	}{
		{name: "set", call: func() (*clientui.RuntimeGoal, error) { return runtimeClient.SetGoal("set goal") }, want: setGoal},
		{name: "pause", call: runtimeClient.PauseGoal, want: pauseGoal},
		{name: "resume", call: runtimeClient.ResumeGoal, want: resumeGoal},
		{name: "clear", call: runtimeClient.ClearGoal, want: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			leaseManager.Set("lease-old")
			controls.resetGoalLeaseIDs()
			goal, err := tt.call()
			if err != nil {
				t.Fatalf("%s goal: %v", tt.name, err)
			}
			if got := controls.goalLeaseIDs(); !reflect.DeepEqual(got, []string{"lease-old", "lease-new"}) {
				t.Fatalf("%s goal lease ids = %+v, want [lease-old lease-new]", tt.name, got)
			}
			assertRuntimeClientGoalCached(t, runtimeClient, goal, runtimeGoalFromAPI(tt.want))
		})
	}
}

func TestCloneRuntimeGoalReturnsIndependentCopy(t *testing.T) {
	original := &clientui.RuntimeGoal{ID: "goal-1", Objective: "ship", Status: clientui.RuntimeGoalStatusActive, Suspended: true}
	cloned := cloneRuntimeGoal(original)
	original.ID = "goal-2"
	original.Objective = "mutated"
	original.Status = clientui.RuntimeGoalStatusPaused
	original.Suspended = false

	want := &clientui.RuntimeGoal{ID: "goal-1", Objective: "ship", Status: clientui.RuntimeGoalStatusActive, Suspended: true}
	if !reflect.DeepEqual(cloned, want) {
		t.Fatalf("clone = %+v, want %+v", cloned, want)
	}
}

func assertRuntimeClientGoalCached(t *testing.T, runtimeClient *sessionRuntimeClient, got *clientui.RuntimeGoal, want *clientui.RuntimeGoal) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("goal = %+v, want %+v", got, want)
	}
	view, ok := runtimeClient.CachedMainView()
	if !ok {
		t.Fatal("expected cached main view")
	}
	if !reflect.DeepEqual(view.Status.Goal, want) {
		t.Fatalf("cached goal = %+v, want %+v", view.Status.Goal, want)
	}
}

func assertRuntimeGoalConversionDropsAPITimestamps(t *testing.T, got *clientui.RuntimeGoal, source *serverapi.RuntimeGoal) {
	t.Helper()
	if source == nil || source.CreatedAt.IsZero() || source.UpdatedAt.IsZero() {
		t.Fatal("test source goal must include timestamps")
	}
	if got == nil || got.ID != source.ID || got.Objective != source.Objective || string(got.Status) != source.Status || got.Suspended != source.Suspended {
		t.Fatalf("converted goal = %+v, source = %+v", got, source)
	}
}

func TestRuntimeClientSubmitUserMessageRecoversInvalidControllerLease(t *testing.T) {
	controls := &leaseRetryRuntimeControlClient{}
	runtimeClient := newUIRuntimeClientWithReads("session-1", &countingSessionViewClient{}, controls).(*sessionRuntimeClient)
	leaseManager := newControllerLeaseManager("lease-old")
	recoveryCalls := 0
	leaseManager.SetRecoverFunc(func(context.Context) (string, error) {
		recoveryCalls++
		return "lease-new", nil
	})
	runtimeClient.SetControllerLeaseManager(leaseManager)

	message, err := runtimeClient.SubmitUserMessage(context.Background(), "hello")
	if err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}
	if message != "recovered" {
		t.Fatalf("SubmitUserMessage message = %q, want recovered", message)
	}
	if recoveryCalls != 1 {
		t.Fatalf("recovery call count = %d, want 1", recoveryCalls)
	}
	if got := runtimeClient.controllerLeaseIDValue(); got != "lease-new" {
		t.Fatalf("controller lease id = %q, want lease-new", got)
	}
	if got := controls.submitLeaseIDs(); !reflect.DeepEqual(got, []string{"lease-old", "lease-new"}) {
		t.Fatalf("submit lease ids = %+v, want [lease-old lease-new]", got)
	}
}

func TestRuntimeClientSubmitUserMessageRecoversRuntimeUnavailable(t *testing.T) {
	controls := &leaseRetryRuntimeControlClient{firstSubmitErr: serverapi.ErrRuntimeUnavailable}
	runtimeClient := newUIRuntimeClientWithReads("session-1", &countingSessionViewClient{}, controls).(*sessionRuntimeClient)
	leaseManager := newControllerLeaseManager("lease-old")
	recoveryCalls := 0
	leaseManager.SetRecoverFunc(func(context.Context) (string, error) {
		recoveryCalls++
		return "lease-new", nil
	})
	runtimeClient.SetControllerLeaseManager(leaseManager)

	message, err := runtimeClient.SubmitUserMessage(context.Background(), "hello")
	if err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}
	if message != "recovered" {
		t.Fatalf("SubmitUserMessage message = %q, want recovered", message)
	}
	if recoveryCalls != 1 {
		t.Fatalf("recovery call count = %d, want 1", recoveryCalls)
	}
	if got := runtimeClient.controllerLeaseIDValue(); got != "lease-new" {
		t.Fatalf("controller lease id = %q, want lease-new", got)
	}
	if got := controls.submitLeaseIDs(); !reflect.DeepEqual(got, []string{"lease-old", "lease-new"}) {
		t.Fatalf("submit lease ids = %+v, want [lease-old lease-new]", got)
	}
	entries := controls.appendedLocalEntries()
	if len(entries) != 1 {
		t.Fatalf("warning entry count = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.ControllerLeaseID != "lease-new" || entry.Role != "warning" || entry.Text != runtimeLeaseRecoveryWarningText || entry.Visibility != string(clientui.EntryVisibilityAll) {
		t.Fatalf("warning entry = %+v, want new lease warning", entry)
	}
}

func TestRuntimeClientLeaseRecoveryWarningFailureDoesNotBlockSubmit(t *testing.T) {
	controls := &leaseRetryRuntimeControlClient{firstSubmitErr: serverapi.ErrRuntimeUnavailable, appendErr: serverapi.ErrRuntimeUnavailable}
	runtimeClient := newUIRuntimeClientWithReads("session-1", &countingSessionViewClient{}, controls).(*sessionRuntimeClient)
	warnings := make(chan runtimeLeaseRecoveryWarningMsg, 1)
	runtimeClient.SetLeaseRecoveryWarningObserver(func(text string, visibility clientui.EntryVisibility) {
		warnings <- runtimeLeaseRecoveryWarningMsg{text: text, visibility: visibility}
	})
	leaseManager := newControllerLeaseManager("lease-old")
	leaseManager.SetRecoverFunc(func(context.Context) (string, error) { return "lease-new", nil })
	runtimeClient.SetControllerLeaseManager(leaseManager)

	message, err := runtimeClient.SubmitUserMessage(context.Background(), "hello")
	if err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}
	if message != "recovered" {
		t.Fatalf("SubmitUserMessage message = %q, want recovered", message)
	}
	if got := controls.submitLeaseIDs(); !reflect.DeepEqual(got, []string{"lease-old", "lease-new"}) {
		t.Fatalf("submit lease ids = %+v, want [lease-old lease-new]", got)
	}
	if entries := controls.appendedLocalEntries(); len(entries) != 1 {
		t.Fatalf("warning append attempts = %d, want 1", len(entries))
	}
	select {
	case warning := <-warnings:
		if warning.text != runtimeLeaseRecoveryWarningText || warning.visibility != clientui.EntryVisibilityAll {
			t.Fatalf("warning = %+v, want lease recovery warning", warning)
		}
	default:
		t.Fatal("expected warning fallback notification")
	}
}

func TestRuntimeClientServerRestartFirstPromptRecoversAndWarnsOngoing(t *testing.T) {
	runtimeEvents := make(chan clientui.Event, 128)
	store, err := session.Create(t.TempDir(), "workspace-x", t.TempDir())
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	client := &runtimeClientFakeLLM{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	engine, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{
		Model: "gpt-5",
		OnEvent: func(evt runtime.Event) {
			runtimeEvents <- runtimeview.EventFromRuntime(evt)
		},
	})
	if err != nil {
		t.Fatalf("create runtime engine: %v", err)
	}
	resolver := &mutableRuntimeResolver{}
	controls := sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(resolver, nil))
	runtimeClient := newUIRuntimeClientWithReads(store.Meta().SessionID, &countingSessionViewClient{}, controls).(*sessionRuntimeClient)
	leaseManager := newControllerLeaseManager("lease-old")
	leaseManager.SetRecoverFunc(func(context.Context) (string, error) {
		resolver.Set(engine)
		return "lease-new", nil
	})
	runtimeClient.SetControllerLeaseManager(leaseManager)
	model := newProjectedTestUIModel(nil, closedProjectedRuntimeEvents(), closedAskEvents())
	sized, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model = sized.(*uiModel)

	message, err := runtimeClient.SubmitUserMessage(context.Background(), "hello after restart")
	if err != nil {
		t.Fatalf("submitRuntimeUserMessage: %v", err)
	}
	if message != "done" {
		t.Fatalf("submitRuntimeUserMessage message = %q, want done", message)
	}

	updated := model
	eventCount := 0
	flushText := ""
	for len(runtimeEvents) > 0 {
		msg := <-runtimeEvents
		eventCount++
		next, cmd := updated.Update(runtimeEventMsg{event: msg})
		updated = next.(*uiModel)
		flushText += collectNativeHistoryFlushText(collectCmdMessages(t, cmd))
	}
	if !strings.Contains(flushText, runtimeLeaseRecoveryWarningText) {
		t.Fatalf("expected ongoing warning flush, events=%d entries=%+v flush=%q", eventCount, updated.transcriptEntries, flushText)
	}
	if strings.Contains(flushText, "runtime for session") {
		t.Fatalf("did not expect runtime unavailable error in ongoing flush, got %q", flushText)
	}
}
