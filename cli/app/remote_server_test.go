package app

import (
	"context"
	"os"
	"testing"
	"time"

	"builder/server/auth"
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

func (i *remoteReauthInteractor) Interact(ctx context.Context, req authInteraction) error {
	i.configured = true
	_, err := req.Manager.SwitchMethod(ctx, auth.Method{
		Type:   auth.MethodAPIKey,
		APIKey: &auth.APIKeyMethod{Key: "reauthed-key"},
	}, true)
	return err
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
