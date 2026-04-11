package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"builder/internal/testopenai"
	"builder/server/auth"
	"builder/server/authflow"
	"builder/server/llm"
	"builder/server/metadata"
	"builder/server/runtime"
	"builder/server/serve"
	"builder/server/session"
	serverstartup "builder/server/startup"
	"builder/server/tools/askquestion"
	"builder/shared/client"
	"builder/shared/config"
	"builder/shared/discovery"
	"builder/shared/protocol"
	"builder/shared/serverapi"
)

type memoryAuthHandler struct {
	state auth.State
}

func (h memoryAuthHandler) WrapStore(auth.Store) auth.Store {
	return auth.NewMemoryStore(h.state)
}

func (memoryAuthHandler) NeedsInteraction(req authflow.InteractionRequest) bool {
	return !req.Gate.Ready
}

func (memoryAuthHandler) Interact(context.Context, authflow.InteractionRequest) (authflow.InteractionOutcome, error) {
	return authflow.InteractionOutcome{}, auth.ErrAuthNotConfigured
}

func (memoryAuthHandler) LookupEnv(string) string {
	return ""
}

type autoOnboarding struct{}

func (autoOnboarding) EnsureOnboardingReady(_ context.Context, req serverstartup.OnboardingRequest) (config.App, error) {
	path, created, err := config.WriteDefaultSettingsFile()
	if err != nil {
		return config.App{}, err
	}
	reloaded, err := req.ReloadConfig()
	if err != nil {
		return config.App{}, err
	}
	reloaded.Source.CreatedDefaultConfig = created
	reloaded.Source.SettingsPath = path
	reloaded.Source.SettingsFileExists = true
	return reloaded, nil
}

func TestEnsureSubagentSessionNameSetsDefault(t *testing.T) {
	containerDir := t.TempDir()
	store, err := session.NewLazy(containerDir, "workspace-x", "/tmp/workspace")
	if err != nil {
		t.Fatalf("new lazy session: %v", err)
	}

	if err := ensureSubagentSessionName(store); err != nil {
		t.Fatalf("ensure subagent session name: %v", err)
	}

	meta := store.Meta()
	want := meta.SessionID + " " + subagentSessionSuffix
	if meta.Name != want {
		t.Fatalf("session name = %q, want %q", meta.Name, want)
	}
}

func TestEnsureSubagentSessionNamePreservesExistingName(t *testing.T) {
	containerDir := t.TempDir()
	store, err := session.NewLazy(containerDir, "workspace-x", "/tmp/workspace")
	if err != nil {
		t.Fatalf("new lazy session: %v", err)
	}
	if err := store.SetName("incident triage"); err != nil {
		t.Fatalf("set name: %v", err)
	}

	if err := ensureSubagentSessionName(store); err != nil {
		t.Fatalf("ensure subagent session name: %v", err)
	}

	if got := store.Meta().Name; got != "incident triage" {
		t.Fatalf("session name = %q, want incident triage", got)
	}
}

func TestWriteRunProgressEventOnlyWritesSelectedKinds(t *testing.T) {
	var out bytes.Buffer

	writeRunProgressEvent(&out, runtime.Event{Kind: runtime.EventAssistantDelta, StepID: "s1", AssistantDelta: "hello"})
	writeRunProgressEvent(&out, runtime.Event{Kind: runtime.EventToolCallStarted, StepID: "s1"})
	writeRunProgressEvent(&out, runtime.Event{Kind: runtime.EventReviewerCompleted, StepID: "s1", Reviewer: &runtime.ReviewerStatus{Outcome: "no_suggestions"}})

	text := out.String()
	if strings.Contains(text, "AssistantDelta") {
		t.Fatalf("unexpected assistant delta in progress output: %q", text)
	}
	if !strings.Contains(text, "Running tool") {
		t.Fatalf("expected tool call started in progress output, got %q", text)
	}
	if !strings.Contains(text, "Review finished") {
		t.Fatalf("expected reviewer completed in progress output, got %q", text)
	}
}

func TestRunPromptAskHandlerReturnsError(t *testing.T) {
	_, err := runPromptAskHandler(askquestion.Request{Question: "Need approval?"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "You can't ask questions") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunPromptWithoutAuthReturnsErrAuthNotConfiguredWithoutReadingStdin(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	t.Setenv("OPENAI_API_KEY", "")

	originalStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdin: %v", err)
	}
	_ = w.Close()
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = originalStdin
		_ = r.Close()
	})

	_, err = RunPrompt(context.Background(), Options{WorkspaceRoot: workspace}, "hello", 0, nil)
	if !errors.Is(err, auth.ErrAuthNotConfigured) {
		t.Fatalf("expected auth not configured without stdin prompt, got %v", err)
	}
}

