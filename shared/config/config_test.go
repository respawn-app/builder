package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"builder/shared/toolspec"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestLoadUsesDefaultsWithoutCreatingConfigOnFirstUse(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	settingsPath := filepath.Join(home, ".builder", "config.toml")
	if _, err := os.Stat(settingsPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected config file to stay absent, got err=%v", err)
	}
	rgConfigPath := filepath.Join(home, ".builder", managedRGConfigName)
	rgConfigBytes, err := os.ReadFile(rgConfigPath)
	if err != nil {
		t.Fatalf("read managed rg config: %v", err)
	}
	if string(rgConfigBytes) != managedRGConfigContents {
		t.Fatalf("managed rg config contents mismatch: %q", string(rgConfigBytes))
	}
	if cfg.Source.CreatedDefaultConfig {
		t.Fatalf("expected CreatedDefaultConfig=false")
	}
	if cfg.Source.SettingsFileExists {
		t.Fatalf("expected SettingsFileExists=false")
	}
	if cfg.Settings.Model != defaultModel {
		t.Fatalf("default model mismatch: %q", cfg.Settings.Model)
	}
	if cfg.Settings.WebSearch != "native" {
		t.Fatalf("default web_search mismatch: %q", cfg.Settings.WebSearch)
	}
	if cfg.Settings.ModelVerbosity != defaultModelVerbosity {
		t.Fatalf("default model_verbosity mismatch: %q", cfg.Settings.ModelVerbosity)
	}
	if cfg.Settings.NotificationMethod != "auto" {
		t.Fatalf("default notification_method mismatch: %q", cfg.Settings.NotificationMethod)
	}
	if !cfg.Settings.ToolPreambles {
		t.Fatalf("expected default tool_preambles=true")
	}
	if cfg.Settings.PriorityRequestMode {
		t.Fatalf("expected default priority_request_mode=false")
	}
	if cfg.Settings.Debug {
		t.Fatalf("expected default debug=false")
	}
	if cfg.Settings.TUIAlternateScreen != TUIAlternateScreenAuto {
		t.Fatalf("default tui_alternate_screen mismatch: %q", cfg.Settings.TUIAlternateScreen)
	}
	if got := cfg.PersistenceRoot; got != filepath.Join(home, ".builder") {
		t.Fatalf("default persistence root mismatch: %q", got)
	}
	if _, err := os.Stat(filepath.Join(cfg.PersistenceRoot, sessionsDirName)); err != nil {
		t.Fatalf("expected sessions root to exist: %v", err)
	}
	if !cfg.Settings.EnabledTools[toolspec.ToolExecCommand] || !cfg.Settings.EnabledTools[toolspec.ToolViewImage] || !cfg.Settings.EnabledTools[toolspec.ToolPatch] || !cfg.Settings.EnabledTools[toolspec.ToolAskQuestion] {
		t.Fatalf("expected all default tools enabled: %+v", cfg.Settings.EnabledTools)
	}
	if cfg.Settings.EnabledTools[toolspec.ToolTriggerHandoff] {
		t.Fatalf("expected %s disabled in static defaults", toolspec.ToolTriggerHandoff)
	}
	if got := cfg.Source.Sources["tools.trigger_handoff"]; got != "default" {
		t.Fatalf("expected untouched %s source to remain default, got %q", toolspec.ToolTriggerHandoff, got)
	}
	if !cfg.Settings.EnabledTools[toolspec.ToolWebSearch] {
		t.Fatalf("expected web_search tool enabled by default: %+v", cfg.Settings.EnabledTools)
	}
	if cfg.Settings.ContextCompactionThresholdTokens != defaultCompactionThreshold {
		t.Fatalf("default compaction threshold mismatch: %d", cfg.Settings.ContextCompactionThresholdTokens)
	}
	if cfg.Settings.PreSubmitCompactionLeadTokens != 35_000 {
		t.Fatalf("default pre-submit runway mismatch: %d", cfg.Settings.PreSubmitCompactionLeadTokens)
	}
	if cfg.Settings.MinimumExecToBgSeconds != defaultMinimumExecToBgSec {
		t.Fatalf("default minimum_exec_to_bg_seconds mismatch: %d", cfg.Settings.MinimumExecToBgSeconds)
	}
	if cfg.Settings.ModelContextWindow != defaultModelContextWindow {
		t.Fatalf("default model context window mismatch: %d", cfg.Settings.ModelContextWindow)
	}
	if cfg.Settings.Store {
		t.Fatalf("expected default store=false")
	}
	if cfg.Settings.AllowNonCwdEdits {
		t.Fatalf("expected default allow_non_cwd_edits=false")
	}
	if cfg.Settings.CompactionMode != CompactionModeLocal {
		t.Fatalf("expected default compaction_mode=local, got %q", cfg.Settings.CompactionMode)
	}
	if cfg.Settings.ShellOutputMaxChars != 16000 {
		t.Fatalf("default shell_output_max_chars mismatch: %d", cfg.Settings.ShellOutputMaxChars)
	}
	if cfg.Settings.BGShellsOutput != BGShellsOutputDefault {
		t.Fatalf("default bg_shells_output mismatch: %q", cfg.Settings.BGShellsOutput)
	}
	if cfg.Settings.Shell.PostprocessingMode != ShellPostprocessingModeBuiltin {
		t.Fatalf("default shell.postprocessing_mode mismatch: %q", cfg.Settings.Shell.PostprocessingMode)
	}
	if cfg.Settings.Shell.PostprocessHook != "" {
		t.Fatalf("default shell.postprocess_hook mismatch: %q", cfg.Settings.Shell.PostprocessHook)
	}
	if got := cfg.Settings.Worktrees.BaseDir; got != filepath.Join(cfg.PersistenceRoot, "worktrees") {
		t.Fatalf("default worktrees.base_dir mismatch: %q", got)
	}
	if cfg.Settings.Worktrees.SetupScript != "" {
		t.Fatalf("expected default worktrees.setup_script empty, got %q", cfg.Settings.Worktrees.SetupScript)
	}
	if cfg.Settings.Reviewer.Frequency != defaultReviewerFrequency {
		t.Fatalf("expected default reviewer.frequency=%s, got %q", defaultReviewerFrequency, cfg.Settings.Reviewer.Frequency)
	}
	if cfg.Settings.Reviewer.Model != cfg.Settings.Model {
		t.Fatalf("default reviewer model mismatch: %q", cfg.Settings.Reviewer.Model)
	}
	if cfg.Settings.Reviewer.ThinkingLevel != cfg.Settings.ThinkingLevel {
		t.Fatalf("default reviewer thinking_level mismatch: %q", cfg.Settings.Reviewer.ThinkingLevel)
	}
	if cfg.Settings.Reviewer.TimeoutSeconds != 60 {
		t.Fatalf("default reviewer timeout mismatch: %d", cfg.Settings.Reviewer.TimeoutSeconds)
	}
	if cfg.Settings.Reviewer.VerboseOutput {
		t.Fatalf("expected default reviewer verbose_output=false")
	}
	settingsBytes := []byte(defaultSettingsTOML())
	if !strings.Contains(string(settingsBytes), "# Config reference: https://opensource.respawn.pro/builder/config/") {
		t.Fatalf("expected default config to include config reference header, got %q", string(settingsBytes))
	}
	if !strings.Contains(string(settingsBytes), "# model_verbosity = \"medium\"") {
		t.Fatalf("expected default config to expose model_verbosity option, got %q", string(settingsBytes))
	}
	if !strings.Contains(string(settingsBytes), "# debug = false") {
		t.Fatalf("expected default config to expose global debug option, got %q", string(settingsBytes))
	}
	if !strings.Contains(string(settingsBytes), "# pre_submit_compaction_lead_tokens = 35000") {
		t.Fatalf("expected default config to expose pre-submit runway default, got %q", string(settingsBytes))
	}
	if !strings.Contains(string(settingsBytes), "[worktrees]") {
		t.Fatalf("expected default config to include worktrees section, got %q", string(settingsBytes))
	}
	if !strings.Contains(string(settingsBytes), "# base_dir = \"~/.builder/worktrees\"") {
		t.Fatalf("expected default config to expose worktrees.base_dir, got %q", string(settingsBytes))
	}
	if !strings.Contains(string(settingsBytes), "# setup_script = \"\"") {
		t.Fatalf("expected default config to expose worktrees.setup_script, got %q", string(settingsBytes))
	}
	if strings.Contains(string(settingsBytes), "thinking_level = \"low\"") {
		t.Fatalf("expected default config not to hardcode reviewer thinking inheritance, got %q", string(settingsBytes))
	}
	if strings.Contains(string(settingsBytes), "max_suggestions") {
		t.Fatalf("expected default config not to expose reviewer.max_suggestions, got %q", string(settingsBytes))
	}
	if !strings.Contains(string(settingsBytes), "verbose_output = false") {
		t.Fatalf("expected default config to expose reviewer.verbose_output, got %q", string(settingsBytes))
	}
	if !strings.Contains(string(settingsBytes), "# model = \"gpt-5.4\" # inherited from main model unless overridden") {
		t.Fatalf("expected default config to show inherited reviewer model line, got %q", string(settingsBytes))
	}
	if !strings.Contains(string(settingsBytes), "[tools]") {
		t.Fatalf("expected default config to include tools section, got %q", string(settingsBytes))
	}
	if !strings.Contains(string(settingsBytes), "[shell]") {
		t.Fatalf("expected default config to include shell section, got %q", string(settingsBytes))
	}
	if !strings.Contains(string(settingsBytes), "# postprocessing_mode = \"builtin\"") {
		t.Fatalf("expected default config to expose shell.postprocessing_mode, got %q", string(settingsBytes))
	}
	if !strings.Contains(string(settingsBytes), "# ask_question = true") {
		t.Fatalf("expected default config to include commented default tool values, got %q", string(settingsBytes))
	}
	if !strings.Contains(string(settingsBytes), "[subagents.fast]") {
		t.Fatalf("expected default config to include built-in fast subagent section, got %q", string(settingsBytes))
	}
	if !strings.Contains(string(settingsBytes), "gpt-5.4-mini") {
		t.Fatalf("expected default config to document built-in fast model heuristic, got %q", string(settingsBytes))
	}
	if strings.Contains(string(settingsBytes), "[model_capabilities]") || strings.Contains(string(settingsBytes), "[provider_capabilities]") {
		t.Fatalf("expected default config to omit capability sections without overrides, got %q", string(settingsBytes))
	}
	if strings.Contains(string(settingsBytes), "This JSON block mirrors") {
		t.Fatalf("expected default config to omit mirrored JSON block, got %q", string(settingsBytes))
	}
}

