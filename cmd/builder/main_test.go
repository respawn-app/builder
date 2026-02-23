package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestParseRunTimeoutDefaultsToInfinite(t *testing.T) {
	got, err := parseRunTimeout("")
	if err != nil {
		t.Fatalf("parse run timeout: %v", err)
	}
	if got != 0 {
		t.Fatalf("timeout = %v, want 0", got)
	}
}

func TestParseRunTimeoutRejectsInvalid(t *testing.T) {
	if _, err := parseRunTimeout("not-a-duration"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRunTimeoutParsesDuration(t *testing.T) {
	got, err := parseRunTimeout("2m")
	if err != nil {
		t.Fatalf("parse run timeout: %v", err)
	}
	if got != 2*time.Minute {
		t.Fatalf("timeout = %v, want %v", got, 2*time.Minute)
	}
}

func TestRunErrorCode(t *testing.T) {
	if got := runErrorCode(context.DeadlineExceeded); got != "timeout" {
		t.Fatalf("run error code = %q, want timeout", got)
	}
	if got := runErrorCode(context.Canceled); got != "interrupted" {
		t.Fatalf("run error code = %q, want interrupted", got)
	}
	if got := runErrorCode(errors.New("boom")); got != "runtime" {
		t.Fatalf("run error code = %q, want runtime", got)
	}
}