func TestRunPromptUsesDiscoveredDaemonWithoutLocalAuth(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	fakeResponses, hits := newFakeResponsesServer(t, []string{"daemon reply"})
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

	loadCfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	_, containerDir, err := config.ResolveWorkspaceContainer(loadCfg)
	if err != nil {
		t.Fatalf("ResolveWorkspaceContainer: %v", err)
	}
	discoveryPath, err := discovery.PathForContainer(containerDir)
	if err != nil {
		t.Fatalf("PathForContainer: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := discovery.Read(discoveryPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("discovery record did not appear at %s", discoveryPath)
		}
		time.Sleep(10 * time.Millisecond)
	}

	result, err := RunPrompt(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, "hello through daemon", 0, nil)
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if result.Result != "daemon reply" {
		t.Fatalf("result = %q, want %q", result.Result, "daemon reply")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected daemon-backed llm call once, got %d", hits.Load())
	}

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestRunPromptRejectsIncompatibleDiscoveredDaemonAndFallsBackToEmbedded(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	t.Setenv("OPENAI_API_KEY", "test-key")

	fakeResponses, hits := newFakeResponsesServer(t, []string{"embedded fallback reply"})
	defer fakeResponses.Close()

	cleanup := publishDiscoveredRemoteForWorkspace(t, workspace, protocol.CapabilityFlags{
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

	result, err := RunPrompt(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         fakeResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, "hello through fallback", 0, nil)
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if result.Result != "embedded fallback reply" {
		t.Fatalf("result = %q, want %q", result.Result, "embedded fallback reply")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected embedded fallback llm call once, got %d", hits.Load())
	}
}

func TestStartRunPromptClientFallsBackToEmbeddedWhenDaemonLaunchFails(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	t.Setenv("OPENAI_API_KEY", "test-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if testopenai.HandleInputTokenCount(w, r, 1) {
			return
		}
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		testopenai.WriteCompletedResponseStream(w, "embedded fallback", 1, 1)
	}))
	defer server.Close()

	originalLaunch := launchRunPromptDaemon
	t.Cleanup(func() { launchRunPromptDaemon = originalLaunch })
	launchRunPromptDaemon = func(context.Context, Options) (*client.Remote, func() error, bool, error) {
		return nil, nil, false, errors.New("daemon launch failed")
	}

	runClient, closeFn, err := startRunPromptClient(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	})
	if err != nil {
		t.Fatalf("startRunPromptClient: %v", err)
	}
	defer func() {
		if closeFn != nil {
			_ = closeFn()
		}
	}()

	response, err := runClient.RunPrompt(context.Background(), serverapi.RunPromptRequest{
		ClientRequestID: "req-embedded-fallback",
		Prompt:          "hello",
	}, nil)
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if response.Result != "embedded fallback" {
		t.Fatalf("result = %q, want embedded fallback", response.Result)
	}
}

func TestOwnedDaemonCloseFallsBackToKillWhenInterruptFails(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("sleep helper is unix-only")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.Wait()
	}()
	originalTerminate := terminateOwnedDaemonProcess
	originalKill := forceKillOwnedDaemonProcess
	t.Cleanup(func() {
		terminateOwnedDaemonProcess = originalTerminate
		forceKillOwnedDaemonProcess = originalKill
	})
	killed := false
	terminateOwnedDaemonProcess = func(process *os.Process) error {
		return errors.New("interrupt unsupported")
	}
	forceKillOwnedDaemonProcess = func(process *os.Process) error {
		killed = true
		if process == nil {
			return nil
		}
		return process.Kill()
	}
	closeFn := newOwnedDaemonClose(nil, cmd, errCh)
	if err := closeFn(); err != nil {
		t.Fatalf("closeFn: %v", err)
	}
	if !killed {
		t.Fatal("expected owned daemon close to fall back to kill")
	}
}

