package startup

import (
	"context"
	"os"

	"builder/server/auth"
	"builder/server/authflow"
	"builder/server/onboarding"
	"builder/shared/config"
)

type headlessAuthHandler struct {
	lookupEnv func(string) string
}

type headlessOnboardingHandler struct{}

func NewHeadlessHandlers(lookupEnv func(string) string) (AuthHandler, OnboardingHandler) {
	return headlessAuthHandler{lookupEnv: lookupEnv}, headlessOnboardingHandler{}
}

func (h headlessAuthHandler) WrapStore(base auth.Store) auth.Store {
	return authflow.WrapStoreWithEnvAPIKeyOverride(base, h.LookupEnv)
}

func (h headlessAuthHandler) NeedsInteraction(req authflow.InteractionRequest) bool {
	return !req.Gate.Ready
}

func (h headlessAuthHandler) Interact(context.Context, authflow.InteractionRequest) error {
	return auth.ErrAuthNotConfigured
}

func (h headlessAuthHandler) LookupEnv(key string) string {
	if h.lookupEnv == nil {
		return os.Getenv(key)
	}
	return h.lookupEnv(key)
}

func (headlessOnboardingHandler) EnsureOnboardingReady(ctx context.Context, req OnboardingRequest) (config.App, error) {
	cfg, _, err := onboarding.EnsureReady(ctx, req.Config, req.AuthManager, false, req.ReloadConfig, nil)
	if err != nil {
		return config.App{}, err
	}
	return cfg, nil
}
