package app

import (
	"context"
	"errors"
	"io"

	"builder/shared/clientui"
	"builder/shared/serverapi"
)

func startSessionActivityEvents(ctx context.Context, sub serverapi.SessionActivitySubscription) (<-chan clientui.Event, func()) {
	out := make(chan clientui.Event, 64)
	if sub == nil {
		close(out)
		return out, func() {}
	}
	pollCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(out)
		defer func() { _ = sub.Close() }()
		for {
			evt, err := sub.Next(pollCtx)
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
					return
				}
				return
			}
			select {
			case <-pollCtx.Done():
				return
			case out <- evt:
			}
		}
	}()
	return out, cancel
}