func TestEnsureManagedRGConfigFilePreservesExistingContents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := ResolveManagedRGConfigPath()
	if err != nil {
		t.Fatalf("resolve managed rg config path: %v", err)
	}
	if err := ensureSettingsDir(path); err != nil {
		t.Fatalf("ensure settings dir: %v", err)
	}
	const existing = "--max-columns=80\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing managed rg config: %v", err)
	}

	createdPath, created, err := EnsureManagedRGConfigFile()
	if err != nil {
		t.Fatalf("ensure managed rg config file: %v", err)
	}
	if created {
		t.Fatal("expected existing managed rg config not to be replaced")
	}
	if createdPath != path {
		t.Fatalf("managed rg config path = %q, want %q", createdPath, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read managed rg config: %v", err)
	}
	if string(data) != existing {
		t.Fatalf("managed rg config contents = %q, want %q", string(data), existing)
	}
}

func TestSettingsTOMLCommentsDefaultAssignmentsForOnboarding(t *testing.T) {
	toml := settingsTOML(defaultSettings())
	if !strings.Contains(toml, "# theme = \"auto\"") {
		t.Fatalf("expected onboarding config to comment auto theme default, got %q", toml)
	}
	if !strings.Contains(toml, "# debug = false") {
		t.Fatalf("expected onboarding config to comment global debug default, got %q", toml)
	}
	for _, want := range []string{
		"# provider_override = \"\"",
		"# openai_base_url = \"\"",
		"[worktrees]",
		"[reviewer]",
		"[timeouts]",
		"[tools]",
	} {
		if !strings.Contains(toml, want) {
			t.Fatalf("expected onboarding config to include %q, got %q", want, toml)
		}
	}
	if strings.Contains(toml, "This JSON block mirrors") {
		t.Fatalf("expected onboarding config to omit mirrored JSON block, got %q", toml)
	}
	if !strings.Contains(toml, "[subagents.fast]") {
		t.Fatalf("expected onboarding config to include built-in fast subagent section, got %q", toml)
	}
}

