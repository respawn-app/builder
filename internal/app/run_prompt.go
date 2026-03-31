package app

import (
	"context"
	"io"
	"strings"
	"time"

	"builder/internal/serverapi"
	"github.com/google/uuid"
)

const subagentSessionSuffix = "subagent"

type RunPromptResult struct {
	SessionID   string
	SessionName string
	Result      string
	Duration    time.Duration
}

func runPrompt(ctx context.Context, boot appBootstrap, initialSessionID, prompt string, timeout time.Duration, progress io.Writer) (RunPromptResult, error) {
	client := newHeadlessRunPromptClient(boot)
	response, err := client.RunPrompt(ctx, serverapi.RunPromptRequest{
		ClientRequestID:   uuid.NewString(),
		SelectedSessionID: strings.TrimSpace(initialSessionID),
		Prompt:            prompt,
		Timeout:           timeout,
	}, runPromptIOProgressSink{writer: progress})
	result := RunPromptResult{
		SessionID:   response.SessionID,
		SessionName: response.SessionName,
		Result:      response.Result,
		Duration:    response.Duration,
	}
	if err != nil {
		return result, err
	}
	return result, nil
}
