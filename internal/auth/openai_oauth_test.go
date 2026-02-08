package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunOpenAIDeviceCodeFlow(t *testing.T) {
	var pollCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_auth_id": "dev-1",
				"user_code":      "ABCD-1234",
				"interval":       "1",
			})
		case "/api/accounts/deviceauth/token":
			if pollCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"status":"pending"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authorization_code": "auth-code-1",
				"code_challenge":     "challenge",
				"code_verifier":      "verifier",
			})
		case "/oauth/token":
			_ = r.ParseForm()
			if got := r.Form.Get("grant_type"); got != "authorization_code" {
				t.Fatalf("unexpected grant_type: %s", got)
			}
			if got := r.Form.Get("code"); got != "auth-code-1" {
				t.Fatalf("unexpected code: %s", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "access-1",
				"refresh_token": "refresh-1",
				"token_type":    "Bearer",
				"expires_in":    1800,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	var shown DeviceCode
	method, err := RunOpenAIDeviceCodeFlow(context.Background(), OpenAIOAuthOptions{
		Issuer:      server.URL,
		ClientID:    "client-1",
		HTTPClient:  server.Client(),
		PollTimeout: 10 * time.Second,
	}, func(code DeviceCode) {
		shown = code
	})
	if err != nil {
		t.Fatalf("device code flow failed: %v", err)
	}
	if shown.UserCode != "ABCD-1234" {
		t.Fatalf("unexpected shown user code: %+v", shown)
	}
	if method.Type != MethodOAuth || method.OAuth == nil {
		t.Fatalf("unexpected method returned: %+v", method)
	}
	if method.OAuth.AccessToken != "access-1" || method.OAuth.RefreshToken != "refresh-1" {
		t.Fatalf("unexpected oauth tokens: %+v", method.OAuth)
	}
	if !method.OAuth.Expiry.After(time.Now().UTC()) {
		t.Fatalf("expected future expiry, got %s", method.OAuth.Expiry)
	}
}

func TestRequestOpenAIDeviceCodeUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := requestOpenAIDeviceCode(context.Background(), OpenAIOAuthOptions{
		Issuer:     server.URL,
		ClientID:   "client-1",
		HTTPClient: server.Client(),
	})
	if err != ErrDeviceCodeUnsupported {
		t.Fatalf("expected ErrDeviceCodeUnsupported, got %v", err)
	}
}

func TestRefreshOpenAIAuthToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["grant_type"] != "refresh_token" {
			t.Fatalf("unexpected grant_type: %s", payload["grant_type"])
		}
		if payload["refresh_token"] != "old-refresh" {
			t.Fatalf("unexpected refresh token: %s", payload["refresh_token"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	updated, err := RefreshOpenAIAuthToken(context.Background(), OpenAIOAuthOptions{
		Issuer:     server.URL,
		ClientID:   "client-1",
		HTTPClient: server.Client(),
	}, Method{
		Type: MethodOAuth,
		OAuth: &OAuthMethod{
			AccessToken:  "old-access",
			RefreshToken: "old-refresh",
			TokenType:    "Bearer",
			Expiry:       time.Now().Add(-time.Minute),
		},
	})
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if updated.OAuth.AccessToken != "new-access" || updated.OAuth.RefreshToken != "new-refresh" {
		t.Fatalf("unexpected refreshed tokens: %+v", updated.OAuth)
	}
}
