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

func TestGenerateWithRetryClient_PersistsExactNonPostfixCacheWarningInDefaultMode(t *testing.T) {
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
	if len(warnings) != 1 {
		t.Fatalf("warning count = %d, want 1", len(warnings))
	}
	if warnings[0].Reason != cachewarn.ReasonNonPostfix {
		t.Fatalf("warning reason = %q, want %q", warnings[0].Reason, cachewarn.ReasonNonPostfix)
	}
}

func TestNew_RejectsInvalidCacheWarningMode(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := New(store, &fakeClient{}, tools.NewRegistry(), Config{Model: "gpt-5", CacheWarningMode: config.CacheWarningMode("bogus")}); err == nil {
		t.Fatal("expected invalid cache_warning_mode to fail")
	}
}

func TestGenerateWithRetryClient_OffModeSuppressesExactNonPostfixWarning(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{Usage: llm.Usage{InputTokens: 10}}, {Usage: llm.Usage{InputTokens: 12}}}}
	eng, err := New(store, client, tools.NewRegistry(), Config{Model: "gpt-5", CacheWarningMode: config.CacheWarningModeOff})
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

func TestGenerateWithRetryClient_FailedRequestDoesNotAdvanceLineage(t *testing.T) {
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
	failingClient := failingCacheClient{caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, SupportsPromptCacheKey: true, IsOpenAIFirstParty: true}}
	if _, err := eng.generateWithRetryClient(context.Background(), "step-2", &failingClient, testPromptCacheRequest("cache-key-1", "beta"), nil, nil, nil); err == nil {
		t.Fatal("expected failed generate")
	}
	if _, err := eng.generateWithRetryClient(context.Background(), "step-3", client, testPromptCacheRequest("cache-key-1", "alpha", "omega"), nil, nil, nil); err != nil {
		t.Fatalf("third generate: %v", err)
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

func TestBuildRequest_SkipsPromptCacheKeyForUnsupportedProvider(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai-compatible", SupportsResponsesAPI: true}}
	eng, err := New(store, client, tools.NewRegistry(), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	req, err := eng.buildRequestWithExtraItems(context.Background(), []llm.ResponseItem{{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "hello"}}, true)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.PromptCacheKey != "" {
		t.Fatalf("PromptCacheKey = %q, want empty", req.PromptCacheKey)
	}
	if req.PromptCacheScope != "" {
		t.Fatalf("PromptCacheScope = %q, want empty", req.PromptCacheScope)
	}
}

func TestBuildRequest_SetsPromptCacheKeyWhenProviderCapabilityEnabled(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai-compatible", SupportsResponsesAPI: true, SupportsPromptCacheKey: true}}
	eng, err := New(store, client, tools.NewRegistry(), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	req, err := eng.buildRequestWithExtraItems(context.Background(), []llm.ResponseItem{{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: "hello"}}, true)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.PromptCacheKey != store.Meta().SessionID {
		t.Fatalf("PromptCacheKey = %q, want %q", req.PromptCacheKey, store.Meta().SessionID)
	}
	if req.PromptCacheScope != cachewarn.ScopeConversation {
		t.Fatalf("PromptCacheScope = %q, want %q", req.PromptCacheScope, cachewarn.ScopeConversation)
	}
}

func TestReviewerSuggestions_SkipsPromptCacheKeyForUnsupportedProvider(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	engineClient := &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, SupportsPromptCacheKey: true, IsOpenAIFirstParty: true}}
	reviewerClient := &fakeClient{responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":[]}`}}}}
	eng, err := New(store, engineClient, tools.NewRegistry(), Config{Model: "gpt-5", Reviewer: ReviewerConfig{Model: "gpt-5"}})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.runReviewerSuggestions(context.Background(), "step-1", reviewerClient); err != nil {
		t.Fatalf("run reviewer suggestions: %v", err)
	}
	if len(reviewerClient.calls) != 1 {
		t.Fatalf("reviewer client calls = %d, want 1", len(reviewerClient.calls))
	}
	if reviewerClient.calls[0].PromptCacheKey != "" {
		t.Fatalf("reviewer PromptCacheKey = %q, want empty", reviewerClient.calls[0].PromptCacheKey)
	}
	if reviewerClient.calls[0].PromptCacheScope != "" {
		t.Fatalf("reviewer PromptCacheScope = %q, want empty", reviewerClient.calls[0].PromptCacheScope)
	}
}

func TestReviewerSuggestions_UsesReviewerClientPromptCacheCapability(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	engineClient := &fakeClient{caps: llm.ProviderCapabilities{ProviderID: "openai-compatible", SupportsResponsesAPI: true}}
	reviewerClient := &fakeClient{
		caps:      llm.ProviderCapabilities{ProviderID: "openai-compatible", SupportsResponsesAPI: true, SupportsPromptCacheKey: true},
		responses: []llm.Response{{Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":[]}`}}},
	}
	eng, err := New(store, engineClient, tools.NewRegistry(), Config{Model: "gpt-5", Reviewer: ReviewerConfig{Model: "gpt-5"}})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.runReviewerSuggestions(context.Background(), "step-1", reviewerClient); err != nil {
		t.Fatalf("run reviewer suggestions: %v", err)
	}
	if len(reviewerClient.calls) != 1 {
		t.Fatalf("reviewer client calls = %d, want 1", len(reviewerClient.calls))
	}
	if reviewerClient.calls[0].PromptCacheKey != reviewerSessionID(store.Meta().SessionID) {
		t.Fatalf("reviewer PromptCacheKey = %q, want %q", reviewerClient.calls[0].PromptCacheKey, reviewerSessionID(store.Meta().SessionID))
	}
	if reviewerClient.calls[0].PromptCacheScope != cachewarn.ScopeReviewer {
		t.Fatalf("reviewer PromptCacheScope = %q, want %q", reviewerClient.calls[0].PromptCacheScope, cachewarn.ScopeReviewer)
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

func TestGenerateWithRetryClient_DefaultModeUsesCompactionWarningForNextExactBreak(t *testing.T) {
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

func TestGenerateWithRetryClient_RestoreIgnoresRequestObservationWithoutResponse(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("legacy-request", sessionEventCacheRequestObserved, persistedCacheRequestObserved{
		DigestVersion: requestCacheDigestVersion,
		CacheKey:      "cache-key-1",
		Scope:         cachewarn.ScopeConversation,
		ChunkCount:    1,
		TerminalHash:  "failed-only-hash",
	}); err != nil {
		t.Fatalf("append request event: %v", err)
	}
	reopened, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{Usage: llm.Usage{InputTokens: 12}}}}
	eng, err := New(reopened, client, tools.NewRegistry(), Config{Model: "gpt-5", CacheWarningMode: config.CacheWarningModeDefault})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.generateWithRetryClient(context.Background(), "step-1", client, testPromptCacheRequest("cache-key-1", "alpha", "omega"), nil, nil, nil); err != nil {
		t.Fatalf("generate after reopen: %v", err)
	}
	warnings := persistedCacheWarnings(t, reopened)
	if len(warnings) != 0 {
		t.Fatalf("warning count = %d, want 0", len(warnings))
	}
}

type failingCacheClient struct {
	caps llm.ProviderCapabilities
}

func (f *failingCacheClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, context.DeadlineExceeded
}

func (f *failingCacheClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return f.caps, nil
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

func TestGenerateWithRetryClient_ReviewerRollbackClearsConversationLineage(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{Usage: llm.Usage{InputTokens: 10}}, {Usage: llm.Usage{InputTokens: 12}}, {Usage: llm.Usage{InputTokens: 14}}}}
	eng, err := New(store, client, tools.NewRegistry(), Config{Model: "gpt-5", CacheWarningMode: config.CacheWarningModeDefault})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.generateWithRetryClient(context.Background(), "step-1", client, testPromptCacheRequest(store.Meta().SessionID, "alpha"), nil, nil, nil); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if _, err := eng.generateWithRetryClient(context.Background(), "step-2", client, testPromptCacheRequest(store.Meta().SessionID, "alpha", "reviewer-feedback"), nil, nil, nil); err != nil {
		t.Fatalf("reviewer follow-up generate: %v", err)
	}
	if err := eng.replaceHistory("step-rollback", "reviewer_rollback", compactionModeManual, llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleUser, Content: "alpha"}})); err != nil {
		t.Fatalf("reviewer rollback replace history: %v", err)
	}
	if _, err := eng.generateWithRetryClient(context.Background(), "step-3", client, testPromptCacheRequest(store.Meta().SessionID, "alpha", "omega"), nil, nil, nil); err != nil {
		t.Fatalf("post-rollback generate: %v", err)
	}

	warnings := persistedCacheWarnings(t, store)
	if len(warnings) != 0 {
		t.Fatalf("warning count = %d, want 0", len(warnings))
	}
}

func TestGenerateWithRetryClient_RestoreSkipsDigestVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	legacyRequest := persistedCacheRequestObserved{
		DigestVersion: 999,
		CacheKey:      "cache-key-1",
		Scope:         cachewarn.ScopeConversation,
		ChunkCount:    1,
		TerminalHash:  "legacy-hash",
	}
	legacyResponse := persistedCacheResponseObserved{
		DigestVersion:        999,
		CacheKey:             "cache-key-1",
		Scope:                cachewarn.ScopeConversation,
		ChunkCount:           1,
		TerminalHash:         "legacy-hash",
		HasCachedInputTokens: true,
		CachedInputTokens:    42,
	}
	if _, err := store.AppendEvent("legacy-request", sessionEventCacheRequestObserved, legacyRequest); err != nil {
		t.Fatalf("append legacy request: %v", err)
	}
	if _, err := store.AppendEvent("legacy-response", sessionEventCacheResponseObserved, legacyResponse); err != nil {
		t.Fatalf("append legacy response: %v", err)
	}

	reopened, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	client := &fakeClient{responses: []llm.Response{{Usage: llm.Usage{InputTokens: 12}}}}
	eng, err := New(reopened, client, tools.NewRegistry(), Config{Model: "gpt-5", CacheWarningMode: config.CacheWarningModeDefault})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	if _, err := eng.generateWithRetryClient(context.Background(), "step-1", client, testPromptCacheRequest("cache-key-1", "beta"), nil, nil, nil); err != nil {
		t.Fatalf("generate after reopen: %v", err)
	}

	warnings := persistedCacheWarnings(t, reopened)
	if len(warnings) != 0 {
		t.Fatalf("warning count = %d, want 0", len(warnings))
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
