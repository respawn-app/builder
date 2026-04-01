package app

import (
	"context"
	"io"
	"strings"
	"time"

	"builder/shared/client"
	"builder/shared/serverapi"
	"github.com/google/uuid"
)

const subagentSessionSuffix = "subagent"

type RunPromptResult struct {
	SessionID   string
	SessionName string
	Result      string
	Duration    time.Duration
}

func runPrompt(ctx context.Context, client client.RunPromptClient, opts Options, initialSessionID, prompt string, timeout time.Duration, progress io.Writer) (RunPromptResult, error) {
	response, err := client.RunPrompt(ctx, serverapi.RunPromptRequest{
		ClientRequestID:   uuid.NewString(),
		SelectedSessionID: strings.TrimSpace(initialSessionID),
		Prompt:            prompt,
		Timeout:           timeout,
		Overrides:         runPromptOverridesFromOptions(opts),
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

func runPromptOverridesFromOptions(opts Options) serverapi.RunPromptOverrides {
	return serverapi.RunPromptOverrides{
		Model:               strings.TrimSpace(opts.Model),
		ProviderOverride:    strings.TrimSpace(opts.ProviderOverride),
		ThinkingLevel:       strings.TrimSpace(opts.ThinkingLevel),
		Theme:               strings.TrimSpace(opts.Theme),
		ModelTimeoutSeconds: opts.ModelTimeoutSeconds,
		ShellTimeoutSeconds: opts.ShellTimeoutSeconds,
		Tools:               strings.TrimSpace(opts.Tools),
		OpenAIBaseURL:       strings.TrimSpace(opts.OpenAIBaseURL),
	}
}
