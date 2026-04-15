package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestProjectSubcommandTreatsNestedDirectoryAsUnregistered(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	nested := filepath.Join(workspace, "subdir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll nested: %v", err)
	}
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	_, err = metadata.RegisterBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := projectSubcommand([]string{nested}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1 stderr=%q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !bytes.Contains([]byte(got), []byte("workspace is not registered")) {
		t.Fatalf("stderr = %q, want unregistered error", got)
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

func TestAttachSubcommandRejectsUnknownExplicitProjectIDCleanly(t *testing.T) {
	home := t.TempDir()
	target := t.TempDir()
	t.Setenv("HOME", home)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := attachSubcommand([]string{"--project", "project-missing", target}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1 stderr=%q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !bytes.Contains([]byte(got), []byte("does not exist in this Builder state")) || !bytes.Contains([]byte(got), []byte("project-missing")) {
		t.Fatalf("stderr = %q, want missing project guidance", got)
	}
}

func TestRebindSubcommandPreservesWorkspaceIdentity(t *testing.T) {
	home := t.TempDir()
	oldWorkspace := t.TempDir()
	newParent := t.TempDir()
	newWorkspace := filepath.Join(newParent, "workspace-moved")
	t.Setenv("HOME", home)

	cfg, err := config.Load(oldWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load oldWorkspace: %v", err)
	}
	binding, err := metadata.RegisterBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding oldWorkspace: %v", err)
	}
	if err := os.Rename(oldWorkspace, newWorkspace); err != nil {
		t.Fatalf("Rename workspace: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := rebindSubcommand([]string{oldWorkspace, newWorkspace}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); got != binding.WorkspaceID+"\n" {
		t.Fatalf("stdout = %q, want %q", got, binding.WorkspaceID+"\n")
	}
	newProjectID, err := projectIDForPath(context.Background(), newWorkspace)
	if err != nil {
		t.Fatalf("projectIDForPath newWorkspace: %v", err)
	}
	if newProjectID != binding.ProjectID {
		t.Fatalf("new project id = %q, want %q", newProjectID, binding.ProjectID)
	}
	if _, err := projectIDForPath(context.Background(), oldWorkspace); !errors.Is(err, metadata.ErrWorkspaceNotRegistered) {
		t.Fatalf("projectIDForPath oldWorkspace error = %v, want ErrWorkspaceNotRegistered", err)
	}
}

func TestRebindSubcommandRejectsInvalidInputs(t *testing.T) {
	home := t.TempDir()
	oldWorkspace := t.TempDir()
	otherWorkspace := t.TempDir()
	missingWorkspace := filepath.Join(t.TempDir(), "missing")
	t.Setenv("HOME", home)

	cfg, err := config.Load(oldWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load oldWorkspace: %v", err)
	}
	_, err = metadata.RegisterBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding oldWorkspace: %v", err)
	}
	otherCfg, err := config.Load(otherWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load otherWorkspace: %v", err)
	}
	_, err = metadata.RegisterBinding(context.Background(), otherCfg.PersistenceRoot, otherCfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding otherWorkspace: %v", err)
	}

	assertRebindError := func(args []string, want string) {
		t.Helper()
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := rebindSubcommand(args, &stdout, &stderr); code != 1 {
			t.Fatalf("exit code = %d, want 1 stderr=%q", code, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", stdout.String())
		}
		if got := stderr.String(); !bytes.Contains([]byte(got), []byte(want)) {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	}

	assertRebindError([]string{filepath.Join(t.TempDir(), "unknown-old"), otherWorkspace}, "workspace is not registered")
	assertRebindError([]string{oldWorkspace, missingWorkspace}, "does not exist")
	assertRebindError([]string{oldWorkspace, otherWorkspace}, "already bound")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := rebindSubcommand([]string{oldWorkspace}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2 stderr=%q", code, stderr.String())
	}
	if got := stderr.String(); !bytes.Contains([]byte(got), []byte("rebind requires <old-path> and <new-path>")) {
		t.Fatalf("stderr = %q, want usage guidance", got)
	}
}
