package authflowadapter

import (
	serverauth "builder/server/auth"
	"builder/server/authflow"
	"builder/shared/auth"
)

type InteractionRequest = authflow.InteractionRequest
type InteractionOutcome = authflow.InteractionOutcome
type Store = serverauth.Store
type Method = auth.Method

const (
	MethodNone                     = auth.MethodNone
	MethodOAuth                    = auth.MethodOAuth
	EnvAPIKeyPreferenceUnspecified = auth.EnvAPIKeyPreferenceUnspecified
	EnvAPIKeyPreferencePreferEnv   = auth.EnvAPIKeyPreferencePreferEnv
	EnvAPIKeyPreferencePreferSaved = auth.EnvAPIKeyPreferencePreferSaved
)
