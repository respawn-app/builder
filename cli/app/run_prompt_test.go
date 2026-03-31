package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"builder/server/auth"
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools/askquestion"
	"builder/shared/config"
	"builder/shared/serverapi"
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
	if strings.Contains(text, "AssistantDelta") {
		t.Fatalf("unexpected assistant delta in progress output: %q", text)
	}
	if !strings.Contains(text, "Running tool") {
		t.Fatalf("expected tool call started in progress output, got %q", text)
	}
	if !strings.Contains(text, "Review finished") {
		t.Fatalf("expected reviewer completed in progress output, got %q", text)
	}
}

func TestRunPromptAskHandlerReturnsError(t *testing.T) {
	_, err := runPromptAskHandler(askquestion.Request{Question: "Need approval?"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "You can't ask questions") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunPromptWithoutAuthReturnsErrAuthNotConfiguredWithoutReadingStdin(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "")

	originalStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdin: %v", err)
	}
	_ = w.Close()
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = originalStdin
		_ = r.Close()
	})

	_, err = RunPrompt(context.Background(), Options{WorkspaceRoot: workspace}, "hello", 0, nil)
	if !errors.Is(err, auth.ErrAuthNotConfigured) {
		t.Fatalf("expected auth not configured without stdin prompt, got %v", err)
	}
}

func TestRunPromptCreatesSessionAndPersistsDurableTranscript(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got == "" {
			t.Fatal("expected authorization header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":11,\"output_tokens\":7,\"total_tokens\":18},\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello from fake\"}]}]}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer server.Close()

	result, err := RunPrompt(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	}, "hello from user", 0, nil)
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if result.Result != "hello from fake" {
		t.Fatalf("result = %q, want %q", result.Result, "hello from fake")
	}
	if strings.TrimSpace(result.SessionID) == "" {
		t.Fatal("expected session id")
	}
	if !strings.HasSuffix(result.SessionName, " "+subagentSessionSuffix) {
		t.Fatalf("expected subagent session name, got %q", result.SessionName)
	}

	cfg, err := config.Load(workspace, config.LoadOptions{OpenAIBaseURL: server.URL})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	store, err := session.OpenByID(cfg.PersistenceRoot, result.SessionID)
	if err != nil {
		t.Fatalf("open session by id: %v", err)
	}
	meta := store.Meta()
	if meta.WorkspaceRoot != cfg.WorkspaceRoot {
		t.Fatalf("workspace root = %q, want %q", meta.WorkspaceRoot, cfg.WorkspaceRoot)
	}
	if meta.FirstPromptPreview != "hello from user" {
		t.Fatalf("first prompt preview = %q, want %q", meta.FirstPromptPreview, "hello from user")
	}
	if meta.Continuation == nil || meta.Continuation.OpenAIBaseURL != server.URL {
		t.Fatalf("unexpected continuation context: %+v", meta.Continuation)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var (
		sawUser      bool
		sawAssistant bool
	)
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var msg llm.Message
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			t.Fatalf("unmarshal message payload: %v", err)
		}
		if msg.Role == llm.RoleUser && msg.Content == "hello from user" {
			sawUser = true
		}
		if msg.Role == llm.RoleAssistant && msg.Content == "hello from fake" && msg.Phase == llm.MessagePhaseFinal {
			sawAssistant = true
		}
	}
	if !sawUser {
		t.Fatal("expected persisted user message in event log")
	}
	if !sawAssistant {
		t.Fatal("expected persisted final assistant message in event log")
	}
}

