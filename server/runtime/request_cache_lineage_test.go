package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"builder/server/llm"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/cachewarn"
	"builder/shared/config"
)

func TestGenerateWithRetryClient_PersistsExactNonPostfixCacheWarningInVerboseMode(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{Usage: llm.Usage{InputTokens: 10}}, {Usage: llm.Usage{InputTokens: 12}}}}
	eng, err := New(store, client, tools.NewRegistry(), Config{Model: "gpt-5", CacheWarningMode: config.CacheWarningModeVerbose})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.generateWithRetryClient(context.Background(), "step-1", client, testPromptCacheRequest("cache-key-1", "alpha"), nil, nil, nil); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if _, err := eng.generateWithRetryClient(context.Background(), "step-2", client, testPromptCacheRequest("cache-key-1", "beta"), nil, nil, nil); err != nil {
		t.Fatalf("second generate: %v", err)
	}

	warnings := persistedCacheWarnings(t, store)
	if len(warnings) != 1 {
		t.Fatalf("warning count = %d, want 1", len(warnings))
	}
	if warnings[0].Reason != cachewarn.ReasonNonPostfix {
		t.Fatalf("warning reason = %q, want %q", warnings[0].Reason, cachewarn.ReasonNonPostfix)
	}
}

func TestGenerateWithRetryClient_DefaultModeSuppressesExactNonPostfixWarning(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{Usage: llm.Usage{InputTokens: 10}}, {Usage: llm.Usage{InputTokens: 12}}}}
	eng, err := New(store, client, tools.NewRegistry(), Config{Model: "gpt-5", CacheWarningMode: config.CacheWarningModeDefault})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.generateWithRetryClient(context.Background(), "step-1", client, testPromptCacheRequest("cache-key-1", "alpha"), nil, nil, nil); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if _, err := eng.generateWithRetryClient(context.Background(), "step-2", client, testPromptCacheRequest("cache-key-1", "beta"), nil, nil, nil); err != nil {
		t.Fatalf("second generate: %v", err)
	}

	warnings := persistedCacheWarnings(t, store)
	if len(warnings) != 0 {
		t.Fatalf("warning count = %d, want 0", len(warnings))
	}
}

func TestGenerateWithRetryClient_PersistsVerboseReuseDropWarning(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{Usage: llm.Usage{InputTokens: 10, HasCachedInputTokens: true, CachedInputTokens: 4}}, {Usage: llm.Usage{InputTokens: 12, HasCachedInputTokens: true, CachedInputTokens: 0}}}}
	eng, err := New(store, client, tools.NewRegistry(), Config{Model: "gpt-5", CacheWarningMode: config.CacheWarningModeVerbose})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.generateWithRetryClient(context.Background(), "step-1", client, testPromptCacheRequest("cache-key-1", "alpha"), nil, nil, nil); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if _, err := eng.generateWithRetryClient(context.Background(), "step-2", client, testPromptCacheRequest("cache-key-1", "alpha", "omega"), nil, nil, nil); err != nil {
		t.Fatalf("second generate: %v", err)
	}

	warnings := persistedCacheWarnings(t, store)
	if len(warnings) != 1 {
		t.Fatalf("warning count = %d, want 1", len(warnings))
	}
	if warnings[0].Reason != cachewarn.ReasonReuseDropped {
		t.Fatalf("warning reason = %q, want %q", warnings[0].Reason, cachewarn.ReasonReuseDropped)
	}
}

func TestGenerateWithRetryClient_DoesNotWarnAcrossDistinctCacheKeys(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{Usage: llm.Usage{InputTokens: 10}}, {Usage: llm.Usage{InputTokens: 12}}}}
	eng, err := New(store, client, tools.NewRegistry(), Config{Model: "gpt-5", CacheWarningMode: config.CacheWarningModeVerbose})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.generateWithRetryClient(context.Background(), "step-1", client, testPromptCacheRequest("cache-key-1", "alpha"), nil, nil, nil); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if _, err := eng.generateWithRetryClient(context.Background(), "step-2", client, testPromptCacheRequest("cache-key-2", "beta"), nil, nil, nil); err != nil {
		t.Fatalf("second generate: %v", err)
	}

	warnings := persistedCacheWarnings(t, store)
	if len(warnings) != 0 {
		t.Fatalf("warning count = %d, want 0", len(warnings))
	}
}

