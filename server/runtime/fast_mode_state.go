package runtime

import "sync/atomic"

type FastModeState struct {
	enabled atomic.Bool
}

func NewFastModeState(enabled bool) *FastModeState {
	state := &FastModeState{}
	state.enabled.Store(enabled)
	return state
}

func (s *FastModeState) Enabled() bool {
	if s == nil {
		return false
	}
	return s.enabled.Load()
}

func (s *FastModeState) SetEnabled(enabled bool) bool {
	if s == nil {
		return false
	}
	for {
		current := s.enabled.Load()
		if current == enabled {
			return false
		}
		if s.enabled.CompareAndSwap(current, enabled) {
			return true
		}
	}
}
