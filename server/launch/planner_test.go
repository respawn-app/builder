package launch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"builder/server/auth"
	"builder/server/session"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/serverapi"
	"builder/shared/toolspec"
)

func TestPlannerHeadlessCreatesNewSessionAndAppliesContinuationContext(t *testing.T) {
	root := t.TempDir()
	containerDir := filepath.Join(root, "sessions", "workspace-a")
	planner := Planner{
		Config: config.App{
			WorkspaceRoot:   "/tmp/workspace-a",
			PersistenceRoot: root,
			Settings: config.Settings{
				OpenAIBaseURL: "http://headless.local/v1",
			},
		},
		ContainerDir: containerDir,
	}

	plan, err := planner.PlanSession(SessionRequest{Mode: ModeHeadless})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	meta := plan.Store.Meta()
	if meta.SessionID == "" {
		t.Fatal("expected session id")
	}
	if !strings.HasSuffix(meta.Name, " "+SubagentSessionSuffix) {
		t.Fatalf("expected subagent session name, got %q", meta.Name)
	}
	if meta.Continuation == nil || meta.Continuation.OpenAIBaseURL != "http://headless.local/v1" {
		t.Fatalf("expected continuation base url applied, got %+v", meta.Continuation)
	}
	if plan.SessionName != meta.Name {
		t.Fatalf("expected plan session name %q, got %q", meta.Name, plan.SessionName)
	}
	if plan.WorkspaceRoot != "/tmp/workspace-a" {
		t.Fatalf("expected workspace root passthrough, got %q", plan.WorkspaceRoot)
	}
}

