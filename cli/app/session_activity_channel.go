package app

import (
	"context"
	"errors"
	"time"

	"builder/shared/clientui"
	"builder/shared/serverapi"
)

const sessionActivityResubscribeDelay = 250 * time.Millisecond

type sessionActivitySubscriber func(context.Context) (serverapi.SessionActivitySubscription, error)

func startSessionActivityEvents(ctx context.Context, sub serverapi.SessionActivitySubscription, subscribe sessionActivitySubscriber) (<-chan clientui.Event, func()) {
	out := make(chan clientui.Event, 64)
	if sub == nil || subscribe == nil {
		close(out)
		return out, func() {}
	}
	pollCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(out)
		current := sub
		resubscribed := false
		for {
			evt, err := current.Next(pollCtx)
			if err != nil {
				_ = current.Close()
				if errors.Is(err, context.Canceled) {
					return
				}
				current, err = resubscribeSessionActivity(pollCtx, subscribe)
				if err != nil {
					return
				}
				if resubscribed {
					continue
				}
				resubscribed = true
				select {
				case <-pollCtx.Done():
					_ = current.Close()
					return
				case out <- clientui.Event{Kind: clientui.EventConversationUpdated}:
				}
				continue
			}
			select {
			case <-pollCtx.Done():
				_ = current.Close()
				return
			case out <- evt:
			}
		}
	}()
	return out, cancel
}

func resubscribeSessionActivity(ctx context.Context, subscribe sessionActivitySubscriber) (serverapi.SessionActivitySubscription, error) {
	for {
		if !waitSessionActivityRetry(ctx) {
			return nil, ctx.Err()
		}
		sub, err := subscribe(ctx)
		if err == nil {
			return sub, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
	}
}

func waitSessionActivityRetry(ctx context.Context) bool {
	timer := time.NewTimer(sessionActivityResubscribeDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
