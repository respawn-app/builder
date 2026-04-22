package runtimewire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	"builder/server/tools/askquestion"
	patchtool "builder/server/tools/patch"
	shelltool "builder/server/tools/shell"
	"builder/shared/config"
	"builder/shared/toolspec"
)

func TestBuildToolRegistryAllowsHostedWebSearchWithoutLocalRuntimeBuilder(t *testing.T) {
	workspace := t.TempDir()

	registry, _, _, err := BuildToolRegistry(
		workspace,
		"",
		[]toolspec.ID{toolspec.ToolExecCommand, toolspec.ToolWebSearch},
		15*time.Second,
		16_000,
		false,
		true,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("build tool registry: %v", err)
	}

	defs := registry.Definitions()
	if len(defs) != 1 {
		t.Fatalf("expected only local runtime tools in registry, got %d", len(defs))
	}
	if defs[0].ID != toolspec.ToolExecCommand {
		t.Fatalf("expected exec_command runtime tool definition, got %+v", defs[0])
	}
}

func TestBuildToolRegistryViewImageApprovedOutsidePathIsLogged(t *testing.T) {
	workspace := t.TempDir()
	outsideFile := filepath.Join(outsideNonTempDir(t), "doc.pdf")
	pdfBytes := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")
	if err := os.WriteFile(outsideFile, pdfBytes, 0o644); err != nil {
		t.Fatalf("write outside pdf: %v", err)
	}

	logger := &testLogger{}
	registry, broker, _, err := BuildToolRegistry(
		workspace,
		"",
		[]toolspec.ID{toolspec.ToolViewImage},
		15*time.Second,
		16_000,
		false,
		true,
		logger,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("build tool registry: %v", err)
	}
	broker.SetAskHandler(func(req askquestion.Request) (askquestion.Response, error) {
		if !strings.Contains(req.Question, "Allow reading") {
			t.Fatalf("expected read-focused approval question, got %q", req.Question)
		}
		return askquestion.Response{Approval: &askquestion.ApprovalPayload{Decision: askquestion.ApprovalDecisionAllowOnce}}, nil
	})

	viewImageHandler, ok := registry.Get(toolspec.ToolViewImage)
	if !ok {
		t.Fatal("expected view_image handler")
	}
	input, err := json.Marshal(map[string]any{"path": outsideFile})
	if err != nil {
		t.Fatalf("marshal view_image input: %v", err)
	}
	result, err := viewImageHandler.Call(context.Background(), tools.Call{ID: "call-1", Name: toolspec.ToolViewImage, Input: input})
	if err != nil {
		t.Fatalf("view_image call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success result, got %s", string(result.Output))
	}
	if !strings.Contains(logger.String(), "tool.view_image.outside_workspace.approved") {
		t.Fatalf("expected outside-workspace approval audit line, got %q", logger.String())
	}
	if !strings.Contains(logger.String(), "reason=allow_once") {
		t.Fatalf("expected allow_once reason in audit line, got %q", logger.String())
	}
}

func TestBuildToolRegistryMissingWorkspaceRootSuggestsRebind(t *testing.T) {
	tests := []struct {
		name string
		tool toolspec.ID
	}{
		{name: "patch", tool: toolspec.ToolPatch},
		{name: "view_image", tool: toolspec.ToolViewImage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			missingWorkspace := filepath.Join(t.TempDir(), "workspace-removed")
			newWorkspace := t.TempDir()
			t.Chdir(newWorkspace)
			sessionID := "session-1"

			_, _, _, err := BuildToolRegistry(
				missingWorkspace,
				sessionID,
				[]toolspec.ID{tt.tool},
				15*time.Second,
				16_000,
				false,
				true,
				nil,
				nil,
				nil,
			)
			if err == nil {
				t.Fatal("expected build tool registry error for missing workspace root")
			}
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected os.ErrNotExist, got %v", err)
			}
			want := `workspace root ` + strconv.Quote(missingWorkspace) + ` is missing; run ` + "`builder rebind " + strconv.Quote(sessionID) + " " + strconv.Quote(newWorkspace) + "`"
			if got := err.Error(); got != want {
				t.Fatalf("error = %q, want %q", got, want)
			}
		})
	}
}