func TestRunPromptUsesInvocationOverridesWhenAttachingToDiscoveredDaemon(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	defaultResponses, defaultHits := newFakeResponsesServer(t, []string{"daemon default"})
	defer defaultResponses.Close()
	overrideResponses, overrideHits := newFakeResponsesServer(t, []string{"override reply"})
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

	loadCfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	_, containerDir, err := config.ResolveWorkspaceContainer(loadCfg)
	if err != nil {
		t.Fatalf("ResolveWorkspaceContainer: %v", err)
	}
	discoveryPath, err := discovery.PathForContainer(containerDir)
	if err != nil {
		t.Fatalf("PathForContainer: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := discovery.Read(discoveryPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("discovery record did not appear at %s", discoveryPath)
		}
		time.Sleep(10 * time.Millisecond)
	}

	result, err := RunPrompt(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         overrideResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, "hello through override", 0, nil)
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if result.Result != "override reply" {
		t.Fatalf("result = %q, want %q", result.Result, "override reply")
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

func TestTryDialMatchingDiscoveredRemoteSkipsRecordThatDoesNotMatchSpawnedPID(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	_, containerDir, err := config.ResolveWorkspaceContainer(cfg)
	if err != nil {
		t.Fatalf("ResolveWorkspaceContainer: %v", err)
	}
	discoveryPath, err := discovery.PathForContainer(containerDir)
	if err != nil {
		t.Fatalf("PathForContainer: %v", err)
	}
	binding, err := metadata.ResolveBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("ResolveBinding: %v", err)
	}
	if err := discovery.Write(discoveryPath, protocol.DiscoveryRecord{
		Identity: protocol.ServerIdentity{ProjectID: binding.ProjectID, PID: 111, Capabilities: protocol.CapabilityFlags{RunPrompt: true}},
		RPCURL:   "ws://127.0.0.1:1/rpc",
	}); err != nil {
		t.Fatalf("discovery.Write: %v", err)
	}

	originalDial := dialDiscoveredRemote
	var dialCalls int
	t.Cleanup(func() { dialDiscoveredRemote = originalDial })
	dialDiscoveredRemote = func(context.Context, protocol.DiscoveryRecord) (*client.Remote, error) {
		dialCalls++
		return nil, errors.New("unexpected dial")
	}

	if remote, ok := tryDialMatchingDiscoveredRemote(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, discoveredRemoteSupportsRunPrompt, func(record protocol.DiscoveryRecord) bool {
		return record.Identity.PID == 222
	}); ok || remote != nil {
		t.Fatalf("expected mismatched pid record to be skipped, got remote=%v ok=%t", remote, ok)
	}
	if dialCalls != 0 {
		t.Fatalf("expected mismatched pid record to be rejected before dialing, got %d dial calls", dialCalls)
	}
}

func TestRunPromptCreatesSessionAndPersistsDurableTranscript(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	t.Setenv("OPENAI_API_KEY", "test-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if testopenai.HandleInputTokenCount(w, r, 11) {
			return
		}
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got == "" {
			t.Fatal("expected authorization header")
		}
		testopenai.WriteCompletedResponseStream(w, "hello from fake", 11, 7)
	}))
	defer server.Close()

	result, err := RunPrompt(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	}, "hello from user", 0, nil)
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if result.Result != "hello from fake" {
		t.Fatalf("result = %q, want %q", result.Result, "hello from fake")
	}
	if strings.TrimSpace(result.SessionID) == "" {
		t.Fatal("expected session id")
	}
	if !strings.HasSuffix(result.SessionName, " "+subagentSessionSuffix) {
		t.Fatalf("expected subagent session name, got %q", result.SessionName)
	}

	cfg, err := config.Load(workspace, config.LoadOptions{OpenAIBaseURL: server.URL})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	store, err := session.OpenByID(cfg.PersistenceRoot, result.SessionID)
	if err != nil {
		t.Fatalf("open session by id: %v", err)
	}
	meta := store.Meta()
	if meta.WorkspaceRoot != cfg.WorkspaceRoot {
		t.Fatalf("workspace root = %q, want %q", meta.WorkspaceRoot, cfg.WorkspaceRoot)
	}
	if meta.FirstPromptPreview != "hello from user" {
		t.Fatalf("first prompt preview = %q, want %q", meta.FirstPromptPreview, "hello from user")
	}
	if meta.Continuation == nil || meta.Continuation.OpenAIBaseURL != server.URL {
		t.Fatalf("unexpected continuation context: %+v", meta.Continuation)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var (
		sawUser      bool
		sawAssistant bool
	)
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var msg llm.Message
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			t.Fatalf("unmarshal message payload: %v", err)
		}
		if msg.Role == llm.RoleUser && msg.Content == "hello from user" {
			sawUser = true
		}
		if msg.Role == llm.RoleAssistant && msg.Content == "hello from fake" && msg.Phase == llm.MessagePhaseFinal {
			sawAssistant = true
		}
	}
	if !sawUser {
		t.Fatal("expected persisted user message in event log")
	}
	if !sawAssistant {
		t.Fatal("expected persisted final assistant message in event log")
	}
}

