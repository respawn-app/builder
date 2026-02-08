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
	if got := cfg.PersistenceRoot; got != filepath.Join(home, ".builder") {
		t.Fatalf("default persistence root mismatch: %q", got)
	}
	if !cfg.Settings.EnabledTools[tools.ToolShell] || !cfg.Settings.EnabledTools[tools.ToolPatch] || !cfg.Settings.EnabledTools[tools.ToolAskQuestion] {
		t.Fatalf("expected all default tools enabled: %+v", cfg.Settings.EnabledTools)
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
