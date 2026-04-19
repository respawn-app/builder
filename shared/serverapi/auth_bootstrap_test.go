package serverapi

import (
	"strings"
	"testing"
)

func TestAuthCompleteBootstrapRequestValidateRequiresOAuthStateForBrowserCallback(t *testing.T) {
	err := AuthCompleteBootstrapRequest{
		Mode:              AuthBootstrapModeBrowserCallbackCode,
		CallbackInput:     "code=abc",
		RedirectURI:       "http://localhost/callback",
		OAuthCodeVerifier: "verifier",
	}.Validate()
	if err == nil || !strings.Contains(err.Error(), "oauth_state is required") {
		t.Fatalf("Validate error = %v, want oauth_state is required", err)
	}
}
