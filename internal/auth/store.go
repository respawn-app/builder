package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Store interface {
	Load(ctx context.Context) (State, error)
	Save(ctx context.Context, state State) error
}

type FileStore struct {
	path string
}

func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (s *FileStore) Load(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return EmptyState(), nil
		}
		return State{}, fmt.Errorf("read auth state: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("parse auth state: %w", err)
	}
	if state.Scope == "" {
		state.Scope = ScopeGlobal
	}
	if err := state.Validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

func (s *FileStore) Save(ctx context.Context, state State) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if state.Scope == "" {
		state.Scope = ScopeGlobal
	}
	if err := state.Validate(); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal auth state: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create auth directory: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write auth tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace auth state: %w", err)
	}
	return nil
}

type MemoryStore struct {
	mu    sync.Mutex
	state State
	set   bool
}

func NewMemoryStore(initial State) *MemoryStore {
	return &MemoryStore{state: initial, set: true}
}

func (s *MemoryStore) Load(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	if s == nil {
		return EmptyState(), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.set {
		return EmptyState(), nil
	}
	state := s.state
	if state.Scope == "" {
		state.Scope = ScopeGlobal
	}
	return state, nil
}

func (s *MemoryStore) Save(ctx context.Context, state State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if state.Scope == "" {
		state.Scope = ScopeGlobal
	}
	if err := state.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	s.set = true
	return nil
}
