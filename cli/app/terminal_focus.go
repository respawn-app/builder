package app

import "sync"

type terminalFocusState struct {
	mu      sync.RWMutex
	known   bool
	focused bool
}

func newTerminalFocusState() *terminalFocusState {
	return &terminalFocusState{}
}

func (s *terminalFocusState) MarkFocused() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.known = true
	s.focused = true
	s.mu.Unlock()
}

func (s *terminalFocusState) MarkBlurred() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.known = true
	s.focused = false
	s.mu.Unlock()
}

func (s *terminalFocusState) FocusedForAttention() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.known && s.focused
}

func (s *terminalFocusState) Known() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.known
}
