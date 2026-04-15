package sessionlifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"builder/server/auth"
	serverlifecycle "builder/server/lifecycle"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/sessionpath"
	"builder/shared/serverapi"
)

type Service struct {
	persistenceRoot string
	containerDir    string
	stores          sessionStoreResolver
	authManager     *auth.Manager
	storeOptions    []session.StoreOption
	resolveMu       sync.Mutex
	resolves        map[string]*resolveTransitionEntry
}

type resolveTransitionFingerprint struct {
	sessionID                string
	action                   string
	initialPrompt            string
	initialInput             string
	targetSessionID          string
	forkUserMessageIndex     int
	forkTranscriptEntryIndex int
	hasForkTranscriptEntry   bool
	parentSessionID          string
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

func NewGlobalService(persistenceRoot string, stores sessionStoreResolver, authManager *auth.Manager, storeOptions ...session.StoreOption) *Service {
	return &Service{persistenceRoot: strings.TrimSpace(persistenceRoot), stores: stores, authManager: authManager, storeOptions: append([]session.StoreOption(nil), storeOptions...), resolves: map[string]*resolveTransitionEntry{}}
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
			sessionID:              strings.TrimSpace(req.SessionID),
			action:                 strings.TrimSpace(req.Transition.Action),
			initialPrompt:          req.Transition.InitialPrompt,
			initialInput:           req.Transition.InitialInput,
			targetSessionID:        strings.TrimSpace(req.Transition.TargetSessionID),
			forkUserMessageIndex:   req.Transition.ForkUserMessageIndex,
			hasForkTranscriptEntry: req.Transition.ForkTranscriptEntryIndex != nil,
			parentSessionID:        strings.TrimSpace(req.Transition.ParentSessionID),
		}
		if req.Transition.ForkTranscriptEntryIndex != nil {
			fp.forkTranscriptEntryIndex = *req.Transition.ForkTranscriptEntryIndex
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
		forkUserMessageIndex, resolveErr := s.resolveForkUserMessageIndex(ctx, store, req.Transition)
		if resolveErr != nil {
			return serverapi.SessionResolveTransitionResponse{}, resolveErr
		}
		req.Transition.ForkUserMessageIndex = forkUserMessageIndex
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

func (s *Service) resolveForkUserMessageIndex(ctx context.Context, store *session.Store, transition serverapi.SessionTransition) (int, error) {
	if transition.ForkTranscriptEntryIndex != nil {
		return resolveForkUserMessageIndexFromTranscriptEntry(ctx, store, *transition.ForkTranscriptEntryIndex)
	}
	if transition.ForkUserMessageIndex > 0 {
		return transition.ForkUserMessageIndex, nil
	}
	return 0, errors.New("rollback fork target is required")
}

func resolveForkUserMessageIndexFromTranscriptEntry(ctx context.Context, store *session.Store, transcriptEntryIndex int) (int, error) {
	if store == nil {
		return 0, errors.New("current store is required for rollback fork")
	}
	if transcriptEntryIndex < 0 {
		return 0, errors.New("rollback fork transcript entry index must be >= 0")
	}
	scan := runtime.NewPersistedTranscriptScan(runtime.PersistedTranscriptScanRequest{Offset: 0, Limit: transcriptEntryIndex + 1})
	resolvedUserIndex := 0
	resolved := false
	if err := store.WalkEvents(func(evt session.Event) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := scan.ApplyPersistedEvent(evt); err != nil {
			return err
		}
		if scan.TotalEntries() <= transcriptEntryIndex {
			return nil
		}
		page := scan.CollectedPageSnapshot()
		if transcriptEntryIndex >= len(page.Entries) {
			return fmt.Errorf("rollback fork transcript entry index %d is out of range", transcriptEntryIndex)
		}
		if strings.TrimSpace(page.Entries[transcriptEntryIndex].Role) != "user" {
			return fmt.Errorf("rollback fork transcript entry %d is not a user message", transcriptEntryIndex)
		}
		for idx := 0; idx <= transcriptEntryIndex; idx++ {
			if strings.TrimSpace(page.Entries[idx].Role) == "user" {
				resolvedUserIndex++
			}
		}
		resolved = true
		return io.EOF
	}); err != nil {
		if errors.Is(err, io.EOF) && resolved {
			return resolvedUserIndex, nil
		}
		return 0, err
	}
	if resolved {
		return resolvedUserIndex, nil
	}
	if scan.TotalEntries() <= transcriptEntryIndex {
		return 0, fmt.Errorf("rollback fork transcript entry index %d is out of range", transcriptEntryIndex)
	}
	return 0, fmt.Errorf("rollback fork transcript entry index %d is out of range", transcriptEntryIndex)
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
		if strings.TrimSpace(s.persistenceRoot) == "" {
			return nil, nil
		}
		return session.OpenByID(s.persistenceRoot, trimmed, s.storeOptions...)
	}
	sessionDir, err := sessionpath.ResolveScopedSessionDir(s.containerDir, trimmed)
	if err != nil {
		return nil, err
	}
	return session.Open(sessionDir, s.storeOptions...)
}
