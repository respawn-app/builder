package app

import (
	"bytes"
	"strings"
	"testing"

	"builder/internal/runtime"
	"builder/internal/session"
	"builder/internal/tools/askquestion"
)

func TestEnsureSubagentSessionNameSetsDefault(t *testing.T) {
	containerDir := t.TempDir()
	store, err := session.NewLazy(containerDir, "workspace-x", "/tmp/workspace")
	if err != nil {
		t.Fatalf("new lazy session: %v", err)
	}

	if err := ensureSubagentSessionName(store); err != nil {
		t.Fatalf("ensure subagent session name: %v", err)
	}

	meta := store.Meta()
	want := meta.SessionID + " " + subagentSessionSuffix
	if meta.Name != want {
		t.Fatalf("session name = %q, want %q", meta.Name, want)
	}
}

func TestEnsureSubagentSessionNamePreservesExistingName(t *testing.T) {
	containerDir := t.TempDir()
	store, err := session.NewLazy(containerDir, "workspace-x", "/tmp/workspace")
	if err != nil {
		t.Fatalf("new lazy session: %v", err)
	}
	if err := store.SetName("incident triage"); err != nil {
		t.Fatalf("set name: %v", err)
	}

	if err := ensureSubagentSessionName(store); err != nil {
		t.Fatalf("ensure subagent session name: %v", err)
	}

	if got := store.Meta().Name; got != "incident triage" {
		t.Fatalf("session name = %q, want incident triage", got)
	}
}

func TestWriteRunProgressEventOnlyWritesSelectedKinds(t *testing.T) {
	var out bytes.Buffer

	writeRunProgressEvent(&out, runtime.Event{Kind: runtime.EventAssistantDelta, StepID: "s1", AssistantDelta: "hello"})
	writeRunProgressEvent(&out, runtime.Event{Kind: runtime.EventToolCallStarted, StepID: "s1"})
	writeRunProgressEvent(&out, runtime.Event{Kind: runtime.EventReviewerCompleted, StepID: "s1", Reviewer: &runtime.ReviewerStatus{Outcome: "no_suggestions"}})

	text := out.String()
	if strings.Contains(text, string(runtime.EventAssistantDelta)) {
		t.Fatalf("unexpected assistant delta in progress output: %q", text)
	}
	if !strings.Contains(text, string(runtime.EventToolCallStarted)) {
		t.Fatalf("expected tool call started in progress output, got %q", text)
	}
	if !strings.Contains(text, string(runtime.EventReviewerCompleted)) {
		t.Fatalf("expected reviewer completed in progress output, got %q", text)
	}
}

func TestRunPromptAskHandlerReturnsError(t *testing.T) {
	_, err := runPromptAskHandler(askquestion.Request{Question: "Need approval?"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ask_question is not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}
