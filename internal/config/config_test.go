package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"builder/internal/tools"
)

func TestMain(m *testing.M) {
	_ = os.Unsetenv("BUILDER_TUI_SCROLL_MODE")
	os.Exit(m.Run())
}

func TestLoadCreatesDefaultConfigOnFirstUse(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BUILDER_TUI_SCROLL_MODE", "")

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
	if !cfg.Settings.ToolPreambles {
		t.Fatalf("expected default tool_preambles=true")
	}
	if cfg.Settings.PriorityRequestMode {
		t.Fatalf("expected default priority_request_mode=false")
	}
	if cfg.Settings.TUIAlternateScreen != TUIAlternateScreenAuto {
		t.Fatalf("default tui_alternate_screen mismatch: %q", cfg.Settings.TUIAlternateScreen)
	}
	if cfg.Settings.TUIScrollMode != TUIScrollModeAlt {
		t.Fatalf("default tui_scroll_mode mismatch: %q", cfg.Settings.TUIScrollMode)
	}
	if got := cfg.PersistenceRoot; got != filepath.Join(home, ".builder") {
		t.Fatalf("default persistence root mismatch: %q", got)
	}
	if _, err := os.Stat(filepath.Join(cfg.PersistenceRoot, sessionsDirName)); err != nil {
		t.Fatalf("expected sessions root to exist: %v", err)
	}
	if !cfg.Settings.EnabledTools[tools.ToolShell] || !cfg.Settings.EnabledTools[tools.ToolViewImage] || !cfg.Settings.EnabledTools[tools.ToolPatch] || !cfg.Settings.EnabledTools[tools.ToolAskQuestion] || !cfg.Settings.EnabledTools[tools.ToolMultiToolUseParallel] {
		t.Fatalf("expected all default tools enabled: %+v", cfg.Settings.EnabledTools)
	}
	if !cfg.Settings.EnabledTools[tools.ToolWebSearch] {
		t.Fatalf("expected web_search tool enabled by default: %+v", cfg.Settings.EnabledTools)
	}
	if cfg.Settings.ContextCompactionThresholdTokens != 360_000 {
		t.Fatalf("default compaction threshold mismatch: %d", cfg.Settings.ContextCompactionThresholdTokens)
	}
	if cfg.Settings.MinimumExecToBgSeconds != defaultMinimumExecToBgSec {
		t.Fatalf("default minimum_exec_to_bg_seconds mismatch: %d", cfg.Settings.MinimumExecToBgSeconds)
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
	if cfg.Settings.CompactionMode != CompactionModeNative {
		t.Fatalf("expected default compaction_mode=native, got %q", cfg.Settings.CompactionMode)
	}
	if cfg.Settings.ShellOutputMaxChars != 16000 {
		t.Fatalf("default shell_output_max_chars mismatch: %d", cfg.Settings.ShellOutputMaxChars)
	}
	if cfg.Settings.BGShellsOutput != BGShellsOutputDefault {
		t.Fatalf("default bg_shells_output mismatch: %q", cfg.Settings.BGShellsOutput)
	}
	if cfg.Settings.Reviewer.Frequency != "off" {
		t.Fatalf("expected default reviewer.frequency=off, got %q", cfg.Settings.Reviewer.Frequency)
	}
	if cfg.Settings.Reviewer.Model != cfg.Settings.Model {
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
}

func TestLoadReviewerModelInheritsMainModelWhenUnset(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model = \"gpt-main-file\"\n[reviewer]\nfrequency = \"all\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.Reviewer.Model != "gpt-main-file" {
		t.Fatalf("expected reviewer.model to inherit file main model, got %q", cfg.Settings.Reviewer.Model)
	}

	t.Setenv("BUILDER_MODEL", "gpt-main-env")
	t.Setenv("BUILDER_REVIEWER_MODEL", "")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env model: %v", err)
	}
	if cfg.Settings.Reviewer.Model != "gpt-main-env" {
		t.Fatalf("expected reviewer.model to inherit env main model, got %q", cfg.Settings.Reviewer.Model)
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
max_suggestions = 3
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

	t.Setenv("BUILDER_REVIEWER_FREQUENCY", "off")
	t.Setenv("BUILDER_REVIEWER_MODEL", "gpt-env-reviewer")
	t.Setenv("BUILDER_REVIEWER_THINKING_LEVEL", "high")
	t.Setenv("BUILDER_REVIEWER_TIMEOUT_SECONDS", "30")
	t.Setenv("BUILDER_REVIEWER_MAX_SUGGESTIONS", "4")

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

	t.Setenv("BUILDER_REVIEWER_MAX_SUGGESTIONS", "0")
	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid reviewer max suggestions")
	}

	t.Setenv("BUILDER_REVIEWER_MAX_SUGGESTIONS", "4")
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

func TestLoadTUIScrollModePrecedenceAndValidation(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BUILDER_TUI_SCROLL_MODE", "")

	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("tui_scroll_mode = \"native\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.TUIScrollMode != TUIScrollModeNative {
		t.Fatalf("expected file tui_scroll_mode=native, got %q", cfg.Settings.TUIScrollMode)
	}
	if got := cfg.Source.Sources["tui_scroll_mode"]; got != "file" {
		t.Fatalf("expected tui_scroll_mode source file, got %q", got)
	}

	t.Setenv("BUILDER_TUI_SCROLL_MODE", "alt")
	cfg, err = Load(workspace, LoadOptions{})
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.Settings.TUIScrollMode != TUIScrollModeAlt {
		t.Fatalf("expected env tui_scroll_mode=alt, got %q", cfg.Settings.TUIScrollMode)
	}
	if got := cfg.Source.Sources["tui_scroll_mode"]; got != "env" {
		t.Fatalf("expected tui_scroll_mode source env, got %q", got)
	}

	t.Setenv("BUILDER_TUI_SCROLL_MODE", "broken")
	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected invalid tui_scroll_mode validation error")
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

func TestLoadRejectsRemovedUseNativeCompactionEnv(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BUILDER_USE_NATIVE_COMPACTION", "true")

	if _, err := Load(workspace, LoadOptions{}); err == nil {
		t.Fatal("expected unsupported BUILDER_USE_NATIVE_COMPACTION error")
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
