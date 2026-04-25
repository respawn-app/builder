package runtime

import (
	"builder/server/llm"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/toolspec"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSystemPromptSnapshotUsesStoredWorkspaceRootWhenTranscriptWorkdirIsNested(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	nested := filepath.Join(workspace, "pkg")
	t.Setenv("HOME", home)
	systemDir := filepath.Join(workspace, agentsGlobalDirName)
	if err := os.MkdirAll(systemDir, 0o755); err != nil {
		t.Fatalf("mkdir system dir: %v", err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}
	writeTestFile(t, filepath.Join(systemDir, systemPromptFileName), "workspace root system")

	store, err := session.Create(t.TempDir(), "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                "gpt-5",
		EnabledTools:         []toolspec.ID{toolspec.ToolExecCommand},
		TranscriptWorkingDir: nested,
		ToolPreambles:        false,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "hello"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if got := client.calls[0].SystemPrompt; got != "workspace root system" {
		t.Fatalf("system prompt = %q, want workspace root system", got)
	}
}

func TestSystemPromptSnapshotFallsBackWhenHomeDirUnavailable(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("HOME", "")
	if err := os.MkdirAll(filepath.Join(workspace, agentsGlobalDirName), 0o755); err != nil {
		t.Fatalf("mkdir system dir: %v", err)
	}
	writeTestFile(t, filepath.Join(workspace, agentsGlobalDirName, systemPromptFileName), "local without home")

	template, sourcePath, ok, err := readSystemPromptTemplate(systemPromptSnapshotOptions{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("read system prompt template: %v", err)
	}
	if !ok || template != "local without home" {
		t.Fatalf("template = %q ok=%t, want local without home true", template, ok)
	}
	if want := filepath.Join(workspace, agentsGlobalDirName, systemPromptFileName); sourcePath != want {
		t.Fatalf("source path = %q, want %q", sourcePath, want)
	}
	template, sourcePath, ok, err = readSystemPromptTemplate(systemPromptSnapshotOptions{})
	if err != nil {
		t.Fatalf("read system prompt template without local prompt: %v", err)
	}
	if ok || template != "" || sourcePath != "" {
		t.Fatalf("template = %q sourcePath=%q ok=%t, want empty fallback", template, sourcePath, ok)
	}
}

func TestEnsureLockedWithSystemPromptAndTranscriptWorkingDirDoesNotDeadlock(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	systemDir := filepath.Join(workspace, agentsGlobalDirName)
	if err := os.MkdirAll(systemDir, 0o755); err != nil {
		t.Fatalf("mkdir system dir: %v", err)
	}
	writeTestFile(t, filepath.Join(systemDir, systemPromptFileName), "deadlock guard")

	store, err := session.Create(t.TempDir(), "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                "gpt-5",
		EnabledTools:         []toolspec.ID{toolspec.ToolExecCommand},
		TranscriptWorkingDir: workspace,
		ToolPreambles:        false,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	done := make(chan struct {
		locked session.LockedContract
		err    error
	}, 1)
	go func() {
		locked, err := eng.ensureLocked()
		done <- struct {
			locked session.LockedContract
			err    error
		}{locked: locked, err: err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("ensureLocked: %v", got.err)
		}
		if got.locked.SystemPrompt != "deadlock guard" {
			t.Fatalf("system prompt = %q, want deadlock guard", got.locked.SystemPrompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ensureLocked deadlocked while resolving SYSTEM.md from TranscriptWorkingDir")
	}
}

func TestBuildSystemPromptSnapshotForRootDoesNotUseMutexTakingWorkspaceAccessor(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	systemDir := filepath.Join(workspace, agentsGlobalDirName)
	if err := os.MkdirAll(systemDir, 0o755); err != nil {
		t.Fatalf("mkdir system dir: %v", err)
	}
	writeTestFile(t, filepath.Join(systemDir, systemPromptFileName), "locked helper guard")

	store, err := session.Create(t.TempDir(), "ws", t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5",
		EnabledTools:  []toolspec.ID{toolspec.ToolExecCommand},
		ToolPreambles: false,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	done := make(chan struct {
		prompt string
		err    error
	}, 1)
	eng.mu.Lock()
	go func() {
		prompt, err := eng.buildSystemPromptSnapshotForRoot(session.LockedContract{
			Model:          "gpt-5",
			Temperature:    1,
			ContextWindow:  272_000,
			ContextPercent: 95,
			ToolPreambles: func() *bool {
				enabled := false
				return &enabled
			}(),
		}, workspace)
		done <- struct {
			prompt string
			err    error
		}{prompt: prompt, err: err}
	}()
	select {
	case got := <-done:
		eng.mu.Unlock()
		if got.err != nil {
			t.Fatalf("buildSystemPromptSnapshotForRoot: %v", got.err)
		}
		if got.prompt != "locked helper guard" {
			t.Fatalf("prompt = %q, want locked helper guard", got.prompt)
		}
	case <-time.After(2 * time.Second):
		eng.mu.Unlock()
		t.Fatal("buildSystemPromptSnapshotForRoot called a mutex-taking workspace accessor")
	}
}

func TestSystemPromptSnapshotUsesTranscriptWorkingDirForRetargetedSession(t *testing.T) {
	home := t.TempDir()
	canonical := t.TempDir()
	worktree := t.TempDir()
	t.Setenv("HOME", home)
	for _, dir := range []string{filepath.Join(canonical, agentsGlobalDirName), filepath.Join(worktree, agentsGlobalDirName)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	writeTestFile(t, filepath.Join(canonical, agentsGlobalDirName, systemPromptFileName), "canonical system")
	writeTestFile(t, filepath.Join(worktree, agentsGlobalDirName, systemPromptFileName), "worktree system")

	store, err := session.Create(t.TempDir(), "ws", canonical)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                "gpt-5",
		EnabledTools:         []toolspec.ID{toolspec.ToolExecCommand},
		TranscriptWorkingDir: canonical,
		ToolPreambles:        false,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.SetTranscriptWorkingDir(worktree)
	if _, err := eng.SubmitUserMessage(context.Background(), "hello"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if got := client.calls[0].SystemPrompt; got != "worktree system" {
		t.Fatalf("system prompt = %q, want worktree system", got)
	}
}

func TestLegacyLockedSessionBackfillsSystemPromptSnapshotOnce(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	systemDir := filepath.Join(workspace, agentsGlobalDirName)
	if err := os.MkdirAll(systemDir, 0o755); err != nil {
		t.Fatalf("mkdir system dir: %v", err)
	}
	systemPath := filepath.Join(systemDir, systemPromptFileName)
	writeTestFile(t, systemPath, "stale legacy {{.EstimatedToolCallsForContext}}")

	store, err := session.Create(t.TempDir(), "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.MarkModelDispatchLocked(session.LockedContract{
		Model:          "gpt-5",
		Temperature:    1,
		MaxOutputToken: 0,
		ContextWindow:  272_000,
		ContextPercent: 95,
		ToolPreambles: func() *bool {
			enabled := false
			return &enabled
		}(),
	}); err != nil {
		t.Fatalf("mark locked: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                "gpt-5",
		EnabledTools:         []toolspec.ID{toolspec.ToolExecCommand},
		TranscriptWorkingDir: workspace,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if snapshot := store.Meta().Locked.SystemPrompt; snapshot != "" {
		t.Fatalf("system prompt snapshot before first dispatch = %q, want empty", snapshot)
	}
	writeTestFile(t, systemPath, "legacy {{.EstimatedToolCallsForContext}}")
	if _, err := eng.SubmitUserMessage(context.Background(), "hello"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	snapshot := store.Meta().Locked.SystemPrompt
	if snapshot != "legacy 185" {
		t.Fatalf("system prompt snapshot = %q, want legacy 185", snapshot)
	}
	writeTestFile(t, systemPath, "changed legacy")
	if got := client.calls[0].SystemPrompt; got != snapshot {
		t.Fatalf("request used changed system prompt\ngot: %q\nwant: %q", got, snapshot)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "again"); err != nil {
		t.Fatalf("submit again: %v", err)
	}
	if got := client.calls[1].SystemPrompt; got != snapshot {
		t.Fatalf("second request used changed system prompt\ngot: %q\nwant: %q", got, snapshot)
	}
	if got := store.Meta().Locked.SystemPrompt; got != snapshot {
		t.Fatalf("stored system prompt changed\ngot: %q\nwant: %q", got, snapshot)
	}
}

func TestEmptySystemPromptSnapshotIsReused(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	systemDir := filepath.Join(workspace, agentsGlobalDirName)
	if err := os.MkdirAll(systemDir, 0o755); err != nil {
		t.Fatalf("mkdir system dir: %v", err)
	}
	systemPath := filepath.Join(systemDir, systemPromptFileName)
	writeTestFile(t, systemPath, "   \n")

	store, err := session.Create(t.TempDir(), "ws", workspace)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "still ok"},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                "gpt-5",
		EnabledTools:         []toolspec.ID{toolspec.ToolExecCommand},
		TranscriptWorkingDir: workspace,
		ToolPreambles:        false,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "hello"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if got := client.calls[0].SystemPrompt; got != "" {
		t.Fatalf("first system prompt = %q, want empty", got)
	}
	if locked := store.Meta().Locked; locked == nil || !locked.HasSystemPrompt || locked.SystemPrompt != "" {
		t.Fatalf("locked system prompt snapshot = %+v, want empty marked snapshot", locked)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}
	writeTestFile(t, systemPath, "changed")
	reopened, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	if locked := reopened.Meta().Locked; locked == nil || !locked.HasSystemPrompt || locked.SystemPrompt != "" {
		t.Fatalf("reopened locked system prompt snapshot = %+v, want empty marked snapshot", locked)
	}
	reopenedClient := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "still ok"},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	reopenedEngine, err := New(reopened, reopenedClient, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:                "gpt-5",
		EnabledTools:         []toolspec.ID{toolspec.ToolExecCommand},
		TranscriptWorkingDir: workspace,
		ToolPreambles:        false,
	})
	if err != nil {
		t.Fatalf("new reopened engine: %v", err)
	}
	if _, err := reopenedEngine.SubmitUserMessage(context.Background(), "again"); err != nil {
		t.Fatalf("submit again: %v", err)
	}
	if got := reopenedClient.calls[0].SystemPrompt; got != "" {
		t.Fatalf("second system prompt = %q, want empty snapshot", got)
	}
	if locked := reopened.Meta().Locked; locked == nil || !locked.HasSystemPrompt || locked.SystemPrompt != "" {
		t.Fatalf("stored system prompt snapshot changed: %+v", locked)
	}
}

func TestLegacyLockedSessionBackfillsContextBudgetOnce(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.MarkModelDispatchLocked(session.LockedContract{
		Model:          "gpt-5",
		Temperature:    1,
		MaxOutputToken: 0,
	}); err != nil {
		t.Fatalf("mark locked: %v", err)
	}

	firstEngine, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:               "gpt-5",
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ContextWindowTokens: 272_000,
	})
	if err != nil {
		t.Fatalf("new first engine: %v", err)
	}
	locked := store.Meta().Locked
	if locked == nil || locked.ContextWindow != 272_000 || locked.ContextPercent != 95 {
		t.Fatalf("expected legacy lock backfilled from first resume config, got %+v", locked)
	}
	if got := firstEngine.estimatedToolCallsForLockedContext(*locked); got != 185 {
		t.Fatalf("first estimated tool calls = %d, want 185", got)
	}

	secondEngine, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:               "gpt-5",
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ContextWindowTokens: 400_000,
	})
	if err != nil {
		t.Fatalf("new second engine: %v", err)
	}
	locked = store.Meta().Locked
	if locked == nil || locked.ContextWindow != 272_000 || locked.ContextPercent != 95 {
		t.Fatalf("expected legacy lock backfill to stay pinned, got %+v", locked)
	}
	if got := secondEngine.estimatedToolCallsForLockedContext(*locked); got != 185 {
		t.Fatalf("second estimated tool calls = %d, want 185", got)
	}
}

func TestThinkingLevelCanChangeAfterLock(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{
		{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "one"}, Usage: llm.Usage{WindowTokens: 200000}},
		{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "two"}, Usage: llm.Usage{WindowTokens: 200000}},
	}}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5",
		Temperature:   1,
		ThinkingLevel: "xhigh",
		EnabledTools:  []toolspec.ID{toolspec.ToolExecCommand},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "hi"); err != nil {
		t.Fatalf("submit first: %v", err)
	}
	if err := eng.SetThinkingLevel("low"); err != nil {
		t.Fatalf("set thinking level: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "again"); err != nil {
		t.Fatalf("submit second: %v", err)
	}

	if len(client.calls) != 2 {
		t.Fatalf("client calls = %d, want 2", len(client.calls))
	}
	if client.calls[0].ReasoningEffort != "xhigh" {
		t.Fatalf("first reasoning effort = %q, want xhigh", client.calls[0].ReasoningEffort)
	}
	if client.calls[1].ReasoningEffort != "low" {
		t.Fatalf("second reasoning effort = %q, want low", client.calls[1].ReasoningEffort)
	}
}

func TestSetThinkingLevelRejectsInvalidValue(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5",
		ThinkingLevel: "high",
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.SetThinkingLevel("ultra"); err == nil {
		t.Fatal("expected invalid thinking level error")
	}
	if got := eng.ThinkingLevel(); got != "high" {
		t.Fatalf("thinking level after invalid set = %q, want high", got)
	}
}

func TestPoisonedLockedSessionFallsBackToModelReasoningSupport(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.MarkModelDispatchLocked(session.LockedContract{
		Model:          "gpt-5.4",
		Temperature:    1,
		MaxOutputToken: 0,
		ProviderContract: session.LockedProviderCapabilities{
			ProviderID:                 "chatgpt-codex",
			SupportsResponsesAPI:       true,
			SupportsResponsesCompact:   true,
			SupportsNativeWebSearch:    true,
			SupportsReasoningEncrypted: true,
			IsOpenAIFirstParty:         true,
		},
	}); err != nil {
		t.Fatalf("mark locked: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok"}}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5.4",
		ThinkingLevel: "high",
		EnabledTools:  []toolspec.ID{toolspec.ToolExecCommand},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "hi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("client calls = %d, want 1", len(client.calls))
	}
	if client.calls[0].ReasoningEffort != "high" {
		t.Fatalf("reasoning effort = %q, want high", client.calls[0].ReasoningEffort)
	}
	if !client.calls[0].SupportsReasoningEffort {
		t.Fatal("expected request to preserve reasoning support fallback for poisoned locked session")
	}
}

