package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"builder/prompts"
	"builder/server/metadata"
	"builder/server/primaryrun"
	"builder/server/session"
	"builder/shared/client"
	"builder/shared/config"
	"builder/shared/serverapi"
	"builder/shared/toolspec"
)

type recordingGoalRemote struct {
	showReq     []serverapi.RuntimeGoalShowRequest
	setReq      []serverapi.RuntimeGoalSetRequest
	completeReq []serverapi.RuntimeGoalStatusRequest
	goal        *serverapi.RuntimeGoal
}

func (r *recordingGoalRemote) Close() error { return nil }

func (r *recordingGoalRemote) ShowGoal(_ context.Context, req serverapi.RuntimeGoalShowRequest) (serverapi.RuntimeGoalShowResponse, error) {
	r.showReq = append(r.showReq, req)
	return serverapi.RuntimeGoalShowResponse{Goal: r.goal}, nil
}

func (r *recordingGoalRemote) SetGoal(_ context.Context, req serverapi.RuntimeGoalSetRequest) (serverapi.RuntimeGoalShowResponse, error) {
	r.setReq = append(r.setReq, req)
	return serverapi.RuntimeGoalShowResponse{Goal: r.goal}, nil
}

func (r *recordingGoalRemote) PauseGoal(context.Context, serverapi.RuntimeGoalStatusRequest) (serverapi.RuntimeGoalShowResponse, error) {
	return serverapi.RuntimeGoalShowResponse{}, nil
}

func (r *recordingGoalRemote) ResumeGoal(context.Context, serverapi.RuntimeGoalStatusRequest) (serverapi.RuntimeGoalShowResponse, error) {
	return serverapi.RuntimeGoalShowResponse{}, nil
}

func (r *recordingGoalRemote) CompleteGoal(_ context.Context, req serverapi.RuntimeGoalStatusRequest) (serverapi.RuntimeGoalShowResponse, error) {
	r.completeReq = append(r.completeReq, req)
	return serverapi.RuntimeGoalShowResponse{Goal: r.goal}, nil
}

func (r *recordingGoalRemote) ClearGoal(context.Context, serverapi.RuntimeGoalClearRequest) (serverapi.RuntimeGoalShowResponse, error) {
	return serverapi.RuntimeGoalShowResponse{}, nil
}

func TestGoalShowUsesBuilderSessionID(t *testing.T) {
	t.Setenv("BUILDER_SESSION_ID", "session-1")
	remote := &recordingGoalRemote{goal: &serverapi.RuntimeGoal{ID: "goal-1", Objective: "ship goal mode", Status: "active"}}
	restore := replaceGoalCommandRemoteOpener(t, remote)
	defer restore()

	stdout := new(strings.Builder)
	stderr := new(strings.Builder)
	if code := goalSubcommand([]string{"show"}, stdout, stderr); code != 0 {
		t.Fatalf("goal show exit = %d stderr=%q", code, stderr.String())
	}
	if len(remote.showReq) != 1 || remote.showReq[0].SessionID != "session-1" {
		t.Fatalf("show requests = %+v", remote.showReq)
	}
	if !strings.Contains(stdout.String(), "ship goal mode") || !strings.Contains(stdout.String(), "active") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "goal-1") || strings.Contains(stdout.String(), "ID:") {
		t.Fatalf("plain goal show leaked goal id: %q", stdout.String())
	}
}

func TestGoalAgentEnvDeniesMutationWithoutDialing(t *testing.T) {
	t.Setenv("BUILDER_SESSION_ID", "session-1")
	remote := &recordingGoalRemote{}
	restore := replaceGoalCommandRemoteOpener(t, remote)
	defer restore()

	stderr := new(strings.Builder)
	if code := goalSubcommand([]string{"set", "new goal"}, new(strings.Builder), stderr); code == 0 {
		t.Fatalf("goal set exit = 0")
	}
	if !strings.Contains(stderr.String(), prompts.GoalAgentCommandDeniedPrompt) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if len(remote.showReq) != 0 || len(remote.completeReq) != 0 {
		t.Fatalf("remote was called: %+v", remote)
	}
}

