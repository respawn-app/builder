package authstatus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"builder/server/auth"
)

func TestFetchUsagePayloadHandlesNonOAuthState(t *testing.T) {
	var accountHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accountHeader = r.Header.Get("ChatGPT-Account-Id")
		_ = json.NewEncoder(w).Encode(usagePayload{
			PlanType: "pro",
		})
	}))
	defer server.Close()

	_, err := fetchUsagePayload(context.Background(), server.URL, auth.State{Method: auth.Method{Type: auth.MethodAPIKey, APIKey: &auth.APIKeyMethod{Key: "test-key"}}})
	if err != nil {
		t.Fatalf("fetchUsagePayload: %v", err)
	}
	if accountHeader != "" {
		t.Fatalf("ChatGPT-Account-Id = %q, want empty for API key auth", accountHeader)
	}
}
