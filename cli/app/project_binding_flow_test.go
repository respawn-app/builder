package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"builder/server/metadata"
	"builder/server/projectview"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/config"
	xansi "github.com/charmbracelet/x/ansi"
)

func TestEnsureInteractiveProjectBindingBindsRegisteredWorkspaceWithoutPrompt(t *testing.T) {
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
	store, err := metadata.Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := projectview.NewMetadataService(store, "", "")
	if err != nil {
		t.Fatalf("NewMetadataService: %v", err)
	}

	originalPicker := runProjectBindingPickerFlow
	originalPrompt := runProjectNamePromptFlow
	t.Cleanup(func() {
		runProjectBindingPickerFlow = originalPicker
		runProjectNamePromptFlow = originalPrompt
	})
	runProjectBindingPickerFlow = func([]clientui.ProjectSummary, string, config.TUIAlternateScreenPolicy) (projectBindingPickerResult, error) {
		t.Fatal("did not expect binding picker for registered workspace")
		return projectBindingPickerResult{}, nil
	}
	runProjectNamePromptFlow = func(string, string, config.TUIAlternateScreenPolicy) (string, error) {
		t.Fatal("did not expect project name prompt for registered workspace")
		return "", nil
	}

	server := &testEmbeddedServer{
		cfg:               cfg,
		containerDir:      config.ProjectSessionsRoot(cfg, binding.ProjectID),
		projectViewClient: client.NewLoopbackProjectViewClient(service),
	}

	bound, err := ensureInteractiveProjectBinding(context.Background(), server)
	if err != nil {
		t.Fatalf("ensureInteractiveProjectBinding: %v", err)
	}
	if got := bound.ProjectID(); got != binding.ProjectID {
		t.Fatalf("bound project id = %q, want %q", got, binding.ProjectID)
	}
}

func TestEnsureInteractiveProjectBindingTreatsNestedDirectoryAsUnknownWorkspace(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	nested := filepath.Join(workspace, "subdir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll nested: %v", err)
	}
	t.Setenv("HOME", home)

	baseCfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load workspace: %v", err)
	}
	nestedCfg, err := config.Load(nested, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load nested: %v", err)
	}
	_, err = metadata.RegisterBinding(context.Background(), baseCfg.PersistenceRoot, baseCfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding: %v", err)
	}
	store, err := metadata.Open(baseCfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := projectview.NewMetadataService(store, "", "")
	if err != nil {
		t.Fatalf("NewMetadataService: %v", err)
	}

	originalPicker := runProjectBindingPickerFlow
	originalPrompt := runProjectNamePromptFlow
	t.Cleanup(func() {
		runProjectBindingPickerFlow = originalPicker
		runProjectNamePromptFlow = originalPrompt
	})
	runProjectBindingPickerFlow = func(projects []clientui.ProjectSummary, theme string, policy config.TUIAlternateScreenPolicy) (projectBindingPickerResult, error) {
		if len(projects) != 1 {
			t.Fatalf("expected parent project to appear in picker, got %+v", projects)
		}
		return projectBindingPickerResult{CreateNew: true}, nil
	}
	runProjectNamePromptFlow = func(defaultName string, theme string, policy config.TUIAlternateScreenPolicy) (string, error) {
		if want := filepath.Base(nested); defaultName != want {
			t.Fatalf("default project name = %q, want %q", defaultName, want)
		}
		return "Nested Project", nil
	}

	server := &testEmbeddedServer{
		cfg:               nestedCfg,
		containerDir:      config.ProjectSessionsRoot(nestedCfg, "project-placeholder"),
		projectViewClient: client.NewLoopbackProjectViewClient(service),
	}

	bound, err := ensureInteractiveProjectBinding(context.Background(), server)
	if err != nil {
		t.Fatalf("ensureInteractiveProjectBinding: %v", err)
	}
	resolved, err := metadata.ResolveBinding(context.Background(), nestedCfg.PersistenceRoot, nestedCfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("ResolveBinding nested: %v", err)
	}
	if got := bound.ProjectID(); got != resolved.ProjectID {
		t.Fatalf("bound project id = %q, want %q", got, resolved.ProjectID)
	}
	canonicalNested, err := config.CanonicalWorkspaceRoot(nested)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceRoot nested: %v", err)
	}
	if resolved.CanonicalRoot != canonicalNested {
		t.Fatalf("nested workspace root = %q, want %q", resolved.CanonicalRoot, canonicalNested)
	}
}