func TestGoalSetRejectsEmptyObjectiveBeforeDialing(t *testing.T) {
	remote := &recordingGoalRemote{}
	restore := replaceGoalCommandRemoteOpener(t, remote)
	defer restore()

	stderr := new(strings.Builder)
	if code := goalSubcommand([]string{"set", "--session", "session-1", "   "}, new(strings.Builder), stderr); code != 2 {
		t.Fatalf("goal set empty exit = %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "goal set requires an objective") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if len(remote.setReq) != 0 {
		t.Fatalf("set called for empty objective: %+v", remote.setReq)
	}
}

func TestGoalAgentCompleteRequiresConfirmTripwire(t *testing.T) {
	t.Setenv("BUILDER_SESSION_ID", "session-1")
	remote := &recordingGoalRemote{goal: &serverapi.RuntimeGoal{ID: "goal-1", Objective: "ship goal mode", Status: "complete"}}
	restore := replaceGoalCommandRemoteOpener(t, remote)
	defer restore()

	stderr := new(strings.Builder)
	if code := goalSubcommand([]string{"complete"}, new(strings.Builder), stderr); code == 0 {
		t.Fatalf("goal complete without confirm exit = 0")
	}
	if !strings.Contains(stderr.String(), prompts.GoalCompleteConfirmRequiredPrompt) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if len(remote.completeReq) != 0 {
		t.Fatalf("complete called before confirm: %+v", remote.completeReq)
	}

	stdout := new(strings.Builder)
	stderr.Reset()
	if code := goalSubcommand([]string{"complete", "--confirm"}, stdout, stderr); code != 0 {
		t.Fatalf("goal complete --confirm exit = %d stderr=%q", code, stderr.String())
	}
	if len(remote.completeReq) != 1 {
		t.Fatalf("complete requests = %+v", remote.completeReq)
	}
	if remote.completeReq[0].SessionID != "session-1" || remote.completeReq[0].Actor != "agent" {
		t.Fatalf("complete req = %+v", remote.completeReq[0])
	}
}

func TestGoalCompleteHelpDoesNotExposeConfirmTripwire(t *testing.T) {
	stderr := new(strings.Builder)
	if code := goalSubcommand([]string{"complete", "--help"}, new(strings.Builder), stderr); code != 0 {
		t.Fatalf("goal complete --help exit = %d", code)
	}
	if strings.Contains(stderr.String(), "--confirm") {
		t.Fatalf("goal complete help leaked hidden confirm flag: %q", stderr.String())
	}
}

func TestGoalCommandSubprocessTargetsLiveSessionFromUnboundWorktree(t *testing.T) {
	builderPath := filepath.Join(t.TempDir(), "builder")
	buildCmd := exec.Command("go", "build", "-o", builderPath, ".")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build subprocess builder: %v\n%s", err, output)
	}

	home := t.TempDir()
	workspace := t.TempDir()
	unboundWorktree := t.TempDir()
	t.Setenv("HOME", home)
	configureBindingCommandTestServerPort(t)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	metadataStore, err := metadata.Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	defer func() { _ = metadataStore.Close() }()
	binding, err := metadataStore.RegisterWorkspaceBinding(context.Background(), cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding: %v", err)
	}
	store, err := session.Create(
		config.ProjectSessionsRoot(cfg, binding.ProjectID),
		filepath.Base(cfg.WorkspaceRoot),
		cfg.WorkspaceRoot,
		metadataStore.AuthoritativeSessionStoreOptions()...,
	)
	if err != nil {
		t.Fatalf("session.Create: %v", err)
	}
	if _, err := store.SetGoal("exercise live goal CLI", session.GoalActorUser); err != nil {
		t.Fatalf("SetGoal: %v", err)
	}
	if err := store.EnsureDurable(); err != nil {
		t.Fatalf("EnsureDurable: %v", err)
	}
	record, err := metadataStore.ResolvePersistedSession(context.Background(), store.Meta().SessionID)
	if err != nil {
		t.Fatalf("ResolvePersistedSession: %v", err)
	}
	if record.Meta == nil || record.Meta.Goal == nil {
		t.Fatalf("persisted goal metadata missing: %+v", record.Meta)
	}

	cleanup := startBindingCommandServer(t, unboundWorktree)
	defer cleanup()
	remote, err := client.DialConfiguredRemoteForProjectWorkspace(context.Background(), cfg, binding.ProjectID, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("DialConfiguredRemoteForProjectWorkspace: %v", err)
	}
	defer func() { _ = remote.Close() }()
	settings := cfg.Settings
	settings.Model = "gpt-5"
	settings.ProviderOverride = "openai"
	activateResp, err := remote.ActivateSessionRuntime(context.Background(), serverapi.SessionRuntimeActivateRequest{
		ClientRequestID: "activate-goal-cli-e2e",
		SessionID:       store.Meta().SessionID,
		ActiveSettings:  settings,
		EnabledToolIDs:  toolIDsAsStrings(config.EnabledToolIDs(settings)),
		Source:          cfg.Source,
	})
	if err != nil {
		t.Fatalf("ActivateSessionRuntime: %v", err)
	}
	defer func() {
		_, _ = remote.ReleaseSessionRuntime(context.Background(), serverapi.SessionRuntimeReleaseRequest{
			ClientRequestID: "release-goal-cli-e2e",
			SessionID:       store.Meta().SessionID,
			LeaseID:         activateResp.LeaseID,
		})
	}()

	t.Setenv("BUILDER_SESSION_ID", store.Meta().SessionID)
	showOutput, showErr := runGoalCommandSubprocess(t, builderPath, unboundWorktree, store.Meta().SessionID, "show", "--json")
	if showErr != "" {
		t.Fatalf("goal show stderr = %q", showErr)
	}
	var show serverapi.RuntimeGoalShowResponse
	if err := json.Unmarshal([]byte(showOutput), &show); err != nil {
		t.Fatalf("decode show json: %v output=%q", err, showOutput)
	}
	if show.Goal == nil || show.Goal.Status != "active" || show.Goal.Objective != "exercise live goal CLI" {
		t.Fatalf("show goal = %+v", show.Goal)
	}

	completeOutput, completeErr := runGoalCommandSubprocess(t, builderPath, unboundWorktree, store.Meta().SessionID, "complete", "--confirm")
	if completeErr != "" {
		t.Fatalf("goal complete stderr = %q", completeErr)
	}
	if !strings.Contains(completeOutput, "Status: complete") {
		t.Fatalf("complete stdout = %q", completeOutput)
	}
}