func TestLoadSubagentRoleFromFile(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	contents := strings.Join([]string{
		"model = \"gpt-5.4\"",
		"",
		"[subagents.fast]",
		"model = \"gpt-5.4-mini\"",
		"thinking_level = \"low\"",
		"",
		"[subagents.fast.tools]",
		"patch = false",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	role, ok := cfg.Settings.Subagents[BuiltInSubagentRoleFast]
	if !ok {
		t.Fatalf("expected fast subagent role, got %+v", cfg.Settings.Subagents)
	}
	if role.Settings.Model != "gpt-5.4-mini" {
		t.Fatalf("role model = %q, want gpt-5.4-mini", role.Settings.Model)
	}
	if role.Settings.ThinkingLevel != "low" {
		t.Fatalf("role thinking = %q, want low", role.Settings.ThinkingLevel)
	}
	if role.Settings.EnabledTools[toolspec.ToolPatch] {
		t.Fatalf("expected fast role patch tool disabled, got %+v", role.Settings.EnabledTools)
	}
	if role.Sources["model"] != "file" || role.Sources["thinking_level"] != "file" || role.Sources["tools.patch"] != "file" {
		t.Fatalf("unexpected role sources: %+v", role.Sources)
	}
	if _, exists := role.Sources["reviewer.model"]; exists {
		t.Fatalf("did not expect inherited reviewer model to be marked explicit, got %+v", role.Sources)
	}
}

func TestLoadSubagentRoleRejectsNestedSubagentsTable(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	contents := strings.Join([]string{
		"model = \"gpt-5.4\"",
		"",
		"[subagents.fast]",
		"thinking_level = \"low\"",
		"",
		"[subagents.fast.subagents.worker]",
		"thinking_level = \"high\"",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(workspace, LoadOptions{})
	if err == nil {
		t.Fatal("expected nested subagents table to fail")
	}
	if !strings.Contains(err.Error(), "unknown settings key(s): subagents.fast.subagents") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadSubagentRoleRejectsUnknownKeys(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	contents := strings.Join([]string{
		"model = \"gpt-5.4\"",
		"",
		"[subagents.fast]",
		"thinking_level = \"low\"",
		"unknown_toggle = true",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(workspace, LoadOptions{})
	if err == nil {
		t.Fatal("expected unknown subagent key to fail")
	}
	if !strings.Contains(err.Error(), "unknown settings key(s): subagents.fast.unknown_toggle") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadResolvesWorktreeBaseDirRelativeToPersistenceRoot(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".builder")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configText := strings.Join([]string{
		"persistence_root = \"~/custom-builder\"",
		"",
		"[worktrees]",
		"base_dir = \"managed/worktrees\"",
		"setup_script = \"scripts/setup-worktree.sh\"",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configText), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got, want := cfg.PersistenceRoot, filepath.Join(home, "custom-builder"); got != want {
		t.Fatalf("persistence root = %q, want %q", got, want)
	}
	if got, want := cfg.Settings.Worktrees.BaseDir, filepath.Join(cfg.PersistenceRoot, "managed", "worktrees"); got != want {
		t.Fatalf("worktrees.base_dir = %q, want %q", got, want)
	}
	if got := cfg.Settings.Worktrees.SetupScript; got != "scripts/setup-worktree.sh" {
		t.Fatalf("worktrees.setup_script = %q, want scripts/setup-worktree.sh", got)
	}
}

func TestLoadDerivesDefaultWorktreeBaseDirFromPersistenceRoot(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".builder")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configText := "persistence_root = \"~/custom-builder\"\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configText), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got, want := cfg.PersistenceRoot, filepath.Join(home, "custom-builder"); got != want {
		t.Fatalf("persistence root = %q, want %q", got, want)
	}
	if got, want := cfg.Settings.Worktrees.BaseDir, filepath.Join(cfg.PersistenceRoot, "worktrees"); got != want {
		t.Fatalf("worktrees.base_dir = %q, want %q", got, want)
	}
}

func TestLoadSubagentRoleRejectsInvalidValues(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	contents := strings.Join([]string{
		"model = \"gpt-5.4\"",
		"",
		"[subagents.fast]",
		"provider_override = \"bogus\"",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(workspace, LoadOptions{})
	if err == nil {
		t.Fatal("expected invalid subagent role values to fail")
	}
	if !strings.Contains(err.Error(), "invalid subagents.fast") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadSubagentRoleRejectsPersistenceRoot(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	contents := strings.Join([]string{
		"model = \"gpt-5.4\"",
		"",
		"[subagents.fast]",
		"persistence_root = \"/tmp/custom\"",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(workspace, LoadOptions{})
	if err == nil {
		t.Fatal("expected persistence_root in subagent role to fail")
	}
	if !strings.Contains(err.Error(), "persistence_root is not supported in subagent roles") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSettingsTOMLForOnboardingMatchesRequestedShape(t *testing.T) {
	settings := defaultSettings()
	settings.Model = "Custom_selected_during_onboarding_hence_uncommented"
	settings.Theme = "auto"
	settings.ModelVerbosity = ModelVerbosityLow
	settings.Reviewer.Model = "gpt-5.4-mini"
	settings.Reviewer.ThinkingLevel = "high"

	toml := settingsTOMLForOnboarding(settings, map[string]bool{
		"model":                   true,
		"theme":                   true,
		"reviewer.model":          true,
		"reviewer.thinking_level": true,
	})

	for _, want := range []string{
		"# Edit and restart to apply changes.",
		"# Config reference: https://opensource.respawn.pro/builder/config/",
		"# model changes are applied only when creating a new session",
		"model = \"Custom_selected_during_onboarding_hence_uncommented\"",
		"# thinking_level = \"medium\"",
		"theme = \"auto\"",
		"model_verbosity = \"low\"",
		"[tools]",
		"[reviewer]",
		"model = \"gpt-5.4-mini\"",
		"thinking_level = \"high\"",
		"# frequency = \"edits\"",
		"# timeout_seconds = 60",
		"# verbose_output = false",
	} {
		if !strings.Contains(toml, want) {
			t.Fatalf("expected onboarding config to contain %q, got %q", want, toml)
		}
	}
	if strings.Contains(toml, "[model_capabilities]") || strings.Contains(toml, "[provider_capabilities]") {
		t.Fatalf("expected onboarding config to omit capability sections without overrides, got %q", toml)
	}
	if strings.Index(toml, "model = \"Custom_selected_during_onboarding_hence_uncommented\"") > strings.Index(toml, "[tools]") {
		t.Fatalf("expected model section before tools, got %q", toml)
	}
	if strings.Index(toml, "[tools]") > strings.Index(toml, "[reviewer]") {
		t.Fatalf("expected tools section before reviewer section, got %q", toml)
	}
	if strings.Contains(toml, "debug =") {
		t.Fatalf("expected onboarding config to omit debug setting, got %q", toml)
	}
}

func TestOnboardingDefaultSettingsTOMLOmitsDebugSetting(t *testing.T) {
	toml := onboardingDefaultSettingsTOML("dark")
	if strings.Contains(toml, "debug =") {
		t.Fatalf("expected onboarding default config to omit debug setting, got %q", toml)
	}
}

func TestSettingsTOMLRoundTripsCapabilityOverrides(t *testing.T) {
	settings := defaultSettings()
	settings.ModelCapabilities.SupportsReasoningEffort = true
	settings.ProviderCapabilities = ProviderCapabilitiesOverride{
		ProviderID:                     "openai-compatible",
		SupportsResponsesAPI:           true,
		SupportsRequestInputTokenCount: true,
		SupportsPromptCacheKey:         true,
		SupportsServerSideContextEdit:  true,
	}
	toml := settingsTOML(settings)
	for _, want := range []string{
		"[model_capabilities]",
		"supports_reasoning_effort = true",
		"[provider_capabilities]",
		"provider_id = \"openai-compatible\"",
		"supports_responses_api = true",
		"supports_request_input_token_count = true",
		"supports_prompt_cache_key = true",
		"supports_server_side_context_edit = true",
	} {
		if !strings.Contains(toml, want) {
			t.Fatalf("expected serialized settings to contain %q, got %q", want, toml)
		}
	}
	if strings.Contains(toml, "# [model_capabilities]") {
		t.Fatalf("expected model_capabilities section to be active when overrides exist, got %q", toml)
	}
	if strings.Contains(toml, "# [provider_capabilities]") {
		t.Fatalf("expected provider_capabilities section to be active when overrides exist, got %q", toml)
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	raw, err := readSettingsFile(path)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	state := configRegistry.defaultState()
	sources := configRegistry.defaultSourceMap()
	if err := configRegistry.applyFile(raw, path, &state, sources); err != nil {
		t.Fatalf("apply file: %v", err)
	}
	if !state.Settings.ModelCapabilities.SupportsReasoningEffort {
		t.Fatal("expected model capability override to round-trip")
	}
	if state.Settings.ProviderCapabilities.ProviderID != "openai-compatible" {
		t.Fatalf("expected provider_id to round-trip, got %q", state.Settings.ProviderCapabilities.ProviderID)
	}
	if !state.Settings.ProviderCapabilities.SupportsResponsesAPI {
		t.Fatal("expected supports_responses_api to round-trip")
	}
	if !state.Settings.ProviderCapabilities.SupportsRequestInputTokenCount {
		t.Fatal("expected supports_request_input_token_count to round-trip")
	}
	if !state.Settings.ProviderCapabilities.SupportsServerSideContextEdit {
		t.Fatal("expected supports_server_side_context_edit to round-trip")
	}
}

func TestWriteDefaultSettingsFileWithThemePersistsSelectedTheme(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, created, err := WriteDefaultSettingsFileWithTheme("light")
	if err != nil {
		t.Fatalf("write default settings with theme: %v", err)
	}
	if !created {
		t.Fatal("expected default settings file to be created")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	if !strings.Contains(string(contents), "theme = \"light\"") {
		t.Fatalf("expected selected theme to be persisted, got %q", string(contents))
	}
}

func TestWriteDefaultSettingsFileUsesAutoThemeByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, created, err := WriteDefaultSettingsFile()
	if err != nil {
		t.Fatalf("write default settings: %v", err)
	}
	if !created {
		t.Fatal("expected default settings file to be created")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	if !strings.Contains(string(contents), "theme = \"auto\"") {
		t.Fatalf("expected default settings file to persist auto theme, got %q", string(contents))
	}
}

func TestWriteSettingsFileForOnboardingPreservesAutoTheme(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := WriteSettingsFileForOnboarding(defaultSettings())
	if err != nil {
		t.Fatalf("write onboarding settings: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	if !strings.Contains(string(contents), "theme = \"auto\"") {
		t.Fatalf("expected onboarding settings file to preserve auto theme, got %q", string(contents))
	}
}

func TestWriteSettingsFileForOnboardingPreservesModelWhenProviderOverrideIsSet(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	settings := defaultSettings()
	settings.ProviderOverride = "openai"
	path, err := WriteSettingsFileForOnboarding(settings)
	if err != nil {
		t.Fatalf("write onboarding settings: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	if !strings.Contains(string(contents), "model = \"gpt-5.4\"") {
		t.Fatalf("expected onboarding settings to preserve explicit model with provider_override, got %q", string(contents))
	}
	if !strings.Contains(string(contents), "provider_override = \"openai\"") {
		t.Fatalf("expected provider_override to be persisted, got %q", string(contents))
	}
	if _, err := Load(workspace, LoadOptions{}); err != nil {
		t.Fatalf("expected persisted provider_override config to load successfully, got %v", err)
	}
}

func TestWriteSettingsFileForOnboardingWithOptionsPreservesExplicitReviewerOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := defaultSettings()
	settings.Reviewer.Model = "gpt-5.4-mini"
	settings.Reviewer.ThinkingLevel = "high"

	path, err := WriteSettingsFileForOnboardingWithOptions(settings, OnboardingWriteOptions{
		PreservedDefaults: map[string]bool{
			"reviewer.model":          true,
			"reviewer.thinking_level": true,
		},
	})
	if err != nil {
		t.Fatalf("write onboarding settings: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	if !strings.Contains(string(contents), "[reviewer]") {
		t.Fatalf("expected reviewer section to be persisted, got %q", string(contents))
	}
	if !strings.Contains(string(contents), "model = \"gpt-5.4-mini\"") {
		t.Fatalf("expected reviewer model override to be persisted, got %q", string(contents))
	}
	if !strings.Contains(string(contents), "thinking_level = \"high\"") {
		t.Fatalf("expected reviewer thinking override to be persisted, got %q", string(contents))
	}
}

func TestWriteSettingsFileForOnboardingDoesNotOverwriteExistingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model = \"existing\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := WriteSettingsFileForOnboarding(defaultSettings())
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected existing settings file error, got %v", err)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	if string(contents) != "model = \"existing\"\n" {
		t.Fatalf("expected existing settings file contents to remain unchanged, got %q", string(contents))
	}
}

func TestValidateThemeAllowsAutoAndEmpty(t *testing.T) {
	for _, value := range []string{"", "auto", "light", "dark"} {
		if err := validateTheme(settingsState{Settings: Settings{Theme: value}}, nil); err != nil {
			t.Fatalf("validate theme %q: %v", value, err)
		}
	}
}

func TestLoadReviewerDefaultsInheritMainSettingsWhenUnset(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model = \"gpt-main-file\"\nthinking_level = \"xhigh\"\n[reviewer]\nfrequency = \"all\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.Reviewer.Model != "gpt-main-file" {
		t.Fatalf("expected reviewer.model to inherit file main model, got %q", cfg.Settings.Reviewer.Model)
	}
	if cfg.Settings.Reviewer.ThinkingLevel != "xhigh" {
		t.Fatalf("expected reviewer.thinking_level to inherit file main thinking level, got %q", cfg.Settings.Reviewer.ThinkingLevel)
	}

	t.Setenv("BUILDER_MODEL", "gpt-main-env")
	t.Setenv("BUILDER_THINKING_LEVEL", "medium")
	t.Setenv("BUILDER_REVIEWER_MODEL", "")
	t.Setenv("BUILDER_REVIEWER_THINKING_LEVEL", "")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env model: %v", err)
	}
	if cfg.Settings.Reviewer.Model != "gpt-main-env" {
		t.Fatalf("expected reviewer.model to inherit env main model, got %q", cfg.Settings.Reviewer.Model)
	}
	if cfg.Settings.Reviewer.ThinkingLevel != "medium" {
		t.Fatalf("expected reviewer.thinking_level to inherit env main thinking level, got %q", cfg.Settings.Reviewer.ThinkingLevel)
	}
}

func TestLoadCapabilityOverridesFromFile(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`model = "gpt-5.4"

[model_capabilities]
supports_reasoning_effort = true
supports_vision_inputs = true

[provider_capabilities]
provider_id = "custom-provider"
supports_responses_api = true
supports_responses_compact = false
supports_request_input_token_count = false
supports_prompt_cache_key = true
supports_native_web_search = true
supports_reasoning_encrypted = false
supports_server_side_context_edit = false
is_openai_first_party = false
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Settings.ModelCapabilities.SupportsReasoningEffort || !cfg.Settings.ModelCapabilities.SupportsVisionInputs {
		t.Fatalf("expected model capability overrides from file, got %+v", cfg.Settings.ModelCapabilities)
	}
	if cfg.Settings.ProviderCapabilities.ProviderID != "custom-provider" || !cfg.Settings.ProviderCapabilities.SupportsResponsesAPI || !cfg.Settings.ProviderCapabilities.SupportsPromptCacheKey || !cfg.Settings.ProviderCapabilities.SupportsNativeWebSearch {
		t.Fatalf("expected provider capability overrides from file, got %+v", cfg.Settings.ProviderCapabilities)
	}
	if cfg.Settings.ProviderCapabilities.SupportsRequestInputTokenCount {
		t.Fatalf("expected supports_request_input_token_count override from file, got %+v", cfg.Settings.ProviderCapabilities)
	}
	if got := cfg.Source.Sources["model_capabilities.supports_reasoning_effort"]; got != "file" {
		t.Fatalf("expected model_capabilities.supports_reasoning_effort source file, got %q", got)
	}
	if got := cfg.Source.Sources["provider_capabilities.provider_id"]; got != "file" {
		t.Fatalf("expected provider_capabilities.provider_id source file, got %q", got)
	}
	if got := cfg.Source.Sources["provider_capabilities.supports_request_input_token_count"]; got != "file" {
		t.Fatalf("expected provider_capabilities.supports_request_input_token_count source file, got %q", got)
	}
}

func TestLoadCapabilityOverridesFromEnv(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BUILDER_MODEL_CAPABILITIES_SUPPORTS_REASONING_EFFORT", "true")
	t.Setenv("BUILDER_MODEL_CAPABILITIES_SUPPORTS_VISION_INPUTS", "true")
	t.Setenv("BUILDER_PROVIDER_CAPABILITIES_PROVIDER_ID", "custom-provider")
	t.Setenv("BUILDER_PROVIDER_CAPABILITIES_SUPPORTS_RESPONSES_API", "true")
	t.Setenv("BUILDER_PROVIDER_CAPABILITIES_SUPPORTS_RESPONSES_COMPACT", "false")
	t.Setenv("BUILDER_PROVIDER_CAPABILITIES_SUPPORTS_REQUEST_INPUT_TOKEN_COUNT", "false")
	t.Setenv("BUILDER_PROVIDER_CAPABILITIES_SUPPORTS_PROMPT_CACHE_KEY", "true")
	t.Setenv("BUILDER_PROVIDER_CAPABILITIES_SUPPORTS_NATIVE_WEB_SEARCH", "true")
	t.Setenv("BUILDER_PROVIDER_CAPABILITIES_SUPPORTS_REASONING_ENCRYPTED", "false")
	t.Setenv("BUILDER_PROVIDER_CAPABILITIES_SUPPORTS_SERVER_SIDE_CONTEXT_EDIT", "false")
	t.Setenv("BUILDER_PROVIDER_CAPABILITIES_IS_OPENAI_FIRST_PARTY", "false")

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Settings.ModelCapabilities.SupportsReasoningEffort || !cfg.Settings.ModelCapabilities.SupportsVisionInputs {
		t.Fatalf("expected model capability overrides from env, got %+v", cfg.Settings.ModelCapabilities)
	}
	if cfg.Settings.ProviderCapabilities.ProviderID != "custom-provider" || !cfg.Settings.ProviderCapabilities.SupportsResponsesAPI || !cfg.Settings.ProviderCapabilities.SupportsPromptCacheKey || !cfg.Settings.ProviderCapabilities.SupportsNativeWebSearch {
		t.Fatalf("expected provider capability overrides from env, got %+v", cfg.Settings.ProviderCapabilities)
	}
	if cfg.Settings.ProviderCapabilities.SupportsRequestInputTokenCount {
		t.Fatalf("expected supports_request_input_token_count override from env, got %+v", cfg.Settings.ProviderCapabilities)
	}
	if got := cfg.Source.Sources["model_capabilities.supports_reasoning_effort"]; got != "env" {
		t.Fatalf("expected model_capabilities.supports_reasoning_effort source env, got %q", got)
	}
	if got := cfg.Source.Sources["provider_capabilities.provider_id"]; got != "env" {
		t.Fatalf("expected provider_capabilities.provider_id source env, got %q", got)
	}
	if got := cfg.Source.Sources["provider_capabilities.supports_request_input_token_count"]; got != "env" {
		t.Fatalf("expected provider_capabilities.supports_request_input_token_count source env, got %q", got)
	}
}

func TestLoadProviderOverrideFromFile(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model = \"my-team-alias\"\nprovider_override = \"OpenAI\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.ProviderOverride != "openai" {
		t.Fatalf("expected normalized provider_override from file, got %q", cfg.Settings.ProviderOverride)
	}
	if got := cfg.Source.Sources["provider_override"]; got != "file" {
		t.Fatalf("expected provider_override source file, got %q", got)
	}
}

func TestLoadProviderOverrideRequiresExplicitModelOverride(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("provider_override = \"openai\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(workspace, LoadOptions{})
	if err == nil {
		t.Fatal("expected provider_override without model override to fail")
	}
	if !strings.Contains(err.Error(), "provider_override requires an explicit model override") {
		t.Fatalf("expected provider_override/model override validation error, got %v", err)
	}
}

func TestLoadProviderOverrideRejectsUnsupportedProviderFamily(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model = \"my-team-alias\"\nprovider_override = \"openrouter\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(workspace, LoadOptions{})
	if err == nil {
		t.Fatal("expected invalid provider_override to fail")
	}
	if !strings.Contains(err.Error(), "invalid provider_override") {
		t.Fatalf("expected invalid provider_override validation error, got %v", err)
	}
}

func TestLoadProviderOverrideRejectsOpenAIBaseURLConflict(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model = \"my-team-alias\"\nprovider_override = \"anthropic\"\nopenai_base_url = \"https://example.openrouter.ai/api/v1\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(workspace, LoadOptions{})
	if err == nil {
		t.Fatal("expected provider_override/openai_base_url conflict to fail")
	}
	if !strings.Contains(err.Error(), "conflicts with openai_base_url") {
		t.Fatalf("expected provider_override/openai_base_url conflict error, got %v", err)
	}
}

func TestLoadProviderOverrideFromCLIWithExplicitFileModel(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model = \"my-team-alias\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{ProviderOverride: "openai"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.ProviderOverride != "openai" {
		t.Fatalf("expected cli provider_override, got %q", cfg.Settings.ProviderOverride)
	}
	if got := cfg.Source.Sources["provider_override"]; got != "cli" {
		t.Fatalf("expected provider_override source cli, got %q", got)
	}
}

func TestLoadCapabilityOverridesRequireProviderID(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BUILDER_PROVIDER_CAPABILITIES_SUPPORTS_NATIVE_WEB_SEARCH", "true")

	_, err := Load(workspace, LoadOptions{})
	if err == nil {
		t.Fatal("expected validation error when provider capability override is set without provider_id")
	}
	if !strings.Contains(err.Error(), "provider_capabilities.provider_id") {
		t.Fatalf("expected provider_id validation error, got %v", err)
	}
}

func TestLoadPriorityRequestModeFromFile(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("priority_request_mode = true\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Settings.PriorityRequestMode {
		t.Fatal("expected priority_request_mode=true from file")
	}
	if got := cfg.Source.Sources["priority_request_mode"]; got != "file" {
		t.Fatalf("expected priority_request_mode source file, got %q", got)
	}
}

func TestLoadModelVerbosityFromFile(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model_verbosity = \"high\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.ModelVerbosity != ModelVerbosityHigh {
		t.Fatalf("expected model_verbosity=high from file, got %q", cfg.Settings.ModelVerbosity)
	}
	if got := cfg.Source.Sources["model_verbosity"]; got != "file" {
		t.Fatalf("expected model_verbosity source file, got %q", got)
	}
}

func TestLoadRejectsInvalidModelVerbosityFromFile(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model_verbosity = \"verbose\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(workspace, LoadOptions{})
	if err == nil {
		t.Fatal("expected validation error for invalid model_verbosity")
	}
	if !strings.Contains(err.Error(), "model_verbosity") {
		t.Fatalf("expected model_verbosity validation error, got %v", err)
	}
}

func TestResolveWorkspaceContainerUsesSessionsSubdirectory(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	containerName, containerDir, err := ResolveWorkspaceContainer(cfg)
	if err != nil {
		t.Fatalf("resolve workspace container: %v", err)
	}
	if containerName == "" {
		t.Fatal("expected non-empty container name")
	}
	wantParent := filepath.Join(cfg.PersistenceRoot, sessionsDirName)
	if filepath.Dir(containerDir) != wantParent {
		t.Fatalf("expected container under %q, got %q", wantParent, containerDir)
	}
	if _, err := os.Stat(containerDir); err != nil {
		t.Fatalf("expected container dir to exist: %v", err)
	}

	againName, againDir, err := ResolveWorkspaceContainer(cfg)
	if err != nil {
		t.Fatalf("resolve workspace container second time: %v", err)
	}
	if againName != containerName || againDir != containerDir {
		t.Fatalf("expected stable workspace container, got %q %q after %q %q", againName, againDir, containerName, containerDir)
	}
	canonicalRoot, err := canonicalWorkspaceRoot(cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("canonical workspace root: %v", err)
	}
	wantName := deterministicWorkspaceContainerName(canonicalRoot)
	if containerName != wantName {
		t.Fatalf("expected deterministic container name %q, got %q", wantName, containerName)
	}
}

func TestResolveWorkspaceContainerUsesLegacyWorkspaceIndexMapping(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	legacyContainer := "workspace-a-legacy"
	indexPath := filepath.Join(cfg.PersistenceRoot, workspaceIndexName)
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatalf("mkdir workspace index dir: %v", err)
	}
	indexData, err := json.Marshal(workspaceIndex{Entries: map[string]string{cfg.WorkspaceRoot: legacyContainer}})
	if err != nil {
		t.Fatalf("marshal workspace index: %v", err)
	}
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		t.Fatalf("write workspace index: %v", err)
	}

	containerName, containerDir, err := ResolveWorkspaceContainer(cfg)
	if err != nil {
		t.Fatalf("resolve workspace container: %v", err)
	}
	if containerName != legacyContainer {
		t.Fatalf("expected legacy workspace container %q, got %q", legacyContainer, containerName)
	}
	if got := filepath.Base(containerDir); got != legacyContainer {
		t.Fatalf("expected legacy container dir %q, got %q", legacyContainer, got)
	}
	if _, err := os.Stat(containerDir); err != nil {
		t.Fatalf("expected legacy container dir to exist: %v", err)
	}
}

func TestResolveWorkspaceContainerRejectsInvalidLegacyWorkspaceIndexMapping(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	indexPath := filepath.Join(cfg.PersistenceRoot, workspaceIndexName)
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatalf("mkdir workspace index dir: %v", err)
	}
	indexData, err := json.Marshal(workspaceIndex{Entries: map[string]string{cfg.WorkspaceRoot: "../outside"}})
	if err != nil {
		t.Fatalf("marshal workspace index: %v", err)
	}
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		t.Fatalf("write workspace index: %v", err)
	}

	_, _, err = ResolveWorkspaceContainer(cfg)
	if err == nil {
		t.Fatal("expected invalid legacy container error")
	}
	if !strings.Contains(err.Error(), "invalid legacy workspace container") {
		t.Fatalf("expected invalid legacy container error, got %v", err)
	}
}

func TestResolveWorkspaceContainerCanonicalizesSymlinkedWorkspace(t *testing.T) {
	home := t.TempDir()
	realWorkspace := t.TempDir()
	linkParent := t.TempDir()
	symlinkPath := filepath.Join(linkParent, "workspace-link")
	if err := os.Symlink(realWorkspace, symlinkPath); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	t.Setenv("HOME", home)

	realCfg, err := Load(realWorkspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load real workspace: %v", err)
	}
	realName, realDir, err := ResolveWorkspaceContainer(realCfg)
	if err != nil {
		t.Fatalf("resolve real workspace container: %v", err)
	}

	symlinkCfg, err := Load(symlinkPath, LoadOptions{})
	if err != nil {
		t.Fatalf("load symlink workspace: %v", err)
	}
	symlinkName, symlinkDir, err := ResolveWorkspaceContainer(symlinkCfg)
	if err != nil {
		t.Fatalf("resolve symlink workspace container: %v", err)
	}

	if symlinkName != realName || symlinkDir != realDir {
		t.Fatalf("expected symlinked workspace to reuse deterministic container, got %q %q want %q %q", symlinkName, symlinkDir, realName, realDir)
	}
	realProjectID, err := ProjectIDForWorkspaceRoot(realCfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("project id for real workspace: %v", err)
	}
	symlinkProjectID, err := ProjectIDForWorkspaceRoot(symlinkCfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("project id for symlink workspace: %v", err)
	}
	if symlinkProjectID != realProjectID {
		t.Fatalf("expected symlinked workspace to reuse project id, got %q want %q", symlinkProjectID, realProjectID)
	}
	if symlinkProjectID == symlinkName {
		t.Fatalf("expected project id to differ from workspace container name %q", symlinkName)
	}
}

func TestLoadReviewerPrecedenceAndValidation(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`[reviewer]
frequency = "all"
model = "gpt-file-reviewer"
thinking_level = "medium"
timeout_seconds = 45
verbose_output = true
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.Reviewer.Frequency != "all" {
		t.Fatalf("expected file reviewer.frequency=all, got %q", cfg.Settings.Reviewer.Frequency)
	}
	if got := cfg.Source.Sources["reviewer.frequency"]; got != "file" {
		t.Fatalf("expected reviewer.frequency source file, got %q", got)
	}
	if cfg.Settings.Reviewer.Model != "gpt-file-reviewer" {
		t.Fatalf("expected file reviewer.model, got %q", cfg.Settings.Reviewer.Model)
	}
	if got := cfg.Source.Sources["reviewer.model"]; got != "file" {
		t.Fatalf("expected reviewer.model source file, got %q", got)
	}
	if !cfg.Settings.Reviewer.VerboseOutput {
		t.Fatalf("expected file reviewer.verbose_output=true")
	}
	if got := cfg.Source.Sources["reviewer.verbose_output"]; got != "file" {
		t.Fatalf("expected reviewer.verbose_output source file, got %q", got)
	}

	t.Setenv("BUILDER_REVIEWER_FREQUENCY", "off")
	t.Setenv("BUILDER_REVIEWER_MODEL", "gpt-env-reviewer")
	t.Setenv("BUILDER_REVIEWER_THINKING_LEVEL", "high")
	t.Setenv("BUILDER_REVIEWER_TIMEOUT_SECONDS", "30")
	t.Setenv("BUILDER_REVIEWER_VERBOSE_OUTPUT", "false")

	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.Settings.Reviewer.Frequency != "off" {
		t.Fatalf("expected env reviewer.frequency=off, got %q", cfg.Settings.Reviewer.Frequency)
	}
	if got := cfg.Source.Sources["reviewer.frequency"]; got != "env" {
		t.Fatalf("expected reviewer.frequency source env, got %q", got)
	}
	if cfg.Settings.Reviewer.Model != "gpt-env-reviewer" {
		t.Fatalf("expected env reviewer.model, got %q", cfg.Settings.Reviewer.Model)
	}
	if got := cfg.Source.Sources["reviewer.model"]; got != "env" {
		t.Fatalf("expected reviewer.model source env, got %q", got)
	}
	if cfg.Settings.Reviewer.VerboseOutput {
		t.Fatalf("expected env reviewer.verbose_output=false")
	}
	if got := cfg.Source.Sources["reviewer.verbose_output"]; got != "env" {
		t.Fatalf("expected reviewer.verbose_output source env, got %q", got)
	}

	t.Setenv("BUILDER_REVIEWER_FREQUENCY", "sometimes")
	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid reviewer frequency")
	}
}

func TestLoadWebSearchPrecedenceAndValidation(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("web_search = \"native\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.WebSearch != "native" {
		t.Fatalf("expected file web_search=native, got %q", cfg.Settings.WebSearch)
	}
	if got := cfg.Source.Sources["web_search"]; got != "file" {
		t.Fatalf("expected web_search source file, got %q", got)
	}
	if !cfg.Settings.EnabledTools[toolspec.ToolWebSearch] {
		t.Fatalf("expected web_search tool to remain enabled by default")
	}

	t.Setenv("BUILDER_WEB_SEARCH", "off")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.Settings.WebSearch != "off" {
		t.Fatalf("expected env web_search=off, got %q", cfg.Settings.WebSearch)
	}
	if got := cfg.Source.Sources["web_search"]; got != "env" {
		t.Fatalf("expected web_search source env, got %q", got)
	}
	if !cfg.Settings.EnabledTools[toolspec.ToolWebSearch] {
		t.Fatalf("expected web_search tool to stay enabled when only web_search mode is off")
	}

	t.Setenv("BUILDER_WEB_SEARCH", "custom")
	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected web_search=custom validation error")
	}
}

func TestLoadWebSearchNativeRespectsExplicitToolToggle(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("web_search = \"native\"\n[tools]\nweb_search = false\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.EnabledTools[toolspec.ToolWebSearch] {
		t.Fatalf("expected explicit tools.web_search=false to stay disabled")
	}
	if got := cfg.Source.Sources["tools.web_search"]; got != "file" {
		t.Fatalf("expected tools.web_search source file, got %q", got)
	}
}

func TestLoadTriggerHandoffToolToggleFromFile(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("[tools]\ntrigger_handoff = true\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Settings.EnabledTools[toolspec.ToolTriggerHandoff] {
		t.Fatalf("expected explicit tools.trigger_handoff=true to enable the tool")
	}
	if got := cfg.Source.Sources["tools.trigger_handoff"]; got != "file" {
		t.Fatalf("expected tools.trigger_handoff source file, got %q", got)
	}
}

func TestLoadSkillTogglesFromFile(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("[skills]\nApiResult = false\n\"Local Helper\" = true\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.SkillToggles["apiresult"] {
		t.Fatalf("expected apiresult skill to be explicitly disabled, got %+v", cfg.Settings.SkillToggles)
	}
	if !cfg.Settings.SkillToggles["local helper"] {
		t.Fatalf("expected quoted skill key to stay enabled, got %+v", cfg.Settings.SkillToggles)
	}
	if got := cfg.Source.Sources["skills.apiresult"]; got != "file" {
		t.Fatalf("expected skills.apiresult source file, got %q", got)
	}
	if got := cfg.Source.Sources["skills.local helper"]; got != "file" {
		t.Fatalf("expected skills.local helper source file, got %q", got)
	}
}

func TestLoadRejectsNonBooleanSkillToggle(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("[skills]\napiresult = \"off\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid skills type error")
	} else if !strings.Contains(err.Error(), "skills.apiresult") {
		t.Fatalf("expected skills.apiresult in error, got %v", err)
	}
}

func TestLoadRejectsDuplicateNormalizedSkillToggleKeys(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("[skills]\nApiResult = false\napiresult = true\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected duplicate normalized skills key error")
	} else {
		for _, want := range []string{"ApiResult", "apiresult", "both normalize to"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("expected %q in error, got %v", want, err)
			}
		}
	}
}

func TestLoadNotificationMethodPrecedenceAndValidation(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("notification_method = \"bel\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.NotificationMethod != "bel" {
		t.Fatalf("expected file notification_method=bel, got %q", cfg.Settings.NotificationMethod)
	}
	if got := cfg.Source.Sources["notification_method"]; got != "file" {
		t.Fatalf("expected notification_method source file, got %q", got)
	}

	t.Setenv("BUILDER_NOTIFICATION_METHOD", "osc9")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.Settings.NotificationMethod != "osc9" {
		t.Fatalf("expected env notification_method=osc9, got %q", cfg.Settings.NotificationMethod)
	}
	if got := cfg.Source.Sources["notification_method"]; got != "env" {
		t.Fatalf("expected notification_method source env, got %q", got)
	}

	t.Setenv("BUILDER_NOTIFICATION_METHOD", "bad")
	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid notification_method validation error")
	}
}

func TestLoadToolPreamblesPrecedence(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("tool_preambles = false\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.ToolPreambles {
		t.Fatalf("expected file tool_preambles=false")
	}
	if got := cfg.Source.Sources["tool_preambles"]; got != "file" {
		t.Fatalf("expected tool_preambles source file, got %q", got)
	}

	t.Setenv("BUILDER_TOOL_PREAMBLES", "true")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if !cfg.Settings.ToolPreambles {
		t.Fatalf("expected env tool_preambles=true")
	}
	if got := cfg.Source.Sources["tool_preambles"]; got != "env" {
		t.Fatalf("expected tool_preambles source env, got %q", got)
	}

	t.Setenv("BUILDER_TOOL_PREAMBLES", "broken")
	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid BUILDER_TOOL_PREAMBLES error")
	}
}

func TestLoadTUIAlternateScreenPrecedenceAndValidation(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("tui_alternate_screen = \"always\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.TUIAlternateScreen != TUIAlternateScreenAlways {
		t.Fatalf("expected file tui_alternate_screen=always, got %q", cfg.Settings.TUIAlternateScreen)
	}
	if got := cfg.Source.Sources["tui_alternate_screen"]; got != "file" {
		t.Fatalf("expected tui_alternate_screen source file, got %q", got)
	}

	if err := os.WriteFile(configPath, []byte("tui_alternate_screen = \"Always\"\n"), 0o644); err != nil {
		t.Fatalf("write config mixed case: %v", err)
	}
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load mixed-case file value: %v", err)
	}
	if cfg.Settings.TUIAlternateScreen != TUIAlternateScreenAlways {
		t.Fatalf("expected mixed-case file value normalized to always, got %q", cfg.Settings.TUIAlternateScreen)
	}

	t.Setenv("BUILDER_TUI_ALTERNATE_SCREEN", "never")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.Settings.TUIAlternateScreen != TUIAlternateScreenNever {
		t.Fatalf("expected env tui_alternate_screen=never, got %q", cfg.Settings.TUIAlternateScreen)
	}
	if got := cfg.Source.Sources["tui_alternate_screen"]; got != "env" {
		t.Fatalf("expected tui_alternate_screen source env, got %q", got)
	}

	t.Setenv("BUILDER_TUI_ALTERNATE_SCREEN", "NEVER")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load mixed-case env value: %v", err)
	}
	if cfg.Settings.TUIAlternateScreen != TUIAlternateScreenNever {
		t.Fatalf("expected mixed-case env value normalized to never, got %q", cfg.Settings.TUIAlternateScreen)
	}

	t.Setenv("BUILDER_TUI_ALTERNATE_SCREEN", "broken")
	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid tui_alternate_screen validation error")
	}
}

func TestLoadPrecedenceCLIOverEnvOverFile(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`model = "gpt-file"
thinking_level = "low"
theme = "light"

[tools]
shell = true
patch = false
ask_question = true

[timeouts]
model_request_seconds = 45
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("BUILDER_MODEL", "gpt-env")
	t.Setenv("BUILDER_THINKING_LEVEL", "medium")
	t.Setenv("BUILDER_TOOLS", "shell,patch")

	cfg, err := Load(workspace, LoadOptions{Model: "gpt-cli", ThinkingLevel: "xhigh"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.Model != "gpt-cli" {
		t.Fatalf("expected cli model, got %q", cfg.Settings.Model)
	}
	if cfg.Settings.ThinkingLevel != "xhigh" {
		t.Fatalf("expected cli thinking_level, got %q", cfg.Settings.ThinkingLevel)
	}
	if !cfg.Settings.EnabledTools[toolspec.ToolPatch] {
		t.Fatalf("expected env tool override to enable patch")
	}
	if got := cfg.Source.Sources["model"]; got != "cli" {
		t.Fatalf("expected model source cli, got %q", got)
	}
	if got := cfg.Source.Sources["thinking_level"]; got != "cli" {
		t.Fatalf("expected thinking_level source cli, got %q", got)
	}
}

func TestLoadRejectsUnknownLegacyTimeoutSettingNames(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`[timeouts]
bash_default_seconds = 42
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected unknown bash_default_seconds settings key error")
	}
}

func TestLoadShellOutputMaxCharsPrecedenceAndValidation(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("shell_output_max_chars = 12000\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.ShellOutputMaxChars != 12000 {
		t.Fatalf("expected file shell_output_max_chars=12000, got %d", cfg.Settings.ShellOutputMaxChars)
	}
	if got := cfg.Source.Sources["shell_output_max_chars"]; got != "file" {
		t.Fatalf("expected shell_output_max_chars source file, got %q", got)
	}

	t.Setenv("BUILDER_SHELL_OUTPUT_MAX_CHARS", "18000")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.Settings.ShellOutputMaxChars != 18000 {
		t.Fatalf("expected env shell_output_max_chars=18000, got %d", cfg.Settings.ShellOutputMaxChars)
	}
	if got := cfg.Source.Sources["shell_output_max_chars"]; got != "env" {
		t.Fatalf("expected shell_output_max_chars source env, got %q", got)
	}

	t.Setenv("BUILDER_SHELL_OUTPUT_MAX_CHARS", "0")
	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid shell_output_max_chars")
	}
}

func TestLoadMinimumExecToBgSecondsPrecedenceAndValidation(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("minimum_exec_to_bg_seconds = 21\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.MinimumExecToBgSeconds != 21 {
		t.Fatalf("expected file minimum_exec_to_bg_seconds=21, got %d", cfg.Settings.MinimumExecToBgSeconds)
	}
	if got := cfg.Source.Sources["minimum_exec_to_bg_seconds"]; got != "file" {
		t.Fatalf("expected minimum_exec_to_bg_seconds source file, got %q", got)
	}

	t.Setenv("BUILDER_MINIMUM_EXEC_TO_BG_SECONDS", "18")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.Settings.MinimumExecToBgSeconds != 18 {
		t.Fatalf("expected env minimum_exec_to_bg_seconds=18, got %d", cfg.Settings.MinimumExecToBgSeconds)
	}
	if got := cfg.Source.Sources["minimum_exec_to_bg_seconds"]; got != "env" {
		t.Fatalf("expected minimum_exec_to_bg_seconds source env, got %q", got)
	}

	t.Setenv("BUILDER_MINIMUM_EXEC_TO_BG_SECONDS", "0")
	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid minimum_exec_to_bg_seconds")
	}
}