func TestLocalToolRegistryBindingRebindUpdatesExecCommandRoot(t *testing.T) {
	rootA := filepath.Join(t.TempDir(), "workspace-a")
	rootB := filepath.Join(t.TempDir(), "workspace-b")
	if err := os.MkdirAll(rootA, 0o755); err != nil {
		t.Fatalf("mkdir rootA: %v", err)
	}
	if err := os.MkdirAll(rootB, 0o755); err != nil {
		t.Fatalf("mkdir rootB: %v", err)
	}
	binding, _, _, err := NewLocalToolRegistryBinding(
		rootA,
		"",
		[]toolspec.ID{toolspec.ToolExecCommand},
		15*time.Second,
		16_000,
		false,
		true,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("new local tool registry binding: %v", err)
	}
	if got := shellPwdOutput(t, binding.Registry()); got != canonicalPathForTest(t, rootA) {
		t.Fatalf("pwd before rebind = %q, want %q", got, canonicalPathForTest(t, rootA))
	}
	if err := binding.Rebind(rootB); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if got := shellPwdOutput(t, binding.Registry()); got != canonicalPathForTest(t, rootB) {
		t.Fatalf("pwd after rebind = %q, want %q", got, canonicalPathForTest(t, rootB))
	}
}

func TestBackgroundEventRouterSkipsDeveloperNoticeForOrphanedShells(t *testing.T) {
	root := t.TempDir()
	storeA, err := session.Create(root, "ws-a", root)
	if err != nil {
		t.Fatalf("create store A: %v", err)
	}
	storeB, err := session.Create(root, "ws-b", root)
	if err != nil {
		t.Fatalf("create store B: %v", err)
	}
	clientA := &busyToggleFakeClient{responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "a", Phase: llm.MessagePhaseFinal}, Usage: llm.Usage{WindowTokens: 200_000}}}}
	clientB := &busyToggleFakeClient{responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "b", Phase: llm.MessagePhaseFinal}, Usage: llm.Usage{WindowTokens: 200_000}}}}
	var mu sync.Mutex
	backgroundUpdates := 0
	engA, err := runtime.New(storeA, clientA, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine A: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := engA.Close(); closeErr != nil {
			t.Fatalf("close engine A: %v", closeErr)
		}
	})
	engB, err := runtime.New(storeB, clientB, tools.NewRegistry(), runtime.Config{Model: "gpt-5", OnEvent: func(evt runtime.Event) {
		if evt.Kind == runtime.EventBackgroundUpdated {
			mu.Lock()
			backgroundUpdates++
			mu.Unlock()
		}
	}})
	if err != nil {
		t.Fatalf("new engine B: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := engB.Close(); closeErr != nil {
			t.Fatalf("close engine B: %v", closeErr)
		}
	})

	router := &BackgroundEventRouter{}
	router.SetActiveSession(storeB.Meta().SessionID, engB)
	router.Handle(shelltool.Event{Snapshot: shelltool.Snapshot{ID: "1000", OwnerSessionID: storeA.Meta().SessionID, State: "completed", Command: "builder run", Workdir: root, LogPath: filepath.Join(root, "1000.log")}, Type: shelltool.EventCompleted, Preview: "done"})

	time.Sleep(150 * time.Millisecond)
	if got := clientB.CallCount(); got != 0 {
		t.Fatalf("expected orphaned completion to skip model notice for active session, got %d client calls", got)
	}
	mu.Lock()
	updates := backgroundUpdates
	mu.Unlock()
	if updates != 0 {
		t.Fatalf("expected orphaned completion to stay isolated from foreign active sessions, got %d background updates", updates)
	}
	if got := clientA.CallCount(); got != 0 {
		t.Fatalf("did not expect inactive owner engine to be called, got %d", got)
	}
}