func TestGoalCommandSubprocessSetRejectsActivePrimaryRunBeforePersisting(t *testing.T) {
	builderPath := filepath.Join(t.TempDir(), "builder")
	buildCmd := exec.Command("go", "build", "-o", builderPath, ".")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build subprocess builder: %v\n%s", err, output)
	}

	home := t.TempDir()
	workspace := t.TempDir()
	unboundWorktree := t.TempDir()
	t.Setenv("HOME", home)
	configureBindingCommandTestServerPort(t)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	metadataStore, err := metadata.Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	defer func() { _ = metadataStore.Close() }()
	binding, err := metadataStore.RegisterWorkspaceBinding(context.Background(), cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding: %v", err)
	}
	store, err := session.Create(
		config.ProjectSessionsRoot(cfg, binding.ProjectID),
		filepath.Base(cfg.WorkspaceRoot),
		cfg.WorkspaceRoot,
		metadataStore.AuthoritativeSessionStoreOptions()...,
	)
	if err != nil {
		t.Fatalf("session.Create: %v", err)
	}
	if err := store.EnsureDurable(); err != nil {
		t.Fatalf("EnsureDurable: %v", err)
	}

	modelRequestStarted := make(chan struct{}, 1)
	releaseModelRequest := make(chan struct{})
	var releaseModelOnce sync.Once
	releaseModel := func() {
		releaseModelOnce.Do(func() {
			close(releaseModelRequest)
		})
	}
	defer releaseModel()
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case modelRequestStarted <- struct{}{}:
		default:
		}
		select {
		case <-releaseModelRequest:
			http.Error(w, "released", http.StatusInternalServerError)
		case <-r.Context().Done():
		}
	}))
	defer modelServer.Close()

	cleanup := startBindingCommandServer(t, unboundWorktree)
	defer cleanup()
	remote, err := client.DialConfiguredRemoteForProjectWorkspace(context.Background(), cfg, binding.ProjectID, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("DialConfiguredRemoteForProjectWorkspace: %v", err)
	}
	defer func() { _ = remote.Close() }()
	settings := cfg.Settings
	settings.Model = "gpt-5"
	settings.ProviderOverride = "openai"
	settings.OpenAIBaseURL = modelServer.URL + "/v1"
	settings.Timeouts.ModelRequestSeconds = 30
	activateResp, err := remote.ActivateSessionRuntime(context.Background(), serverapi.SessionRuntimeActivateRequest{
		ClientRequestID: "activate-goal-cli-busy-e2e",
		SessionID:       store.Meta().SessionID,
		ActiveSettings:  settings,
		EnabledToolIDs:  toolIDsAsStrings(config.EnabledToolIDs(settings)),
		Source:          cfg.Source,
	})
	if err != nil {
		t.Fatalf("ActivateSessionRuntime: %v", err)
	}
	defer func() {
		_, _ = remote.ReleaseSessionRuntime(context.Background(), serverapi.SessionRuntimeReleaseRequest{
			ClientRequestID: "release-goal-cli-busy-e2e",
			SessionID:       store.Meta().SessionID,
			LeaseID:         activateResp.LeaseID,
		})
	}()

	submitCtx, cancelSubmit := context.WithCancel(context.Background())
	defer cancelSubmit()
	submitDone := make(chan error, 1)
	go func() {
		_, err := remote.SubmitUserMessage(submitCtx, serverapi.RuntimeSubmitUserMessageRequest{
			ClientRequestID:   "submit-hanging-run",
			SessionID:         store.Meta().SessionID,
			ControllerLeaseID: activateResp.LeaseID,
			Text:              "hold the primary run",
		})
		submitDone <- err
	}()
	select {
	case <-modelRequestStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for active model request")
	}

	stdout, stderr, err := runGoalCommandSubprocessRaw(t, builderPath, unboundWorktree, "", "set", "--session", store.Meta().SessionID, "new goal while busy")
	if err == nil {
		t.Fatalf("goal set succeeded during active primary run stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stderr, primaryrun.ErrActivePrimaryRun.Error()) {
		t.Fatalf("goal set stderr = %q, want active primary run", stderr)
	}
	record, err := metadataStore.ResolvePersistedSession(context.Background(), store.Meta().SessionID)
	if err != nil {
		t.Fatalf("ResolvePersistedSession: %v", err)
	}
	if record.Meta == nil {
		t.Fatal("persisted session metadata missing")
	}
	if goal := record.Meta.Goal; goal != nil {
		t.Fatalf("goal persisted after busy subprocess set: %+v", goal)
	}
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	for _, event := range events {
		if event.Kind == "goal_set" {
			t.Fatalf("goal_set event persisted after busy subprocess set: %+v", event)
		}
	}

	cancelSubmit()
	releaseModel()
	select {
	case <-submitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for active model request to stop")
	}
}

