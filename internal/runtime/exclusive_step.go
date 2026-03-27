package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"builder/internal/llm"
	"github.com/google/uuid"
)

var errExclusiveStepBusy = errors.New("agent is busy")

type defaultExclusiveStepLifecycle struct {
	engine     *Engine
	background backgroundNoticeScheduler

	mu     sync.Mutex
	busy   bool
	cancel context.CancelFunc
}

func (s *defaultExclusiveStepLifecycle) Run(ctx context.Context, options exclusiveStepOptions, fn func(stepCtx context.Context, stepID string) error) (err error) {
	stepCtx, stepID, err := s.begin(ctx)
	if err != nil {
		return err
	}
	if options.EmitRunState {
		s.engine.emit(Event{Kind: EventRunStateChanged, StepID: stepID, RunState: &RunState{Busy: true}})
	}
	defer func() {
		s.end()
		if options.EmitRunState {
			s.engine.emit(Event{Kind: EventRunStateChanged, StepID: stepID, RunState: &RunState{Busy: false}})
		}
		if s.background != nil {
			s.background.ScheduleIfIdle()
		}
		if clearErr := s.engine.store.MarkInFlight(false); clearErr != nil {
			wrapped := fmt.Errorf("mark in-flight false: %w", clearErr)
			s.engine.emit(Event{Kind: EventInFlightClearFailed, StepID: stepID, Error: wrapped.Error()})
			err = errors.Join(err, wrapped)
		}
	}()
	return fn(stepCtx, stepID)
}

func (s *defaultExclusiveStepLifecycle) Interrupt() error {
	s.mu.Lock()
	busy := s.busy
	cancel := s.cancel
	s.mu.Unlock()

	if !busy || cancel == nil {
		return nil
	}
	cancel()
	if err := s.engine.appendMessage("", llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeInterruption, Content: interruptMessage}); err != nil {
		return err
	}
	if err := s.engine.store.MarkInFlight(false); err != nil {
		return err
	}
	return nil
}

func (s *defaultExclusiveStepLifecycle) IsBusy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy
}

func (s *defaultExclusiveStepLifecycle) begin(ctx context.Context) (context.Context, string, error) {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return nil, "", errExclusiveStepBusy
	}
	stepCtx, cancel := context.WithCancel(ctx)
	s.busy = true
	s.cancel = cancel
	s.mu.Unlock()

	if err := s.engine.store.MarkInFlight(true); err != nil {
		s.end()
		return nil, "", err
	}
	return stepCtx, uuid.NewString(), nil
}

func (s *defaultExclusiveStepLifecycle) end() {
	s.mu.Lock()
	s.busy = false
	s.cancel = nil
	s.mu.Unlock()
}