func TestEnsureInteractiveProjectBindingCreatesProjectForUnknownWorkspace(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := metadata.Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := projectview.NewMetadataService(store, "", "")
	if err != nil {
		t.Fatalf("NewMetadataService: %v", err)
	}

	originalPicker := runProjectBindingPickerFlow
	originalPrompt := runProjectNamePromptFlow
	t.Cleanup(func() {
		runProjectBindingPickerFlow = originalPicker
		runProjectNamePromptFlow = originalPrompt
	})
	runProjectBindingPickerFlow = func(projects []clientui.ProjectSummary, theme string, policy config.TUIAlternateScreenPolicy) (projectBindingPickerResult, error) {
		if len(projects) != 0 {
			t.Fatalf("expected no projects, got %+v", projects)
		}
		return projectBindingPickerResult{CreateNew: true}, nil
	}
	runProjectNamePromptFlow = func(defaultName string, theme string, policy config.TUIAlternateScreenPolicy) (string, error) {
		if want := filepath.Base(workspace); defaultName != want {
			t.Fatalf("default project name = %q, want %q", defaultName, want)
		}
		return "Project Alpha", nil
	}

	server := &testEmbeddedServer{
		cfg:               cfg,
		containerDir:      config.ProjectSessionsRoot(cfg, "project-placeholder"),
		projectViewClient: client.NewLoopbackProjectViewClient(service),
	}

	bound, err := ensureInteractiveProjectBinding(context.Background(), server)
	if err != nil {
		t.Fatalf("ensureInteractiveProjectBinding: %v", err)
	}
	if bound.ProjectID() == "" {
		t.Fatal("expected created project id")
	}
	resolved, err := metadata.ResolveBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("ResolveBinding: %v", err)
	}
	if resolved.ProjectID != bound.ProjectID() {
		t.Fatalf("resolved project id = %q, want %q", resolved.ProjectID, bound.ProjectID())
	}
	if resolved.ProjectName != "Project Alpha" {
		t.Fatalf("project name = %q, want Project Alpha", resolved.ProjectName)
	}
}

