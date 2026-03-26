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

func TestEnvAPIKeyOverrideStoreLoadAlwaysPrefersEnvironmentWithoutPersistedState(t *testing.T) {
	store := NewEnvAPIKeyOverrideStore(NewMemoryStore(State{
		Scope: ScopeGlobal,
		Method: Method{
			Type: MethodOAuth,
			OAuth: &OAuthMethod{
				AccessToken:  "oauth-access",
				RefreshToken: "oauth-refresh",
				TokenType:    "Bearer",
				Expiry:       time.Date(2026, time.January, 1, 11, 0, 0, 0, time.UTC),
			},
		},
	}), func(key string) (string, bool) {
		if key == "OPENAI_API_KEY" {
			return "  sk-env  ", true
		}
		return "", false
	})

	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load auth state: %v", err)
	}
	if state.Method.Type != MethodAPIKey {
		t.Fatalf("expected api key override, got %q", state.Method.Type)
	}
	if state.Method.APIKey == nil || state.Method.APIKey.Key != "sk-env" {
		t.Fatalf("expected trimmed env api key, got %+v", state.Method.APIKey)
	}
}

func TestEnvAPIKeyOverrideStoreRespectsSavedPreference(t *testing.T) {
	store := NewEnvAPIKeyOverrideStore(NewMemoryStore(State{
		Scope:               ScopeGlobal,
		EnvAPIKeyPreference: EnvAPIKeyPreferencePreferEnv,
		Method: Method{
			Type: MethodOAuth,
			OAuth: &OAuthMethod{
				AccessToken:  "oauth-access",
				RefreshToken: "oauth-refresh",
				TokenType:    "Bearer",
				Expiry:       time.Date(2026, time.January, 1, 11, 0, 0, 0, time.UTC),
			},
		},
	}), func(key string) (string, bool) {
		if key == "OPENAI_API_KEY" {
			return "sk-env", true
		}
		return "", false
	})

	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load auth state: %v", err)
	}
	if state.Method.Type != MethodAPIKey {
		t.Fatalf("expected api key override, got %q", state.Method.Type)
	}
	if state.Method.APIKey == nil || state.Method.APIKey.Key != "sk-env" {
		t.Fatalf("expected env api key override, got %+v", state.Method.APIKey)
	}
}

func TestEnvAPIKeyOverrideStoreKeepsSavedOAuthWhenPreferencePrefersSaved(t *testing.T) {
	store := NewEnvAPIKeyOverrideStore(NewMemoryStore(State{
		Scope:               ScopeGlobal,
		EnvAPIKeyPreference: EnvAPIKeyPreferencePreferSaved,
		Method: Method{
			Type: MethodOAuth,
			OAuth: &OAuthMethod{
				AccessToken:  "oauth-access",
				RefreshToken: "oauth-refresh",
				TokenType:    "Bearer",
				Expiry:       time.Date(2026, time.January, 1, 11, 0, 0, 0, time.UTC),
			},
		},
	}), func(key string) (string, bool) {
		if key == "OPENAI_API_KEY" {
			return "sk-env", true
		}
		return "", false
	})

	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load auth state: %v", err)
	}
	if state.Method.Type != MethodOAuth {
		t.Fatalf("expected saved oauth method, got %q", state.Method.Type)
	}
}

func TestEnvAPIKeyOverrideStoreSaveDelegatesToBaseStore(t *testing.T) {
	base := NewMemoryStore(EmptyState())
	store := NewEnvAPIKeyOverrideStore(base, func(string) (string, bool) { return "", false })

	want := State{
		Scope: ScopeGlobal,
		Method: Method{
			Type:   MethodAPIKey,
			APIKey: &APIKeyMethod{Key: "sk-saved"},
		},
		UpdatedAt: time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC),
	}
	if err := store.Save(context.Background(), want); err != nil {
		t.Fatalf("save auth state: %v", err)
	}

	loaded, err := base.Load(context.Background())
	if err != nil {
		t.Fatalf("load delegated state: %v", err)
	}
	if loaded.Method.Type != MethodAPIKey {
		t.Fatalf("expected delegated api key save, got %q", loaded.Method.Type)
	}
	if loaded.Method.APIKey == nil || loaded.Method.APIKey.Key != "sk-saved" {
		t.Fatalf("expected delegated saved key, got %+v", loaded.Method.APIKey)
	}
}

func TestEnvAPIKeyOverrideStoreLoadPersistedReturnsBaseState(t *testing.T) {
	base := NewMemoryStore(State{
		Scope: ScopeGlobal,
		Method: Method{
			Type: MethodOAuth,
			OAuth: &OAuthMethod{
				AccessToken:  "oauth-access",
				RefreshToken: "oauth-refresh",
				TokenType:    "Bearer",
				Expiry:       time.Date(2026, time.January, 1, 11, 0, 0, 0, time.UTC),
			},
		},
	})
	store := NewEnvAPIKeyOverrideStore(base, func(key string) (string, bool) {
		if key == "OPENAI_API_KEY" {
			return "sk-env", true
		}
		return "", false
	})

	loaded, err := store.LoadPersisted(context.Background())
	if err != nil {
		t.Fatalf("load persisted state: %v", err)
	}
	if loaded.Method.Type != MethodOAuth {
		t.Fatalf("expected base oauth state, got %q", loaded.Method.Type)
	}
}
