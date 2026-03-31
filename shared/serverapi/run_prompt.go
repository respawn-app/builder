package serverapi

import (
	"context"
	"errors"
	"strings"
	"time"
)

type RunPromptRequest struct {
	ClientRequestID   string
	SelectedSessionID string
	Prompt            string
	Timeout           time.Duration
}

type RunPromptResponse struct {
	SessionID   string
	SessionName string
	Result      string
	Duration    time.Duration
}

type RunPromptProgress struct {
	Kind    RunPromptProgressKind
	Message string
}

type RunPromptProgressKind string

const (
	RunPromptProgressKindStatus  RunPromptProgressKind = "status"
	RunPromptProgressKindWarning RunPromptProgressKind = "warning"
)

type RunPromptProgressSink interface {
	PublishRunPromptProgress(RunPromptProgress)
}

type RunPromptProgressFunc func(RunPromptProgress)

func (fn RunPromptProgressFunc) PublishRunPromptProgress(progress RunPromptProgress) {
	if fn != nil {
		fn(progress)
	}
}

type PromptAssistantMessage struct {
	Content string
}

type PromptSessionRuntime interface {
	SubmitUserMessage(ctx context.Context, prompt string) (PromptAssistantMessage, error)
	SessionID() string
	SessionName() string
	DroppedEvents() uint64
	Logf(format string, args ...any)
	Close() error
}

type HeadlessPromptLauncher interface {
	PrepareHeadlessPrompt(ctx context.Context, req RunPromptRequest, progress RunPromptProgressSink) (PromptSessionRuntime, error)
}

type RunPromptService interface {
	RunPrompt(ctx context.Context, req RunPromptRequest, progress RunPromptProgressSink) (RunPromptResponse, error)
}

type PromptService struct {
	launcher HeadlessPromptLauncher
}

func NewPromptService(launcher HeadlessPromptLauncher) *PromptService {
	return &PromptService{launcher: launcher}
}

func (s *PromptService) RunPrompt(ctx context.Context, req RunPromptRequest, progress RunPromptProgressSink) (RunPromptResponse, error) {
	if s == nil || s.launcher == nil {
		return RunPromptResponse{}, errors.New("prompt service launcher is required")
	}
	req.ClientRequestID = strings.TrimSpace(req.ClientRequestID)
	req.SelectedSessionID = strings.TrimSpace(req.SelectedSessionID)
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.ClientRequestID == "" {
		return RunPromptResponse{}, errors.New("client_request_id is required")
	}
	if req.Prompt == "" {
		return RunPromptResponse{}, errors.New("prompt is required")
	}

	runtimeHandle, err := s.launcher.PrepareHeadlessPrompt(ctx, req, progress)
	if err != nil {
		return RunPromptResponse{}, err
	}
	defer func() {
		_ = runtimeHandle.Close()
	}()

	runCtx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	startedAt := time.Now()
	assistant, runErr := runtimeHandle.SubmitUserMessage(runCtx, req.Prompt)
	duration := time.Since(startedAt)
	result := RunPromptResponse{
		SessionID:   runtimeHandle.SessionID(),
		SessionName: runtimeHandle.SessionName(),
		Result:      assistant.Content,
		Duration:    duration,
	}
	if dropped := runtimeHandle.DroppedEvents(); dropped > 0 {
		runtimeHandle.Logf("runtime.event.drop.total=%d", dropped)
	}
	if runErr != nil {
		runtimeHandle.Logf("app.run_prompt.exit err=%q", runErr.Error())
		return result, runErr
	}
	runtimeHandle.Logf("app.run_prompt.exit ok")
	return result, nil
}
