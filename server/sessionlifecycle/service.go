package sessionlifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"builder/server/auth"
	serverlifecycle "builder/server/lifecycle"
	"builder/server/session"
	"builder/server/sessionpath"
	"builder/shared/serverapi"
)

type Service struct {
	containerDir string
	stores       sessionStoreResolver
	authManager  *auth.Manager
	storeOptions []session.StoreOption
	resolveMu    sync.Mutex
	resolves     map[string]*resolveTransitionEntry
}

type resolveTransitionFingerprint struct {
	sessionID             string
	action                string
	initialPrompt         string
	initialInput          string
	targetSessionID       string
	forkUserMessageIndex  int
	parentSessionID       string
}

type resolveTransitionEntry struct {
	fingerprint resolveTransitionFingerprint
	response    serverapi.SessionResolveTransitionResponse
	err         error
	done        bool
	cacheable   bool
	completedAt time.Time
	ready       chan struct{}
}

const resolveTransitionDedupeRetention = 10 * time.Minute

var resolveTransitionDedupeNow = time.Now

type sessionStoreResolver interface {
	ResolveStore(ctx context.Context, sessionID string) (*session.Store, error)
}

func NewService(containerDir string, stores sessionStoreResolver, authManager *auth.Manager, storeOptions ...session.StoreOption) *Service {
	return &Service{containerDir: strings.TrimSpace(containerDir), stores: stores, authManager: authManager, storeOptions: append([]session.StoreOption(nil), storeOptions...), resolves: map[string]*resolveTransitionEntry{}}
}

func (s *Service) sweepExpiredResolveEntriesLocked(now time.Time) {
	for key, entry := range s.resolves {
		if entry == nil || !entry.done || entry.completedAt.IsZero() {
			continue
		}
		if now.Sub(entry.completedAt) >= resolveTransitionDedupeRetention {
			delete(s.resolves, key)
		}
	}
}

func (s *Service) GetInitialInput(_ context.Context, req serverapi.SessionInitialInputRequest) (serverapi.SessionInitialInputResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.SessionInitialInputResponse{}, err
	}
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
	for {
		key := strings.TrimSpace(req.ClientRequestID)
		fp := resolveTransitionFingerprint{
			sessionID:            strings.TrimSpace(req.SessionID),
			action:               strings.TrimSpace(req.Transition.Action),
			initialPrompt:        req.Transition.InitialPrompt,
			initialInput:         req.Transition.InitialInput,
			targetSessionID:      strings.TrimSpace(req.Transition.TargetSessionID),
			forkUserMessageIndex: req.Transition.ForkUserMessageIndex,
			parentSessionID:      strings.TrimSpace(req.Transition.ParentSessionID),
		}

		s.resolveMu.Lock()
		s.sweepExpiredResolveEntriesLocked(resolveTransitionDedupeNow())
		entry, exists := s.resolves[key]
		if exists {
			if entry.fingerprint != fp {
				s.resolveMu.Unlock()
				return serverapi.SessionResolveTransitionResponse{}, fmt.Errorf("client_request_id %q reused with different payload", req.ClientRequestID)
			}
			if entry.done {
				if entry.cacheable {
					response, err := entry.response, entry.err
					s.resolveMu.Unlock()
					return response, err
				}
				delete(s.resolves, key)
				s.resolveMu.Unlock()
				continue
			}
			ready := entry.ready
			s.resolveMu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return serverapi.SessionResolveTransitionResponse{}, ctx.Err()
			}
		}

		entry = &resolveTransitionEntry{fingerprint: fp, ready: make(chan struct{})}
		s.resolves[key] = entry
		s.resolveMu.Unlock()

		response, err := s.resolveTransitionOnce(ctx, req)
		cacheable := !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)

		s.resolveMu.Lock()
		entry.response = response
		entry.err = err
		entry.done = true
		entry.cacheable = cacheable
		entry.completedAt = resolveTransitionDedupeNow()
		close(entry.ready)
		if !cacheable {
			delete(s.resolves, key)
		}
		s.resolveMu.Unlock()
		return response, err
	}
}

func (s *Service) resolveTransitionOnce(ctx context.Context, req serverapi.SessionResolveTransitionRequest) (serverapi.SessionResolveTransitionResponse, error) {
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
	if s != nil && s.stores != nil {
		if store, err := s.stores.ResolveStore(context.Background(), trimmed); err != nil {
			return nil, err
		} else if store != nil {
			return store, nil
		}
	}
	if strings.TrimSpace(s.containerDir) == "" {
		return nil, nil
	}
	sessionDir, err := sessionpath.ResolveScopedSessionDir(s.containerDir, trimmed)
	if err != nil {
		return nil, err
	}
	return session.Open(sessionDir, s.storeOptions...)
}
