package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"builder/shared/cachewarn"
)

var errForkReplayBoundary = errors.New("fork replay boundary reached")

func ForkAtUserMessage(parent *Store, userMessageIndex int, forkName string) (*Store, error) {
	if parent == nil {
		return nil, fmt.Errorf("parent store is required")
	}
	if userMessageIndex <= 0 {
		return nil, fmt.Errorf("user message index must be >= 1")
	}

	parentMeta := parent.Meta()
	replay := make([]ReplayEvent, 0)
	visibleUserCount := 0
	reviewerCacheStateSeen := false
	err := parent.WalkEvents(func(evt Event) error {
		if hasVisibleUserMessageEvent(evt.Kind, evt.Payload) {
			visibleUserCount++
			if visibleUserCount == userMessageIndex {
				return errForkReplayBoundary
			}
		}
		if evt.Kind == "cache_state" {
			var state cachewarn.StateEvent
			if err := json.Unmarshal(evt.Payload, &state); err == nil && state.Scope == cachewarn.ScopeReviewer {
				reviewerCacheStateSeen = true
			}
		}
		replay = append(replay, ReplayEvent{StepID: evt.StepID, Kind: evt.Kind, Payload: append([]byte(nil), evt.Payload...)})
		return nil
	})
	if err != nil && !errors.Is(err, errForkReplayBoundary) {
		return nil, fmt.Errorf("read parent events: %w", err)
	}

	if visibleUserCount < userMessageIndex {
		return nil, fmt.Errorf("user message index %d is out of range", userMessageIndex)
	}

	containerDir := filepath.Dir(parent.Dir())
	child, err := NewLazy(containerDir, parentMeta.WorkspaceContainer, parentMeta.WorkspaceRoot)
	if err != nil {
		return nil, err
	}

	child.mu.Lock()
	child.meta.Locked = cloneLockedContract(parentMeta.Locked)
	child.meta.AgentsInjected = parentMeta.AgentsInjected
	child.meta.ParentSessionID = parentMeta.SessionID
	child.meta.Name = strings.TrimSpace(forkName)
	child.meta.Continuation = cloneContinuationContext(parentMeta.Continuation)
	child.mu.Unlock()

	if _, err := child.AppendReplayEvents(replay); err != nil {
		return nil, fmt.Errorf("append fork replay events: %w", err)
	}
	if _, err := child.AppendEvent("", "cache_invalidation", cachewarn.InvalidationEvent{Scope: cachewarn.ScopePrimary, Reason: cachewarn.ReasonFork}); err != nil {
		return nil, fmt.Errorf("append fork cache invalidation event: %w", err)
	}
	if reviewerCacheStateSeen {
		if _, err := child.AppendEvent("", "cache_invalidation", cachewarn.InvalidationEvent{Scope: cachewarn.ScopeReviewer, Reason: cachewarn.ReasonFork}); err != nil {
			return nil, fmt.Errorf("append fork reviewer cache invalidation event: %w", err)
		}
	}
	return child, nil
}

func cloneLockedContract(in *LockedContract) *LockedContract {
	if in == nil {
		return nil
	}
	copyLocked := *in
	if len(in.EnabledTools) > 0 {
		copyLocked.EnabledTools = append([]string(nil), in.EnabledTools...)
	}
	return &copyLocked
}

func cloneContinuationContext(in *ContinuationContext) *ContinuationContext {
	if in == nil {
		return nil
	}
	copyContext := *in
	return &copyContext
}
