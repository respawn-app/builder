package sessionview

import (
	"context"
	"strings"
	"sync"

	"builder/server/runtime"
	"builder/server/runtimeview"
	"builder/server/session"
	"builder/shared/clientui"
	"builder/shared/config"
)

const dormantTranscriptCacheMaxEntries = 16

type dormantTranscriptCache struct {
	mu      sync.RWMutex
	entries map[string]dormantTranscriptCacheEntry
	build   func(context.Context, *session.Store) (dormantTranscriptCacheEntry, error)
	maxSize int
	clock   uint64
}

type dormantTranscriptCacheEntry struct {
	sessionDir                   string
	sessionID                    string
	revision                     int64
	totalEntries                 int
	lastCommittedAssistantAnswer string
	ongoingTail                  runtime.TranscriptWindowSnapshot
	activeRun                    *clientui.RunView
	lastUsed                     uint64
}

func newDormantTranscriptCache(build func(context.Context, *session.Store) (dormantTranscriptCacheEntry, error)) *dormantTranscriptCache {
	return newDormantTranscriptCacheWithLimit(dormantTranscriptCacheMaxEntries, build)
}

func newDormantTranscriptCacheWithLimit(limit int, build func(context.Context, *session.Store) (dormantTranscriptCacheEntry, error)) *dormantTranscriptCache {
	if build == nil {
		build = buildDormantTranscriptCacheEntry
	}
	if limit <= 0 {
		limit = dormantTranscriptCacheMaxEntries
	}
	return &dormantTranscriptCache{entries: make(map[string]dormantTranscriptCacheEntry), build: build, maxSize: limit}
}

func (c *dormantTranscriptCache) get(ctx context.Context, store *session.Store) (dormantTranscriptCacheEntry, error) {
	if c == nil || store == nil {
		return dormantTranscriptCacheEntry{}, nil
	}
	meta := store.Meta()
	key := dormantTranscriptCacheKey(store.Dir(), meta.SessionID)
	c.mu.Lock()
	entry, ok := c.entries[key]
	if ok && entry.matchesStore(store, meta) {
		entry.lastUsed = c.nextStampLocked()
		c.entries[key] = entry
		c.mu.Unlock()
		return entry, nil
	}
	c.mu.Unlock()
	built, err := c.build(ctx, store)
	if err != nil {
		return dormantTranscriptCacheEntry{}, err
	}
	c.mu.Lock()
	if existing, ok := c.entries[key]; ok && existing.matchesStore(store, meta) {
		existing.lastUsed = c.nextStampLocked()
		c.entries[key] = existing
		c.mu.Unlock()
		return existing, nil
	}
	built.lastUsed = c.nextStampLocked()
	c.entries[key] = built
	c.evictIfNeededLocked()
	c.mu.Unlock()
	return built, nil
}

func (c *dormantTranscriptCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.entries)
	c.clock = 0
}

func (c *dormantTranscriptCache) nextStampLocked() uint64 {
	c.clock++
	return c.clock
}