func runGoalCommandSubprocess(t *testing.T, builderPath string, workdir string, sessionID string, args ...string) (stdout string, stderr string) {
	t.Helper()
	stdout, stderr, err := runGoalCommandSubprocessRaw(t, builderPath, workdir, sessionID, args...)
	if err != nil {
		t.Fatalf("%s goal %s failed: %v stdout=%q stderr=%q", builderPath, strings.Join(args, " "), err, stdout, stderr)
	}
	return stdout, stderr
}

func runGoalCommandSubprocessRaw(t *testing.T, builderPath string, workdir string, sessionID string, args ...string) (stdout string, stderr string, err error) {
	t.Helper()
	cmd := exec.Command(builderPath, append([]string{"goal"}, args...)...)
	cmd.Dir = workdir
	cmd.Env = goalCommandSubprocessEnv(sessionID)
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

func goalCommandSubprocessEnv(sessionID string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "BUILDER_SESSION_ID=") {
			continue
		}
		env = append(env, item)
	}
	if strings.TrimSpace(sessionID) != "" {
		env = append(env, "BUILDER_SESSION_ID="+sessionID)
	}
	return env
}

func toolIDsAsStrings(ids []toolspec.ID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	return out
}

func replaceGoalCommandRemoteOpener(t *testing.T, remote *recordingGoalRemote) func() {
	t.Helper()
	previous := goalCommandRemoteOpener
	goalCommandRemoteOpener = func(context.Context) (goalCommandRemote, error) {
		return remote, nil
	}
	return func() { goalCommandRemoteOpener = previous }
}
