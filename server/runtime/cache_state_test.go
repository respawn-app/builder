package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"builder/server/llm"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/cachewarn"
)

func TestReplaceHistoryAppendsCacheWarningsForKnownScopes(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	primaryReq := llm.Request{Model: "gpt-5", SystemPrompt: "main", Items: llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleUser, Content: "hi"}})}
	reviewerReq := llm.Request{Model: "gpt-5", SystemPrompt: "reviewer", Items: llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleDeveloper, Content: "context"}})}
	if err := eng.recordCacheState("step-1", cachewarn.ScopePrimary, primaryReq, llm.Usage{InputTokens: 200_000, CachedInputTokens: 128_000, HasCachedInputTokens: true}); err != nil {
		t.Fatalf("record primary cache state: %v", err)
	}
	if err := eng.recordCacheState("step-1", cachewarn.ScopeReviewer, reviewerReq, llm.Usage{InputTokens: 120_000, CachedInputTokens: 64_000, HasCachedInputTokens: true}); err != nil {
		t.Fatalf("record reviewer cache state: %v", err)
	}

	if err := eng.replaceHistory("step-2", "compactor", compactionModeManual, llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleUser, Content: "summary"}})); err != nil {
		t.Fatalf("replace history: %v", err)
	}

	var warnings []ChatEntry
	for _, entry := range eng.ChatSnapshot().Entries {
		if entry.Role == roleCacheWarning {
			warnings = append(warnings, entry)
		}
	}
	if len(warnings) != 2 {
		t.Fatalf("expected 2 cache warnings, got %+v", warnings)
	}
	if got := warnings[0].Text; got != "Cache invalidated by context compaction. Previous cached tokens: 128k." {
		t.Fatalf("primary warning = %q", got)
	}
	if got := warnings[1].Text; got != "Supervisor cache invalidated by context compaction. Previous cached tokens: 64k." {
		t.Fatalf("reviewer warning = %q", got)
	}
	if events, err := store.ReadEvents(); err != nil {
		t.Fatalf("read events: %v", err)
	} else if count := countSessionEventsByKind(events, cacheInvalidationEventKind); count != 2 {
		t.Fatalf("cache invalidation event count = %d, want 2", count)
	}
}

func TestReplaceHistoryKeepsTrackingWhenWarningsDisabled(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	disabled := false
	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5", CacheInvalidationWarning: &disabled})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.recordCacheState("step-1", cachewarn.ScopePrimary, llm.Request{Model: "gpt-5", SystemPrompt: "main"}, llm.Usage{InputTokens: 80_000, CachedInputTokens: 48_000, HasCachedInputTokens: true}); err != nil {
		t.Fatalf("record cache state: %v", err)
	}

	if err := eng.replaceHistory("step-2", "compactor", compactionModeManual, llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleUser, Content: "summary"}})); err != nil {
		t.Fatalf("replace history: %v", err)
	}
	for _, entry := range eng.ChatSnapshot().Entries {
		if entry.Role == roleCacheWarning {
			t.Fatalf("did not expect visible cache warning when disabled, got %+v", eng.ChatSnapshot().Entries)
		}
	}
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if count := countSessionEventsByKind(events, cacheInvalidationEventKind); count != 1 {
		t.Fatalf("cache invalidation event count = %d, want 1", count)
	}
}