func TestBackgroundEventRouterRoutesCompletionToMatchingActiveOwnerSession(t *testing.T) {
	root := t.TempDir()
	storeA, err := session.Create(root, "ws-a", root)
	if err != nil {
		t.Fatalf("create store A: %v", err)
	}
	storeB, err := session.Create(root, "ws-b", root)
	if err != nil {
		t.Fatalf("create store B: %v", err)
	}
	clientA := &busyToggleFakeClient{responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "a", Phase: llm.MessagePhaseFinal}, Usage: llm.Usage{WindowTokens: 200_000}}}}
	clientB := &busyToggleFakeClient{responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "b", Phase: llm.MessagePhaseFinal}, Usage: llm.Usage{WindowTokens: 200_000}}}}
	engA, err := runtime.New(storeA, clientA, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine A: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := engA.Close(); closeErr != nil {
			t.Fatalf("close engine A: %v", closeErr)
		}
	})
	engB, err := runtime.New(storeB, clientB, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine B: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := engB.Close(); closeErr != nil {
			t.Fatalf("close engine B: %v", closeErr)
		}
	})

	router := &BackgroundEventRouter{}
	router.SetActiveSession(storeA.Meta().SessionID, engA)
	router.SetActiveSession(storeB.Meta().SessionID, engB)
	router.Handle(shelltool.Event{Snapshot: shelltool.Snapshot{ID: "1002", OwnerSessionID: storeA.Meta().SessionID, State: "completed", Command: "builder run", Workdir: root, LogPath: filepath.Join(root, "1002.log")}, Type: shelltool.EventCompleted, Preview: "done"})

	deadline := time.Now().Add(2 * time.Second)
	for clientA.CallCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := clientA.CallCount(); got == 0 {
		t.Fatal("expected owner session completion to route to its active engine even when another session is also active")
	}
	if got := clientB.CallCount(); got != 0 {
		t.Fatalf("did not expect foreign active session to receive routed completion, got %d", got)
	}
}

func TestBackgroundEventRouterClearActiveSessionDropsOnlyThatOwner(t *testing.T) {
	root := t.TempDir()
	storeA, err := session.Create(root, "ws-a", root)
	if err != nil {
		t.Fatalf("create store A: %v", err)
	}
	storeB, err := session.Create(root, "ws-b", root)
	if err != nil {
		t.Fatalf("create store B: %v", err)
	}
	clientA := &busyToggleFakeClient{responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "a", Phase: llm.MessagePhaseFinal}, Usage: llm.Usage{WindowTokens: 200_000}}}}
	clientB := &busyToggleFakeClient{responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "b", Phase: llm.MessagePhaseFinal}, Usage: llm.Usage{WindowTokens: 200_000}}}}
	engA, err := runtime.New(storeA, clientA, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine A: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := engA.Close(); closeErr != nil {
			t.Fatalf("close engine A: %v", closeErr)
		}
	})
	engB, err := runtime.New(storeB, clientB, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine B: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := engB.Close(); closeErr != nil {
			t.Fatalf("close engine B: %v", closeErr)
		}
	})

	router := &BackgroundEventRouter{}
	router.SetActiveSession(storeA.Meta().SessionID, engA)
	router.SetActiveSession(storeB.Meta().SessionID, engB)
	router.ClearActiveSession(storeA.Meta().SessionID)
	router.Handle(shelltool.Event{Snapshot: shelltool.Snapshot{ID: "1003", OwnerSessionID: storeA.Meta().SessionID, State: "completed", Command: "builder run", Workdir: root, LogPath: filepath.Join(root, "1003.log")}, Type: shelltool.EventCompleted, Preview: "done"})
	time.Sleep(150 * time.Millisecond)
	if got := clientA.CallCount(); got != 0 {
		t.Fatalf("expected cleared owner session to drop completions, got %d", got)
	}
	if got := clientB.CallCount(); got != 0 {
		t.Fatalf("did not expect foreign active session to receive cleared-owner completion, got %d", got)
	}

	router.Handle(shelltool.Event{Snapshot: shelltool.Snapshot{ID: "1004", OwnerSessionID: storeB.Meta().SessionID, State: "completed", Command: "builder run", Workdir: root, LogPath: filepath.Join(root, "1004.log")}, Type: shelltool.EventCompleted, Preview: "done"})
	deadline := time.Now().Add(2 * time.Second)
	for clientB.CallCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := clientB.CallCount(); got == 0 {
		t.Fatal("expected other active sessions to keep receiving their own completions after clearing a different owner")
	}
}