func TestLoadBGShellsOutputPrecedenceAndValidation(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("bg_shells_output = \"concise\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.BGShellsOutput != BGShellsOutputConcise {
		t.Fatalf("expected file bg_shells_output=concise, got %q", cfg.Settings.BGShellsOutput)
	}
	if got := cfg.Source.Sources["bg_shells_output"]; got != "file" {
		t.Fatalf("expected bg_shells_output source file, got %q", got)
	}

	t.Setenv("BUILDER_BG_SHELLS_OUTPUT", "verbose")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.Settings.BGShellsOutput != BGShellsOutputVerbose {
		t.Fatalf("expected env bg_shells_output=verbose, got %q", cfg.Settings.BGShellsOutput)
	}
	if got := cfg.Source.Sources["bg_shells_output"]; got != "env" {
		t.Fatalf("expected bg_shells_output source env, got %q", got)
	}

	t.Setenv("BUILDER_BG_SHELLS_OUTPUT", "loud")
	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid bg_shells_output")
	}
}

func TestLoadShellPostprocessingPrecedenceAndValidation(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("[shell]\npostprocessing_mode = \"all\"\npostprocess_hook = \"/tmp/file-hook\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.Shell.PostprocessingMode != ShellPostprocessingModeAll {
		t.Fatalf("expected file shell.postprocessing_mode=all, got %q", cfg.Settings.Shell.PostprocessingMode)
	}
	if cfg.Settings.Shell.PostprocessHook != "/tmp/file-hook" {
		t.Fatalf("expected file shell.postprocess_hook, got %q", cfg.Settings.Shell.PostprocessHook)
	}
	if got := cfg.Source.Sources["shell.postprocessing_mode"]; got != "file" {
		t.Fatalf("expected shell.postprocessing_mode source file, got %q", got)
	}
	if got := cfg.Source.Sources["shell.postprocess_hook"]; got != "file" {
		t.Fatalf("expected shell.postprocess_hook source file, got %q", got)
	}

	t.Setenv("BUILDER_SHELL_POSTPROCESSING_MODE", "user")
	t.Setenv("BUILDER_SHELL_POSTPROCESS_HOOK", "/tmp/env-hook")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.Settings.Shell.PostprocessingMode != ShellPostprocessingModeUser {
		t.Fatalf("expected env shell.postprocessing_mode=user, got %q", cfg.Settings.Shell.PostprocessingMode)
	}
	if cfg.Settings.Shell.PostprocessHook != "/tmp/env-hook" {
		t.Fatalf("expected env shell.postprocess_hook, got %q", cfg.Settings.Shell.PostprocessHook)
	}
	if got := cfg.Source.Sources["shell.postprocessing_mode"]; got != "env" {
		t.Fatalf("expected shell.postprocessing_mode source env, got %q", got)
	}
	if got := cfg.Source.Sources["shell.postprocess_hook"]; got != "env" {
		t.Fatalf("expected shell.postprocess_hook source env, got %q", got)
	}

	t.Setenv("BUILDER_SHELL_POSTPROCESSING_MODE", "broken")
	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid shell.postprocessing_mode")
	}
}

