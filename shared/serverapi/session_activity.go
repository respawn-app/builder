package serverapi

import (
	"context"

	"builder/shared/clientui"
)

type SessionActivitySubscribeRequest struct {
	SessionID     string
	AfterSequence uint64
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
