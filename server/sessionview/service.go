package sessionview

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"builder/server/runtime"
	"builder/server/runtimeview"
	"builder/server/session"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type SessionStoreResolver interface {
	ResolveSessionStore(ctx context.Context, sessionID string) (*session.Store, error)
}

type RuntimeResolver interface {
	ResolveRuntime(ctx context.Context, sessionID string) (*runtime.Engine, error)
}

type ExecutionTargetResolver interface {
	ResolveSessionExecutionTarget(ctx context.Context, sessionID string) (clientui.SessionExecutionTarget, error)
}

type Service struct {
	sessions SessionStoreResolver
	runtimes RuntimeResolver
	targets  ExecutionTargetResolver
	dormant  *dormantTranscriptCache
}

func NewService(sessions SessionStoreResolver, runtimes RuntimeResolver, targets ExecutionTargetResolver) *Service {
	return &Service{sessions: sessions, runtimes: runtimes, targets: targets, dormant: newDormantTranscriptCache(nil)}
}

type staticSessionResolver struct {
	store *session.Store
}

func NewStaticSessionResolver(store *session.Store) SessionStoreResolver {
	if store == nil {
		return nil
	}
	return staticSessionResolver{store: store}
}

func (r staticSessionResolver) ResolveSessionStore(_ context.Context, sessionID string) (*session.Store, error) {
	if r.store == nil {
		return nil, errors.New("session store is required")
	}
	if strings.TrimSpace(sessionID) != strings.TrimSpace(r.store.Meta().SessionID) {
		return nil, fmt.Errorf("session %q not available", strings.TrimSpace(sessionID))
	}
	return r.store, nil
}

type staticRuntimeResolver struct {
	engine *runtime.Engine
}

func NewStaticRuntimeResolver(engine *runtime.Engine) RuntimeResolver {
	if engine == nil {
		return nil
	}
	return staticRuntimeResolver{engine: engine}
}

func (r staticRuntimeResolver) ResolveRuntime(_ context.Context, sessionID string) (*runtime.Engine, error) {
	if r.engine == nil {
		return nil, nil
	}
	if strings.TrimSpace(sessionID) != strings.TrimSpace(r.engine.SessionID()) {
		return nil, fmt.Errorf("session %q not available", strings.TrimSpace(sessionID))
	}
	return r.engine, nil
}

func (s *Service) GetSessionMainView(ctx context.Context, req serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.SessionMainViewResponse{}, err
	}
	if runtimeEngine, err := s.resolveRuntime(ctx, req.SessionID); err != nil {
		return serverapi.SessionMainViewResponse{}, err
	} else if runtimeEngine != nil {
		view, err := s.enrichMainViewWithExecutionTarget(ctx, runtimeview.MainViewFromRuntime(runtimeEngine))
		if err != nil {
			return serverapi.SessionMainViewResponse{}, err
		}
		return serverapi.SessionMainViewResponse{MainView: view}, nil
	}
	if store, err := s.resolveSessionStore(ctx, req.SessionID); err != nil {
		return serverapi.SessionMainViewResponse{}, err
	} else if store != nil {
		view, err := s.dormantMainViewFromStore(ctx, store)
		if err != nil {
			return serverapi.SessionMainViewResponse{}, err
		}
		return serverapi.SessionMainViewResponse{MainView: view}, nil
	}
	return serverapi.SessionMainViewResponse{}, errors.New("session store resolver is required")
}

func (s *Service) GetSessionTranscriptPage(ctx context.Context, req serverapi.SessionTranscriptPageRequest) (serverapi.SessionTranscriptPageResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.SessionTranscriptPageResponse{}, err
	}
	pageReq := clientui.TranscriptPageRequest{Offset: req.Offset, Limit: req.Limit, Page: req.Page, PageSize: req.PageSize, Window: req.Window, KnownRevision: req.KnownRevision, KnownCommittedEntryCount: req.KnownCommittedEntryCount}
	pageReq = runtimeview.NormalizeDefaultTranscriptRequest(pageReq)
	if runtimeEngine, err := s.resolveRuntime(ctx, req.SessionID); err != nil {
		return serverapi.SessionTranscriptPageResponse{}, err
	} else if runtimeEngine != nil {
		return serverapi.SessionTranscriptPageResponse{Transcript: runtimeview.TranscriptPageFromRuntime(runtimeEngine, pageReq)}, nil
	}
	if store, err := s.resolveSessionStore(ctx, req.SessionID); err != nil {
		return serverapi.SessionTranscriptPageResponse{}, err
	} else if store != nil {
		page, err := s.dormantTranscriptPageFromStore(ctx, store, pageReq)
		if err != nil {
			return serverapi.SessionTranscriptPageResponse{}, err
		}
		return serverapi.SessionTranscriptPageResponse{Transcript: page}, nil
	}
	return serverapi.SessionTranscriptPageResponse{}, errors.New("session store resolver is required")
}

