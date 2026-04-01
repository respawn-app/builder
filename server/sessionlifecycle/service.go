package sessionlifecycle

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"builder/server/auth"
	serverlifecycle "builder/server/lifecycle"
	"builder/server/session"
	"builder/shared/serverapi"
)

type Service struct {
	persistenceRoot string
	authManager     *auth.Manager
}

func NewService(persistenceRoot string, authManager *auth.Manager) *Service {
	return &Service{persistenceRoot: strings.TrimSpace(persistenceRoot), authManager: authManager}
}

func (s *Service) GetInitialInput(_ context.Context, req serverapi.SessionInitialInputRequest) (serverapi.SessionInitialInputResponse, error) {
	store, err := s.openStore(req.SessionID)
	if err != nil {
		return serverapi.SessionInitialInputResponse{}, err
	}
	return serverapi.SessionInitialInputResponse{Input: serverlifecycle.InitialInput(store, req.TransitionInput)}, nil
}

func (s *Service) PersistInputDraft(_ context.Context, req serverapi.SessionPersistInputDraftRequest) (serverapi.SessionPersistInputDraftResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.SessionPersistInputDraftResponse{}, err
	}
	store, err := s.openStore(req.SessionID)
	if err != nil {
		return serverapi.SessionPersistInputDraftResponse{}, err
	}
	if err := serverlifecycle.PersistInputDraft(store, req.Input); err != nil {
		return serverapi.SessionPersistInputDraftResponse{}, err
	}
	return serverapi.SessionPersistInputDraftResponse{}, nil
}

func (s *Service) ResolveTransition(ctx context.Context, req serverapi.SessionResolveTransitionRequest) (serverapi.SessionResolveTransitionResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.SessionResolveTransitionResponse{}, err
	}
	action := serverlifecycle.Action(req.Transition.Action)
	if action == serverlifecycle.ActionLogout {
		if s.authManager == nil {
			return serverapi.SessionResolveTransitionResponse{}, errors.New("auth manager is required for logout")
		}
		if _, err := s.authManager.ClearMethod(ctx, true); err != nil {
			return serverapi.SessionResolveTransitionResponse{}, err
		}
		return serverapi.SessionResolveTransitionResponse{
			NextSessionID:  strings.TrimSpace(req.SessionID),
			ShouldContinue: true,
			RequiresReauth: true,
		}, nil
	}

	var (
		store *session.Store
		err   error
	)
	if action == serverlifecycle.ActionForkRollback {
		store, err = s.openStore(req.SessionID)
		if err != nil {
			return serverapi.SessionResolveTransitionResponse{}, err
		}
	}
	resolved, err := serverlifecycle.Resolve(ctx, serverlifecycle.ResolveRequest{
		Store: store,
		Transition: serverlifecycle.Transition{
			Action:               action,
			InitialPrompt:        req.Transition.InitialPrompt,
			InitialInput:         req.Transition.InitialInput,
			TargetSessionID:      req.Transition.TargetSessionID,
			ForkUserMessageIndex: req.Transition.ForkUserMessageIndex,
			ParentSessionID:      req.Transition.ParentSessionID,
		},
		AuthManager: s.authManager,
	})
	if err != nil {
		return serverapi.SessionResolveTransitionResponse{}, err
	}
	return serverapi.SessionResolveTransitionResponse{
		NextSessionID:   resolved.NextSessionID,
		InitialPrompt:   resolved.InitialPrompt,
		InitialInput:    resolved.InitialInput,
		ParentSessionID: resolved.ParentSessionID,
		ForceNewSession: resolved.ForceNewSession,
		ShouldContinue:  resolved.ShouldContinue,
		RequiresReauth:  resolved.RequiresReauth,
	}, nil
}

func (s *Service) openStore(sessionID string) (*session.Store, error) {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil, nil
	}
	if store, err := session.OpenByID(s.persistenceRoot, trimmed); err == nil {
		return store, nil
	}
	if strings.TrimSpace(s.persistenceRoot) == "" {
		return nil, nil
	}
	return session.Open(filepath.Join(s.persistenceRoot, trimmed))
}
