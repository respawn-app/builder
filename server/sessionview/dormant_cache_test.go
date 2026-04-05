package sessionview

import (
	"context"
	"fmt"
	"testing"

	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/shared/serverapi"
)

func TestDormantTranscriptCacheReusesEntryForUnchangedRevision(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "hello"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	buildCalls := 0
	cache := newDormantTranscriptCache(func(_ context.Context, store *session.Store) (dormantTranscriptCacheEntry, error) {
		buildCalls++
		meta := store.Meta()
		return dormantTranscriptCacheEntry{
			sessionDir:                   store.Dir(),
			sessionID:                    meta.SessionID,
			revision:                     meta.LastSequence,
			totalEntries:                 7,
			lastCommittedAssistantAnswer: "done",
			ongoingTail:                  dormantTailWindow(meta.SessionID, 2, 7, []string{"tail-1", "tail-2"}),
		}, nil
	})

	entry, err := cache.get(context.Background(), store)
	if err != nil {
		t.Fatalf("cache get: %v", err)
	}
	if entry.totalEntries != 7 || entry.lastCommittedAssistantAnswer != "done" {
		t.Fatalf("unexpected cache entry: %+v", entry)
	}
	entry, err = cache.get(context.Background(), store)
	if err != nil {
		t.Fatalf("cache get second time: %v", err)
	}
	if entry.totalEntries != 7 {
		t.Fatalf("unexpected second cache entry: %+v", entry)
	}
	if buildCalls != 1 {
		t.Fatalf("build calls = %d, want 1", buildCalls)
	}
}

func TestDormantTranscriptCacheInvalidatesOnRevisionAdvance(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "hello"}); err != nil {
		t.Fatalf("append first message: %v", err)
	}

	buildCalls := 0
	cache := newDormantTranscriptCache(func(_ context.Context, store *session.Store) (dormantTranscriptCacheEntry, error) {
		buildCalls++
		meta := store.Meta()
		return dormantTranscriptCacheEntry{
			sessionDir: store.Dir(),
			sessionID:  meta.SessionID,
			revision:   meta.LastSequence,
		}, nil
	})

	if _, err := cache.get(context.Background(), store); err != nil {
		t.Fatalf("cache get: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal}); err != nil {
		t.Fatalf("append second message: %v", err)
	}
	if _, err := cache.get(context.Background(), store); err != nil {
		t.Fatalf("cache get after revision advance: %v", err)
	}
	if buildCalls != 2 {
		t.Fatalf("build calls = %d, want 2", buildCalls)
	}
}

func TestDormantTranscriptCacheEvictsLeastRecentlyUsedEntry(t *testing.T) {
	root := t.TempDir()
	storeA, err := session.Create(root, "ws", root)
	if err != nil {
		t.Fatalf("create store A: %v", err)
	}
	storeB, err := session.Create(root, "ws", root)
	if err != nil {
		t.Fatalf("create store B: %v", err)
	}
	storeC, err := session.Create(root, "ws", root)
	if err != nil {
		t.Fatalf("create store C: %v", err)
	}
	buildCalls := 0
	cache := newDormantTranscriptCacheWithLimit(2, func(_ context.Context, store *session.Store) (dormantTranscriptCacheEntry, error) {
		buildCalls++
		meta := store.Meta()
		return dormantTranscriptCacheEntry{sessionDir: store.Dir(), sessionID: meta.SessionID, revision: meta.LastSequence}, nil
	})

	if _, err := cache.get(context.Background(), storeA); err != nil {
		t.Fatalf("cache get A: %v", err)
	}
	if _, err := cache.get(context.Background(), storeB); err != nil {
		t.Fatalf("cache get B: %v", err)
	}
	if _, err := cache.get(context.Background(), storeA); err != nil {
		t.Fatalf("cache re-get A: %v", err)
	}
	if _, err := cache.get(context.Background(), storeC); err != nil {
		t.Fatalf("cache get C: %v", err)
	}
	if _, err := cache.get(context.Background(), storeB); err != nil {
		t.Fatalf("cache re-get B after eviction: %v", err)
	}
	if buildCalls != 4 {
		t.Fatalf("build calls = %d, want 4", buildCalls)
	}
}

func TestServiceUsesDormantCacheForMainViewAndTailCoveredPages(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.SetName("incident triage"); err != nil {
		t.Fatalf("set name: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "seed"}); err != nil {
		t.Fatalf("append seed message: %v", err)
	}

	buildCalls := 0
	cache := newDormantTranscriptCache(func(_ context.Context, store *session.Store) (dormantTranscriptCacheEntry, error) {
		buildCalls++
		meta := store.Meta()
		return dormantTranscriptCacheEntry{
			sessionDir:                   store.Dir(),
			sessionID:                    meta.SessionID,
			revision:                     meta.LastSequence,
			totalEntries:                 600,
			lastCommittedAssistantAnswer: "done",
			ongoingTail:                  dormantTailWindow(meta.SessionID, 100, 600, buildTailTexts(100, 500)),
		}, nil
	})

	svc := &Service{sessions: NewStaticSessionResolver(store), dormant: cache}
	mainViewResp, err := svc.GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("get session main view: %v", err)
	}
	if got := mainViewResp.MainView.Status.LastCommittedAssistantFinalAnswer; got != "done" {
		t.Fatalf("last committed assistant final answer = %q, want done", got)
	}
	if got := mainViewResp.MainView.Session.Transcript.CommittedEntryCount; got != 600 {
		t.Fatalf("committed entry count = %d, want 600", got)
	}

	pageResp, err := svc.GetSessionTranscriptPage(context.Background(), serverapi.SessionTranscriptPageRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("get default transcript page: %v", err)
	}
	if got := pageResp.Transcript.Offset; got != 100 {
		t.Fatalf("default tail offset = %d, want 100", got)
	}
	if got := len(pageResp.Transcript.Entries); got != 500 {
		t.Fatalf("default tail entry count = %d, want 500", got)
	}

	boundedResp, err := svc.GetSessionTranscriptPage(context.Background(), serverapi.SessionTranscriptPageRequest{SessionID: store.Meta().SessionID, Offset: 350, Limit: 100})
	if err != nil {
		t.Fatalf("get bounded tail-covered transcript page: %v", err)
	}
	if got := boundedResp.Transcript.Offset; got != 350 {
		t.Fatalf("bounded page offset = %d, want 350", got)
	}
	if got := len(boundedResp.Transcript.Entries); got != 100 {
		t.Fatalf("bounded page entry count = %d, want 100", got)
	}
	if got := boundedResp.Transcript.Entries[0].Text; got != "line 350" {
		t.Fatalf("bounded page first entry = %q, want line 350", got)
	}
	if buildCalls != 1 {
		t.Fatalf("build calls = %d, want 1", buildCalls)
	}
}

func dormantTailWindow(sessionID string, offset, total int, texts []string) runtime.TranscriptWindowSnapshot {
	entries := make([]runtime.ChatEntry, 0, len(texts))
	for _, text := range texts {
		entries = append(entries, runtime.ChatEntry{Role: "assistant", Text: text})
	}
	return runtime.TranscriptWindowSnapshot{
		Snapshot:     runtime.ChatSnapshot{Entries: entries},
		Offset:       offset,
		TotalEntries: total,
	}
}

func buildTailTexts(offset, count int) []string {
	texts := make([]string, 0, count)
	for i := 0; i < count; i++ {
		texts = append(texts, fmt.Sprintf("line %d", offset+i))
	}
	return texts
}
