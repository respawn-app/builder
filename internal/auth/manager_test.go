package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestSwitchMethodRequiresIdle(t *testing.T) {
	now := time.Date(2026, time.January, 1, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(State{
		Scope: ScopeGlobal,
		Method: Method{
			Type: MethodAPIKey,
			APIKey: &APIKeyMethod{
				Key: "old-key",
			},
		},
		UpdatedAt: now,
	})
	mgr := NewManager(store, nil, func() time.Time { return now.Add(time.Minute) })

	_, err := mgr.SwitchMethod(context.Background(), Method{
		Type: MethodOAuth,
		OAuth: &OAuthMethod{
			AccessToken:  "token-a",
			RefreshToken: "refresh-a",
			TokenType:    "Bearer",
			Expiry:       now.Add(time.Hour),
		},
	}, false)
	if !errors.Is(err, ErrSwitchRequiresIdle) {
		t.Fatalf("expected ErrSwitchRequiresIdle, got %v", err)
	}

	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.Method.Type != MethodAPIKey {
		t.Fatalf("expected api key method to remain unchanged, got %q", state.Method.Type)
	}
	if state.Method.APIKey == nil || state.Method.APIKey.Key != "old-key" {
		t.Fatalf("unexpected api key state after failed switch: %+v", state.Method.APIKey)
	}
}

func TestAuthorizationHeaderSurfacesOAuthRefreshFailure(t *testing.T) {
	now := time.Date(2026, time.January, 1, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(State{
		Scope: ScopeGlobal,
		Method: Method{
			Type: MethodOAuth,
			OAuth: &OAuthMethod{
				AccessToken:  "stale-token",
				RefreshToken: "refresh-token",
				TokenType:    "Bearer",
				Expiry:       now.Add(-time.Minute),
			},
		},
		UpdatedAt: now,
	})

	refreshErr := errors.New("refresh failed")
	refresher := NewOAuthRefresher(stubTokenFactory{source: stubTokenSource{err: refreshErr}}, func() time.Time {
		return now
	}, 30*time.Second)
	mgr := NewManager(store, refresher, func() time.Time { return now })

	_, err := mgr.AuthorizationHeader(context.Background())
	if !errors.Is(err, ErrOAuthRefreshFailed) {
		t.Fatalf("expected ErrOAuthRefreshFailed, got %v", err)
	}

	state, loadErr := store.Load(context.Background())
	if loadErr != nil {
		t.Fatalf("load state: %v", loadErr)
	}
	if state.Method.OAuth == nil || state.Method.OAuth.AccessToken != "stale-token" {
		t.Fatalf("oauth state changed on refresh failure: %+v", state.Method.OAuth)
	}
}

type stubTokenFactory struct {
	source OAuthTokenSource
}

func (f stubTokenFactory) TokenSource(context.Context, oauth2.Token) OAuthTokenSource {
	return f.source
}

type stubTokenSource struct {
	tok *oauth2.Token
	err error
}

func (s stubTokenSource) Token() (*oauth2.Token, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.tok, nil
}
