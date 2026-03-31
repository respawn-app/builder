package authflow

import (
	"context"
	"errors"
	"os"
	"strings"

	"builder/server/auth"
	"builder/shared/config"
)

type InteractionRequest struct {
	Manager         *auth.Manager
	State           auth.State
	StoredState     auth.State
	Gate            auth.StartupGate
	StartupErr      error
	FlowErr         error
	OAuthOptions    auth.OpenAIOAuthOptions
	Theme           string
	AlternateScreen config.TUIAlternateScreenPolicy
	HasEnvAPIKey    bool
}

type Handler interface {
	NeedsInteraction(req InteractionRequest) bool
	Interact(ctx context.Context, req InteractionRequest) error
}

func WrapStoreWithEnvAPIKeyOverride(base auth.Store, lookupEnv func(string) string) auth.Store {
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}
	return auth.NewEnvAPIKeyOverrideStore(base, func(key string) (string, bool) {
		value := lookupEnv(key)
		return value, strings.TrimSpace(value) != ""
	})
}

func EnsureReady(ctx context.Context, mgr *auth.Manager, oauthOpts auth.OpenAIOAuthOptions, theme string, alternateScreen config.TUIAlternateScreenPolicy, lookupEnv func(string) string, handler Handler) error {
	if mgr == nil {
		return errors.New("auth manager is required")
	}
	if handler == nil {
		return errors.New("auth flow handler is required")
	}
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}
	for {
		state, err := mgr.Load(ctx)
		if err != nil {
			return err
		}
		storedState, err := mgr.StoredState(ctx)
		if err != nil {
			return err
		}
		gate := auth.EvaluateStartupGate(state)
		var startupErr error
		if !gate.Ready {
			startupErr = auth.EnsureStartupReady(state)
		}
		req := InteractionRequest{
			Manager:         mgr,
			State:           state,
			StoredState:     storedState,
			Gate:            gate,
			StartupErr:      startupErr,
			OAuthOptions:    oauthOpts,
			Theme:           theme,
			AlternateScreen: alternateScreen,
			HasEnvAPIKey:    strings.TrimSpace(lookupEnv("OPENAI_API_KEY")) != "",
		}
		if !handler.NeedsInteraction(req) {
			if startupErr != nil {
				return startupErr
			}
			return nil
		}
		if err := handler.Interact(ctx, req); err != nil {
			return err
		}
	}
}
