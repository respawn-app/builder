package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"builder/internal/tools"
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
	if cfg.Settings.TUIAlternateScreen != TUIAlternateScreenAuto {
		t.Fatalf("default tui_alternate_screen mismatch: %q", cfg.Settings.TUIAlternateScreen)
	}
	if got := cfg.PersistenceRoot; got != filepath.Join(home, ".builder") {
		t.Fatalf("default persistence root mismatch: %q", got)
	}
	if _, err := os.Stat(filepath.Join(cfg.PersistenceRoot, sessionsDirName)); err != nil {
		t.Fatalf("expected sessions root to exist: %v", err)
	}
	if !cfg.Settings.EnabledTools[tools.ToolShell] || !cfg.Settings.EnabledTools[tools.ToolViewImage] || !cfg.Settings.EnabledTools[tools.ToolPatch] || !cfg.Settings.EnabledTools[tools.ToolAskQuestion] {
		t.Fatalf("expected all default tools enabled: %+v", cfg.Settings.EnabledTools)
	}
	if cfg.Settings.EnabledTools[tools.ToolMultiToolUseParallel] {
		t.Fatalf("expected %s disabled in static defaults; it should be derived from model capability", tools.ToolMultiToolUseParallel)
	}
	if got := cfg.Source.Sources["tools.multi_tool_use_parallel"]; got != "default" {
		t.Fatalf("expected untouched %s source to remain default, got %q", tools.ToolMultiToolUseParallel, got)
	}
	if !cfg.Settings.EnabledTools[tools.ToolWebSearch] {
		t.Fatalf("expected web_search tool enabled by default: %+v", cfg.Settings.EnabledTools)
	}
	if cfg.Settings.ContextCompactionThresholdTokens != defaultCompactionThreshold {
		t.Fatalf("default compaction threshold mismatch: %d", cfg.Settings.ContextCompactionThresholdTokens)
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
	if !strings.Contains(string(settingsBytes), "model_verbosity = \"medium\"") {
		t.Fatalf("expected default config to expose model_verbosity option, got %q", string(settingsBytes))
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
	if !strings.Contains(string(settingsBytes), "# [skills]") {
		t.Fatalf("expected default config to mention skills toggles, got %q", string(settingsBytes))
	}
	if strings.Contains(string(settingsBytes), "[tools]") {
		t.Fatalf("expected default config to omit [tools] section entirely, got %q", string(settingsBytes))
	}
	if strings.Contains(string(settingsBytes), "This JSON block mirrors") {
		t.Fatalf("expected default config to omit mirrored JSON block, got %q", string(settingsBytes))
	}
}

func TestSettingsTOMLOmitsDefaultAssignmentsForOnboarding(t *testing.T) {
	toml := settingsTOML(defaultSettings())
	if strings.Contains(toml, "theme =") {
		t.Fatalf("expected onboarding config to omit auto theme default, got %q", toml)
	}
	for _, forbidden := range []string{
		"provider_override = \"\"",
		"openai_base_url = \"\"",
		"This JSON block mirrors",
		"[reviewer]",
		"[timeouts]",
	} {
		if strings.Contains(toml, forbidden) {
			t.Fatalf("expected onboarding config to omit %q, got %q", forbidden, toml)
		}
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
	if cfg.Settings.ProviderCapabilities.ProviderID != "custom-provider" || !cfg.Settings.ProviderCapabilities.SupportsResponsesAPI || !cfg.Settings.ProviderCapabilities.SupportsNativeWebSearch {
		t.Fatalf("expected provider capability overrides from file, got %+v", cfg.Settings.ProviderCapabilities)
	}
	if got := cfg.Source.Sources["model_capabilities.supports_reasoning_effort"]; got != "file" {
		t.Fatalf("expected model_capabilities.supports_reasoning_effort source file, got %q", got)
	}
	if got := cfg.Source.Sources["provider_capabilities.provider_id"]; got != "file" {
		t.Fatalf("expected provider_capabilities.provider_id source file, got %q", got)
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
	if cfg.Settings.ProviderCapabilities.ProviderID != "custom-provider" || !cfg.Settings.ProviderCapabilities.SupportsResponsesAPI || !cfg.Settings.ProviderCapabilities.SupportsNativeWebSearch {
		t.Fatalf("expected provider capability overrides from env, got %+v", cfg.Settings.ProviderCapabilities)
	}
	if got := cfg.Source.Sources["model_capabilities.supports_reasoning_effort"]; got != "env" {
		t.Fatalf("expected model_capabilities.supports_reasoning_effort source env, got %q", got)
	}
	if got := cfg.Source.Sources["provider_capabilities.provider_id"]; got != "env" {
		t.Fatalf("expected provider_capabilities.provider_id source env, got %q", got)
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
	if !cfg.Settings.EnabledTools[tools.ToolWebSearch] {
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
	if !cfg.Settings.EnabledTools[tools.ToolWebSearch] {
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
	if cfg.Settings.EnabledTools[tools.ToolWebSearch] {
		t.Fatalf("expected explicit tools.web_search=false to stay disabled")
	}
	if got := cfg.Source.Sources["tools.web_search"]; got != "file" {
		t.Fatalf("expected tools.web_search source file, got %q", got)
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
shell_default_seconds = 50
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
	if !cfg.Settings.EnabledTools[tools.ToolPatch] {
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
	t.Setenv("BUILDER_TIMEOUTS_SHELL_DEFAULT_SECONDS", "234")

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.Timeouts.ModelRequestSeconds != 123 {
		t.Fatalf("expected canonical env model timeout, got %d", cfg.Settings.Timeouts.ModelRequestSeconds)
	}
	if cfg.Settings.Timeouts.ShellDefaultSeconds != 234 {
		t.Fatalf("expected canonical env shell timeout, got %d", cfg.Settings.Timeouts.ShellDefaultSeconds)
	}
	if got := cfg.Source.Sources["timeouts.model_request_seconds"]; got != "env" {
		t.Fatalf("expected timeouts.model_request_seconds source env, got %q", got)
	}
	if got := cfg.Source.Sources["timeouts.shell_default_seconds"]; got != "env" {
		t.Fatalf("expected timeouts.shell_default_seconds source env, got %q", got)
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