func TestPlannerInteractiveUsesPickerSelection(t *testing.T) {
	root := t.TempDir()
	containerDir := filepath.Join(root, "sessions", "workspace-a")
	first, err := session.Create(containerDir, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create first session: %v", err)
	}
	if err := first.SetName("first"); err != nil {
		t.Fatalf("persist first session meta: %v", err)
	}
	second, err := session.Create(containerDir, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	if err := second.SetName("second"); err != nil {
		t.Fatalf("persist second session meta: %v", err)
	}
	planner := Planner{
		Config: config.App{
			WorkspaceRoot:   "/tmp/workspace-a",
			PersistenceRoot: root,
			Settings:        config.Settings{},
		},
		ContainerDir: containerDir,
		PickSession: func(summaries []session.Summary) (SessionSelection, error) {
			if len(summaries) != 2 {
				t.Fatalf("expected two summaries, got %d", len(summaries))
			}
			for _, summary := range summaries {
				if summary.SessionID == second.Meta().SessionID {
					picked := summary
					return SessionSelection{Session: &picked}, nil
				}
			}
			t.Fatalf("expected picker summaries to include %q", second.Meta().SessionID)
			return SessionSelection{}, nil
		},
	}

	plan, err := planner.PlanSession(SessionRequest{Mode: ModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	if plan.Store.Meta().SessionID != second.Meta().SessionID {
		t.Fatalf("expected selected session %q, got %q", second.Meta().SessionID, plan.Store.Meta().SessionID)
	}
	if plan.Store.Meta().SessionID == first.Meta().SessionID {
		t.Fatalf("did not expect first session %q", first.Meta().SessionID)
	}
}

func TestPlannerInteractiveUsesProjectViewSessionsAndReopensBySessionID(t *testing.T) {
	root := t.TempDir()
	containerDir := filepath.Join(root, "sessions", "workspace-a")
	first, err := session.Create(containerDir, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create first session: %v", err)
	}
	if err := first.SetName("first"); err != nil {
		t.Fatalf("persist first session meta: %v", err)
	}
	second, err := session.Create(containerDir, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	if err := second.SetName("second"); err != nil {
		t.Fatalf("persist second session meta: %v", err)
	}
	projectViews := &stubLaunchProjectViewService{overview: serverapi.ProjectGetOverviewResponse{Overview: clientui.ProjectOverview{
		Project: clientui.ProjectSummary{ProjectID: "project-1", DisplayName: "workspace-a", RootPath: "/tmp/workspace-a"},
		Sessions: []clientui.SessionSummary{
			{SessionID: first.Meta().SessionID, Name: "first", UpdatedAt: first.Meta().UpdatedAt},
			{SessionID: second.Meta().SessionID, Name: "second", UpdatedAt: second.Meta().UpdatedAt},
		},
	}}}
	planner := Planner{
		Config: config.App{
			WorkspaceRoot:   "/tmp/workspace-a",
			PersistenceRoot: root,
			Settings:        config.Settings{},
		},
		ContainerDir: containerDir,
		ProjectID:    "project-1",
		ProjectViews: client.NewLoopbackProjectViewClient(projectViews),
		PickSession: func(summaries []session.Summary) (SessionSelection, error) {
			if len(summaries) != 2 {
				t.Fatalf("expected two summaries, got %d", len(summaries))
			}
			picked := summaries[1]
			return SessionSelection{Session: &picked}, nil
		},
	}

	plan, err := planner.PlanSession(SessionRequest{Mode: ModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	if plan.Store.Meta().SessionID != second.Meta().SessionID {
		t.Fatalf("expected selected session %q, got %q", second.Meta().SessionID, plan.Store.Meta().SessionID)
	}
	if projectViews.overviewCalls != 1 {
		t.Fatalf("expected project overview to be used once, got %d", projectViews.overviewCalls)
	}
}

func TestApplyRunPromptOverridesOverridesHeadlessSettingsWithoutMutatingBasePlan(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	containerDir := filepath.Join(root, "sessions", "workspace-a")
	store, err := session.Create(containerDir, "workspace-a", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	plan := SessionPlan{
		Store: store,
		ActiveSettings: config.Settings{
			Model:         "base-model",
			ThinkingLevel: "low",
			Theme:         "dark",
			EnabledTools: map[toolspec.ID]bool{
				toolspec.ToolExecCommand: true,
			},
			Timeouts: config.Timeouts{ModelRequestSeconds: 100},
		},
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: "base-model",
		WorkspaceRoot:       workspace,
	}

	updated, warnings, err := ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{
		Model:               "gpt-5-mini",
		ThinkingLevel:       "medium",
		Theme:               "light",
		ModelTimeoutSeconds: 12,
		Tools:               "shell,patch",
		OpenAIBaseURL:       "http://override.local/v1",
	}, auth.EmptyState())
	if err != nil {
		t.Fatalf("ApplyRunPromptOverrides: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if updated.ActiveSettings.Model != "gpt-5-mini" {
		t.Fatalf("model = %q, want gpt-5-mini", updated.ActiveSettings.Model)
	}
	if updated.ConfiguredModelName != "gpt-5-mini" {
		t.Fatalf("configured model = %q, want gpt-5-mini", updated.ConfiguredModelName)
	}
	if updated.ActiveSettings.ThinkingLevel != "medium" {
		t.Fatalf("thinking level = %q, want medium", updated.ActiveSettings.ThinkingLevel)
	}
	if updated.ActiveSettings.Theme != "light" {
		t.Fatalf("theme = %q, want light", updated.ActiveSettings.Theme)
	}
	if updated.ActiveSettings.Timeouts.ModelRequestSeconds != 12 {
		t.Fatalf("timeouts = %+v, want model_request_seconds=12", updated.ActiveSettings.Timeouts)
	}
	if len(updated.EnabledTools) != 2 || updated.EnabledTools[0] != toolspec.ToolExecCommand || updated.EnabledTools[1] != toolspec.ToolPatch {
		t.Fatalf("enabled tools = %+v, want patch+shell", updated.EnabledTools)
	}
	if updated.ActiveSettings.OpenAIBaseURL != "http://override.local/v1" {
		t.Fatalf("openai base url = %q, want http://override.local/v1", updated.ActiveSettings.OpenAIBaseURL)
	}
	if got := updated.Store.Meta().Continuation; got == nil || got.OpenAIBaseURL != "http://override.local/v1" {
		t.Fatalf("continuation = %+v, want override url", got)
	}
	if plan.ActiveSettings.Model != "base-model" {
		t.Fatalf("base plan mutated: %+v", plan.ActiveSettings)
	}
}

func TestApplyRunPromptOverridesRejectsInvalidAgentRole(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	containerDir := filepath.Join(root, "sessions", "workspace-a")
	store, err := session.Create(containerDir, "workspace-a", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	plan := SessionPlan{
		Store:               store,
		ActiveSettings:      config.Settings{Model: "gpt-5.4"},
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: "gpt-5.4",
		WorkspaceRoot:       workspace,
	}

	_, _, err = ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{AgentRole: "fast!"}, auth.EmptyState())
	if err == nil {
		t.Fatal("expected invalid agent role to fail")
	}
	if !strings.Contains(err.Error(), "invalid agent role") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyRunPromptOverridesRecomputesEnabledToolsForModelOverride(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	containerDir := filepath.Join(root, "sessions", "workspace-a")
	store, err := session.Create(containerDir, "workspace-a", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	plan := SessionPlan{
		Store: store,
		ActiveSettings: config.Settings{
			Model: "gpt-5.4",
			EnabledTools: map[toolspec.ID]bool{
				toolspec.ToolExecCommand: true,
			},
		},
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: "gpt-5.4",
		WorkspaceRoot:       workspace,
	}

	updated, warnings, err := ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{Model: "gpt-5.3-codex"}, auth.EmptyState())
	if err != nil {
		t.Fatalf("ApplyRunPromptOverrides: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if updated.ActiveSettings.Model != "gpt-5.3-codex" {
		t.Fatalf("model = %q, want gpt-5.3-codex", updated.ActiveSettings.Model)
	}
	if len(updated.EnabledTools) != 1 || updated.EnabledTools[0] != toolspec.ToolExecCommand {
		t.Fatalf("enabled tools = %+v, want shell only", updated.EnabledTools)
	}
}

func TestApplyRunPromptOverridesKeepsExplicitToolSourcesWhenOnlyModelOverrides(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	containerDir := filepath.Join(root, "sessions", "workspace-a")
	store, err := session.Create(containerDir, "workspace-a", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	plan := SessionPlan{
		Store: store,
		ActiveSettings: config.Settings{
			Model: "gpt-5.4",
			EnabledTools: map[toolspec.ID]bool{
				toolspec.ToolExecCommand: true,
			},
		},
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: "gpt-5.4",
		WorkspaceRoot:       workspace,
		Source: config.SourceReport{Sources: map[string]string{
			"model":       "file",
			"tools.shell": "cli",
		}},
	}

	updated, warnings, err := ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{Model: "gpt-5.3-codex"}, auth.EmptyState())
	if err != nil {
		t.Fatalf("ApplyRunPromptOverrides: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if updated.ActiveSettings.Model != "gpt-5.3-codex" {
		t.Fatalf("model = %q, want gpt-5.3-codex", updated.ActiveSettings.Model)
	}
	if len(updated.EnabledTools) != 1 || updated.EnabledTools[0] != toolspec.ToolExecCommand {
		t.Fatalf("enabled tools = %+v, want shell only", updated.EnabledTools)
	}
	if updated.Source.Sources["tools.shell"] != "cli" {
		t.Fatalf("tool source = %q, want cli", updated.Source.Sources["tools.shell"])
	}
}

func TestApplyRunPromptOverridesFastRoleWarnsWhenHeuristicDoesNothing(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model = \"gpt-5.4\"\nopenai_base_url = \"https://example.test/v1\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := session.Create(filepath.Join(t.TempDir(), "sessions", "workspace-a"), "workspace-a", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	plan := SessionPlan{
		Store:               store,
		ActiveSettings:      loaded.Settings,
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: loaded.Settings.Model,
		WorkspaceRoot:       workspace,
		Source:              loaded.Source,
	}

	updated, warnings, err := ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{AgentRole: config.BuiltInSubagentRoleFast}, auth.EmptyState())
	if err != nil {
		t.Fatalf("ApplyRunPromptOverrides: %v", err)
	}
	if updated.ActiveSettings.Model != loaded.Settings.Model {
		t.Fatalf("model = %q, want %q", updated.ActiveSettings.Model, loaded.Settings.Model)
	}
	if len(warnings) != 1 || warnings[0] != fastRoleSameAsMainWarning {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
}

func TestApplyRunPromptOverridesFastRoleAppliesBuiltInHeuristics(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	loaded, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := session.Create(filepath.Join(t.TempDir(), "sessions", "workspace-a"), "workspace-a", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	plan := SessionPlan{
		Store:               store,
		ActiveSettings:      loaded.Settings,
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: loaded.Settings.Model,
		WorkspaceRoot:       workspace,
		Source:              loaded.Source,
	}

	updated, warnings, err := ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{AgentRole: config.BuiltInSubagentRoleFast}, auth.State{Method: auth.Method{Type: auth.MethodAPIKey, APIKey: &auth.APIKeyMethod{Key: "test-key"}}})
	if err != nil {
		t.Fatalf("ApplyRunPromptOverrides: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if updated.ActiveSettings.Model != "gpt-5.4-mini" {
		t.Fatalf("model = %q, want gpt-5.4-mini", updated.ActiveSettings.Model)
	}
	if !updated.ActiveSettings.PriorityRequestMode {
		t.Fatal("expected priority request mode enabled for fast role")
	}
	if updated.ActiveSettings.Reviewer.Model != "gpt-5.4-mini" {
		t.Fatalf("reviewer model = %q, want gpt-5.4-mini", updated.ActiveSettings.Reviewer.Model)
	}
	if updated.ActiveSettings.ModelContextWindow != 272_000 {
		t.Fatalf("context window = %d, want 272000", updated.ActiveSettings.ModelContextWindow)
	}
	if updated.ConfiguredModelName != "gpt-5.4-mini" {
		t.Fatalf("configured model = %q, want gpt-5.4-mini", updated.ConfiguredModelName)
	}
}

func TestApplyRunPromptOverridesFastRoleWarnsWhenExplicitRoleMatchesBase(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	contents := strings.Join([]string{
		"model = \"gpt-5.4\"",
		"thinking_level = \"medium\"",
		"",
		"[subagents.fast]",
		"model = \"gpt-5.4\"",
		"thinking_level = \"medium\"",
		"priority_request_mode = false",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := session.Create(filepath.Join(t.TempDir(), "sessions", "workspace-a"), "workspace-a", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	plan := SessionPlan{
		Store:               store,
		ActiveSettings:      loaded.Settings,
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: loaded.Settings.Model,
		WorkspaceRoot:       workspace,
		Source:              loaded.Source,
	}

	updated, warnings, err := ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{AgentRole: config.BuiltInSubagentRoleFast}, auth.State{Method: auth.Method{Type: auth.MethodAPIKey, APIKey: &auth.APIKeyMethod{Key: "test-key"}}})
	if err != nil {
		t.Fatalf("ApplyRunPromptOverrides: %v", err)
	}
	if updated.ActiveSettings.Model != loaded.Settings.Model || updated.ActiveSettings.ThinkingLevel != loaded.Settings.ThinkingLevel {
		t.Fatalf("expected explicit fast role to match base settings, got %+v vs %+v", updated.ActiveSettings, loaded.Settings)
	}
	if len(warnings) != 1 || warnings[0] != fastRoleSameAsMainWarning {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
}

func TestApplyRunPromptOverridesSubagentProviderOverrideCanInheritBaseModel(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	contents := strings.Join([]string{
		"model = \"my-team-alias\"",
		"",
		"[subagents.worker]",
		"provider_override = \"openai\"",
		"openai_base_url = \"https://api.openai.com/v1\"",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := session.Create(filepath.Join(t.TempDir(), "sessions", "workspace-a"), "workspace-a", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	plan := SessionPlan{
		Store:               store,
		ActiveSettings:      loaded.Settings,
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: loaded.Settings.Model,
		WorkspaceRoot:       workspace,
		Source:              loaded.Source,
	}

	updated, warnings, err := ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{AgentRole: "worker"}, auth.EmptyState())
	if err != nil {
		t.Fatalf("ApplyRunPromptOverrides: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if updated.ActiveSettings.Model != "my-team-alias" {
		t.Fatalf("model = %q, want my-team-alias", updated.ActiveSettings.Model)
	}
	if updated.ActiveSettings.ProviderOverride != "openai" {
		t.Fatalf("provider override = %q, want openai", updated.ActiveSettings.ProviderOverride)
	}
	if updated.ActiveSettings.OpenAIBaseURL != "https://api.openai.com/v1" {
		t.Fatalf("openai base url = %q, want https://api.openai.com/v1", updated.ActiveSettings.OpenAIBaseURL)
	}
}

func TestApplyRunPromptOverridesRoleModelOverrideRecomputesContextBudget(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	contents := strings.Join([]string{
		"model = \"gpt-5.4\"",
		"model_context_window = 272000",
		"context_compaction_threshold_tokens = 258400",
		"pre_submit_compaction_lead_tokens = 35000",
		"",
		"[subagents.fast]",
		"model = \"gpt-5.3-codex-spark\"",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := session.Create(filepath.Join(t.TempDir(), "sessions", "workspace-a"), "workspace-a", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	plan := SessionPlan{
		Store:               store,
		ActiveSettings:      loaded.Settings,
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: loaded.Settings.Model,
		WorkspaceRoot:       workspace,
		Source:              loaded.Source,
	}

	updated, warnings, err := ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{AgentRole: config.BuiltInSubagentRoleFast}, auth.EmptyState())
	if err != nil {
		t.Fatalf("ApplyRunPromptOverrides: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if updated.ActiveSettings.Model != "gpt-5.3-codex-spark" {
		t.Fatalf("model = %q, want gpt-5.3-codex-spark", updated.ActiveSettings.Model)
	}
	if updated.ActiveSettings.ModelContextWindow != 128_000 {
		t.Fatalf("context window = %d, want 128000", updated.ActiveSettings.ModelContextWindow)
	}
	if updated.ActiveSettings.ContextCompactionThresholdTokens != 121_600 {
		t.Fatalf("compaction threshold = %d, want 121600", updated.ActiveSettings.ContextCompactionThresholdTokens)
	}
	if updated.ActiveSettings.PreSubmitCompactionLeadTokens != 35_000 {
		t.Fatalf("pre-submit lead = %d, want 35000", updated.ActiveSettings.PreSubmitCompactionLeadTokens)
	}
}

func TestApplyRunPromptOverridesRoleModelOverrideKeepsExplicitContextWindow(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	contents := strings.Join([]string{
		"model = \"gpt-5.4\"",
		"model_context_window = 272000",
		"context_compaction_threshold_tokens = 258400",
		"pre_submit_compaction_lead_tokens = 35000",
		"",
		"[subagents.fast]",
		"model = \"gpt-5.3-codex-spark\"",
		"model_context_window = 100000",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := session.Create(filepath.Join(t.TempDir(), "sessions", "workspace-a"), "workspace-a", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	plan := SessionPlan{
		Store:               store,
		ActiveSettings:      loaded.Settings,
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: loaded.Settings.Model,
		WorkspaceRoot:       workspace,
		Source:              loaded.Source,
	}

	updated, warnings, err := ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{AgentRole: config.BuiltInSubagentRoleFast}, auth.EmptyState())
	if err != nil {
		t.Fatalf("ApplyRunPromptOverrides: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if updated.ActiveSettings.ModelContextWindow != 100_000 {
		t.Fatalf("context window = %d, want 100000", updated.ActiveSettings.ModelContextWindow)
	}
	if updated.ActiveSettings.ContextCompactionThresholdTokens != 95_000 {
		t.Fatalf("compaction threshold = %d, want 95000", updated.ActiveSettings.ContextCompactionThresholdTokens)
	}
	if updated.ActiveSettings.PreSubmitCompactionLeadTokens != 35_000 {
		t.Fatalf("pre-submit lead = %d, want 35000", updated.ActiveSettings.PreSubmitCompactionLeadTokens)
	}
}

func TestApplyRunPromptOverridesFastRoleUsesCLIProviderOverrideForHeuristic(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	contents := strings.Join([]string{
		"model = \"gpt-5.4\"",
		"openai_base_url = \"https://example.test/v1\"",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := session.Create(filepath.Join(t.TempDir(), "sessions", "workspace-a"), "workspace-a", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	plan := SessionPlan{
		Store:               store,
		ActiveSettings:      loaded.Settings,
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: loaded.Settings.Model,
		WorkspaceRoot:       workspace,
		Source:              loaded.Source,
	}

	updated, warnings, err := ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{
		AgentRole:        config.BuiltInSubagentRoleFast,
		ProviderOverride: "openai",
		OpenAIBaseURL:    "https://api.openai.com/v1",
	}, auth.State{Method: auth.Method{Type: auth.MethodAPIKey, APIKey: &auth.APIKeyMethod{Key: "test-key"}}})
	if err != nil {
		t.Fatalf("ApplyRunPromptOverrides: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if updated.ActiveSettings.Model != "gpt-5.4-mini" {
		t.Fatalf("model = %q, want gpt-5.4-mini", updated.ActiveSettings.Model)
	}
	if !updated.ActiveSettings.PriorityRequestMode {
		t.Fatal("expected priority request mode enabled")
	}
}

func TestApplyRunPromptOverridesFailedConfigOverrideDoesNotPersistContinuation(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	contents := strings.Join([]string{
		"model = \"gpt-5.4\"",
		"openai_base_url = \"https://base.example/v1\"",
		"",
		"[subagents.worker]",
		"provider_override = \"openai\"",
		"openai_base_url = \"https://worker.example/v1\"",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := session.Create(filepath.Join(t.TempDir(), "sessions", "workspace-a"), "workspace-a", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: loaded.Settings.OpenAIBaseURL}); err != nil {
		t.Fatalf("seed continuation: %v", err)
	}
	plan := SessionPlan{
		Store:               store,
		ActiveSettings:      loaded.Settings,
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: loaded.Settings.Model,
		WorkspaceRoot:       workspace,
		Source:              loaded.Source,
	}

	_, _, err = ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{
		AgentRole: "worker",
		Tools:     "not-a-tool",
	}, auth.EmptyState())
	if err == nil {
		t.Fatal("expected invalid tools override to fail")
	}
	got := store.Meta().Continuation
	if got == nil || got.OpenAIBaseURL != "https://base.example/v1" {
		t.Fatalf("continuation = %+v, want unchanged base url", got)
	}
}

func TestApplyRunPromptOverridesRoleOnlyOverridePersistsContinuation(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	contents := strings.Join([]string{
		"model = \"gpt-5.4\"",
		"openai_base_url = \"https://base.example/v1\"",
		"",
		"[subagents.worker]",
		"provider_override = \"openai\"",
		"openai_base_url = \"https://worker.example/v1\"",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := session.Create(filepath.Join(t.TempDir(), "sessions", "workspace-a"), "workspace-a", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: loaded.Settings.OpenAIBaseURL}); err != nil {
		t.Fatalf("seed continuation: %v", err)
	}
	plan := SessionPlan{
		Store:               store,
		ActiveSettings:      loaded.Settings,
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: loaded.Settings.Model,
		WorkspaceRoot:       workspace,
		Source:              loaded.Source,
	}

	updated, warnings, err := ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{AgentRole: "worker"}, auth.EmptyState())
	if err != nil {
		t.Fatalf("ApplyRunPromptOverrides: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if updated.ActiveSettings.OpenAIBaseURL != "https://worker.example/v1" {
		t.Fatalf("openai base url = %q, want worker override", updated.ActiveSettings.OpenAIBaseURL)
	}
	got := store.Meta().Continuation
	if got == nil || got.OpenAIBaseURL != "https://worker.example/v1" {
		t.Fatalf("continuation = %+v, want worker base url", got)
	}
}

func TestApplyRunPromptOverridesCLIModelOverrideRecomputesBudgetAfterFastRole(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	loaded, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := session.Create(filepath.Join(t.TempDir(), "sessions", "workspace-a"), "workspace-a", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	plan := SessionPlan{
		Store:               store,
		ActiveSettings:      loaded.Settings,
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: loaded.Settings.Model,
		WorkspaceRoot:       workspace,
		Source:              loaded.Source,
	}

	updated, warnings, err := ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{
		AgentRole: config.BuiltInSubagentRoleFast,
		Model:     "gpt-5.3-codex-spark",
	}, auth.State{Method: auth.Method{Type: auth.MethodAPIKey, APIKey: &auth.APIKeyMethod{Key: "test-key"}}})
	if err != nil {
		t.Fatalf("ApplyRunPromptOverrides: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if updated.ActiveSettings.Model != "gpt-5.3-codex-spark" {
		t.Fatalf("model = %q, want gpt-5.3-codex-spark", updated.ActiveSettings.Model)
	}
	if updated.ConfiguredModelName != "gpt-5.3-codex-spark" {
		t.Fatalf("configured model = %q, want gpt-5.3-codex-spark", updated.ConfiguredModelName)
	}
	if updated.ActiveSettings.ModelContextWindow != 128_000 {
		t.Fatalf("context window = %d, want 128000", updated.ActiveSettings.ModelContextWindow)
	}
	if updated.ActiveSettings.ContextCompactionThresholdTokens != 121_600 {
		t.Fatalf("compaction threshold = %d, want 121600", updated.ActiveSettings.ContextCompactionThresholdTokens)
	}
	if updated.ActiveSettings.PreSubmitCompactionLeadTokens != 35_000 {
		t.Fatalf("pre-submit lead = %d, want 35000", updated.ActiveSettings.PreSubmitCompactionLeadTokens)
	}
	if !updated.ActiveSettings.PriorityRequestMode {
		t.Fatal("expected fast-role priority mode to stay enabled")
	}
}

func TestApplyRunPromptOverridesCLIModelOverridePreservesExplicitThreshold(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	contents := strings.Join([]string{
		"model = \"gpt-5.4\"",
		"context_compaction_threshold_tokens = 180000",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := session.Create(filepath.Join(t.TempDir(), "sessions", "workspace-a"), "workspace-a", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	plan := SessionPlan{
		Store:               store,
		ActiveSettings:      loaded.Settings,
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: loaded.Settings.Model,
		WorkspaceRoot:       workspace,
		Source:              loaded.Source,
	}

	updated, warnings, err := ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{Model: "gpt-5.4-mini"}, auth.EmptyState())
	if err != nil {
		t.Fatalf("ApplyRunPromptOverrides: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if updated.ActiveSettings.Model != "gpt-5.4-mini" {
		t.Fatalf("model = %q, want gpt-5.4-mini", updated.ActiveSettings.Model)
	}
	if updated.ActiveSettings.ModelContextWindow != 272_000 {
		t.Fatalf("context window = %d, want 272000", updated.ActiveSettings.ModelContextWindow)
	}
	if updated.ActiveSettings.ContextCompactionThresholdTokens != 180_000 {
		t.Fatalf("compaction threshold = %d, want 180000", updated.ActiveSettings.ContextCompactionThresholdTokens)
	}
}

func TestPlannerInteractivePickerReopensSelectedSessionWithinActiveContainer(t *testing.T) {
	root := t.TempDir()
	containerA := filepath.Join(root, "sessions", "workspace-a")
	containerB := filepath.Join(root, "sessions", "workspace-b")
	selected, err := session.Create(containerA, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create selected session: %v", err)
	}
	if err := selected.SetName("selected"); err != nil {
		t.Fatalf("persist selected session meta: %v", err)
	}
	otherDir := filepath.Join(containerB, selected.Meta().SessionID)
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatalf("mkdir duplicate session dir: %v", err)
	}
	duplicateMeta := selected.Meta()
	duplicateMeta.WorkspaceContainer = "workspace-b"
	duplicateMeta.WorkspaceRoot = "/tmp/workspace-b"
	duplicateMeta.Name = "duplicate"
	duplicateData, err := json.Marshal(duplicateMeta)
	if err != nil {
		t.Fatalf("marshal duplicate session meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "session.json"), duplicateData, 0o644); err != nil {
		t.Fatalf("write duplicate session meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "events.jsonl"), nil, 0o644); err != nil {
		t.Fatalf("write duplicate session events: %v", err)
	}
	planner := Planner{
		Config:       config.App{WorkspaceRoot: "/tmp/workspace-a", PersistenceRoot: root, Settings: config.Settings{}},
		ContainerDir: containerA,
		PickSession: func(summaries []session.Summary) (SessionSelection, error) {
			picked := summaries[0]
			return SessionSelection{Session: &picked}, nil
		},
	}

	plan, err := planner.PlanSession(SessionRequest{Mode: ModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	openedDir, err := filepath.EvalSymlinks(plan.Store.Dir())
	if err != nil {
		t.Fatalf("EvalSymlinks opened dir: %v", err)
	}
	selectedDir, err := filepath.EvalSymlinks(selected.Dir())
	if err != nil {
		t.Fatalf("EvalSymlinks selected dir: %v", err)
	}
	if openedDir != selectedDir {
		t.Fatalf("opened session dir = %q, want %q", openedDir, selectedDir)
	}
	if plan.Store.Meta().WorkspaceContainer != "workspace-a" {
		t.Fatalf("opened workspace container = %q, want workspace-a", plan.Store.Meta().WorkspaceContainer)
	}
}

func TestPlannerSelectedSessionIDUsesActiveContainerScope(t *testing.T) {
	root := t.TempDir()
	containerA := filepath.Join(root, "sessions", "workspace-a")
	containerB := filepath.Join(root, "sessions", "workspace-b")
	selected, err := session.Create(containerA, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create selected session: %v", err)
	}
	if err := selected.SetName("selected"); err != nil {
		t.Fatalf("persist selected session meta: %v", err)
	}
	duplicateDir := filepath.Join(containerB, selected.Meta().SessionID)
	if err := os.MkdirAll(duplicateDir, 0o755); err != nil {
		t.Fatalf("mkdir duplicate session dir: %v", err)
	}
	duplicateMeta := selected.Meta()
	duplicateMeta.WorkspaceContainer = "workspace-b"
	duplicateMeta.WorkspaceRoot = "/tmp/workspace-b"
	duplicateData, err := json.Marshal(duplicateMeta)
	if err != nil {
		t.Fatalf("marshal duplicate session meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(duplicateDir, "session.json"), duplicateData, 0o644); err != nil {
		t.Fatalf("write duplicate session meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(duplicateDir, "events.jsonl"), nil, 0o644); err != nil {
		t.Fatalf("write duplicate session events: %v", err)
	}
	planner := Planner{
		Config:       config.App{WorkspaceRoot: "/tmp/workspace-a", PersistenceRoot: root, Settings: config.Settings{}},
		ContainerDir: containerA,
	}

	plan, err := planner.PlanSession(SessionRequest{Mode: ModeInteractive, SelectedSessionID: selected.Meta().SessionID})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	openedDir, err := filepath.EvalSymlinks(plan.Store.Dir())
	if err != nil {
		t.Fatalf("EvalSymlinks opened dir: %v", err)
	}
	selectedDir, err := filepath.EvalSymlinks(selected.Dir())
	if err != nil {
		t.Fatalf("EvalSymlinks selected dir: %v", err)
	}
	if openedDir != selectedDir {
		t.Fatalf("opened session dir = %q, want %q", openedDir, selectedDir)
	}
}

func TestPlannerSelectedSessionIDRejectsSymlinkOutsideActiveContainer(t *testing.T) {
	root := t.TempDir()
	containerA := filepath.Join(root, "sessions", "workspace-a")
	containerB := filepath.Join(root, "sessions", "workspace-b")
	if err := os.MkdirAll(containerA, 0o755); err != nil {
		t.Fatalf("mkdir container A: %v", err)
	}
	escaped, err := session.Create(containerB, "workspace-b", "/tmp/workspace-b")
	if err != nil {
		t.Fatalf("create escaped session: %v", err)
	}
	if err := os.Symlink(escaped.Dir(), filepath.Join(containerA, "escaped-link")); err != nil {
		t.Fatalf("symlink escaped session: %v", err)
	}
	planner := Planner{
		Config:       config.App{WorkspaceRoot: "/tmp/workspace-a", PersistenceRoot: root, Settings: config.Settings{}},
		ContainerDir: containerA,
	}

	if _, err := planner.PlanSession(SessionRequest{Mode: ModeInteractive, SelectedSessionID: "escaped-link"}); err == nil {
		t.Fatal("expected planner to reject symlinked selected session outside active container")
	}
}

type stubLaunchProjectViewService struct {
	overview      serverapi.ProjectGetOverviewResponse
	overviewCalls int
}

func (s *stubLaunchProjectViewService) ListProjects(_ context.Context, _ serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error) {
	return serverapi.ProjectListResponse{}, nil
}

func (s *stubLaunchProjectViewService) ResolveProjectPath(_ context.Context, _ serverapi.ProjectResolvePathRequest) (serverapi.ProjectResolvePathResponse, error) {
	return serverapi.ProjectResolvePathResponse{}, errors.New("ResolveProjectPath should not be called in planner tests")
}

func (s *stubLaunchProjectViewService) CreateProject(_ context.Context, _ serverapi.ProjectCreateRequest) (serverapi.ProjectCreateResponse, error) {
	return serverapi.ProjectCreateResponse{}, errors.New("CreateProject should not be called in planner tests")
}

func (s *stubLaunchProjectViewService) AttachWorkspaceToProject(_ context.Context, _ serverapi.ProjectAttachWorkspaceRequest) (serverapi.ProjectAttachWorkspaceResponse, error) {
	return serverapi.ProjectAttachWorkspaceResponse{}, errors.New("AttachWorkspaceToProject should not be called in planner tests")
}

func (s *stubLaunchProjectViewService) RebindWorkspace(_ context.Context, _ serverapi.ProjectRebindWorkspaceRequest) (serverapi.ProjectRebindWorkspaceResponse, error) {
	return serverapi.ProjectRebindWorkspaceResponse{}, errors.New("RebindWorkspace should not be called in planner tests")
}

func (s *stubLaunchProjectViewService) GetProjectOverview(_ context.Context, _ serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error) {
	s.overviewCalls++
	return s.overview, nil
}

func (s *stubLaunchProjectViewService) ListSessionsByProject(_ context.Context, _ serverapi.SessionListByProjectRequest) (serverapi.SessionListByProjectResponse, error) {
	return serverapi.SessionListByProjectResponse{}, errors.New("ListSessionsByProject should not be called when project overview is available")
}
