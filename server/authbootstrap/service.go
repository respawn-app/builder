package authbootstrap

import (
	"context"
	"strings"

	"builder/server/auth"
	"builder/shared/serverapi"
)

type Service struct {
	manager        *auth.Manager
	oauthOptions   auth.OpenAIOAuthOptions
	allowedPreAuth []string
	supportedModes []serverapi.AuthBootstrapMode
}

func NewService(manager *auth.Manager, oauthOptions auth.OpenAIOAuthOptions, allowedPreAuthMethods []string) *Service {
	return &Service{
		manager:        manager,
		oauthOptions:   oauthOptions,
		allowedPreAuth: append([]string(nil), allowedPreAuthMethods...),
		supportedModes: []serverapi.AuthBootstrapMode{
			serverapi.AuthBootstrapModeBrowserCallbackURL,
			serverapi.AuthBootstrapModeBrowserCallbackCode,
			serverapi.AuthBootstrapModeDeviceCode,
			serverapi.AuthBootstrapModeAPIKey,
		},
	}
}

func (s *Service) GetBootstrapStatus(ctx context.Context, _ serverapi.AuthGetBootstrapStatusRequest) (serverapi.AuthGetBootstrapStatusResponse, error) {
	ready, err := s.authReady(ctx)
	if err != nil {
		return serverapi.AuthGetBootstrapStatusResponse{}, err
	}
	return serverapi.AuthGetBootstrapStatusResponse{
		AuthReady:              ready,
		AuthBootstrapSupported: true,
		AllowedPreAuthMethods:  append([]string(nil), s.allowedPreAuth...),
		SupportedModes:         append([]serverapi.AuthBootstrapMode(nil), s.supportedModes...),
		OAuth: serverapi.AuthBootstrapOAuthConfig{
			Issuer:   strings.TrimSpace(s.oauthOptions.Issuer),
			ClientID: strings.TrimSpace(s.oauthOptions.ClientID),
		},
	}, nil
}

func (s *Service) CompleteBootstrap(ctx context.Context, req serverapi.AuthCompleteBootstrapRequest) (serverapi.AuthCompleteBootstrapResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.AuthCompleteBootstrapResponse{}, err
	}
	if s == nil || s.manager == nil {
		return serverapi.AuthCompleteBootstrapResponse{}, serverapi.ErrServerAuthRequired
	}
	var (
		method auth.Method
		err    error
	)
	switch req.Mode {
	case serverapi.AuthBootstrapModeAPIKey:
		method = auth.Method{Type: auth.MethodAPIKey, APIKey: &auth.APIKeyMethod{Key: strings.TrimSpace(req.APIKey)}}
	case serverapi.AuthBootstrapModeBrowserCallbackURL, serverapi.AuthBootstrapModeBrowserCallbackCode:
		method, err = auth.CompleteOpenAIBrowserFlow(ctx, s.oauthOptions, auth.BrowserAuthSession{
			RedirectURI:  strings.TrimSpace(req.RedirectURI),
			State:        strings.TrimSpace(req.OAuthState),
			CodeVerifier: strings.TrimSpace(req.OAuthCodeVerifier),
		}, req.CallbackInput)
	case serverapi.AuthBootstrapModeDeviceCode:
		method, err = auth.CompleteOpenAIDeviceAuthorizationGrant(ctx, s.oauthOptions, strings.TrimSpace(req.DeviceAuthorizationCode), strings.TrimSpace(req.DeviceCodeVerifier))
	default:
		return serverapi.AuthCompleteBootstrapResponse{}, req.Validate()
	}
	if err != nil {
		return serverapi.AuthCompleteBootstrapResponse{}, err
	}
	state, err := s.manager.SwitchMethodAndSetEnvAPIKeyPreference(ctx, method, auth.EnvAPIKeyPreferencePreferSaved, true, true)
	if err != nil {
		return serverapi.AuthCompleteBootstrapResponse{}, err
	}
	return serverapi.AuthCompleteBootstrapResponse{
		AuthReady:  state.IsConfigured(),
		MethodType: strings.TrimSpace(string(state.Method.Type)),
		AccountID:  methodAccountID(state.Method),
		Email:      methodEmail(state.Method),
	}, nil
}

func (s *Service) authReady(ctx context.Context) (bool, error) {
	if s == nil || s.manager == nil {
		return false, nil
	}
	state, err := s.manager.Load(ctx)
	if err != nil {
		return false, err
	}
	return auth.EvaluateStartupGate(state).Ready, nil
}

func methodAccountID(method auth.Method) string {
	if method.Type == auth.MethodOAuth && method.OAuth != nil {
		return strings.TrimSpace(method.OAuth.AccountID)
	}
	return ""
}

func methodEmail(method auth.Method) string {
	if method.Type == auth.MethodOAuth && method.OAuth != nil {
		return strings.TrimSpace(method.OAuth.Email)
	}
	return ""
}

var _ serverapi.AuthBootstrapService = (*Service)(nil)
