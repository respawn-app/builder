package processoutput

import (
	"context"
	"errors"

	shelltool "builder/server/tools/shell"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type Subscriber interface {
	SubscribeOutput(ctx context.Context, processID string, offsetBytes int64) (shelltool.OutputSubscription, error)
}

type Service struct {
	subscriber Subscriber
}

func NewService(subscriber Subscriber) *Service {
	return &Service{subscriber: subscriber}
}

func (s *Service) SubscribeProcessOutput(ctx context.Context, req serverapi.ProcessOutputSubscribeRequest) (serverapi.ProcessOutputSubscription, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if s == nil || s.subscriber == nil {
		return nil, errors.New("process output subscriber is required")
	}
	sub, err := s.subscriber.SubscribeOutput(ctx, req.ProcessID, req.OffsetBytes)
	if err != nil {
		return nil, err
	}
	return &subscription{inner: sub}, nil
}

type subscription struct {
	inner shelltool.OutputSubscription
}

func (s *subscription) Next(ctx context.Context) (clientui.ProcessOutputChunk, error) {
	chunk, err := s.inner.Next(ctx)
	if err != nil {
		return clientui.ProcessOutputChunk{}, err
	}
	return clientui.ProcessOutputChunk{
		ProcessID:   chunk.ProcessID,
		OffsetBytes: chunk.OffsetBytes,
		Text:        chunk.Text,
	}, nil
}

func (s *subscription) Close() error {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.Close()
}

var _ serverapi.ProcessOutputService = (*Service)(nil)
