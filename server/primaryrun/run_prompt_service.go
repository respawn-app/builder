package primaryrun

import (
	"context"
	"strings"

	"builder/shared/serverapi"
)

type guardingPromptService struct {
	inner serverapi.RunPromptService
	gate  Gate
}

func NewGuardingPromptService(gate Gate, inner serverapi.RunPromptService) serverapi.RunPromptService {
	if inner == nil {
		return nil
	}
	if gate == nil {
		return inner
	}
	return &guardingPromptService{inner: inner, gate: gate}
}

func (s *guardingPromptService) RunPrompt(ctx context.Context, req serverapi.RunPromptRequest, progress serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
	selectedSessionID := strings.TrimSpace(req.SelectedSessionID)
	if selectedSessionID == "" {
		return s.inner.RunPrompt(ctx, req, progress)
	}
	lease, err := s.gate.AcquirePrimaryRun(selectedSessionID)
	if err != nil {
		return serverapi.RunPromptResponse{}, err
	}
	defer lease.Release()
	return s.inner.RunPrompt(ctx, req, progress)
}

var _ serverapi.RunPromptService = (*guardingPromptService)(nil)