func TestEnsureInteractiveProjectBindingAttachesUnknownWorkspaceToExistingProject(t *testing.T) {
	home := t.TempDir()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	t.Setenv("HOME", home)

	cfgA, err := config.Load(workspaceA, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load A: %v", err)
	}
	bindingA, err := metadata.RegisterBinding(context.Background(), cfgA.PersistenceRoot, cfgA.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding A: %v", err)
	}

	cfgB, err := config.Load(workspaceB, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load B: %v", err)
	}
	store, err := metadata.Open(cfgB.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := projectview.NewMetadataService(store, "", "")
	if err != nil {
		t.Fatalf("NewMetadataService: %v", err)
	}

	originalPicker := runProjectBindingPickerFlow
	originalPrompt := runProjectNamePromptFlow
	t.Cleanup(func() {
		runProjectBindingPickerFlow = originalPicker
		runProjectNamePromptFlow = originalPrompt
	})
	runProjectBindingPickerFlow = func(projects []clientui.ProjectSummary, theme string, policy config.TUIAlternateScreenPolicy) (projectBindingPickerResult, error) {
		if len(projects) != 1 || projects[0].ProjectID != bindingA.ProjectID {
			t.Fatalf("unexpected projects: %+v", projects)
		}
		picked := projects[0]
		return projectBindingPickerResult{Project: &picked}, nil
	}
	runProjectNamePromptFlow = func(string, string, config.TUIAlternateScreenPolicy) (string, error) {
		t.Fatal("did not expect project name prompt when attaching to existing project")
		return "", nil
	}

	server := &testEmbeddedServer{
		cfg:               cfgB,
		containerDir:      config.ProjectSessionsRoot(cfgB, bindingA.ProjectID),
		projectViewClient: client.NewLoopbackProjectViewClient(service),
	}

	bound, err := ensureInteractiveProjectBinding(context.Background(), server)
	if err != nil {
		t.Fatalf("ensureInteractiveProjectBinding: %v", err)
	}
	if bound.ProjectID() != bindingA.ProjectID {
		t.Fatalf("bound project id = %q, want %q", bound.ProjectID(), bindingA.ProjectID)
	}
	resolved, err := metadata.ResolveBinding(context.Background(), cfgB.PersistenceRoot, cfgB.WorkspaceRoot)
	if err != nil {
		t.Fatalf("ResolveBinding B: %v", err)
	}
	if resolved.ProjectID != bindingA.ProjectID {
		t.Fatalf("workspace B project id = %q, want %q", resolved.ProjectID, bindingA.ProjectID)
	}
}

func TestEnsureInteractiveProjectBindingFormatsMissingSelectedProjectError(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := metadata.Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := projectview.NewMetadataService(store, "", "")
	if err != nil {
		t.Fatalf("NewMetadataService: %v", err)
	}

	originalPicker := runProjectBindingPickerFlow
	originalPrompt := runProjectNamePromptFlow
	t.Cleanup(func() {
		runProjectBindingPickerFlow = originalPicker
		runProjectNamePromptFlow = originalPrompt
	})
	runProjectBindingPickerFlow = func([]clientui.ProjectSummary, string, config.TUIAlternateScreenPolicy) (projectBindingPickerResult, error) {
		picked := clientui.ProjectSummary{ProjectID: "project-missing", DisplayName: "Missing Project"}
		return projectBindingPickerResult{Project: &picked}, nil
	}
	runProjectNamePromptFlow = func(string, string, config.TUIAlternateScreenPolicy) (string, error) {
		t.Fatal("did not expect project name prompt when attaching to existing project")
		return "", nil
	}

	server := &testEmbeddedServer{
		cfg:               cfg,
		containerDir:      config.ProjectSessionsRoot(cfg, "project-placeholder"),
		projectViewClient: client.NewLoopbackProjectViewClient(service),
	}

	_, err = ensureInteractiveProjectBinding(context.Background(), server)
	if !errors.Is(err, metadata.ErrProjectNotFound) {
		t.Fatalf("ensureInteractiveProjectBinding error = %v, want ErrProjectNotFound", err)
	}
	if got := err.Error(); !strings.Contains(got, "Restart Builder and choose another project") || !strings.Contains(got, "project-missing") {
		t.Fatalf("error = %q, want missing project picker guidance", got)
	}
}

func TestEnsureInteractiveProjectBindingReturnsCancelWhenPickerAborts(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := metadata.Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := projectview.NewMetadataService(store, "", "")
	if err != nil {
		t.Fatalf("NewMetadataService: %v", err)
	}

	originalPicker := runProjectBindingPickerFlow
	originalPrompt := runProjectNamePromptFlow
	t.Cleanup(func() {
		runProjectBindingPickerFlow = originalPicker
		runProjectNamePromptFlow = originalPrompt
	})
	runProjectBindingPickerFlow = func([]clientui.ProjectSummary, string, config.TUIAlternateScreenPolicy) (projectBindingPickerResult, error) {
		return projectBindingPickerResult{Canceled: true}, nil
	}
	runProjectNamePromptFlow = func(string, string, config.TUIAlternateScreenPolicy) (string, error) {
		t.Fatal("did not expect project name prompt after picker cancel")
		return "", nil
	}

	server := &testEmbeddedServer{
		cfg:               cfg,
		containerDir:      config.ProjectSessionsRoot(cfg, "project-placeholder"),
		projectViewClient: client.NewLoopbackProjectViewClient(service),
	}

	if _, err := ensureInteractiveProjectBinding(context.Background(), server); err == nil || !strings.Contains(err.Error(), "startup canceled by user") {
		t.Fatalf("expected startup canceled error, got %v", err)
	}
	if _, err := metadata.ResolveBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot); err != metadata.ErrWorkspaceNotRegistered {
		t.Fatalf("ResolveBinding after picker cancel = %v, want ErrWorkspaceNotRegistered", err)
	}
}

