package app

import (
	"context"
	"os"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/authflow"
	"builder/shared/client"
	"builder/shared/config"
)

type remoteReauthInteractor struct {
	configured bool
}

func (i *remoteReauthInteractor) WrapStore(base auth.Store) auth.Store { return base }
func (i *remoteReauthInteractor) LookupEnv(string) string              { return "" }
func (i *remoteReauthInteractor) Interactive() bool                    { return true }
func (i *remoteReauthInteractor) NeedsInteraction(req authInteraction) bool {
	return !req.Gate.Ready
}

func (i *remoteReauthInteractor) Interact(ctx context.Context, req authInteraction) (authflow.InteractionOutcome, error) {
	i.configured = true
	_, err := req.Manager.SwitchMethod(ctx, auth.Method{
		Type:   auth.MethodAPIKey,
		APIKey: &auth.APIKeyMethod{Key: "reauthed-key"},
	}, true)
	return authflow.InteractionOutcome{}, err
}

func TestRemoteAppServerReauthenticateUsesLocalAuthStore(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store := auth.NewFileStore(config.GlobalAuthConfigPath(cfg))
	manager := auth.NewManager(store, nil, time.Now)
	if _, err := manager.ClearMethod(context.Background(), true); err != nil {
		t.Fatalf("ClearMethod: %v", err)
	}

	server := &remoteAppServer{cfg: cfg}
	interactor := &remoteReauthInteractor{}
	if err := server.Reauthenticate(context.Background(), interactor); err != nil {
		t.Fatalf("Reauthenticate: %v", err)
	}
	if !interactor.configured {
		t.Fatal("expected reauthenticate to invoke auth interactor")
	}

	state, err := manager.StoredState(context.Background())
	if err != nil {
		t.Fatalf("StoredState: %v", err)
	}
	if state.Method.APIKey == nil || state.Method.APIKey.Key != "reauthed-key" {
		t.Fatalf("unexpected stored auth state: %+v", state.Method)
	}

	if _, err := os.Stat(config.GlobalAuthConfigPath(cfg)); err != nil {
		t.Fatalf("expected auth file to exist: %v", err)
	}
}

func TestRemoteAppServerAuthManagerUsesLocalAuthStore(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store := auth.NewFileStore(config.GlobalAuthConfigPath(cfg))
	if err := store.Save(context.Background(), auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type: auth.MethodOAuth,
			OAuth: &auth.OAuthMethod{
				AccessToken: "access-token",
				AccountID:   "acct-123",
				Email:       "user@example.com",
			},
		},
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save auth state: %v", err)
	}

	server := &remoteAppServer{cfg: cfg}
	manager := server.AuthManager()
	if manager == nil {
		t.Fatal("expected auth manager")
	}
	state, err := manager.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if state.Method.Type != auth.MethodOAuth {
		t.Fatalf("expected oauth auth, got %+v", state.Method)
	}
	if state.Method.OAuth == nil || state.Method.OAuth.Email != "user@example.com" || state.Method.OAuth.AccountID != "acct-123" {
		t.Fatalf("unexpected oauth state: %+v", state.Method.OAuth)
	}
	if manager != server.AuthManager() {
		t.Fatal("expected auth manager to be memoized")
	}
}

func TestRemoteAppServerCloseUsesOwnedCloser(t *testing.T) {
	called := false
	server := newRemoteAppServerWithClose(&client.Remote{}, config.App{}, func() error {
		called = true
		return nil
	})
	if !server.OwnsServer() {
		t.Fatal("expected launched remote server to be owned")
	}
	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !called {
		t.Fatal("expected owned remote closer to be invoked")
	}
}

func TestRemoteAppServerDiscoveredRemoteIsNotOwned(t *testing.T) {
	server := newRemoteAppServer(&client.Remote{}, config.App{})
	if server.OwnsServer() {
		t.Fatal("expected discovered remote server to not be owned")
	}
}