func TestLoadAcceptsCustomThinkingLevel(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BUILDER_THINKING_LEVEL", "ultra")

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.ThinkingLevel != "ultra" {
		t.Fatalf("expected custom thinking level preserved, got %q", cfg.Settings.ThinkingLevel)
	}
}

func TestLoadExpandsTildePersistenceRootFromEnv(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BUILDER_PERSISTENCE_ROOT", "~/.builder-custom")

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.PersistenceRoot; got != filepath.Join(home, ".builder-custom") {
		t.Fatalf("expanded persistence root mismatch: %q", got)
	}
}

func TestLoadOpenAIBaseURLPrecedence(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`openai_base_url = "http://file.local/v1"`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("BUILDER_OPENAI_BASE_URL", "http://env.local/v1")
	cfg, err := Load(workspace, LoadOptions{OpenAIBaseURL: "http://cli.local/v1"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.OpenAIBaseURL != "http://cli.local/v1" {
		t.Fatalf("expected cli openai base url, got %q", cfg.Settings.OpenAIBaseURL)
	}
	if got := cfg.Source.Sources["openai_base_url"]; got != "cli" {
		t.Fatalf("expected openai_base_url source cli, got %q", got)
	}
}

func TestNormalizeSettingsForPersistence_AllowsDisabledThinkingWithReviewerInheritance(t *testing.T) {
	settings := defaultSettings()
	settings.Model = "gpt-5.4"
	settings.ThinkingLevel = ""
	settings.Reviewer = ReviewerSettings{
		Frequency:      "edits",
		Model:          "",
		ThinkingLevel:  "",
		TimeoutSeconds: defaultReviewerTimeoutSec,
		VerboseOutput:  false,
	}

	normalized, err := NormalizeSettingsForPersistence(settings)
	if err != nil {
		t.Fatalf("normalize settings for persistence: %v", err)
	}
	if normalized.Reviewer.Model != "gpt-5.4" {
		t.Fatalf("expected reviewer model to inherit main model, got %q", normalized.Reviewer.Model)
	}
	if normalized.Reviewer.ThinkingLevel != "" {
		t.Fatalf("expected reviewer thinking to stay disabled, got %q", normalized.Reviewer.ThinkingLevel)
	}
}

func TestNormalizeSettingsForPersistence_AllowsProviderOverrideWithExplicitPersistedModel(t *testing.T) {
	settings := defaultSettings()
	settings.Model = "my-team-alias"
	settings.ProviderOverride = "openai"

	normalized, err := NormalizeSettingsForPersistence(settings)
	if err != nil {
		t.Fatalf("normalize settings for persistence: %v", err)
	}
	if normalized.ProviderOverride != "openai" {
		t.Fatalf("expected provider_override preserved, got %q", normalized.ProviderOverride)
	}
}

func TestLoadCanonicalTimeoutEnvAndSourceKeys(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BUILDER_TIMEOUTS_MODEL_REQUEST_SECONDS", "123")
	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.Timeouts.ModelRequestSeconds != 123 {
		t.Fatalf("expected canonical env model timeout, got %d", cfg.Settings.Timeouts.ModelRequestSeconds)
	}
	if got := cfg.Source.Sources["timeouts.model_request_seconds"]; got != "env" {
		t.Fatalf("expected timeouts.model_request_seconds source env, got %q", got)
	}
}

func TestLoadStorePrecedence(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`store = true`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Settings.Store {
		t.Fatalf("expected file store=true")
	}
	if got := cfg.Source.Sources["store"]; got != "file" {
		t.Fatalf("expected store source file, got %q", got)
	}

	t.Setenv("BUILDER_STORE", "false")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.Settings.Store {
		t.Fatalf("expected env store=false")
	}
	if got := cfg.Source.Sources["store"]; got != "env" {
		t.Fatalf("expected store source env, got %q", got)
	}
}

func TestLoadIgnoresUnknownBuilderEnvVars(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BUILDER_PROVIDER_CAPABILITY_ID", "custom-provider")
	t.Setenv("BUILDER_MODEL_SUPPORTS_REASONING_EFFORT", "true")
	t.Setenv("BUILDER_MODEL_TIMEOUT_SECONDS", "123")
	t.Setenv("BUILDER_USE_NATIVE_COMPACTION", "true")
	t.Setenv("BUILDER_REVIEWER_MAX_SUGGESTIONS", "15")

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.ModelCapabilities.SupportsReasoningEffort {
		t.Fatal("expected unknown legacy env vars to be ignored")
	}
	if cfg.Settings.Timeouts.ModelRequestSeconds != defaultModelTimeoutSeconds {
		t.Fatalf("expected unknown legacy env vars not to affect model timeout, got %d", cfg.Settings.Timeouts.ModelRequestSeconds)
	}
	if cfg.Settings.CompactionMode != CompactionModeLocal {
		t.Fatalf("expected unknown legacy env vars not to affect compaction mode, got %q", cfg.Settings.CompactionMode)
	}
}

func TestLoadRejectsRemovedReviewerMaxSuggestionsFileKey(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("[reviewer]\nmax_suggestions = 15\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected removed reviewer.max_suggestions file key to be rejected")
	}
}

func TestLoadAllowNonCwdEditsPrecedence(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`allow_non_cwd_edits = true`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Settings.AllowNonCwdEdits {
		t.Fatalf("expected file allow_non_cwd_edits=true")
	}
	if got := cfg.Source.Sources["allow_non_cwd_edits"]; got != "file" {
		t.Fatalf("expected allow_non_cwd_edits source file, got %q", got)
	}

	t.Setenv("BUILDER_ALLOW_NON_CWD_EDITS", "false")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.Settings.AllowNonCwdEdits {
		t.Fatalf("expected env allow_non_cwd_edits=false")
	}
	if got := cfg.Source.Sources["allow_non_cwd_edits"]; got != "env" {
		t.Fatalf("expected allow_non_cwd_edits source env, got %q", got)
	}
}

func TestLoadDebugPrecedenceAndValidation(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("debug = true\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Settings.Debug {
		t.Fatalf("expected file debug=true")
	}
	if got := cfg.Source.Sources["debug"]; got != "file" {
		t.Fatalf("expected debug source file, got %q", got)
	}

	t.Setenv("BUILDER_DEBUG", "false")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.Settings.Debug {
		t.Fatalf("expected env debug=false")
	}
	if got := cfg.Source.Sources["debug"]; got != "env" {
		t.Fatalf("expected debug source env, got %q", got)
	}

	t.Setenv("BUILDER_DEBUG", "broken")
	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid BUILDER_DEBUG error")
	}
}

