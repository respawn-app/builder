package app

import (
	"builder/internal/config"
	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/session"
	"builder/internal/tools"
	shelltool "builder/internal/tools/shell"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestResolveSessionActionResumeReopensPicker(t *testing.T) {
	nextSessionID, initialPrompt, parentSessionID, forceNewSession, shouldContinue, err := resolveSessionAction(
		context.Background(),
		appBootstrap{},
		nil,
		UITransition{Action: UIActionResume},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !shouldContinue {
		t.Fatal("expected lifecycle to continue for resume action")
	}
	if nextSessionID != "" {
		t.Fatalf("expected empty session id to force picker, got %q", nextSessionID)
	}
	if forceNewSession {
		t.Fatal("did not expect force-new for resume action")
	}
	if parentSessionID != "" {
		t.Fatalf("expected no parent session id on resume, got %q", parentSessionID)
	}
	if initialPrompt != "" {
		t.Fatalf("expected no initial prompt on resume, got %q", initialPrompt)
	}
}

func TestResolveSessionActionNewSessionUsesForceNewFlow(t *testing.T) {
	nextSessionID, initialPrompt, parentSessionID, forceNewSession, shouldContinue, err := resolveSessionAction(
		context.Background(),
		appBootstrap{},
		nil,
		UITransition{Action: UIActionNewSession, InitialPrompt: "hello", ParentSessionID: "parent-1"},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !shouldContinue {
		t.Fatal("expected lifecycle to continue for new session action")
	}
	if !forceNewSession {
		t.Fatal("expected force-new session flow")
	}
	if nextSessionID != "" {
		t.Fatalf("expected empty session id for force-new flow, got %q", nextSessionID)
	}
	if parentSessionID != "parent-1" {
		t.Fatalf("expected parent session id passthrough, got %q", parentSessionID)
	}
	if initialPrompt != "hello" {
		t.Fatalf("expected initial prompt passthrough, got %q", initialPrompt)
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
	nextSessionID, initialPrompt, parentSessionID, forceNewSession, shouldContinue, err := resolveSessionAction(
		context.Background(),
		appBootstrap{background: manager},
		nil,
		UITransition{Action: UIActionNewSession, InitialPrompt: "hello", ParentSessionID: "parent-1"},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !shouldContinue || !forceNewSession {
		t.Fatalf("expected new-session transition, shouldContinue=%t forceNew=%t", shouldContinue, forceNewSession)
	}
	if nextSessionID != "" || initialPrompt != "hello" {
		t.Fatalf("unexpected transition payload nextSessionID=%q initialPrompt=%q", nextSessionID, initialPrompt)
	}

	wiring := &runtimeWiring{background: manager}
	if err := wiring.Close(); err != nil {
		t.Fatalf("close wiring: %v", err)
	}

	store, err := openOrCreateSession(root, root, nextSessionID, workdir, "dark", config.TUIAlternateScreenAuto, forceNewSession, parentSessionID)
	if err != nil {
		t.Fatalf("open or create next session: %v", err)
	}
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

	nextSessionID, initialPrompt, parentSessionID, forceNewSession, shouldContinue, err := resolveSessionAction(
		context.Background(),
		appBootstrap{},
		store,
		UITransition{Action: UIActionForkRollback, InitialPrompt: "edited user message", ForkUserMessageIndex: 1},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !shouldContinue {
		t.Fatal("expected lifecycle to continue for fork rollback action")
	}
	if forceNewSession {
		t.Fatal("did not expect force-new for fork rollback action")
	}
	if parentSessionID != "" {
		t.Fatalf("expected no deferred parent for pre-created fork session, got %q", parentSessionID)
	}
	if nextSessionID == "" {
		t.Fatal("expected target fork session id")
	}
	if nextSessionID == store.Meta().SessionID {
		t.Fatalf("expected fork session id to differ from parent, got %q", nextSessionID)
	}
	if initialPrompt != "edited user message" {
		t.Fatalf("expected initial prompt passthrough, got %q", initialPrompt)
	}
}

func TestResolveSessionActionOpenSessionUsesTargetID(t *testing.T) {
	nextSessionID, initialPrompt, parentSessionID, forceNewSession, shouldContinue, err := resolveSessionAction(
		context.Background(),
		appBootstrap{},
		nil,
		UITransition{Action: UIActionOpenSession, TargetSessionID: "session-42"},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !shouldContinue {
		t.Fatal("expected lifecycle to continue for open session action")
	}
	if nextSessionID != "session-42" {
		t.Fatalf("expected target session id passthrough, got %q", nextSessionID)
	}
	if initialPrompt != "" {
		t.Fatalf("expected no initial prompt, got %q", initialPrompt)
	}
	if parentSessionID != "" {
		t.Fatalf("expected no parent session id, got %q", parentSessionID)
	}
	if forceNewSession {
		t.Fatal("did not expect force-new session")
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

	nextSessionID, _, _, _, shouldContinue, err := resolveSessionAction(
		context.Background(),
		appBootstrap{},
		store,
		UITransition{Action: UIActionForkRollback, InitialPrompt: "edited user message", ForkUserMessageIndex: 2},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !shouldContinue {
		t.Fatal("expected lifecycle to continue for fork rollback action")
	}

	forkedStore, err := session.Open(filepath.Join(root, nextSessionID))
	if err != nil {
		t.Fatalf("open fork session store: %v", err)
	}
	eng, err := runtime.New(forkedStore, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new runtime for fork: %v", err)
	}

	m := NewUIModel(
		eng,
		make(chan runtime.Event),
		make(chan askEvent),
	).(*uiModel)
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
