package main

import (
	"bytes"
	"context"
	"os"
	"testing"

	"builder/server/metadata"
	"builder/shared/config"
)

func TestProjectSubcommandPrintsBoundProjectID(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	binding, err := metadata.RegisterBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := projectSubcommand([]string{workspace}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); got != binding.ProjectID+"\n" {
		t.Fatalf("stdout = %q, want %q", got, binding.ProjectID+"\n")
	}
}

func TestAttachSubcommandPathFirstBindsTargetToCurrentProject(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	target := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(source, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load source: %v", err)
	}
	binding, err := metadata.RegisterBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding source: %v", err)
	}

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(source); err != nil {
		t.Fatalf("Chdir source: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousWD) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := attachSubcommand([]string{target}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); got != binding.ProjectID+"\n" {
		t.Fatalf("stdout = %q, want %q", got, binding.ProjectID+"\n")
	}

	targetCfg, err := config.Load(target, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load target: %v", err)
	}
	resolved, err := metadata.ResolveBinding(context.Background(), targetCfg.PersistenceRoot, targetCfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("ResolveBinding target: %v", err)
	}
	if resolved.ProjectID != binding.ProjectID {
		t.Fatalf("target project id = %q, want %q", resolved.ProjectID, binding.ProjectID)
	}
}

func TestAttachSubcommandExplicitProjectOverridesCurrentWorkspace(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	target := t.TempDir()
	working := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(source, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load source: %v", err)
	}
	binding, err := metadata.RegisterBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding source: %v", err)
	}

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(working); err != nil {
		t.Fatalf("Chdir working: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousWD) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := attachSubcommand([]string{"--project", binding.ProjectID, target}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); got != binding.ProjectID+"\n" {
		t.Fatalf("stdout = %q, want %q", got, binding.ProjectID+"\n")
	}
}

func TestAttachSubcommandWithoutProjectGuidanceFailsWhenCurrentWorkspaceUnregistered(t *testing.T) {
	home := t.TempDir()
	working := t.TempDir()
	target := t.TempDir()
	t.Setenv("HOME", home)

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(working); err != nil {
		t.Fatalf("Chdir working: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousWD) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := attachSubcommand([]string{target}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); got == "" || !bytes.Contains([]byte(got), []byte("builder project")) || !bytes.Contains([]byte(got), []byte("--project <project-id>")) {
		t.Fatalf("stderr = %q, want recovery guidance", got)
	}
}