func (c *dormantTranscriptCache) evictIfNeededLocked() {
	if c == nil || c.maxSize <= 0 || len(c.entries) <= c.maxSize {
		return
	}
	oldestKey := ""
	oldestStamp := uint64(0)
	for key, entry := range c.entries {
		if oldestKey == "" || entry.lastUsed < oldestStamp {
			oldestKey = key
			oldestStamp = entry.lastUsed
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

func dormantTranscriptCacheKey(sessionDir, sessionID string) string {
	return strings.TrimSpace(sessionDir) + "::" + strings.TrimSpace(sessionID)
}

func (e dormantTranscriptCacheEntry) matchesStore(store *session.Store, meta session.Meta) bool {
	if store == nil {
		return false
	}
	return strings.TrimSpace(e.sessionDir) == strings.TrimSpace(store.Dir()) &&
		strings.TrimSpace(e.sessionID) == strings.TrimSpace(meta.SessionID) &&
		e.revision == meta.LastSequence
}

func buildDormantTranscriptCacheEntry(ctx context.Context, store *session.Store) (dormantTranscriptCacheEntry, error) {
	meta := store.Meta()
	scan, err := scanDormantTranscript(ctx, store, runtime.PersistedTranscriptScanRequest{
		TrackOngoingTail: true,
		TailLimit:        runtimeview.OngoingTailEntryLimit,
		CacheWarningMode: config.CacheWarningModeDefault,
	})
	if err != nil {
		return dormantTranscriptCacheEntry{}, err
	}
	var activeRun *clientui.RunView
	latestRun, err := store.LatestRun()
	if err != nil {
		return dormantTranscriptCacheEntry{}, err
	}
	if latestRun != nil && latestRun.Status == session.RunStatusRunning {
		activeRun = runtimeview.RunViewFromSessionRecord(meta.SessionID, latestRun)
	}
	return dormantTranscriptCacheEntry{
		sessionDir:                   store.Dir(),
		sessionID:                    meta.SessionID,
		revision:                     meta.LastSequence,
		totalEntries:                 scan.TotalEntries(),
		lastCommittedAssistantAnswer: scan.LastCommittedAssistantFinalAnswer(),
		ongoingTail:                  scan.OngoingTailSnapshot(),
		activeRun:                    activeRun,
	}, nil
}

func (e dormantTranscriptCacheEntry) mainView(meta session.Meta, freshness clientui.ConversationFreshness) clientui.RuntimeMainView {
	return clientui.RuntimeMainView{
		Status: clientui.RuntimeStatus{
			ConversationFreshness:             freshness,
			ParentSessionID:                   meta.ParentSessionID,
			LastCommittedAssistantFinalAnswer: e.lastCommittedAssistantAnswer,
		},
		Session: clientui.RuntimeSessionView{
			SessionID:             meta.SessionID,
			SessionName:           meta.Name,
			ConversationFreshness: freshness,
			Transcript: clientui.TranscriptMetadata{
				Revision:            meta.LastSequence,
				CommittedEntryCount: e.totalEntries,
			},
		},
		ActiveRun: e.activeRun,
	}
}

func (e dormantTranscriptCacheEntry) transcriptPageFromTail(meta session.Meta, freshness clientui.ConversationFreshness, req clientui.TranscriptPageRequest) clientui.TranscriptPage {
	return runtimeview.TranscriptPageFromOngoingTailWindow(meta.SessionID, meta.Name, freshness, meta.LastSequence, e.ongoingTail, req)
}

func (e dormantTranscriptCacheEntry) transcriptPageCoveredByTail(meta session.Meta, freshness clientui.ConversationFreshness, req clientui.TranscriptPageRequest) (clientui.TranscriptPage, bool) {
	if req.Limit <= 0 {
		return clientui.TranscriptPage{}, false
	}
	tailOffset := e.ongoingTail.Offset
	tailEntries := e.ongoingTail.Snapshot.Entries
	if req.Offset < tailOffset {
		return clientui.TranscriptPage{}, false
	}
	end := req.Offset + req.Limit
	tailEnd := tailOffset + len(tailEntries)
	if end > tailEnd {
		return clientui.TranscriptPage{}, false
	}
	start := req.Offset - tailOffset
	snapshot := runtime.ChatSnapshot{Entries: cloneDormantChatEntries(tailEntries[start : start+req.Limit])}
	return runtimeview.TranscriptPageFromCollectedChat(
		meta.SessionID,
		meta.Name,
		freshness,
		meta.LastSequence,
		runtimeview.ChatSnapshotFromRuntime(snapshot),
		e.totalEntries,
		req.Offset,
		clientui.TranscriptPageRequest{Offset: req.Offset, Limit: req.Limit},
	), true
}

func cloneDormantChatEntries(entries []runtime.ChatEntry) []runtime.ChatEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]runtime.ChatEntry, 0, len(entries))
	for _, entry := range entries {
		cloned = append(cloned, entry)
	}
	return cloned
}