func TestGenerateWithRetryClient_KeepsReviewerLineageIndependent(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{Usage: llm.Usage{InputTokens: 10}}, {Usage: llm.Usage{InputTokens: 10}}, {Usage: llm.Usage{InputTokens: 12}}, {Usage: llm.Usage{InputTokens: 12}}}}
	eng, err := New(store, client, tools.NewRegistry(), Config{Model: "gpt-5", CacheWarningMode: config.CacheWarningModeVerbose})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.generateWithRetryClient(context.Background(), "step-1", client, testPromptCacheRequest("cache-key-1", "alpha"), nil, nil, nil); err != nil {
		t.Fatalf("conversation first generate: %v", err)
	}
	if _, err := eng.generateWithRetryClient(context.Background(), "step-2", client, testReviewerPromptCacheRequest("cache-key-1-review", "beta"), nil, nil, nil); err != nil {
		t.Fatalf("reviewer first generate: %v", err)
	}
	if _, err := eng.generateWithRetryClient(context.Background(), "step-3", client, testPromptCacheRequest("cache-key-1", "alpha", "omega"), nil, nil, nil); err != nil {
		t.Fatalf("conversation postfix generate: %v", err)
	}
	if _, err := eng.generateWithRetryClient(context.Background(), "step-4", client, testReviewerPromptCacheRequest("cache-key-1-review", "gamma"), nil, nil, nil); err != nil {
		t.Fatalf("reviewer non-postfix generate: %v", err)
	}

	warnings := persistedCacheWarnings(t, store)
	if len(warnings) != 1 {
		t.Fatalf("warning count = %d, want 1", len(warnings))
	}
	if warnings[0].Reason != cachewarn.ReasonNonPostfix {
		t.Fatalf("warning reason = %q, want %q", warnings[0].Reason, cachewarn.ReasonNonPostfix)
	}
	if warnings[0].Scope != cachewarn.ScopeReviewer {
		t.Fatalf("warning scope = %q, want %q", warnings[0].Scope, cachewarn.ScopeReviewer)
	}
}

func TestGenerateWithRetryClient_UsesCompactionWarningForNextExactBreak(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{Usage: llm.Usage{InputTokens: 10}}, {Usage: llm.Usage{InputTokens: 12}}}}
	eng, err := New(store, client, tools.NewRegistry(), Config{Model: "gpt-5", CacheWarningMode: config.CacheWarningModeVerbose})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.generateWithRetryClient(context.Background(), "step-1", client, testPromptCacheRequest(store.Meta().SessionID, "alpha"), nil, nil, nil); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if err := eng.replaceHistory("step-compact", "local", compactionModeManual, llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleAssistant, MessageType: llm.MessageTypeCompactionSummary, Content: "summary"}})); err != nil {
		t.Fatalf("replace history: %v", err)
	}
	if len(persistedCacheWarnings(t, store)) != 0 {
		t.Fatal("expected compaction to defer cache warning until next same-key exact break")
	}
	if _, err := eng.generateWithRetryClient(context.Background(), "step-2", client, testPromptCacheRequest(store.Meta().SessionID, "beta"), nil, nil, nil); err != nil {
		t.Fatalf("second generate: %v", err)
	}

	warnings := persistedCacheWarnings(t, store)
	if len(warnings) != 1 {
		t.Fatalf("warning count = %d, want 1", len(warnings))
	}
	if warnings[0].Reason != cachewarn.ReasonCompaction {
		t.Fatalf("warning reason = %q, want %q", warnings[0].Reason, cachewarn.ReasonCompaction)
	}
}

