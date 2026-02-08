package llm

import (
	"errors"
	"testing"
)

func TestIsAuthenticationError(t *testing.T) {
	if !IsAuthenticationError(&APIStatusError{StatusCode: 401, Body: "unauthorized"}) {
		t.Fatal("expected 401 to be auth error")
	}
	if !IsAuthenticationError(&APIStatusError{StatusCode: 403, Body: "forbidden"}) {
		t.Fatal("expected 403 to be auth error")
	}
	if IsAuthenticationError(&APIStatusError{StatusCode: 429, Body: "rate limit"}) {
		t.Fatal("did not expect 429 to be auth error")
	}
	if !IsAuthenticationError(&AuthError{Err: errors.New("token refresh failed")}) {
		t.Fatal("expected AuthError to be auth error")
	}
}
