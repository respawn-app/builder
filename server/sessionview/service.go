package sessionview

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/runtimeview"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type Service struct {
	store   *session.Store
	runtime *runtime.Engine
	reads   runtimeview.Reader
}

func NewService(store *session.Store, engine *runtime.Engine) *Service {
	service := &Service{store: store, runtime: engine}
	if engine != nil {
		service.reads = runtimeview.NewReader(engine)
	}
	return service
}

func (s *Service) GetSessionMainView(ctx context.Context, req serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.SessionMainViewResponse{}, err
	}
	if err := s.validateSessionID(req.SessionID); err != nil {
		return serverapi.SessionMainViewResponse{}, err
	}
	if s.reads != nil {
		return serverapi.SessionMainViewResponse{MainView: s.reads.MainView()}, nil
	}
	view, err := s.dormantMainView(ctx)
	if err != nil {
		return serverapi.SessionMainViewResponse{}, err
	}
	return serverapi.SessionMainViewResponse{MainView: view}, nil
}

func (s *Service) GetRun(ctx context.Context, req serverapi.RunGetRequest) (serverapi.RunGetResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.RunGetResponse{}, err
	}
	if err := s.validateSessionID(req.SessionID); err != nil {
		return serverapi.RunGetResponse{}, err
	}
	if s.reads != nil {
		main := s.reads.MainView()
		if main.ActiveRun != nil && strings.TrimSpace(main.ActiveRun.RunID) == strings.TrimSpace(req.RunID) {
			return serverapi.RunGetResponse{Run: main.ActiveRun}, nil
		}
	}
	if s.store == nil {
		return serverapi.RunGetResponse{}, errors.New("session store is required")
	}
	runs, err := s.store.ReadRuns()
	if err != nil {
		return serverapi.RunGetResponse{}, err
	}
	for _, run := range runs {
		if run.RunID == strings.TrimSpace(req.RunID) {
			copyRun := run
			return serverapi.RunGetResponse{Run: runtimeview.RunViewFromSessionRecord(s.store.Meta().SessionID, &copyRun)}, nil
		}
	}
	return serverapi.RunGetResponse{}, fmt.Errorf("run %q not found", strings.TrimSpace(req.RunID))
}

func (s *Service) validateSessionID(sessionID string) error {
	if s == nil {
		return errors.New("session view service is required")
	}
	want := ""
	if s.store != nil {
		want = strings.TrimSpace(s.store.Meta().SessionID)
	} else if s.reads != nil {
		want = strings.TrimSpace(s.reads.MainView().Session.SessionID)
	} else {
		return errors.New("session store is required")
	}
	if got := strings.TrimSpace(sessionID); got != want {
		return fmt.Errorf("session %q not available", got)
	}
	return nil
}

func (s *Service) dormantMainView(ctx context.Context) (clientui.RuntimeMainView, error) {
	meta := s.store.Meta()
	freshness := runtimeview.ConversationFreshnessFromSession(s.store.ConversationFreshness())
	chat, lastAnswer, err := replayDormantSession(ctx, s.store)
	if err != nil {
		return clientui.RuntimeMainView{}, err
	}
	view := clientui.RuntimeMainView{
		Status: clientui.RuntimeStatus{
			ConversationFreshness:             freshness,
			ParentSessionID:                   meta.ParentSessionID,
			LastCommittedAssistantFinalAnswer: lastAnswer,
		},
		Session: clientui.RuntimeSessionView{
			SessionID:             meta.SessionID,
			SessionName:           meta.Name,
			ConversationFreshness: freshness,
			Chat:                  chat,
		},
	}
	latestRun, err := s.store.LatestRun()
	if err != nil {
		return clientui.RuntimeMainView{}, err
	}
	if latestRun != nil && latestRun.Status == session.RunStatusRunning {
		view.ActiveRun = runtimeview.RunViewFromSessionRecord(meta.SessionID, latestRun)
	}
	return view, nil
}

type dormantReplayClient struct{}

func (dormantReplayClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("replay client does not support generation")
}

func (dormantReplayClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return llm.ProviderCapabilities{ProviderID: "replay"}, nil
}

func replayDormantSession(ctx context.Context, store *session.Store) (clientui.ChatSnapshot, string, error) {
	if store == nil {
		return clientui.ChatSnapshot{}, "", errors.New("session store is required")
	}
	model := "gpt-5"
	if locked := store.Meta().Locked; locked != nil && strings.TrimSpace(locked.Model) != "" {
		model = strings.TrimSpace(locked.Model)
	}
	eng, err := runtime.New(store, dormantReplayClient{}, tools.NewRegistry(), runtime.Config{Model: model})
	if err != nil {
		return clientui.ChatSnapshot{}, "", err
	}
	select {
	case <-ctx.Done():
		return clientui.ChatSnapshot{}, "", ctx.Err()
	default:
	}
	return runtimeview.ChatSnapshotFromRuntime(eng.ChatSnapshot()), eng.LastCommittedAssistantFinalAnswer(), nil
}

var _ serverapi.SessionViewService = (*Service)(nil)
