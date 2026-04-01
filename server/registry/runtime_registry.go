package registry

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"builder/server/runtime"
	"builder/server/runtimeview"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

const sessionActivityBufferSize = 256

type RuntimeRegistry struct {
	mu      sync.RWMutex
	engines map[string]*runtimeEntry
}

type runtimeEntry struct {
	engine *runtime.Engine
	hub    *sessionActivityHub
}

type sessionActivityHub struct {
	mu          sync.Mutex
	nextID      uint64
	closed      bool
	subscribers map[uint64]*sessionActivitySubscription
}

type sessionActivitySubscription struct {
	ch      chan clientui.Event
	onClose func()

	mu   sync.Mutex
	err  error
	done bool
}

func NewRuntimeRegistry() *RuntimeRegistry {
	return &RuntimeRegistry{engines: make(map[string]*runtimeEntry)}
}

func (r *RuntimeRegistry) Register(sessionID string, engine *runtime.Engine) {
	if r == nil || engine == nil {
		return
	}
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return
	}
	entry := &runtimeEntry{engine: engine, hub: newSessionActivityHub()}
	r.mu.Lock()
	previous := r.engines[id]
	r.engines[id] = entry
	r.mu.Unlock()
	if previous != nil && previous.hub != nil {
		previous.hub.close(io.EOF)
	}
}

func (r *RuntimeRegistry) Unregister(sessionID string) {
	if r == nil {
		return
	}
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return
	}
	r.mu.Lock()
	entry := r.engines[id]
	delete(r.engines, id)
	r.mu.Unlock()
	if entry != nil && entry.hub != nil {
		entry.hub.close(io.EOF)
	}
}

func (r *RuntimeRegistry) ResolveRuntime(_ context.Context, sessionID string) (*runtime.Engine, error) {
	if r == nil {
		return nil, nil
	}
	id := strings.TrimSpace(sessionID)
	r.mu.RLock()
	entry := r.engines[id]
	r.mu.RUnlock()
	if entry == nil {
		return nil, nil
	}
	return entry.engine, nil
}

func (r *RuntimeRegistry) PublishRuntimeEvent(sessionID string, evt runtime.Event) {
	if r == nil {
		return
	}
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return
	}
	r.mu.RLock()
	entry := r.engines[id]
	r.mu.RUnlock()
	if entry == nil || entry.hub == nil {
		return
	}
	entry.hub.publish(runtimeview.EventFromRuntime(evt))
}

func (r *RuntimeRegistry) SubscribeSessionActivity(_ context.Context, sessionID string) (serverapi.SessionActivitySubscription, error) {
	if r == nil {
		return nil, fmt.Errorf("runtime registry is required")
	}
	id := strings.TrimSpace(sessionID)
	r.mu.RLock()
	entry := r.engines[id]
	r.mu.RUnlock()
	if entry == nil || entry.hub == nil {
		return nil, fmt.Errorf("session %q is not active", id)
	}
	return entry.hub.subscribe(), nil
}

func newSessionActivityHub() *sessionActivityHub {
	return &sessionActivityHub{subscribers: make(map[uint64]*sessionActivitySubscription)}
}

func (h *sessionActivityHub) subscribe() *sessionActivitySubscription {
	if h == nil {
		return nil
	}
	sub := &sessionActivitySubscription{ch: make(chan clientui.Event, sessionActivityBufferSize)}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		sub.closeWithError(io.EOF)
		return sub
	}
	id := h.nextID
	h.nextID++
	h.subscribers[id] = sub
	h.mu.Unlock()
	sub.onClose = func() {
		h.mu.Lock()
		delete(h.subscribers, id)
		h.mu.Unlock()
	}
	return sub
}

func (h *sessionActivityHub) publish(evt clientui.Event) {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	subs := make([]*sessionActivitySubscription, 0, len(h.subscribers))
	for _, sub := range h.subscribers {
		subs = append(subs, sub)
	}
	h.mu.Unlock()
	for _, sub := range subs {
		if !sub.publish(evt) {
			sub.closeWithError(serverapi.ErrSessionActivityGap)
		}
	}
}

func (h *sessionActivityHub) close(err error) {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	subs := make([]*sessionActivitySubscription, 0, len(h.subscribers))
	for id, sub := range h.subscribers {
		subs = append(subs, sub)
		delete(h.subscribers, id)
	}
	h.mu.Unlock()
	for _, sub := range subs {
		sub.closeWithError(err)
	}
}

func (s *sessionActivitySubscription) publish(evt clientui.Event) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return false
	}
	select {
	case s.ch <- evt:
		return true
	default:
		return false
	}
}

func (s *sessionActivitySubscription) Next(ctx context.Context) (clientui.Event, error) {
	if s == nil {
		return clientui.Event{}, io.EOF
	}
	select {
	case <-ctx.Done():
		return clientui.Event{}, ctx.Err()
	case evt, ok := <-s.ch:
		if ok {
			return evt, nil
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.err != nil {
			return clientui.Event{}, s.err
		}
		return clientui.Event{}, io.EOF
	}
}

func (s *sessionActivitySubscription) Close() error {
	if s == nil {
		return nil
	}
	s.closeWithError(io.EOF)
	return nil
}

func (s *sessionActivitySubscription) closeWithError(err error) {
	if s == nil {
		return
	}
	var onClose func()
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	s.done = true
	s.err = err
	close(s.ch)
	onClose = s.onClose
	s.mu.Unlock()
	if onClose != nil {
		onClose()
	}
}

var _ serverapi.SessionActivitySubscription = (*sessionActivitySubscription)(nil)