func TestHeadlessRunPromptClientResumesExistingSessionByID(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	t.Setenv("OPENAI_API_KEY", "test-key")

	server, hits := newFakeResponsesServer(t, []string{"first response", "second response"})
	defer server.Close()

	created, err := RunPrompt(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	}, "first prompt", 0, nil)
	if err != nil {
		t.Fatalf("initial RunPrompt: %v", err)
	}

	boot, err := startEmbeddedServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		SessionID:             created.SessionID,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	defer func() { _ = boot.Close() }()

	runClient := newHeadlessRunPromptClient(boot)
	resumed, err := runClient.RunPrompt(context.Background(), serverapi.RunPromptRequest{
		ClientRequestID:   "req-resume-1",
		SelectedSessionID: created.SessionID,
		Prompt:            "second prompt",
	}, nil)
	if err != nil {
		t.Fatalf("resumed client RunPrompt: %v", err)
	}
	if resumed.SessionID != created.SessionID {
		t.Fatalf("resumed session id = %q, want %q", resumed.SessionID, created.SessionID)
	}
	if resumed.Result != "second response" {
		t.Fatalf("resumed result = %q, want %q", resumed.Result, "second response")
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("fake response server hit count = %d, want 2", got)
	}

	store, err := openWorkspaceSessionStore(workspace, server.URL, created.SessionID)
	if err != nil {
		t.Fatalf("open workspace session store: %v", err)
	}
	messages, err := readStoredMessages(store)
	if err != nil {
		t.Fatalf("read stored messages: %v", err)
	}
	assertMessagePresent(t, messages, llm.RoleUser, "first prompt")
	assertMessagePresent(t, messages, llm.RoleAssistant, "first response")
	assertMessagePresent(t, messages, llm.RoleUser, "second prompt")
	assertMessagePresent(t, messages, llm.RoleAssistant, "second response")
	if got := store.Meta().FirstPromptPreview; got != "first prompt" {
		t.Fatalf("first prompt preview = %q, want %q", got, "first prompt")
	}
}

func TestHeadlessRunPromptClientRestoresContinuationContextFromSelectedSession(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	t.Setenv("OPENAI_API_KEY", "test-key")

	server, hits := newFakeResponsesServer(t, []string{"created via explicit base url", "resumed via continuation"})
	defer server.Close()

	created, err := RunPrompt(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	}, "first prompt", 0, nil)
	if err != nil {
		t.Fatalf("initial RunPrompt: %v", err)
	}

	boot, err := startEmbeddedServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		SessionID:             created.SessionID,
		Model:                 "gpt-5",
	}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	defer func() { _ = boot.Close() }()

	runClient := newHeadlessRunPromptClient(boot)
	resumed, err := runClient.RunPrompt(context.Background(), serverapi.RunPromptRequest{
		ClientRequestID:   "req-resume-2",
		SelectedSessionID: created.SessionID,
		Prompt:            "second prompt",
	}, nil)
	if err != nil {
		t.Fatalf("resumed client RunPrompt: %v", err)
	}
	if resumed.Result != "resumed via continuation" {
		t.Fatalf("resumed result = %q, want %q", resumed.Result, "resumed via continuation")
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("fake response server hit count = %d, want 2", got)
	}

	store, err := openWorkspaceSessionStore(workspace, server.URL, created.SessionID)
	if err != nil {
		t.Fatalf("open workspace session store: %v", err)
	}
	if store.Meta().Continuation == nil || store.Meta().Continuation.OpenAIBaseURL != server.URL {
		t.Fatalf("unexpected continuation context: %+v", store.Meta().Continuation)
	}
}

