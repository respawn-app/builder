package serverapi

import (
	"context"
	"errors"
	"strings"

	"builder/shared/clientui"
)

var ErrSessionActivityGap = errors.New("session activity subscriber fell behind and must rehydrate")

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
	if strings.TrimSpace(r.SessionID) == "" {
		return errors.New("session_id is required")
	}
	return nil
}
