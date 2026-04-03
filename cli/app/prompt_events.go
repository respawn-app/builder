package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	askquestion "builder/server/tools/askquestion"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/serverapi"
	"github.com/google/uuid"
)

const promptActivityResubscribeDelay = 250 * time.Millisecond

type promptActivitySubscriber func(context.Context) (serverapi.PromptActivitySubscription, error)
type pendingPromptSnapshotProvider func(context.Context) (map[string]struct{}, error)

func startPendingPromptEvents(ctx context.Context, sub serverapi.PromptActivitySubscription, subscribe promptActivitySubscriber, snapshot pendingPromptSnapshotProvider, control client.PromptControlClient) (<-chan askEvent, func()) {
	out := make(chan askEvent, 16)
	if sub == nil || subscribe == nil || control == nil {
		close(out)
		return out, func() {}
	}
	pollCtx, cancel := context.WithCancel(ctx)
	var pendingMu sync.Mutex
	pendingPromptIDs := make(map[string]struct{})
	var requeue func(clientui.PendingPromptEvent)
	requeue = func(item clientui.PendingPromptEvent) {
		select {
		case <-pollCtx.Done():
			return
		case out <- pendingPromptEvent(pollCtx, item, control, requeue):
		}
	}
	go func() {
		defer close(out)
		current := sub
		for {
			evt, err := current.Next(pollCtx)
			if err != nil {
				_ = current.Close()
				if errors.Is(err, context.Canceled) {
					return
				}
				for {
					nextSub, err := resubscribePromptActivity(pollCtx, subscribe)
					if err != nil {
						return
					}
					if err := reconcilePendingPromptSnapshot(pollCtx, snapshot, &pendingMu, pendingPromptIDs, out); err != nil {
						if errors.Is(err, context.Canceled) {
							_ = nextSub.Close()
							return
						}
						_ = nextSub.Close()
						continue
					}
					current = nextSub
					break
				}
				continue
			}
			if strings.TrimSpace(evt.PromptID) == "" {
				continue
			}
			switch evt.Type {
			case clientui.PendingPromptEventResolved:
				pendingMu.Lock()
				delete(pendingPromptIDs, evt.PromptID)
				pendingMu.Unlock()
				select {
				case <-pollCtx.Done():
					_ = current.Close()
					return
				case out <- resolvedPromptEvent(evt.PromptID):
				}
				continue
			case clientui.PendingPromptEventPending:
				pendingMu.Lock()
				if _, exists := pendingPromptIDs[evt.PromptID]; exists {
					pendingMu.Unlock()
					continue
				}
				pendingPromptIDs[evt.PromptID] = struct{}{}
				pendingMu.Unlock()
			default:
				continue
			}
			select {
			case <-pollCtx.Done():
				_ = current.Close()
				return
			case out <- pendingPromptEvent(pollCtx, evt, control, requeue):
			}
		}
	}()
	return out, cancel
}

func reconcilePendingPromptSnapshot(ctx context.Context, snapshot pendingPromptSnapshotProvider, pendingMu *sync.Mutex, pendingPromptIDs map[string]struct{}, out chan<- askEvent) error {
	if snapshot == nil {
		return nil
	}
	currentPending, err := snapshot(ctx)
	if err != nil {
		return err
	}
	resolved := make([]string, 0)
	pendingMu.Lock()
	for promptID := range pendingPromptIDs {
		if _, ok := currentPending[promptID]; ok {
			continue
		}
		delete(pendingPromptIDs, promptID)
		resolved = append(resolved, promptID)
	}
	pendingMu.Unlock()
	for _, promptID := range resolved {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- resolvedPromptEvent(promptID):
		}
	}
	return nil
}

func resubscribePromptActivity(ctx context.Context, subscribe promptActivitySubscriber) (serverapi.PromptActivitySubscription, error) {
	for {
		if !waitPromptActivityRetry(ctx) {
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

func waitPromptActivityRetry(ctx context.Context) bool {
	timer := time.NewTimer(promptActivityResubscribeDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func pendingPromptEvent(ctx context.Context, item clientui.PendingPromptEvent, control client.PromptControlClient, retry func(clientui.PendingPromptEvent)) askEvent {
	req := askquestion.Request{
		ID:                     item.PromptID,
		Question:               item.Question,
		Suggestions:            append([]string(nil), item.Suggestions...),
		RecommendedOptionIndex: item.RecommendedOptionIndex,
		Approval:               item.Approval,
	}
	if len(item.ApprovalOptions) > 0 {
		req.ApprovalOptions = make([]askquestion.ApprovalOption, 0, len(item.ApprovalOptions))
		for _, option := range item.ApprovalOptions {
			req.ApprovalOptions = append(req.ApprovalOptions, askquestion.ApprovalOption{Decision: askquestion.ApprovalDecision(option.Decision), Label: option.Label})
		}
	}
	reply := make(chan askReply, 1)
	promptCtx, cancelPrompt := context.WithCancel(ctx)
	go func() {
		var (
			result askReply
			ok     bool
		)
		select {
		case <-promptCtx.Done():
			return
		case result, ok = <-reply:
			if !ok {
				return
			}
		}
		if item.Approval {
			answerReq := serverapi.ApprovalAnswerRequest{ClientRequestID: uuid.NewString(), SessionID: item.SessionID, ApprovalID: item.PromptID}
			if result.err != nil {
				answerReq.ErrorMessage = result.err.Error()
			} else if result.response.Approval != nil {
				answerReq.Decision = clientui.ApprovalDecision(result.response.Approval.Decision)
				answerReq.Commentary = result.response.Approval.Commentary
			} else {
				answerReq.ErrorMessage = errors.New("approval response is required").Error()
			}
			if err := control.AnswerApproval(promptCtx, answerReq); err != nil {
				if retry != nil && shouldRetryPromptAnswerError(err) {
					retry(item)
				}
			}
			return
		}
		answerReq := serverapi.AskAnswerRequest{ClientRequestID: uuid.NewString(), SessionID: item.SessionID, AskID: item.PromptID}
		if result.err != nil {
			answerReq.ErrorMessage = result.err.Error()
		} else {
			answerReq.Answer = result.response.Answer
			answerReq.SelectedOptionNumber = result.response.SelectedOptionNumber
			answerReq.FreeformAnswer = result.response.FreeformAnswer
		}
		if err := control.AnswerAsk(promptCtx, answerReq); err != nil {
			if retry != nil && shouldRetryPromptAnswerError(err) {
				retry(item)
			}
		}
	}()
	return askEvent{req: req, reply: reply, cancel: cancelPrompt}
}

func resolvedPromptEvent(promptID string) askEvent {
	return askEvent{resolvedPromptID: strings.TrimSpace(promptID)}
}

func shouldRetryPromptAnswerError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, serverapi.ErrPromptNotFound) || errors.Is(err, serverapi.ErrPromptAlreadyResolved) || errors.Is(err, serverapi.ErrPromptUnsupported) {
		return false
	}
	return true
}