func TestHeadlessRunPromptClientDeduplicatesDuplicateClientRequestID(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	t.Setenv("OPENAI_API_KEY", "test-key")

	secondRelease := make(chan struct{})
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if testopenai.HandleInputTokenCount(w, r, 11) {
			return
		}
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got == "" {
			t.Fatal("expected authorization header")
		}
		index := int(hits.Add(1)) - 1
		switch index {
		case 0:
		case 1:
			<-secondRelease
		default:
			t.Fatalf("unexpected response request index %d", index)
		}
		reply := []string{"created", "deduped"}[index]
		testopenai.WriteCompletedResponseStream(w, reply, 11, 7)
	}))
	defer server.Close()

	created, err := RunPrompt(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	}, "first prompt", 0, nil)
	if err != nil {
		t.Fatalf("initial RunPrompt: %v", err)
	}

	boot, err := startEmbeddedServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		SessionID:             created.SessionID,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	defer func() { _ = boot.Close() }()

	runClient := newHeadlessRunPromptClient(boot)
	req := serverapi.RunPromptRequest{
		ClientRequestID:   "dup-e2e-1",
		SelectedSessionID: created.SessionID,
		Prompt:            "second prompt",
	}

	type runResult struct {
		response serverapi.RunPromptResponse
		err      error
	}
	results := make(chan runResult, 2)
	go func() {
		response, err := runClient.RunPrompt(context.Background(), req, nil)
		results <- runResult{response: response, err: err}
	}()
	go func() {
		response, err := runClient.RunPrompt(context.Background(), req, nil)
		results <- runResult{response: response, err: err}
	}()

	close(secondRelease)
	first := <-results
	second := <-results
	if first.err != nil {
		t.Fatalf("first duplicate run error: %v", first.err)
	}
	if second.err != nil {
		t.Fatalf("second duplicate run error: %v", second.err)
	}
	if first.response != second.response {
		t.Fatalf("duplicate run responses differ: first=%+v second=%+v", first.response, second.response)
	}
	if first.response.SessionID != created.SessionID {
		t.Fatalf("duplicate run session id = %q, want %q", first.response.SessionID, created.SessionID)
	}
	if first.response.Result != "deduped" {
		t.Fatalf("duplicate run result = %q, want deduped", first.response.Result)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("fake response server hit count = %d, want 2 total requests", got)
	}

	store, err := openWorkspaceSessionStore(workspace, server.URL, created.SessionID)
	if err != nil {
		t.Fatalf("open workspace session store: %v", err)
	}
	messages, err := readStoredMessages(store)
	if err != nil {
		t.Fatalf("read stored messages: %v", err)
	}
	assertMessagePresent(t, messages, llm.RoleUser, "first prompt")
	assertMessagePresent(t, messages, llm.RoleAssistant, "created")
	assertMessagePresent(t, messages, llm.RoleUser, "second prompt")
	assertMessagePresent(t, messages, llm.RoleAssistant, "deduped")

	secondPromptUsers := 0
	secondPromptAssistants := 0
	for _, msg := range messages {
		if msg.Role == llm.RoleUser && msg.Content == "second prompt" {
			secondPromptUsers++
		}
		if msg.Role == llm.RoleAssistant && msg.Content == "deduped" {
			secondPromptAssistants++
		}
	}
	if secondPromptUsers != 1 || secondPromptAssistants != 1 {
		t.Fatalf("expected deduped transcript entries once, got users=%d assistants=%d messages=%+v", secondPromptUsers, secondPromptAssistants, messages)
	}
}

func newFakeResponsesServer(t *testing.T, assistantReplies []string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if testopenai.HandleInputTokenCount(w, r, 11) {
			return
		}
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got == "" {
			t.Fatal("expected authorization header")
		}
		index := int(hits.Add(1)) - 1
		if index >= len(assistantReplies) {
			t.Fatalf("unexpected response request index %d", index)
		}
		testopenai.WriteCompletedResponseStream(w, assistantReplies[index], 11, 7)
	}))
	return server, &hits
}

func openWorkspaceSessionStore(workspaceRoot, openAIBaseURL, sessionID string) (*session.Store, error) {
	loadOpts := config.LoadOptions{}
	if strings.TrimSpace(openAIBaseURL) != "" {
		loadOpts.OpenAIBaseURL = openAIBaseURL
	}
	cfg, err := config.Load(workspaceRoot, loadOpts)
	if err != nil {
		return nil, err
	}
	return session.OpenByID(cfg.PersistenceRoot, sessionID)
}

func readStoredMessages(store *session.Store) ([]llm.Message, error) {
	events, err := store.ReadEvents()
	if err != nil {
		return nil, err
	}
	messages := make([]llm.Message, 0, len(events))
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var msg llm.Message
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func assertMessagePresent(t *testing.T, messages []llm.Message, role llm.Role, content string) {
	t.Helper()
	for _, msg := range messages {
		if msg.Role == role && msg.Content == content {
			return
		}
	}
	t.Fatalf("expected message role=%s content=%q in %+v", role, content, messages)
}
