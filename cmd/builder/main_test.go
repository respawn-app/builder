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

func TestParseRunOutputMode(t *testing.T) {
	got, err := parseRunOutputMode("final-text")
	if err != nil {
		t.Fatalf("parse output mode: %v", err)
	}
	if got != runOutputModeFinalText {
		t.Fatalf("output mode = %q, want %q", got, runOutputModeFinalText)
	}
	got, err = parseRunOutputMode("json")
	if err != nil {
		t.Fatalf("parse output mode: %v", err)
	}
	if got != runOutputModeJSON {
		t.Fatalf("output mode = %q, want %q", got, runOutputModeJSON)
	}
	if _, err := parseRunOutputMode("verbose"); err == nil {
		t.Fatal("expected invalid output mode error")
	}
}

func TestParseRunProgressMode(t *testing.T) {
	got, err := parseRunProgressMode("quiet")
	if err != nil {
		t.Fatalf("parse progress mode: %v", err)
	}
	if got != runProgressModeQuiet {
		t.Fatalf("progress mode = %q, want %q", got, runProgressModeQuiet)
	}
	got, err = parseRunProgressMode("stderr")
	if err != nil {
		t.Fatalf("parse progress mode: %v", err)
	}
	if got != runProgressModeStderr {
		t.Fatalf("progress mode = %q, want %q", got, runProgressModeStderr)
	}
	if _, err := parseRunProgressMode("chatty"); err == nil {
		t.Fatal("expected invalid progress mode error")
	}
}