func TestEnsureInteractiveProjectBindingReturnsCancelWhenProjectNamingAborts(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := metadata.Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := projectview.NewMetadataService(store, "", "")
	if err != nil {
		t.Fatalf("NewMetadataService: %v", err)
	}

	originalPicker := runProjectBindingPickerFlow
	originalPrompt := runProjectNamePromptFlow
	t.Cleanup(func() {
		runProjectBindingPickerFlow = originalPicker
		runProjectNamePromptFlow = originalPrompt
	})
	runProjectBindingPickerFlow = func([]clientui.ProjectSummary, string, config.TUIAlternateScreenPolicy) (projectBindingPickerResult, error) {
		return projectBindingPickerResult{CreateNew: true}, nil
	}
	runProjectNamePromptFlow = func(string, string, config.TUIAlternateScreenPolicy) (string, error) {
		return "", context.Canceled
	}

	server := &testEmbeddedServer{
		cfg:               cfg,
		containerDir:      config.ProjectSessionsRoot(cfg, "project-placeholder"),
		projectViewClient: client.NewLoopbackProjectViewClient(service),
	}

	if _, err := ensureInteractiveProjectBinding(context.Background(), server); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from project name prompt, got %v", err)
	}
	if _, err := metadata.ResolveBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot); err != metadata.ErrWorkspaceNotRegistered {
		t.Fatalf("ResolveBinding after naming cancel = %v, want ErrWorkspaceNotRegistered", err)
	}
}

func TestProjectBindingHeadersTrimMarkdownInset(t *testing.T) {
	picker := newProjectBindingPickerModel(nil, "dark")
	if got := xansi.Strip(picker.renderHeader()); strings.HasPrefix(got, "  ") {
		t.Fatalf("picker header has unexpected left padding: %q", got)
	}

	prompt := newProjectNamePromptModel("demo", "dark")
	if got := xansi.Strip(prompt.renderHeader()); strings.HasPrefix(got, "  ") {
		t.Fatalf("project name header has unexpected left padding: %q", got)
	}
}

func TestProjectNamePromptViewUsesFramedEditableInput(t *testing.T) {
	model := newProjectNamePromptModel("demo", "dark")
	model.width = 40
	model.height = 10
	view := model.View()
	if !strings.Contains(view, "────────────────") {
		t.Fatalf("expected framed input border in prompt view, got %q", view)
	}
	if strings.Contains(view, "Unknown directory opened") {
		t.Fatalf("unexpected picker subtitle leaked into prompt view: %q", view)
	}
}

func TestProjectNamePromptViewTracksLongInputCursor(t *testing.T) {
	model := newProjectNamePromptModel("", "dark")
	model.width = 18
	model.height = 4
	model.input.SetValue("project-name-with-long-tail")
	model.input.SetCursor(len([]rune(model.input.Value())))
	view := model.View()
	if strings.Contains(view, "project-name") {
		t.Fatalf("expected long input view to follow cursor near tail, got %q", view)
	}
	if !strings.Contains(view, "long-tail") {
		t.Fatalf("expected long input tail to remain visible, got %q", view)
	}
}

