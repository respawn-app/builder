package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"builder/server/llm"
	"builder/shared/cachewarn"
)

const (
	cacheStateEventKind        = "cache_state"
	cacheInvalidationEventKind = "cache_invalidation"
	roleCacheWarning           = "cache_warning"
)

type cacheScopeState struct {
	Known           bool
	RequestDigest   string
	InputTokens     int
	CachedTokens    int
	HasCachedTokens bool
}

type cacheStateTracker struct {
	scopes map[cachewarn.Scope]cacheScopeState
}

func newCacheStateTracker() *cacheStateTracker {
	return &cacheStateTracker{scopes: map[cachewarn.Scope]cacheScopeState{}}
}

func (t *cacheStateTracker) hasState(scope cachewarn.Scope) bool {
	if t == nil {
		return false
	}
	state, ok := t.scopes[scope]
	if !ok {
		return false
	}
	return state.Known
}

func (t *cacheStateTracker) applyState(evt cachewarn.StateEvent) {
	if t == nil {
		return
	}
	scope, ok := cachewarn.NormalizeScope(string(evt.Scope))
	if !ok {
		return
	}
	inputTokens := evt.InputTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	cachedTokens := evt.CachedInputTokens
	if cachedTokens < 0 {
		cachedTokens = 0
	}
	if inputTokens > 0 && cachedTokens > inputTokens {
		cachedTokens = inputTokens
	}
	t.scopes[scope] = cacheScopeState{
		Known:           true,
		RequestDigest:   evt.RequestDigest,
		InputTokens:     inputTokens,
		CachedTokens:    cachedTokens,
		HasCachedTokens: evt.HasCachedInput,
	}
}

func (t *cacheStateTracker) consumeWarningText(evt cachewarn.InvalidationEvent) (string, bool) {
	if t == nil {
		return "", false
	}
	scope, ok := cachewarn.NormalizeScope(string(evt.Scope))
	if !ok {
		return "", false
	}
	state, exists := t.scopes[scope]
	if scope == cachewarn.ScopeReviewer && (!exists || !state.Known) {
		return "", false
	}
	message := cachewarn.WarningText(scope, evt.Reason, state.CachedTokens, state.HasCachedTokens)
	state.Known = true
	state.RequestDigest = ""
	state.InputTokens = 0
	state.CachedTokens = 0
	state.HasCachedTokens = false
	t.scopes[scope] = state
	return message, true
}

func (e *Engine) recordCacheState(stepID string, scope cachewarn.Scope, req llm.Request, usage llm.Usage) error {
	if e == nil || e.store == nil || e.cache == nil {
		return nil
	}
	evt := cachewarn.StateEvent{
		Scope:             scope,
		RequestDigest:     cacheRequestDigest(req),
		InputTokens:       usage.InputTokens,
		CachedInputTokens: usage.CachedInputTokens,
		HasCachedInput:    usage.HasCachedInputTokens,
	}
	_, err := e.store.AppendEvent(stepID, cacheStateEventKind, evt)
	if err != nil {
		return err
	}
	e.cache.applyState(evt)
	return nil
}

func (e *Engine) noteCacheInvalidation(stepID string, reason cachewarn.Reason) error {
	if e == nil || e.store == nil || e.cache == nil {
		return nil
	}
	for _, scope := range e.cacheInvalidationScopes() {
		evt := cachewarn.InvalidationEvent{Scope: scope, Reason: reason}
		if _, err := e.store.AppendEvent(stepID, cacheInvalidationEventKind, evt); err != nil {
			return err
		}
		if !e.CacheInvalidationWarningEnabled() {
			continue
		}
		warningText, ok := e.cache.consumeWarningText(evt)
		if !ok {
			continue
		}
		e.chat.appendLocalEntry(roleCacheWarning, warningText)
	}
	return nil
}

func (e *Engine) restoreCacheState(evtType string, payload []byte) error {
	if e == nil || e.cache == nil {
		return nil
	}
	switch evtType {
	case cacheStateEventKind:
		var evt cachewarn.StateEvent
		if err := json.Unmarshal(payload, &evt); err != nil {
			return fmt.Errorf("decode cache_state event: %w", err)
		}
		e.cache.applyState(evt)
		return nil
	case cacheInvalidationEventKind:
		var evt cachewarn.InvalidationEvent
		if err := json.Unmarshal(payload, &evt); err != nil {
			return fmt.Errorf("decode cache_invalidation event: %w", err)
		}
		if !e.CacheInvalidationWarningEnabled() {
			return nil
		}
		warningText, ok := e.cache.consumeWarningText(evt)
		if !ok {
			return nil
		}
		e.chat.appendLocalEntry(roleCacheWarning, warningText)
		return nil
	default:
		return nil
	}
}

func (e *Engine) cacheInvalidationScopes() []cachewarn.Scope {
	scopes := []cachewarn.Scope{cachewarn.ScopePrimary}
	if e.cache != nil && e.cache.hasState(cachewarn.ScopeReviewer) {
		scopes = append(scopes, cachewarn.ScopeReviewer)
	}
	return scopes
}

func cacheRequestDigest(req llm.Request) string {
	payload, err := json.Marshal(struct {
		Model        string             `json:"model"`
		SystemPrompt string             `json:"system_prompt"`
		Items        []llm.ResponseItem `json:"items"`
	}{
		Model:        req.Model,
		SystemPrompt: req.SystemPrompt,
		Items:        llm.CloneResponseItems(req.Items),
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:12])
}
