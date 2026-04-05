package app

import (
	"context"
	"errors"
	"os"
	"time"

	"builder/shared/clientui"
	"builder/shared/serverapi"
	"builder/shared/transcriptdiag"
)

const sessionActivityResubscribeDelay = 250 * time.Millisecond

type sessionActivitySubscriber func(context.Context) (serverapi.SessionActivitySubscription, error)

func startSessionActivityEvents(ctx context.Context, sub serverapi.SessionActivitySubscription, subscribe sessionActivitySubscriber, logDiag func(string)) (<-chan clientui.Event, func()) {
	out := make(chan clientui.Event, 64)
	if sub == nil || subscribe == nil {
		close(out)
		return out, func() {}
	}
	pollCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(out)
		current := sub
		for {
			evt, err := current.Next(pollCtx)
			if err != nil {
				if transcriptdiag.EnabledFromEnv(os.Getenv) && logDiag != nil {
					logDiag(transcriptdiag.FormatLine("transcript.diag.client.activity_gap", map[string]string{
						"path": "recovery",
						"err":  err.Error(),
					}))
				}
				_ = current.Close()
				if errors.Is(err, context.Canceled) {
					return
				}
				current, err = resubscribeSessionActivity(pollCtx, subscribe)
				if err != nil {
					return
				}
				select {
				case <-pollCtx.Done():
					_ = current.Close()
					return
				case out <- clientui.Event{Kind: clientui.EventConversationUpdated}:
					if transcriptdiag.EnabledFromEnv(os.Getenv) && logDiag != nil {
						logDiag(transcriptdiag.FormatLine("transcript.diag.client.synthetic_conversation_updated", map[string]string{
							"path":  "recovery",
							"kind":  string(clientui.EventConversationUpdated),
							"cause": "stream_gap",
						}))
					}
				}
				continue
			}
			if transcriptdiag.EnabledFromEnv(os.Getenv) && logDiag != nil {
				fields := map[string]string{
					"path":         "live_event",
					"kind":         string(evt.Kind),
					"step_id":      evt.StepID,
					"event_digest": transcriptdiag.EventDigest(evt),
				}
				fields = transcriptdiag.AddEntriesFields(fields, evt.TranscriptEntries)
				logDiag(transcriptdiag.FormatLine("transcript.diag.client.recv_activity", fields))
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
