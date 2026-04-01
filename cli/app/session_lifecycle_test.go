package app

import (
	"builder/cli/tui"
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	shelltool "builder/server/tools/shell"
	"builder/shared/config"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestResolveSessionActionResumeReopensPicker(t *testing.T) {
	resolved, err := resolveSessionAction(
		context.Background(),
		&testEmbeddedServer{},
		nil,
		nil,
		UITransition{Action: UIActionResume},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !resolved.ShouldContinue {
		t.Fatal("expected lifecycle to continue for resume action")
	}
	if resolved.NextSessionID != "" {
		t.Fatalf("expected empty session id to force picker, got %q", resolved.NextSessionID)
	}
	if resolved.ForceNewSession {
		t.Fatal("did not expect force-new for resume action")
	}
	if resolved.ParentSessionID != "" {
		t.Fatalf("expected no parent session id on resume, got %q", resolved.ParentSessionID)
	}
	if resolved.InitialPrompt != "" || resolved.InitialInput != "" {
		t.Fatalf("expected no initial payload on resume, got prompt=%q input=%q", resolved.InitialPrompt, resolved.InitialInput)
	}
}

func TestResolveSessionActionNewSessionUsesForceNewFlow(t *testing.T) {
	resolved, err := resolveSessionAction(
		context.Background(),
		&testEmbeddedServer{},
		nil,
		nil,
		UITransition{Action: UIActionNewSession, InitialPrompt: "hello", ParentSessionID: "parent-1"},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !resolved.ShouldContinue {
		t.Fatal("expected lifecycle to continue for new session action")
	}
	if !resolved.ForceNewSession {
		t.Fatal("expected force-new session flow")
	}
	if resolved.NextSessionID != "" {
		t.Fatalf("expected empty session id for force-new flow, got %q", resolved.NextSessionID)
	}
	if resolved.ParentSessionID != "parent-1" {
		t.Fatalf("expected parent session id passthrough, got %q", resolved.ParentSessionID)
	}
	if resolved.InitialPrompt != "hello" || resolved.InitialInput != "" {
		t.Fatalf("expected initial prompt passthrough, got prompt=%q input=%q", resolved.InitialPrompt, resolved.InitialInput)
	}
}

func TestNewSessionTransitionKeepsBackgroundProcessesAlive(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	workdir := t.TempDir()
	res, err := manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "printf 'transition-job\n'; sleep 30"},
		DisplayCommand: "transition-job",
		Workdir:        workdir,
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start background process: %v", err)
	}
	if !res.Backgrounded {
		t.Fatal("expected process to move to background")
	}

	root := t.TempDir()
	resolved, err := resolveSessionAction(
		context.Background(),
		&testEmbeddedServer{background: manager},
		nil,
		nil,
		UITransition{Action: UIActionNewSession, InitialPrompt: "hello", ParentSessionID: "parent-1"},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !resolved.ShouldContinue || !resolved.ForceNewSession {
		t.Fatalf("expected new-session transition, shouldContinue=%t forceNew=%t", resolved.ShouldContinue, resolved.ForceNewSession)
	}
	if resolved.NextSessionID != "" || resolved.InitialPrompt != "hello" || resolved.InitialInput != "" {
		t.Fatalf("unexpected transition payload nextSessionID=%q initialPrompt=%q initialInput=%q", resolved.NextSessionID, resolved.InitialPrompt, resolved.InitialInput)
	}

	wiring := &runtimeWiring{background: manager}
	if err := wiring.Close(); err != nil {
		t.Fatalf("close wiring: %v", err)
	}

	planner := &launchPlanner{
		server: &testEmbeddedServer{
			cfg: config.App{
				WorkspaceRoot:   workdir,
				PersistenceRoot: root,
				Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenAuto},
			},
			containerDir: root,
		},
	}
	storePlan, err := planner.PlanSession(sessionLaunchRequest{
		Mode:              launchModeInteractive,
		SelectedSessionID: resolved.NextSessionID,
		ForceNewSession:   resolved.ForceNewSession,
		ParentSessionID:   resolved.ParentSessionID,
	})
	if err != nil {
		t.Fatalf("open or create next session: %v", err)
	}
	store := storePlan.Store
	if store.Meta().ParentSessionID != "parent-1" {
		t.Fatalf("expected parent session id preserved across new session transition, got %q", store.Meta().ParentSessionID)
	}
	entries := manager.List()
	if len(entries) != 1 {
		t.Fatalf("expected background process to survive session transition, got %d entries", len(entries))
	}
	if entries[0].ID != res.SessionID {
		t.Fatalf("expected surviving background process %s, got %s", res.SessionID, entries[0].ID)
	}
}

func TestResolveSessionActionForkRollbackTeleportsToForkWithPrompt(t *testing.T) {
	root := t.TempDir()
	store, err := session.Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleUser, Content: "u1"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleAssistant, Content: "a1"}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}

	resolved, err := resolveSessionAction(
		context.Background(),
		&testEmbeddedServer{cfg: config.App{PersistenceRoot: root}},
		nil,
		store,
		UITransition{Action: UIActionForkRollback, InitialPrompt: "edited user message", ForkUserMessageIndex: 1},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !resolved.ShouldContinue {
		t.Fatal("expected lifecycle to continue for fork rollback action")
	}
	if resolved.ForceNewSession {
		t.Fatal("did not expect force-new for fork rollback action")
	}
	if resolved.ParentSessionID != "" {
		t.Fatalf("expected no deferred parent for pre-created fork session, got %q", resolved.ParentSessionID)
	}
	if resolved.NextSessionID == "" {
		t.Fatal("expected target fork session id")
	}
	if resolved.NextSessionID == store.Meta().SessionID {
		t.Fatalf("expected fork session id to differ from parent, got %q", resolved.NextSessionID)
	}
	if resolved.InitialPrompt != "edited user message" || resolved.InitialInput != "" {
		t.Fatalf("expected initial prompt passthrough, got prompt=%q input=%q", resolved.InitialPrompt, resolved.InitialInput)
	}
}