func TestFastModeCanChangeAfterLock(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{
		responses: []llm.Response{
			{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "one"}, Usage: llm.Usage{WindowTokens: 200000}},
			{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "two"}, Usage: llm.Usage{WindowTokens: 200000}},
		},
		caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true},
	}

	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5.3-codex",
		Temperature:   1,
		ThinkingLevel: "high",
		EnabledTools:  []toolspec.ID{toolspec.ToolExecCommand},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "hi"); err != nil {
		t.Fatalf("submit first: %v", err)
	}
	changed, err := eng.SetFastModeEnabled(true)
	if err != nil {
		t.Fatalf("set fast mode: %v", err)
	}
	if !changed {
		t.Fatal("expected fast mode change")
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "again"); err != nil {
		t.Fatalf("submit second: %v", err)
	}

	if len(client.calls) != 2 {
		t.Fatalf("client calls = %d, want 2", len(client.calls))
	}
	if client.calls[0].FastMode {
		t.Fatal("did not expect first request to enable fast mode")
	}
	if !client.calls[1].FastMode {
		t.Fatal("expected second request to enable fast mode")
	}
}

func TestSetFastModeRejectsUnsupportedProvider(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "azure-openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: false}}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model: "gpt-5.3-codex",
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	changed, err := eng.SetFastModeEnabled(true)
	if err == nil {
		t.Fatal("expected fast mode unsupported error")
	}
	if changed {
		t.Fatal("did not expect changed=true for unsupported fast mode")
	}
	if eng.FastModeEnabled() {
		t.Fatal("did not expect fast mode enabled after failure")
	}
}