func TestLoadServerHostPortPrecedenceAndValidation(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("server_host = \"127.0.0.2\"\nserver_port = 54321\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.ServerHost != "127.0.0.2" || cfg.Settings.ServerPort != 54321 {
		t.Fatalf("unexpected server settings from file: host=%q port=%d", cfg.Settings.ServerHost, cfg.Settings.ServerPort)
	}
	if got := cfg.Source.Sources["server_host"]; got != "file" {
		t.Fatalf("expected server_host source file, got %q", got)
	}
	if got := cfg.Source.Sources["server_port"]; got != "file" {
		t.Fatalf("expected server_port source file, got %q", got)
	}

	t.Setenv("BUILDER_SERVER_HOST", "::1")
	t.Setenv("BUILDER_SERVER_PORT", "65432")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.Settings.ServerHost != "::1" || cfg.Settings.ServerPort != 65432 {
		t.Fatalf("unexpected server settings from env: host=%q port=%d", cfg.Settings.ServerHost, cfg.Settings.ServerPort)
	}
	if got := cfg.Source.Sources["server_host"]; got != "env" {
		t.Fatalf("expected server_host source env, got %q", got)
	}
	if got := cfg.Source.Sources["server_port"]; got != "env" {
		t.Fatalf("expected server_port source env, got %q", got)
	}
	if got := ServerListenAddress(cfg); got != "[::1]:65432" {
		t.Fatalf("ServerListenAddress = %q, want [::1]:65432", got)
	}
	if got := ServerHTTPBaseURL(cfg); got != "http://[::1]:65432" {
		t.Fatalf("ServerHTTPBaseURL = %q, want http://[::1]:65432", got)
	}
	if got := ServerRPCURL(cfg); got != "ws://[::1]:65432/rpc" {
		t.Fatalf("ServerRPCURL = %q, want ws://[::1]:65432/rpc", got)
	}

	t.Setenv("BUILDER_SERVER_PORT", "broken")
	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid BUILDER_SERVER_PORT error")
	}
}

