package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/session"
	"builder/internal/tools"
	"builder/internal/tools/askquestion"
	shelltool "builder/internal/tools/shell"
)

func TestBuildToolRegistry_AllowsHostedWebSearchWithoutLocalFactory(t *testing.T) {
	workspace := t.TempDir()

	registry, _, _, err := buildToolRegistry(
		workspace,
		"",
		[]tools.ID{tools.ToolShell, tools.ToolWebSearch},
		5*time.Second,
		15*time.Second,
		16_000,
		false,
		true,
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
	if defs[0].ID != tools.ToolShell {
		t.Fatalf("expected shell runtime tool definition, got %+v", defs[0])
	}
}

func TestBuildToolRegistry_IncludesParallelWrapperWhenEnabled(t *testing.T) {
	workspace := t.TempDir()

	registry, _, _, err := buildToolRegistry(
		workspace,
		"",
		[]tools.ID{tools.ToolShell, tools.ToolMultiToolUseParallel},
		5*time.Second,
		15*time.Second,
		16_000,
		false,
		true,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("build tool registry: %v", err)
	}

	defs := registry.Definitions()
	if len(defs) != 2 {
		t.Fatalf("expected 2 local runtime tools in registry, got %d", len(defs))
	}
	if defs[0].ID != tools.ToolMultiToolUseParallel || defs[1].ID != tools.ToolShell {
		t.Fatalf("unexpected runtime tool definitions: %+v", defs)
	}
}

func TestBuildToolRegistry_IncludesViewImageWhenEnabled(t *testing.T) {
	workspace := t.TempDir()

	registry, _, _, err := buildToolRegistry(
		workspace,
		"",
		[]tools.ID{tools.ToolViewImage},
		5*time.Second,
		15*time.Second,
		16_000,
		false,
		true,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("build tool registry: %v", err)
	}

	defs := registry.Definitions()
	if len(defs) != 1 {
		t.Fatalf("expected 1 local runtime tool in registry, got %d", len(defs))
	}
	if defs[0].ID != tools.ToolViewImage {
		t.Fatalf("unexpected runtime tool definition: %+v", defs[0])
	}
}

func TestBuildToolRegistry_ViewImageApprovedOutsidePathIsLogged(t *testing.T) {
	workspace := t.TempDir()
	outsideFile := filepath.Join(t.TempDir(), "doc.pdf")
	pdfBytes := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")
	if err := os.WriteFile(outsideFile, pdfBytes, 0o644); err != nil {
		t.Fatalf("write outside pdf: %v", err)
	}

	sessionDir := t.TempDir()
	logger, err := newRunLogger(sessionDir, nil)
	if err != nil {
		t.Fatalf("new run logger: %v", err)
	}

	registry, broker, _, err := buildToolRegistry(
		workspace,
		"",
		[]tools.ID{tools.ToolViewImage},
		5*time.Second,
		15*time.Second,
		16_000,
		false,
		true,
		logger,
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

	viewImageHandler, ok := registry.Get(tools.ToolViewImage)
	if !ok {
		t.Fatal("expected view_image handler")
	}
	input, err := json.Marshal(map[string]any{"path": outsideFile})
	if err != nil {
		t.Fatalf("marshal view_image input: %v", err)
	}
	result, err := viewImageHandler.Call(context.Background(), tools.Call{ID: "call-1", Name: tools.ToolViewImage, Input: input})
	if err != nil {
		t.Fatalf("view_image call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success result, got %s", string(result.Output))
	}

	if err := logger.Close(); err != nil {
		t.Fatalf("close run logger: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(sessionDir, runLogFileName))
	if err != nil {
		t.Fatalf("read run log: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "tool.view_image.outside_workspace.approved") {
		t.Fatalf("expected outside-workspace approval audit line, got %q", text)
	}
	realOutside, err := filepath.EvalSymlinks(outsideFile)
	if err != nil {
		t.Fatalf("resolve outside real path: %v", err)
	}
	if !strings.Contains(text, `reason=allow_once`) {
		t.Fatalf("expected allow_once reason in audit line, got %q", text)
	}
	if !strings.Contains(text, realOutside) {
		t.Fatalf("expected canonical resolved outside path in audit line, got %q", text)
	}
}

func TestBuildToolRegistry_ViewImageConfiguredAllowBypassesApprovalForOutsidePath(t *testing.T) {
	workspace := t.TempDir()
	outsideDir := filepath.Join(t.TempDir(), "missing")
	outsideFile := filepath.Join(outsideDir, "doc.pdf")
	pdfBytes := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("mkdir outside dir: %v", err)
	}
	if err := os.WriteFile(outsideFile, pdfBytes, 0o644); err != nil {
		t.Fatalf("write outside pdf: %v", err)
	}

	registry, broker, _, err := buildToolRegistry(
		workspace,
		"",
		[]tools.ID{tools.ToolViewImage},
		5*time.Second,
		15*time.Second,
		16_000,
		true,
		true,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("build tool registry: %v", err)
	}

	askCalls := 0
	broker.SetAskHandler(func(req askquestion.Request) (askquestion.Response, error) {
		askCalls++
		return askquestion.Response{Approval: &askquestion.ApprovalPayload{Decision: askquestion.ApprovalDecisionAllowOnce}}, nil
	})

	viewImageHandler, ok := registry.Get(tools.ToolViewImage)
	if !ok {
		t.Fatal("expected view_image handler")
	}
	input, err := json.Marshal(map[string]any{"path": outsideFile})
	if err != nil {
		t.Fatalf("marshal view_image input: %v", err)
	}
	result, err := viewImageHandler.Call(context.Background(), tools.Call{ID: "call-config-allow", Name: tools.ToolViewImage, Input: input})
	if err != nil {
		t.Fatalf("view_image call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success result, got %s", string(result.Output))
	}
	if askCalls != 0 {
		t.Fatalf("expected configured allow to bypass approval, got %d asks", askCalls)
	}
}

func TestRuntimeWiringCloseDoesNotCloseSharedBackgroundManager(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	wiring := &runtimeWiring{background: manager}
	if err := wiring.Close(); err != nil {
		t.Fatalf("close wiring: %v", err)
	}

	if _, _, _, err := buildToolRegistry(t.TempDir(), "", []tools.ID{tools.ToolExecCommand}, 5*time.Second, 15*time.Second, 16_000, false, true, nil, manager); err != nil {
		t.Fatalf("expected shared background manager to remain usable after wiring close: %v", err)
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
	_, err = runtime.New(storeA, clientA, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine A: %v", err)
	}
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

	router := &backgroundEventRouter{}
	router.SetActiveSession(storeB.Meta().SessionID, engB)
	router.handle(shelltool.Event{Snapshot: shelltool.Snapshot{ID: "1000", OwnerSessionID: storeA.Meta().SessionID, State: "completed", Command: "builder run", Workdir: root, LogPath: filepath.Join(root, "1000.log")}, Type: shelltool.EventCompleted, Preview: "done"})

	time.Sleep(150 * time.Millisecond)
	if got := clientB.CallCount(); got != 0 {
		t.Fatalf("expected orphaned completion to skip model notice for active session, got %d client calls", got)
	}
	mu.Lock()
	updates := backgroundUpdates
	mu.Unlock()
	if updates != 1 {
		t.Fatalf("expected orphaned completion to still emit one background update event, got %d", updates)
	}
	if got := clientA.CallCount(); got != 0 {
		t.Fatalf("did not expect inactive owner engine to be called, got %d", got)
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

	router := &backgroundEventRouter{}
	router.SetActiveSession(store.Meta().SessionID, eng)
	router.handle(shelltool.Event{Snapshot: shelltool.Snapshot{ID: "1001", OwnerSessionID: store.Meta().SessionID, State: "completed", Command: "builder run", Workdir: root, LogPath: filepath.Join(root, "1001.log")}, Type: shelltool.EventCompleted, Preview: "done"})

	deadline := time.Now().Add(2 * time.Second)
	for client.CallCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := client.CallCount(); got == 0 {
		t.Fatal("expected active owner completion to queue a model notice")
	}
}

func TestBackgroundEventRouterShapesBackgroundNoticeByOutputMode(t *testing.T) {
	tests := []struct {
		name            string
		mode            shelltool.BackgroundOutputMode
		exitCode        int
		maxChars        int
		content         string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:     "concise success omits output section",
			mode:     shelltool.BackgroundOutputConcise,
			exitCode: 0,
			maxChars: 16,
			content:  "alpha\nbeta\ngamma\n",
			wantContains: []string{
				"Output file (3 lines):",
			},
			wantNotContains: []string{
				"Output:",
				"alpha",
			},
		},
		{
			name:     "verbose success keeps full output",
			mode:     shelltool.BackgroundOutputVerbose,
			exitCode: 0,
			maxChars: 5,
			content:  "alpha\nbeta\ngamma\n",
			wantContains: []string{
				"Output:",
				"alpha\nbeta\ngamma",
			},
			wantNotContains: []string{
				"omitted",
			},
		},
		{
			name:     "concise non-zero falls back to default truncation",
			mode:     shelltool.BackgroundOutputConcise,
			exitCode: 17,
			maxChars: 32,
			content:  "alpha line\n" + strings.Repeat("middle-noise-", 40) + "\nomega line\n",
			wantContains: []string{
				"Output:",
				"alpha line",
				"omega line",
				"Omitted ",
				"read log file for details",
			},
		},
		{
			name:     "verbose non-zero keeps full output",
			mode:     shelltool.BackgroundOutputVerbose,
			exitCode: 17,
			maxChars: 5,
			content:  "alpha\nbeta\ngamma\n",
			wantContains: []string{
				"Output:",
				"alpha\nbeta\ngamma",
			},
			wantNotContains: []string{
				"omitted",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := session.Create(root, "ws", root)
			if err != nil {
				t.Fatalf("create store: %v", err)
			}
			client := &busyToggleFakeClient{}
			events := make(chan runtime.Event, 4)
			eng, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{
				Model: "gpt-5",
				OnEvent: func(evt runtime.Event) {
					if evt.Kind == runtime.EventBackgroundUpdated {
						events <- evt
					}
				},
			})
			if err != nil {
				t.Fatalf("new engine: %v", err)
			}

			logPath := filepath.Join(root, "1000.log")
			if err := os.WriteFile(logPath, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("write log: %v", err)
			}

			router := newBackgroundEventRouter(nil, tt.maxChars, tt.mode)
			router.SetActiveSession(store.Meta().SessionID, eng)
			router.handle(shelltool.Event{
				Type: shelltool.EventCompleted,
				Snapshot: shelltool.Snapshot{
					ID:             "1000",
					OwnerSessionID: "other-session",
					State:          "completed",
					LogPath:        logPath,
					ExitCode:       &tt.exitCode,
				},
			})

			select {
			case evt := <-events:
				if evt.Background == nil {
					t.Fatal("expected background payload")
				}
				for _, needle := range tt.wantContains {
					if !strings.Contains(evt.Background.NoticeText, needle) {
						t.Fatalf("expected notice to contain %q, got %q", needle, evt.Background.NoticeText)
					}
				}
				for _, needle := range tt.wantNotContains {
					if strings.Contains(evt.Background.NoticeText, needle) {
						t.Fatalf("expected notice to omit %q, got %q", needle, evt.Background.NoticeText)
					}
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for background update event")
			}
		})
	}
}

func TestBackgroundEventRouterWhitespacePreviewUsesNoOutputLine(t *testing.T) {
	root := t.TempDir()
	store, err := session.Create(root, "ws", root)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &busyToggleFakeClient{}
	events := make(chan runtime.Event, 4)
	eng, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{
		Model: "gpt-5",
		OnEvent: func(evt runtime.Event) {
			if evt.Kind == runtime.EventBackgroundUpdated {
				events <- evt
			}
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	router := newBackgroundEventRouter(nil, 80, shelltool.BackgroundOutputDefault)
	router.SetActiveSession(store.Meta().SessionID, eng)
	exitCode := 0
	router.handle(shelltool.Event{
		Type: shelltool.EventCompleted,
		Snapshot: shelltool.Snapshot{
			ID:             "1000",
			OwnerSessionID: "other-session",
			State:          "completed",
			ExitCode:       &exitCode,
		},
		Preview: "  \n\t  ",
	})

	select {
	case evt := <-events:
		if evt.Background == nil {
			t.Fatal("expected background payload")
		}
		if !strings.Contains(evt.Background.NoticeText, "\nno output") {
			t.Fatalf("expected no output line, got %q", evt.Background.NoticeText)
		}
		if strings.Contains(evt.Background.NoticeText, "Output:") {
			t.Fatalf("did not expect output header for blank preview, got %q", evt.Background.NoticeText)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for background update event")
	}
}

func TestBuildToolRegistryExecCommandPropagatesOwnerSessionID(t *testing.T) {
	workspace := t.TempDir()
	registry, _, manager, err := buildToolRegistry(
		workspace,
		"session-owner-1",
		[]tools.ID{tools.ToolExecCommand},
		5*time.Second,
		250*time.Millisecond,
		16_000,
		false,
		true,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("build tool registry: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	handler, ok := registry.Get(tools.ToolExecCommand)
	if !ok {
		t.Fatal("expected exec_command handler")
	}
	input, err := json.Marshal(map[string]any{
		"cmd":           "printf owner-check\\n; sleep 30",
		"yield_time_ms": 250,
	})
	if err != nil {
		t.Fatalf("marshal exec_command input: %v", err)
	}
	if _, err := handler.Call(context.Background(), tools.Call{ID: "call-1", Name: tools.ToolExecCommand, Input: input}); err != nil {
		t.Fatalf("exec_command call: %v", err)
	}
	entries := manager.List()
	if len(entries) != 1 {
		t.Fatalf("expected one background process, got %d", len(entries))
	}
	if entries[0].OwnerSessionID != "session-owner-1" {
		t.Fatalf("expected owner session id propagation, got %q", entries[0].OwnerSessionID)
	}
}

func TestBackgroundEventRouterDoesNotRetroactivelyQueueNoticeAfterOwnerSessionResume(t *testing.T) {
	root := t.TempDir()
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	router := newBackgroundEventRouter(manager, 16_000, shelltool.BackgroundOutputDefault)

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
	engB, err := runtime.New(storeB, clientB, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine B: %v", err)
	}

	router.SetActiveSession(storeA.Meta().SessionID, engA)
	workdir := t.TempDir()
	res, err := manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "printf resume-check\\n; sleep 1"},
		DisplayCommand: "resume-check",
		OwnerSessionID: storeA.Meta().SessionID,
		Workdir:        workdir,
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start process: %v", err)
	}
	if !res.Backgrounded {
		t.Fatal("expected process to background")
	}
	router.SetActiveSession(storeB.Meta().SessionID, engB)

	deadline := time.Now().Add(2 * time.Second)
	for {
		entries := manager.List()
		if len(entries) == 1 && !entries[0].Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for background process completion")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := clientA.CallCount(); got != 0 {
		t.Fatalf("expected owner session to receive no notice while orphaned, got %d", got)
	}
	if got := clientB.CallCount(); got != 0 {
		t.Fatalf("expected active foreign session to receive no notice, got %d", got)
	}

	router.SetActiveSession(storeA.Meta().SessionID, engA)
	time.Sleep(150 * time.Millisecond)
	if got := clientA.CallCount(); got != 0 {
		t.Fatalf("expected no retroactive notice on owner session resume, got %d", got)
	}
	entries := manager.List()
	if len(entries) != 1 || entries[0].ID != res.SessionID || entries[0].Running {
		t.Fatalf("expected finished process to remain visible in manager state after resume, got %+v", entries)
	}
}

func TestBackgroundEventRouterDropsNoticeWhenNoSessionIsActive(t *testing.T) {
	root := t.TempDir()
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	router := newBackgroundEventRouter(manager, 16_000, shelltool.BackgroundOutputDefault)

	store, err := session.Create(root, "ws", root)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &busyToggleFakeClient{responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal}, Usage: llm.Usage{WindowTokens: 200_000}}}}
	eng, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	router.SetActiveSession(store.Meta().SessionID, eng)
	router.ClearActiveSession(store.Meta().SessionID)
	router.handle(shelltool.Event{Snapshot: shelltool.Snapshot{ID: "1002", OwnerSessionID: store.Meta().SessionID, State: "completed"}, Type: shelltool.EventCompleted, Preview: "done"})
	time.Sleep(150 * time.Millisecond)
	if got := client.CallCount(); got != 0 {
		t.Fatalf("expected no notice delivery while no session is active, got %d", got)
	}
}
