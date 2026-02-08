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

func TestIsNonRetriableModelError(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404} {
		if !IsNonRetriableModelError(&APIStatusError{StatusCode: status, Body: "x"}) {
			t.Fatalf("expected %d to be non-retriable", status)
		}
	}
	for _, status := range []int{408, 409, 429, 500} {
		if IsNonRetriableModelError(&APIStatusError{StatusCode: status, Body: "x"}) {
			t.Fatalf("did not expect %d to be non-retriable", status)
		}
	}
	if !IsNonRetriableModelError(&AuthError{Err: errors.New("token refresh failed")}) {
		t.Fatal("expected AuthError to be non-retriable")
	}
}
