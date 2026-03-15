package auth

import (
	"context"
	"strings"
)

type envLookup func(string) (string, bool)

type EnvAPIKeyOverrideStore struct {
	base      Store
	lookupEnv envLookup
}

func NewEnvAPIKeyOverrideStore(base Store, lookupEnv envLookup) *EnvAPIKeyOverrideStore {
	if lookupEnv == nil {
		lookupEnv = func(string) (string, bool) { return "", false }
	}
	return &EnvAPIKeyOverrideStore{base: base, lookupEnv: lookupEnv}
}

func (s *EnvAPIKeyOverrideStore) Load(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	if s != nil && s.lookupEnv != nil {
		if key, ok := s.lookupEnv("OPENAI_API_KEY"); ok {
			if trimmed := strings.TrimSpace(key); trimmed != "" {
				return State{
					Scope: ScopeGlobal,
					Method: Method{
						Type:   MethodAPIKey,
						APIKey: &APIKeyMethod{Key: trimmed},
					},
				}, nil
			}
		}
	}
	if s == nil || s.base == nil {
		return EmptyState(), nil
	}
	return s.base.Load(ctx)
}

func (s *EnvAPIKeyOverrideStore) Save(ctx context.Context, state State) error {
	if s == nil || s.base == nil {
		return nil
	}
	return s.base.Save(ctx, state)
}