func TestSetFastModeTogglesRuntimeOnly(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	cfg := Config{Model: "gpt-5.3-codex"}
	eng, err := New(store, &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	changed, err := eng.SetFastModeEnabled(true)
	if err != nil {
		t.Fatalf("enable fast mode: %v", err)
	}
	if !changed || !eng.FastModeEnabled() {
		t.Fatalf("expected fast mode enabled, changed=%v enabled=%v", changed, eng.FastModeEnabled())
	}

	restarted, err := New(store, &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), cfg)
	if err != nil {
		t.Fatalf("new restarted engine: %v", err)
	}
	if restarted.FastModeEnabled() {
		t.Fatal("expected fast mode disabled after restart")
	}
}

func TestFastModeSharedStateAppliesAcrossEngines(t *testing.T) {
	dir := t.TempDir()
	state := NewFastModeState(false)
	storeA, err := session.Create(dir, "ws-a", dir)
	if err != nil {
		t.Fatalf("create store A: %v", err)
	}
	engA, err := New(storeA, &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5.3-codex",
		FastModeState: state,
	})
	if err != nil {
		t.Fatalf("new engine A: %v", err)
	}

	changed, err := engA.SetFastModeEnabled(true)
	if err != nil {
		t.Fatalf("enable fast mode: %v", err)
	}
	if !changed || !state.Enabled() {
		t.Fatalf("expected shared fast mode enabled, changed=%v enabled=%v", changed, state.Enabled())
	}

	storeB, err := session.Create(dir, "ws-b", dir)
	if err != nil {
		t.Fatalf("create store B: %v", err)
	}
	engB, err := New(storeB, &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model:         "gpt-5.3-codex",
		FastModeState: state,
	})
	if err != nil {
		t.Fatalf("new engine B: %v", err)
	}
	if !engB.FastModeEnabled() {
		t.Fatal("expected shared fast mode to carry into next engine")
	}
}

