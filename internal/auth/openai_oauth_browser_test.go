package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompleteOpenAIBrowserFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			if got := r.Form.Get("grant_type"); got != "authorization_code" {
				t.Fatalf("grant_type=%q", got)
			}
			if got := r.Form.Get("code"); got != "auth-code-1" {
				t.Fatalf("code=%q", got)
			}
			if got := r.Form.Get("redirect_uri"); got != "http://127.0.0.1:5555/callback" {
				t.Fatalf("redirect_uri=%q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "browser-access",
				"refresh_token": "browser-refresh",
				"token_type":    "Bearer",
				"expires_in":    1800,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	session, err := BeginOpenAIBrowserFlow(OpenAIOAuthOptions{
		Issuer:     server.URL,
		ClientID:   "client-1",
		HTTPClient: server.Client(),
	}, "http://127.0.0.1:5555/callback")
	if err != nil {
		t.Fatalf("begin flow: %v", err)
	}

	method, err := CompleteOpenAIBrowserFlow(context.Background(), OpenAIOAuthOptions{
		Issuer:     server.URL,
		ClientID:   "client-1",
		HTTPClient: server.Client(),
	}, session, "http://127.0.0.1:5555/callback?code=auth-code-1&state="+session.State)
	if err != nil {
		t.Fatalf("complete flow: %v", err)
	}
	if method.Type != MethodOAuth || method.OAuth == nil {
		t.Fatalf("unexpected method: %+v", method)
	}
	if method.OAuth.AccessToken != "browser-access" || method.OAuth.RefreshToken != "browser-refresh" {
		t.Fatalf("unexpected tokens: %+v", method.OAuth)
	}
}

func TestCompleteOpenAIBrowserFlowRejectsStateMismatch(t *testing.T) {
	session := BrowserAuthSession{
		State:        "expected",
		CodeVerifier: "verifier",
		RedirectURI:  "http://127.0.0.1:5555/callback",
	}
	_, err := CompleteOpenAIBrowserFlow(context.Background(), OpenAIOAuthOptions{}, session, "http://127.0.0.1:5555/callback?code=c1&state=wrong")
	if err == nil {
		t.Fatal("expected state mismatch error")
	}
}

func TestParseOAuthCallbackInput(t *testing.T) {
	parsed, err := ParseOAuthCallbackInput("http://localhost/callback?code=abc&state=s1")
	if err != nil {
		t.Fatalf("parse url callback: %v", err)
	}
	if parsed.Code != "abc" || parsed.State != "s1" {
		t.Fatalf("unexpected parsed callback: %+v", parsed)
	}

	parsed, err = ParseOAuthCallbackInput("code=abc&state=s2")
	if err != nil {
		t.Fatalf("parse query callback: %v", err)
	}
	if parsed.Code != "abc" || parsed.State != "s2" {
		t.Fatalf("unexpected parsed callback: %+v", parsed)
	}

	parsed, err = ParseOAuthCallbackInput("abc")
	if err != nil {
		t.Fatalf("parse plain callback: %v", err)
	}
	if parsed.Code != "abc" {
		t.Fatalf("unexpected parsed callback: %+v", parsed)
	}
}
