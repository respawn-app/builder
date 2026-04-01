package sessionactivity

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

type Subscriber interface {
	SubscribeSessionActivity(ctx context.Context, sessionID string) (serverapi.SessionActivitySubscription, error)
}

type Service struct {
	subscriber Subscriber
}

func NewService(subscriber Subscriber) *Service {
	return &Service{subscriber: subscriber}
}

func (s *Service) SubscribeSessionActivity(ctx context.Context, req serverapi.SessionActivitySubscribeRequest) (serverapi.SessionActivitySubscription, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if s == nil || s.subscriber == nil {
		return nil, errors.New("session activity subscriber is required")
	}
	return s.subscriber.SubscribeSessionActivity(ctx, req.SessionID)
}

var _ serverapi.SessionActivityService = (*Service)(nil)