func (s *Service) GetRun(ctx context.Context, req serverapi.RunGetRequest) (serverapi.RunGetResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.RunGetResponse{}, err
	}
	if runtimeEngine, err := s.resolveRuntime(ctx, req.SessionID); err != nil {
		return serverapi.RunGetResponse{}, err
	} else if runtimeEngine != nil {
		if active := runtimeview.RunViewFromRuntime(runtimeEngine.SessionID(), runtimeEngine.ActiveRun()); active != nil && strings.TrimSpace(active.RunID) == strings.TrimSpace(req.RunID) {
			return serverapi.RunGetResponse{Run: active}, nil
		}
	}
	store, err := s.resolveSessionStore(ctx, req.SessionID)
	if err != nil {
		return serverapi.RunGetResponse{}, err
	}
	if store == nil {
		return serverapi.RunGetResponse{}, errors.New("session store resolver is required")
	}
	runs, err := store.ReadRuns()
	if err != nil {
		return serverapi.RunGetResponse{}, err
	}
	for _, run := range runs {
		if run.RunID == strings.TrimSpace(req.RunID) {
			copyRun := run
			return serverapi.RunGetResponse{Run: runtimeview.RunViewFromSessionRecord(store.Meta().SessionID, &copyRun)}, nil
		}
	}
	return serverapi.RunGetResponse{}, fmt.Errorf("run %q not found", strings.TrimSpace(req.RunID))
}

func (s *Service) resolveSessionStore(ctx context.Context, sessionID string) (*session.Store, error) {
	if s == nil || s.sessions == nil {
		return nil, nil
	}
	return s.sessions.ResolveSessionStore(ctx, sessionID)
}

func (s *Service) resolveRuntime(ctx context.Context, sessionID string) (*runtime.Engine, error) {
	if s == nil || s.runtimes == nil {
		return nil, nil
	}
	return s.runtimes.ResolveRuntime(ctx, sessionID)
}

func (s *Service) enrichMainViewWithExecutionTarget(ctx context.Context, view clientui.RuntimeMainView) (clientui.RuntimeMainView, error) {
	sessionView, err := s.enrichSessionViewWithExecutionTarget(ctx, view.Session)
	if err != nil {
		return clientui.RuntimeMainView{}, err
	}
	view.Session = sessionView
	return view, nil
}

func (s *Service) enrichSessionViewWithExecutionTarget(ctx context.Context, view clientui.RuntimeSessionView) (clientui.RuntimeSessionView, error) {
	if s == nil || s.targets == nil || strings.TrimSpace(view.SessionID) == "" {
		return view, nil
	}
	target, err := s.targets.ResolveSessionExecutionTarget(ctx, view.SessionID)
	if err != nil {
		return clientui.RuntimeSessionView{}, err
	}
	view.ExecutionTarget = target
	return view, nil
}

func (s *Service) dormantMainViewFromStore(ctx context.Context, store *session.Store) (clientui.RuntimeMainView, error) {
	if store == nil {
		return clientui.RuntimeMainView{}, errors.New("session store is required")
	}
	entry, err := s.dormant.get(ctx, store)
	if err != nil {
		return clientui.RuntimeMainView{}, err
	}
	meta := store.Meta()
	freshness := runtimeview.ConversationFreshnessFromSession(store.ConversationFreshness())
	view := entry.mainView(meta, freshness)
	return s.enrichMainViewWithExecutionTarget(ctx, view)
}

func (s *Service) dormantTranscriptPageFromStore(ctx context.Context, store *session.Store, req clientui.TranscriptPageRequest) (clientui.TranscriptPage, error) {
	if store == nil {
		return clientui.TranscriptPage{}, errors.New("session store is required")
	}
	req = runtimeview.NormalizeDefaultTranscriptRequest(req)
	meta := store.Meta()
	freshness := runtimeview.ConversationFreshnessFromSession(store.ConversationFreshness())
	entry, err := s.dormant.get(ctx, store)
	if err != nil {
		return clientui.TranscriptPage{}, err
	}
	if req.Window == clientui.TranscriptWindowOngoingTail {
		return entry.transcriptPageFromTail(meta, freshness, req), nil
	}
	offset := req.Offset
	limit := req.Limit
	if req.PageSize > 0 {
		offset = req.Page * req.PageSize
		limit = req.PageSize
	}
	if page, ok := entry.transcriptPageCoveredByTail(meta, freshness, clientui.TranscriptPageRequest{Offset: offset, Limit: limit}); ok {
		return page, nil
	}
	scan, err := scanDormantTranscript(ctx, store, runtime.PersistedTranscriptScanRequest{Offset: offset, Limit: limit})
	if err != nil {
		return clientui.TranscriptPage{}, err
	}
	if offset > scan.TotalEntries() {
		offset = scan.TotalEntries()
	}
	return runtimeview.TranscriptPageFromCollectedChat(
		meta.SessionID,
		meta.Name,
		freshness,
		meta.LastSequence,
		runtimeview.ChatSnapshotFromRuntime(scan.CollectedPageSnapshot()),
		scan.TotalEntries(),
		offset,
		clientui.TranscriptPageRequest{Offset: offset, Limit: limit},
	), nil
}

func scanDormantTranscript(ctx context.Context, store *session.Store, req runtime.PersistedTranscriptScanRequest) (*runtime.PersistedTranscriptScan, error) {
	scan := runtime.NewPersistedTranscriptScan(req)
	if err := store.WalkEvents(func(evt session.Event) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		return scan.ApplyPersistedEvent(evt)
	}); err != nil {
		return nil, err
	}
	return scan, nil
}

var _ serverapi.SessionViewService = (*Service)(nil)