func TestSetAutoCompactionEnabledTogglesRuntimeOnly(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	cfg := Config{Model: "gpt-5"}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	changed, enabled := eng.SetAutoCompactionEnabled(false)
	if !changed || enabled {
		t.Fatalf("expected changed=true enabled=false, got changed=%v enabled=%v", changed, enabled)
	}
	if got := eng.AutoCompactionEnabled(); got {
		t.Fatalf("expected runtime auto-compaction disabled, got %v", got)
	}

	restarted, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), cfg)
	if err != nil {
		t.Fatalf("new restarted engine: %v", err)
	}
	if got := restarted.AutoCompactionEnabled(); !got {
		t.Fatalf("expected auto-compaction enabled after restart, got %v", got)
	}
}

func TestSetAutoCompactionDisabledConcurrentWithBusyStepSkipsCompactionForCurrentRun(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeCompactionClient{
		responses: []llm.Response{
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
				ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(toolspec.ToolExecCommand), Input: json.RawMessage(`{"command":"pwd"}`)}},
				Usage:     llm.Usage{InputTokens: 390000, OutputTokens: 1000, WindowTokens: 400000},
			},
			{
				Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
				Usage:     llm.Usage{WindowTokens: 400000},
			},
		},
		compactionResponses: []llm.CompactionResponse{
			{
				OutputItems: []llm.ResponseItem{
					{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "run tools"},
					{Type: llm.ResponseItemTypeCompaction, ID: "cmp_1", EncryptedContent: "enc_1"},
				},
				Usage: llm.Usage{InputTokens: 8000, OutputTokens: 500, WindowTokens: 400000},
			},
		},
	}

	started := make(chan struct{})
	release := make(chan struct{})
	eng, err := New(store, client, tools.NewRegistry(blockingTool{name: toolspec.ToolExecCommand, started: started, release: release}), Config{
		Model:                 "gpt-5",
		AutoCompactTokenLimit: 350000,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	submitDone := make(chan error, 1)
	go func() {
		_, submitErr := eng.SubmitUserMessage(context.Background(), "run tools")
		submitDone <- submitErr
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for tool call to start")
	}
	changed, enabled := eng.SetAutoCompactionEnabled(false)
	if !changed || enabled {
		t.Fatalf("expected changed=true enabled=false, got changed=%v enabled=%v", changed, enabled)
	}
	close(release)

	if err := <-submitDone; err != nil {
		t.Fatalf("submit while disabling auto-compaction: %v", err)
	}
	if got := len(client.compactionCalls); got != 0 {
		t.Fatalf("expected no compaction call for in-flight run after disabling auto-compaction, got %d", got)
	}
}

func TestSetReviewerEnabledTogglesRuntimeOnly(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	cfg := Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "off",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        &fakeClient{},
		},
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	changed, mode, err := eng.SetReviewerEnabled(true)
	if err != nil {
		t.Fatalf("enable reviewer: %v", err)
	}
	if !changed || mode != "edits" {
		t.Fatalf("expected changed=true mode=edits, got changed=%v mode=%q", changed, mode)
	}
	if got := eng.ReviewerFrequency(); got != "edits" {
		t.Fatalf("reviewer frequency = %q, want edits", got)
	}

	restarted, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), cfg)
	if err != nil {
		t.Fatalf("new restarted engine: %v", err)
	}
	if got := restarted.ReviewerFrequency(); got != "off" {
		t.Fatalf("reviewer frequency after restart = %q, want off", got)
	}
}

func TestSetReviewerEnabledFailsWhenReviewerClientMissing(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "off",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        nil,
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	changed, mode, err := eng.SetReviewerEnabled(true)
	if err == nil {
		t.Fatal("expected enable reviewer error when reviewer client is missing")
	}
	if changed {
		t.Fatal("did not expect changed=true when reviewer client is missing")
	}
	if mode != "off" {
		t.Fatalf("expected mode off on failure, got %q", mode)
	}
}

func TestSetReviewerEnabledLazyInitializesReviewerClient(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: toolspec.ToolExecCommand}), Config{
		Model: "gpt-5",
		Reviewer: ReviewerConfig{
			Frequency:     "off",
			Model:         "gpt-5",
			ThinkingLevel: "low",
			Client:        nil,
			ClientFactory: func() (llm.Client, error) {
				return &fakeClient{}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	changed, mode, err := eng.SetReviewerEnabled(true)
	if err != nil {
		t.Fatalf("enable reviewer with lazy client init: %v", err)
	}
	if !changed || mode != "edits" {
		t.Fatalf("expected changed=true mode=edits, got changed=%v mode=%q", changed, mode)
	}
}