func TestReplaceHistoryClearsCachedTokenStateAfterInvalidation(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	eng, err := New(store, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.recordCacheState("step-1", cachewarn.ScopePrimary, llm.Request{Model: "gpt-5", SystemPrompt: "main"}, llm.Usage{InputTokens: 200_000, CachedInputTokens: 128_000, HasCachedInputTokens: true}); err != nil {
		t.Fatalf("record primary cache state: %v", err)
	}
	if err := eng.recordCacheState("step-1", cachewarn.ScopeReviewer, llm.Request{Model: "gpt-5", SystemPrompt: "reviewer"}, llm.Usage{InputTokens: 120_000, CachedInputTokens: 64_000, HasCachedInputTokens: true}); err != nil {
		t.Fatalf("record reviewer cache state: %v", err)
	}

	if err := eng.noteCacheInvalidation("step-2", cachewarn.ReasonFork); err != nil {
		t.Fatalf("note fork invalidation: %v", err)
	}
	if err := eng.noteCacheInvalidation("step-3", cachewarn.ReasonContextCompaction); err != nil {
		t.Fatalf("note compaction invalidation: %v", err)
	}

	var warnings []ChatEntry
	for _, entry := range eng.ChatSnapshot().Entries {
		if entry.Role == roleCacheWarning {
			warnings = append(warnings, entry)
		}
	}
	if len(warnings) != 4 {
		t.Fatalf("expected 4 cache warnings, got %+v", warnings)
	}
	if got := warnings[0].Text; got != "Cache invalidated by fork. Previous cached tokens: 128k." {
		t.Fatalf("first primary warning = %q", got)
	}
	if got := warnings[1].Text; got != "Supervisor cache invalidated by fork. Previous cached tokens: 64k." {
		t.Fatalf("first reviewer warning = %q", got)
	}
	if got := warnings[2].Text; got != "Cache invalidated by context compaction." {
		t.Fatalf("second primary warning = %q", got)
	}
	if got := warnings[3].Text; got != "Supervisor cache invalidated by context compaction." {
		t.Fatalf("second reviewer warning = %q", got)
	}
}

func TestSubmitUserMessagePersistsPrimaryCacheStateEvent(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	client := &fakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "ok", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{InputTokens: 210_000, CachedInputTokens: 128_000, HasCachedInputTokens: true, WindowTokens: 400_000},
	}}}
	eng, err := New(store, client, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if _, err := eng.SubmitUserMessage(context.Background(), "hello"); err != nil {
		t.Fatalf("submit user message: %v", err)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	state, ok := findCacheStateEvent(t, events, cachewarn.ScopePrimary)
	if !ok {
		t.Fatalf("expected persisted primary cache state event, events=%+v", events)
	}
	if state.CachedInputTokens != 128_000 || !state.HasCachedInput {
		t.Fatalf("unexpected cache state payload: %+v", state)
	}
	if strings.TrimSpace(state.RequestDigest) == "" {
		t.Fatalf("expected request digest in cache state payload: %+v", state)
	}
}

func TestForkRestoreShowsCacheWarningsForKnownScopes(t *testing.T) {
	dir := t.TempDir()
	parent, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create parent store: %v", err)
	}
	if _, err := parent.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "u1"}); err != nil {
		t.Fatalf("append u1: %v", err)
	}
	if _, err := parent.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: "a1", Phase: llm.MessagePhaseFinal}); err != nil {
		t.Fatalf("append a1: %v", err)
	}
	if _, err := parent.AppendEvent("step-1", cacheStateEventKind, cachewarn.StateEvent{Scope: cachewarn.ScopePrimary, RequestDigest: "primary-digest", InputTokens: 200_000, CachedInputTokens: 128_000, HasCachedInput: true}); err != nil {
		t.Fatalf("append primary cache state: %v", err)
	}
	if _, err := parent.AppendEvent("step-1", cacheStateEventKind, cachewarn.StateEvent{Scope: cachewarn.ScopeReviewer, RequestDigest: "reviewer-digest", InputTokens: 100_000, CachedInputTokens: 64_000, HasCachedInput: true}); err != nil {
		t.Fatalf("append reviewer cache state: %v", err)
	}
	if _, err := parent.AppendEvent("step-2", "message", llm.Message{Role: llm.RoleUser, Content: "u2"}); err != nil {
		t.Fatalf("append u2: %v", err)
	}

	forked, err := session.ForkAtUserMessage(parent, 2, "fork")
	if err != nil {
		t.Fatalf("fork at user message: %v", err)
	}
	eng, err := New(forked, &fakeClient{}, tools.NewRegistry(fakeTool{name: tools.ToolShell}), Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new fork engine: %v", err)
	}

	var warnings []ChatEntry
	for _, entry := range eng.ChatSnapshot().Entries {
		if entry.Role == roleCacheWarning {
			warnings = append(warnings, entry)
		}
	}
	if len(warnings) != 2 {
		t.Fatalf("expected 2 fork cache warnings, got %+v", warnings)
	}
	if got := warnings[0].Text; got != "Cache invalidated by fork. Previous cached tokens: 128k." {
		t.Fatalf("primary fork warning = %q", got)
	}
	if got := warnings[1].Text; got != "Supervisor cache invalidated by fork. Previous cached tokens: 64k." {
		t.Fatalf("reviewer fork warning = %q", got)
	}
}

func countSessionEventsByKind(events []session.Event, kind string) int {
	count := 0
	for _, evt := range events {
		if evt.Kind == kind {
			count++
		}
	}
	return count
}

func findCacheStateEvent(t *testing.T, events []session.Event, scope cachewarn.Scope) (cachewarn.StateEvent, bool) {
	t.Helper()
	for _, evt := range events {
		if evt.Kind != cacheStateEventKind {
			continue
		}
		var state cachewarn.StateEvent
		if err := json.Unmarshal(evt.Payload, &state); err != nil {
			t.Fatalf("decode cache state event: %v", err)
		}
		if state.Scope == scope {
			return state, true
		}
	}
	return cachewarn.StateEvent{}, false
}