func TestBackgroundEventRouterQueuesNoticeForActiveOwnerSession(t *testing.T) {
	root := t.TempDir()
	store, err := session.Create(root, "ws", root)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &busyToggleFakeClient{responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "notice handled", Phase: llm.MessagePhaseFinal}, Usage: llm.Usage{WindowTokens: 200_000}}}}
	eng, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := eng.Close(); closeErr != nil {
			t.Fatalf("close engine: %v", closeErr)
		}
	})

	router := &BackgroundEventRouter{}
	router.SetActiveSession(store.Meta().SessionID, eng)
	router.Handle(shelltool.Event{Snapshot: shelltool.Snapshot{ID: "1001", OwnerSessionID: store.Meta().SessionID, State: "completed", Command: "builder run", Workdir: root, LogPath: filepath.Join(root, "1001.log")}, Type: shelltool.EventCompleted, Preview: "done"})

	deadline := time.Now().Add(2 * time.Second)
	for client.CallCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := client.CallCount(); got == 0 {
		t.Fatal("expected active owner completion to queue a model notice")
	}
}

func TestNewRuntimeWiringRejectsEmptyModelAfterBypassingConfigDefaults(t *testing.T) {
	root := t.TempDir()
	store, err := session.Create(root, "ws", root)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	_, err = NewRuntimeWiringWithBackground(
		store,
		config.Settings{
			Model:              "",
			ProviderOverride:   "openai",
			OpenAIBaseURL:      "http://example.test/v1",
			ModelContextWindow: 272_000,
			Timeouts: config.Timeouts{
				ModelRequestSeconds: 1,
			},
		},
		[]toolspec.ID{toolspec.ToolExecCommand},
		root,
		auth.NewManager(auth.NewMemoryStore(auth.EmptyState()), nil, nil),
		nil,
		nil,
		RuntimeWiringOptions{},
	)
	if err == nil {
		t.Fatal("expected runtime wiring to reject empty model")
	}
	if !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("expected model-required error, got %v", err)
	}
}

type testLogger struct {
	lines []string
}

func (l *testLogger) Logf(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *testLogger) String() string {
	return strings.Join(l.lines, "\n")
}

func outsideNonTempDir(t *testing.T) string {
	t.Helper()
	bases := make([]string, 0, 2)
	if wd, err := os.Getwd(); err == nil {
		bases = append(bases, wd)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		bases = append(bases, home)
	}
	for _, base := range bases {
		dir, err := os.MkdirTemp(base, "builder-runtimewire-outside-*")
		if err != nil {
			continue
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			_ = os.RemoveAll(dir)
			continue
		}
		if patchtool.IsPathInTemporaryDir(abs) {
			_ = os.RemoveAll(dir)
			continue
		}
		t.Cleanup(func() {
			_ = os.RemoveAll(dir)
		})
		return abs
	}
	t.Skip("unable to create non-temporary outside directory for test")
	return ""
}

func shellPwdOutput(t *testing.T, registry *tools.Registry) string {
	t.Helper()
	handler, ok := registry.Get(toolspec.ToolExecCommand)
	if !ok {
		t.Fatal("expected exec_command handler")
	}
	result, err := handler.Call(context.Background(), tools.Call{ID: "call-pwd", Name: toolspec.ToolExecCommand, Input: json.RawMessage(`{"command":"pwd"}`)})
	if err != nil {
		t.Fatalf("exec_command call: %v", err)
	}
	var payload string
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatalf("decode exec_command output: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(payload), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected exec_command output, got %q", payload)
	}
	return canonicalPathForTest(t, strings.TrimSpace(lines[len(lines)-1]))
}

func canonicalPathForTest(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("canonicalize path %q: %v", path, err)
	}
	return filepath.Clean(canonical)
}

type busyToggleFakeClient struct {
	mu        sync.Mutex
	responses []llm.Response
	calls     int
}

func (f *busyToggleFakeClient) Generate(ctx context.Context, _ llm.Request) (llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if len(f.responses) == 0 {
		return llm.Response{}, errors.New("no fake response configured")
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func (f *busyToggleFakeClient) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}
