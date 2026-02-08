package auth

import (
	"context"
	"time"
)

type Manager struct {
	store     Store
	refresher *OAuthRefresher
	now       func() time.Time
}

func NewManager(store Store, refresher *OAuthRefresher, now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	return &Manager{
		store:     store,
		refresher: refresher,
		now:       now,
	}
}

func (m *Manager) Load(ctx context.Context) (State, error) {
	if m.store == nil {
		return EmptyState(), nil
	}
	state, err := m.store.Load(ctx)
	if err != nil {
		return State{}, err
	}
	if state.Scope == "" {
		state.Scope = ScopeGlobal
	}
	if err := state.Validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

func (m *Manager) EnsureStartupReady(ctx context.Context) error {
	state, err := m.Load(ctx)
	if err != nil {
		return err
	}
	return EnsureStartupReady(state)
}

func (m *Manager) SwitchMethod(ctx context.Context, method Method, isIdle bool) (State, error) {
	if err := EnsureIdleForMethodSwitch(isIdle); err != nil {
		return State{}, err
	}
	if err := method.Validate(); err != nil {
		return State{}, err
	}

	state, err := m.Load(ctx)
	if err != nil {
		return State{}, err
	}
	state.Scope = ScopeGlobal
	state.Method = method
	state.UpdatedAt = m.now().UTC()
	if m.store != nil {
		if err := m.store.Save(ctx, state); err != nil {
			return State{}, err
		}
	}
	return state, nil
}

func (m *Manager) ClearMethod(ctx context.Context, isIdle bool) (State, error) {
	if err := EnsureIdleForMethodSwitch(isIdle); err != nil {
		return State{}, err
	}

	state, err := m.Load(ctx)
	if err != nil {
		return State{}, err
	}
	state.Scope = ScopeGlobal
	state.Method = Method{Type: MethodNone}
	state.UpdatedAt = m.now().UTC()
	if m.store != nil {
		if err := m.store.Save(ctx, state); err != nil {
			return State{}, err
		}
	}
	return state, nil
}

func (m *Manager) AuthorizationHeader(ctx context.Context) (string, error) {
	state, err := m.Load(ctx)
	if err != nil {
		return "", err
	}
	if !state.IsConfigured() {
		return "", ErrAuthNotConfigured
	}

	method := state.Method
	if m.refresher != nil {
		var refreshed bool
		method, refreshed, err = m.refresher.MaybeRefresh(ctx, method)
		if err != nil {
			return "", err
		}
		if refreshed {
			state.Method = method
			state.UpdatedAt = m.now().UTC()
			if m.store != nil {
				if err := m.store.Save(ctx, state); err != nil {
					return "", err
				}
			}
		}
	}

	return method.AuthHeaderValue()
}

// OpenAIAuthMetadata exposes auth mode details for OpenAI transport behavior.
func (m *Manager) OpenAIAuthMetadata(ctx context.Context) (method string, accountID string, err error) {
	state, err := m.Load(ctx)
	if err != nil {
		return "", "", err
	}
	switch state.Method.Type {
	case MethodOAuth:
		if state.Method.OAuth != nil {
			return string(MethodOAuth), state.Method.OAuth.AccountID, nil
		}
		return string(MethodOAuth), "", nil
	case MethodAPIKey:
		return string(MethodAPIKey), "", nil
	default:
		return "", "", nil
	}
}
