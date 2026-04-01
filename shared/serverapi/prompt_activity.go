package serverapi

import (
	"context"
	"errors"
	"strings"

	"builder/shared/clientui"
)

type PromptActivitySubscribeRequest struct {
	SessionID string
}

type PromptActivitySubscription interface {
	Next(ctx context.Context) (clientui.PendingPromptEvent, error)
	Close() error
}

type PromptActivityService interface {
	SubscribePromptActivity(ctx context.Context, req PromptActivitySubscribeRequest) (PromptActivitySubscription, error)
}

func (r PromptActivitySubscribeRequest) Validate() error {
	if strings.TrimSpace(r.SessionID) == "" {
		return errors.New("session_id is required")
	}
	return nil
}
