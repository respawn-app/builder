package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/session"
	"builder/shared/config"
)

type stubAuthInteractor struct {
	callCount int
	interact  func(context.Context, authInteraction) error
}

func (s *stubAuthInteractor) WrapStore(base auth.Store) auth.Store {
	return base
}

func (s *stubAuthInteractor) NeedsInteraction(req authInteraction) bool {
	return !req.Gate.Ready
}

func (s *stubAuthInteractor) Interact(ctx context.Context, req authInteraction) error {
	s.callCount++
	if s.interact == nil {
		return nil
	}
	return s.interact(ctx, req)
}

func (s *stubAuthInteractor) LookupEnv(string) string {
	return ""
}

func (s *stubAuthInteractor) Interactive() bool {
	return true
}

func TestEnsureAuthReadyHeadlessReturnsStartupErrorWithoutCredentials(t *testing.T) {
	mgr := auth.NewManager(auth.NewMemoryStore(auth.EmptyState()), nil, time.Now)

	err := ensureAuthReady(context.Background(), mgr, auth.OpenAIOAuthOptions{}, "dark", config.TUIAlternateScreenAuto, &headlessAuthInteractor{
		lookupEnv: func(string) string { return "" },
	})
	if !errors.Is(err, auth.ErrAuthNotConfigured) {
		t.Fatalf("expected auth not configured, got %v", err)
	}
}

func TestBootstrapAppHeadlessUsesEnvAPIKeyWithoutPersistingAuthState(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "sk-env")

	boot, err := startEmbeddedServer(context.Background(), Options{WorkspaceRoot: workspace}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	defer func() { _ = boot.Close() }()

	state, err := boot.AuthManager().Load(context.Background())
	if err != nil {
		t.Fatalf("load auth state: %v", err)
	}
	if state.Method.Type != auth.MethodAPIKey {
		t.Fatalf("expected env api key auth, got %q", state.Method.Type)
	}
	if state.Method.APIKey == nil || state.Method.APIKey.Key != "sk-env" {
		t.Fatalf("expected env api key to be visible through manager, got %+v", state.Method.APIKey)
	}

	authPath := config.GlobalAuthConfigPath(boot.Config())
	if _, err := os.Stat(authPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no persisted auth state at %q, got err=%v", authPath, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".builder", "config.toml")); err != nil {
		t.Fatalf("expected config bootstrap artifacts to exist: %v", err)
	}
}

func TestResolveSessionActionLogoutUsesBootstrapAuthInteractor(t *testing.T) {
	ctx := context.Background()
	mgr := auth.NewManager(auth.NewMemoryStore(auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "sk-before"},
		},
	}), nil, time.Now)
	interactor := &stubAuthInteractor{}
	interactor.interact = func(ctx context.Context, req authInteraction) error {
		if req.Manager != mgr {
			t.Fatal("expected resolveSessionAction logout to reuse bootstrap auth manager")
		}
		if !errors.Is(req.StartupErr, auth.ErrAuthNotConfigured) {
			t.Fatalf("expected auth not configured after logout, got %v", req.StartupErr)
		}
		if req.Gate.Reason != auth.ErrAuthNotConfigured.Error() {
			t.Fatalf("expected auth gate reason %q, got %q", auth.ErrAuthNotConfigured.Error(), req.Gate.Reason)
		}
		_, err := req.Manager.SwitchMethod(ctx, auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "sk-after"},
		}, true)
		return err
	}

	root := t.TempDir()
	store, err := session.Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}

	resolved, err := resolveSessionAction(
		ctx,
		&testEmbeddedServer{cfg: config.App{PersistenceRoot: root}, authManager: mgr},
		interactor,
		store,
		UITransition{Action: UIActionLogout},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if interactor.callCount != 1 {
		t.Fatalf("expected auth interactor to be called once, got %d", interactor.callCount)
	}
	if !resolved.ShouldContinue {
		t.Fatal("expected logout flow to continue after reauth")
	}
	if resolved.NextSessionID != store.Meta().SessionID {
		t.Fatalf("expected session to continue in place, got %q", resolved.NextSessionID)
	}
	if resolved.InitialPrompt != "" || resolved.InitialInput != "" || resolved.ParentSessionID != "" || resolved.ForceNewSession {
		t.Fatalf("unexpected logout transition values prompt=%q input=%q parent=%q forceNew=%t", resolved.InitialPrompt, resolved.InitialInput, resolved.ParentSessionID, resolved.ForceNewSession)
	}

	state, err := mgr.Load(ctx)
	if err != nil {
		t.Fatalf("load auth state: %v", err)
	}
	if state.Method.APIKey == nil || state.Method.APIKey.Key != "sk-after" {
		t.Fatalf("expected logout flow to restore auth method, got %+v", state.Method.APIKey)
	}
}

func TestResolveSessionActionLogoutAllowsNilStore(t *testing.T) {
	ctx := context.Background()
	mgr := auth.NewManager(auth.NewMemoryStore(auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "sk-before"},
		},
	}), nil, time.Now)
	interactor := &stubAuthInteractor{}
	interactor.interact = func(ctx context.Context, req authInteraction) error {
		_, err := req.Manager.SwitchMethod(ctx, auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "sk-after"},
		}, true)
		return err
	}

	resolved, err := resolveSessionAction(
		ctx,
		&testEmbeddedServer{authManager: mgr},
		interactor,
		nil,
		UITransition{Action: UIActionLogout},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if interactor.callCount != 1 {
		t.Fatalf("expected auth interactor to be called once, got %d", interactor.callCount)
	}
	if !resolved.ShouldContinue {
		t.Fatal("expected logout flow to continue after reauth")
	}
	if resolved.NextSessionID != "" {
		t.Fatalf("expected no next session id without a current store, got %q", resolved.NextSessionID)
	}
}
