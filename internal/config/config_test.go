package config

import (
	"os"
	"path/filepath"
	"testing"

	"builder/internal/tools"
)

func TestLoadCreatesDefaultConfigOnFirstUse(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	settingsPath := filepath.Join(home, ".builder", "config.toml")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("expected config file to exist: %v", err)
	}
	if !cfg.Source.CreatedDefaultConfig {
		t.Fatalf("expected CreatedDefaultConfig=true")
	}
	if cfg.Settings.Model != defaultModel {
		t.Fatalf("default model mismatch: %q", cfg.Settings.Model)
	}
	if cfg.Settings.WebSearch != "off" {
		t.Fatalf("default web_search mismatch: %q", cfg.Settings.WebSearch)
	}
	if cfg.Settings.NotificationMethod != "auto" {
		t.Fatalf("default notification_method mismatch: %q", cfg.Settings.NotificationMethod)
	}
	if got := cfg.PersistenceRoot; got != filepath.Join(home, ".builder") {
		t.Fatalf("default persistence root mismatch: %q", got)
	}
	if !cfg.Settings.EnabledTools[tools.ToolShell] || !cfg.Settings.EnabledTools[tools.ToolPatch] || !cfg.Settings.EnabledTools[tools.ToolAskQuestion] || !cfg.Settings.EnabledTools[tools.ToolMultiToolUseParallel] {
		t.Fatalf("expected all default tools enabled: %+v", cfg.Settings.EnabledTools)
	}
	if cfg.Settings.ContextCompactionThresholdTokens != 360_000 {
		t.Fatalf("default compaction threshold mismatch: %d", cfg.Settings.ContextCompactionThresholdTokens)
	}
	if cfg.Settings.ModelContextWindow != 400_000 {
		t.Fatalf("default model context window mismatch: %d", cfg.Settings.ModelContextWindow)
	}
	if cfg.Settings.Store {
		t.Fatalf("expected default store=false")
	}
	if cfg.Settings.AllowNonCwdEdits {
		t.Fatalf("expected default allow_non_cwd_edits=false")
	}
	if !cfg.Settings.UseNativeCompaction {
		t.Fatalf("expected default use_native_compaction=true")
	}
	if cfg.Settings.Reviewer.Enabled {
		t.Fatalf("expected default reviewer.enabled=false")
	}
	if cfg.Settings.Reviewer.Model != "gpt-5-mini" {
		t.Fatalf("default reviewer model mismatch: %q", cfg.Settings.Reviewer.Model)
	}
	if cfg.Settings.Reviewer.ThinkingLevel != "low" {
		t.Fatalf("default reviewer thinking_level mismatch: %q", cfg.Settings.Reviewer.ThinkingLevel)
	}
	if cfg.Settings.Reviewer.TimeoutSeconds != 60 {
		t.Fatalf("default reviewer timeout mismatch: %d", cfg.Settings.Reviewer.TimeoutSeconds)
	}
	if cfg.Settings.Reviewer.MaxSuggestions != 5 {
		t.Fatalf("default reviewer max_suggestions mismatch: %d", cfg.Settings.Reviewer.MaxSuggestions)
	}
	if cfg.Settings.Reviewer.MaxToolOutputChars != 1200 {
		t.Fatalf("default reviewer max_tool_output_chars mismatch: %d", cfg.Settings.Reviewer.MaxToolOutputChars)
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
enabled = true
model = "gpt-file-reviewer"
thinking_level = "medium"
timeout_seconds = 45
max_suggestions = 3
max_tool_output_chars = 900
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Settings.Reviewer.Enabled {
		t.Fatalf("expected file reviewer.enabled=true")
	}
	if cfg.Settings.Reviewer.Model != "gpt-file-reviewer" {
		t.Fatalf("expected file reviewer.model, got %q", cfg.Settings.Reviewer.Model)
	}
	if got := cfg.Source.Sources["reviewer.model"]; got != "file" {
		t.Fatalf("expected reviewer.model source file, got %q", got)
	}

	t.Setenv("BUILDER_REVIEWER_ENABLED", "false")
	t.Setenv("BUILDER_REVIEWER_MODEL", "gpt-env-reviewer")
	t.Setenv("BUILDER_REVIEWER_THINKING_LEVEL", "high")
	t.Setenv("BUILDER_REVIEWER_TIMEOUT_SECONDS", "30")
	t.Setenv("BUILDER_REVIEWER_MAX_SUGGESTIONS", "4")
	t.Setenv("BUILDER_REVIEWER_MAX_TOOL_OUTPUT_CHARS", "700")

	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.Settings.Reviewer.Enabled {
		t.Fatalf("expected env reviewer.enabled=false")
	}
	if cfg.Settings.Reviewer.Model != "gpt-env-reviewer" {
		t.Fatalf("expected env reviewer.model, got %q", cfg.Settings.Reviewer.Model)
	}
	if got := cfg.Source.Sources["reviewer.model"]; got != "env" {
		t.Fatalf("expected reviewer.model source env, got %q", got)
	}

	t.Setenv("BUILDER_REVIEWER_MAX_SUGGESTIONS", "0")
	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid reviewer max suggestions")
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

	t.Setenv("BUILDER_WEB_SEARCH", "custom")
	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected web_search=custom validation error")
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
	t.Setenv("BUILDER_TOOLS", "shell,patch")

	cfg, err := Load(workspace, LoadOptions{Model: "gpt-cli"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.Model != "gpt-cli" {
		t.Fatalf("expected cli model, got %q", cfg.Settings.Model)
	}
	if !cfg.Settings.EnabledTools[tools.ToolPatch] {
		t.Fatalf("expected env tool override to enable patch")
	}
	if got := cfg.Source.Sources["model"]; got != "cli" {
		t.Fatalf("expected model source cli, got %q", got)
	}
}

func TestLoadSupportsLegacyBashTimeoutSettingNames(t *testing.T) {
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

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.Timeouts.ShellDefaultSeconds != 42 {
		t.Fatalf("legacy bash timeout was not mapped, got %d", cfg.Settings.Timeouts.ShellDefaultSeconds)
	}

	t.Setenv("BUILDER_SHELL_TIMEOUT_SECONDS", "")
	t.Setenv("BUILDER_BASH_TIMEOUT_SECONDS", "51")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with legacy env: %v", err)
	}
	if cfg.Settings.Timeouts.ShellDefaultSeconds != 51 {
		t.Fatalf("legacy bash env timeout was not mapped, got %d", cfg.Settings.Timeouts.ShellDefaultSeconds)
	}
}

func TestLoadRejectsInvalidThinkingLevel(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BUILDER_THINKING_LEVEL", "ultra")

	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid thinking level error")
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

func TestLoadUseNativeCompactionPrecedence(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("use_native_compaction = false\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.UseNativeCompaction {
		t.Fatalf("expected file override use_native_compaction=false")
	}
	if got := cfg.Source.Sources["use_native_compaction"]; got != "file" {
		t.Fatalf("expected use_native_compaction source file, got %q", got)
	}

	t.Setenv("BUILDER_USE_NATIVE_COMPACTION", "true")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if !cfg.Settings.UseNativeCompaction {
		t.Fatalf("expected env override use_native_compaction=true")
	}
	if got := cfg.Source.Sources["use_native_compaction"]; got != "env" {
		t.Fatalf("expected use_native_compaction source env, got %q", got)
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