func TestLoadContextCompactionThresholdPrecedence(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`context_compaction_threshold_tokens = 123456`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("BUILDER_CONTEXT_COMPACTION_THRESHOLD_TOKENS", "234567")
	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.ContextCompactionThresholdTokens != 234567 {
		t.Fatalf("expected env threshold override, got %d", cfg.Settings.ContextCompactionThresholdTokens)
	}
	if got := cfg.Source.Sources["context_compaction_threshold_tokens"]; got != "env" {
		t.Fatalf("expected threshold source env, got %q", got)
	}
}

func TestLoadCompactionModePrecedence(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("compaction_mode = \"local\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.CompactionMode != CompactionModeLocal {
		t.Fatalf("expected file override compaction_mode=local, got %q", cfg.Settings.CompactionMode)
	}
	if got := cfg.Source.Sources["compaction_mode"]; got != "file" {
		t.Fatalf("expected compaction_mode source file, got %q", got)
	}

	t.Setenv("BUILDER_COMPACTION_MODE", "none")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.Settings.CompactionMode != CompactionModeNone {
		t.Fatalf("expected env override compaction_mode=none, got %q", cfg.Settings.CompactionMode)
	}
	if got := cfg.Source.Sources["compaction_mode"]; got != "env" {
		t.Fatalf("expected compaction_mode source env, got %q", got)
	}
}