func TestForkRollbackLifecycleDoesNotPersistEditedPromptAsSourceDraft(t *testing.T) {
	root := t.TempDir()
	store, err := session.Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create source store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleUser, Content: "u1"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleAssistant, Content: "a1"}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}

	m := newProjectedStaticUIModel()
	testSetRollbackEditing(m, 0, 1)
	m.input = "edited user message"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected quit cmd for rollback fork")
	}
	if err := persistSessionDraft(store, updated); err != nil {
		t.Fatalf("persist source draft: %v", err)
	}
	reopenedSource, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("reopen source store: %v", err)
	}
	if reopenedSource.Meta().InputDraft != "" {
		t.Fatalf("expected no persisted source draft after fork handoff, got %q", reopenedSource.Meta().InputDraft)
	}

	resolved, err := resolveSessionAction(context.Background(), &testEmbeddedServer{cfg: config.App{PersistenceRoot: root}}, nil, reopenedSource, updated.Transition())
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if resolved.InitialPrompt != "edited user message" {
		t.Fatalf("expected fork prompt passthrough, got %q", resolved.InitialPrompt)
	}
	if resolved.InitialInput != "" {
		t.Fatalf("expected no fork input draft payload, got %q", resolved.InitialInput)
	}
}

func TestResolveSessionActionOpenSessionUsesTargetID(t *testing.T) {
	resolved, err := resolveSessionAction(
		context.Background(),
		&testEmbeddedServer{},
		nil,
		nil,
		UITransition{Action: UIActionOpenSession, TargetSessionID: "session-42", InitialInput: "draft reply"},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !resolved.ShouldContinue {
		t.Fatal("expected lifecycle to continue for open session action")
	}
	if resolved.NextSessionID != "session-42" {
		t.Fatalf("expected target session id passthrough, got %q", resolved.NextSessionID)
	}
	if resolved.InitialPrompt != "" {
		t.Fatalf("expected no initial prompt, got %q", resolved.InitialPrompt)
	}
	if resolved.InitialInput != "draft reply" {
		t.Fatalf("expected initial input passthrough, got %q", resolved.InitialInput)
	}
	if resolved.ParentSessionID != "" {
		t.Fatalf("expected no parent session id, got %q", resolved.ParentSessionID)
	}
	if resolved.ForceNewSession {
		t.Fatal("did not expect force-new session")
	}
}

func TestBackTeleportLifecycleSeedsParentDraftWithoutAutoSubmit(t *testing.T) {
	tests := []struct {
		name                string
		childMessages       []llm.Message
		childOngoing        string
		childActivity       uiActivity
		existingParentDraft string
		want                string
	}{
		{name: "copy idle child final assistant reply", childMessages: []llm.Message{{Role: llm.RoleAssistant, Content: "test", Phase: llm.MessagePhaseFinal}}, childActivity: uiActivityIdle, want: "test"},
		{name: "copy latest child final assistant reply past reminder entry", childMessages: []llm.Message{{Role: llm.RoleAssistant, Content: "test", Phase: llm.MessagePhaseFinal}, {Role: llm.RoleDeveloper, MessageType: llm.MessageTypeCompactionSoonReminder, Content: "heads up"}}, childActivity: uiActivityIdle, want: "test"},
		{name: "copy latest child final assistant reply past trailing error feedback", childMessages: []llm.Message{{Role: llm.RoleAssistant, Content: "test", Phase: llm.MessagePhaseFinal}, {Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: "phase mismatch"}}, childActivity: uiActivityIdle, want: "test"},
		{name: "ignore interrupted child streaming reply", childOngoing: "review findings", childActivity: uiActivityInterrupted, want: ""},
		{name: "preserve existing parent draft", childMessages: []llm.Message{{Role: llm.RoleAssistant, Content: "test", Phase: llm.MessagePhaseFinal}}, childActivity: uiActivityIdle, existingParentDraft: "keep existing", want: "keep existing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			parentStore, err := session.Create(root, "workspace-x", "/tmp/work")
			if err != nil {
				t.Fatalf("create parent store: %v", err)
			}
			if err := parentStore.SetInputDraft(tt.existingParentDraft); err != nil {
				t.Fatalf("set parent draft: %v", err)
			}

			childStore, err := session.Create(root, "workspace-x", "/tmp/work")
			if err != nil {
				t.Fatalf("create child store: %v", err)
			}
			if err := childStore.SetParentSessionID(parentStore.Meta().SessionID); err != nil {
				t.Fatalf("set child parent id: %v", err)
			}

			for idx, message := range tt.childMessages {
				if _, err := childStore.AppendEvent("step-1", "message", message); err != nil {
					t.Fatalf("append child transcript message %d: %v", idx, err)
				}
			}
			childEngine, err := runtime.New(childStore, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
			if err != nil {
				t.Fatalf("new child engine after transcript seed: %v", err)
			}
			childModel := newProjectedEngineUIModel(childEngine)
			childModel.activity = tt.childActivity
			if tt.childOngoing != "" {
				childModel.forwardToView(tui.SetConversationMsg{Entries: childModel.transcriptEntries, Ongoing: tt.childOngoing})
			}
			childModel.input = "/back"

			next, cmd := childModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
			updatedChild := next.(*uiModel)
			if cmd == nil {
				t.Fatal("expected quit cmd for /back")
			}
			if err := persistSessionDraft(childStore, updatedChild); err != nil {
				t.Fatalf("persist child draft: %v", err)
			}

			resolved, err := resolveSessionAction(context.Background(), &testEmbeddedServer{cfg: config.App{PersistenceRoot: root}}, nil, childStore, updatedChild.Transition())
			if err != nil {
				t.Fatalf("resolve session action: %v", err)
			}
			if !resolved.ShouldContinue {
				t.Fatal("expected lifecycle to continue")
			}
			if resolved.NextSessionID != parentStore.Meta().SessionID {
				t.Fatalf("expected parent session target, got %q", resolved.NextSessionID)
			}

			reopenedParent, err := session.Open(parentStore.Dir())
			if err != nil {
				t.Fatalf("reopen parent store: %v", err)
			}
			parentEngine, err := runtime.New(reopenedParent, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
			if err != nil {
				t.Fatalf("new parent engine: %v", err)
			}
			parentModel := newProjectedEngineUIModel(
				parentEngine,
				WithUIInitialInput(sessionLaunchInitialInput(reopenedParent, resolved.InitialInput)),
			)

			if parentModel.input != tt.want {
				t.Fatalf("expected parent draft %q, got %q", tt.want, parentModel.input)
			}
			if parentModel.busy {
				t.Fatal("did not expect parent draft to auto-submit")
			}

			next, _ = parentModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
			editable := next.(*uiModel)
			if editable.input != tt.want+"x" {
				t.Fatalf("expected editable parent draft, got %q", editable.input)
			}
		})
	}
}

func TestForkRollbackNativeStartupReplayUsesForkedHistory(t *testing.T) {
	root := t.TempDir()
	store, err := session.Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleUser, Content: "u1"}); err != nil {
		t.Fatalf("append u1: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleAssistant, Content: "a1"}); err != nil {
		t.Fatalf("append a1: %v", err)
	}
	if _, err := store.AppendEvent("s2", "message", llm.Message{Role: llm.RoleUser, Content: "u2"}); err != nil {
		t.Fatalf("append u2: %v", err)
	}
	if _, err := store.AppendEvent("s2", "message", llm.Message{Role: llm.RoleAssistant, Content: "a2"}); err != nil {
		t.Fatalf("append a2: %v", err)
	}

	resolved, err := resolveSessionAction(
		context.Background(),
		&testEmbeddedServer{cfg: config.App{PersistenceRoot: root}},
		nil,
		store,
		UITransition{Action: UIActionForkRollback, InitialPrompt: "edited user message", ForkUserMessageIndex: 2},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !resolved.ShouldContinue {
		t.Fatal("expected lifecycle to continue for fork rollback action")
	}

	forkedStore, err := session.Open(filepath.Join(root, resolved.NextSessionID))
	if err != nil {
		t.Fatalf("open fork session store: %v", err)
	}
	eng, err := runtime.New(forkedStore, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new runtime for fork: %v", err)
	}

	m := newProjectedEngineUIModel(eng)
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected native startup replay command for fork session")
	}
	flushMsg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
	plain := stripANSIAndTrimRight(flushMsg.Text)
	if !strings.Contains(plain, "u1") || !strings.Contains(plain, "a1") {
		t.Fatalf("expected startup replay to include fork base history, got %q", plain)
	}
	if strings.Contains(plain, "u2") || strings.Contains(plain, "a2") {
		t.Fatalf("expected startup replay to exclude trimmed history after fork point, got %q", plain)
	}
	if len(updated.transcriptEntries) != 2 {
		t.Fatalf("expected forked transcript to include only two committed entries, got %d", len(updated.transcriptEntries))
	}
}
