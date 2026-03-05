package auth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStoreSaveWritesWithSecurePermissions(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	store := NewFileStore(statePath)

	state := State{
		Scope: ScopeGlobal,
		Method: Method{
			Type: MethodAPIKey,
			APIKey: &APIKeyMethod{
				Key: "secret-key",
			},
		},
		UpdatedAt: time.Date(2026, time.January, 1, 10, 0, 0, 0, time.UTC),
	}

	if err := store.Save(context.Background(), state); err != nil {
		t.Fatalf("save auth state: %v", err)
	}

	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat auth state: %v", err)
	}
	if got := info.Mode().Perm(); got != authStateFileMode {
		t.Fatalf("expected auth state mode %04o, got %04o", authStateFileMode, got)
	}
}

func TestFileStoreLoadCorrectsExistingFilePermissions(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "auth-state.json")

	state := State{
		Scope: ScopeGlobal,
		Method: Method{
			Type: MethodOAuth,
			OAuth: &OAuthMethod{
				AccessToken:  "access-token",
				RefreshToken: "refresh-token",
				TokenType:    "Bearer",
				Expiry:       time.Date(2026, time.January, 1, 11, 0, 0, 0, time.UTC),
			},
		},
		UpdatedAt: time.Date(2026, time.January, 1, 10, 0, 0, 0, time.UTC),
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal auth state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		t.Fatalf("seed auth state: %v", err)
	}

	store := NewFileStore(statePath)
	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load auth state: %v", err)
	}
	if loaded.Method.Type != MethodOAuth {
		t.Fatalf("expected oauth method, got %q", loaded.Method.Type)
	}

	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat auth state: %v", err)
	}
	if got := info.Mode().Perm(); got != authStateFileMode {
		t.Fatalf("expected corrected auth state mode %04o, got %04o", authStateFileMode, got)
	}
}

func TestFileStoreLoadDoesNotBroadenStrictPermissions(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "auth-state.json")

	state := State{
		Scope: ScopeGlobal,
		Method: Method{
			Type: MethodOAuth,
			OAuth: &OAuthMethod{
				AccessToken:  "access-token",
				RefreshToken: "refresh-token",
				TokenType:    "Bearer",
				Expiry:       time.Date(2026, time.January, 1, 11, 0, 0, 0, time.UTC),
			},
		},
		UpdatedAt: time.Date(2026, time.January, 1, 10, 0, 0, 0, time.UTC),
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal auth state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o400); err != nil {
		t.Fatalf("seed auth state: %v", err)
	}

	store := NewFileStore(statePath)
	if _, err := store.Load(context.Background()); err != nil {
		t.Fatalf("load auth state: %v", err)
	}

	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat auth state: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o400 {
		t.Fatalf("expected strict auth state mode to stay 0400, got %04o", got)
	}
}

func TestFileStoreSaveCorrectsExistingInsecurePermissions(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "auth-state.json")

	seed := State{
		Scope: ScopeGlobal,
		Method: Method{
			Type: MethodAPIKey,
			APIKey: &APIKeyMethod{
				Key: "seed-key",
			},
		},
		UpdatedAt: time.Date(2026, time.January, 1, 9, 0, 0, 0, time.UTC),
	}
	seedData, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("marshal seed auth state: %v", err)
	}
	if err := os.WriteFile(statePath, seedData, 0o644); err != nil {
		t.Fatalf("seed insecure auth state: %v", err)
	}

	store := NewFileStore(statePath)
	next := State{
		Scope: ScopeGlobal,
		Method: Method{
			Type: MethodAPIKey,
			APIKey: &APIKeyMethod{
				Key: "next-key",
			},
		},
		UpdatedAt: time.Date(2026, time.January, 1, 10, 0, 0, 0, time.UTC),
	}
	if err := store.Save(context.Background(), next); err != nil {
		t.Fatalf("save auth state: %v", err)
	}

	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat auth state: %v", err)
	}
	if got := info.Mode().Perm(); got != authStateFileMode {
		t.Fatalf("expected corrected auth state mode %04o, got %04o", authStateFileMode, got)
	}
}