func TestLoadRejectsRemovedUseNativeCompactionSetting(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("use_native_compaction = true\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected unsupported use_native_compaction settings key error")
	}
}

func TestLoadRejectsUnrelatedUnknownSettingKeys(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model = \"gpt-5\"\nfoo = 1\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected unknown settings key error")
	} else if !strings.Contains(err.Error(), "foo") {
		t.Fatalf("expected unknown key name in error, got %v", err)
	}
}

func TestLoadRejectsInvalidCompactionMode(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("compaction_mode = \"remote\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid compaction_mode validation error")
	}
}

func TestLoadModelContextWindowPrecedence(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model_context_window = 350000\ncontext_compaction_threshold_tokens = 250000\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("BUILDER_MODEL_CONTEXT_WINDOW", "420000")
	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.ModelContextWindow != 420000 {
		t.Fatalf("expected env model context window override, got %d", cfg.Settings.ModelContextWindow)
	}
	if got := cfg.Source.Sources["model_context_window"]; got != "env" {
		t.Fatalf("expected model_context_window source env, got %q", got)
	}
}

func TestLoadRejectsCompactionThresholdNotBelowContextWindow(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model_context_window = 300000\ncontext_compaction_threshold_tokens = 300000\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected threshold/window validation error")
	}
}

func TestLoadRejectsCompactionThresholdBelowHalfWindow(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model_context_window = 300000\ncontext_compaction_threshold_tokens = 149999\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected threshold minimum-window-percent validation error")
	} else if !strings.Contains(err.Error(), "context_compaction_threshold_tokens must be >= 150000") {
		t.Fatalf("expected threshold minimum-window-percent validation detail, got %v", err)
	}
}

func TestLoadRejectsPreSubmitLeadBandBelowHalfWindow(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model_context_window = 300000\ncontext_compaction_threshold_tokens = 200000\npre_submit_compaction_lead_tokens = 100000\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected pre-submit effective threshold validation error")
	} else if !strings.Contains(err.Error(), "effective pre-submit threshold 100000, below 150000") {
		t.Fatalf("expected pre-submit effective threshold validation detail, got %v", err)
	}
}
