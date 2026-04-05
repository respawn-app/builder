package serverapi

import (
	"context"

	"builder/shared/clientui"
)

type SessionActivitySubscribeRequest struct {
	SessionID string
}

type SessionActivitySubscription interface {
	Next(ctx context.Context) (clientui.Event, error)
	Close() error
}

type SessionActivityService interface {
	SubscribeSessionActivity(ctx context.Context, req SessionActivitySubscribeRequest) (SessionActivitySubscription, error)
}

func (r SessionActivitySubscribeRequest) Validate() error {
	return validateRequiredSessionID(r.SessionID)
}
