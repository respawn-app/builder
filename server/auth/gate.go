package auth

import (
	"fmt"
)

type StartupGate struct {
	Ready  bool
	Reason string
}

func EvaluateStartupGate(state State) StartupGate {
	if err := state.Validate(); err != nil {
		return StartupGate{Ready: false, Reason: err.Error()}
	}
	if !state.IsConfigured() {
		return StartupGate{Ready: false, Reason: ErrAuthNotConfigured.Error()}
	}
	if _, err := state.Method.AuthHeaderValue(); err != nil {
		return StartupGate{Ready: false, Reason: err.Error()}
	}
	return StartupGate{Ready: true}
}

func EnsureStartupReady(state State) error {
	gate := EvaluateStartupGate(state)
	if gate.Ready {
		return nil
	}
	if gate.Reason == ErrAuthNotConfigured.Error() {
		return ErrAuthNotConfigured
	}
	return fmt.Errorf("startup auth gate: %s", gate.Reason)
}

func EnsureIdleForMethodSwitch(isIdle bool) error {
	if isIdle {
		return nil
	}
	return ErrSwitchRequiresIdle
}