func TestGenerateWithRetryClient_RestoresCompactionInvalidationAcrossEngineReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{Usage: llm.Usage{InputTokens: 10}}}}
	eng, err := New(store, client, tools.NewRegistry(), Config{Model: "gpt-5", CacheWarningMode: config.CacheWarningModeVerbose})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.generateWithRetryClient(context.Background(), "step-1", client, testPromptCacheRequest(store.Meta().SessionID, "alpha"), nil, nil, nil); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if err := eng.replaceHistory("step-compact", "local", compactionModeManual, llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleAssistant, MessageType: llm.MessageTypeCompactionSummary, Content: "summary"}})); err != nil {
		t.Fatalf("replace history: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	reopened, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	reopenedClient := &fakeClient{responses: []llm.Response{{Usage: llm.Usage{InputTokens: 12}}}}
	reopenedEng, err := New(reopened, reopenedClient, tools.NewRegistry(), Config{Model: "gpt-5", CacheWarningMode: config.CacheWarningModeVerbose})
	if err != nil {
		t.Fatalf("new reopened engine: %v", err)
	}

	if _, err := reopenedEng.generateWithRetryClient(context.Background(), "step-2", reopenedClient, testPromptCacheRequest(reopened.Meta().SessionID, "beta"), nil, nil, nil); err != nil {
		t.Fatalf("generate after reopen: %v", err)
	}

	warnings := persistedCacheWarnings(t, reopened)
	if len(warnings) != 1 {
		t.Fatalf("warning count = %d, want 1", len(warnings))
	}
	if warnings[0].Reason != cachewarn.ReasonCompaction {
		t.Fatalf("warning reason = %q, want %q", warnings[0].Reason, cachewarn.ReasonCompaction)
	}
}

func TestGenerateWithRetryClient_DoesNotInventCompactionCauseWithoutPriorLineageOnReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("legacy-compact", "history_replaced", historyReplacementPayload{
		Engine: "local",
		Mode:   string(compactionModeManual),
		Items:  llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleAssistant, MessageType: llm.MessageTypeCompactionSummary, Content: "summary"}}),
	}); err != nil {
		t.Fatalf("append history_replaced: %v", err)
	}

	reopened, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{Usage: llm.Usage{InputTokens: 12}}}}
	eng, err := New(reopened, client, tools.NewRegistry(), Config{Model: "gpt-5", CacheWarningMode: config.CacheWarningModeVerbose})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.generateWithRetryClient(context.Background(), "step-1", client, testPromptCacheRequest(reopened.Meta().SessionID, "beta"), nil, nil, nil); err != nil {
		t.Fatalf("generate after reopen: %v", err)
	}

	warnings := persistedCacheWarnings(t, reopened)
	if len(warnings) != 0 {
		t.Fatalf("warning count = %d, want 0", len(warnings))
	}
}

func testPromptCacheRequest(cacheKey string, messages ...string) llm.Request {
	items := make([]llm.ResponseItem, 0, len(messages))
	for _, message := range messages {
		items = append(items, llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleUser, Content: message}})...)
	}
	return llm.Request{
		Model:            "gpt-5",
		SystemPrompt:     "system",
		PromptCacheKey:   cacheKey,
		PromptCacheScope: cachewarn.ScopeConversation,
		Items:            items,
	}
}

func testReviewerPromptCacheRequest(cacheKey string, messages ...string) llm.Request {
	request := testPromptCacheRequest(cacheKey, messages...)
	request.PromptCacheScope = cachewarn.ScopeReviewer
	return request
}

func persistedCacheWarnings(t *testing.T, store *session.Store) []cachewarn.Warning {
	t.Helper()
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	warnings := make([]cachewarn.Warning, 0, len(events))
	for _, evt := range events {
		if evt.Kind != sessionEventCacheWarning {
			continue
		}
		var warning cachewarn.Warning
		if err := json.Unmarshal(evt.Payload, &warning); err != nil {
			t.Fatalf("decode warning: %v", err)
		}
		warnings = append(warnings, warning)
	}
	return warnings
}
