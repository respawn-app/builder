package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
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

func TestEffectiveSessionIDPrefersContinueAlias(t *testing.T) {
	got, err := effectiveSessionID(commonFlags{SessionID: "abc", ContinueID: "abc"})
	if err != nil {
		t.Fatalf("effective session id: %v", err)
	}
	if got != "abc" {
		t.Fatalf("session id = %q, want abc", got)
	}

	got, err = effectiveSessionID(commonFlags{ContinueID: "xyz"})
	if err != nil {
		t.Fatalf("effective session id: %v", err)
	}
	if got != "xyz" {
		t.Fatalf("session id = %q, want xyz", got)
	}

	if _, err := effectiveSessionID(commonFlags{SessionID: "abc", ContinueID: "xyz"}); err == nil {
		t.Fatal("expected conflicting --session/--continue error")
	}
}

func TestBuildRunContinueCommandAndHint(t *testing.T) {
	if got := buildRunContinueCommand(""); got != "" {
		t.Fatalf("expected empty command for empty session id, got %q", got)
	}
	command := buildRunContinueCommand("session-123")
	if command != "builder run --continue session-123 \"follow-up\"" {
		t.Fatalf("unexpected continue command: %q", command)
	}
	hint := buildRunContinueHint("session-123")
	if !strings.Contains(hint, command) {
		t.Fatalf("expected continue hint to include command, got %q", hint)
	}
}

func TestEmitRunFinalTextIncludesContinuationHint(t *testing.T) {
	var out bytes.Buffer
	emitRunFinalText(&out, "done", "To continue this run, execute `builder run --continue session-123 \"follow-up\"`.")
	got := out.String()
	if !strings.Contains(got, "done\n\nTo continue this run") {
		t.Fatalf("unexpected final-text output: %q", got)
	}
}

func TestMarkExplicitCommonFlagsTracksOnlyParsedFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := registerCommonFlags(fs)
	if err := fs.Parse([]string{"--workspace", "/tmp/w", "--openai-base-url=http://local/v1", "prompt"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	markExplicitCommonFlags(fs, &flags)
	if !flags.WorkspaceExplicit {
		t.Fatal("expected workspace override to be marked explicit")
	}
	if !flags.OpenAIBaseURLExplicit {
		t.Fatal("expected openai base url override to be marked explicit")
	}
}

func TestMarkExplicitCommonFlagsIgnoresFlagTextInsidePrompt(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := registerCommonFlags(fs)
	prompt := "please keep --workspace unchanged and ignore --openai-base-url"
	if err := fs.Parse([]string{"--continue", "session-123", prompt}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	markExplicitCommonFlags(fs, &flags)
	if flags.WorkspaceExplicit {
		t.Fatal("did not expect prompt text to mark workspace explicit")
	}
	if flags.OpenAIBaseURLExplicit {
		t.Fatal("did not expect prompt text to mark openai base url explicit")
	}
}
