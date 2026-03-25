package auth

import (
	"context"
	"fmt"
	"strings"
)

type envLookup func(string) (string, bool)

type EnvAPIKeyOverrideMode string

const (
	EnvAPIKeyOverrideAlways                  EnvAPIKeyOverrideMode = "always"
	EnvAPIKeyOverrideRespectStoredPreference EnvAPIKeyOverrideMode = "respect_stored_preference"
)

type EnvAPIKeyOverrideStore struct {
	base      Store
	lookupEnv envLookup
	mode      EnvAPIKeyOverrideMode
}

func NewEnvAPIKeyOverrideStore(base Store, lookupEnv envLookup, mode EnvAPIKeyOverrideMode) *EnvAPIKeyOverrideStore {
	if lookupEnv == nil {
		lookupEnv = func(string) (string, bool) { return "", false }
	}
	return &EnvAPIKeyOverrideStore{base: base, lookupEnv: lookupEnv, mode: normalizeEnvAPIKeyOverrideMode(mode)}
}

func normalizeEnvAPIKeyOverrideMode(mode EnvAPIKeyOverrideMode) EnvAPIKeyOverrideMode {
	if mode == "" {
		return EnvAPIKeyOverrideAlways
	}
	switch mode {
	case EnvAPIKeyOverrideAlways, EnvAPIKeyOverrideRespectStoredPreference:
		return mode
	default:
		panic(fmt.Sprintf("invalid env api key override mode %q", mode))
	}
}

func (s *EnvAPIKeyOverrideStore) Load(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}

	state := EmptyState()
	if s == nil || s.base == nil {
		state = EmptyState()
	} else {
		var err error
		state, err = s.base.Load(ctx)
		if err != nil {
			return State{}, err
		}
	}

	trimmed, ok := "", false
	if s != nil && s.lookupEnv != nil {
		if key, present := s.lookupEnv("OPENAI_API_KEY"); present {
			trimmed = strings.TrimSpace(key)
			ok = trimmed != ""
		}
	}
	if !ok {
		return state, nil
	}

	shouldOverride := false
	switch s.mode {
	case EnvAPIKeyOverrideAlways:
		shouldOverride = true
	case EnvAPIKeyOverrideRespectStoredPreference:
		shouldOverride = state.EnvAPIKeyPreference == EnvAPIKeyPreferencePreferEnv
	default:
		return State{}, fmt.Errorf("invalid env api key override mode %q", s.mode)
	}
	if !shouldOverride {
		return state, nil
	}
	state.Scope = ScopeGlobal
	state.Method = Method{
		Type:   MethodAPIKey,
		APIKey: &APIKeyMethod{Key: trimmed},
	}
	return state, nil
}

func (s *EnvAPIKeyOverrideStore) Save(ctx context.Context, state State) error {
	if s == nil || s.base == nil {
		return nil
	}
	return s.base.Save(ctx, state)
}