func TestHeadlessRunPromptClientResumesExistingSessionByID(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	server, hits := newFakeResponsesServer(t, []string{"first response", "second response"})
	defer server.Close()

	created, err := RunPrompt(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	}, "first prompt", 0, nil)
	if err != nil {
		t.Fatalf("initial RunPrompt: %v", err)
	}

	boot, err := bootstrapApp(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		SessionID:             created.SessionID,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	defer func() {
		if boot.background != nil {
			_ = boot.background.Close()
		}
	}()

	runClient := newHeadlessRunPromptClient(boot)
	resumed, err := runClient.RunPrompt(context.Background(), serverapi.RunPromptRequest{
		ClientRequestID:   "req-resume-1",
		SelectedSessionID: created.SessionID,
		Prompt:            "second prompt",
	}, nil)
	if err != nil {
		t.Fatalf("resumed client RunPrompt: %v", err)
	}
	if resumed.SessionID != created.SessionID {
		t.Fatalf("resumed session id = %q, want %q", resumed.SessionID, created.SessionID)
	}
	if resumed.Result != "second response" {
		t.Fatalf("resumed result = %q, want %q", resumed.Result, "second response")
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("fake response server hit count = %d, want 2", got)
	}

	store, err := openWorkspaceSessionStore(workspace, server.URL, created.SessionID)
	if err != nil {
		t.Fatalf("open workspace session store: %v", err)
	}
	messages, err := readStoredMessages(store)
	if err != nil {
		t.Fatalf("read stored messages: %v", err)
	}
	assertMessagePresent(t, messages, llm.RoleUser, "first prompt")
	assertMessagePresent(t, messages, llm.RoleAssistant, "first response")
	assertMessagePresent(t, messages, llm.RoleUser, "second prompt")
	assertMessagePresent(t, messages, llm.RoleAssistant, "second response")
	if got := store.Meta().FirstPromptPreview; got != "first prompt" {
		t.Fatalf("first prompt preview = %q, want %q", got, "first prompt")
	}
}

func TestHeadlessRunPromptClientRestoresContinuationContextFromSelectedSession(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	server, hits := newFakeResponsesServer(t, []string{"created via explicit base url", "resumed via continuation"})
	defer server.Close()

	created, err := RunPrompt(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	}, "first prompt", 0, nil)
	if err != nil {
		t.Fatalf("initial RunPrompt: %v", err)
	}

	boot, err := bootstrapApp(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		SessionID:             created.SessionID,
		Model:                 "gpt-5",
	}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	defer func() {
		if boot.background != nil {
			_ = boot.background.Close()
		}
	}()

	runClient := newHeadlessRunPromptClient(boot)
	resumed, err := runClient.RunPrompt(context.Background(), serverapi.RunPromptRequest{
		ClientRequestID:   "req-resume-2",
		SelectedSessionID: created.SessionID,
		Prompt:            "second prompt",
	}, nil)
	if err != nil {
		t.Fatalf("resumed client RunPrompt: %v", err)
	}
	if resumed.Result != "resumed via continuation" {
		t.Fatalf("resumed result = %q, want %q", resumed.Result, "resumed via continuation")
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("fake response server hit count = %d, want 2", got)
	}

	store, err := openWorkspaceSessionStore(workspace, server.URL, created.SessionID)
	if err != nil {
		t.Fatalf("open workspace session store: %v", err)
	}
	if store.Meta().Continuation == nil || store.Meta().Continuation.OpenAIBaseURL != server.URL {
		t.Fatalf("unexpected continuation context: %+v", store.Meta().Continuation)
	}
}

func newFakeResponsesServer(t *testing.T, assistantReplies []string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got == "" {
			t.Fatal("expected authorization header")
		}
		index := int(hits.Add(1)) - 1
		if index >= len(assistantReplies) {
			t.Fatalf("unexpected response request index %d", index)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":11,\"output_tokens\":7,\"total_tokens\":18},\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}]}}\n\n", assistantReplies[index])
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	return server, &hits
}

func openWorkspaceSessionStore(workspaceRoot, openAIBaseURL, sessionID string) (*session.Store, error) {
	loadOpts := config.LoadOptions{}
	if strings.TrimSpace(openAIBaseURL) != "" {
		loadOpts.OpenAIBaseURL = openAIBaseURL
	}
	cfg, err := config.Load(workspaceRoot, loadOpts)
	if err != nil {
		return nil, err
	}
	return session.OpenByID(cfg.PersistenceRoot, sessionID)
}

func readStoredMessages(store *session.Store) ([]llm.Message, error) {
	events, err := store.ReadEvents()
	if err != nil {
		return nil, err
	}
	messages := make([]llm.Message, 0, len(events))
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var msg llm.Message
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func assertMessagePresent(t *testing.T, messages []llm.Message, role llm.Role, content string) {
	t.Helper()
	for _, msg := range messages {
		if msg.Role == role && msg.Content == content {
			return
		}
	}
	t.Fatalf("expected message role=%s content=%q in %+v", role, content, messages)
}