func TestEnsureInteractiveProjectBindingFormatsMissingBoundProjectError(t *testing.T) {
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
	store, err := metadata.Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := projectview.NewMetadataService(store, "", "")
	if err != nil {
		t.Fatalf("NewMetadataService: %v", err)
	}

	server := &failingBindProjectServer{
		testEmbeddedServer: &testEmbeddedServer{
			cfg:               cfg,
			containerDir:      config.ProjectSessionsRoot(cfg, binding.ProjectID),
			projectViewClient: client.NewLoopbackProjectViewClient(service),
		},
		bindErr: fmt.Errorf("bind project: %w", metadata.ErrProjectNotFound),
	}

	_, err = ensureInteractiveProjectBinding(context.Background(), server)
	if !errors.Is(err, metadata.ErrProjectNotFound) {
		t.Fatalf("ensureInteractiveProjectBinding error = %v, want ErrProjectNotFound", err)
	}
	if got := err.Error(); !strings.Contains(got, "attached to missing project") || !strings.Contains(got, binding.ProjectID) {
		t.Fatalf("error = %q, want missing project guidance", got)
	}
}

func TestEnsureInteractiveProjectBindingFormatsUnavailableBoundProjectError(t *testing.T) {
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
	store, err := metadata.Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := projectview.NewMetadataService(store, "", "")
	if err != nil {
		t.Fatalf("NewMetadataService: %v", err)
	}

	server := &failingBindProjectServer{
		testEmbeddedServer: &testEmbeddedServer{
			cfg:               cfg,
			containerDir:      config.ProjectSessionsRoot(cfg, binding.ProjectID),
			projectViewClient: client.NewLoopbackProjectViewClient(service),
		},
		bindErr: metadata.ProjectUnavailableError{ProjectID: binding.ProjectID, RootPath: cfg.WorkspaceRoot, Availability: clientui.ProjectAvailabilityMissing},
	}

	_, err = ensureInteractiveProjectBinding(context.Background(), server)
	if !errors.Is(err, metadata.ErrProjectUnavailable) {
		t.Fatalf("ensureInteractiveProjectBinding error = %v, want ErrProjectUnavailable", err)
	}
	if got := err.Error(); !strings.Contains(got, "builder rebind") || !strings.Contains(got, "missing") {
		t.Fatalf("error = %q, want rebind guidance", got)
	}
}

func TestEnsureInteractiveProjectBindingFormatsInaccessibleBoundProjectError(t *testing.T) {
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
	store, err := metadata.Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := projectview.NewMetadataService(store, "", "")
	if err != nil {
		t.Fatalf("NewMetadataService: %v", err)
	}

	server := &failingBindProjectServer{
		testEmbeddedServer: &testEmbeddedServer{
			cfg:               cfg,
			containerDir:      config.ProjectSessionsRoot(cfg, binding.ProjectID),
			projectViewClient: client.NewLoopbackProjectViewClient(service),
		},
		bindErr: metadata.ProjectUnavailableError{ProjectID: binding.ProjectID, RootPath: cfg.WorkspaceRoot, Availability: clientui.ProjectAvailabilityInaccessible},
	}

	_, err = ensureInteractiveProjectBinding(context.Background(), server)
	if !errors.Is(err, metadata.ErrProjectUnavailable) {
		t.Fatalf("ensureInteractiveProjectBinding error = %v, want ErrProjectUnavailable", err)
	}
	if got := err.Error(); !strings.Contains(got, "Restore access") || !strings.Contains(got, "inaccessible") || !strings.Contains(got, "builder rebind") {
		t.Fatalf("error = %q, want inaccessible-root recovery guidance", got)
	}
}

type failingBindProjectServer struct {
	*testEmbeddedServer
	bindErr error
}

func (s *failingBindProjectServer) ProjectID() string { return "" }

func (s *failingBindProjectServer) BindProject(context.Context, string) (embeddedServer, error) {
	if s.bindErr != nil {
		return nil, s.bindErr
	}
	return s.testEmbeddedServer, nil
}
